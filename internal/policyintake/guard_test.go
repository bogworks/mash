package policyintake_test

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/bogworks/mash/internal/policyintake"
	"github.com/bogworks/mash/internal/store"
)

// H1: a next submitter that is a key mash holds is refused before the apply, whatever class of key it is.
func TestHeldNextSubmitterIsRefusedBeforeApply(t *testing.T) {
	r := newRig(t, true)
	classes := []string{newUser(t).pub, newUser(t).pub, newUser(t).pub, newUser(t).pub, newUser(t).pub} // operator, system, account, zone, admin
	r.heldFn = func() ([]string, error) { return classes, nil }
	r.start(r.intake())
	for i, k := range classes {
		p := r.doc(uint64(i+1), 0)
		p.NextSubmitter = k
		r.put(sign(t, r.submitter, p))
		// The bucket keeps one revision per key: a put that lands before the intake has read the previous one replaces
		// it, so each document waits for its own refusal and a slow runner cannot skip one.
		r.waitRefused(i + 1)
	}
	rs := r.waitRefused(len(classes))
	for _, x := range rs {
		if x.Stage != policyintake.StageDocument || x.Detail != "next_submitter is a key mash holds" {
			t.Fatalf("%+v", x)
		}
	}
	r.settle(0, len(classes))
	if r.current().NextSubmitter != "" {
		t.Fatal("a next was recorded")
	}
}

// A key store that cannot be read refuses the rotation before the apply (fail closed).
func TestUnreadableKeyStoreRefusesBeforeApply(t *testing.T) {
	r := newRig(t, true)
	r.heldFn = func() ([]string, error) { return nil, errors.New("key store unreadable") }
	r.start(r.intake())
	p := r.doc(1, 0)
	p.NextSubmitter = newUser(t).pub
	r.put(sign(t, r.submitter, p))
	rs := r.waitRefused(1)
	if rs[0].Stage != policyintake.StageDocument || !strings.Contains(rs[0].Detail, "next_submitter") {
		t.Fatalf("%+v", rs[0])
	}
	r.settle(0, 1)
}

// A fresh key passes the check; the same check at the marker write refuses a key that became held after the apply
// (nothing is written), and an unreadable key store at the write is retried and writes nothing meanwhile.
func TestHeldCheckAtTheMarkerWrite(t *testing.T) {
	t.Run("became held", func(t *testing.T) {
		r := newRig(t, true)
		next := newUser(t)
		var mu sync.Mutex
		calls := 0
		r.heldFn = func() ([]string, error) {
			mu.Lock()
			defer mu.Unlock()
			calls++
			if calls >= 2 {
				return []string{next.pub}, nil
			}
			return nil, nil
		}
		r.start(r.intake())
		p := r.doc(1, 0)
		p.NextSubmitter = next.pub
		r.put(sign(t, r.submitter, p))
		r.waitApplied(1)
		eventually(t, "abandoned", func() bool { return strings.Contains(r.logbuf.String(), "abandoned") })
		if r.current().NextSubmitter != "" {
			t.Fatal("a held key was recorded")
		}
	})
	t.Run("unreadable then readable", func(t *testing.T) {
		r := newRig(t, true)
		next := newUser(t)
		var mu sync.Mutex
		calls := 0
		r.heldFn = func() ([]string, error) {
			mu.Lock()
			defer mu.Unlock()
			calls++
			if calls == 2 || calls == 3 {
				return nil, errors.New("key store unreadable")
			}
			return nil, nil
		}
		r.start(r.intake())
		p := r.doc(1, 0)
		p.NextSubmitter = next.pub
		r.put(sign(t, r.submitter, p))
		eventually(t, "next recorded", func() bool { return r.current().NextSubmitter == next.pub })
		mu.Lock()
		defer mu.Unlock()
		if calls < 4 {
			t.Fatalf("the key store was read %d times", calls)
		}
	})
}

// stepApplier calls Guard.Before at three steps, running between hooks; it stops at the first refusal.
type stepApplier struct {
	mu      sync.Mutex
	reached []string
	errs    []error
	between func(call int, step string) // runs before Before at each step; call counts Apply calls
	calls   int
	docs    int
}

func (s *stepApplier) Apply(ctx context.Context, d policyintake.Doc, g policyintake.Guard) (policyintake.Result, error) {
	s.mu.Lock()
	s.calls++
	call := s.calls
	s.mu.Unlock()
	for _, step := range []string{"push", "revoke", "commit"} {
		if s.between != nil {
			s.between(call, step)
		}
		if err := g.Before(ctx, step); err != nil {
			s.mu.Lock()
			s.errs = append(s.errs, err)
			s.mu.Unlock()
			return policyintake.Result{}, err
		}
		s.mu.Lock()
		s.reached = append(s.reached, step)
		s.mu.Unlock()
	}
	s.mu.Lock()
	s.docs++
	s.mu.Unlock()
	return policyintake.Result{StatusWritten: true}, nil
}

func (s *stepApplier) snapshot() (reached []string, errs int, docs int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.reached...), len(s.errs), s.docs
}

// lostAfterLock is a lock whose held check fails once lost is set; a new Lock call clears it.
type lostAfterLock struct {
	mu   sync.Mutex
	lost bool
}

func (l *lostAfterLock) setLost() { l.mu.Lock(); l.lost = true; l.mu.Unlock() }

func (l *lostAfterLock) Lock(context.Context) (policyintake.Release, func(context.Context) error, error) {
	l.mu.Lock()
	l.lost = false
	l.mu.Unlock()
	return func() {}, func(context.Context) error {
		l.mu.Lock()
		defer l.mu.Unlock()
		if l.lost {
			return errors.New("the lock was lost")
		}
		return nil
	}, nil
}

// H2: the lock is lost before the second step: the apply aborts, the third step is never reached, the revision is
// processed again from the top and the old signer's document is refused (a recovery adopt replaced the signer).
func TestGuardLockLostDuringApplyAborts(t *testing.T) {
	r := newRig(t, true)
	rec := newUser(t)
	lk := &lostAfterLock{}
	sa := &stepApplier{}
	sa.between = func(call int, step string) {
		if call == 1 && step == "revoke" {
			lk.setLost()
			if err := r.recoveryAdopt(rec); err != nil {
				t.Error(err)
			}
		}
	}
	in := r.intake()
	in.Lock, in.Apply = lk, sa
	r.start(in)
	r.put(sign(t, r.submitter, r.doc(1, 0)))
	rs := r.waitRefused(1)
	if rs[0].Stage != policyintake.StageSignature {
		t.Fatalf("%+v", rs[0])
	}
	reached, errs, docs := sa.snapshot()
	if len(reached) != 1 || reached[0] != "push" || errs != 1 || docs != 0 {
		t.Fatalf("reached %v, errors %d, completed %d", reached, errs, docs)
	}
	log := r.logbuf.String()
	if !strings.Contains(log, "stopped before revoke") || !strings.Contains(log, "processed again") {
		t.Errorf("the step was not logged: %s", log)
	}
	r.settle(0, 1)
}

// H2: the marker moved before the second step: same.
func TestGuardMarkerMovedDuringApplyAborts(t *testing.T) {
	r := newRig(t, true)
	rec := newUser(t)
	sa := &stepApplier{}
	sa.between = func(call int, step string) {
		if call == 1 && step == "revoke" {
			if err := r.recoveryAdopt(rec); err != nil {
				t.Error(err)
			}
		}
	}
	in := r.intake()
	in.Lock, in.Apply = neverLock{}, sa
	r.start(in)
	r.put(sign(t, r.submitter, r.doc(1, 0)))
	rs := r.waitRefused(1)
	if rs[0].Stage != policyintake.StageSignature {
		t.Fatalf("%+v", rs[0])
	}
	reached, errs, docs := sa.snapshot()
	if len(reached) != 1 || errs != 1 || docs != 0 {
		t.Fatalf("reached %v, errors %d, completed %d", reached, errs, docs)
	}
	r.settle(0, 1)
}

// H2: a healthy lock and an unmoved marker pass every step.
func TestGuardHealthyPassesEveryStep(t *testing.T) {
	r := newRig(t, true)
	sa := &stepApplier{}
	in := r.intake()
	in.Apply = sa
	r.start(in)
	r.put(sign(t, r.submitter, r.doc(1, 0)))
	eventually(t, "completed", func() bool { _, _, d := sa.snapshot(); return d == 1 })
	reached, errs, _ := sa.snapshot()
	if strings.Join(reached, ",") != "push,revoke,commit" || errs != 0 {
		t.Fatalf("reached %v errors %d", reached, errs)
	}
	if strings.Contains(r.logbuf.String(), "stopped before") {
		t.Error("a healthy apply logged a stop")
	}
	r.settle(0, 0)
}

// blockingMarker blocks Current until its context ends while block is set.
type blockingMarker struct {
	policyintake.MarkerStore
	mu    sync.Mutex
	block bool
}

func (b *blockingMarker) setBlock(v bool) { b.mu.Lock(); b.block = v; b.mu.Unlock() }

func (b *blockingMarker) Current(ctx context.Context) (store.Marker, uint64, bool, error) {
	b.mu.Lock()
	blocked := b.block
	b.mu.Unlock()
	if _, bounded := ctx.Deadline(); blocked && bounded { // only the guard's bounded calls block
		<-ctx.Done()
		return store.Marker{}, 0, false, ctx.Err()
	}
	return b.MarkerStore.Current(ctx)
}

// Guard.Before never blocks beyond the guard timeout, and a refusal for that reason aborts the apply.
func TestGuardBeforeIsBoundedByTheTimeout(t *testing.T) {
	r := newRig(t, true)
	bm := &blockingMarker{MarkerStore: r.marker}
	sa := &stepApplier{}
	var took time.Duration
	sa.between = func(call int, step string) {
		if call == 1 && step == "push" {
			bm.setBlock(true)
		}
	}
	in := r.intake()
	in.Marker, in.Apply, in.GuardTimeout = bm, sa, 100*time.Millisecond
	in.Lock = neverLock{}
	r.start(in)
	stepStart := time.Now()
	r.put(sign(t, r.submitter, r.doc(1, 0)))
	eventually(t, "the guard refused", func() bool { _, e, _ := sa.snapshot(); return e == 1 })
	took = time.Since(stepStart)
	bm.setBlock(false) // the revision is processed again and completes
	eventually(t, "completed after the reprocess", func() bool { _, _, d := sa.snapshot(); return d == 1 })
	if took > 5*time.Second {
		t.Errorf("the guard took %s", took)
	}
}
