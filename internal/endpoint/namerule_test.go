package endpoint

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bogworks/mash/internal/config"
	"github.com/bogworks/mash/internal/namerule"
	"github.com/bogworks/mash/internal/render"
)

// ruleOf loads a name-rule file that refuses each of the given tokens, one rule per entry: "instance:bad" refuses the
// instance token bad, "group:team" the group token team, "owner:elsewhere" the owner token elsewhere.
func ruleOf(t *testing.T, refuse ...string) *namerule.Rule {
	t.Helper()
	var rules []string
	for _, r := range refuse {
		key, tok, _ := strings.Cut(r, ":")
		rules = append(rules, `{"id":"no-`+tok+`","match":{"`+key+`":{"is":"`+tok+`"}},"message":"`+tok+` is not allowed here"}`)
	}
	p := filepath.Join(t.TempDir(), "rule.json")
	if err := os.WriteFile(p, []byte(`{"version":1,"rules":[`+strings.Join(rules, ",")+`]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	r, err := namerule.Load(p)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

// wantRefusal fails unless err is a name-rule refusal of rule id carrying its message, raised at the entry point.
func wantRefusal(t *testing.T, err error, id, point string) {
	t.Helper()
	var ref *namerule.Refusal
	if !errors.As(err, &ref) {
		t.Fatalf("error %v is not a name-rule refusal", err)
	}
	if ref.RuleID != id || !strings.Contains(err.Error(), ref.Message) || !strings.Contains(err.Error(), "name rule "+`"`+id+`"`) {
		t.Fatalf("refusal %+v does not name %q and carry its message: %v", ref, id, err)
	}
	if !strings.Contains(err.Error(), point) {
		t.Fatalf("error %q does not name the entry point %q", err, point)
	}
	if strings.Contains(err.Error(), "no endpoint named") || strings.Contains(err.Error(), "not provisioned") {
		t.Fatalf("a refusal read as not found: %v", err)
	}
}

func (f *fixture) cfgRule(name string, r *namerule.Rule) config.Config {
	c := f.cfg(name)
	c.NameRule = r
	return c
}

// (a) start: the endpoint's own identity, in Open and in OpenClient, before anything is connected or read.
func TestNameRuleStart(t *testing.T) {
	f := newFixture(t, "a", "bad")
	r := ruleOf(t, "instance:bad")
	if _, err := Open(context.Background(), f.cfgRule("bad", r)); err == nil {
		t.Fatal("Open started a refused identity")
	} else {
		wantRefusal(t, err, "no-bad", "start")
	}
	if _, err := OpenClient(context.Background(), f.cfgRule("bad", r)); err == nil {
		t.Fatal("OpenClient started a refused identity")
	} else {
		wantRefusal(t, err, "no-bad", "start")
	}
	// Refused before the credentials are read: a missing creds file does not hide the rule.
	c := f.cfgRule("bad", r)
	c.Creds = filepath.Join(t.TempDir(), "missing.creds")
	if _, err := OpenClient(context.Background(), c); err == nil {
		t.Fatal("started")
	} else {
		wantRefusal(t, err, "no-bad", "start")
	}
	ep, err := Open(context.Background(), f.cfgRule("a", r))
	if err != nil {
		t.Fatalf("an allowed identity was refused: %v", err)
	}
	ep.Close()
	cl, err := OpenClient(context.Background(), f.cfgRule("a", r))
	if err != nil {
		t.Fatalf("an allowed identity was refused: %v", err)
	}
	cl.Close()
}

// (b) send: the addressed identity and the sender's own, before the directory is read.
func TestNameRuleSend(t *testing.T) {
	f := newFixture(t, "a", "b", "bad")
	r := ruleOf(t, "instance:bad", "instance:gone")
	a, err := OpenClient(context.Background(), f.cfgRule("a", r))
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	ctx := context.Background()

	_, err = a.Send(ctx, tdot("bad"), "hi", 0)
	wantRefusal(t, err, "no-bad", "send: the recipient")
	// The refused identity has no record at all: still the rule, not "no endpoint named".
	_, err = a.Send(ctx, tdot("gone"), "hi", 0)
	wantRefusal(t, err, "no-gone", "send: the recipient")
	if _, err := a.Send(ctx, tdot("b"), "hi", 0); err != nil {
		t.Fatalf("an allowed recipient was refused: %v", err)
	}
	// Nothing reached the refused endpoint.
	bad := f.client(t, "bad")
	if ms, err := bad.Inbox(ctx, 10); err != nil || len(ms) != 0 {
		t.Fatalf("the refused recipient's inbox: %v %v", ms, err)
	}

	// The sender's own identity: a rule that came to refuse the sender stops its send.
	a.cfg.NameRule = ruleOf(t, "instance:a")
	_, err = a.Send(ctx, tdot("b"), "hi", 0)
	wantRefusal(t, err, "no-a", "send: the sender")
}

// (b) a record named in the open's first wave (clientNeeds) is not read when its identity is refused.
func TestNameRuleSendDoesNotPrefetchARefusedRecord(t *testing.T) {
	f := newFixture(t, "a", "bad")
	r := ruleOf(t, "instance:bad")
	var read []string
	hook := func(key string) error { read = append(read, key); return nil }
	fetchRecordHook.Store(&hook)
	defer fetchRecordHook.Store(nil)
	a, err := OpenClientWith(context.Background(), f.cfgRule("a", r), ClientOptions{Records: []string{tdot("bad")}})
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	for _, k := range read {
		if k == tkey("bad") {
			t.Fatalf("the record of a refused identity was read: %v", read)
		}
	}
	_, err = a.Send(context.Background(), tdot("bad"), "hi", 0)
	wantRefusal(t, err, "no-bad", "send: the recipient")
}

// (b) a recipient named by its flat key or dotted address is parsed to an address and checked too.
func TestNameRuleSendFlatKey(t *testing.T) {
	f := newFixture(t, "a")
	peer := f.addAt(t, "team", "peer", addOpts{})
	other := f.addAt(t, "crew", "peer", addOpts{})
	a, err := OpenClient(context.Background(), f.cfgRule("a", ruleOf(t, "group:team")))
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	for _, to := range []string{peer.Flat(), peer.String()} {
		_, err = a.Send(context.Background(), to, "hi", 0)
		wantRefusal(t, err, "no-team", "send: the recipient")
	}
	// Allowed by the rule: the send goes through.
	if _, err := a.Send(context.Background(), other.Flat(), "hi", 0); err != nil {
		t.Fatalf("an allowed recipient was refused: %v", err)
	}
}

// (e) lookup: a refused identity fails with the rule, never "not found" and never silently omitted.
func TestNameRuleLookup(t *testing.T) {
	f := newFixture(t, "a", "b", "bad")
	a, err := OpenClient(context.Background(), f.cfgRule("a", ruleOf(t, "instance:bad", "instance:gone")))
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	ctx := context.Background()
	_, err = a.lookup(ctx, tdot("bad"))
	wantRefusal(t, err, "no-bad", "lookup")
	_, err = a.lookup(ctx, tdot("gone")) // no record
	wantRefusal(t, err, "no-gone", "lookup")
	if rec, err := a.lookup(ctx, tdot("b")); err != nil || rec.Instance != "b" {
		t.Fatalf("an allowed lookup: %+v %v", rec, err)
	}
	// Without a rule a missing name is still "no endpoint named", as before.
	plain, err := OpenClient(ctx, f.cfg("a"))
	if err != nil {
		t.Fatal(err)
	}
	defer plain.Close()
	if _, err := plain.lookup(ctx, tdot("gone")); err == nil || !strings.Contains(err.Error(), `no endpoint named "`+tdot("gone")+`"`) {
		t.Fatalf("no rule: %v", err)
	}
}

// (c) reply: the identity being answered. The asker is accepted when the prompt arrives (the service checks it too),
// so the rule is set after that, from inside the prompt callback, to reach the reply check. The replier gets the
// rule's error and the asker is told, not left waiting.
func TestNameRuleReply(t *testing.T) {
	f := newFixture(t, "a", "b", "c")
	replyErr := make(chan error, 2)
	ctx, cancel := context.WithCancel(context.Background())
	ep, err := Open(ctx, f.cfg("a"))
	if err != nil {
		t.Fatal(err)
	}
	if err := ep.StartService(ctx, func(p PromptEvent) {
		if p.From == tkey("b") {
			ep.cfg.NameRule = ruleOf(t, "instance:b")
		}
		replyErr <- ep.Reply(p.RequestID, "pong", true)
	}); err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	go func() { _ = ep.Run(ctx, (&events{}).deliver); close(done) }()
	t.Cleanup(func() { cancel(); <-done; ep.Close() })

	b := f.client(t, "b")
	subj, err := Discover(ctx, b.Conn(), taddr("a"))
	if err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	err = Prompt(ctx, b.Conn(), b.Identity(), b.Key(), subj, "ping", &out)
	if err == nil || !strings.Contains(err.Error(), `name rule "no-b"`) || !strings.Contains(err.Error(), "b is not allowed here") {
		t.Fatalf("the asker was not told the rule: %v", err)
	}
	wantRefusal(t, <-replyErr, "no-b", "reply")
	if out.Len() != 0 {
		t.Fatalf("an answer reached the refused asker: %q", out.String())
	}

	c := f.client(t, "c")
	out.Reset()
	if err := Prompt(ctx, c.Conn(), c.Identity(), c.Key(), subj, "ping", &out); err != nil || out.String() != "pong" {
		t.Fatalf("an allowed asker: %q %v", out.String(), err)
	}
	if err := <-replyErr; err != nil {
		t.Fatalf("an allowed reply failed: %v", err)
	}
}

// The receiving side of a prompt: the asker's identity is checked by the service itself, so a peer that skipped the
// sender-side check is answered 403 with the rule's id and message, and the prompt is never surfaced.
func TestNameRulePromptAskerRefusedByService(t *testing.T) {
	f := newFixture(t, "a", "b", "c")
	fired := make(chan string, 4)
	ctx, cancel := context.WithCancel(context.Background())
	ep, err := Open(ctx, f.cfgRule("a", ruleOf(t, "instance:b")))
	if err != nil {
		t.Fatal(err)
	}
	if err := ep.StartService(ctx, func(p PromptEvent) {
		fired <- p.From
		_ = ep.Reply(p.RequestID, "pong", true)
	}); err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	go func() { _ = ep.Run(ctx, (&events{}).deliver); close(done) }()
	t.Cleanup(func() { cancel(); <-done; ep.Close() })

	// b's own client has no rule, as an older client would not.
	b := f.client(t, "b")
	subj, err := Discover(ctx, b.Conn(), taddr("a"))
	if err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	err = Prompt(ctx, b.Conn(), b.Identity(), b.Key(), subj, "ping", &out)
	if err == nil || !strings.Contains(err.Error(), "403") || !strings.Contains(err.Error(), `name rule "no-b"`) ||
		!strings.Contains(err.Error(), "b is not allowed here") || !strings.Contains(err.Error(), "prompt: the asker") {
		t.Fatalf("the refused asker was not answered 403 with the rule: %v", err)
	}
	if out.Len() != 0 {
		t.Fatalf("an answer reached the refused asker: %q", out.String())
	}

	c := f.client(t, "c")
	out.Reset()
	if err := Prompt(ctx, c.Conn(), c.Identity(), c.Key(), subj, "ping", &out); err != nil || out.String() != "pong" {
		t.Fatalf("an allowed asker: %q %v", out.String(), err)
	}
	if from := <-fired; from != tkey("c") {
		t.Fatalf("the prompt event fired for %q; the refused asker must never surface", from)
	}
	select {
	case from := <-fired:
		t.Fatalf("a second prompt event for %q", from)
	default:
	}
}

// A relay declaration naming an origin the rule refuses is not honoured: the message is still delivered, as from its
// sender, without "relayed from".
func TestNameRuleRelayOriginNotHonoured(t *testing.T) {
	f := newFixture(t, "a", "b", "c")
	b, c := f.client(t, "b"), f.client(t, "c")
	ctx := context.Background()
	if _, err := c.SendWith(ctx, tdot("b"), "the original", SendOptions{}); err != nil {
		t.Fatal(err)
	}
	if _, err := b.SendWith(ctx, tdot("a"), "passing it on", SendOptions{Via: 1}); err != nil {
		t.Fatal(err)
	}
	plain := f.client(t, "a")
	if m, err := plain.Read(ctx, 1); err != nil || m.Via != tkey("c")+" #1" {
		t.Fatalf("no rule: %+v %v", m, err)
	}
	a, err := OpenClient(ctx, f.cfgRule("a", ruleOf(t, "instance:c")))
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	m, err := a.Read(ctx, 1)
	if err != nil || m.Via != "" || m.From != tkey("b") || string(m.Body) != "passing it on" {
		t.Fatalf("a refused origin was honoured or the mail lost: %+v %v", m, err)
	}
	if out := render.One(m); strings.Contains(out, "relayed") {
		t.Fatalf("render shows the relay: %s", out)
	}
}

// (d) prompt: the addressed identity and the sender's own, before anything is asked of the network.
func TestNameRulePrompt(t *testing.T) {
	f := newFixture(t, "a", "b", "bad")
	f.serve(t, "b", noPrompt)
	a, err := OpenClient(context.Background(), f.cfgRule("a", ruleOf(t, "instance:bad", "instance:gone")))
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	ctx := context.Background()
	_, err = a.DiscoverRecipient(ctx, tdot("bad"))
	wantRefusal(t, err, "no-bad", "prompt: the recipient")
	_, err = a.DiscoverRecipient(ctx, tdot("gone"))
	wantRefusal(t, err, "no-gone", "prompt: the recipient")
	subj, err := a.DiscoverRecipient(ctx, tdot("b"))
	if err != nil || subj != mustSubj(taddr("b")).Prompt {
		t.Fatalf("an allowed recipient: %q %v", subj, err)
	}
	a.cfg.NameRule = ruleOf(t, "instance:a")
	_, err = a.DiscoverRecipient(ctx, tdot("b"))
	wantRefusal(t, err, "no-a", "prompt: the sender")
}

// With no rule configured, nothing changes: a name any rule file might forbid works.
func TestNoNameRuleChangesNothing(t *testing.T) {
	f := newFixture(t, "a", "bad")
	a := f.client(t, "a")
	if _, err := a.Send(context.Background(), tdot("bad"), "hi", 0); err != nil {
		t.Fatal(err)
	}
	if _, err := a.lookup(context.Background(), tdot("bad")); err != nil {
		t.Fatal(err)
	}
}
