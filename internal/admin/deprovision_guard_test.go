package admin_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/nats-io/nats.go/jetstream"

	"github.com/bogworks/mash/internal/admin"
	"github.com/bogworks/mash/internal/sap"
	"github.com/bogworks/mash/internal/store"
	"github.com/bogworks/mash/internal/testenv"
)

// provisionAt provisions addr with a fresh user key and returns the key.
func provisionAt(t *testing.T, e *testenv.Env, a *admin.Admin, addr sap.Address) string {
	t.Helper()
	_, user := e.Mint(t, addr)
	if _, err := a.Provision(context.Background(), addr, user, e.Account, "", admin.Meta{}); err != nil {
		t.Fatal(err)
	}
	return user
}

// putFinalMarker writes a retired marker for addr in the given state, as a finished or interrupted removal leaves it.
func putFinalMarker(t *testing.T, js jetstream.JetStream, addr sap.Address, user, state string) {
	t.Helper()
	rk, _ := js.KeyValue(context.Background(), store.RetiredBucket)
	val, _ := json.Marshal(store.Retired{Owner: addr.Owner, Group: addr.Group, Instance: addr.Instance, User: user,
		RetiredAt: "2026-11-10T00:00:00Z", State: state})
	if _, err := rk.Put(context.Background(), addr.Flat(), val); err != nil {
		t.Fatal(err)
	}
}

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
	id := epIn(t, "acme", "team1", "ops1")
	user := provisionAt(t, e, a, id)
	prev := admin.SetAfterDeprovisionGet(func() {
		if _, err := a.SetMeta(ctx, id, admin.MetaUpdate{Role: sp(time.Now().String())}); err != nil {
			t.Error(err)
		}
	})
	err := a.DeprovisionIfUser(ctx, id, user)
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
	if _, err := a.SetMeta(ctx, id, admin.MetaUpdate{Role: sp("still-operable")}); err != nil {
		t.Fatalf("the identity must still be operable: %v", err)
	}
	if err := a.DeprovisionIfUser(ctx, id, user); err != nil {
		t.Fatal(err)
	}
	if !markerPresent(t, js, id.Flat()) {
		t.Fatal("a completed removal must retire the name")
	}
}

// A crash after the purge and before the marker is final: the marker is pending; running the same removal again finishes it.
func TestDeprovisionIfUserCrashAfterPurgeLeavesPendingMarkerThatIsCompleted(t *testing.T) {
	e, a, _ := setup(t)
	ctx := context.Background()
	id := epIn(t, "acme", "team1", "ops1")
	user := provisionAt(t, e, a, id)
	prev := admin.SetAfterDeprovisionPurge(func() { a.Conn().Close() })
	if err := a.DeprovisionIfUser(ctx, id, user); err == nil {
		t.Fatal("the removal cannot have finished on a closed connection")
	}
	admin.SetAfterDeprovisionPurge(prev)
	b, js := initOn(t, e)
	kv, _ := js.KeyValue(ctx, store.LeaseBucket)
	_ = kv.Delete(ctx, admin.LockKey(id.Flat()))
	dir, _ := js.KeyValue(ctx, store.DirectoryBucket)
	if _, err := dir.Get(ctx, id.Flat()); !errors.Is(err, jetstream.ErrKeyNotFound) {
		t.Fatalf("the record should be purged: %v", err)
	}
	wantMarker(t, js, id.Flat(), store.RetiredPending, user) // the marker precedes the purge, so the name is blocked
	if err := b.DeprovisionIfUser(ctx, id, user); err != nil {
		t.Fatalf("re-running must complete the removal: %v", err)
	}
	wantMarker(t, js, id.Flat(), store.RetiredFinal, user)
	_, err := b.Provision(ctx, id, user, e.Account, "", admin.Meta{})
	wantRetired(t, err, id)
	if err := b.DeprovisionIfUser(ctx, id, user); err != nil { // idempotent once finished
		t.Fatal(err)
	}
}

// A guarded removal of a new-form key that was never provisioned (a mistyped command) changes nothing: ErrNoRecord, no
// marker, no lease fence, and an unrelated inbox stream with that flat name survives.
func TestDeprovisionIfUserNeverProvisionedChangesNothing(t *testing.T) {
	e, a, js := setup(t)
	ctx := context.Background()
	id := epIn(t, "acme", "team1", "ghost")
	_, user := e.Mint(t, id)
	if _, err := js.CreateStream(ctx, jetstream.StreamConfig{Name: sap.InboxStream(id.Flat()), Subjects: []string{"unrelated." + id.Flat()}}); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		if err := a.DeprovisionIfUser(ctx, id, user); !errors.Is(err, admin.ErrNoRecord) {
			t.Fatalf("want ErrNoRecord, got %v", err)
		}
	}
	if markerPresent(t, js, id.Flat()) {
		t.Fatal("a never-provisioned name was burned")
	}
	if _, err := js.Stream(ctx, sap.InboxStream(id.Flat())); err != nil {
		t.Fatalf("the unrelated stream was deleted: %v", err)
	}
	if leasePresent(t, js, id.Flat()) {
		t.Fatal("the lease was fenced")
	}
}

// A partial provision (stream, no record, no marker) keeps its mail and gets no marker from a guarded removal.
func TestDeprovisionIfUserPartialProvisionKeepsStreamAndWritesNoMarker(t *testing.T) {
	e, a, js := setup(t)
	ctx := context.Background()
	id := epIn(t, "acme", "team1", "ops1")
	user := provisionAt(t, e, a, id)
	dir, _ := js.KeyValue(ctx, store.DirectoryBucket)
	if err := dir.Purge(ctx, id.Flat()); err != nil {
		t.Fatal(err)
	}
	if err := a.DeprovisionIfUser(ctx, id, user); !errors.Is(err, admin.ErrNoRecord) {
		t.Fatalf("want ErrNoRecord, got %v", err)
	}
	if markerPresent(t, js, id.Flat()) {
		t.Fatal("a marker was written for a record-less partial provision")
	}
	if _, err := js.Stream(ctx, sap.InboxStream(id.Flat())); err != nil {
		t.Fatalf("the inbox stream was deleted: %v", err)
	}
	if leasePresent(t, js, id.Flat()) {
		t.Fatal("the lease was fenced")
	}
}

// A final marker with the record gone: the guarded removal is idempotent and still finishes the fence and inbox delete.
func TestDeprovisionIfUserFinalMarkerRecordGoneIsIdempotent(t *testing.T) {
	e, a, js := setup(t)
	ctx := context.Background()
	id := epIn(t, "acme", "team1", "ops1")
	user := provisionAt(t, e, a, id)
	dir, _ := js.KeyValue(ctx, store.DirectoryBucket)
	if err := dir.Purge(ctx, id.Flat()); err != nil {
		t.Fatal(err)
	}
	putFinalMarker(t, js, id, user, store.RetiredFinal)
	for i := 0; i < 2; i++ {
		if err := a.DeprovisionIfUser(ctx, id, user); err != nil {
			t.Fatalf("run %d: %v", i, err)
		}
	}
	wantMarker(t, js, id.Flat(), store.RetiredFinal, user)
	if _, err := js.Stream(ctx, sap.InboxStream(id.Flat())); err == nil {
		t.Fatal("the inbox stream was left")
	}
}
