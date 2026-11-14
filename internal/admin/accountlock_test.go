package admin_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/nats-io/nkeys"

	"github.com/bogworks/mash/internal/admin"
	"github.com/bogworks/mash/internal/store"
)

// Timing of the account-lock tests: a small TTL with a refresh well below it, so a refresh that works keeps the lock
// through several TTLs and one that does not loses it within one.
var (
	smallTTL     = 3 * time.Second
	smallSafety  = 500 * time.Millisecond
	smallRefresh = 300 * time.Millisecond
	shortWait    = 600 * time.Millisecond
)

func smallLocks(t *testing.T) {
	t.Helper()
	t.Cleanup(admin.SetLockTiming(smallTTL, 5*time.Second, fastPoll))
	t.Cleanup(admin.SetLockSafety(smallSafety))
	t.Cleanup(admin.SetLockRefresh(smallRefresh))
}

func adoptArgs(t *testing.T) (acct, sub, by string) {
	t.Helper()
	return pubKey(t, nkeys.CreateAccount), pubKey(t, nkeys.CreateUser), pubKey(t, nkeys.CreateUser)
}

// A held account lock keeps itself alive past several TTLs, and a second taker still waits.
func TestAccountLockRefreshKeepsALongHeldLockAlive(t *testing.T) {
	smallLocks(t)
	_, a, _ := setup(t)
	ctx := context.Background()
	lk, err := a.LockAccount(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer lk.Release()
	time.Sleep(2*smallTTL + smallRefresh) // more than one TTL, twice over
	if err := lk.Held(ctx); err != nil {
		t.Fatalf("a refreshed lock past its TTL is not held: %v", err)
	}
	other := a.WithAccountLockWait(shortWait)
	if _, err := other.LockAccount(ctx); !errors.Is(err, admin.ErrAccountBusy) {
		t.Fatalf("a second taker got the lock past the first one's TTL: %v", err)
	}
}

// Two takers serialise: the second gets the lock only after the first releases.
func TestAccountLockSerialisesTwoHolders(t *testing.T) {
	smallLocks(t)
	_, a, _ := setup(t)
	ctx := context.Background()
	first, err := a.LockAccount(ctx)
	if err != nil {
		t.Fatal(err)
	}
	got := make(chan error, 1)
	go func() {
		second, err := a.LockAccount(ctx)
		if err == nil {
			second.Release()
		}
		got <- err
	}()
	select {
	case err := <-got:
		t.Fatalf("the second holder did not wait: %v", err)
	case <-time.After(shortWait):
	}
	first.Release()
	select {
	case err := <-got:
		if err != nil {
			t.Fatalf("the second holder failed after the release: %v", err)
		}
	case <-time.After(longWait):
		t.Fatal("the second holder never got the lock")
	}
}

// A lock that is removed or taken while held is reported by Held, and a refresh does not bring it back.
func TestAccountLockLostIsReported(t *testing.T) {
	smallLocks(t)
	_, a, js := setup(t)
	ctx := context.Background()
	lk, err := a.LockAccount(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer lk.Release()
	kv, _ := js.KeyValue(ctx, store.LeaseBucket)
	if err := kv.Delete(ctx, admin.LockKey(admin.AccountLockName)); err != nil {
		t.Fatal(err)
	}
	if err := lk.Held(ctx); err == nil {
		t.Fatal("Held is nil for a lock whose key was removed")
	}
	time.Sleep(2 * smallRefresh) // a refresh tick must not resurrect it
	if err := lk.Held(ctx); err == nil {
		t.Fatal("Held is nil after a refresh of a lost lock")
	}
	// A taker now gets the lock, and the lost holder's release leaves the taker's lock alone.
	b := a.WithAccountLockWait(shortWait)
	other, err := b.LockAccount(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer other.Release()
	lk.Release()
	if err := other.Held(ctx); err != nil {
		t.Fatalf("the lost holder's release removed the new holder's lock: %v", err)
	}
}

// The lock is gone after Release: taking it again succeeds at once.
func TestAccountLockReleaseFreesIt(t *testing.T) {
	smallLocks(t)
	_, a, _ := setup(t)
	ctx := context.Background()
	lk, err := a.LockAccount(ctx)
	if err != nil {
		t.Fatal(err)
	}
	lk.Release()
	b := a.WithAccountLockWait(fastPoll)
	again, err := b.LockAccount(ctx)
	if err != nil {
		t.Fatalf("lock still held after Release: %v", err)
	}
	again.Release()
}

// Adopt waits for a holder, then writes: it was started second, so it lands second.
func TestAdoptWaitsForTheAccountLock(t *testing.T) {
	smallLocks(t)
	_, a, _ := setup(t)
	ctx := context.Background()
	acct, sub, by := adoptArgs(t)
	lk, err := a.LockAccount(ctx)
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() {
		_, _, err := a.Adopt(ctx, acct, sub, by, nil)
		done <- err
	}()
	select {
	case err := <-done:
		t.Fatalf("Adopt did not wait for the lock: %v", err)
	case <-time.After(shortWait):
	}
	if _, found, err := a.Adoption(ctx); err != nil || found {
		t.Fatalf("the marker was written while the lock was held: %v %v", found, err)
	}
	lk.Release()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Adopt after the release: %v", err)
		}
	case <-time.After(longWait):
		t.Fatal("Adopt never finished")
	}
	if _, found, _ := a.Adoption(ctx); !found {
		t.Fatal("no marker after Adopt")
	}
	// Adopt released the lock behind it.
	again, err := a.WithAccountLockWait(fastPoll).LockAccount(ctx)
	if err != nil {
		t.Fatalf("Adopt left the lock held: %v", err)
	}
	again.Release()
}

// Adopt that cannot take the lock within its wait refuses and writes nothing.
func TestAdoptRefusesWhenTheLockStaysHeld(t *testing.T) {
	smallLocks(t)
	_, a, _ := setup(t)
	ctx := context.Background()
	acct, sub, by := adoptArgs(t)
	lk, err := a.LockAccount(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer lk.Release()
	_, _, err = a.WithAccountLockWait(shortWait).Adopt(ctx, acct, sub, by, nil)
	if !errors.Is(err, admin.ErrAccountBusy) {
		t.Fatalf("Adopt with the lock held: %v", err)
	}
	if _, found, err := a.Adoption(ctx); err != nil || found {
		t.Fatalf("a refused Adopt wrote the marker: %v %v", found, err)
	}
}
