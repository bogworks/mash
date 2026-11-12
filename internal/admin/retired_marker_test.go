package admin_test

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/nats-io/nats.go/jetstream"

	"github.com/bogworks/mash/internal/admin"
	"github.com/bogworks/mash/internal/sap"
	"github.com/bogworks/mash/internal/store"
)

// The two-phase retired marker: pending before anything is revoked or purged, final once the record is gone.

func readMarker(t *testing.T, js jetstream.JetStream, key string) (store.Retired, bool) {
	t.Helper()
	rk, err := js.KeyValue(context.Background(), store.RetiredBucket)
	if err != nil {
		t.Fatal(err)
	}
	e, err := rk.Get(context.Background(), key)
	if errors.Is(err, jetstream.ErrKeyNotFound) {
		return store.Retired{}, false
	}
	if err != nil {
		t.Fatal(err)
	}
	var m store.Retired
	if err := json.Unmarshal(e.Value(), &m); err != nil {
		t.Fatal(err)
	}
	return m, true
}

func wantMarker(t *testing.T, js jetstream.JetStream, key, state, user string) store.Retired {
	t.Helper()
	m, ok := readMarker(t, js, key)
	if !ok || m.State != state || (user != "" && m.User != user) || m.RetiredAt == "" {
		t.Fatalf("marker of %s: %+v (present %v), want state %q user %q", key, m, ok, state, user)
	}
	return m
}

func putMarker(t *testing.T, js jetstream.JetStream, id sap.EndpointIdentity, user, state string) {
	t.Helper()
	rk, _ := js.KeyValue(context.Background(), store.RetiredBucket)
	val, _ := json.Marshal(store.Retired{Agent: id.Agent, Owner: id.Owner, Name: id.Name, User: user,
		RetiredAt: "2026-11-10T00:00:00Z", State: state})
	if _, err := rk.Put(context.Background(), id.Flat(), val); err != nil {
		t.Fatal(err)
	}
}

// recreateRetired replaces the retired bucket with one made from RetiredConfig as mut changes it.
func recreateRetired(t *testing.T, js jetstream.JetStream, mut func(*jetstream.KeyValueConfig)) jetstream.KeyValue {
	t.Helper()
	ctx := context.Background()
	if err := js.DeleteKeyValue(ctx, store.RetiredBucket); err != nil {
		t.Fatal(err)
	}
	cfg := store.RetiredConfig()
	mut(&cfg)
	kv, err := js.CreateKeyValue(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	return kv
}

// fillBucket puts distinct keys until the bucket refuses a write.
func fillBucket(t *testing.T, kv jetstream.KeyValue) {
	t.Helper()
	val := []byte(strings.Repeat("x", 100))
	for i := range 1000 {
		if _, err := kv.Put(context.Background(), "fill-"+strings.Repeat("k", i), val); err != nil {
			return
		}
	}
	t.Fatal("the bucket never filled")
}

func recordIntact(t *testing.T, js jetstream.JetStream, id sap.EndpointIdentity, user string) {
	t.Helper()
	if got := recordUserOf(t, js, id.Flat()); got != user {
		t.Fatalf("record user %s, want %s", got, user)
	}
	if _, err := js.Stream(context.Background(), "inbox-"+id.Flat()); err != nil {
		t.Fatalf("stream gone: %v", err)
	}
	noFence(t, js, id.Flat())
}

func wantPendingBlock(t *testing.T, err error, id sap.EndpointIdentity) {
	t.Helper()
	wantRetired(t, err, id)
	if !strings.Contains(err.Error(), "pending") {
		t.Fatalf("a pending marker must say so, got %v", err)
	}
}

// runPath runs the unconditional deprovision or the user-guarded one.
func runPath(ctx context.Context, x *admin.Admin, path, key, user string) error {
	if path == "deprovision" {
		return x.Deprovision(ctx, key)
	}
	return x.DeprovisionIfUser(ctx, key, user)
}

var bothPaths = []string{"deprovision", "if-user"}

// A final marker carries state final and the user; the absent state of an older marker counts as final.
func TestRetiredMarkerFinalAfterBothDeprovisions(t *testing.T) {
	e, a, js := setup(t)
	ctx := context.Background()
	id1, u1 := provNewForm(t, e, a, "acme", "team1", "ops1")
	id2, u2 := provNewForm(t, e, a, "acme", "team1", "ops2")
	if err := a.Deprovision(ctx, id1.Flat()); err != nil {
		t.Fatal(err)
	}
	if err := a.DeprovisionIfUser(ctx, id2.Flat(), u2); err != nil {
		t.Fatal(err)
	}
	wantMarker(t, js, id1.Flat(), store.RetiredFinal, u1)
	wantMarker(t, js, id2.Flat(), store.RetiredFinal, u2)
	if (store.Retired{}).IsPending() {
		t.Fatal("an absent state is final")
	}
}

// A pending marker blocks ProvisionIdentity exactly like a final one and says the retirement is unfinished.
func TestPendingMarkerBlocksProvisionIdentity(t *testing.T) {
	e, a, js := setup(t)
	id := newID(t, "acme", "team1", "ops1")
	_, user := e.Mint(t, "ops1")
	putMarker(t, js, id, user, store.RetiredPending)
	_, err := a.ProvisionIdentity(context.Background(), id, nil, user, e.Account, "", admin.Meta{})
	wantPendingBlock(t, err, id)
	untouched(t, js, nil, nil)
}

// The marker is already pending when the user is revoked.
func TestPendingMarkerIsWrittenBeforeRevocation(t *testing.T) {
	for _, path := range bothPaths {
		t.Run(path, func(t *testing.T) {
			e, a, js := setup(t)
			id, user := provNewForm(t, e, a, "acme", "team1", "ops1")
			var seen store.Retired
			var calls int
			rev := a.WithRevoker(func(context.Context, string) error {
				calls++
				seen, _ = readMarker(t, js, id.Flat())
				return nil
			})
			if err := runPath(context.Background(), rev, path, id.Flat(), user); err != nil {
				t.Fatal(err)
			}
			if calls != 1 || seen.State != store.RetiredPending || seen.User != user {
				t.Fatalf("at revocation (%d calls) the marker was %+v, want pending for %s", calls, seen, user)
			}
			wantMarker(t, js, id.Flat(), store.RetiredFinal, user)
		})
	}
}

// A crash right after the guarded purge leaves the marker pending (the name stays blocked); the same call run again
// completes it to final and finishes the fence and the inbox delete. Both paths.
func TestCrashAfterPurgeLeavesPendingAndRerunFinalizes(t *testing.T) {
	for _, path := range bothPaths {
		t.Run(path, func(t *testing.T) {
			e, a, _ := setup(t)
			ctx := context.Background()
			id, user := provNewForm(t, e, a, "acme", "team1", "ops1")
			prev := admin.SetAfterDeprovisionPurge(func() { a.Conn().Close() })
			if err := runPath(ctx, a, path, id.Flat(), user); err == nil {
				t.Fatal("the removal cannot have finished on a closed connection")
			}
			admin.SetAfterDeprovisionPurge(prev)
			b, js := initOn(t, e)
			lease, _ := js.KeyValue(ctx, store.LeaseBucket)
			_ = lease.Delete(ctx, admin.LockKey(id.Flat())) // the dead admin's lock would otherwise wait out its TTL
			dir, _ := js.KeyValue(ctx, store.DirectoryBucket)
			if _, err := dir.Get(ctx, id.Flat()); !errors.Is(err, jetstream.ErrKeyNotFound) {
				t.Fatalf("the record should be purged: %v", err)
			}
			wantMarker(t, js, id.Flat(), store.RetiredPending, user)
			_, err := b.ProvisionIdentity(ctx, id, nil, user, e.Account, "", admin.Meta{})
			wantPendingBlock(t, err, id)
			if err := runPath(ctx, b, path, id.Flat(), user); err != nil {
				t.Fatalf("re-running must finish: %v", err)
			}
			wantMarker(t, js, id.Flat(), store.RetiredFinal, user)
			if _, err := js.Stream(ctx, "inbox-"+id.Flat()); err == nil {
				t.Fatal("the inbox was not deleted by the re-run")
			}
			if _, err := lease.Get(ctx, id.Flat()); err != nil {
				t.Fatalf("the re-run did not fence the lease: %v", err)
			}
			_, err = b.ProvisionIdentity(ctx, id, nil, user, e.Account, "", admin.Meta{})
			wantRetired(t, err, id)
		})
	}
}

// A pending marker from an earlier run for the record's own user is resumed: revoke, purge, finalize.
func TestPendingMarkerForTheSameUserIsResumed(t *testing.T) {
	for _, path := range bothPaths {
		t.Run(path, func(t *testing.T) {
			e, a, js := setup(t)
			id, user := provNewForm(t, e, a, "acme", "team1", "ops1")
			putMarker(t, js, id, user, store.RetiredPending)
			var revoked []string
			rev := a.WithRevoker(func(_ context.Context, u string) error { revoked = append(revoked, u); return nil })
			if err := runPath(context.Background(), rev, path, id.Flat(), user); err != nil {
				t.Fatal(err)
			}
			if len(revoked) != 1 || revoked[0] != user {
				t.Fatalf("revoked %v", revoked)
			}
			m := wantMarker(t, js, id.Flat(), store.RetiredFinal, user)
			if m.RetiredAt != "2026-11-10T00:00:00Z" {
				t.Fatalf("the first retired-at must be kept: %s", m.RetiredAt)
			}
			gone(t, js, id.Flat())
		})
	}
}

// A refusal after the marker was written deletes the marker this call wrote.
func TestRefusalDeletesThePendingMarkerThisCallWrote(t *testing.T) {
	for _, path := range bothPaths {
		t.Run(path+"/revoke-fails", func(t *testing.T) {
			e, a, js := setup(t)
			ctx := context.Background()
			id, user := provNewForm(t, e, a, "acme", "team1", "ops1")
			boom := errors.New("resolver down")
			rev := a.WithRevoker(func(context.Context, string) error { return boom })
			err := runPath(ctx, rev, path, id.Flat(), user)
			if !errors.Is(err, boom) || !admin.IsRefusal(err) {
				t.Fatalf("%v", err)
			}
			if _, ok := readMarker(t, js, id.Flat()); ok {
				t.Fatal("a refused removal left a marker")
			}
			recordIntact(t, js, id, user)
			if _, err := a.SetMeta(ctx, id.Flat(), admin.MetaUpdate{Role: sp("still-operable")}); err != nil {
				t.Fatal(err)
			}
		})
	}
	t.Run("changed-record", func(t *testing.T) {
		e, a, js := setup(t)
		ctx := context.Background()
		id, user := provNewForm(t, e, a, "acme", "team1", "ops1")
		prev := admin.SetAfterDeprovisionGet(func() {
			if _, err := a.SetMeta(ctx, id.Flat(), admin.MetaUpdate{Role: sp(time.Now().String())}); err != nil {
				t.Error(err)
			}
		})
		err := a.DeprovisionIfUser(ctx, id.Flat(), user)
		admin.SetAfterDeprovisionGet(prev)
		if !errors.Is(err, admin.ErrRecordChanged) || !admin.IsRefusal(err) {
			t.Fatalf("%v", err)
		}
		if _, ok := readMarker(t, js, id.Flat()); ok {
			t.Fatal("the pending marker of a refused removal was not deleted")
		}
	})
	t.Run("other-user", func(t *testing.T) {
		e, a, js := setup(t)
		id, user := provNewForm(t, e, a, "acme", "team1", "ops1")
		_, other := e.Mint(t, "someone-else")
		if err := a.DeprovisionIfUser(context.Background(), id.Flat(), other); !errors.Is(err, admin.ErrRecordOtherUser) {
			t.Fatalf("%v", err)
		}
		if _, ok := readMarker(t, js, id.Flat()); ok {
			t.Fatal("a marker for another user's refusal")
		}
		recordIntact(t, js, id, user)
	})
}

// A failure to delete the pending marker is returned, joined with the refusal, and the marker stays (still blocking).
func TestFailedMarkerDeleteSurfaces(t *testing.T) {
	for _, path := range bothPaths {
		t.Run(path, func(t *testing.T) {
			e, a, _ := setup(t)
			id, user := provNewForm(t, e, a, "acme", "team1", "ops1")
			boom := errors.New("resolver down")
			rev := a.WithRevoker(func(context.Context, string) error { a.Conn().Close(); return boom })
			err := runPath(context.Background(), rev, path, id.Flat(), user)
			if !errors.Is(err, boom) || admin.IsRefusal(err) || !strings.Contains(err.Error(), "pending") || !strings.Contains(err.Error(), id.Flat()) {
				t.Fatalf("want the refusal joined with the failed marker delete, got %v", err)
			}
			_, js := initOn(t, e)
			wantMarker(t, js, id.Flat(), store.RetiredPending, user)
		})
	}
}

// A full retired bucket makes both deprovisions fail before the user is revoked and the record purged, and
// provisioning fail closed.
func TestFullRetiredBucketFailsBeforeAnyRevocation(t *testing.T) {
	for _, path := range bothPaths {
		t.Run(path, func(t *testing.T) {
			e, a, js := setup(t)
			id, user := provNewForm(t, e, a, "acme", "team1", "ops1")
			kv := recreateRetired(t, js, func(c *jetstream.KeyValueConfig) { c.MaxBytes = 2048 })
			fillBucket(t, kv)
			var calls int
			rev := a.WithRevoker(func(context.Context, string) error { calls++; return nil })
			err := runPath(context.Background(), rev, path, id.Flat(), user)
			if err == nil {
				t.Fatal("a full bucket must fail the deprovision")
			}
			if calls != 0 || admin.IsPartial(err) {
				t.Fatalf("revoked (%d calls) before the marker could be written: %v", calls, err)
			}
			recordIntact(t, js, id, user)
			if _, ok := readMarker(t, js, id.Flat()); ok {
				t.Fatal("a marker in a full bucket?")
			}
		})
	}
	t.Run("provision", func(t *testing.T) {
		e, a, js := setup(t)
		kv := recreateRetired(t, js, func(c *jetstream.KeyValueConfig) { c.MaxBytes = 2048 })
		fillBucket(t, kv)
		id := newID(t, "acme", "team1", "ops1")
		_, user := e.Mint(t, "ops1")
		keys, streams := dirKeys(t, js), streamNames(t, js)
		_, err := a.ProvisionIdentity(context.Background(), id, nil, user, e.Account, "", admin.Meta{})
		if err == nil || !strings.Contains(err.Error(), "full") {
			t.Fatalf("a full retired bucket must refuse provisioning, got %v", err)
		}
		untouched(t, js, keys, streams)
	})
}

// A TTL, or a history other than the configured one, is refused where the bucket is opened (the lazy open of every
// operation) and at Init, and the bucket is not modified.
func TestRetiredBucketConfigFailsClosed(t *testing.T) {
	cases := map[string]func(*jetstream.KeyValueConfig){
		"ttl":     func(c *jetstream.KeyValueConfig) { c.TTL = time.Hour },
		"history": func(c *jetstream.KeyValueConfig) { c.History = 5 },
	}
	for name, mut := range cases {
		t.Run(name, func(t *testing.T) {
			e, a, js := setup(t)
			ctx := context.Background()
			id, user := provNewForm(t, e, a, "acme", "team1", "ops1")
			recreateRetired(t, js, mut)
			if err := a.Init(ctx); err == nil || !strings.Contains(err.Error(), store.RetiredBucket) {
				t.Fatalf("Init must refuse, got %v", err)
			}
			kv, _ := js.KeyValue(ctx, store.RetiredBucket)
			st, _ := kv.Status(ctx)
			if (name == "ttl" && st.TTL() != time.Hour) || (name == "history" && st.History() != 5) {
				t.Fatal("mash modified the bucket")
			}
			_, err := a.ProvisionIdentity(ctx, newID(t, "acme", "team1", "ops2"), nil, user, e.Account, "", admin.Meta{})
			if err == nil || !strings.Contains(err.Error(), store.RetiredBucket) {
				t.Fatalf("ProvisionIdentity must refuse, got %v", err)
			}
			var calls int
			rev := a.WithRevoker(func(context.Context, string) error { calls++; return nil })
			for _, path := range bothPaths {
				if err := runPath(ctx, rev, path, id.Flat(), user); err == nil || !strings.Contains(err.Error(), store.RetiredBucket) {
					t.Fatalf("%s must refuse, got %v", path, err)
				}
			}
			if calls != 0 {
				t.Fatal("revoked against a bucket that would not hold the marker")
			}
			recordIntact(t, js, id, user)
		})
	}
}

// Exactly one of several concurrent provisions of one name wins; no panic, one record, one stream.
func TestConcurrentProvisionIdentitySameName(t *testing.T) {
	quickLocks(t, longWait)
	e, a, js := setup(t)
	id := newID(t, "acme", "team1", "ops1")
	_, user := e.Mint(t, "ops1")
	admins := []*admin.Admin{a, secondAdmin(t, e, store.DefaultLimits()), secondAdmin(t, e, store.DefaultLimits()), a}
	errs := make([]error, len(admins))
	var wg sync.WaitGroup
	for i, x := range admins {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, errs[i] = x.ProvisionIdentity(context.Background(), id, nil, user, e.Account, "", admin.Meta{})
		}()
	}
	wg.Wait()
	wins := 0
	for _, err := range errs {
		if err == nil {
			wins++
		} else if !strings.Contains(err.Error(), "already provisioned") {
			t.Fatalf("a loser must be refused as already provisioned, got %v", err)
		}
	}
	if wins != 1 {
		t.Fatalf("%d provisions won: %v", wins, errs)
	}
	if k := dirKeys(t, js); len(k) != 1 || k[0] != id.Flat() {
		t.Fatalf("directory %v", k)
	}
	if s := streamNames(t, js); len(s) != 1 {
		t.Fatalf("streams %v", s)
	}
}

// Provision and Deprovision racing on one flat key end consistent: the record present with no marker, or the record
// gone with the name blocked by a final marker.
func TestProvisionRacingDeprovisionIsConsistent(t *testing.T) {
	quickLocks(t, longWait)
	e, a, js := setup(t)
	ctx := context.Background()
	for i, nm := range []string{"r1", "r2", "r3", "r4"} {
		id := newID(t, "acme", "team1", nm)
		_, user := e.Mint(t, nm)
		b := secondAdmin(t, e, store.DefaultLimits())
		path := bothPaths[i%2]
		if i >= 2 { // the second half races a repeat provision against a removal of a live record
			if _, err := a.ProvisionIdentity(ctx, id, nil, user, e.Account, "", admin.Meta{}); err != nil {
				t.Fatal(err)
			}
		}
		var wg sync.WaitGroup
		var perr, derr error
		wg.Add(2)
		go func() {
			defer wg.Done()
			_, perr = a.ProvisionIdentity(ctx, id, nil, user, e.Account, "", admin.Meta{})
		}()
		go func() { defer wg.Done(); derr = runPath(ctx, b, path, id.Flat(), user) }()
		wg.Wait()
		_, marked := readMarker(t, js, id.Flat())
		dir, _ := js.KeyValue(ctx, store.DirectoryBucket)
		_, rerr := dir.Get(ctx, id.Flat())
		present := rerr == nil
		switch {
		case present && !marked && perr == nil:
		case !present && !marked && perr != nil && strings.Contains(perr.Error(), "was deprovisioned"):
			// the removal ran first on a name with no record: it burns nothing, and only the 90 s lease fence refuses
		case !present && marked:
			wantMarker(t, js, id.Flat(), store.RetiredFinal, user)
			_, err := a.ProvisionIdentity(ctx, id, nil, user, e.Account, "", admin.Meta{})
			wantRetired(t, err, id)
		default:
			t.Fatalf("%s: record present %v, marked %v, provision %v, deprovision %v", nm, present, marked, perr, derr)
		}
	}
}

// Losing the flat-key lock, the alias name lock or the admission lock while ProvisionIdentity runs stops it with
// nothing written after the loss.
func TestProvisionIdentityLockLossWritesNothing(t *testing.T) {
	for _, lost := range []string{"flat", "alias", "admission"} {
		for _, at := range []string{"after-admission-lock", "before-stream-write"} {
			t.Run(lost+"/"+at, func(t *testing.T) {
				quickLocks(t, longWait)
				e, a, js := setup(t)
				provLegacy(t, e, a, "ghost", admin.Meta{})
				id := newID(t, "acme", "team1", "ops1")
				_, user := e.Mint(t, "ops1")
				name := map[string]string{"flat": id.Flat(), "alias": "ghost", "admission": admin.AdmissionLockName}[lost]
				keys, streams := dirKeys(t, js), streamNames(t, js)
				steal := func() { putLock(t, js, name, time.Now().Add(time.Minute)) }
				if at == "after-admission-lock" {
					seam(t, admin.SetAfterAdmissionLock, steal)
				} else {
					prev := admin.SetAfterStreamRead(func(int64) { steal() })
					t.Cleanup(func() { admin.SetAfterStreamRead(prev) })
				}
				_, err := a.ProvisionIdentity(context.Background(), id, &store.Alias{Owner: "o", Name: "ghost"}, user, e.Account, "", admin.Meta{})
				if err == nil || !strings.Contains(err.Error(), "was lost or is about to expire") {
					t.Fatalf("want a lock-loss error, got %v", err)
				}
				untouched(t, js, keys, streams)
			})
		}
	}
}
