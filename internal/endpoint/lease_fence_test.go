package endpoint

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/nats-io/nats.go/jetstream"

	"github.com/bogworks/mash/internal/spool"
)

// Codex H1 on #363: after a same-session takeover the earlier serve must not deliver or ack in the window before its
// watch of the lease notices. The ack-time fence reads the lease key live; the new serve waits a grace before it pulls.

// setLiveGet replaces the fence's direct read of the lease key, safely against the goroutines that make it.
func setLiveGet(ep *Endpoint, fn func(ctx context.Context, key string) (jetstream.KeyValueEntry, error)) {
	ep.live.mu.Lock()
	ep.live.liveGetSet(fn)
	ep.live.mu.Unlock()
}

func (l *liveLease) liveGetSet(fn func(ctx context.Context, key string) (jetstream.KeyValueEntry, error)) {
	l.get = fn
	l.res = [2]leaseVerdict{} // forget what was confirmed
}

func setVar[T any](t *testing.T, p *T, v T) {
	t.Helper()
	old := *p
	*p = v
	t.Cleanup(func() { *p = old })
}

// (a) With a watch that cannot notice for an hour, the earlier serve still delivers nothing and acks nothing of mail
// that arrives after the takeover, and the new serve receives each message exactly once.
func TestFenceEarlierServeDeliversAndAcksNothingBeforeItsWatchNotices(t *testing.T) {
	bigRefresh(t)
	setVar(t, &leaseWatchEvery, time.Hour)
	setVar(t, &takeoverGrace, func() time.Duration { return 300 * time.Millisecond })
	setSession(t, ours(t))
	stubHolderGone(t, func(string, string) bool { return false })
	f := newFixture(t, "a", "snd")
	first, oldEv := f.run(t, "a")
	second, err := Open(context.Background(), f.cfg("a"))
	if err != nil {
		t.Fatal(err)
	}
	defer second.Close()
	if first.leaseGone.Load() {
		t.Fatal("the watch must not have noticed (it polls hourly)")
	}
	snd := f.client(t, "snd")
	const n = 3
	for i := 0; i < n; i++ {
		if _, err := snd.Send(context.Background(), "a", fmt.Sprintf("m-%d", i), 0); err != nil {
			t.Fatal(err)
		}
	}
	until(t, "the earlier serve's fence marks the lease lost", func() bool { return first.leaseGone.Load() })
	time.Sleep(200 * time.Millisecond)
	if got := oldEv.all(); len(got) != 0 {
		t.Fatalf("the earlier serve delivered %q", got)
	}
	floor, pend, ackp := consumerState(t, second, "a")
	if floor != 0 || pend+ackp != n {
		t.Fatalf("ack floor %d, pending %d, ack pending %d: the earlier serve acked mail", floor, pend, ackp)
	}

	ev2 := &events{}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = second.Run(ctx, ev2.deliver) }()
	until(t, "the new serve is delivered all of it", func() bool { return strings.Count(strings.Join(ev2.all(), "\n"), "m-") >= n })
	until(t, "and acks it", func() bool { return unread(t, second) == 0 })
	all := strings.Join(ev2.all(), "\n")
	for i := 0; i < n; i++ {
		if c := strings.Count(all, fmt.Sprintf("m-%d", i)); c != 1 {
			t.Fatalf("m-%d delivered %d times to the new serve", i, c)
		}
	}
}

// (a, held) The same window for mail held for a hook: a hook drains it on the earlier serve after the takeover, and the
// ack that follows is refused by the live check, not by the watch.
func TestFenceEarlierServeAckOfDrainedMailRefusedBeforeItsWatchNotices(t *testing.T) {
	bigRefresh(t)
	setVar(t, &leaseWatchEvery, time.Hour)
	setVar(t, &takeoverGrace, func() time.Duration { return 300 * time.Millisecond })
	setSession(t, ours(t))
	stubHolderGone(t, func(string, string) bool { return false })
	f := newFixture(t, "a", "snd")
	first, _, sp := f.runSpooled(t, "a")
	if err := sp.Touch(); err != nil {
		t.Fatal(err)
	}
	snd := f.client(t, "snd")
	const n = 2
	for i := 0; i < n; i++ {
		if _, err := snd.Send(context.Background(), "a", fmt.Sprintf("h-%d", i), 0); err != nil {
			t.Fatal(err)
		}
	}
	until(t, "held", func() bool { return len(waiting(t, sp)) == n })
	second, err := Open(context.Background(), f.cfg("a"))
	if err != nil {
		t.Fatal(err)
	}
	defer second.Close()
	if first.leaseGone.Load() {
		t.Fatal("the watch must not have noticed")
	}
	// A confirmation is reused for leaseConfirmFor: a takeover inside that window is the residual the fence leaves open.
	time.Sleep(leaseConfirmFor + 50*time.Millisecond)
	for _, w := range waiting(t, sp) { // the hook shows the mail on the old serve
		if err := sp.Drained(spool.Ref{Seq: w.Seq}); err != nil {
			t.Fatal(err)
		}
	}
	until(t, "the earlier serve's fence marks the lease lost", func() bool { return first.leaseGone.Load() })
	time.Sleep(3 * SpoolCheck)
	floor, pend, ackp := consumerState(t, second, "a")
	if floor != 0 || pend+ackp != n {
		t.Fatalf("ack floor %d, pending %d, ack pending %d: the earlier serve acked drained mail after the takeover", floor, pend, ackp)
	}
}

// (b) A read of the lease that fails or times out marks no loss, but nothing is acked or delivered on it; the mail is
// delivered and acked once the lease can be confirmed again.
func TestFenceUnconfirmedLeaseAcksNothing(t *testing.T) {
	setVar(t, &leaseConfirmTimeout, 150*time.Millisecond)
	f := newFixture(t, "a", "snd")
	ep, ev := f.run(t, "a")
	hang := func(ctx context.Context, key string) (jetstream.KeyValueEntry, error) {
		<-ctx.Done()
		return nil, ctx.Err()
	}
	setLiveGet(ep, hang)
	start := time.Now()
	if ep.mayAck(pending{}) {
		t.Fatal("acked on a lease it could not confirm")
	}
	if d := time.Since(start); d < 100*time.Millisecond || d > 2*time.Second {
		t.Fatalf("the failed check took %v, want about the timeout", d)
	}
	if ep.leaseGone.Load() {
		t.Fatal("a failed read must not mark the lease lost")
	}
	start = time.Now()
	for i := 0; i < 20; i++ { // a burst reuses the one failed check
		ep.mayAck(pending{})
	}
	if d := time.Since(start); d > 100*time.Millisecond {
		t.Fatalf("a burst of 20 checks took %v: not cached", d)
	}

	snd := f.client(t, "snd")
	if _, err := snd.Send(context.Background(), "a", "while unconfirmed", 0); err != nil {
		t.Fatal(err)
	}
	time.Sleep(700 * time.Millisecond)
	if got := ev.all(); len(got) != 0 {
		t.Fatalf("delivered on an unconfirmed lease: %q", got)
	}
	if unread(t, ep) != 1 {
		t.Fatalf("unread %d, want 1 (not acked)", unread(t, ep))
	}
	setLiveGet(ep, nil) // reads work again
	waitText(t, ev, "while unconfirmed")
	until(t, "acked once confirmed", func() bool { return unread(t, ep) == 0 })
	if ep.leaseGone.Load() {
		t.Fatal("lease marked lost")
	}
}

// A confirmed lease costs no more than one read per leaseConfirmFor.
func TestFenceConfirmationIsReusedWithinItsWindow(t *testing.T) {
	f := newFixture(t, "a")
	ep, _ := f.run(t, "a")
	reads := 0
	setLiveGet(ep, func(ctx context.Context, key string) (jetstream.KeyValueEntry, error) {
		reads++ // under live.mu
		return ep.leaseKV.Get(ctx, key)
	})
	for i := 0; i < 50; i++ {
		if !ep.mayAck(pending{}) {
			t.Fatal("refused while holding the lease")
		}
	}
	ep.live.mu.Lock()
	n := reads
	ep.live.mu.Unlock()
	if n != 1 {
		t.Fatalf("%d reads for one burst, want 1", n)
	}
}

// (c) A serve that took the lease from a live holder of its session consumes nothing before the grace, and does after.
func TestTakeoverGraceBeforeNewServeConsumes(t *testing.T) {
	bigRefresh(t)
	setVar(t, &leaseWatchEvery, 500*time.Millisecond) // grace 2 x = 1s
	setSession(t, ours(t))
	stubHolderGone(t, func(string, string) bool { return false })
	f := newFixture(t, "a", "snd")
	first, err := Open(context.Background(), f.cfg("a"))
	if err != nil {
		t.Fatal(err)
	}
	defer first.Close()
	opened := time.Now()
	second, err := Open(context.Background(), f.cfg("a"))
	if err != nil {
		t.Fatal(err)
	}
	defer second.Close()
	snd := f.client(t, "snd")
	if _, err := snd.Send(context.Background(), "a", "after takeover", 0); err != nil {
		t.Fatal(err)
	}
	ev := &events{}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = second.Run(ctx, ev.deliver) }()
	time.Sleep(400 * time.Millisecond)
	if got := ev.all(); len(got) != 0 {
		t.Fatalf("consumed %q inside the grace", got)
	}
	if _, _, ackp := consumerState(t, second, "a"); ackp != 0 {
		t.Fatalf("%d messages pulled inside the grace", ackp)
	}
	waitText(t, ev, "after takeover")
	if d := time.Since(opened); d < 2*leaseWatchEvery-100*time.Millisecond {
		t.Fatalf("first delivery %v after the takeover, want at least the grace %v", d, 2*leaseWatchEvery)
	}
}

// (d) No grace when the lease was free or its holder dead.
func TestNoTakeoverGraceOnFreeOrDeadHolder(t *testing.T) {
	setVar(t, &leaseWatchEvery, time.Hour) // a grace would be two hours
	f := newFixture(t, "a", "b", "snd")
	free, err := Open(context.Background(), f.cfg("a"))
	if err != nil {
		t.Fatal(err)
	}
	if !free.graceUntil.IsZero() {
		t.Fatalf("grace on a free lease: %v", free.graceUntil)
	}
	snd := f.client(t, "snd")
	if _, err := snd.Send(context.Background(), "a", "at once", 0); err != nil {
		t.Fatal(err)
	}
	ev := &events{}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = free.Run(ctx, ev.deliver) }()
	defer free.Close()
	waitText(t, ev, "at once")

	host, _ := os.Hostname()
	putLease(t, f, "b", leaseValue{Instance: "dead", Host: host, Machine: needMachine(t), PID: deadPID(t), Since: "x"})
	dead, err := Open(context.Background(), f.cfg("b"))
	if err != nil {
		t.Fatal(err)
	}
	defer dead.Close()
	if !dead.graceUntil.IsZero() {
		t.Fatalf("grace on a dead holder: %v", dead.graceUntil)
	}
}

// takeoverPair opens a live same-session first endpoint and a second that takes over from it, with the grace set to
// grace; it returns both and the time the second was opened.
func takeoverPair(t *testing.T, f *fixture, name string, grace time.Duration) (first, second *Endpoint, opened time.Time) {
	t.Helper()
	bigRefresh(t)
	setVar(t, &takeoverGrace, func() time.Duration { return grace })
	setSession(t, ours(t))
	stubHolderGone(t, func(string, string) bool { return false })
	first, err := Open(context.Background(), f.cfg(name))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(first.Close)
	second, err = Open(context.Background(), f.cfg(name))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(second.Close)
	return first, second, time.Now()
}

// AwaitTakeover returns nothing before the grace and nil after it.
func TestAwaitTakeoverWaitsOutGrace(t *testing.T) {
	f := newFixture(t, "a")
	_, second, opened := takeoverPair(t, f, "a", 400*time.Millisecond)
	if !second.TakeoverPending() {
		t.Fatal("no takeover pending")
	}
	short, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
	defer cancel()
	if err := second.AwaitTakeover(short); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("inside the grace: %v", err)
	}
	if err := second.AwaitTakeover(context.Background()); err != nil {
		t.Fatal(err)
	}
	if d := time.Since(opened); d < 380*time.Millisecond {
		t.Fatalf("returned %v after the takeover, want at least the grace", d)
	}
}

// With no takeover there is nothing to wait for.
func TestAwaitTakeoverNoGrace(t *testing.T) {
	f := newFixture(t, "a")
	ep, err := Open(context.Background(), f.cfg("a"))
	if err != nil {
		t.Fatal(err)
	}
	defer ep.Close()
	if ep.TakeoverPending() {
		t.Fatal("takeover pending on a free lease")
	}
	if err := ep.AwaitTakeover(context.Background()); err != nil {
		t.Fatal(err)
	}
}

// A lease taken by a third before the grace ends is a *LeaseLostError, not nil.
func TestAwaitTakeoverLeaseLost(t *testing.T) {
	f := newFixture(t, "a")
	_, second, _ := takeoverPair(t, f, "a", 400*time.Millisecond)
	putLease(t, f, "a", leaseValue{Instance: "third", Host: "elsewhere", PID: "1", Since: "x"})
	err := second.AwaitTakeover(context.Background())
	var lost *LeaseLostError
	if !errors.As(err, &lost) {
		t.Fatalf("got %v, want a LeaseLostError", err)
	}
}

// The sequence serve.go follows after a takeover: the SAP service is registered after AwaitTakeover, so a live prompt
// sent inside the grace has no responder and one sent after it reaches onPrompt.
func TestPromptDuringTakeoverGraceNotDelivered(t *testing.T) {
	f := newFixture(t, "a", "snd")
	_, second, _ := takeoverPair(t, f, "a", 600*time.Millisecond)
	got := make(chan PromptEvent, 4)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	started := make(chan struct{})
	go func() {
		defer close(started)
		if second.AwaitTakeover(ctx) == nil {
			_ = second.StartService(ctx, func(p PromptEvent) {
				got <- p
				_ = second.Reply(p.RequestID, "ok", true)
			})
		}
	}()
	snd := f.client(t, "snd")
	var out bytes.Buffer
	short, c2 := context.WithTimeout(ctx, 2*time.Second)
	defer c2()
	if err := Prompt(short, snd.Conn(), snd.Identity(), "snd", "agents.prompt.cc.o.a", "early", &out); err == nil {
		t.Fatal("a prompt inside the grace was answered")
	}
	select {
	case p := <-got:
		t.Fatalf("delivered inside the grace: %+v", p)
	default:
	}
	<-started
	out.Reset()
	if err := Prompt(ctx, snd.Conn(), snd.Identity(), "snd", "agents.prompt.cc.o.a", "late", &out); err != nil {
		t.Fatal(err)
	}
	if p := <-got; p.Text != "late" {
		t.Fatalf("got %+v", p)
	}
}
