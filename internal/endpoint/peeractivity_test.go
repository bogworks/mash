package endpoint

import (
	"context"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/nats-io/nats.go"

	"github.com/bogworks/mash/internal/admin"
	"github.com/bogworks/mash/internal/sap"
)

// peerStatusBody is a heartbeat-shaped status reply with extra top-level keys.
func peerStatusBody(extra string) []byte {
	b := `{"agent":"agents","owner":"o","session":"p","instance_id":"INST1","ts":"2026-01-01T00:00:00Z","interval_s":30,"protocol_version":"0.3"`
	if extra != "" {
		b += "," + extra
	}
	return []byte(b + "}")
}

// fakePeer answers status requests for the legacy endpoint name with body, from the admin connection.
func (f *fixture) fakePeer(t *testing.T, name string, body []byte) {
	t.Helper()
	nc := f.e.Conn(t, f.e.AdminCreds)
	if _, err := nc.Subscribe(sap.For(f.e.Owner, name).Status, func(m *nats.Msg) { _ = m.Respond(body) }); err != nil {
		t.Fatal(err)
	}
	if err := nc.Flush(); err != nil {
		t.Fatal(err)
	}
}

func TestPeerActivityOutcomes(t *testing.T) {
	f := newFixture(t, "a")
	a := f.client(t, "a")
	for _, c := range []struct {
		peer, extra string
		want        PeerState
	}{
		{"p1", `"program_status":{"state":"blocked","kind":"question"}`, PeerState{Outcome: PeerReported, State: "blocked", Kind: "question", InstanceID: "INST1"}},
		{"p2", ``, PeerState{Outcome: PeerNotReported, InstanceID: "INST1"}},
		{"p3", `"program_status":{"state":"sleeping"}`, PeerState{Outcome: PeerNotReported, InstanceID: "INST1"}},
		{"p4", `"program_status":{"state":"blocked","kind":"lunch"}`, PeerState{Outcome: PeerReported, State: "blocked", Kind: "", InstanceID: "INST1"}},
		{"p5", `"program_status":{"state":"working","msg":"SECRET-MSG","extra":1},"program_detail":{"title":"SECRET-TITLE","msg":"SECRET-MSG"},"progress":0.5,"app":"x","id":"y"`, PeerState{Outcome: PeerReported, State: "working", Kind: "", InstanceID: "INST1"}},
		{"p6", `"program_status":{"state":"working","kind":"question"}`, PeerState{Outcome: PeerReported, State: "working", Kind: "", InstanceID: "INST1"}}, // kind means something only with blocked
		{"p7", `"program_status":{"state":7}`, PeerState{Outcome: PeerNotReported, InstanceID: "INST1"}},
		{"p9", `"program_status":"working"`, PeerState{Outcome: PeerNotReported, InstanceID: "INST1"}},
		{"p10", `"program_status":null`, PeerState{Outcome: PeerNotReported, InstanceID: "INST1"}},
		{"p11", `"program_status":["working"]`, PeerState{Outcome: PeerNotReported, InstanceID: "INST1"}},
		{"p12", `"state":"working","kind":"question"`, PeerState{Outcome: PeerNotReported, InstanceID: "INST1"}}, // top-level keys are ignored
		{"p13", `"state":"blocked","kind":"question","program_status":{"state":"idle"}`, PeerState{Outcome: PeerReported, State: "idle", Kind: "", InstanceID: "INST1"}},
		{"p14", `"program_status":{"kind":"question"}`, PeerState{Outcome: PeerNotReported, InstanceID: "INST1"}},
		{"p15", `"program_status":{"state":"blocked","kind":"auth"}`, PeerState{Outcome: PeerReported, State: "blocked", Kind: "auth", InstanceID: "INST1"}},
		// delivery is the object {health, reason}, read with or without a state; only known words and classes are kept.
		{"d1", `"delivery":{"health":"holding","reason":"held_unacked"}`, PeerState{Outcome: PeerNotReported, InstanceID: "INST1", Delivery: "holding", DeliveryReason: "held_unacked"}},
		{"d2", `"program_status":{"state":"idle"},"delivery":{"health":"ok"}`, PeerState{Outcome: PeerReported, State: "idle", InstanceID: "INST1", Delivery: "ok"}},
		{"d3", `"delivery":{"health":"fine","reason":"spool_failing"}`, PeerState{Outcome: PeerNotReported, InstanceID: "INST1"}},
		{"d4", `"delivery":{"health":"degraded","reason":"SECRET-ERR"}`, PeerState{Outcome: PeerNotReported, InstanceID: "INST1", Delivery: "degraded"}},
		{"d5", `"delivery":{"health":"ok","reason":"spool_failing"}`, PeerState{Outcome: PeerNotReported, InstanceID: "INST1", Delivery: "ok"}},
		{"d6", `"delivery":{"health":7,"reason":["x"]}`, PeerState{Outcome: PeerNotReported, InstanceID: "INST1"}},
		{"d7", `"delivery":"holding","delivery_reason":"held_unacked"`, PeerState{Outcome: PeerNotReported, InstanceID: "INST1"}}, // top-level forms are ignored
		{"d8", `"delivery":null`, PeerState{Outcome: PeerNotReported, InstanceID: "INST1"}},
		{"d9", `"delivery":{"health":"holding"}`, PeerState{Outcome: PeerNotReported, InstanceID: "INST1", Delivery: "holding"}},
	} {
		f.fakePeer(t, c.peer, peerStatusBody(c.extra))
		got := a.PeerActivity(context.Background(), c.peer)
		if got != c.want {
			t.Errorf("%s: got %+v, want %+v", c.peer, got, c.want)
		}
		if strings.Contains(got.Display(), "SECRET") {
			t.Errorf("%s: free text shown: %q", c.peer, got.Display())
		}
	}
	// Not valid JSON: answered, not reported.
	f.fakePeer(t, "p8", []byte("not json"))
	if got := a.PeerActivity(context.Background(), "p8"); got.Outcome != PeerNotReported {
		t.Errorf("garbage reply: %+v", got)
	}
}

func TestPeerStateDisplay(t *testing.T) {
	for _, c := range []struct {
		st   PeerState
		want string
	}{
		{PeerState{Outcome: PeerReported, State: "blocked", Kind: "question"}, "blocked(question)"},
		{PeerState{Outcome: PeerReported, State: "working"}, "working"},
		{PeerState{Outcome: PeerNotReported}, "-"},
		{PeerState{Outcome: PeerNoAnswer}, "?"},
		{PeerState{Outcome: PeerRefused}, "?"},
	} {
		if got := c.st.Display(); got != c.want {
			t.Errorf("%+v: %q, want %q", c.st, got, c.want)
		}
	}
}

func TestPeerStateDeliveryDisplay(t *testing.T) {
	for _, c := range []struct {
		st   PeerState
		want string
	}{
		{PeerState{Outcome: PeerReported, State: "idle", Delivery: "ok"}, "ok"},
		{PeerState{Outcome: PeerNotReported, Delivery: "holding", DeliveryReason: "held_unacked"}, "holding(held_unacked)"},
		{PeerState{Outcome: PeerReported, State: "idle"}, "-"}, // answered, no delivery report: never "ok"
		{PeerState{Outcome: PeerNotReported}, "-"},
		{PeerState{Outcome: PeerNoAnswer}, "?"},
		{PeerState{Outcome: PeerRefused}, "?"},
	} {
		if got := c.st.DeliveryDisplay(); got != c.want {
			t.Errorf("%+v: %q, want %q", c.st, got, c.want)
		}
	}
}

func TestKnownDelivery(t *testing.T) {
	for _, c := range []struct {
		d    Delivery
		want bool
	}{
		{Delivery{Health: DeliveryOK}, true},
		{Delivery{DeliveryOK, "x"}, false},
		{Delivery{DeliveryHolding, DeliveryReasonHeldUnacked}, true},
		{Delivery{DeliveryDegraded, "disk full: /home/x"}, false},
		{Delivery{Health: "SECRET-ERR"}, false},
	} {
		if got := KnownDelivery(c.d); got != c.want {
			t.Errorf("%+v: %v, want %v", c.d, got, c.want)
		}
	}
}

func TestPeerActivityNoResponderIsQuick(t *testing.T) {
	f := newFixture(t, "a")
	a := f.client(t, "a")
	start := time.Now()
	if got := a.PeerActivity(context.Background(), "nobody"); got.Outcome != PeerNoAnswer {
		t.Fatalf("got %+v", got)
	}
	if d := time.Since(start); d > 500*time.Millisecond {
		t.Fatalf("no responders took %v", d)
	}
}

func TestPeerActivityNeverRepliesTimesOut(t *testing.T) {
	f := newFixture(t, "a")
	a := f.client(t, "a")
	nc := f.e.Conn(t, f.e.AdminCreds)
	if _, err := nc.Subscribe(sap.For(f.e.Owner, "mute").Status, func(*nats.Msg) {}); err != nil {
		t.Fatal(err)
	}
	_ = nc.Flush()
	start := time.Now()
	got := a.PeerActivity(context.Background(), "mute")
	d := time.Since(start)
	if got.Outcome != PeerNoAnswer || d < PeerActivityTimeout-100*time.Millisecond || d > PeerActivityTimeout+700*time.Millisecond {
		t.Fatalf("got %+v after %v", got, d)
	}
	// A caller that gives up sooner is not kept waiting.
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	start = time.Now()
	a2 := f.client(t, "a")
	if got := a2.PeerActivity(ctx, "mute"); got.Outcome != PeerNoAnswer || time.Since(start) > 600*time.Millisecond {
		t.Fatalf("cancelled: %+v after %v", got, time.Since(start))
	}
}

func TestPeerActivityRefusedByPermissions(t *testing.T) {
	f := newFixture(t, "a", "c")
	p := admin.PermissionsTo(f.e.Owner, "s", []string{"c"}) // may address c only
	c, u := f.e.MintRaw(t, "s", p)
	if _, err := f.adm.Provision(context.Background(), "s", u, f.e.Account, "", admin.Meta{}); err != nil {
		t.Fatal(err)
	}
	f.creds["s"], f.users["s"] = c, u
	s := f.client(t, "s")
	f.fakePeer(t, "b", peerStatusBody(`"program_status":{"state":"working"}`))
	if got := s.PeerActivity(context.Background(), "b"); got.Outcome != PeerRefused {
		t.Fatalf("b is not addressable by this sender: %+v", got)
	}
}

func TestPeerActivityThreeTokenAndLegacyResolve(t *testing.T) {
	f := newFixture(t, "a")
	a := f.client(t, "a")
	id, err := sap.New("bogworks", "team", "peer")
	if err != nil {
		t.Fatal(err)
	}
	for _, arg := range []string{id.Flat(), id.String()} {
		got, ok := a.peerStatusSubject(arg)
		if !ok || got != id.Subjects().Status {
			t.Errorf("%q resolves to %q (%v), want %q", arg, got, ok, id.Subjects().Status)
		}
	}
	if got, ok := a.peerStatusSubject("b"); !ok || got != sap.For(f.e.Owner, "b").Status {
		t.Errorf("legacy: %q %v", got, ok)
	}
	if _, ok := a.peerStatusSubject("ag.team"); ok {
		t.Error("two tokens are not an endpoint key")
	}
	if got := a.PeerActivity(context.Background(), "ag.team"); got.Outcome != PeerNoAnswer {
		t.Errorf("bad key: %+v", got)
	}
}

func TestPeerActivityCachesForFiveSeconds(t *testing.T) {
	f := newFixture(t, "a")
	a := f.client(t, "a")
	nc := f.e.Conn(t, f.e.AdminCreds)
	var n atomic.Int64
	if _, err := nc.Subscribe(sap.For(f.e.Owner, "cached").Status, func(m *nats.Msg) {
		n.Add(1)
		_ = m.Respond(peerStatusBody(`"program_status":{"state":"idle"}`))
	}); err != nil {
		t.Fatal(err)
	}
	_ = nc.Flush()
	for i := 0; i < 3; i++ {
		if got := a.PeerActivity(context.Background(), "cached"); got.State != "idle" {
			t.Fatalf("%+v", got)
		}
	}
	if n.Load() != 1 {
		t.Fatalf("%d requests for three calls", n.Load())
	}
	// Age the entry past the TTL.
	subj := sap.For(f.e.Owner, "cached").Status
	a.peers.mu.Lock()
	e := a.peers.cache[subj]
	e.at = e.at.Add(-PeerActivityCacheTTL - time.Second)
	a.peers.cache[subj] = e
	a.peers.mu.Unlock()
	a.PeerActivity(context.Background(), "cached")
	if n.Load() != 2 {
		t.Fatalf("%d requests after expiry, want 2", n.Load())
	}
}

func TestPeerActivityConcurrencyLimit(t *testing.T) {
	f := newFixture(t, "a")
	a := f.client(t, "a")
	nc := f.e.Conn(t, f.e.AdminCreds)
	var inFlight, peak atomic.Int64
	const peers = 20
	names := make([]string, peers)
	for i := range names {
		names[i] = "slow" + string(rune('a'+i))
		if _, err := nc.Subscribe(sap.For(f.e.Owner, names[i]).Status, func(m *nats.Msg) {
			cur := inFlight.Add(1)
			for {
				p := peak.Load()
				if cur <= p || peak.CompareAndSwap(p, cur) {
					break
				}
			}
			time.Sleep(60 * time.Millisecond)
			inFlight.Add(-1)
			_ = m.Respond(peerStatusBody(`"program_status":{"state":"working"}`))
		}); err != nil {
			t.Fatal(err)
		}
	}
	_ = nc.Flush()
	res := make(chan PeerState, peers)
	for _, n := range names {
		go func() { res <- a.PeerActivity(context.Background(), n) }()
	}
	for range names {
		if got := <-res; got.Outcome != PeerReported {
			t.Fatalf("%+v", got)
		}
	}
	if p := peak.Load(); p > PeerActivityConcurrency || p < 2 {
		t.Fatalf("peak %d in flight, want 2..%d", p, PeerActivityConcurrency)
	}
}

func TestPeerActivityRequestCarriesSignedSender(t *testing.T) {
	f := newFixture(t, "a")
	a := f.client(t, "a")
	nc := f.e.Conn(t, f.e.AdminCreds)
	subj := sap.For(f.e.Owner, "checker").Status
	got := make(chan sap.Trust, 1)
	user := make(chan string, 1)
	if _, err := nc.Subscribe(subj, func(m *nats.Msg) {
		h, tr := sap.VerifyLive(m.Header.Get(sap.HeaderName), m.Subject, m.Data, time.Now(), sap.NewNonceSet(time.Minute))
		got <- tr
		user <- h.User
		_ = m.Respond(peerStatusBody(`"program_status":{"state":"idle"}`))
	}); err != nil {
		t.Fatal(err)
	}
	_ = nc.Flush()
	if st := a.PeerActivity(context.Background(), "checker"); st.State != "idle" {
		t.Fatalf("%+v", st)
	}
	if tr := <-got; tr != sap.TrustVerified {
		t.Fatalf("status request trust %q, want verified", tr)
	}
	if u := <-user; u != f.users["a"] {
		t.Fatalf("signed by %q, want %q", u, f.users["a"])
	}
}
