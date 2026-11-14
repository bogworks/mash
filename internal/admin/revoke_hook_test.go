package admin_test

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/nats-io/nats.go/jetstream"

	"github.com/bogworks/mash/internal/admin"
	"github.com/bogworks/mash/internal/apply"
	"github.com/bogworks/mash/internal/store"
)

// hookRec is a RevokeHook that records its calls and what the world looked like at the moment of each.
type hookRec struct {
	mu       sync.Mutex
	users    []string
	locked   []bool // the name's lock key existed
	hadInbox []bool // the inbox stream existed
	hadRec   []bool // the directory record existed
	err      error
}

func (h *hookRec) hook(t *testing.T, js jetstream.JetStream, name string) admin.RevokeHook {
	return func(ctx context.Context, user string) error {
		h.mu.Lock()
		defer h.mu.Unlock()
		h.users = append(h.users, user)
		lease, err := js.KeyValue(ctx, store.LeaseBucket)
		if err != nil {
			t.Error(err)
			return err
		}
		_, lerr := lease.Get(ctx, admin.LockKey(name))
		h.locked = append(h.locked, lerr == nil)
		_, serr := js.Stream(ctx, "inbox-"+name)
		h.hadInbox = append(h.hadInbox, serr == nil)
		dir, _ := js.KeyValue(ctx, store.DirectoryBucket)
		_, rerr := dir.Get(ctx, name)
		h.hadRec = append(h.hadRec, rerr == nil)
		return h.err
	}
}

func (h *hookRec) calledOnceWith(t *testing.T, user string) {
	t.Helper()
	if len(h.users) != 1 || h.users[0] != user || !h.locked[0] || !h.hadInbox[0] || !h.hadRec[0] {
		t.Fatalf("hook calls %v locked %v inbox %v record %v, want one call with %s under the lock before any removal", h.users, h.locked, h.hadInbox, h.hadRec, user)
	}
}

func recordUserOf(t *testing.T, js jetstream.JetStream, name string) string {
	t.Helper()
	kv, _ := js.KeyValue(context.Background(), store.DirectoryBucket)
	rec, err := store.GetRecord(context.Background(), kv, name)
	if err != nil {
		t.Fatal(err)
	}
	return rec.User
}

func noFence(t *testing.T, js jetstream.JetStream, name string) {
	t.Helper()
	lease, _ := js.KeyValue(context.Background(), store.LeaseBucket)
	if _, err := lease.Get(context.Background(), name); !errors.Is(err, jetstream.ErrKeyNotFound) {
		t.Fatalf("lease of %s was written: %v", name, err)
	}
}

func gone(t *testing.T, js jetstream.JetStream, name string) {
	t.Helper()
	if _, err := js.Stream(context.Background(), "inbox-"+name); err == nil {
		t.Fatal("stream left")
	}
	kv, _ := js.KeyValue(context.Background(), store.DirectoryBucket)
	if _, err := kv.Get(context.Background(), name); err == nil {
		t.Fatal("record left")
	}
}

func TestWithRevokerLeavesTheOriginalAlone(t *testing.T) {
	e, a, js := setup(t)
	provisionFor(t, e, a, "w")
	h := &hookRec{}
	_ = a.WithRevoker(h.hook(t, js, "w"))
	if err := a.Deprovision(context.Background(), "w"); err != nil || len(h.users) != 0 {
		t.Fatalf("%v %v", err, h.users)
	}
}

func TestDeprovisionRevokesRecordUserUnderLock(t *testing.T) {
	e, a, js := setup(t)
	provisionFor(t, e, a, "w")
	user := recordUserOf(t, js, "w")
	h := &hookRec{}
	if err := a.WithRevoker(h.hook(t, js, "w")).Deprovision(context.Background(), "w"); err != nil {
		t.Fatal(err)
	}
	h.calledOnceWith(t, user)
	gone(t, js, "w")
}

func TestDeprovisionHookErrorRemovesNothing(t *testing.T) {
	e, a, js := setup(t)
	provisionFor(t, e, a, "w")
	h := &hookRec{err: errors.New("resolver down")}
	err := a.WithRevoker(h.hook(t, js, "w")).Deprovision(context.Background(), "w")
	if err == nil || !errors.Is(err, h.err) || !admin.IsRefusal(err) {
		t.Fatalf("%v", err)
	}
	intact(t, a, js, "w")
	noFence(t, js, "w")
}

func TestDeprovisionWithoutRecordDoesNotCallHook(t *testing.T) {
	e, a, js := setup(t)
	provisionFor(t, e, a, "w")
	kv, _ := js.KeyValue(context.Background(), store.DirectoryBucket)
	if err := kv.Purge(context.Background(), "w"); err != nil {
		t.Fatal(err)
	}
	h := &hookRec{err: errors.New("must not be called")}
	if err := a.WithRevoker(h.hook(t, js, "w")).Deprovision(context.Background(), "w"); err != nil {
		t.Fatal(err)
	}
	if len(h.users) != 0 {
		t.Fatalf("hook called %v", h.users)
	}
	gone(t, js, "w")
}

func TestDeprovisionIfUserRevokesOnceAfterGuard(t *testing.T) {
	e, a, js := setup(t)
	provisionFor(t, e, a, "w")
	user := recordUserOf(t, js, "w")
	h := &hookRec{}
	if err := a.WithRevoker(h.hook(t, js, "w")).DeprovisionIfUser(context.Background(), "w", user); err != nil {
		t.Fatal(err)
	}
	h.calledOnceWith(t, user)
	gone(t, js, "w")
}

func TestDeprovisionIfUserHookErrorRemovesNothing(t *testing.T) {
	e, a, js := setup(t)
	provisionFor(t, e, a, "w")
	user := recordUserOf(t, js, "w")
	h := &hookRec{err: errors.New("resolver down")}
	err := a.WithRevoker(h.hook(t, js, "w")).DeprovisionIfUser(context.Background(), "w", user)
	if err == nil || !errors.Is(err, h.err) || !admin.IsRefusal(err) {
		t.Fatalf("%v", err)
	}
	intact(t, a, js, "w")
	noFence(t, js, "w")
}

func TestDeprovisionIfUserRefusalsNeverCallHook(t *testing.T) {
	e, a, js := setup(t)
	provisionFor(t, e, a, "w")
	_, other := e.Mint(t, "other")
	h := &hookRec{}
	ra := a.WithRevoker(h.hook(t, js, "w"))
	if err := ra.DeprovisionIfUser(context.Background(), "w", other); !errors.Is(err, admin.ErrRecordOtherUser) {
		t.Fatalf("other user: %v", err)
	}
	kv, _ := js.KeyValue(context.Background(), store.DirectoryBucket)
	user := recordUserOf(t, js, "w")
	if err := kv.Purge(context.Background(), "w"); err != nil {
		t.Fatal(err)
	}
	if err := ra.DeprovisionIfUser(context.Background(), "w", user); !errors.Is(err, admin.ErrNoRecord) {
		t.Fatalf("no record: %v", err)
	}
	if len(h.users) != 0 {
		t.Fatalf("hook called %v", h.users)
	}
	if _, err := js.Stream(context.Background(), "inbox-w"); err != nil {
		t.Fatal(err)
	}
}

func TestDeprovisionIfUserReplacedRecordNeverRevokesTheOtherKey(t *testing.T) {
	e, a, js := setup(t)
	provisionFor(t, e, a, "w")
	user := recordUserOf(t, js, "w")
	_, other := e.Mint(t, "other")
	done := false
	seam(t, admin.SetAfterDeprovisionGet, func() {
		if !done {
			done = true
			putUser(t, js, "w", other)
		}
	})
	h := &hookRec{}
	err := a.WithRevoker(h.hook(t, js, "w")).DeprovisionIfUser(context.Background(), "w", user)
	// The hook revoked the key the caller named (the record's at the read); the purge then found the record replaced,
	// so the call is a PARTIAL for that key, never a refusal that claims nothing changed.
	wantPartial(t, err, user)
	for _, u := range h.users {
		if u == other {
			t.Fatal("the replacing record's key was revoked")
		}
	}
	intact(t, a, js, "w")
	if recordUserOf(t, js, "w") != other {
		t.Fatal("the replaced record was removed")
	}
}

func TestDeprovisionDrainedRevokesAfterEveryGuard(t *testing.T) {
	e, a, js := setup(t)
	provisionFor(t, e, a, "w")
	user := recordUserOf(t, js, "w")
	st, err := a.Suspend(context.Background(), "w")
	if err != nil {
		t.Fatal(err)
	}
	h := &hookRec{}
	ra := a.WithRevoker(h.hook(t, js, "w"))
	ctx := context.Background()
	if err := ra.DeprovisionDrained(ctx, "w", "wrong-id", st.LastSeq); err == nil {
		t.Fatal("wrong id accepted")
	}
	if err := ra.DeprovisionDrained(ctx, "w", st.ID, st.LastSeq+1); err == nil {
		t.Fatal("wrong seq accepted")
	}
	_, other := e.Mint(t, "other")
	putUser(t, js, "w", other)
	if err := ra.DeprovisionDrained(ctx, "w", st.ID, st.LastSeq); err == nil {
		t.Fatal("replaced record accepted")
	}
	if len(h.users) != 0 {
		t.Fatalf("hook called by a refused removal: %v", h.users)
	}
	putUser(t, js, "w", user)
	h.err = errors.New("resolver down")
	if err := ra.DeprovisionDrained(ctx, "w", st.ID, st.LastSeq); !errors.Is(err, h.err) {
		t.Fatalf("%v", err)
	}
	intact(t, a, js, "w")
	parked(t, a, "w", st.ID)
	noFence(t, js, "w")
	h.err, h.users, h.locked, h.hadInbox, h.hadRec = nil, nil, nil, nil, nil
	if err := ra.DeprovisionDrained(ctx, "w", st.ID, st.LastSeq); err != nil {
		t.Fatal(err)
	}
	h.calledOnceWith(t, user)
	gone(t, js, "w")
}

// corruptRecord overwrites name's directory record with bytes ParseRecord rejects.
func corruptRecord(t *testing.T, js jetstream.JetStream, name string) {
	t.Helper()
	kv, _ := js.KeyValue(context.Background(), store.DirectoryBucket)
	if _, err := kv.Put(context.Background(), name, []byte("not json {")); err != nil {
		t.Fatal(err)
	}
	if _, err := store.GetRecord(context.Background(), kv, name); err == nil {
		t.Fatal("the corrupted record still parses")
	}
}

func TestDeprovisionUnparseableRecordWithRevokerFailsClosed(t *testing.T) {
	e, a, js := setup(t)
	provisionFor(t, e, a, "w")
	corruptRecord(t, js, "w")
	h := &hookRec{}
	err := a.WithRevoker(h.hook(t, js, "w")).Deprovision(context.Background(), "w")
	if err == nil || !admin.IsRefusal(err) || !strings.Contains(err.Error(), "its record cannot be read, so its user key is unknown; --no-revoke removes it without revoking") {
		t.Fatalf("%v", err)
	}
	if len(h.users) != 0 {
		t.Fatalf("hook called %v", h.users)
	}
	if _, err := js.Stream(context.Background(), "inbox-w"); err != nil {
		t.Fatalf("inbox removed: %v", err)
	}
	kv, _ := js.KeyValue(context.Background(), store.DirectoryBucket)
	if _, err := kv.Get(context.Background(), "w"); err != nil {
		t.Fatalf("record removed: %v", err)
	}
	noFence(t, js, "w")
}

func TestDeprovisionUnparseableRecordWithoutRevokerRemoves(t *testing.T) {
	e, a, js := setup(t)
	provisionFor(t, e, a, "w")
	corruptRecord(t, js, "w")
	if err := a.Deprovision(context.Background(), "w"); err != nil {
		t.Fatal(err)
	}
	gone(t, js, "w")
}

func TestHookWrappingErrNotConfirmedIsARefusalThatSaysItMayBeInForce(t *testing.T) {
	e, a, js := setup(t)
	provisionFor(t, e, a, "w")
	user := recordUserOf(t, js, "w")
	h := &hookRec{err: fmt.Errorf("servers: %w", apply.ErrNotConfirmed)}
	err := a.WithRevoker(h.hook(t, js, "w")).DeprovisionIfUser(context.Background(), "w", user)
	if err == nil || !admin.IsRefusal(err) || admin.IsPartial(err) || !errors.Is(err, apply.ErrNotConfirmed) {
		t.Fatalf("%v", err)
	}
	want := "could not confirm the revocation of " + user[:8] + ": "
	if !strings.Contains(err.Error(), want) || !strings.Contains(err.Error(), "it may already be in force on some servers; nothing was removed; run the same command again (revoking again is safe)") {
		t.Fatalf("%v", err)
	}
	intact(t, a, js, "w")
	noFence(t, js, "w")
}

// wantPartial checks err is the PARTIAL non-refusal for name's user.
func wantPartial(t *testing.T, err error, user string) {
	t.Helper()
	if err == nil || admin.IsRefusal(err) || !admin.IsPartial(err) {
		t.Fatalf("want a non-refusal PARTIAL, got %v", err)
	}
	for _, w := range []string{"mash: PARTIAL: w's user key " + user[:8] + " was revoked in the account JWT", "the removal did not complete: ", "run the same command again (revoking again is safe)"} {
		if !strings.Contains(err.Error(), w) {
			t.Fatalf("missing %q in %v", w, err)
		}
	}
	if strings.Contains(err.Error(), "nothing was") {
		t.Fatalf("contradicts itself: %v", err)
	}
}

func TestRecordChangedAfterTheHookIsPartialNotARefusal(t *testing.T) {
	e, a, js := setup(t)
	provisionFor(t, e, a, "w")
	user := recordUserOf(t, js, "w")
	_, other := e.Mint(t, "other")
	h := &hookRec{}
	inner := h.hook(t, js, "w")
	hook := func(ctx context.Context, u string) error {
		if err := inner(ctx, u); err != nil {
			return err
		}
		putUser(t, js, "w", other) // another admin replaces the record while the hook ran
		return nil
	}
	err := a.WithRevoker(hook).DeprovisionIfUser(context.Background(), "w", user)
	wantPartial(t, err, user)
	intact(t, a, js, "w")
	noFence(t, js, "w")
	if recordUserOf(t, js, "w") != other {
		t.Fatal("the replaced record was removed")
	}
}

func TestDrainedRecordChangedAfterTheHookIsPartial(t *testing.T) {
	e, a, js := setup(t)
	provisionFor(t, e, a, "w")
	user := recordUserOf(t, js, "w")
	st, err := a.Suspend(context.Background(), "w")
	if err != nil {
		t.Fatal(err)
	}
	h := &hookRec{}
	inner := h.hook(t, js, "w")
	hook := func(ctx context.Context, u string) error {
		if err := inner(ctx, u); err != nil {
			return err
		}
		putUser(t, js, "w", user) // a new revision of the same record
		return nil
	}
	err = a.WithRevoker(hook).DeprovisionDrained(context.Background(), "w", st.ID, st.LastSeq)
	wantPartial(t, err, user)
	intact(t, a, js, "w")
	noFence(t, js, "w")
}

func TestLockLostAfterTheHookIsPartial(t *testing.T) {
	e, a, js := setup(t)
	provisionFor(t, e, a, "w")
	user := recordUserOf(t, js, "w")
	hook := func(ctx context.Context, u string) error {
		putLock(t, js, "w", time.Now().Add(time.Minute)) // another admin took the lock
		return nil
	}
	err := a.WithRevoker(hook).DeprovisionIfUser(context.Background(), "w", user)
	wantPartial(t, err, user)
	intact(t, a, js, "w")
	noFence(t, js, "w")
}

func TestLockExpiringDuringTheHookIsPartial(t *testing.T) {
	e, a, js := setup(t)
	provisionFor(t, e, a, "w")
	user := recordUserOf(t, js, "w")
	st, err := a.Suspend(context.Background(), "w")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(admin.SetLockTiming(expiryTTL, longWait, fastPoll))
	t.Cleanup(admin.SetLockSafety(expirySafety))
	hook := func(ctx context.Context, u string) error {
		time.Sleep(expiryStall)
		return nil
	}
	err = a.WithRevoker(hook).DeprovisionDrained(context.Background(), "w", st.ID, st.LastSeq)
	wantPartial(t, err, user)
	intact(t, a, js, "w")
	noFence(t, js, "w")
}
