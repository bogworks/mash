package policyapply

import (
	"context"
	"errors"
	"slices"
	"sort"
	"strconv"
	"sync"
	"testing"
)

// markerLedger is a ledger that keeps provisioning markers.
type markerLedger struct {
	evLedger
	mu    *sync.Mutex
	marks map[string]bool // "address@generation"
}

func newMarkerLedger(l evLedger) markerLedger {
	return markerLedger{evLedger: l, mu: &sync.Mutex{}, marks: map[string]bool{}}
}

func markKey(a string, g uint64) string { return a + "@" + strconv.FormatUint(g, 10) }

func (l markerLedger) MarkProvisioning(a string, g uint64) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.marks[markKey(a, g)] = true
	return nil
}

func (l markerLedger) Provisioning(g uint64) ([]string, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	var out []string
	for _, a := range []string{"foo.c.new", "foo.c.core", "foo.c.docs"} {
		if l.marks[markKey(a, g)] {
			out = append(out, a)
		}
	}
	sort.Strings(out)
	return out, nil
}

func (l markerLedger) ClearProvisioning(a string, g uint64) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	delete(l.marks, markKey(a, g))
	return nil
}

func (l markerLedger) count() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return len(l.marks)
}

// removerProv is a Provisioner that can remove an inbox.
type removerProv struct {
	*recProv
	removed []string
}

func (p *removerProv) RemoveInbox(_ context.Context, a string) error {
	p.ev.add("remove:" + a)
	p.removed = append(p.removed, a)
	return nil
}

// provisionedThenGateFails: the first run creates the inbox and fails to issue the credential, so the generation is
// pending; the second run finds the cluster split, which would roll the target back.
func provisionedThenGateFails(t *testing.T, prov Provisioner, rp *recProv, w *world14) (markerLedger, OrderedStatus, error) {
	t.Helper()
	ml := newMarkerLedger(evLedger{w.ledger, w.ev})
	w.in.Ledger = ml
	rp.fail("issue:foo.c.new", true)
	if _, err := RunOrdered(context.Background(), w.in, prov); err != nil {
		t.Fatal(err)
	}
	if _, ok, _ := w.ledger.Pending(); !ok || ml.count() != 1 {
		t.Fatalf("after the first run: pending %v, markers %d", ok, ml.count())
	}
	rp.fail("issue:foo.c.new", false)
	// One server went back to the previous generation and refuses the push: the cluster is split, so the run rolls back.
	w.cl.mu.Lock()
	w.cl.jwts["s2"] = w.j1
	w.cl.failPush["s2"] = true
	w.cl.mu.Unlock()
	os, err := RunOrdered(context.Background(), w.in, prov)
	return ml, os, err
}

// A rollback is refused while a provisioning marker stands: the generation stays pending, nothing is rolled back
// and the run fails, so the new inbox is never exposed to the sender the target refuses.
func TestRunOrderedRefusesRollbackWhileMarkerStands(t *testing.T) {
	w := newWorld14(t)
	ml, os, err := provisionedThenGateFails(t, w.prov, w.prov, w)
	if !errors.Is(err, ErrProvisioningMarker) {
		t.Fatalf("err = %v, want ErrProvisioningMarker (status %+v)", err, os.Status)
	}
	if pend, ok, _ := w.ledger.Pending(); !ok || pend.Generation != 2 {
		t.Errorf("pending = %+v %v: the generation must stay pending", pend, ok)
	}
	if ml.count() != 1 {
		t.Errorf("markers = %d, want the marker to stand", ml.count())
	}
	if rec, _ := w.ledger.InForce(); rec.Generation != 1 {
		t.Errorf("in force = %d", rec.Generation)
	}
	// The target stayed on the servers: the rollback JWT was not pushed.
	w.cl.mu.Lock()
	defer w.cl.mu.Unlock()
	if w.cl.jwts["s1"] == w.j1 {
		t.Error("s1 holds the previous generation: it was rolled back")
	}
}

// When the Provisioner removes the inbox, the marker is cleared and the rollback goes ahead.
func TestRunOrderedRollbackAfterInboxRemoved(t *testing.T) {
	w := newWorld14(t)
	rp := &removerProv{recProv: w.prov}
	ml, _, err := provisionedThenGateFails(t, rp, w.prov, w)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(rp.removed, []string{"foo.c.new"}) || ml.count() != 0 {
		t.Errorf("removed %v, markers %d", rp.removed, ml.count())
	}
	if _, ok, _ := w.ledger.Pending(); ok {
		t.Error("pending stayed after a rollback")
	}
}

// A committed generation clears its markers.
func TestRunOrderedClearsMarkersOnCommit(t *testing.T) {
	w := newWorld14(t)
	ml := newMarkerLedger(evLedger{w.ledger, w.ev})
	w.in.Ledger = ml
	os, err := RunOrdered(context.Background(), w.in, w.prov)
	if err != nil || os.Status.State != StateApplied {
		t.Fatalf("%+v %v", os.Status, err)
	}
	if ml.count() != 0 {
		t.Errorf("markers = %d after the commit", ml.count())
	}
}

// RunOrdered itself refuses a Provisioner with a ledger that keeps no provisioning markers, before anything is pushed.
func TestRunOrderedRefusesLedgerWithoutProvisionMarkers(t *testing.T) {
	w := newWorld14(t)
	w.in.Ledger = evLedger{w.ledger, w.ev}
	_, err := RunOrdered(context.Background(), w.in, w.prov)
	if !errors.Is(err, ErrNoProvisionMarkers) {
		t.Fatalf("err = %v, want ErrNoProvisionMarkers", err)
	}
	if got := w.ev.get(); len(got) != 0 {
		t.Errorf("events before the refusal: %v", got)
	}
}
