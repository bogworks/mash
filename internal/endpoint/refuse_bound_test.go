package endpoint

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/bogworks/mash/internal/sap"
	"github.com/bogworks/mash/internal/store"
)

// A flood of mail from one unknown (name, key) costs one directory lookup per poll interval, not one each.
func TestUnknownSignerLookupIsCachedForAPollInterval(t *testing.T) {
	f := newFixture(t, "a", "b")
	a := f.client(t, "a")
	subj := taddr("a").MailSubject()
	xcreds, _ := f.e.Mint(t, taddr("x")) // a valid user, never provisioned, claiming b's name
	xid := identity(t, xcreds)
	var lookups atomic.Int64
	h := func(string) error { lookups.Add(1); return nil }
	fetchRecordHook.Store(&h)
	t.Cleanup(func() { fetchRecordHook.Store(nil) })
	for i := 0; i < 50; i++ {
		hdr, _ := xid.Sign(subj, []byte("flood"), tkey("b"), time.Now(), "")
		_, trust, _, ok := a.classify(subj, map[string][]string{sap.HeaderName: {hdr.Encode()}}, []byte("flood"), time.Now())
		if !ok || trust != sap.TrustUnknownSigner {
			t.Fatalf("message %d: trust %s ok %v", i, trust, ok)
		}
	}
	if n := lookups.Load(); n != 1 {
		t.Fatalf("%d lookups for 50 messages of one unknown (name, key), want 1", n)
	}
	// The table is bounded: when full of unexpired entries, a new key is still looked up, the oldest entry making room.
	a.mu.Lock()
	for i := 0; len(a.failedLookups) < maxFailedLookups; i++ {
		a.failedLookups[time.Duration(i).String()] = time.Now().Add(time.Minute)
	}
	a.mu.Unlock()
	before := lookups.Load()
	a.reloadSender(sap.SenderHeader{V: 1, User: "UNEW", Name: tkey("b")})
	if lookups.Load() != before+1 {
		t.Fatal("a full table did not look up")
	}
	a.mu.Lock()
	size := len(a.failedLookups)
	a.mu.Unlock()
	if size > maxFailedLookups {
		t.Fatalf("table grew to %d", size)
	}
}

// Mail refused earlier stays refused: with accept_unverified on later, and when the directory later knows the signer.
func TestRecordedRefusalSurvivesSettingAndDirectoryChange(t *testing.T) {
	f := newFixture(t, "a", "b")
	state := t.TempDir()
	cfg := f.cfgState("a", state, false)
	f.publishUnverified(t, "a")
	_, _, stop := serveCfg(t, cfg)
	waitRefused(t, cfg, 4)
	stop()

	ctx := context.Background()
	on := f.cfgState("a", state, true)
	c, err := OpenClient(ctx, on)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	// The directory now knows x, whose message (#2) was refused as unknown-signer.
	xcreds, _ := f.e.Mint(t, taddr("x"))
	xuser := identity(t, xcreds).User
	c.mu.Lock()
	c.dir[tkey("x")] = store.Record{User: xuser}
	c.byUser[xuser] = tkey("x")
	c.mu.Unlock()
	if ms, err := c.Inbox(ctx, 20); err != nil || len(ms) != 0 {
		t.Fatalf("inbox lists refused mail: %+v %v", ms, err)
	}
	if r, err := c.ReadInbox(ctx, InboxQuery{Limit: 20}); err != nil || len(r.Mail) != 0 {
		t.Fatalf("ReadInbox: %+v %v", r, err)
	}
	for n := uint64(1); n <= 4; n++ {
		var rf *RefusedMessageError
		if m, err := c.Read(ctx, n); !errors.As(err, &rf) {
			t.Fatalf("read #%d with accept_unverified on: %+v %v", n, m, err)
		}
	}
}

// A serve that loses its lease while recording a refusal does not acknowledge the message; the record stays (never taken back), so the new holder finds it refused.
func TestRefusalRechecksLeaseBeforeAck(t *testing.T) {
	f := newFixture(t, "a", "b")
	cfg := f.cfgState("a", t.TempDir(), false)
	subj := taddr("a").MailSubject()
	ctx, cancel := context.WithCancel(context.Background())
	ep, err := Open(ctx, cfg)
	if err != nil {
		cancel()
		t.Fatal(err)
	}
	var hooked atomic.Bool
	ep.refusedHook = func() { hooked.Store(true); ep.leaseGone.Store(true) }
	done := make(chan struct{})
	go func() { _ = ep.Run(ctx, (&events{}).deliver); close(done) }()
	defer func() { cancel(); <-done; ep.Close() }()
	publishRaw(t, f.e.ConnAs(t, f.creds["b"], tkey("b")), subj, "", "absent body")
	until(t, "the refusal is recorded", hooked.Load)
	time.Sleep(500 * time.Millisecond)
	if n := RefusedCount(cfg); n != 1 {
		t.Fatalf("count %d after the lease was lost while recording, want 1 (kept)", n)
	}
	if !ep.refusedSeq(1, *ep.refusedStream.Load()) {
		t.Fatal("the sequence record was lost")
	}
	cons, err := ep.js.Consumer(ctx, ep.ident.InboxStream(), store.Consumer)
	if err != nil {
		t.Fatal(err)
	}
	if ci, err := cons.Info(ctx); err != nil || ci.AckFloor.Stream != 0 {
		t.Fatalf("the message was acknowledged: %+v %v", ci, err)
	}
}

// A refusal whose sequence cannot be recorded is not acknowledged (it is retried) and not counted.
func TestRefusalNotAckedWhenSequenceUnrecordable(t *testing.T) {
	f := newFixture(t, "a", "b")
	cfg := f.cfgState("a", t.TempDir(), false)
	dir, err := SpoolDir(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dir, refusedSeqsFile), 0o700); err != nil { // a directory where the file goes
		t.Fatal(err)
	}
	subj := taddr("a").MailSubject()
	ctx, cancel := context.WithCancel(context.Background())
	ep, err := Open(ctx, cfg)
	if err != nil {
		cancel()
		t.Fatal(err)
	}
	done := make(chan struct{})
	go func() { _ = ep.Run(ctx, (&events{}).deliver); close(done) }()
	defer func() { cancel(); <-done; ep.Close() }()
	publishRaw(t, f.e.ConnAs(t, f.creds["b"], tkey("b")), subj, "", "absent body")
	time.Sleep(1500 * time.Millisecond)
	if n := RefusedCount(cfg); n != 0 {
		t.Fatalf("count %d, want 0", n)
	}
	cons, err := ep.js.Consumer(ctx, ep.ident.InboxStream(), store.Consumer)
	if err != nil {
		t.Fatal(err)
	}
	if ci, err := cons.Info(ctx); err != nil || ci.AckFloor.Stream != 0 {
		t.Fatalf("the message was acknowledged: %+v %v", ci, err)
	}
}
