package endpoint

import (
	"context"
	"errors"
	"io"
	"log"
	"os"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/bogworks/mash/internal/kind"
	"github.com/bogworks/mash/internal/render"
	"github.com/bogworks/mash/internal/sap"
	"github.com/bogworks/mash/internal/spool"
	"github.com/bogworks/mash/internal/store"
	"github.com/nats-io/nats.go/jetstream"
)

// lockedBuf is a log sink a test reads while serve writes.
type lockedBuf struct {
	mu sync.Mutex
	b  strings.Builder
}

func (l *lockedBuf) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.Write(p)
}

func (l *lockedBuf) String() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.String()
}

// runSpooledWith is runSpooled with the deliverer and the log sink the test chooses.
func (f *fixture) runSpooledWith(t *testing.T, name string, deliver Deliverer, logw io.Writer) (*Endpoint, spool.Spool) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	ep, err := Open(ctx, f.cfg(name))
	if err != nil {
		t.Fatal(err)
	}
	if logw != nil {
		ep.log = log.New(logw, "", 0)
	}
	sp := spool.Spool{Dir: t.TempDir() + "/spool"}
	if err := ep.SetSpool(sp, ""); err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	go func() { _ = ep.Run(ctx, deliver); close(done) }()
	t.Cleanup(func() { cancel(); <-done; ep.Close() })
	return ep, sp
}

// ackFake is a held message whose plain Ack always "works" but whose confirmed ack fails.
type ackFake struct {
	jetstream.Msg
	acks, doubles int
	fail          bool
}

func (a *ackFake) Ack() error { a.acks++; return nil }
func (a *ackFake) DoubleAck(context.Context) error {
	a.doubles++
	if a.fail {
		return errors.New("no confirmation")
	}
	return nil
}

// Review item 1: the tombstone goes only on a server-confirmed ack; a fire-and-forget Ack that is lost would
// otherwise leave the message unacked in NATS with no tombstone, shown again by the next hook.
func TestAckKeepsTheTombstoneUntilConfirmed(t *testing.T) {
	sp := spool.Spool{Dir: t.TempDir()}
	if err := sp.Init(); err != nil {
		t.Fatal(err)
	}
	e := &Endpoint{spool: &sp, log: log.New(io.Discard, "", 0)}
	if err := sp.Drained(7); err != nil {
		t.Fatal(err)
	}
	m := &ackFake{fail: true}
	e.ack(pending{mail: render.Mail{Seq: 7}, msg: m})
	if !sp.IsDrained(7) {
		t.Fatalf("tombstone removed though the ack was not confirmed (acks=%d doubles=%d)", m.acks, m.doubles)
	}
	m.fail = false
	e.ack(pending{mail: render.Mail{Seq: 7}, msg: m})
	if sp.IsDrained(7) {
		t.Fatal("tombstone kept after a confirmed ack")
	}
}

// Review item 1, through a real outage: the hook drains while NATS is down, so serve's ack cannot be confirmed; once
// NATS is back the message ends acked (a redelivery is acked via its tombstone), not spooled again, not shown again.
func TestHookAckLostToANatsRestartIsRedeliveredAndAcked(t *testing.T) {
	f := newFixture(t, "a", "b")
	_, ev, sp := f.runSpooled(t, "a")
	cc := store.ConsumerConfig()
	cc.AckWait = 2 * time.Second
	ajs, err := jetstream.New(f.e.Conn(t, f.e.AdminCreds)) // the endpoint's own user may not change its consumer
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ajs.UpdateConsumer(context.Background(), sap.InboxStream("a"), cc); err != nil {
		t.Fatal(err)
	}
	if err := sp.Touch(); err != nil {
		t.Fatal(err)
	}
	if _, err := f.client(t, "b").SendWith(context.Background(), "a", "once", SendOptions{Kind: kind.Question}); err != nil {
		t.Fatal(err)
	}
	until(t, "spooled", func() bool { return len(waiting(t, sp)) == 1 })
	f.e.Shutdown()
	for _, e := range waiting(t, sp) { // the hook shows it while NATS is down
		if err := sp.Drained(e.Seq); err != nil {
			t.Fatal(err)
		}
	}
	time.Sleep(4 * SpoolCheck) // serve tries to ack
	f.e.Restart(t)
	for deadline := time.Now().Add(20 * time.Second); ; time.Sleep(50 * time.Millisecond) {
		c, err := ajs.Consumer(context.Background(), sap.InboxStream("a"), store.Consumer)
		if err == nil {
			if i, err := c.Info(context.Background()); err == nil && i.NumPending+uint64(i.NumAckPending) == 0 {
				break
			}
		}
		if time.Now().After(deadline) {
			t.Fatal("timed out: the message is acked in the end")
		}
	}
	// The ack may have been applied after the restart with only its confirmation lost, leaving a harmless tombstone
	// no redelivery will clear; what matters is the message is acked and never spooled or shown again.
	time.Sleep(3 * SpoolCheck)
	if w := waiting(t, sp); len(w) != 0 {
		t.Fatalf("spooled again: %+v", w)
	}
	if got := ev.all(); len(got) != 0 {
		t.Fatalf("channel events %q", got)
	}
}

// Review item 3: held mail no hook shows wakes the session again, with backoff: 2x IdleWake after the previous wake,
// then 4x, and so on; a drain resets it.
func TestHookReWakesWithBackoff(t *testing.T) {
	oi, oc := IdleWake, SpoolCheck
	IdleWake, SpoolCheck = 200*time.Millisecond, 20*time.Millisecond
	t.Cleanup(func() { IdleWake, SpoolCheck = oi, oc })
	f := newFixture(t, "a", "b")
	var mu sync.Mutex
	var wakes []time.Time
	deliver := func(string, map[string]string) error { // the wake lands no turn
		mu.Lock()
		defer mu.Unlock()
		wakes = append(wakes, time.Now())
		return nil
	}
	count := func() int { mu.Lock(); defer mu.Unlock(); return len(wakes) }
	_, sp := f.runSpooledWith(t, "a", deliver, nil)
	if err := sp.Touch(); err != nil {
		t.Fatal(err)
	}
	if _, err := f.client(t, "b").SendWith(context.Background(), "a", "held", SendOptions{Kind: kind.Question}); err != nil {
		t.Fatal(err)
	}
	until(t, "spooled", func() bool { return len(waiting(t, sp)) == 1 })
	for deadline := time.Now().Add(8 * time.Second); count() < 4; time.Sleep(20 * time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatalf("only %d wakes", count())
		}
	}
	mu.Lock()
	for i, want := range []time.Duration{2, 4, 8} { // gaps after the first wake: 2x, 4x, 8x IdleWake
		gap, min := wakes[i+1].Sub(wakes[i]), want*IdleWake
		if gap < min-30*time.Millisecond || gap > min+400*time.Millisecond {
			t.Errorf("gap %d is %s, want about %s", i+1, gap, min)
		}
	}
	mu.Unlock()
	for _, e := range waiting(t, sp) {
		if err := sp.Drained(e.Seq); err != nil {
			t.Fatal(err)
		}
	}
	time.Sleep(6 * SpoolCheck)
	n := count()
	time.Sleep(3 * IdleWake)
	if count() != n {
		t.Fatalf("woke after a drain with nothing held")
	}
	if _, err := f.client(t, "b").SendWith(context.Background(), "a", "next spell", SendOptions{Kind: kind.Question}); err != nil {
		t.Fatal(err)
	}
	until(t, "the next idle spell wakes again", func() bool { return count() == n+1 })
	mu.Lock()
	if gap := wakes[n].Sub(wakes[n-1]); gap < 3*IdleWake {
		t.Errorf("gap to the next spell's wake %s", gap)
	}
	mu.Unlock()
}

func TestWakeBackoffDoublesToTheCap(t *testing.T) {
	for _, c := range []struct {
		base time.Duration
		want []time.Duration
	}{
		{5 * time.Minute, []time.Duration{5 * time.Minute, 10 * time.Minute, 20 * time.Minute, 30 * time.Minute, 30 * time.Minute, 30 * time.Minute}},
		{45 * time.Second, []time.Duration{45 * time.Second, 90 * time.Second, 3 * time.Minute, 6 * time.Minute, 12 * time.Minute, 24 * time.Minute, 30 * time.Minute, 30 * time.Minute}},
	} {
		for i, want := range c.want {
			if got := wakeBackoff(c.base, i); got != want {
				t.Errorf("wakeBackoff(%s, %d) = %s, want %s", c.base, i, got, want)
			}
		}
		if got := wakeBackoff(c.base, 99); got != 30*time.Minute {
			t.Errorf("wakeBackoff(%s, 99) = %s", c.base, got)
		}
	}
}

// A busy session (a hook drains the spool every ~100 ms) never idles for the configured wake, so mail over several wake
// periods costs no channel event: all of it is shown through the spool.
func TestBusySessionGetsNoWake(t *testing.T) {
	f := newFixture(t, "a", "b")
	f.idle = 300 * time.Millisecond
	var mu sync.Mutex
	var wakes []string
	deliver := func(text string, _ map[string]string) error {
		mu.Lock()
		defer mu.Unlock()
		wakes = append(wakes, text)
		return nil
	}
	_, sp := f.runSpooledWith(t, "a", deliver, nil)
	if err := sp.Touch(); err != nil {
		t.Fatal(err)
	}
	b := f.client(t, "b")
	seen := map[uint64]bool{}
	drain := func() {
		for _, e := range waiting(t, sp) {
			seen[e.Seq] = true
			if err := sp.Drained(e.Seq); err != nil {
				t.Fatal(err)
			}
		}
	}
	const n = 12 // 12 x 100 ms = 4 idle-wake periods
	for i := 0; i < n; i++ {
		if _, err := b.Send(context.Background(), "a", "tick "+strconv.Itoa(i), 0); err != nil {
			t.Fatal(err)
		}
		time.Sleep(100 * time.Millisecond)
		drain()
	}
	until(t, "every message drained via the spool", func() bool { drain(); return len(seen) == n })
	time.Sleep(f.idle + 200*time.Millisecond)
	mu.Lock()
	defer mu.Unlock()
	if len(wakes) != 0 {
		t.Fatalf("a busy session was woken: %q", wakes)
	}
}

// The converse: an idle session (no hook) is woken exactly once after the configured wake, and not before.
func TestIdleSessionWakesOnceAfterTheConfiguredWake(t *testing.T) {
	f := newFixture(t, "a", "b")
	f.idle = 300 * time.Millisecond
	var mu sync.Mutex
	var wakes []time.Time
	deliver := func(string, map[string]string) error {
		mu.Lock()
		defer mu.Unlock()
		wakes = append(wakes, time.Now())
		return nil
	}
	count := func() int { mu.Lock(); defer mu.Unlock(); return len(wakes) }
	_, sp := f.runSpooledWith(t, "a", deliver, nil)
	if err := sp.Touch(); err != nil {
		t.Fatal(err)
	}
	sent := time.Now()
	if _, err := f.client(t, "b").SendWith(context.Background(), "a", "held", SendOptions{Kind: kind.Question}); err != nil {
		t.Fatal(err)
	}
	until(t, "spooled", func() bool { return len(waiting(t, sp)) == 1 })
	time.Sleep(f.idle / 2)
	if count() != 0 {
		t.Fatal("woke before the idle wake")
	}
	until(t, "the wake", func() bool { return count() == 1 })
	mu.Lock()
	first := wakes[0]
	mu.Unlock()
	if first.Sub(sent) < f.idle {
		t.Fatalf("woke after %s, before the idle wake %s", first.Sub(sent), f.idle)
	}
	time.Sleep(f.idle) // the re-wake is 2x the base away
	if count() != 1 {
		t.Fatalf("%d wakes, want exactly one", count())
	}
}

// Review item 6: a spool that cannot be written must not spin at 500 ms forever: after 3 failures the message is
// delivered by the channel, as before any hook, shown and acked; the spool failure is logged once.
func TestHookUnwritableSpoolFallsBackToTheChannel(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root writes through a 0500 directory")
	}
	f := newFixture(t, "a", "b")
	ev := &events{}
	lb := &lockedBuf{}
	a, sp := f.runSpooledWith(t, "a", ev.deliver, lb)
	if err := sp.Touch(); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(sp.Dir, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(sp.Dir, 0o700) })
	b := f.client(t, "b")
	for _, text := range []string{"first", "second"} {
		if _, err := b.Send(context.Background(), "a", text, 0); err != nil {
			t.Fatal(err)
		}
	}
	until(t, "both arrive on the channel", func() bool { return strings.Count(strings.Join(ev.all(), "\n"), "> ") >= 2 })
	until(t, "both acked", func() bool { return unread(t, a) == 0 })
	time.Sleep(2 * time.Second)
	if got := strings.Join(ev.all(), "\n"); strings.Count(got, "> first") != 1 || strings.Count(got, "> second") != 1 {
		t.Fatalf("each message exactly once on the channel: %q", got)
	}
	if n := strings.Count(lb.String(), "spool #"); n != 1 {
		t.Fatalf("%d spool-failure lines, want 1:\n%s", n, lb.String())
	}
}

// Review item 12: a held message whose file is missing or unparsable is rewritten from memory, so the next hook
// shows it; one a hook has drained is never rewritten.
func TestHookSpoolFileSelfHeals(t *testing.T) {
	for _, mode := range []string{"truncated", "deleted"} {
		t.Run(mode, func(t *testing.T) {
			f := newFixture(t, "a", "b")
			a, _, sp := f.runSpooled(t, "a")
			if err := sp.Touch(); err != nil {
				t.Fatal(err)
			}
			if _, err := f.client(t, "b").Send(context.Background(), "a", "keep me", 0); err != nil {
				t.Fatal(err)
			}
			until(t, "spooled", func() bool { return len(waiting(t, sp)) == 1 })
			seq := waiting(t, sp)[0].Seq
			p := sp.Dir + "/" + strconv.FormatUint(seq, 10) + ".json"
			var err error
			if mode == "truncated" {
				err = os.WriteFile(p, []byte(`{"seq": 1, "bo`), 0o600)
			} else {
				err = os.Remove(p)
			}
			if err != nil {
				t.Fatal(err)
			}
			until(t, "rewritten", func() bool { w := waiting(t, sp); return len(w) == 1 && string(w[0].Body) == "keep me" })
			if err := sp.Drained(seq); err != nil { // the hook shows it
				t.Fatal(err)
			}
			until(t, "acked", func() bool { return unread(t, a) == 0 })
			time.Sleep(3 * SpoolCheck)
			if w := waiting(t, sp); len(w) != 0 {
				t.Fatalf("a drained message came back: %+v", w)
			}
		})
	}
}
