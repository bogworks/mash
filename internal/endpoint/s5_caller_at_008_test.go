package endpoint

// AT-008 (REQ-008, REQ-015): S5 on the wire. The request subjects and headers of discovery and prompt are captured on
// the reference (the same steps with the clock and nonce fixed, s5TodayPrompt) and on main's own discovery and prompt
// sender through the S5 seam, and compared; reply subjects are not compared byte for byte (AT-016 checks them).
//
// Main's promptMsg draws its clock and nonce inside, so the two differ by ts, nonce and sig. Amendment 5 (N8) masks
// those three values from byte equality only on three conditions, all asserted: ts and the nonce are checked for form,
// the signature verifies over the captured bytes, and everything else is byte-equal. A drift guard checks the fixed
// reference's header against the real Prompt's. The mixed-version cases (old asker, new responder and the reverse) use
// today's code as the old side. The real-binary variants (the last released 0.5.x mash downloaded in a guest, and W's
// binary) are W's; the endpoint service of this commit is this package's service.go.

import (
	"bytes"
	"context"
	"encoding/json"
	"reflect"
	"regexp"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/nats-io/nats.go"

	"github.com/bogworks/mash/internal/sap"
)

// s5RawBob answers as bob without a service: the genuine registration to every discovery request, and "pong" and a
// terminator to every prompt. It keeps no nonce set, so the same signed request can be sent twice.
func (z *s5Fix) rawBob(t *testing.T, genuine []byte) {
	t.Helper()
	c := z.connect(t, z.bob)
	if _, err := c.nc.Subscribe("$SRV.INFO."+sap.ServiceName, func(m *nats.Msg) {
		if m.Reply != "" {
			_ = m.Respond(genuine)
		}
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := c.nc.Subscribe(mustSubj(z.bob).Prompt, func(m *nats.Msg) {
		if m.Reply != "" {
			_ = c.nc.Publish(m.Reply, []byte(`{"type":"response","data":"pong"}`))
			_ = c.nc.Publish(m.Reply, nil)
		}
	}); err != nil {
		t.Fatal(err)
	}
	if err := c.nc.Flush(); err != nil {
		t.Fatal(err)
	}
}

// s5TodayPrompt is Prompt (promptSend) with the signing clock and nonce given: it publishes the request main
// publishes and reads the reply chunks as main does, and returns the joined response text.
func s5TodayPrompt(ctx context.Context, nc *nats.Conn, id sap.Identity, name, subject, text string, in s5Inputs) (string, error) {
	inbox := secretInbox(nc)
	sub, err := nc.SubscribeSync(inbox)
	if err != nil {
		return "", err
	}
	defer sub.Unsubscribe()
	payload := []byte(text)
	h, err := id.Sign(subject, payload, name, in.Now(), in.Nonce)
	if err != nil {
		return "", err
	}
	m := nats.NewMsg(subject)
	m.Reply, m.Data = inbox, payload
	m.Header.Set(sap.HeaderName, h.Encode())
	if err := nc.PublishMsg(m); err != nil {
		return "", err
	}
	var out strings.Builder
	for {
		r, err := sub.NextMsg(5 * time.Second)
		if err != nil {
			return out.String(), err
		}
		if len(r.Data) == 0 && len(r.Header) == 0 {
			return out.String(), nil
		}
		var c struct {
			Type string `json:"type"`
			Data string `json:"data"`
		}
		if json.Unmarshal(r.Data, &c) == nil && c.Type == "response" {
			out.WriteString(c.Data)
		}
	}
}

// s5Wire is what a client put on the wire for one discovery and one prompt.
type s5Wire struct{ Discovery, Prompt s5Msg }

var s5SenderValues = regexp.MustCompile(`"(ts|nonce|sig)":"[^"]*"`)

// s5MaskSender is the headers of a message with the values of ts, nonce and sig replaced by a placeholder in the
// Agent-Sender value, so two headers compare byte for byte (field order, escaping, every other value) apart from them.
func s5MaskSender(h nats.Header) map[string][]string {
	out := map[string][]string{}
	for k, vs := range h {
		for _, v := range vs {
			if k == sap.HeaderName {
				v = s5SenderValues.ReplaceAllString(v, `"$1":"*"`)
			}
			out[k] = append(out[k], v)
		}
	}
	return out
}

var (
	s5TSForm    = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}Z$`)
	s5NonceForm = regexp.MustCompile(`^[A-Za-z0-9_-]{1,64}$`) // the form internal/sap accepts (nonceRE); nuid.Next gives 22 of it
)

// s5CheckSenderForm checks the two masked values by form: ts is UTC RFC 3339 as Sign writes it, nonce has the form
// main's nonce has. The signature is verified separately, over the captured bytes.
func s5CheckSenderForm(t *testing.T, what string, m s5Msg) {
	t.Helper()
	var h struct{ TS, Nonce string }
	if err := json.Unmarshal([]byte(m.Header.Get(sap.HeaderName)), &h); err != nil {
		t.Errorf("%s: Agent-Sender is not JSON: %q", what, m.Header.Get(sap.HeaderName))
		return
	}
	if !s5TSForm.MatchString(h.TS) {
		t.Errorf("%s: ts %q is not UTC RFC 3339 (2006-01-02T15:04:05Z)", what, h.TS)
	} else if _, err := time.Parse(time.RFC3339, h.TS); err != nil {
		t.Errorf("%s: ts %q does not parse: %v", what, h.TS, err)
	}
	if !s5NonceForm.MatchString(h.Nonce) {
		t.Errorf("%s: nonce %q does not have main's form", what, h.Nonce)
	}
}

// s5MaskedHeader is an Agent-Sender header with the fields that differ by clock, nonce and signature taken out.
func s5MaskedHeader(t *testing.T, m s5Msg) map[string]any {
	t.Helper()
	var h map[string]any
	if err := json.Unmarshal([]byte(m.Header.Get(sap.HeaderName)), &h); err != nil {
		t.Fatalf("Agent-Sender is not JSON: %q", m.Header.Get(sap.HeaderName))
	}
	delete(h, "ts")
	delete(h, "nonce")
	delete(h, "sig")
	return h
}

// todayWire captures main's discovery and prompt requests with the inputs fixed. The real Prompt is run as well and
// its header is compared (clock, nonce and signature masked) with the driver's, so the driver cannot drift from main.
func (z *s5Fix) todayWire(t *testing.T, in s5Inputs) s5Wire {
	t.Helper()
	tp := z.tap(t, "$SRV.INFO.>", "agents.prompt.>")
	c := z.connect(t, z.alice)
	id := identity(t, z.creds[z.alice.Flat()])
	subj, err := Discover(s5Ctx(t), c.nc, z.bob)
	if err != nil {
		t.Fatal(err)
	}
	if got, err := s5TodayPrompt(s5Ctx(t), c.nc, id, z.alice.Flat(), subj, "ping", in); err != nil || got != "pong" {
		t.Fatalf("today's driver prompt: %q, %v", got, err)
	}
	var out bytes.Buffer
	if err := Prompt(s5Ctx(t), c.nc, sdkIdentity(t, z.creds[z.alice.Flat()]), z.alice.Flat(), subj, "ping", &out); err != nil || out.String() != "pong" {
		t.Fatalf("today's Prompt: %q, %v", out.String(), err)
	}
	d, p := tp.waitRequestsFrom(t, z.alice, 1, 2)
	if len(d) != 1 || len(p) != 2 {
		t.Fatalf("captured %d discovery and %d prompt requests, want 1 and 2", len(d), len(p))
	}
	if !reflect.DeepEqual(s5MaskedHeader(t, p[0]), s5MaskedHeader(t, p[1])) || p[0].Subject != p[1].Subject || !bytes.Equal(p[0].Data, p[1].Data) {
		t.Fatalf("the test driver drifted from Prompt: driver %v %q, Prompt %v %q", s5MaskedHeader(t, p[0]), p[0].Data, s5MaskedHeader(t, p[1]), p[1].Data)
	}
	return s5Wire{Discovery: d[0], Prompt: p[0]}
}

// TestS5Today_WireOracle runs now and records main's request shape, the oracle AT-008 compares against: discovery is
// a request on $SRV.INFO.agents with no header and no payload; the prompt goes to the prompt subject the registration
// named, with the text as the payload and one Agent-Sender header whose fields are fixed by the inputs.
func TestS5Today_WireOracle(t *testing.T) {
	z := newS5Fix(t)
	z.rawBob(t, z.genuineRegistration(t, z.bob))
	in := s5Fixed()
	w := z.todayWire(t, in)
	if w.Discovery.Subject != "$SRV.INFO.agents" || len(w.Discovery.Header) != 0 || len(w.Discovery.Data) != 0 {
		t.Fatalf("discovery request: %+v", w.Discovery)
	}
	p := w.Prompt
	if p.Subject != mustSubj(z.bob).Prompt || string(p.Data) != "ping" || len(p.Header) != 1 || p.Header.Get(sap.HeaderName) == "" {
		t.Fatalf("prompt request: %+v", p)
	}
	h := s5MaskedHeader(t, p)
	if h["name"] != z.alice.Flat() || h["sub"] != p.Subject || h["user"] != z.users[z.alice.Flat()] || h["account"] != z.e.Account {
		t.Fatalf("Agent-Sender fields %v", h)
	}
	if !strings.Contains(p.Header.Get(sap.HeaderName), in.Nonce) || !strings.Contains(p.Header.Get(sap.HeaderName), in.Now().UTC().Format("2006-01-02T15:04:05Z")) {
		t.Fatalf("Agent-Sender %q does not carry the fixed clock and nonce", p.Header.Get(sap.HeaderName))
	}
}

// TestS5AT008_WireEqualsToday: main's own discovery and prompt sender, through the S5 seam, put the same request
// subjects, headers (ts, nonce and sig masked as N8 says) and payloads on the wire as the reference with the clock and
// nonce fixed. Any difference is listed and ruled on by the maintainer (REQ-008), so the test fails on it rather than hiding it.
func TestS5AT008_WireEqualsToday(t *testing.T) {
	needS5Caller(t, "AT-008 wire equality")
	z := newS5Fix(t)
	z.rawBob(t, z.genuineRegistration(t, z.bob))
	in := s5Fixed()
	want := z.todayWire(t, in)

	tp := z.tap(t, "$SRV.INFO.>", "agents.prompt.>")
	env, _ := z.env(t, z.alice, in)
	cl := newS5Caller(t, env)
	defer cl.Close()
	chunks, err := cl.Prompt(s5Ctx(t), z.bob, "ping")
	if err != nil || s5Join(chunks) != "pong" {
		t.Fatalf("the constructor's prompt: %q, %v", chunks, err)
	}
	d, p := tp.waitRequestsFrom(t, z.alice, 1, 1)
	if len(d) != 1 || len(p) != 1 {
		t.Fatalf("the constructor put %d discovery and %d prompt requests on the wire, want 1 and 1", len(d), len(p))
	}
	same := func(what string, got, exp s5Msg) {
		if got.Subject != exp.Subject {
			t.Errorf("%s subject: %q, main %q", what, got.Subject, exp.Subject)
		}
		if !bytes.Equal(got.Data, exp.Data) {
			t.Errorf("%s payload: %q, main %q", what, got.Data, exp.Data)
		}
		// Headers: byte-equal once the three values that depend on the clock, the nonce and the signature are masked
		// (N8), on the conditions that ts and nonce are format-checked and the signature verifies over the captured bytes.
		gh, eh := s5MaskSender(got.Header), s5MaskSender(exp.Header)
		if !reflect.DeepEqual(gh, eh) {
			t.Errorf("%s headers: %v, main %v", what, gh, eh)
		}
		if got.Header.Get(sap.HeaderName) != "" {
			s5CheckSenderForm(t, what+" (constructor)", got)
			_, trust := sap.VerifyStored(got.Header.Get(sap.HeaderName), got.Subject, got.Data)
			if trust != sap.TrustVerified {
				t.Errorf("%s: the constructor's Agent-Sender does not verify over the captured bytes: %s", what, trust)
			}
		}
	}
	same("discovery", d[0], want.Discovery)
	same("prompt", p[0], want.Prompt) // payload byte-equal to main's bare text (SAP 0.3 section 5.1): main's sender stays
	if string(p[0].Data) != "ping" {
		t.Errorf("prompt payload %q, want main's bare text %q", p[0].Data, "ping")
	}
}

// The four mixed-version cases. The old side is today's client code and today's service; the new side is the
// constructor (asker) and the endpoint service of this commit (responder). Expected in every case: the result two
// peers of one version give, no refusal, no timeout, the asker finds the endpoint online and the answer arrives. Each
// case captures the reply subject and checks it against the asker's layout and the golden (no permission violation).

// Case 1: discovery, old asker and new responder.
func TestS5AT008_Mixed1_OldAskerDiscoversNewResponder(t *testing.T) {
	z := newS5Fix(t)
	z.serveEcho(t, z.bob, nil)
	tp := z.tap(t, "$SRV.INFO.>")
	c := z.connect(t, z.alice)
	subj, err := Discover(s5Ctx(t), c.nc, z.bob)
	if err != nil || subj != mustSubj(z.bob).Prompt {
		t.Fatalf("old asker, new responder: %q, %v (want bob online)", subj, err)
	}
	d, _ := tp.waitRequestsFrom(t, z.alice, 1, 0)
	if len(d) != 1 || !s5Hex32.MatchString(strings.TrimPrefix(d[0].Reply, z.alice.InboxPrefix()+".")) {
		t.Fatalf("old asker's reply subject: %+v (want the prefix and one 32-hex token)", d)
	}
	if v := c.violations(); len(v) != 0 {
		t.Fatalf("golden refused: %q", v)
	}
}

// Case 2: discovery, new asker and old responder.
func TestS5AT008_Mixed2_NewAskerDiscoversOldResponder(t *testing.T) {
	needS5Caller(t, "AT-008 mixed case 2 (new asker, old responder, discovery)")
	z := newS5Fix(t)
	z.serveEcho(t, z.bob, nil)
	tp := z.tap(t, "$SRV.INFO.>")
	cl, c := z.caller(t, z.alice, s5Inputs{})
	got, err := cl.Discover(s5Ctx(t), z.bob)
	if err != nil || len(got.Accepted) != 1 || got.Accepted[0].Address != z.bob || got.NotVerified != 0 {
		t.Fatalf("new asker, old responder: %+v, %v (want bob online, nothing refused)", got, err)
	}
	d, _ := tp.waitRequestsFrom(t, z.alice, 1, 0)
	if len(d) != 1 {
		t.Fatalf("captured %d discovery requests", len(d))
	}
	if _, problem := s5Layout(z.alice, d[0].Reply); problem != "" {
		t.Fatal(problem)
	}
	if v := c.violations(); len(v) != 0 {
		t.Fatalf("golden refused: %q", v)
	}
}

// Case 3: prompt, old asker and new responder.
func TestS5AT008_Mixed3_OldAskerPromptsNewResponder(t *testing.T) {
	z := newS5Fix(t)
	z.serveEcho(t, z.bob, func(ep *Endpoint, p PromptEvent) {
		_ = ep.Reply(p.RequestID, "one ", false)
		_ = ep.Reply(p.RequestID, "two", true)
	})
	tp := z.tap(t, "agents.prompt.>")
	c := z.connect(t, z.alice)
	var out bytes.Buffer
	subj, err := Discover(s5Ctx(t), c.nc, z.bob)
	if err != nil {
		t.Fatal(err)
	}
	if err := Prompt(s5Ctx(t), c.nc, sdkIdentity(t, z.creds[z.alice.Flat()]), z.alice.Flat(), subj, "ping", &out); err != nil || out.String() != "one two" {
		t.Fatalf("old asker, new responder: %q, %v (want both chunks)", out.String(), err)
	}
	_, p := tp.waitRequestsFrom(t, z.alice, 0, 1)
	if len(p) != 1 || !s5Hex32.MatchString(strings.TrimPrefix(p[0].Reply, z.alice.InboxPrefix()+".")) {
		t.Fatalf("old asker's prompt reply subject: %+v", p)
	}
	if v := c.violations(); len(v) != 0 {
		t.Fatalf("golden refused: %q", v)
	}
}

// Case 4: prompt, new asker and old responder.
func TestS5AT008_Mixed4_NewAskerPromptsOldResponder(t *testing.T) {
	needS5Caller(t, "AT-008 mixed case 4 (new asker, old responder, prompt)")
	z := newS5Fix(t)
	z.serveEcho(t, z.bob, func(ep *Endpoint, p PromptEvent) {
		_ = ep.Reply(p.RequestID, "one ", false)
		_ = ep.Reply(p.RequestID, "two", true)
	})
	tp := z.tap(t, "agents.prompt.>")
	cl, c := z.caller(t, z.alice, s5Inputs{})
	chunks, err := cl.Prompt(s5Ctx(t), z.bob, "ping")
	if err != nil || s5Join(chunks) != "one two" {
		t.Fatalf("new asker, old responder: %q, %v (want both chunks)", chunks, err)
	}
	_, p := tp.waitRequestsFrom(t, z.alice, 0, 1)
	if len(p) != 1 {
		t.Fatalf("captured %d prompt requests", len(p))
	}
	if _, problem := s5Layout(z.alice, p[0].Reply); problem != "" {
		t.Fatal(problem)
	}
	if v := c.violations(); len(v) != 0 {
		t.Fatalf("golden refused: %q", v)
	}
}

// A prompt over 32 KiB to a mash peer gets the same result as on main (#1536 is not fixed here): main refuses it
// before sending (413 prompt_too_large, mash's words), the endpoint's prompt handler is never called, and the text of
// exactly MaxPromptBytes is accepted.
func s5OverLimit(t *testing.T, z *s5Fix, prompt func(text string) (string, error)) {
	t.Helper()
	var handled atomic.Int32
	z.serveEcho(t, z.bob, func(ep *Endpoint, p PromptEvent) {
		handled.Add(1)
		_ = ep.Reply(p.RequestID, "ok", true)
	})
	tp := z.tap(t, "agents.prompt.>")
	// Listed difference, not a failure (REQ-008, #1536): the 32768-byte boundary (MaxPromptBytes versus the server's
	// payload limit including headers) is main's behaviour and is kept; only equality with main is asserted below.
	t.Logf("difference listed (#1536): the %d-byte prompt boundary is as on main, not changed here", MaxPromptBytes)
	at := strings.Repeat("x", MaxPromptBytes)
	if got, err := prompt(at); err != nil || got != "ok" {
		t.Fatalf("a prompt of exactly %d bytes: %q, %v", MaxPromptBytes, got, err)
	}
	s5Wait(t, "the tap to see the exact-size prompt", 5*time.Second, func() bool { return len(tp.all()) >= 1 })
	before := len(tp.all())
	_, err := prompt(at + "x")
	if err == nil || !strings.Contains(err.Error(), PromptTooLargeCode) {
		t.Fatalf("a prompt over %d bytes: %v (want a refusal naming %s)", MaxPromptBytes, err, PromptTooLargeCode)
	}
	time.Sleep(300 * time.Millisecond)
	if n := len(tp.all()); n != before {
		t.Fatalf("the oversized prompt was published (%d requests, was %d)", n, before)
	}
	if handled.Load() != 1 {
		t.Fatalf("the endpoint's prompt handler ran %d times, want 1 (the exact-size prompt only)", handled.Load())
	}
}

func TestS5Today_PromptOver32KiB(t *testing.T) {
	z := newS5Fix(t)
	c := z.connect(t, z.alice)
	s5OverLimit(t, z, func(text string) (string, error) {
		subj, err := Discover(s5Ctx(t), c.nc, z.bob)
		if err != nil {
			return "", err
		}
		var out bytes.Buffer
		err = Prompt(s5Ctx(t), c.nc, sdkIdentity(t, z.creds[z.alice.Flat()]), z.alice.Flat(), subj, text, &out)
		return out.String(), err
	})
}

func TestS5AT008_PromptOver32KiBAsOnMain(t *testing.T) {
	needS5Caller(t, "AT-008 prompt over 32 KiB")
	z := newS5Fix(t)
	cl, _ := z.caller(t, z.alice, s5Inputs{})
	s5OverLimit(t, z, func(text string) (string, error) {
		chunks, err := cl.Prompt(s5Ctx(t), z.bob, text)
		return s5Join(chunks), err
	})
}
