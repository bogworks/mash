package policyapply

import (
	"context"
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/nats-io/nats.go/jetstream"

	"github.com/bogworks/mash/internal/listset"
)

func newKVMarkers(t *testing.T, e *kvEnv) *KVProvisionMarkers {
	t.Helper()
	m, err := NewKVProvisionMarkers(e.kv, kvAccount, 0)
	if err != nil {
		t.Fatal(err)
	}
	return m
}

func TestKVProvisionMarkersSurviveNewInstance(t *testing.T) {
	e := newKVEnv(t)
	m1 := newKVMarkers(t, e)
	for _, a := range []string{"c.new", "c.core"} {
		if err := m1.MarkProvisioning(a, 2); err != nil {
			t.Fatal(err)
		}
	}
	if err := m1.MarkProvisioning("c.new", 2); err != nil { // idempotent
		t.Fatal(err)
	}
	got, err := newKVMarkers(t, e).Provisioning(2)
	if err != nil || !slices.Equal(got, []string{"c.core", "c.new"}) {
		t.Fatalf("got %v, %v", got, err)
	}
}

func TestKVProvisionMarkersClearAndRemark(t *testing.T) {
	e := newKVEnv(t)
	m := newKVMarkers(t, e)
	if err := m.ClearProvisioning("nobody", 2); err != nil {
		t.Fatal(err)
	}
	if err := m.MarkProvisioning("c.new", 2); err != nil {
		t.Fatal(err)
	}
	if err := m.ClearProvisioning("c.new", 2); err != nil {
		t.Fatal(err)
	}
	if got, err := newKVMarkers(t, e).Provisioning(2); err != nil || len(got) != 0 {
		t.Fatalf("after clear: %v, %v", got, err)
	}
	if err := m.MarkProvisioning("c.new", 2); err != nil {
		t.Fatal(err)
	}
	if got, _ := m.Provisioning(2); !slices.Equal(got, []string{"c.new"}) {
		t.Fatalf("after re-mark: %v", got)
	}
}

func TestKVProvisionMarkersGenerationIsolation(t *testing.T) {
	e := newKVEnv(t)
	m := newKVMarkers(t, e)
	_ = m.MarkProvisioning("c.new", 2)
	_ = m.MarkProvisioning("c.docs", 12)
	if got, _ := m.Provisioning(2); !slices.Equal(got, []string{"c.new"}) {
		t.Errorf("gen 2: %v", got)
	}
	if got, _ := m.Provisioning(12); !slices.Equal(got, []string{"c.docs"}) {
		t.Errorf("gen 12: %v", got)
	}
	if got, _ := m.Provisioning(1); len(got) != 0 {
		t.Errorf("gen 1: %v", got)
	}
}

func TestMemProvisionMarkers(t *testing.T) {
	m := NewMemProvisionMarkers()
	_ = m.MarkProvisioning("b", 2)
	_ = m.MarkProvisioning("a", 2)
	_ = m.MarkProvisioning("a", 3)
	if got, _ := m.Provisioning(2); !slices.Equal(got, []string{"a", "b"}) {
		t.Errorf("got %v", got)
	}
	_ = m.ClearProvisioning("a", 2)
	if got, _ := m.Provisioning(2); !slices.Equal(got, []string{"b"}) {
		t.Errorf("got %v", got)
	}
}

// RunOrdered through WithProvisionMarkers refuses the rollback while a marker stands.
func TestRunOrderedWithProvisionMarkersRefusesRollback(t *testing.T) {
	for name, mk := range map[string]func(t *testing.T) ProvisionMarkers{
		"mem": func(*testing.T) ProvisionMarkers { return NewMemProvisionMarkers() },
		"kv":  func(t *testing.T) ProvisionMarkers { return newKVMarkers(t, newKVEnv(t)) },
	} {
		t.Run(name, func(t *testing.T) {
			w := newWorld14(t)
			pm := mk(t)
			w.in.Ledger = WithProvisionMarkers(evLedger{w.ledger, w.ev}, pm)
			w.prov.fail("issue:c.new", true)
			if _, err := RunOrdered(context.Background(), w.in, w.prov); err != nil {
				t.Fatal(err)
			}
			if got, _ := pm.Provisioning(2); !slices.Equal(got, []string{"c.new"}) {
				t.Fatalf("markers after first run: %v", got)
			}
			w.prov.fail("issue:c.new", false)
			w.cl.mu.Lock()
			w.cl.jwts["s2"] = w.j1
			w.cl.failPush["s2"] = true
			w.cl.mu.Unlock()
			_, err := RunOrdered(context.Background(), w.in, w.prov)
			if !errors.Is(err, ErrProvisioningMarker) {
				t.Fatalf("err = %v, want ErrProvisioningMarker", err)
			}
			if _, ok, _ := w.ledger.Pending(); !ok {
				t.Error("pending was cleared")
			}
		})
	}
}

// A marker the store cannot read, or one with no address, is not a tombstone: it counts as standing, so Provisioning
// fails rather than reporting fewer markers.
func TestKVProvisionMarkersUnreadableMarkerIsAnError(t *testing.T) {
	for name, val := range map[string]string{"malformed": "not json", "no address": `{}`, "empty address": `{"address":""}`} {
		t.Run(name, func(t *testing.T) {
			e := newKVEnv(t)
			m := newKVMarkers(t, e)
			if err := m.MarkProvisioning("c.new", 2); err != nil {
				t.Fatal(err)
			}
			if _, err := e.kv.Put(context.Background(), m.key("c.other", 2), []byte(val)); err != nil {
				t.Fatal(err)
			}
			if got, err := m.Provisioning(2); err == nil {
				t.Fatalf("got %v, nil; want an error", got)
			}
		})
	}
	e := newKVEnv(t)
	m := newKVMarkers(t, e)
	_, _ = e.kv.Put(context.Background(), m.key("c.gone", 2), []byte(`{"cleared":true}`))
	if got, err := m.Provisioning(2); err != nil || len(got) != 0 {
		t.Errorf("tombstone: %v, %v", got, err)
	}
}

// expiringKV is a bucket whose key lister, like the library's, closes its channel without keys when the context
// expires: a timeout that looks like an empty listing.
type expiringKV struct{ jetstream.KeyValue }

type closedLister struct{ ch chan string }

func (l closedLister) Keys() <-chan string { return l.ch }
func (closedLister) Stop() error           { return nil }

func (expiringKV) ListKeysFiltered(ctx context.Context, _ ...string) (jetstream.KeyLister, error) {
	<-ctx.Done()
	ch := make(chan string)
	close(ch)
	return closedLister{ch}, nil
}

// A listing that ended because the context expired is not "no markers": Provisioning fails, so a rollback is refused.
func TestKVProvisionMarkersExpiredListingIsAnError(t *testing.T) {
	e := newKVEnv(t)
	m := newKVMarkers(t, e)
	if err := m.MarkProvisioning("c.new", 2); err != nil {
		t.Fatal(err)
	}
	const expiry = 20 * time.Millisecond
	slow, err := NewKVProvisionMarkers(expiringKV{e.kv}, kvAccount, expiry)
	if err != nil {
		t.Fatal(err)
	}
	if got, err := slow.Provisioning(2); err == nil {
		t.Fatalf("got %v, nil; want an error for the expired listing", got)
	}
	// A context that is already over when the call starts is an error too.
	const tiny = time.Nanosecond
	over, err := NewKVProvisionMarkers(e.kv, kvAccount, tiny)
	if err != nil {
		t.Fatal(err)
	}
	if got, err := over.Provisioning(2); err == nil {
		t.Fatalf("got %v, nil; want an error for the expired context", got)
	}
}

// The wrappers keep every optional ledger interface Run looks for, and a rollback through them is recorded as one.
func TestProvisionWrappersForwardOptionalLedgerInterfaces(t *testing.T) {
	w := newWorld14(t)
	check := func(t *testing.T, l Ledger) {
		t.Helper()
		if _, ok := l.(PendingJWTLedger); !ok {
			t.Error("not a PendingJWTLedger")
		}
		if _, ok := l.(CommitSizeChecker); !ok {
			t.Error("not a CommitSizeChecker")
		}
		rl, ok := l.(RollbackLedger)
		if !ok {
			t.Fatal("not a RollbackLedger")
		}
		if err := w.ledger.SetPending(listset.Record{Generation: 2}); err != nil {
			t.Fatal(err)
		}
		const rollback = "rollback-jwt"
		if err := rl.CommitRollback(rollback); err != nil {
			t.Fatal(err)
		}
		if got, _ := w.ledger.InForceJWT(); got != rollback {
			t.Errorf("JWT in force = %q, want the rollback JWT", got)
		}
		if _, pending, _ := w.ledger.Pending(); pending {
			t.Error("pending stayed")
		}
	}
	t.Run("WithProvisionMarkers", func(t *testing.T) { check(t, WithProvisionMarkers(w.ledger, NewMemProvisionMarkers())) })
	t.Run("guard", func(t *testing.T) {
		in, _ := withProvisionGuard(RunInput{Ledger: WithProvisionMarkers(w.ledger, NewMemProvisionMarkers())}, nil)
		check(t, in.Ledger)
	})
}
