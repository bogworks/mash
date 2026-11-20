package endpoint

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/bogworks/mash/internal/config"
	"github.com/bogworks/mash/internal/sap"
)

// cfgState is cfg(name) with its own state directory and the refusal of unverified mail on or off.
func (f *fixture) cfgState(name, state string, accept bool) config.Config {
	c := f.cfg(name)
	c.StateDir, c.AcceptUnverified = state, accept
	return c
}

// serveCfg runs the endpoint cfg names until the returned stop is called.
func serveCfg(t *testing.T, cfg config.Config) (*Endpoint, *events, func()) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	ep, err := Open(ctx, cfg)
	if err != nil {
		cancel()
		t.Fatal(err)
	}
	ev := &events{}
	done := make(chan struct{})
	go func() { _ = ep.Run(ctx, ev.deliver); close(done) }()
	var once sync.Once
	stop := func() { once.Do(func() { cancel(); <-done; ep.Close() }) }
	t.Cleanup(stop)
	return ep, ev, stop
}

// publishUnverified stores one message of each unverified label in a's inbox and returns their labels in order.
func (f *fixture) publishUnverified(t *testing.T, to string) []sap.Trust {
	t.Helper()
	subj := taddr(to).Subjects().Mail
	nc := f.e.ConnAs(t, f.creds["b"], tkey("b"))
	id := identity(t, f.creds["b"])
	publishRaw(t, nc, subj, "", "absent body")
	xcreds, _ := f.e.Mint(t, taddr("x")) // a valid user, never provisioned
	xh, _ := identity(t, xcreds).Sign(subj, []byte("unknown body"), tkey("x"), time.Now(), "")
	publishRaw(t, f.e.ConnAs(t, xcreds, tkey("x")), subj, xh.Encode(), "unknown body")
	claimed := sap.SenderHeader{V: 1, Account: id.Account, User: id.User, Name: tkey("b")}
	publishRaw(t, nc, subj, claimed.Encode(), "claimed body")
	sh, _ := id.Sign(subj, []byte("stale body"), tkey("b"), time.Now().Add(-10*time.Minute), "")
	publishRaw(t, nc, subj, sh.Encode(), "stale body")
	return []sap.Trust{sap.TrustAbsent, sap.TrustUnknownSigner, sap.TrustClaimed, sap.TrustStale}
}

func waitRefused(t *testing.T, cfg config.Config, want int) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if RefusedCount(cfg) == want {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("refused count = %d, want %d", RefusedCount(cfg), want)
}

// Only verified mail is delivered. Each other label is dropped and counted in the file, which survives a restart; the
// sender is told nothing; neither the listing, a read nor the inbox counts show the refused mail.
func TestUnverifiedMailIsRefusedByDefault(t *testing.T) {
	f := newFixture(t, "a", "b")
	cfg := f.cfgState("a", t.TempDir(), false)
	labels := f.publishUnverified(t, "a")
	b := f.client(t, "b")
	seq, err := b.Send(context.Background(), tdot("a"), "hello a", 0)
	if err != nil {
		t.Fatal(err)
	}
	_, ev, stop := serveCfg(t, cfg)
	got := ev.waitFor(t, 1, 5*time.Second)
	if !strings.Contains(got[0], "[verified]") || !strings.Contains(got[0], "hello a") {
		t.Fatalf("event: %s", got[0])
	}
	waitRefused(t, cfg, len(labels))
	time.Sleep(300 * time.Millisecond)
	if all := strings.Join(ev.all(), "\n"); strings.Contains(all, "absent body") || strings.Contains(all, "claimed body") ||
		strings.Contains(all, "unknown body") || strings.Contains(all, "stale body") || len(ev.all()) != 1 {
		t.Fatalf("unverified mail reached the session:\n%s", all)
	}
	dir, _ := SpoolDir(cfg)
	if b, err := os.ReadFile(filepath.Join(dir, RefusedFile)); err != nil || string(b) != "4\n" {
		t.Fatalf("count file %q %v", b, err)
	}
	if ents, _ := os.ReadDir(dir); len(ents) != 2 { // the count and its sequences: no temporary file left behind
		t.Fatalf("spool dir holds %v", ents)
	}
	// The sender is told nothing: b has no reply in its own inbox.
	if ms, err := f.client(t, "b").Inbox(context.Background(), 20); err != nil || len(ms) != 0 {
		t.Fatalf("the sender's inbox: %+v %v", ms, err)
	}

	// The listing, a read and the counts leave the refused mail out; the stream still holds it.
	c, err := OpenClient(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	ms, err := c.Inbox(context.Background(), 20)
	if err != nil || len(ms) != 1 || ms[0].Seq != seq || ms[0].Trust != sap.TrustVerified {
		t.Fatalf("inbox: %+v %v", ms, err)
	}
	r, err := c.ReadInbox(context.Background(), InboxQuery{Limit: 20})
	if err != nil || len(r.Mail) != 1 || r.State.Total != 1 {
		t.Fatalf("ReadInbox: %d messages, total %d, %v", len(r.Mail), r.State.Total, err)
	}
	for n := uint64(1); n <= 5; n++ {
		m, err := c.Read(context.Background(), n)
		var nm *NoMessageError
		if n == seq {
			if err != nil || m.Trust != sap.TrustVerified {
				t.Fatalf("read #%d: %+v %v", n, m, err)
			}
		} else if !errors.As(err, &nm) {
			t.Fatalf("read #%d of refused mail: %+v %v", n, m, err)
		}
	}

	// The count persists across a restart, and mail refused before it is not counted again.
	stop()
	f.publishUnverified(t, "a")
	_, _, _ = serveCfg(t, cfg)
	waitRefused(t, cfg, 8)
	time.Sleep(300 * time.Millisecond)
	if n := RefusedCount(cfg); n != 8 {
		t.Fatalf("count after the restart %d, want 8", n)
	}
}

// With accept_unverified the mail is delivered with its label, as before, and nothing is counted.
func TestAcceptUnverifiedDeliversLabelled(t *testing.T) {
	f := newFixture(t, "a", "b")
	cfg := f.cfgState("a", t.TempDir(), true)
	f.publishUnverified(t, "a")
	_, ev, _ := serveCfg(t, cfg)
	got := strings.Join(ev.waitFor(t, 1, 5*time.Second), "\n")
	for _, l := range []string{"[absent]", "[unknown-signer]", "[claimed]", "[stale]"} {
		if !strings.Contains(got, l) {
			t.Errorf("no %s in\n%s", l, got)
		}
	}
	if n := RefusedCount(cfg); n != 0 {
		t.Fatalf("count %d, want 0", n)
	}
	c, err := OpenClient(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	if ms, err := c.Inbox(context.Background(), 20); err != nil || len(ms) != 4 {
		t.Fatalf("inbox: %d %v", len(ms), err)
	}
}

// Forged mail stays dropped and uncounted as before (it is not unverified mail, it is never shown).
func TestForgedMailIsNotCountedAsRefused(t *testing.T) {
	f := newFixture(t, "a", "b")
	cfg := f.cfgState("a", t.TempDir(), false)
	subj := taddr("a").Subjects().Mail
	id := identity(t, f.creds["b"])
	h, _ := id.Sign(subj, []byte("forged body"), tkey("b"), time.Now(), "")
	h.Sig = strings.Repeat("A", len(h.Sig))
	publishRaw(t, f.e.ConnAs(t, f.creds["b"], tkey("b")), subj, h.Encode(), "forged body")
	a, _, _ := serveCfg(t, cfg)
	deadline := time.Now().Add(5 * time.Second)
	for a.Dropped() != 1 && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	if a.Dropped() != 1 || RefusedCount(cfg) != 0 {
		t.Fatalf("dropped %d, refused %d", a.Dropped(), RefusedCount(cfg))
	}
}

// Concurrent arrivals lose no count, and the file is always whole.
func TestRefusedCountConcurrent(t *testing.T) {
	cfg := config.Config{Owner: "acme", Group: "demo", Name: "bob", StateDir: t.TempDir()}
	e := &Endpoint{cfg: cfg}
	const n = 60
	var wg sync.WaitGroup
	for i := 1; i <= n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := e.recordRefusal(uint64(i)); err != nil {
				t.Error(err)
			}
			_ = RefusedCount(cfg)
		}()
	}
	wg.Wait()
	if got := RefusedCount(cfg); got != n {
		t.Fatalf("count %d, want %d", got, n)
	}
	if got := e.refusedStored(1, n); got != n {
		t.Fatalf("stored %d, want %d", got, n)
	}
	if got := e.refusedStored(31, n); got != n-30 {
		t.Fatalf("stored above 30: %d", got)
	}
}

// Without a state directory nothing is written (a relative path would litter the working directory).
func TestRefusalNeedsAStateDir(t *testing.T) {
	e := &Endpoint{cfg: config.Config{Owner: "acme", Group: "demo", Name: "bob"}}
	if _, err := e.recordRefusal(1); err == nil {
		t.Fatal("recorded with no state directory")
	}
}
