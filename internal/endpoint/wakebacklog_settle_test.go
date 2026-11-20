package endpoint

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/bogworks/mash/internal/kind"
	"github.com/bogworks/mash/internal/spool"
)

// settle advances the clock and runs the whole settle pass (the idle wake and the backlog wake together), as serve's
// loop does.
func (r *backlogRig) settle(d time.Duration) *backlogRig {
	r.now = r.now.Add(d)
	r.e.settleHeld(func(text string, meta map[string]string) error {
		r.wakes = append(r.wakes, text)
		return nil
	}, r.h)
	return r
}

// oldTask holds a task that has waited long enough for the idle wake.
func (r *backlogRig) oldTask() spool.Ref {
	ref := r.add(kind.Task, 0)
	r.h.at[ref] = time.Now().Add(-time.Hour)
	return ref
}

// A suspected-hookless session (HooklessWakes wakes and no hook run) is not woken again by a backlog, as the idle wake
// is not.
func TestBacklogHonoursTheHooklessStop(t *testing.T) {
	r := newBacklogRig(t, 3, 0)
	r.addN(16, kind.Result, 0)
	r.e.hl.bind(r.sp, time.Now().Add(-time.Hour))
	r.e.hl.fromStart()
	r.e.hl.wakes = HooklessWakes
	if st := r.e.hl.state(time.Time{}); st != HooklessSuspected {
		t.Fatalf("state %v, want suspected", st)
	}
	for range 5 {
		r.settle(wakeCooldown + time.Second)
	}
	r.want(0)
}

// An idle wake and a backlog wake share one last-wake time: the backlog wake is not sent just after an idle wake.
func TestBacklogIsSpacedFromTheIdleWake(t *testing.T) {
	r := newBacklogRig(t, 3, 0)
	r.e.cfg.IdleWake = time.Hour // the idle backoff outlasts the test's clock steps
	for range 5 {
		r.oldTask()
	}
	r.settle(wakeCooldown).want(1) // the idle wake, which stands for the backlog wake of the same pass
	if strings.Contains(r.wakes[0], "waiting, the oldest") {
		t.Fatalf("the first wake is the idle one: %q", r.wakes[0])
	}
	r.settle(250 * time.Millisecond).want(1) // not a backlog wake 250 ms later
	r.settle(wakeCooldown - time.Second).want(1)
	r.settle(2 * time.Second).want(2) // the cooldown from the idle wake has passed
	if !strings.Contains(r.wakes[1], "waiting, the oldest") {
		t.Fatalf("the second wake is the backlog's: %q", r.wakes[1])
	}
	r.settle(250 * time.Millisecond).want(2)
}

// A hook that ran within the spacing window means the session is busy: it is not woken (ADR 0028).
func TestBacklogIsNotSentWhileAHookIsRunning(t *testing.T) {
	r := newBacklogRig(t, 3, 0)
	r.addN(5, kind.Result, 0)
	r.now = r.now.Add(wakeCooldown)
	r.h.hookSeen = r.now
	r.settle(5 * time.Minute).want(0)
	r.settle(5*time.Minute + time.Second).want(1)
}

// The same through a real drain: settleHeld sees the hook's tombstone, and the backlog left held does not wake.
func TestBacklogIsNotSentRightAfterADrain(t *testing.T) {
	r := newBacklogRig(t, 3, 0)
	r.addN(6, kind.Result, 0)
	drained := r.add(kind.Result, 0)
	if err := r.sp.Drained(drained); err != nil {
		t.Fatal(err)
	}
	r.settle(wakeCooldown).want(0) // the drain ran: busy
	if _, ok := r.h.msgs[drained]; ok || r.h.hookSeen.IsZero() {
		t.Fatal("the drain was not settled")
	}
	r.settle(wakeCooldown - time.Second).want(0)
	r.settle(2 * time.Second).want(1)
}

// A session that takes delivery by channel alone has no spool: nothing is held for it, and nothing wakes it.
func TestBacklogNeverWakesAChannelOnlySession(t *testing.T) {
	r := newBacklogRig(t, 1, time.Minute)
	r.addN(20, kind.Task, 2*time.Hour)
	r.e.spool = nil
	r.settle(wakeCooldown).settle(wakeCooldown).want(0)
}

// Refused mail (unverified, dropped and counted) is never held, so it is not waiting and does not count toward the
// backlog; verified mail does.
func TestBacklogDoesNotCountRefusedMail(t *testing.T) {
	var mu sync.Mutex
	now := time.Date(2026, 11, 20, 8, 0, 0, 0, time.UTC)
	old := backlogClock
	backlogClock = func() time.Time { mu.Lock(); defer mu.Unlock(); return now }
	t.Cleanup(func() { backlogClock = old })
	advance := func(d time.Duration) { mu.Lock(); now = now.Add(d); mu.Unlock() }

	f := newFixture(t, "a", "b")
	cfg := f.cfgState("a", t.TempDir(), false)
	cfg.WakeCount = 1
	ctx, cancel := context.WithCancel(context.Background())
	ep, err := Open(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	if err := ep.SetSpool(spool.Spool{Dir: t.TempDir() + "/spool"}, ""); err != nil {
		t.Fatal(err)
	}
	ep.SpoolFromStart()
	ev := &events{}
	done := make(chan struct{})
	go func() { _ = ep.Run(ctx, ev.deliver); close(done) }()
	t.Cleanup(func() { cancel(); <-done; ep.Close() })

	labels := f.publishUnverified(t, "a") // four refused messages
	sendTask(t, f, "one")
	ev.waitFor(t, 1, 5*time.Second) // the ring of the one verified message
	waitRefused(t, cfg, len(labels))
	advance(wakeCooldown + time.Minute)
	time.Sleep(500 * time.Millisecond)
	for _, w := range ev.all() {
		if strings.Contains(w, "waiting, the oldest") {
			t.Fatalf("a backlog wake with one message waiting and %d refused: %q", len(labels), w)
		}
	}
	sendTask(t, f, "two") // two waiting: more than the count of 1
	ev.waitFor(t, 2, 5*time.Second)
	advance(wakeCooldown + time.Minute)
	until(t, "backlog wake", func() bool {
		for _, w := range ev.all() {
			if strings.Contains(w, "2 messages") && strings.Contains(w, "waiting, the oldest") {
				return true
			}
		}
		return false
	})
}

// A backlog wake starts the idle backoff: an idle wake is not sent seconds after it, only once the backoff has passed.
func TestIdleWakeIsSpacedFromTheBacklogWake(t *testing.T) {
	r := newBacklogRig(t, 3, 0)
	r.addN(5, kind.Result, 0)
	r.settle(wakeCooldown).want(1) // the backlog wake: results alone never wake idle
	if !strings.Contains(r.wakes[0], "waiting, the oldest") {
		t.Fatalf("the first wake is the backlog's: %q", r.wakes[0])
	}
	r.oldTask() // waiting mail that has waited long enough for the idle wake
	r.settle(time.Second).want(1)
	r.settle(r.e.idleWake()).want(1) // the backoff after one wake is double the idle wake
	r.settle(r.e.idleWake()).want(2)
	if strings.Contains(r.wakes[1], "waiting, the oldest") {
		t.Fatalf("the second wake is the idle one: %q", r.wakes[1])
	}
}

// Urgent mail is exempt from the spacing: it wakes an idle session at once, after a backlog wake too.
func TestUrgentWakeIsNotSpacedFromTheBacklogWake(t *testing.T) {
	r := newBacklogRig(t, 3, 0)
	r.addN(5, kind.Result, 0)
	r.settle(wakeCooldown).want(1)
	ref := r.add(kind.Task, 0)
	m := r.h.msgs[ref]
	m.mail.Urgent = true
	r.h.msgs[ref] = m
	r.h.at[ref] = time.Now()
	r.h.rung = map[spool.Ref]bool{}
	r.settle(time.Second).want(2)
	if r.e.lastWake != r.now {
		t.Fatal("the urgent wake did not set the shared last-wake time")
	}
}

// A hook that only listed mail in a digest is a hook run: the session is busy and the backlog does not wake it.
func TestBacklogIsNotSentRightAfterADigest(t *testing.T) {
	r := newBacklogRig(t, 3, 0)
	r.addN(5, kind.Result, 0)
	listed := r.add(kind.Result, 0)
	if err := r.sp.Listed(listed); err != nil {
		t.Fatal(err)
	}
	r.settle(wakeCooldown).want(0)
	if r.h.hookSeen.IsZero() {
		t.Fatal("the digest was not taken as a hook run")
	}
	r.settle(wakeCooldown - time.Second).want(0)
	r.settle(2 * time.Second).want(1)
}

// A session proven hookless is not woken by a backlog either.
func TestBacklogHonoursTheProvenHooklessStop(t *testing.T) {
	r := newBacklogRig(t, 3, 0)
	r.addN(5, kind.Result, 0)
	r.now = r.now.Add(wakeCooldown + time.Second)
	r.e.wakeBacklog(func(string, map[string]string) error { r.wakes = append(r.wakes, "x"); return nil }, r.h, HooklessProven)
	r.want(0)
}

// A hook run that listed and drained nothing new still means a busy session: the spool's marker time counts.
func TestBacklogIsNotSentRightAfterAnyHookRun(t *testing.T) {
	r := newBacklogRig(t, 3, 0)
	r.addN(5, kind.Result, 0)
	r.settle(wakeCooldown + 30*time.Second).want(1) // past the start hold: the first backlog wake
	r.wakes = nil
	r.e.lastWake = time.Time{}
	marker := filepath.Join(r.sp.Dir, ".hooked")
	if err := os.WriteFile(marker, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(marker, r.now.Add(-5*time.Second), r.now.Add(-5*time.Second)); err != nil {
		t.Fatal(err)
	}
	r.settle(time.Second).want(0)
	r.settle(wakeCooldown).want(1)
}
