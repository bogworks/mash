package admin_test

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/nats-io/nats.go/jetstream"

	"github.com/bogworks/mash/internal/admin"
	"github.com/bogworks/mash/internal/store"
)

func markerPresent(t *testing.T, js jetstream.JetStream, key string) bool {
	t.Helper()
	rk, err := js.KeyValue(context.Background(), store.RetiredBucket)
	if err != nil {
		t.Fatal(err)
	}
	_, err = rk.Get(context.Background(), key)
	if err != nil && !errors.Is(err, jetstream.ErrKeyNotFound) {
		t.Fatal(err)
	}
	return err == nil
}

// A guarded removal that is refused (the record keeps changing) leaves the record in place and burns nothing: the
// identity is still operable and a later removal retires it.
func TestDeprovisionIfUserRefusedRemovalWritesNoMarker(t *testing.T) {
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
	if !errors.Is(err, admin.ErrRecordChanged) {
		t.Fatalf("want ErrRecordChanged, got %v", err)
	}
	kv, _ := js.KeyValue(ctx, store.DirectoryBucket)
	if _, err := kv.Get(ctx, id.Flat()); err != nil {
		t.Fatalf("the record must still be present: %v", err)
	}
	if markerPresent(t, js, id.Flat()) {
		t.Fatal("a refused removal burned the name")
	}
	if _, err := a.SetMeta(ctx, id.Flat(), admin.MetaUpdate{Role: sp("still-operable")}); err != nil {
		t.Fatalf("the identity must still be operable: %v", err)
	}
	if err := a.DeprovisionIfUser(ctx, id.Flat(), user); err != nil {
		t.Fatal(err)
	}
	if !markerPresent(t, js, id.Flat()) {
		t.Fatal("a completed removal must retire the name")
	}
}

// A crash after the purge and before the marker: running the same removal again writes the marker and finishes.
func TestDeprovisionIfUserCrashAfterPurgeBeforeMarkerIsCompleted(t *testing.T) {
	e, a, _ := setup(t)
	ctx := context.Background()
	id, user := provNewForm(t, e, a, "acme", "team1", "ops1")
	prev := admin.SetAfterDeprovisionPurge(func() { a.Conn().Close() })
	if err := a.DeprovisionIfUser(ctx, id.Flat(), user); err == nil {
		t.Fatal("the removal cannot have finished on a closed connection")
	}
	admin.SetAfterDeprovisionPurge(prev)
	b, js := initOn(t, e)
	kv, _ := js.KeyValue(ctx, store.LeaseBucket)
	_ = kv.Purge(ctx, admin.LockKey(id.Flat()))
	dir, _ := js.KeyValue(ctx, store.DirectoryBucket)
	if _, err := dir.Get(ctx, id.Flat()); !errors.Is(err, jetstream.ErrKeyNotFound) {
		t.Fatalf("the record should be purged: %v", err)
	}
	if markerPresent(t, js, id.Flat()) {
		t.Fatal("the crash happened before the marker")
	}
	if err := b.DeprovisionIfUser(ctx, id.Flat(), user); err != nil {
		t.Fatalf("re-running must complete the removal: %v", err)
	}
	if !markerPresent(t, js, id.Flat()) {
		t.Fatal("re-running did not write the marker")
	}
	_, err := b.ProvisionIdentity(ctx, id, nil, user, e.Account, "", admin.Meta{})
	wantRetired(t, err, id)
	if err := b.DeprovisionIfUser(ctx, id.Flat(), user); err != nil { // idempotent once finished
		t.Fatal(err)
	}
}

// A legacy name with no record still refuses as before (nothing proves ownership) and writes no marker.
func TestDeprovisionIfUserLegacyNoRecordStillRefuses(t *testing.T) {
	e, a, js := setup(t)
	_, user := e.Mint(t, "ghost")
	if err := a.DeprovisionIfUser(context.Background(), "ghost", user); !errors.Is(err, admin.ErrNoRecord) {
		t.Fatalf("want ErrNoRecord, got %v", err)
	}
	if markerPresent(t, js, "ghost") {
		t.Fatal("legacy names are never marked")
	}
}

func suspendIDSeq(t *testing.T, a *admin.Admin, name string) (string, uint64) {
	t.Helper()
	st, err := a.Suspend(context.Background(), name)
	if err != nil {
		t.Fatal(err)
	}
	return st.ID, st.LastSeq
}

type legacyRemoval struct {
	name string
	run  func(a *admin.Admin, ctx context.Context, user string) error
}

var legacyRemovals = []legacyRemoval{
	{"Deprovision", func(a *admin.Admin, ctx context.Context, _ string) error { return a.Deprovision(ctx, "ghost") }},
	{"DeprovisionIfUser", func(a *admin.Admin, ctx context.Context, u string) error { return a.DeprovisionIfUser(ctx, "ghost", u) }},
}

func TestLegacyDeprovisionRefusedWhileAliased(t *testing.T) {
	for _, rm := range legacyRemovals {
		t.Run(rm.name, func(t *testing.T) {
			e, a, js := setup(t)
			ctx := context.Background()
			_, legUser := e.Mint(t, "ghost")
			if _, err := a.Provision(ctx, "ghost", legUser, e.Account, "", admin.Meta{}); err != nil {
				t.Fatal(err)
			}
			id := newID(t, "acme", "team1", "ops1")
			_, user := e.Mint(t, "ops1")
			if _, err := a.ProvisionIdentity(ctx, id, &store.Alias{Owner: "o", Name: "ghost"}, user, e.Account, "", admin.Meta{}); err != nil {
				t.Fatal(err)
			}
			keys, streams := dirKeys(t, js), streamNames(t, js)
			err := rm.run(a, ctx, legUser)
			if err == nil || !strings.Contains(err.Error(), id.Flat()) || !strings.Contains(err.Error(), "retire") {
				t.Fatalf("want a refusal naming %s and saying to retire it first, got %v", id.Flat(), err)
			}
			untouched(t, js, keys, streams)
			ls, _ := js.KeyValue(ctx, store.LeaseBucket)
			if e, err := ls.Get(ctx, "ghost"); err == nil && strings.Contains(string(e.Value()), "tombstone") {
				t.Fatalf("a refused removal fenced the lease: %s", e.Value())
			}
			// Allowed once the aliasing new-form record is gone.
			if err := a.Deprovision(ctx, id.Flat()); err != nil {
				t.Fatal(err)
			}
			if err := rm.run(a, ctx, legUser); err != nil {
				t.Fatalf("after the alias is gone: %v", err)
			}
		})
	}
}

func TestLegacyDeprovisionDrainedRefusedWhileAliased(t *testing.T) {
	e, a, js := setup(t)
	ctx := context.Background()
	_, legUser := e.Mint(t, "ghost")
	if _, err := a.Provision(ctx, "ghost", legUser, e.Account, "", admin.Meta{}); err != nil {
		t.Fatal(err)
	}
	id := newID(t, "acme", "team1", "ops1")
	_, user := e.Mint(t, "ops1")
	if _, err := a.ProvisionIdentity(ctx, id, &store.Alias{Owner: "o", Name: "ghost"}, user, e.Account, "", admin.Meta{}); err != nil {
		t.Fatal(err)
	}
	sid, seq := suspendIDSeq(t, a, "ghost")
	keys, streams := dirKeys(t, js), streamNames(t, js)
	err := a.DeprovisionDrained(ctx, "ghost", sid, seq)
	if err == nil || !strings.Contains(err.Error(), id.Flat()) {
		t.Fatalf("want a refusal naming %s, got %v", id.Flat(), err)
	}
	untouched(t, js, keys, streams)
	if err := a.Deprovision(ctx, id.Flat()); err != nil {
		t.Fatal(err)
	}
	if err := a.DeprovisionDrained(ctx, "ghost", sid, seq); err != nil {
		t.Fatalf("after the alias is gone: %v", err)
	}
}

// Without any alias, legacy deprovision is unchanged, even beside unrelated new-form records.
func TestLegacyDeprovisionUnchangedWithoutAlias(t *testing.T) {
	e, a, js := setup(t)
	ctx := context.Background()
	provLegacy(t, e, a, "ghost", admin.Meta{})
	provLegacy(t, e, a, "other", admin.Meta{})
	provNewForm(t, e, a, "acme", "team1", "ops1")
	if err := a.Deprovision(ctx, "ghost"); err != nil {
		t.Fatal(err)
	}
	kv, _ := js.KeyValue(ctx, store.DirectoryBucket)
	if _, err := kv.Get(ctx, "ghost"); !errors.Is(err, jetstream.ErrKeyNotFound) {
		t.Fatalf("legacy record should be gone: %v", err)
	}
	provLegacy(t, e, a, "ghost", admin.Meta{}) // reusable
}

// An unreadable new-form record hides a possible alias: the legacy removal fails closed.
func TestLegacyDeprovisionFailsClosedOnUnreadableRecord(t *testing.T) {
	e, a, js := setup(t)
	ctx := context.Background()
	provLegacy(t, e, a, "ghost", admin.Meta{})
	kv, _ := js.KeyValue(ctx, store.DirectoryBucket)
	if _, err := kv.Put(ctx, "acme_team1_bad", []byte("not json")); err != nil {
		t.Fatal(err)
	}
	if err := a.Deprovision(ctx, "ghost"); err == nil {
		t.Fatal("want a refusal: an unreadable new-form record may hold an alias")
	}
	if _, err := kv.Get(ctx, "ghost"); err != nil {
		t.Fatalf("nothing may be removed: %v", err)
	}
}

// A legacy deprovision racing ProvisionIdentity for an alias to it ends consistent, whichever side wins.
func TestLegacyDeprovisionAliasRace(t *testing.T) {
	quickLocks(t, longWait)
	e, a, js := setup(t)
	ctx := context.Background()
	provLegacy(t, e, a, "ghost", admin.Meta{})
	provLegacy(t, e, a, "zzz", admin.Meta{})
	id := newID(t, "acme", "team1", "ops1")
	_, user := e.Mint(t, "ops1")
	var once sync.Once
	depDone := make(chan error, 1)
	prev := admin.SetAfterDirectoryGet(func(k string) {
		if k != "zzz" {
			return
		}
		once.Do(func() {
			go func() { depDone <- a.Deprovision(ctx, "ghost") }()
			time.Sleep(700 * time.Millisecond)
		})
	})
	t.Cleanup(func() { admin.SetAfterDirectoryGet(prev) })
	_, perr := a.ProvisionIdentity(ctx, id, &store.Alias{Owner: "o", Name: "ghost"}, user, e.Account, "", admin.Meta{})
	var derr error
	select {
	case derr = <-depDone:
	case <-time.After(30 * time.Second):
		t.Fatal("deprovision never finished")
	}
	kv, _ := js.KeyValue(ctx, store.DirectoryBucket)
	_, legErr := kv.Get(ctx, "ghost")
	_, newErr := kv.Get(ctx, id.Flat())
	legacyThere, newThere := legErr == nil, newErr == nil
	if newThere && !legacyThere {
		t.Fatalf("dangling alias: new-form record present, legacy gone (provision %v, deprovision %v)", perr, derr)
	}
	if newThere && derr == nil {
		t.Fatalf("deprovision succeeded yet the alias holder remains")
	}
	if !newThere && perr == nil {
		t.Fatalf("provision succeeded but its record is missing")
	}
}
