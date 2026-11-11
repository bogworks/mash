package admin_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/nats-io/nats.go/jetstream"

	"github.com/bogworks/mash/internal/admin"
	"github.com/bogworks/mash/internal/sap"
	"github.com/bogworks/mash/internal/store"
)

// suspendFlat parks a new-form identity's inbox the way Suspend does (Suspend itself does not take a flat key yet), after
// storing one message, and returns the suspension id and last sequence DeprovisionDrained needs.
func suspendFlat(t *testing.T, js jetstream.JetStream, id sap.EndpointIdentity, user string) (string, uint64) {
	t.Helper()
	ctx := context.Background()
	if _, err := js.Publish(ctx, id.Subjects().Mail, []byte("stored")); err != nil {
		t.Fatal(err)
	}
	s, err := js.Stream(ctx, sap.InboxStream(id.Flat()))
	if err != nil {
		t.Fatal(err)
	}
	cfg := s.CachedInfo().Config
	saved, _ := json.Marshal(cfg.Subjects)
	cfg.Metadata = map[string]string{store.MetaSuspendedSubjects: string(saved), store.MetaSuspendedID: "sid-1", store.MetaSuspendedUser: user}
	cfg.Subjects = []string{store.SuspendedSubject(id.Flat())}
	if s, err = js.UpdateStream(ctx, cfg); err != nil {
		t.Fatal(err)
	}
	info, err := s.Info(ctx)
	if err != nil {
		t.Fatal(err)
	}
	return "sid-1", info.State.LastSeq
}

// A drained new-form identity is deprovisioned: marker pending before the revoke, final after the purge, never reusable.
func TestDeprovisionDrainedNewFormWritesPendingThenFinal(t *testing.T) {
	e, a, js := setup(t)
	ctx := context.Background()
	id, user := provNewForm(t, e, a, "acme", "team1", "ops1")
	sid, seq := suspendFlat(t, js, id, user)
	var seen store.Retired
	rev := a.WithRevoker(func(context.Context, string) error { seen, _ = readMarker(t, js, id.Flat()); return nil })
	if err := rev.DeprovisionDrained(ctx, id.Flat(), sid, seq); err != nil {
		t.Fatal(err)
	}
	if seen.State != store.RetiredPending || seen.User != user {
		t.Fatalf("at revocation the marker was %+v, want pending for %s", seen, user)
	}
	wantMarker(t, js, id.Flat(), store.RetiredFinal, user)
	gone(t, js, id.Flat())
	lease, _ := js.KeyValue(ctx, store.LeaseBucket)
	if _, err := lease.Get(ctx, id.Flat()); err != nil {
		t.Fatalf("lease not fenced: %v", err)
	}
	_, err := a.ProvisionIdentity(ctx, id, nil, user, e.Account, "", admin.Meta{})
	wantRetired(t, err, id)
}

// Every drain check still refuses, touching nothing and writing no marker.
func TestDeprovisionDrainedNewFormRefusalsTouchNothing(t *testing.T) {
	e, a, js := setup(t)
	ctx := context.Background()
	id, user := provNewForm(t, e, a, "acme", "team1", "ops1")
	sid, seq := suspendFlat(t, js, id, user)
	for name, c := range map[string]struct {
		id  string
		seq uint64
	}{"wrong-seq": {sid, seq + 1}, "wrong-id": {"other", seq}, "empty-id": {"", seq}} {
		if err := a.DeprovisionDrained(ctx, id.Flat(), c.id, c.seq); err == nil {
			t.Fatalf("%s: refusal expected", name)
		}
		if _, ok := readMarker(t, js, id.Flat()); ok {
			t.Fatalf("%s: a refused call wrote a marker", name)
		}
		recordIntact(t, js, id, user)
	}
	// A revoke that fails is a definitive refusal before the purge: the pending marker this call wrote is deleted.
	boom := errors.New("resolver down")
	err := a.WithRevoker(func(context.Context, string) error { return boom }).DeprovisionDrained(ctx, id.Flat(), sid, seq)
	if !errors.Is(err, boom) {
		t.Fatalf("%v", err)
	}
	if _, ok := readMarker(t, js, id.Flat()); ok {
		t.Fatal("a refused removal left a marker")
	}
	recordIntact(t, js, id, user)
}

// A crash after the purge leaves the marker pending; the same command run again finishes it.
func TestDeprovisionDrainedNewFormCrashResume(t *testing.T) {
	e, a, js := setup(t)
	ctx := context.Background()
	id, user := provNewForm(t, e, a, "acme", "team1", "ops1")
	sid, seq := suspendFlat(t, js, id, user)
	prev := admin.SetAfterDeprovisionPurge(func() { a.Conn().Close() })
	if err := a.DeprovisionDrained(ctx, id.Flat(), sid, seq); err == nil {
		t.Fatal("the removal cannot have finished on a closed connection")
	}
	admin.SetAfterDeprovisionPurge(prev)
	b, js := initOn(t, e)
	lease, _ := js.KeyValue(ctx, store.LeaseBucket)
	_ = lease.Purge(ctx, admin.LockKey(id.Flat()))
	wantMarker(t, js, id.Flat(), store.RetiredPending, user)
	_, err := b.ProvisionIdentity(ctx, id, nil, user, e.Account, "", admin.Meta{})
	wantPendingBlock(t, err, id)
	if err := b.DeprovisionDrained(ctx, id.Flat(), sid, seq); err != nil {
		t.Fatalf("re-running must finish: %v", err)
	}
	wantMarker(t, js, id.Flat(), store.RetiredFinal, user)
	gone(t, js, id.Flat())
	if _, err := lease.Get(ctx, id.Flat()); err != nil {
		t.Fatalf("the re-run did not fence the lease: %v", err)
	}
	_, err = b.ProvisionIdentity(ctx, id, nil, user, e.Account, "", admin.Meta{})
	wantRetired(t, err, id)
}
