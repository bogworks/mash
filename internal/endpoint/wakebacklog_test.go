package endpoint

import (
	"io"
	"log"
	"strings"
	"testing"
	"time"

	"github.com/bogworks/mash/internal/config"
	"github.com/bogworks/mash/internal/kind"
	"github.com/bogworks/mash/internal/render"
	"github.com/bogworks/mash/internal/spool"
)

// backlogRig drives wakeBacklog with an injected clock: no endpoint, no NATS, no sleeping.
type backlogRig struct {
	t     *testing.T
	e     *Endpoint
	h     *held
	sp    spool.Spool
	now   time.Time
	wakes []string
}

func newBacklogRig(t *testing.T, count int, age time.Duration) *backlogRig {
	t.Helper()
	r := &backlogRig{t: t, now: time.Date(2026, 11, 20, 8, 0, 0, 0, time.UTC)}
	old := backlogClock
	backlogClock = func() time.Time { return r.now }
	t.Cleanup(func() { backlogClock = old })
	r.sp = spool.Spool{Dir: t.TempDir()}
	if err := r.sp.Init(); err != nil {
		t.Fatal(err)
	}
	r.e = r.newEndpoint(count, age)
	r.h = &held{msgs: map[spool.Ref]pending{}, at: map[spool.Ref]time.Time{}, listed: map[spool.Ref]bool{}}
	return r
}

// newEndpoint is an endpoint that has just started serving (a restart makes another).
func (r *backlogRig) newEndpoint(count int, age time.Duration) *Endpoint {
	return &Endpoint{spool: &r.sp, wakeStart: r.now, log: log.New(io.Discard, "", 0),
		cfg: config.Config{Name: "me", Owner: "o", WakeCount: count, WakeAge: age, BatchMsgs: 10, BatchBytes: 16384}}
}

var seqN uint64

// add holds a message that arrived `ago` before now.
func (r *backlogRig) add(k kind.Kind, ago time.Duration) spool.Ref {
	seqN++
	ref := spool.Ref{Seq: seqN}
	r.h.msgs[ref] = pending{mail: render.Mail{Seq: seqN, From: "alice", Kind: k, At: r.now.Add(-ago), Body: []byte("x")}}
	return ref
}

func (r *backlogRig) addN(n int, k kind.Kind, ago time.Duration) {
	for range n {
		r.add(k, ago)
	}
}

func (r *backlogRig) tick(d time.Duration) *backlogRig {
	r.now = r.now.Add(d)
	r.e.wakeBacklog(func(text string, meta map[string]string) error {
		r.wakes = append(r.wakes, text)
		return nil
	}, r.h, HooklessNone)
	return r
}

func (r *backlogRig) want(n int) {
	r.t.Helper()
	if len(r.wakes) != n {
		r.t.Fatalf("wakes = %d, want %d: %q", len(r.wakes), n, r.wakes)
	}
}

func TestBacklogCountWakesOnceAfterTheStartHold(t *testing.T) {
	r := newBacklogRig(t, 15, time.Hour)
	r.addN(16, kind.Result, 0)
	r.tick(0).want(0)
	r.tick(wakeCooldown - time.Second).want(0)
	r.tick(time.Second).want(1)
	if !strings.Contains(r.wakes[0], "16 messages") || !strings.Contains(r.wakes[0], "waiting") {
		t.Fatalf("the notice says how many wait: %q", r.wakes[0])
	}
	r.tick(time.Minute).want(1) // the cooldown
}

func TestBacklogExactlyTheLimitDoesNotWake(t *testing.T) {
	r := newBacklogRig(t, 15, 0)
	r.addN(15, kind.Task, 0)
	r.tick(wakeCooldown).want(0)
	r.tick(time.Hour).want(0)
}

func TestBacklogAgeWakes(t *testing.T) {
	r := newBacklogRig(t, 0, time.Hour)
	r.add(kind.FYI, time.Hour+time.Minute) // arrived 1h01m before the check at +0, so it is older still later
	r.tick(0).want(0)                      // the start hold
	r.tick(wakeCooldown).want(1)
	r2 := newBacklogRig(t, 0, time.Hour)
	r2.add(kind.FYI, 30*time.Minute)
	r2.tick(wakeCooldown).want(0) // 40 minutes old
	r2.tick(21 * time.Minute).want(1)
}

func TestBacklogExactlyTheAgeDoesNotWake(t *testing.T) {
	r := newBacklogRig(t, 0, time.Hour)
	r.now = r.now.Add(wakeCooldown) // past the start hold
	r.add(kind.Result, time.Hour)   // exactly an hour old
	r.tick(0).want(0)
	r.tick(time.Second).want(1)
}

func TestBacklogEachSettingOffIndependently(t *testing.T) {
	r := newBacklogRig(t, 0, 0)
	r.addN(50, kind.Task, 100*time.Hour)
	r.tick(wakeCooldown).want(0) // both off
	r.tick(time.Hour).want(0)

	c := newBacklogRig(t, 5, 0) // age off: the count alone
	c.addN(6, kind.Task, 100*time.Hour)
	c.tick(wakeCooldown).want(1)
	c2 := newBacklogRig(t, 5, 0)
	c2.addN(5, kind.Task, 100*time.Hour)
	c2.tick(wakeCooldown).want(0)

	a := newBacklogRig(t, 0, time.Hour) // count off: the age alone
	a.addN(500, kind.Task, 0)
	a.tick(wakeCooldown).want(0)
	a.tick(time.Hour + time.Second).want(1)
}

func TestBacklogBothOverIsOneWake(t *testing.T) {
	r := newBacklogRig(t, 3, time.Hour)
	r.addN(10, kind.Result, 2*time.Hour)
	r.tick(wakeCooldown).want(1)
}

func TestBacklogCooldownRunsFromTheLastWake(t *testing.T) {
	r := newBacklogRig(t, 2, 0)
	r.addN(3, kind.FYI, 0)
	r.tick(wakeCooldown).want(1)
	r.tick(wakeCooldown - time.Second).want(1)
	r.tick(time.Second).want(2) // still over: again
	r.tick(wakeCooldown - time.Second).want(2)
	r.tick(time.Second).want(3)
}

func TestBacklogDrainDoesNotResetTheCooldownAndUnderLimitDoesNotWake(t *testing.T) {
	r := newBacklogRig(t, 2, 0)
	var refs []spool.Ref
	for range 3 {
		refs = append(refs, r.add(kind.Task, 0))
	}
	r.tick(wakeCooldown).want(1)
	for _, ref := range refs { // a hook shows them all: drained
		delete(r.h.msgs, ref)
	}
	r.tick(time.Hour).want(1) // nothing waits
	r.addN(3, kind.Task, 0)   // a new backlog right after a drain
	r.e.backlogAt = r.now.Add(-time.Minute)
	r.tick(0).want(1) // 1 minute after the last wake: held back
	r.tick(10 * time.Minute).want(2)
}

func TestBacklogCountsListedMailAndNotDrainedMail(t *testing.T) {
	r := newBacklogRig(t, 3, 0)
	listed := r.add(kind.FYI, 0)
	r.h.listed[listed] = true
	r.addN(2, kind.Result, 0)
	r.tick(wakeCooldown).want(0) // 3 waiting, one listed: not more than 3
	r.add(kind.Result, 0)
	r.tick(0).want(1) // 4 waiting
	if !strings.Contains(r.wakes[0], "4 messages") || !strings.Contains(r.wakes[0], "1 already listed") {
		t.Fatalf("%q", r.wakes[0])
	}

	d := newBacklogRig(t, 3, 0)
	var refs []spool.Ref
	for range 5 {
		refs = append(refs, d.add(kind.Task, 0))
	}
	for _, ref := range refs[:3] { // the hook has shown three: tombstoned, not waiting
		if err := d.sp.Drained(ref); err != nil {
			t.Fatal(err)
		}
	}
	d.tick(wakeCooldown).want(0)
}

func TestBacklogFallsBackToTheSpoolTimeAndASentenceOfAge(t *testing.T) {
	r := newBacklogRig(t, 0, time.Hour)
	ref := r.add(kind.Task, 0)
	p := r.h.msgs[ref]
	p.mail.At = time.Time{}
	r.h.msgs[ref] = p
	r.h.at[ref] = r.now.Add(-2 * time.Hour)
	r.tick(wakeCooldown).want(1)
	if !strings.Contains(r.wakes[0], "the oldest for 2h 10m") {
		t.Fatalf("%q", r.wakes[0])
	}
}

func TestBacklogRestartDoesNotCarryTheCooldown(t *testing.T) {
	r := newBacklogRig(t, 1, 0)
	r.addN(2, kind.Task, 0)
	r.tick(wakeCooldown).want(1)
	r.e = r.newEndpoint(1, 0) // a restart: a new endpoint, its own hold, no memory of the wake
	r.tick(0).want(1)
	r.tick(wakeCooldown - time.Second).want(1)
	r.tick(time.Second).want(2)
}
