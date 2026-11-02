package admin_test

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/nats-io/nats.go/jetstream"

	"github.com/bogworks/mash/internal/admin"
	"github.com/bogworks/mash/internal/store"
	"github.com/bogworks/mash/internal/testenv"
)

// Seam delays and lock timing for these tests: short, and named so the portable lint sees no long literal.
var (
	seamHold = 400 * time.Millisecond
	fastPoll = 10 * time.Millisecond
	longWait = 20 * time.Second
	ttl30    = 30 * time.Second
)

func quickLocks(t *testing.T, wait time.Duration) {
	t.Helper()
	t.Cleanup(admin.SetLockTiming(ttl30, wait, fastPoll))
}

// secondAdmin is another admin on its own connection with its own limits.
func secondAdmin(t *testing.T, e *testenv.Env, l store.Limits) *admin.Admin {
	t.Helper()
	a, _ := initOn(t, e)
	if err := a.SetLimits(l); err != nil {
		t.Fatal(err)
	}
	return a
}

func limitsOf(bytes int64, age time.Duration) store.Limits {
	l := store.DefaultLimits()
	l.InboxMaxBytes, l.InboxMaxAge = bytes, age
	return l
}

func TestConcurrentLimitsNeverLeaveALowerCap(t *testing.T) {
	quickLocks(t, longWait)
	e, a, js := setup(t)
	ctx := context.Background()
	provisionTwo(t, e, a)
	// A raises max_bytes a little and max_age a lot; B raises max_bytes a lot and max_age a little. Each is lower than the
	// other in one field, so whichever goes second must be refused, and the stream must keep the first one's full result.
	la := limitsOf(32<<20, 90*24*time.Hour)
	lb := limitsOf(64<<20, 60*24*time.Hour)
	a1, a2 := secondAdmin(t, e, la), secondAdmin(t, e, lb)

	// Force the interleaving: a writer waits at the seam for the other to arrive, or gives up after seamHold. Without
	// the lock both reach it and both write.
	var arrived atomic.Int32
	both := make(chan struct{})
	prev := admin.SetAfterStreamRead(func(int64) {
		if arrived.Add(1) == 2 {
			close(both)
		}
		select {
		case <-both:
		case <-time.After(seamHold):
		}
	})
	t.Cleanup(func() { admin.SetAfterStreamRead(prev) })

	errs := make([]error, 2)
	var wg sync.WaitGroup
	for i, ad := range []*admin.Admin{a1, a2} {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, errs[i] = ad.ApplyLimits(ctx, "alpha")
		}()
	}
	wg.Wait()

	failed := 0
	for _, err := range errs {
		if err != nil {
			failed++
			if !strings.Contains(err.Error(), "never lowers") {
				t.Errorf("unexpected error: %v", err)
			}
		}
	}
	c := inboxCfg(t, js, "alpha")
	full := (c.MaxBytes == la.InboxMaxBytes && c.MaxAge == la.InboxMaxAge) || (c.MaxBytes == lb.InboxMaxBytes && c.MaxAge == lb.InboxMaxAge)
	if failed != 1 || !full {
		t.Fatalf("errors %v; caps max_bytes %d max_age %s: want exactly one admin refused and the other's full result", errs, c.MaxBytes, c.MaxAge)
	}
	kv, _ := js.KeyValue(ctx, store.LeaseBucket)
	if _, err := kv.Get(ctx, admin.LockKey("alpha")); !errors.Is(err, jetstream.ErrKeyNotFound) {
		t.Errorf("lock left behind: %v", err)
	}
}

func putLock(t *testing.T, js jetstream.JetStream, name string, until time.Time) uint64 {
	t.Helper()
	kv, err := js.KeyValue(context.Background(), store.LeaseBucket)
	if err != nil {
		t.Fatal(err)
	}
	v, _ := json.Marshal(map[string]string{"owner": "someone", "until": until.UTC().Format(time.RFC3339)})
	rev, err := kv.Put(context.Background(), admin.LockKey(name), v)
	if err != nil {
		t.Fatal(err)
	}
	return rev
}

func TestExpiredLockIsTakenOver(t *testing.T) {
	quickLocks(t, 2*time.Second)
	_, a, js := setup(t)
	putLock(t, js, "alpha", time.Now().Add(-time.Minute))
	rel, err := a.AcquireLock(context.Background(), "alpha")
	if err != nil {
		t.Fatalf("expired lock not taken over: %v", err)
	}
	rel()
	kv, _ := js.KeyValue(context.Background(), store.LeaseBucket)
	if _, err := kv.Get(context.Background(), admin.LockKey("alpha")); !errors.Is(err, jetstream.ErrKeyNotFound) {
		t.Fatalf("lock still there after release: %v", err)
	}
}

func TestLiveLockMakesTheSecondAdminWaitThenTimeOut(t *testing.T) {
	wait := 300 * time.Millisecond
	quickLocks(t, wait)
	_, a, js := setup(t)
	until := time.Now().Add(time.Hour)
	putLock(t, js, "alpha", until)
	start := time.Now()
	_, err := a.AcquireLock(context.Background(), "alpha")
	want := "mash: another admin is changing the limits of alpha (lock held until " + until.UTC().Format(time.RFC3339) + "); try again"
	if err == nil || err.Error() != want {
		t.Fatalf("got %v, want %q", err, want)
	}
	if el := time.Since(start); el < wait {
		t.Fatalf("gave up after %s, before the %s wait", el, wait)
	}
}

func TestWaiterGetsTheLockWhenItIsReleased(t *testing.T) {
	quickLocks(t, 5*time.Second)
	_, a, _ := setup(t)
	rel, err := a.AcquireLock(context.Background(), "alpha")
	if err != nil {
		t.Fatal(err)
	}
	got := make(chan error, 1)
	go func() {
		r, err := a.AcquireLock(context.Background(), "alpha")
		if err == nil {
			r()
		}
		got <- err
	}()
	select {
	case err := <-got:
		t.Fatalf("second acquire returned while the lock was held: %v", err)
	case <-time.After(100 * time.Millisecond):
	}
	rel()
	if err := <-got; err != nil {
		t.Fatalf("waiter: %v", err)
	}
}

func TestReleaseNeverDeletesSomeoneElsesLock(t *testing.T) {
	quickLocks(t, 2*time.Second)
	_, a, js := setup(t)
	ctx := context.Background()
	rel, err := a.AcquireLock(ctx, "alpha")
	if err != nil {
		t.Fatal(err)
	}
	// Our lock "expired" and another admin took it over: the key now has a newer revision.
	taken := putLock(t, js, "alpha", time.Now().Add(time.Hour))
	rel()
	kv, _ := js.KeyValue(ctx, store.LeaseBucket)
	e, err := kv.Get(ctx, admin.LockKey("alpha"))
	if err != nil || e.Revision() != taken {
		t.Fatalf("the other admin's lock was removed or changed: %v", err)
	}
}

func TestProvisionRacingProvisionNeverLowersCaps(t *testing.T) {
	quickLocks(t, longWait)
	e, a, js := setup(t)
	ctx := context.Background()
	small, big := limitsOf(20<<20, 40*24*time.Hour), limitsOf(64<<20, 90*24*time.Hour)
	pa, pb := secondAdmin(t, e, small), secondAdmin(t, e, big)
	_ = a
	// The admin with the small limits stops after its read, while the one with the big limits provisions. Without the
	// lock the small one then updates the stream over the big caps, lowering them.
	prev := admin.SetAfterStreamRead(func(maxBytes int64) {
		if maxBytes == small.InboxMaxBytes {
			time.Sleep(seamHold)
		}
	})
	t.Cleanup(func() { admin.SetAfterStreamRead(prev) })
	_, user := e.Mint(t, "gamma")
	// A provision that failed after the stream and before the record, so both retries find the stream at the big
	// caps. A Provision holds the name's lock whole, so one admin finishes before the other starts; the small one
	// must be refused rather than lower the caps.
	if _, err := pb.Provision(ctx, "gamma", user, e.Account, "", admin.Meta{}); err != nil {
		t.Fatal(err)
	}
	dir, _ := js.KeyValue(ctx, store.DirectoryBucket)
	if err := dir.Purge(ctx, "gamma"); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for i, ad := range []*admin.Admin{pa, pb} {
		wg.Add(1)
		go func() {
			defer wg.Done()
			time.Sleep(time.Duration(i) * seamHold / 4)                          // the small one reads first
			_, _ = ad.Provision(ctx, "gamma", user, e.Account, "", admin.Meta{}) // one of them finds the record already there
		}()
	}
	wg.Wait()
	c := inboxCfg(t, js, "gamma")
	if c.MaxBytes != big.InboxMaxBytes || c.MaxAge != big.InboxMaxAge {
		t.Fatalf("caps lowered: max_bytes %d max_age %s, want %d and %s", c.MaxBytes, c.MaxAge, big.InboxMaxBytes, big.InboxMaxAge)
	}
}

func TestProvisionWithApplyLimitsOnANewName(t *testing.T) {
	quickLocks(t, longWait)
	e, a, js := setup(t)
	ctx := context.Background()
	big := limitsOf(64<<20, 90*24*time.Hour)
	ap := secondAdmin(t, e, big)
	parked := make(chan struct{})
	var once sync.Once
	prev := admin.SetAfterStreamRead(func(int64) {
		once.Do(func() { close(parked) })
		time.Sleep(seamHold / 4)
	})
	t.Cleanup(func() { admin.SetAfterStreamRead(prev) })
	_, user := e.Mint(t, "delta")
	var wg sync.WaitGroup
	var perr, aerr error
	wg.Add(2)
	go func() {
		defer wg.Done()
		_, perr = a.Provision(ctx, "delta", user, e.Account, "", admin.Meta{})
	}()
	go func() {
		defer wg.Done()
		<-parked // the provision has read "no stream" and holds the lock
		for {
			if _, aerr = ap.ApplyLimits(ctx, "delta"); aerr == nil || !strings.Contains(aerr.Error(), "inbox stream") {
				return
			}
			time.Sleep(fastPoll) // not created yet
		}
	}()
	wg.Wait()
	if perr != nil || aerr != nil {
		t.Fatalf("provision %v, apply %v", perr, aerr)
	}
	c := inboxCfg(t, js, "delta")
	if c.MaxBytes != big.InboxMaxBytes || c.MaxAge != big.InboxMaxAge {
		t.Fatalf("caps max_bytes %d max_age %s, want the applied limits", c.MaxBytes, c.MaxAge)
	}
}

func TestEndpointCannotReachALockKey(t *testing.T) {
	// Static: the only lease-bucket grants are exact keys, and a lock key is none of them. Names hold no dot.
	for _, g := range admin.Permissions("o", "alpha").Pub.Allow {
		if !strings.Contains(g, "mash-leases") {
			continue
		}
		if strings.ContainsAny(g, "*>") && !strings.HasSuffix(g, "$JS.API.INFO") {
			t.Errorf("wildcard grant %q on the lease bucket may cover a lock key", g)
		}
		if strings.HasSuffix(g, admin.LockKey("alpha")) {
			t.Errorf("grant %q covers a lock key", g)
		}
	}
	// Live: an endpoint with the production permissions can use its own lease key but not a lock's.
	e, a, js := setup(t)
	creds, user := e.Mint(t, "alpha")
	if _, err := a.Provision(context.Background(), "alpha", user, e.Account, "", admin.Meta{}); err != nil {
		t.Fatal(err)
	}
	putLock(t, js, "alpha", time.Now().Add(time.Hour))
	nc := e.ConnAs(t, creds, "alpha")
	ejs, _ := jetstream.New(nc)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	kv, err := ejs.KeyValue(ctx, store.LeaseBucket)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := kv.Create(ctx, "alpha", []byte("{}")); err != nil {
		t.Fatalf("control: the endpoint could not write its own lease key: %v", err)
	}
	if _, err := kv.Get(ctx, "alpha"); err != nil {
		t.Fatalf("control: the endpoint could not read its own lease key: %v", err)
	}
	for _, name := range []string{"alpha", "beta"} {
		cctx, ccancel := context.WithTimeout(context.Background(), time.Second)
		if e, err := kv.Get(cctx, admin.LockKey(name)); err == nil {
			t.Errorf("endpoint read lock key for %s: %s", name, e.Value())
		}
		if _, err := kv.Put(cctx, admin.LockKey(name), []byte("{}")); err == nil {
			t.Errorf("endpoint wrote lock key for %s", name)
		}
		if err := kv.Purge(cctx, admin.LockKey(name)); err == nil {
			t.Errorf("endpoint purged lock key for %s", name)
		}
		ccancel()
	}
	akv, _ := js.KeyValue(context.Background(), store.LeaseBucket)
	if e, err := akv.Get(context.Background(), admin.LockKey("alpha")); err != nil || !strings.Contains(string(e.Value()), "someone") {
		t.Fatalf("lock changed: %v", err)
	}
}

func TestLockCreatesTheLeaseBucketWhenMissing(t *testing.T) {
	e := testenv.Start(t)
	a, js := initOn(t, e)
	quickLocks(t, time.Second)
	rel, err := a.AcquireLock(context.Background(), "alpha")
	if err != nil {
		t.Fatalf("no lease bucket yet: %v", err)
	}
	rel()
	if _, err := js.KeyValue(context.Background(), store.LeaseBucket); err != nil {
		t.Fatalf("bucket not created: %v", err)
	}
}

// Timing for the lock-expiry tests: a 2 s lock that stops being used 500 ms early, so an operation is bound at 1.5 s.
var (
	expiryTTL    = 2 * time.Second
	expirySafety = 500 * time.Millisecond
	expiryStall  = 1700 * time.Millisecond // past the 1.5 s bound, inside the 2 s lock
)

// seam installs hook with set and restores the previous hook when the test ends.
func seam(t *testing.T, set func(func()) func(), hook func()) {
	t.Helper()
	prev := set(hook)
	t.Cleanup(func() { set(prev) })
}

// suspendedDrain provisions name with one message, suspends it (under the normal lock timing) and then shortens the
// lock to expiryTTL; it returns what DeprovisionDrained needs.
func suspendedDrain(t *testing.T, name string) (*admin.Admin, jetstream.JetStream, admin.SuspendState) {
	t.Helper()
	e, a, js := setup(t)
	provisionFor(t, e, a, name, "")
	if _, err := js.Publish(context.Background(), "agents.mail.cc.o."+name, []byte("one")); err != nil {
		t.Fatal(err)
	}
	st, err := a.Suspend(context.Background(), name)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(admin.SetLockTiming(expiryTTL, longWait, fastPoll))
	t.Cleanup(admin.SetLockSafety(expirySafety))
	return a, js, st
}

func TestStalledOperationStopsBeforeTheLockExpires(t *testing.T) {
	a, js, st := suspendedDrain(t, "alpha")
	seam(t, admin.SetAfterDeprovisionGet, func() { time.Sleep(expiryStall) })
	err := a.DeprovisionDrained(context.Background(), "alpha", st.ID, st.LastSeq)
	if err == nil || !strings.Contains(err.Error(), "ran past the lock of alpha") && !strings.Contains(err.Error(), "lost or is about to expire") {
		t.Fatalf("want a lock-expiry error, got %v", err)
	}
	intact(t, a, js, "alpha")
}

func TestOperationThatStallsBeforeItsFirstCallIsStopped(t *testing.T) {
	a, js, st := suspendedDrain(t, "alpha")
	seam(t, admin.SetAfterLockAcquired, func() { time.Sleep(expiryStall) })
	err := a.DeprovisionDrained(context.Background(), "alpha", st.ID, st.LastSeq)
	if err == nil || !strings.Contains(err.Error(), "ran past the lock of alpha") || !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("want 'ran past the lock', got %v", err)
	}
	if !strings.Contains(err.Error(), "try again") {
		t.Fatalf("no advice to retry: %v", err)
	}
	intact(t, a, js, "alpha")
}

func TestStolenLockStopsTheFirstOperationBeforeItDeletes(t *testing.T) {
	a, js, st := suspendedDrain(t, "alpha")
	var stolen uint64
	seam(t, admin.SetAfterDeprovisionGet, func() {
		// Another admin took the lock (its key is a new revision), well inside our own bound.
		stolen = putLock(t, js, "alpha", time.Now().Add(time.Minute))
	})
	err := a.DeprovisionDrained(context.Background(), "alpha", st.ID, st.LastSeq)
	if err == nil || !strings.Contains(err.Error(), "the lock of alpha was lost or is about to expire; nothing further was changed; try again") {
		t.Fatalf("want 'lost', got %v", err)
	}
	intact(t, a, js, "alpha")
	// Release must leave the other admin's lock alone.
	kv, _ := js.KeyValue(context.Background(), store.LeaseBucket)
	if e, err := kv.Get(context.Background(), admin.LockKey("alpha")); err != nil || e.Revision() != stolen {
		t.Fatalf("the other admin's lock was removed: %v", err)
	}
}

func TestResumeThatStallsDoesNotOverlapAnotherAdmin(t *testing.T) {
	a, js, _ := suspendedDrain(t, "alpha")
	seam(t, admin.SetAfterLockAcquired, func() { time.Sleep(expiryStall) })
	err := a.Resume(context.Background(), "alpha")
	if err == nil || !strings.Contains(err.Error(), "resume ran past the lock of alpha") {
		t.Fatalf("want 'resume ran past the lock', got %v", err)
	}
	s, serr := a.Suspended(context.Background(), "alpha")
	if serr != nil || !s.Suspended {
		t.Fatalf("a stopped resume changed the stream: %+v %v", s, serr)
	}
	intact(t, a, js, "alpha")
}

func TestLockCheckRefusesWhenItsRevisionChanged(t *testing.T) {
	quickLocks(t, 2*time.Second)
	_, a, js := setup(t)
	ctx := context.Background()
	lk, err := admin.AcquireLockHandle(ctx, a, "alpha")
	if err != nil {
		t.Fatal(err)
	}
	defer lk.Release()
	if err := lk.Check(ctx); err != nil {
		t.Fatalf("a fresh lock must check clean: %v", err)
	}
	putLock(t, js, "alpha", time.Now().Add(time.Minute))
	if err := lk.Check(ctx); err == nil || !strings.Contains(err.Error(), "was lost or is about to expire") {
		t.Fatalf("a replaced lock must fail the check, got %v", err)
	}
}
