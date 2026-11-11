package admin_test

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/nats-io/nats.go/jetstream"

	"github.com/bogworks/mash/internal/admin"
	"github.com/bogworks/mash/internal/sap"
	"github.com/bogworks/mash/internal/store"
)

// drainedFlat takes the supported steps for a new-form identity: it stores one message, Suspend parks the inbox, the
// stored mail is consumed and acked as the legacy drained tests do, and the suspension id and last sequence that
// DeprovisionDrained needs come back.
func drainedFlat(t *testing.T, a *admin.Admin, js jetstream.JetStream, id sap.EndpointIdentity) (string, uint64) {
	t.Helper()
	ctx := context.Background()
	if _, err := js.Publish(ctx, id.Subjects().Mail, []byte("stored")); err != nil {
		t.Fatal(err)
	}
	st, err := a.Suspend(ctx, id.Flat())
	if err != nil {
		t.Fatalf("Suspend of a flat key: %v", err)
	}
	if got, err := a.Suspended(ctx, id.Flat()); err != nil || !got.Suspended || got.ID != st.ID || got.LastSeq != st.LastSeq {
		t.Fatalf("Suspended: %+v %v, Suspend gave %+v", got, err, st)
	}
	s, err := js.Stream(ctx, sap.InboxStream(id.Flat()))
	if err != nil {
		t.Fatal(err)
	}
	cons, err := s.Consumer(ctx, store.Consumer)
	if err != nil {
		t.Fatal(err)
	}
	var read uint64
	for deadline := time.Now().Add(20 * time.Second); read < st.LastSeq && time.Now().Before(deadline); {
		b, err := cons.Fetch(10, jetstream.FetchMaxWait(time.Second))
		if err != nil {
			t.Fatal(err)
		}
		for m := range b.Messages() {
			if err := m.DoubleAck(ctx); err != nil {
				t.Fatal(err)
			}
			read++
		}
	}
	if read != st.LastSeq || st.LastSeq == 0 {
		t.Fatalf("drained %d messages, last seq %d", read, st.LastSeq)
	}
	return st.ID, st.LastSeq
}

// A drained new-form identity is deprovisioned: marker pending before the revoke, final after the purge, never reusable.
func TestDeprovisionDrainedNewFormWritesPendingThenFinal(t *testing.T) {
	e, a, js := setup(t)
	ctx := context.Background()
	id, user := provNewForm(t, e, a, "acme", "team1", "ops1")
	sid, seq := drainedFlat(t, a, js, id)
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
	sid, seq := drainedFlat(t, a, js, id)
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
	sid, seq := drainedFlat(t, a, js, id)
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

// Suspend, Resume and Suspended take a flat key; a name that is neither a legacy name nor a flat key is still refused.
func TestSuspendResumeSuspendedAcceptFlatKeys(t *testing.T) {
	e, a, _ := setup(t)
	ctx := context.Background()
	id, _ := provNewForm(t, e, a, "acme", "team1", "ops1")
	st, err := a.Suspend(ctx, id.Flat())
	if err != nil || !st.Suspended || st.ID == "" {
		t.Fatalf("Suspend: %+v %v", st, err)
	}
	if got, err := a.Suspended(ctx, id.Flat()); err != nil || !got.Suspended {
		t.Fatalf("Suspended: %+v %v", got, err)
	}
	if err := a.Resume(ctx, id.Flat()); err != nil {
		t.Fatal(err)
	}
	if got, err := a.Suspended(ctx, id.Flat()); err != nil || got.Suspended {
		t.Fatalf("after Resume: %+v %v", got, err)
	}
	for _, bad := range []string{"", "Acme_team1_ops1", "acme_team1", "a b", "_x_y"} {
		if _, err := a.Suspend(ctx, bad); err == nil {
			t.Errorf("Suspend(%q) accepted", bad)
		}
		if err := a.Resume(ctx, bad); err == nil {
			t.Errorf("Resume(%q) accepted", bad)
		}
		if _, err := a.Suspended(ctx, bad); err == nil {
			t.Errorf("Suspended(%q) accepted", bad)
		}
	}
}

// A recovery (the re-run after a stop that followed the purge) holds the flat key's lock, which is the lock
// ProvisionIdentity takes first, so a provision of the same key cannot interleave: it waits and is then refused as retired.
func TestDeprovisionDrainedRecoveryHoldsNameLock(t *testing.T) {
	quickLocks(t, longWait)
	e, a, js := setup(t)
	ctx := context.Background()
	id, user := provNewForm(t, e, a, "acme", "team1", "ops1")
	sid, seq := drainedFlat(t, a, js, id)
	prev := admin.SetAfterDeprovisionPurge(func() { a.Conn().Close() })
	if err := a.DeprovisionDrained(ctx, id.Flat(), sid, seq); err == nil {
		t.Fatal("the removal cannot have finished on a closed connection")
	}
	admin.SetAfterDeprovisionPurge(prev)
	b, js := initOn(t, e)
	lease, _ := js.KeyValue(ctx, store.LeaseBucket)
	_ = lease.Purge(ctx, admin.LockKey(id.Flat()))
	wantMarker(t, js, id.Flat(), store.RetiredPending, user)

	var once sync.Once
	var provDone = make(chan error, 1)
	var provReturned atomic.Bool
	prevHook := admin.SetAfterLockAcquired(func() {
		once.Do(func() { // the recovery holds the lock now
			go func() {
				c, _ := initOn(t, e)
				_, err := c.ProvisionIdentity(ctx, id, nil, user, e.Account, "", admin.Meta{})
				provReturned.Store(true)
				provDone <- err
			}()
			time.Sleep(700 * time.Millisecond)
			if provReturned.Load() {
				t.Error("a provision of the same key got past the recovery's lock")
			}
			// The marker is still pending while the recovery runs: nothing was finished before the lock was held.
			if m, _ := readMarker(t, js, id.Flat()); m.State != store.RetiredPending {
				t.Errorf("marker %+v changed before the recovery's checks", m)
			}
		})
	})
	t.Cleanup(func() { admin.SetAfterLockAcquired(prevHook) })
	if err := b.DeprovisionDrained(ctx, id.Flat(), sid, seq); err != nil {
		t.Fatalf("recovery: %v", err)
	}
	select {
	case err := <-provDone:
		wantRetired(t, err, id)
	case <-time.After(30 * time.Second):
		t.Fatal("the provision never finished")
	}
	wantMarker(t, js, id.Flat(), store.RetiredFinal, user)
}

// A recovery verifies the stream before it finishes anything: mail stored after the drain, or a suspension that was
// resumed and made again, refuses it and leaves the pending marker, the lease and the inbox as they were.
func TestDeprovisionDrainedRecoveryRefusesChangedStream(t *testing.T) {
	for name, change := range map[string]func(t *testing.T, a *admin.Admin, js jetstream.JetStream, id sap.EndpointIdentity){
		"extra-mail": func(t *testing.T, _ *admin.Admin, js jetstream.JetStream, id sap.EndpointIdentity) {
			// The parked subject is the suspended stream's one subject: this stores one more message.
			if _, err := js.Publish(context.Background(), store.SuspendedSubject(id.Flat()), []byte("late")); err != nil {
				t.Fatal(err)
			}
		},
		"new-suspension": func(t *testing.T, a *admin.Admin, _ jetstream.JetStream, id sap.EndpointIdentity) {
			if err := a.Resume(context.Background(), id.Flat()); err != nil {
				t.Fatal(err)
			}
			if _, err := a.Suspend(context.Background(), id.Flat()); err != nil {
				t.Fatal(err)
			}
		},
	} {
		t.Run(name, func(t *testing.T) {
			e, a, js := setup(t)
			ctx := context.Background()
			id, user := provNewForm(t, e, a, "acme", "team1", "ops1")
			sid, seq := drainedFlat(t, a, js, id)
			prev := admin.SetAfterDeprovisionPurge(func() { a.Conn().Close() })
			if err := a.DeprovisionDrained(ctx, id.Flat(), sid, seq); err == nil {
				t.Fatal("the removal cannot have finished on a closed connection")
			}
			admin.SetAfterDeprovisionPurge(prev)
			b, js := initOn(t, e)
			lease, _ := js.KeyValue(ctx, store.LeaseBucket)
			_ = lease.Purge(ctx, admin.LockKey(id.Flat()))
			change(t, b, js, id)
			if err := b.DeprovisionDrained(ctx, id.Flat(), sid, seq); err == nil {
				t.Fatal("a recovery against a changed stream must refuse")
			}
			wantMarker(t, js, id.Flat(), store.RetiredPending, user)
			if _, err := js.Stream(ctx, sap.InboxStream(id.Flat())); err != nil {
				t.Fatalf("the inbox must survive a refused recovery: %v", err)
			}
			if _, err := lease.Get(ctx, id.Flat()); err == nil {
				t.Fatal("a refused recovery fenced the lease")
			}
		})
	}
}
