package endpoint

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/micro"

	"github.com/bogworks/mash/internal/sap"
)

// serve runs name with its SAP service; prompts go to onPrompt.
func (f *fixture) serve(t *testing.T, name string, onPrompt func(*Endpoint, PromptEvent)) *Endpoint {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	ep, err := Open(ctx, f.cfg(name))
	if err != nil {
		t.Fatal(err)
	}
	if err := ep.StartService(ctx, func(p PromptEvent) { onPrompt(ep, p) }); err != nil {
		t.Fatal(err)
	}
	ev := &events{}
	done := make(chan struct{})
	go func() { _ = ep.Run(ctx, ev.deliver); close(done) }()
	t.Cleanup(func() { cancel(); <-done; ep.Close() })
	return ep
}

func noPrompt(*Endpoint, PromptEvent) {}

func TestRegistrationMetadata(t *testing.T) {
	f := newFixture(t, "a", "b")
	a := f.serve(t, "a", noPrompt)
	nc := f.e.ConnAs(t, f.creds["b"], "b")
	m, err := nc.Request("$SRV.INFO.agents", nil, 2*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	var info micro.Info
	if err := json.Unmarshal(m.Data, &info); err != nil {
		t.Fatal(err)
	}
	md := info.Metadata
	if info.Name != "agents" || md["agent"] != "claude-code" || md["owner"] != "o" || md["session"] != "a" ||
		md["protocol_version"] != "0.3" || md["user_nkey"] != f.users["a"] || md["account"] != f.e.Account ||
		md["mail_subject"] != "agents.mail.cc.o.a" {
		t.Fatalf("service info %+v", info)
	}
	if info.ID != a.Instance() {
		t.Fatalf("instance id %q vs %q", info.ID, a.Instance())
	}
	eps := map[string]micro.EndpointInfo{}
	for _, e := range info.Endpoints {
		eps[e.Name] = e
	}
	p, s := eps["prompt"], eps["status"]
	if p.Subject != "agents.prompt.cc.o.a" || p.QueueGroup != "agents" || p.Metadata["min_sender_trust"] != "signed" ||
		p.Metadata["max_payload"] != "1MB" || p.Metadata["attachments_ok"] != "false" {
		t.Fatalf("prompt endpoint %+v", p)
	}
	if s.Subject != "agents.status.cc.o.a" || s.QueueGroup != "agents" {
		t.Fatalf("status endpoint %+v", s)
	}
	if _, ok := eps["mail"]; ok {
		t.Fatal("mail must be declared, not registered (it would answer the JetStream ack)")
	}
	if !sap.VerifyIDSig(md["user_nkey"], md["account"], "claude-code", "o", p.Subject, md["id_sig"]) {
		t.Fatal("id_sig does not verify")
	}
}

type hbPayload struct {
	Agent           string `json:"agent"`
	Owner           string `json:"owner"`
	Session         string `json:"session"`
	InstanceID      string `json:"instance_id"`
	TS              string `json:"ts"`
	IntervalS       int    `json:"interval_s"`
	ProtocolVersion string `json:"protocol_version"`
	Endpoints       map[string]struct {
		Subject  string            `json:"subject"`
		Metadata map[string]string `json:"metadata"`
	} `json:"endpoints"`
}

func TestHeartbeatSigned(t *testing.T) {
	f := newFixture(t, "a", "b")
	nc := f.e.ConnAs(t, f.creds["b"], "b")
	sub, err := nc.SubscribeSync("agents.hb.cc.o.a")
	if err != nil {
		t.Fatal(err)
	}
	a := f.serve(t, "a", noPrompt)
	m, err := sub.NextMsg(3 * time.Second)
	if err != nil {
		t.Fatal("no heartbeat:", err)
	}
	var hb hbPayload
	if err := json.Unmarshal(m.Data, &hb); err != nil {
		t.Fatal(err)
	}
	if hb.Agent != "claude-code" || hb.Owner != "o" || hb.Session != "a" || hb.InstanceID != a.Instance() ||
		hb.IntervalS != 30 || hb.ProtocolVersion != "0.3" ||
		hb.Endpoints["mail"].Subject != "agents.mail.cc.o.a" || hb.Endpoints["prompt"].Subject != "agents.prompt.cc.o.a" ||
		hb.Endpoints["prompt"].Metadata["min_sender_trust"] != "signed" {
		t.Fatalf("heartbeat %s", m.Data)
	}
	if _, tr := sap.VerifyLive(m.Header.Get(sap.HeaderName), m.Subject, m.Data, time.Now(), sap.NewNonceSet(time.Minute)); tr != sap.TrustVerified {
		t.Fatalf("heartbeat signature: %s", tr)
	}
}

func TestWhoShowsOnline(t *testing.T) {
	f := newFixture(t, "a", "b")
	f.serve(t, "a", noPrompt)
	b := f.serve(t, "b", noPrompt)
	// Presence is asked for, not heard: WhoFresh probes, and both running endpoints answer (ADR 0066).
	ps, err := b.WhoFresh(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if online := onlineSet(ps); !online["a"] || !online["b"] {
		t.Fatalf("presence %+v", ps)
	}
}

func TestStatusReply(t *testing.T) {
	f := newFixture(t, "a", "b")
	a := f.serve(t, "a", noPrompt)
	m, err := f.e.ConnAs(t, f.creds["b"], "b").Request("agents.status.cc.o.a", nil, 2*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	var hb hbPayload
	if err := json.Unmarshal(m.Data, &hb); err != nil || hb.InstanceID != a.Instance() || hb.Session != "a" {
		t.Fatalf("status %s %v", m.Data, err)
	}
	if m.Header.Get(sap.HeaderName) != "" {
		t.Fatal("a status reply must not be signed (SAP §13.11)")
	}
}

func TestDiscoverAndPromptReply(t *testing.T) {
	f := newFixture(t, "a", "b")
	got := make(chan PromptEvent, 1)
	f.serve(t, "a", func(ep *Endpoint, p PromptEvent) {
		got <- p
		_ = ep.Reply(p.RequestID, "pong ", false)
		_ = ep.Reply(p.RequestID, "from a", true)
	})
	b := f.client(t, "b")
	subj, err := Discover(context.Background(), b.Conn(), "o", "a")
	if err != nil || subj != "agents.prompt.cc.o.a" {
		t.Fatalf("discover: %q %v", subj, err)
	}
	var out bytes.Buffer
	if err := Prompt(context.Background(), b.Conn(), b.Identity(), "b", subj, "ping", &out); err != nil {
		t.Fatal(err)
	}
	if out.String() != "pong from a" {
		t.Fatalf("reply %q", out.String())
	}
	p := <-got
	if p.From != "b" || p.Trust != sap.TrustVerified || p.Text != "ping" || p.RequestID == "" {
		t.Fatalf("event %+v", p)
	}
}

func TestPromptEnvelopeJSON(t *testing.T) {
	f := newFixture(t, "a", "b")
	got := make(chan PromptEvent, 1)
	f.serve(t, "a", func(ep *Endpoint, p PromptEvent) { got <- p; _ = ep.Reply(p.RequestID, "ok", true) })
	b := f.client(t, "b")
	chunks := promptRaw(t, b, "agents.prompt.cc.o.a", []byte("  {\"prompt\":\"json hi\",\"extra\":1}"), true)
	if !strings.Contains(strings.Join(chunks, "|"), `"response"`) {
		t.Fatalf("chunks %q", chunks)
	}
	if p := <-got; p.Text != "json hi" {
		t.Fatalf("text %q", p.Text)
	}
}

// promptRaw sends a request (signed or not) and returns every reply message,
// rendering an error message as "ERR <code>" and the terminator as "END".
func promptRaw(t *testing.T, ep *Endpoint, subj string, payload []byte, sign bool) []string {
	t.Helper()
	nc := ep.Conn()
	inbox := nc.NewInbox()
	sub, err := nc.SubscribeSync(inbox)
	if err != nil {
		t.Fatal(err)
	}
	defer sub.Unsubscribe()
	m := nats.NewMsg(subj)
	m.Reply = inbox
	m.Data = payload
	if sign {
		h, _ := ep.Identity().Sign(subj, payload, ep.Name(), time.Now(), "")
		m.Header.Set(sap.HeaderName, h.Encode())
	}
	if err := nc.PublishMsg(m); err != nil {
		t.Fatal(err)
	}
	var out []string
	for {
		r, err := sub.NextMsg(5 * time.Second)
		if err != nil {
			t.Fatalf("after %q: %v", out, err)
		}
		if code := r.Header.Get("Nats-Service-Error-Code"); code != "" {
			out = append(out, "ERR "+code)
			continue
		}
		if len(r.Data) == 0 && len(r.Header) == 0 {
			return append(out, "END")
		}
		out = append(out, string(r.Data))
	}
}

func TestPromptUnsigned401(t *testing.T) {
	f := newFixture(t, "a", "b")
	f.serve(t, "a", func(*Endpoint, PromptEvent) { t.Error("unsigned prompt reached the session") })
	got := promptRaw(t, f.client(t, "b"), "agents.prompt.cc.o.a", []byte("hi"), false)
	if strings.Join(got, "|") != "ERR 401|END" {
		t.Fatalf("got %q", got)
	}
}

func TestPromptBadEnvelope400(t *testing.T) {
	f := newFixture(t, "a", "b")
	f.serve(t, "a", func(*Endpoint, PromptEvent) { t.Error("bad envelope reached the session") })
	b := f.client(t, "b")
	for _, body := range []string{`{"nope":1}`, `{bad json`, `{"prompt":"x","attachments":[{"filename":"f","content":"AA=="}]}`} {
		if got := promptRaw(t, b, "agents.prompt.cc.o.a", []byte(body), true); strings.Join(got, "|") != "ERR 400|END" {
			t.Errorf("%s: got %q", body, got)
		}
	}
}

func TestPromptWorkingKeepaliveAndTimeout(t *testing.T) {
	oldK, oldT := PromptKeepalive, PromptTimeout
	PromptKeepalive, PromptTimeout = 50*time.Millisecond, 300*time.Millisecond
	t.Cleanup(func() { PromptKeepalive, PromptTimeout = oldK, oldT })
	f := newFixture(t, "a", "b")
	f.serve(t, "a", noPrompt) // never replies
	got := promptRaw(t, f.client(t, "b"), "agents.prompt.cc.o.a", []byte("hello?"), true)
	joined := strings.Join(got, "|")
	if !strings.HasPrefix(joined, `{"type":"status","data":"ack"}`) || !strings.Contains(joined, `{"type":"status","data":"working"}`) ||
		!strings.HasSuffix(joined, "ERR 500|END") {
		t.Fatalf("got %q", got)
	}
}

func TestReplyUnknownRequest(t *testing.T) {
	f := newFixture(t, "a")
	a := f.serve(t, "a", noPrompt)
	if err := a.Reply("nope", "x", true); err == nil {
		t.Fatal("reply to an unknown request must fail")
	}
}

// Two final replies race: exactly one wins, and nothing follows the terminator.
func TestPromptNothingAfterTerminator(t *testing.T) {
	f := newFixture(t, "a", "b")
	results := make(chan error, 2)
	f.serve(t, "a", func(ep *Endpoint, p PromptEvent) {
		start := make(chan struct{})
		for i := 0; i < 2; i++ {
			go func(i int) { <-start; results <- ep.Reply(p.RequestID, fmt.Sprintf("answer %d", i), true) }(i)
		}
		close(start)
	})
	b := f.client(t, "b")
	nc := b.Conn()
	inbox := nc.NewInbox()
	sub, _ := nc.SubscribeSync(inbox)
	m := nats.NewMsg("agents.prompt.cc.o.a")
	m.Reply, m.Data = inbox, []byte("race")
	h, _ := b.Identity().Sign(m.Subject, m.Data, "b", time.Now(), "")
	m.Header.Set(sap.HeaderName, h.Encode())
	if err := nc.PublishMsg(m); err != nil {
		t.Fatal(err)
	}
	var seen []string
	ended := false
	for {
		r, err := sub.NextMsg(700 * time.Millisecond)
		if err != nil {
			break
		}
		if len(r.Data) == 0 && len(r.Header) == 0 {
			if ended {
				t.Fatal("two terminators")
			}
			ended = true
			continue
		}
		if ended {
			t.Fatalf("a chunk after the terminator: %s (stream %q)", r.Data, seen)
		}
		seen = append(seen, string(r.Data))
	}
	if !ended {
		t.Fatalf("no terminator: %q", seen)
	}
	ok := 0
	for i := 0; i < 2; i++ {
		if <-results == nil {
			ok++
		}
	}
	if ok != 1 {
		t.Fatalf("%d replies succeeded; exactly one may", ok)
	}
}

// A StartService that fails after registering (here the flush) leaves its service stopped: it would otherwise take
// prompts with no heartbeat (codex H1 on #20). It stays installed, so a callback from before the stop finds it.
func TestStartServiceFailureLeavesNoService(t *testing.T) {
	old := serviceFlush
	serviceFlush = func(*nats.Conn) error { return errors.New("simulated flush timeout") }
	t.Cleanup(func() { serviceFlush = old })
	f := newFixture(t, "a", "b")
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	ep, err := Open(ctx, f.cfg("a"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(ep.Close)
	if err := ep.StartService(ctx, func(PromptEvent) {}); err == nil || !strings.Contains(err.Error(), "simulated flush timeout") {
		t.Fatalf("StartService: %v", err)
	}
	if s := ep.svc(); s == nil {
		t.Fatal("the failed service was removed: a prompt callback still running would find none")
	} else {
		select {
		case <-s.quit: // stopped: no heartbeat, no endpoints
		default:
			t.Fatal("the failed service was not stopped")
		}
	}
	nc := f.e.ConnAs(t, f.creds["b"], "b")
	if _, err := nc.Request("$SRV.INFO.agents", nil, 500*time.Millisecond); err == nil {
		t.Fatal("the failed service still answers $SRV.INFO")
	}
}

func TestPromptLimit(t *testing.T) {
	f := newFixture(t, "a", "b")
	got := make(chan PromptEvent, 4)
	a := f.serve(t, "a", func(ep *Endpoint, p PromptEvent) { got <- p; _ = ep.Reply(p.RequestID, "ok", true) })
	b := f.client(t, "b")

	at := strings.Repeat("x", MaxPromptBytes)
	var out bytes.Buffer
	if err := Prompt(context.Background(), b.Conn(), b.Identity(), "b", "agents.prompt.cc.o.a", at, &out); err != nil || out.String() != "ok" {
		t.Fatalf("at the limit: %q %v", out.String(), err)
	}
	if p := <-got; p.Text != at {
		t.Fatalf("delivered %d bytes, want %d", len(p.Text), len(at))
	}

	over := at + "x"
	// Raw, past the client's own check: the service answers the error and nothing else, with no ack.
	if r := promptRaw(t, b, "agents.prompt.cc.o.a", []byte(over), true); strings.Join(r, "|") != "ERR "+PromptTooLargeCode+"|END" {
		t.Fatalf("over the limit: %q", r)
	}
	// The same in an envelope.
	env, _ := json.Marshal(map[string]string{"prompt": over})
	if r := promptRaw(t, b, "agents.prompt.cc.o.a", env, true); strings.Join(r, "|") != "ERR "+PromptTooLargeCode+"|END" {
		t.Fatalf("envelope over the limit: %q", r)
	}
	// What the caller's Prompt returns for the service's refusal.
	err := promptSend(context.Background(), b.Conn(), b.Identity(), "b", "agents.prompt.cc.o.a", over, &out)
	want := "prompt_too_large: a live prompt is at most 32768 bytes; send the longer text as mail"
	if err == nil || !strings.Contains(err.Error(), PromptTooLargeCode) || !strings.Contains(err.Error(), want) {
		t.Fatalf("service refusal: %v", err)
	}
	// And the client's pre-check says the same before sending.
	if err := Prompt(context.Background(), b.Conn(), b.Identity(), "b", "agents.prompt.cc.o.a", over, &out); err == nil || !strings.Contains(err.Error(), want) {
		t.Fatalf("client pre-check: %v", err)
	}
	select {
	case p := <-got:
		t.Fatalf("the session was told of an oversized prompt (%d bytes)", len(p.Text))
	case <-time.After(200 * time.Millisecond):
	}
	svc := a.svc()
	svc.mu.Lock()
	open := len(svc.prompts)
	svc.mu.Unlock()
	if open != 0 {
		t.Fatalf("%d request ids are open after refused prompts", open)
	}
}
