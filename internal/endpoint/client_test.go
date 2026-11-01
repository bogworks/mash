package endpoint

import (
	"context"
	"fmt"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/nats-io/nats.go/jetstream"

	"github.com/bogworks/mash/internal/sap"
)

func (f *fixture) leanClient(t *testing.T, name string, opts ClientOptions) *Endpoint {
	t.Helper()
	ep, err := OpenClientWith(context.Background(), f.cfg(name), opts)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(ep.Close)
	return ep
}

// A client that reads only the records it needs labels every message as one that read the whole directory does,
// including a signed message whose header names no sender: the key alone is looked up in the directory.
func TestLeanClientLabelsLikeTheWholeDirectory(t *testing.T) {
	f := newFixture(t, "a", "b", "c")
	subj := sap.For(f.e.Owner, "a").Mail
	nc := f.e.ConnAs(t, f.creds["b"], "b")
	id := identity(t, f.creds["b"])
	signed := func(name, body string) {
		h, err := id.Sign(subj, []byte(body), name, time.Now(), "")
		if err != nil {
			t.Fatal(err)
		}
		publishRaw(t, nc, subj, h.Encode(), body)
	}
	signed("b", "named")                      // verified
	signed("", "unnamed")                     // verified by its key alone
	signed("c", "wrong name")                 // b's key under c's name: unknown signer
	signed("nobody", "no such endpoint")      // unknown signer
	signed("Not A Name!", "not a valid name") // unknown signer, never looked up
	publishRaw(t, nc, subj, "", "no header")  // absent
	signed("b", "named again")                // verified

	ctx := context.Background()
	full, err := f.client(t, "a").Inbox(ctx, 20)
	if err != nil {
		t.Fatal(err)
	}
	for _, opts := range []ClientOptions{{Inbox: true}, {}} {
		lean, err := f.leanClient(t, "a", opts).Inbox(ctx, 20)
		if err != nil {
			t.Fatal(err)
		}
		if len(lean) != len(full) || len(full) != 7 {
			t.Fatalf("%d lean and %d full messages, want 7", len(lean), len(full))
		}
		for i := range full {
			if lean[i].From != full[i].From || lean[i].Trust != full[i].Trust || lean[i].Seq != full[i].Seq {
				t.Fatalf("#%d: lean %s [%s], whole directory %s [%s]", full[i].Seq, lean[i].From, lean[i].Trust, full[i].From, full[i].Trust)
			}
		}
	}
	want := []sap.Trust{sap.TrustVerified, sap.TrustVerified, sap.TrustUnknownSigner, sap.TrustUnknownSigner, sap.TrustUnknownSigner, sap.TrustAbsent, sap.TrustVerified}
	for i, m := range full {
		if m.Trust != want[i] {
			t.Fatalf("#%d is %s, want %s", m.Seq, m.Trust, want[i])
		}
	}
	// read follows the same rule.
	m, err := f.leanClient(t, "a", ClientOptions{Inbox: true}).Read(ctx, 2)
	if err != nil || m.Trust != sap.TrustVerified || m.From != "b" {
		t.Fatalf("read of the unnamed message: %s [%s] %v", m.From, m.Trust, err)
	}
}

// A lean client reads no directory records beyond its own, the recipient's and its senders'.
func TestLeanClientDoesNotLoadTheDirectory(t *testing.T) {
	f := newFixture(t, "a", "b", "c")
	c := f.leanClient(t, "a", ClientOptions{Records: []string{"b"}})
	c.mu.RLock()
	defer c.mu.RUnlock()
	if c.fullDir || len(c.dir) != 2 || c.dir["a"].Name != "a" || c.dir["b"].Name != "b" {
		t.Fatalf("lean directory %v (full %v)", c.dir, c.fullDir)
	}
}

// A client that is not provisioned or holds another endpoint's credentials is refused as before, lean or not.
func TestLeanClientRefusals(t *testing.T) {
	f := newFixture(t, "a", "b")
	cfg := f.cfg("a")
	cfg.Creds = f.creds["b"]
	if _, err := OpenClientWith(context.Background(), cfg, ClientOptions{}); err == nil || !strings.Contains(err.Error(), "do not match") {
		t.Fatalf("another endpoint's credentials: %v", err)
	}
	f.creds["ghost"], _ = f.e.Mint(t, "ghost")
	if _, err := OpenClientWith(context.Background(), f.cfg("ghost"), ClientOptions{}); err == nil || !strings.Contains(err.Error(), "not provisioned") {
		t.Fatalf("an endpoint with no record: %v", err)
	}
}

// collect reads in batches and tops up: seqs that are gone or forged leave the batch short, and the next batch makes it
// up, newest first, never past the limit.
func TestCollectTopsUpAfterGaps(t *testing.T) {
	f := newFixture(t, "a", "b")
	a, b := f.client(t, "a"), f.client(t, "b")
	ctx := context.Background()
	for i := 1; i <= 8; i++ {
		if _, err := b.Send(ctx, "a", fmt.Sprintf("m%d", i), 0); err != nil {
			t.Fatal(err)
		}
	}
	s, err := a.inbox(ctx)
	if err != nil {
		t.Fatal(err)
	}
	gone := map[uint64]bool{8: true, 7: true, 5: true}
	get := func(ctx context.Context, seq uint64, opts ...jetstream.GetMsgOpt) (*jetstream.RawStreamMsg, error) {
		if gone[seq] {
			return nil, jetstream.ErrMsgNotFound
		}
		return s.GetMsg(ctx, seq, opts...)
	}
	ms, err := a.collect(ctx, get, seqsDown(8, 1), 4)
	if err != nil {
		t.Fatal(err)
	}
	var got []uint64
	for _, m := range ms {
		got = append(got, m.Seq)
	}
	if fmt.Sprint(got) != "[6 4 3 2]" {
		t.Fatalf("the newest 4 of what is left, newest first: %v", got)
	}
}

// collect never has more than fetchConcurrency reads in flight, and does have many.
func TestCollectBoundsItsConcurrency(t *testing.T) {
	f := newFixture(t, "a")
	a := f.client(t, "a")
	var inflight, peak atomic.Int32
	get := func(ctx context.Context, seq uint64, _ ...jetstream.GetMsgOpt) (*jetstream.RawStreamMsg, error) {
		n := inflight.Add(1)
		defer inflight.Add(-1)
		for {
			p := peak.Load()
			if n <= p || peak.CompareAndSwap(p, n) {
				break
			}
		}
		time.Sleep(5 * time.Millisecond)
		return nil, jetstream.ErrMsgNotFound
	}
	ms, err := a.collect(context.Background(), get, seqsDown(300, 1), 300)
	if err != nil || len(ms) != 0 {
		t.Fatalf("%v %v", ms, err)
	}
	if p := peak.Load(); p > fetchConcurrency || p < 8 {
		t.Fatalf("peak %d reads in flight, want between 8 and %d", p, fetchConcurrency)
	}
}

// A who that has its answers stops waiting once they go quiet, not at the two-second cap.
func TestRefreshEndsWhenAnswersGoQuiet(t *testing.T) {
	f := newFixture(t, "a", "b", "c")
	f.serve(t, "b", noPrompt)
	f.serve(t, "c", noPrompt)
	ep := f.leanClient(t, "a", ClientOptions{FullDirectory: true, Presence: true})
	start := time.Now()
	if err := ep.Refresh(context.Background()); err != nil {
		t.Fatal(err)
	}
	if d := time.Since(start); d > PresenceCap-300*time.Millisecond {
		t.Fatalf("who waited %v for answers that had all come", d)
	}
	online := map[string]bool{}
	for _, p := range ep.Who() {
		online[p.Record.Name] = p.Online
	}
	if !online["b"] || !online["c"] || online["a"] {
		t.Fatalf("presence %v", online)
	}
}

// With a listener that never answers, the window stays open to its cap.
func TestRefreshWaitsTheCapWhenNothingAnswers(t *testing.T) {
	f := newFixture(t, "a")
	if _, err := f.e.Conn(t, f.e.AdminCreds).SubscribeSync("$SRV.INFO." + sap.ServiceName); err != nil {
		t.Fatal(err)
	}
	// The cap counts from when the probe is sent, at open, so the clock starts before the client opens: on a loaded
	// machine the open itself can take a good part of the cap.
	start := time.Now()
	ep := f.leanClient(t, "a", ClientOptions{FullDirectory: true, Presence: true})
	_ = ep.Refresh(context.Background())
	if d := time.Since(start); d < PresenceCap-500*time.Millisecond {
		t.Fatalf("who gave up after %v with no answer at all", d)
	}
}
