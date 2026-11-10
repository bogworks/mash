package endpoint

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nats-io/nats.go/jetstream"

	"github.com/bogworks/mash/internal/config"
	"github.com/bogworks/mash/internal/namerule"
	"github.com/bogworks/mash/internal/store"
)

// ruleOf loads a name-rule file that refuses each of the given tokens, one rule per entry: "name:bad" refuses the
// name token bad, "agent:claude" the agent token claude, "owner:legacyo" the owner token legacyo.
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
	r := ruleOf(t, "name:bad")
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
	r := ruleOf(t, "name:bad", "name:gone")
	a, err := OpenClient(context.Background(), f.cfgRule("a", r))
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	ctx := context.Background()

	_, err = a.Send(ctx, "bad", "hi", 0)
	wantRefusal(t, err, "no-bad", "send: the recipient")
	// The refused identity has no record at all: still the rule, not "no endpoint named".
	_, err = a.Send(ctx, "gone", "hi", 0)
	wantRefusal(t, err, "no-gone", "send: the recipient")
	if _, err := a.Send(ctx, "b", "hi", 0); err != nil {
		t.Fatalf("an allowed recipient was refused: %v", err)
	}
	// Nothing reached the refused endpoint.
	bad := f.client(t, "bad")
	if ms, err := bad.Inbox(ctx, 10); err != nil || len(ms) != 0 {
		t.Fatalf("the refused recipient's inbox: %v %v", ms, err)
	}

	// The sender's own identity: a rule that came to refuse the sender stops its send.
	a.cfg.NameRule = ruleOf(t, "name:a")
	_, err = a.Send(ctx, "b", "hi", 0)
	wantRefusal(t, err, "no-a", "send: the sender")
}

// (b) a record named in the open's first wave (clientNeeds) is not read when its identity is refused.
func TestNameRuleSendDoesNotPrefetchARefusedRecord(t *testing.T) {
	f := newFixture(t, "a", "bad")
	r := ruleOf(t, "name:bad")
	var read []string
	hook := func(key string) error { read = append(read, key); return nil }
	fetchRecordHook.Store(&hook)
	defer fetchRecordHook.Store(nil)
	a, err := OpenClientWith(context.Background(), f.cfgRule("a", r), ClientOptions{Records: []string{"bad"}})
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	for _, k := range read {
		if k == "bad" {
			t.Fatalf("the record of a refused identity was read: %v", read)
		}
	}
	_, err = a.Send(context.Background(), "bad", "hi", 0)
	wantRefusal(t, err, "no-bad", "send: the recipient")
}

// (b) a recipient named by the flat key of a three-token endpoint is formed with sap.FromFlat and checked too.
func TestNameRuleSendFlatKey(t *testing.T) {
	f := newFixture(t, "a")
	peer := f.addNewForm(t, "claude", "acme", "peer", newFormOpts{})
	other := f.addNewForm(t, "codex", "acme", "peer", newFormOpts{})
	a, err := OpenClient(context.Background(), f.cfgRule("a", ruleOf(t, "agent:claude")))
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	_, err = a.Send(context.Background(), peer.Flat(), "hi", 0)
	wantRefusal(t, err, "no-claude", "send: the recipient")
	// Allowed by the rule. A legacy sender may not publish to a new-form subject, so the send ends in the server's
	// policy refusal: the rule let it through, which is all this test asks.
	var ref *namerule.Refusal
	if _, err := a.Send(context.Background(), other.Flat(), "hi", 0); errors.As(err, &ref) {
		t.Fatalf("an allowed flat key was refused by the rule: %v", err)
	}
}

// (e) lookup: a refused identity fails with the rule, never "not found" and never silently omitted.
func TestNameRuleLookup(t *testing.T) {
	f := newFixture(t, "a", "b", "bad")
	a, err := OpenClient(context.Background(), f.cfgRule("a", ruleOf(t, "name:bad", "name:gone")))
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	ctx := context.Background()
	_, err = a.lookup(ctx, "bad")
	wantRefusal(t, err, "no-bad", "lookup")
	_, err = a.lookup(ctx, "gone") // no record
	wantRefusal(t, err, "no-gone", "lookup")
	if rec, err := a.lookup(ctx, "b"); err != nil || rec.Name != "b" {
		t.Fatalf("an allowed lookup: %+v %v", rec, err)
	}
	// Without a rule a missing name is still "no endpoint named", as before.
	plain, err := OpenClient(ctx, f.cfg("a"))
	if err != nil {
		t.Fatal(err)
	}
	defer plain.Close()
	if _, err := plain.lookup(ctx, "gone"); err == nil || !strings.Contains(err.Error(), `no endpoint named "gone"`) {
		t.Fatalf("no rule: %v", err)
	}
}

// (c) reply: the identity being answered. The replier gets the rule's error and the asker is told, not left waiting.
func TestNameRuleReply(t *testing.T) {
	f := newFixture(t, "a", "b", "c")
	replyErr := make(chan error, 2)
	ctx, cancel := context.WithCancel(context.Background())
	ep, err := Open(ctx, f.cfgRule("a", ruleOf(t, "name:b")))
	if err != nil {
		t.Fatal(err)
	}
	if err := ep.StartService(ctx, func(p PromptEvent) { replyErr <- ep.Reply(p.RequestID, "pong", true) }); err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	go func() { _ = ep.Run(ctx, (&events{}).deliver); close(done) }()
	t.Cleanup(func() { cancel(); <-done; ep.Close() })

	b := f.client(t, "b")
	subj, err := Discover(ctx, b.Conn(), "o", "a")
	if err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	err = Prompt(ctx, b.Conn(), b.Identity(), "b", subj, "ping", &out)
	if err == nil || !strings.Contains(err.Error(), `name rule "no-b"`) || !strings.Contains(err.Error(), "b is not allowed here") {
		t.Fatalf("the asker was not told the rule: %v", err)
	}
	wantRefusal(t, <-replyErr, "no-b", "reply")
	if out.Len() != 0 {
		t.Fatalf("an answer reached the refused asker: %q", out.String())
	}

	c := f.client(t, "c")
	out.Reset()
	if err := Prompt(ctx, c.Conn(), c.Identity(), "c", subj, "ping", &out); err != nil || out.String() != "pong" {
		t.Fatalf("an allowed asker: %q %v", out.String(), err)
	}
	if err := <-replyErr; err != nil {
		t.Fatalf("an allowed reply failed: %v", err)
	}
}

// (d) prompt: the addressed identity and the sender's own, before anything is asked of the network.
func TestNameRulePrompt(t *testing.T) {
	f := newFixture(t, "a", "b", "bad")
	f.serve(t, "b", noPrompt)
	a, err := OpenClient(context.Background(), f.cfgRule("a", ruleOf(t, "name:bad", "name:gone")))
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	ctx := context.Background()
	_, err = a.DiscoverRecipient(ctx, "bad")
	wantRefusal(t, err, "no-bad", "prompt: the recipient")
	_, err = a.DiscoverRecipient(ctx, "gone")
	wantRefusal(t, err, "no-gone", "prompt: the recipient")
	subj, err := a.DiscoverRecipient(ctx, "b")
	if err != nil || subj != "agents.prompt.cc.o.b" {
		t.Fatalf("an allowed recipient: %q %v", subj, err)
	}
	a.cfg.NameRule = ruleOf(t, "name:a")
	_, err = a.DiscoverRecipient(ctx, "b")
	wantRefusal(t, err, "no-a", "prompt: the sender")
}

// (g) the compatibility alias: the alias identity of the endpoint's own record is checked at start.
func TestNameRuleAlias(t *testing.T) {
	f := newFixture(t)
	id := f.addNewForm(t, "claude", "acme", "peer", newFormOpts{})
	js, err := jetstream.New(f.e.Conn(t, f.e.AdminCreds))
	if err != nil {
		t.Fatal(err)
	}
	kv, err := js.KeyValue(context.Background(), store.DirectoryBucket)
	if err != nil {
		t.Fatal(err)
	}
	e, err := kv.Get(context.Background(), id.Flat())
	if err != nil {
		t.Fatal(err)
	}
	var rec store.Record
	if err := json.Unmarshal(e.Value(), &rec); err != nil {
		t.Fatal(err)
	}
	rec.Alias = &store.Alias{Owner: "legacyo", Name: "peer"}
	val, _ := json.Marshal(rec)
	if _, err := kv.Put(context.Background(), id.Flat(), val); err != nil {
		t.Fatal(err)
	}

	// No rule, and a rule that refuses neither identity: starts.
	for _, r := range []*namerule.Rule{nil, ruleOf(t, "owner:elsewhere")} {
		c := f.cfgNF(id)
		c.NameRule = r
		cl, err := OpenClient(context.Background(), c)
		if err != nil {
			t.Fatalf("start with rule %v: %v", r != nil, err)
		}
		cl.Close()
	}
	// A rule that refuses only the alias (owner legacyo): start is refused, naming the alias.
	c := f.cfgNF(id)
	c.NameRule = ruleOf(t, "owner:legacyo")
	_, err = OpenClient(context.Background(), c)
	if err == nil {
		t.Fatal("started with a refused alias")
	}
	wantRefusal(t, err, "no-legacyo", "the compatibility alias")
	_, err = Open(context.Background(), c)
	wantRefusal(t, err, "no-legacyo", "the compatibility alias")
}

// The alias is data of the endpoint's own record: with no record there is no alias to check, so the start reports
// "not provisioned" and never a rule refusal. (TestNameRuleAlias covers a record whose alias is refused.)
func TestNameRuleAliasOrder(t *testing.T) {
	f := newFixture(t, "a")
	noRec := f.addNewForm(t, "ag", "team", "norec", newFormOpts{noRecord: true})
	c := f.cfgNF(noRec)
	c.NameRule = ruleOf(t, "owner:legacyo")
	for opener, open := range map[string]func() error{
		"Open":       func() error { _, err := Open(context.Background(), c); return err },
		"OpenClient": func() error { _, err := OpenClient(context.Background(), c); return err },
	} {
		err := open()
		if err == nil || !strings.Contains(err.Error(), "is not provisioned") {
			t.Errorf("%s with no record: %v (want not provisioned)", opener, err)
		}
		var ref *namerule.Refusal
		if errors.As(err, &ref) {
			t.Errorf("%s with no record reported a rule refusal: %v", opener, err)
		}
	}
}

// With no rule configured, nothing changes: a name any rule file might forbid works.
func TestNoNameRuleChangesNothing(t *testing.T) {
	f := newFixture(t, "a", "bad")
	a := f.client(t, "a")
	if _, err := a.Send(context.Background(), "bad", "hi", 0); err != nil {
		t.Fatal(err)
	}
	if _, err := a.lookup(context.Background(), "bad"); err != nil {
		t.Fatal(err)
	}
}
