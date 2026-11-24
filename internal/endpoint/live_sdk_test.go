package endpoint

import (
	"bytes"
	"context"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/nats-io/nats.go"

	"github.com/bogworks/mash/internal/sap"
	"github.com/bogworks/mash/internal/sapsdk"
)

// The live prompt path and the signing paths through the SDK adapter (stage W, part 1: CHG-5 to CHG-8, CHG-13,
// REQ-020). NEEDS A NATS SERVER (testenv.Start, in process): run only in a throwaway container.

// promptReplies publishes a request with the given Agent-Sender values (none when values is nil) and returns every
// reply as "ERR <code> <text>", a response chunk as its data, and the terminator as "END".
func promptReplies(t *testing.T, nc *nats.Conn, subj string, payload []byte, values []string) []string {
	t.Helper()
	inbox := nc.NewInbox()
	sub, err := nc.SubscribeSync(inbox)
	if err != nil {
		t.Fatal(err)
	}
	defer sub.Unsubscribe()
	m := nats.NewMsg(subj)
	m.Reply, m.Data = inbox, payload
	for _, v := range values {
		m.Header.Add(sap.HeaderName, v)
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
			out = append(out, "ERR "+code+" "+r.Header.Get("Nats-Service-Error"))
			continue
		}
		if len(r.Data) == 0 && len(r.Header) == 0 {
			return append(out, "END")
		}
		out = append(out, string(r.Data))
	}
}

// answering is a prompt callback that answers at once and counts what reached the session.
func answering(n *atomic.Int32) func(*Endpoint, PromptEvent) {
	return func(ep *Endpoint, p PromptEvent) { n.Add(1); _ = ep.Reply(p.RequestID, "ok", true) }
}

// CHG-5, CHG-6: a malformed header is 400 with today's text, whatever its shape; an absent one keeps 401.
func TestLivePromptMalformedIs400(t *testing.T) {
	f := newFixture(t, "a", "b")
	var reached atomic.Int32
	f.serve(t, "a", answering(&reached))
	b := f.client(t, "b")
	subj, payload := taddr("a").Subjects().Prompt, []byte("hi")
	good, err := b.Identity().Sign(subj, payload, tkey("b"), time.Now(), "")
	if err != nil {
		t.Fatal(err)
	}
	badSig := good
	badSig.Sig = "AAAA" // shape 8: not base64url of 64 bytes
	const want = "ERR 400 malformed Agent-Sender header|END"
	for name, values := range map[string][]string{
		"not json":           {"not json"},
		"unreadable sig (8)": {badSig.Encode()},
		"empty value (9)":    {""},
		"two values (10)":    {good.Encode(), good.Encode()},
	} {
		if got := strings.Join(promptReplies(t, b.Conn(), subj, payload, values), "|"); got != want {
			t.Errorf("%s: got %q, want %q", name, got, want)
		}
	}
	if got := strings.Join(promptReplies(t, b.Conn(), subj, payload, nil), "|"); got !=
		"ERR 401 a signed Agent-Sender header is required (min_sender_trust=signed)|END" {
		t.Errorf("no header: got %q", got)
	}
	if n := reached.Load(); n != 0 {
		t.Errorf("%d refused prompts reached the session", n)
	}
}

// CHG-7: a correctly signed, listed sender past the nonce cap gets 401 with its own text, not the signed-required one.
func TestLivePromptOverNonceCap401(t *testing.T) {
	f := newFixture(t, "a", "b")
	var reached atomic.Int32
	a := f.serve(t, "a", answering(&reached))
	a.mu.RLock()
	a.service.nonces = sapsdk.NewNonceSetCaps(0, 0, 2) // two pairs for one sender
	a.mu.RUnlock()
	b := f.client(t, "b")
	subj := taddr("a").Subjects().Prompt
	for i := 0; i < 2; i++ {
		var out bytes.Buffer
		if err := Prompt(context.Background(), b.Conn(), b.Identity(), tkey("b"), subj, "ping", &out); err != nil {
			t.Fatalf("prompt %d: %v", i, err)
		}
	}
	var out bytes.Buffer
	err := Prompt(context.Background(), b.Conn(), b.Identity(), tkey("b"), subj, "ping", &out)
	const want = "mash: prompt refused: 401 too many recent prompts: try again shortly"
	if err == nil || err.Error() != want || out.Len() != 0 {
		t.Fatalf("third prompt: err %v, out %q, want %q", err, out.String(), want)
	}
	if n := reached.Load(); n != 2 {
		t.Errorf("%d prompts reached the session, want 2", n)
	}
}

// CHG-13, REQ-020: a sender not in the directory gets 403 for the request and for its replay, and no nonce is
// recorded; a listed sender's replay is still refused.
func TestLivePromptUnlistedReplay403ListedReplayRefused(t *testing.T) {
	f := newFixture(t, "a", "b")
	var reached atomic.Int32
	a := f.serve(t, "a", answering(&reached))
	subj, payload := taddr("a").Subjects().Prompt, []byte("let me in")

	xcreds, _ := f.e.Mint(t, taddr("x")) // a valid user with its own key, never provisioned
	xnc := f.e.ConnAs(t, xcreds, tkey("x"))
	xid, err := readIdentity(xcreds)
	if err != nil {
		t.Fatal(err)
	}
	xh, err := xid.Sign(subj, payload, tkey("x"), time.Now(), "replay-x")
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		if got := strings.Join(promptReplies(t, xnc, subj, payload, []string{xh.Encode()}), "|"); got != "ERR 403 the sender is not a mash endpoint|END" {
			t.Errorf("unlisted request %d: %q", i+1, got)
		}
	}
	a.mu.RLock()
	set, ok := a.service.nonces.(*sapsdk.SDKNonceSet)
	a.mu.RUnlock()
	if !ok || set.Len() != 0 || set.Contains(xid.User, "replay-x") {
		t.Errorf("an unlisted sender left a nonce behind (set %T, len %v)", set, ok && set.Len() != 0)
	}

	b := f.client(t, "b")
	bh, err := b.Identity().Sign(subj, payload, tkey("b"), time.Now(), "replay-b")
	if err != nil {
		t.Fatal(err)
	}
	if got := promptReplies(t, b.Conn(), subj, payload, []string{bh.Encode()}); got[len(got)-1] != "END" || strings.HasPrefix(got[0], "ERR") {
		t.Fatalf("listed first request: %q", got)
	}
	if got := strings.Join(promptReplies(t, b.Conn(), subj, payload, []string{bh.Encode()}), "|"); got !=
		"ERR 401 a signed Agent-Sender header is required (min_sender_trust=signed)|END" {
		t.Errorf("listed replay: %q", got)
	}
	if n := reached.Load(); n != 1 {
		t.Errorf("%d prompts reached the session, want 1", n)
	}
}

// CHG-8, TO-8: a signing refusal sends nothing, and the error is mash's class words (never SDK text).
func TestSigningRefusalSendsNothing(t *testing.T) {
	f := newFixture(t, "a", "b")
	var reached, seen atomic.Int32
	f.serve(t, "a", answering(&reached))
	b := f.client(t, "b")
	subj := taddr("a").Subjects().Prompt
	ta := f.e.ConnAs(t, f.creds["a"], tkey("a"))
	if _, err := ta.Subscribe(subj, func(*nats.Msg) { seen.Add(1) }); err != nil {
		t.Fatal(err)
	}
	if err := ta.Flush(); err != nil {
		t.Fatal(err)
	}

	// The prompt: a display name over 64 UTF-16 units is refused by the SDK.
	var out bytes.Buffer
	err := Prompt(context.Background(), b.Conn(), b.Identity(), strings.Repeat("n", 65), subj, "ping", &out)
	if err == nil || err.Error() != "not sent: could not sign: your name is too long" || out.Len() != 0 {
		t.Fatalf("prompt: err %v, out %q", err, out.String())
	}
	// An identity that cannot sign at all takes the form with no cause.
	err = Prompt(context.Background(), b.Conn(), sapsdk.Identity{}, tkey("b"), subj, "ping", &out)
	if err == nil || err.Error() != "not sent: could not sign" {
		t.Fatalf("prompt, no key: %v", err)
	}

	// The send: the endpoint's SDK identity refuses, and the recipient's inbox stays empty.
	b.sid = sapsdk.Identity{}
	if _, err := b.Send(context.Background(), tdot("a"), "ready?", 0); err == nil || err.Error() != "not sent: could not sign" {
		t.Fatalf("send: %v", err)
	}
	st, err := adminJS(t, f).Stream(context.Background(), taddr("a").InboxStream())
	if err != nil {
		t.Fatal(err)
	}
	if info, err := st.Info(context.Background()); err != nil || info.State.Msgs != 0 {
		t.Fatalf("inbox after a refused send: %+v %v", info, err)
	}
	if err := ta.Flush(); err != nil {
		t.Fatal(err)
	}
	if n, r := seen.Load(), reached.Load(); n != 0 || r != 0 {
		t.Fatalf("a refused signing published %d messages, %d reached the session", n, r)
	}
}
