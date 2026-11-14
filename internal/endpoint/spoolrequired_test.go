package endpoint

import (
	"context"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/bogworks/mash/internal/spool"
)

type reports struct {
	mu   sync.Mutex
	msgs []string
}

func (r *reports) add(m string) { r.mu.Lock(); r.msgs = append(r.msgs, m); r.mu.Unlock() }
func (r *reports) all() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.msgs...)
}

// quickHold shortens the redelivery delay of held mail for a test.
func quickHold(t *testing.T) {
	t.Helper()
	od := spoolFullDelay
	spoolFullDelay = time.Second
	t.Cleanup(func() { spoolFullDelay = od })
}

func noBody(t *testing.T, ev *events, body string) {
	t.Helper()
	for _, g := range ev.all() {
		if strings.Contains(g, body) {
			t.Fatalf("channel event %q carries the body", g)
		}
	}
}

// (a) A spool that cannot be bound at start: mail is held unacked and reported; once the spool can be bound it is
// spooled from the held backlog and, drained, acked.
func TestSpoolRequiredHoldsWithoutASpoolAndBindsLater(t *testing.T) {
	quickHold(t)
	f := newFixture(t, "a", "b")
	ctx, cancel := context.WithCancel(context.Background())
	ep, err := Open(ctx, f.cfg("a"))
	if err != nil {
		t.Fatal(err)
	}
	rep := &reports{}
	ep.NoChannel()
	ep.ReportSpoolFaults(rep.add)
	dir := t.TempDir() + "/spool"
	if err := os.WriteFile(dir, nil, 0o600); err != nil { // a file where the spool directory goes
		t.Fatal(err)
	}
	sp := spool.Spool{Dir: dir}
	serr := ep.SetSpool(sp, "")
	if serr == nil {
		t.Fatal("SetSpool succeeded on a file")
	}
	ep.NoteSpoolFault(serr)
	ev := &events{}
	done := make(chan struct{})
	go func() { _ = ep.Run(ctx, ev.deliver); close(done) }()
	t.Cleanup(func() { cancel(); <-done; ep.Close() })
	if _, err := f.client(t, "b").Send(context.Background(), "a", "held body", 0); err != nil {
		t.Fatal(err)
	}
	time.Sleep(4 * time.Second) // past several redeliveries
	noBody(t, ev, "held body")
	if n := unread(t, ep); n != 1 {
		t.Fatalf("unread %d, want 1: held unacked", n)
	}
	if r := rep.all(); len(r) != 1 || !strings.Contains(r[0], "held unacked in the inbox because the spool is failing") || !strings.Contains(r[0], "inbox and read tools") {
		t.Fatalf("reports %q", r)
	}
	if l, ok := ep.SpoolFaultLine(); !ok || !strings.Contains(l, "delivered once the spool recovers") {
		t.Fatalf("status line %q, %v", l, ok)
	}
	// Recovery: the file goes, the spool binds.
	if err := os.Rename(dir, dir+".was"); err != nil {
		t.Fatal(err)
	}
	if err := ep.BindSpool(ctx, sp, "", true); err != nil {
		t.Fatal(err)
	}
	if _, ok := ep.SpoolFaultLine(); ok {
		t.Fatal("the line stands after the spool bound")
	}
	until(t, "spooled", func() bool { return len(waiting(t, sp)) == 1 })
	w := waiting(t, sp)
	if string(w[0].Body) != "held body" {
		t.Fatalf("waiting %+v", w)
	}
	if n := unread(t, ep); n != 1 {
		t.Fatalf("unread %d, want 1: spooled, not yet drained", n)
	}
	if err := sp.Drained(spool.Ref{Seq: w[0].Seq}); err != nil {
		t.Fatal(err)
	}
	until(t, "acked once drained", func() bool { return unread(t, ep) == 0 })
	time.Sleep(3 * time.Second)
	if n := len(waiting(t, sp)); n > 1 {
		t.Fatalf("%d entries, want the one", n)
	}
	noBody(t, ev, "held body")
}

// (b) A bound, healthy spool that starts failing: mail is held unacked past the spool tries, not channel-delivered,
// reported; after recovery it is spooled and, drained, acked.
func TestSpoolRequiredHoldsMidSessionWriteFailure(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root writes through a 0500 directory")
	}
	quickHold(t)
	f := newFixture(t, "a", "b")
	ctx, cancel := context.WithCancel(context.Background())
	ep, err := Open(ctx, f.cfg("a"))
	if err != nil {
		t.Fatal(err)
	}
	rep := &reports{}
	ep.NoChannel()
	ep.ReportSpoolFaults(rep.add)
	sp := spool.Spool{Dir: t.TempDir() + "/spool"}
	if err := ep.SetSpool(sp, ""); err != nil {
		t.Fatal(err)
	}
	ep.SpoolFromStart()
	if _, ok := ep.SpoolFaultLine(); ok {
		t.Fatal("a line before any failure")
	}
	ev := &events{}
	done := make(chan struct{})
	go func() { _ = ep.Run(ctx, ev.deliver); close(done) }()
	t.Cleanup(func() { cancel(); <-done; ep.Close() })
	if err := sp.Touch(); err != nil { // a hook has run: the channel would take it, were it allowed
		t.Fatal(err)
	}
	if err := os.Chmod(sp.Dir, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(sp.Dir, 0o700) })
	if _, err := f.client(t, "b").Send(context.Background(), "a", "mid body", 0); err != nil {
		t.Fatal(err)
	}
	until(t, "the report", func() bool { return len(rep.all()) == 1 })
	time.Sleep(4 * time.Second) // past the three spool tries and a redelivery
	noBody(t, ev, "mid body")
	if n := unread(t, ep); n != 1 {
		t.Fatalf("unread %d, want 1: held unacked", n)
	}
	if l, ok := ep.SpoolFaultLine(); !ok || !strings.Contains(l, "spool is failing") {
		t.Fatalf("status line %q, %v", l, ok)
	}
	if len(rep.all()) != 1 {
		t.Fatalf("reports %q, want one per spell", rep.all())
	}
	if err := os.Chmod(sp.Dir, 0o700); err != nil {
		t.Fatal(err)
	}
	until(t, "spooled after recovery", func() bool { return len(waiting(t, sp)) == 1 })
	until(t, "the line clears", func() bool { _, ok := ep.SpoolFaultLine(); return !ok })
	w := waiting(t, sp)
	if err := sp.Drained(spool.Ref{Seq: w[0].Seq}); err != nil {
		t.Fatal(err)
	}
	until(t, "acked once drained", func() bool { return unread(t, ep) == 0 })
	noBody(t, ev, "mid body")
}
