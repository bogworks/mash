package endpoint

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"

	"github.com/bogworks/mash/internal/config"
	"github.com/bogworks/mash/internal/namerule"
	"github.com/bogworks/mash/internal/sap"
	"github.com/bogworks/mash/internal/store"
)

// Configuration A of the naming plan's negative tests (steps 1.10 and 3.1): a rule file that refuses the old forms,
// the agent token cc and the agent token mash. These tests pin it permanently, before and after the break; none of them
// may be skipped or weakened. They cover each old form, and the plain name, at every entry point of REQ-016:
// provision (internal/admin and cmd/mashctl), start, send, reply, prompt, lookup and the compatibility alias.
//
// What refuses which form, by layer:
//   - a bare legacy name is the identity (cc, owner, name): the rule no-cc refuses it;
//   - a three-token identity whose agent token is mash: the rule no-mash refuses it (it stands for the plain name
//     inside a deployment, which the deployment's own file refuses);
//   - a three-token identity whose agent token is cc (the flat key cc_owner_name, the dotted display form
//     cc.owner.name, a configured agent_token of cc): sap refuses it, not the rule, because sap.New never builds a
//     new-form identity with the agent token cc while the legacy form exists (errLegacyAgent). The rule is never
//     reached for it, so the entry point refuses it as "not an endpoint key" (or, at start, as an invalid identity),
//     and in no case is it accepted;
//   - a three-token identity with an ordinary agent token is the allowed control at every entry point.
const (
	configAMsgCC   = "the agent token cc is retired"
	configAMsgMash = "the agent token mash is reserved"
)

// configAForm is one row of the table that every entry point below walks.
type configAForm struct {
	name string
	key  string // the recipient argument or directory key, as an entry point receives it
	// want is the refusal: "no-cc" or "no-mash" (the rule), "sap" (sap refuses the identity before the rule is
	// reached), or "" (accepted: the control).
	want string
	// exists is whether a directory record is provisioned under the key. A form without one proves that the rule is
	// applied before the directory is read: the refusal is the rule's, never "not found".
	exists bool
}

func configAForms(owner string) []configAForm {
	return []configAForm{
		{"bare legacy name, no record", "gone", "no-cc", false},
		{"bare legacy name, record exists", "legacy", "no-cc", true},
		{"three-token flat key with agent cc, no record", "cc_acme_gone", "sap", false},
		{"three-token flat key with agent cc under the asker's owner", "cc_" + owner + "_gone", "sap", false},
		{"dotted display form with agent cc", "cc.acme.gone", "sap", false},
		{"three-token identity with agent mash, no record", "mash_acme_gone", "no-mash", false},
		{"three-token identity with agent mash, record exists", "mash_acme_peer", "no-mash", true},
		{"control: three-token identity with an ordinary agent token", "claude_acme_peer", "", true},
	}
}

// configAFile writes configuration A: no-cc and no-mash, each refusing its agent token.
func configAFile(t *testing.T) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "name-rule.json")
	body := `{"version":1,"rules":[` +
		`{"id":"no-cc","match":{"agent":{"is":"cc"}},"message":"` + configAMsgCC + `"},` +
		`{"id":"no-mash","match":{"agent":{"is":"mash"}},"message":"` + configAMsgMash + `"}]}`
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

func configARule(t *testing.T) *namerule.Rule {
	t.Helper()
	r, err := namerule.Load(configAFile(t))
	if err != nil {
		t.Fatal(err)
	}
	return r
}

// configAWorld is a fixture with a legacy endpoint "a" (the legacy sender) and "legacy" (a legacy recipient), the
// three-token endpoints of the table (agent mash and agent claude) and two senders, one per agent token.
type configAWorld struct {
	f      *fixture
	rule   *namerule.Rule
	peer   sap.EndpointIdentity // claude_acme_peer: the control recipient
	mashEp sap.EndpointIdentity // mash_acme_peer: refused by no-mash
	me     sap.EndpointIdentity // claude_acme_me: the control sender
	mashMe sap.EndpointIdentity // mash_acme_me: a sender the rule refuses
}

func newConfigAWorld(t *testing.T) *configAWorld {
	t.Helper()
	f := newFixture(t, "a", "legacy")
	return &configAWorld{
		f:      f,
		rule:   configARule(t),
		peer:   f.addNewForm(t, "claude", "acme", "peer", newFormOpts{}),
		mashEp: f.addNewForm(t, "mash", "acme", "peer", newFormOpts{}),
		me:     f.addNewForm(t, "claude", "acme", "me", newFormOpts{}),
		mashMe: f.addNewForm(t, "mash", "acme", "me", newFormOpts{}),
	}
}

// asSender opens a client of the identity with no rule (as an endpoint opened before the rule came), then puts
// configuration A on it: the sender's own identity is then checked at send and prompt.
func (w *configAWorld) asSender(t *testing.T, id sap.EndpointIdentity) *Endpoint {
	t.Helper()
	c := w.f.clientNF(t, id)
	c.cfg.NameRule = w.rule
	return c
}

// wantConfigA fails unless err is the refusal the form's layer owes at the entry point.
func wantConfigA(t *testing.T, form configAForm, err error, point string) {
	t.Helper()
	switch form.want {
	case "no-cc", "no-mash":
		wantRefusal(t, err, form.want, point)
		msg := configAMsgCC
		if form.want == "no-mash" {
			msg = configAMsgMash
		}
		if !strings.Contains(err.Error(), msg) {
			t.Fatalf("%s: the refusal does not carry %q: %v", point, msg, err)
		}
	case "sap":
		// sap, not the rule, refuses the identity: the error is not a rule refusal, says the key is not an endpoint
		// key, and the form is not accepted.
		var ref *namerule.Refusal
		if err == nil || errors.As(err, &ref) || !strings.Contains(err.Error(), "is not an endpoint key") {
			t.Fatalf("%s: want sap's refusal (not an endpoint key), got %v", point, err)
		}
	default:
		var ref *namerule.Refusal
		if errors.As(err, &ref) {
			t.Fatalf("%s: the control was refused by the rule: %v", point, err)
		}
		if err != nil {
			t.Fatalf("%s: the control failed: %v", point, err)
		}
	}
}

// The sap layer is what refuses a three-token identity with agent cc: asserted directly, so a change to sap that
// began to accept it would fail here and not pass as a rule refusal.
func TestConfigASapRefusesAgentCC(t *testing.T) {
	for _, flat := range []string{"cc_acme_gone", "cc_o_gone"} {
		if _, err := sap.FromFlat(flat); err == nil || !strings.Contains(err.Error(), "legacy form") {
			t.Errorf("sap.FromFlat(%q): %v", flat, err)
		}
	}
	if _, err := sap.New("cc", "acme", "gone"); err == nil || !strings.Contains(err.Error(), "legacy form") {
		t.Errorf("sap.New(cc): %v", err)
	}
	if _, err := sap.FromFlat("cc.acme.gone"); err == nil {
		t.Error("the dotted display form was parsed as an identity")
	}
	// And the legacy identity the bare name stands for is the one the rule refuses.
	legacy, err := sap.Legacy("o", "gone")
	if err != nil || legacy.Agent != "cc" {
		t.Fatalf("sap.Legacy: %+v %v", legacy, err)
	}
	if err := configARule(t).Check(legacy); err == nil || !strings.Contains(err.Error(), `"no-cc"`) {
		t.Fatalf("the rule did not refuse the bare name: %v", err)
	}
}

// start: every form of the endpoint's own identity, in Open and in OpenClient, before anything is connected or read.
func TestConfigAStart(t *testing.T) {
	w := newConfigAWorld(t)
	type startForm struct {
		name string
		cfg  config.Config
		want string
	}
	cc := w.f.cfg("a") // the bare legacy name: identity (cc, owner, a)
	missingCreds := w.f.cfg("a")
	missingCreds.Creds = filepath.Join(t.TempDir(), "missing.creds")
	mash := w.f.cfgNF(w.mashMe)
	mashNoCreds := w.f.cfgNF(w.mashMe)
	mashNoCreds.Creds = filepath.Join(t.TempDir(), "missing.creds")
	agentCC := w.f.cfgNF(w.me) // agent_token cc: no three-token identity exists for it
	agentCC.AgentToken = "cc"
	forms := []startForm{
		{"bare legacy name", cc, "no-cc"},
		{"bare legacy name, credentials missing (refused before they are read)", missingCreds, "no-cc"},
		{"three-token identity with agent mash", mash, "no-mash"},
		{"three-token identity with agent mash, credentials missing", mashNoCreds, "no-mash"},
		{"agent_token cc", agentCC, "sap"},
		{"control: ordinary agent token", w.f.cfgNF(w.me), ""},
	}
	for _, form := range forms {
		cfg := form.cfg
		cfg.NameRule = w.rule
		opens := map[string]func() (*Endpoint, error){
			"Open":       func() (*Endpoint, error) { return Open(context.Background(), cfg) },
			"OpenClient": func() (*Endpoint, error) { return OpenClient(context.Background(), cfg) },
		}
		for opener, open := range opens {
			t.Run(form.name+"/"+opener, func(t *testing.T) {
				ep, err := open()
				switch form.want {
				case "no-cc", "no-mash":
					if err == nil {
						ep.Close()
						t.Fatal("a refused identity started")
					}
					wantRefusal(t, err, form.want, "start")
				case "sap":
					var ref *namerule.Refusal
					if err == nil || errors.As(err, &ref) || !strings.Contains(err.Error(), "legacy form") {
						if ep != nil {
							ep.Close()
						}
						t.Fatalf("want sap's refusal of agent cc, got %v", err)
					}
				default:
					if err != nil {
						t.Fatalf("the control was refused: %v", err)
					}
					ep.Close()
				}
			})
		}
	}
}

// send: the addressed identity (every form, before the directory is read) and the sender's own. The sender is told
// and nothing reaches any recipient.
func TestConfigASend(t *testing.T) {
	w := newConfigAWorld(t)
	ctx := context.Background()
	sender := w.asSender(t, w.me)
	for _, form := range configAForms(w.f.e.Owner) {
		t.Run("recipient/"+form.name, func(t *testing.T) {
			_, err := sender.Send(ctx, form.key, "hi", 0)
			wantConfigA(t, form, err, "send: the recipient")
		})
	}
	// Nothing was dropped silently and nothing was delivered to a refused recipient: its inbox holds no message.
	for _, key := range []string{"legacy", w.mashEp.Flat()} {
		var ep *Endpoint
		if key == "legacy" {
			ep = w.f.client(t, key)
		} else {
			ep = w.f.clientNF(t, w.mashEp)
		}
		if ms, err := ep.Inbox(ctx, 10); err != nil || len(ms) != 0 {
			t.Errorf("the refused recipient %q has mail: %v %v", key, ms, err)
		}
	}
	if ms, err := w.f.clientNF(t, w.peer).Inbox(ctx, 10); err != nil || len(ms) != 1 {
		t.Errorf("the control recipient's inbox: %v %v (want the one control message)", ms, err)
	}

	// The sender's own identity, for each form a sender can have: refused before anything is sent.
	owns := []struct {
		name   string
		sender *Endpoint
		want   string
	}{
		{"bare legacy name", w.f.client(t, "a"), "no-cc"},
		{"three-token identity with agent mash", w.f.clientNF(t, w.mashMe), "no-mash"},
		{"control: ordinary agent token", w.f.clientNF(t, w.me), ""},
	}
	for _, o := range owns {
		t.Run("sender/"+o.name, func(t *testing.T) {
			o.sender.cfg.NameRule = w.rule
			_, err := o.sender.Send(ctx, w.peer.Flat(), "from "+o.name, 0)
			if o.want == "" {
				if err != nil {
					t.Fatalf("the control sender was refused: %v", err)
				}
				return
			}
			wantRefusal(t, err, o.want, "send: the sender")
		})
	}
	if ms, err := w.f.clientNF(t, w.peer).Inbox(ctx, 10); err != nil || len(ms) != 2 {
		t.Errorf("the control recipient's inbox: %d messages %v (want the two control messages, none from a refused sender)", len(ms), err)
	}
}

// lookup: a refused identity fails with the rule, never "not found" and never silently omitted.
func TestConfigALookup(t *testing.T) {
	w := newConfigAWorld(t)
	ctx := context.Background()
	var read []string
	hook := func(key string) error { read = append(read, key); return nil }
	fetchRecordHook.Store(&hook)
	defer fetchRecordHook.Store(nil)
	var keys []string
	for _, form := range configAForms(w.f.e.Owner) {
		keys = append(keys, form.key)
	}
	// The open's first wave asks for every key: the record of a refused identity is not read for it.
	c, err := OpenClientWith(ctx, w.f.cfgNF(w.me), ClientOptions{Records: keys})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(c.Close)
	c.cfg.NameRule = w.rule
	for _, form := range configAForms(w.f.e.Owner) {
		t.Run(form.name, func(t *testing.T) {
			rec, err := c.lookup(ctx, form.key)
			wantConfigA(t, form, err, "lookup")
			if form.want == "" && rec.Name != "peer" {
				t.Fatalf("the control lookup: %+v", rec)
			}
			if form.want != "" && (rec.Name != "" || rec.User != "") {
				t.Fatalf("a refused lookup returned a record: %+v", rec)
			}
		})
	}
	// With the rule in force at open, the first wave reads no refused record.
	read = nil
	rc := w.f.cfgNF(w.me)
	rc.NameRule = w.rule
	c2, err := OpenClientWith(ctx, rc, ClientOptions{Records: keys})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(c2.Close)
	for _, k := range read {
		for _, form := range configAForms(w.f.e.Owner) {
			if k == form.key && form.want != "" {
				t.Errorf("the record of a refused identity %q (%s) was read: %v", k, form.name, read)
			}
		}
	}
}

// prompt: the addressed identity (every form) and the sender's own, before anything is asked of the network.
func TestConfigAPrompt(t *testing.T) {
	w := newConfigAWorld(t)
	ctx := context.Background()
	w.f.serveNF(t, w.peer, noPrompt)
	sender := w.asSender(t, w.me)
	for _, form := range configAForms(w.f.e.Owner) {
		t.Run("recipient/"+form.name, func(t *testing.T) {
			subj, err := sender.DiscoverRecipient(ctx, form.key)
			wantConfigA(t, form, err, "prompt: the recipient")
			if form.want != "" && subj != "" {
				t.Fatalf("a refused recipient got a subject: %q", subj)
			}
			if form.want == "" && subj != "agents.prompt.claude.acme.peer" {
				t.Fatalf("the control subject: %q", subj)
			}
		})
	}
	owns := []struct {
		name   string
		sender *Endpoint
		want   string
	}{
		{"bare legacy name", w.f.client(t, "a"), "no-cc"},
		{"three-token identity with agent mash", w.f.clientNF(t, w.mashMe), "no-mash"},
		{"control: ordinary agent token", w.f.clientNF(t, w.me), ""},
	}
	for _, o := range owns {
		t.Run("sender/"+o.name, func(t *testing.T) {
			o.sender.cfg.NameRule = w.rule
			_, err := o.sender.DiscoverRecipient(ctx, w.peer.Flat())
			if o.want == "" {
				if err != nil {
					t.Fatalf("the control sender was refused: %v", err)
				}
				return
			}
			wantRefusal(t, err, o.want, "prompt: the sender")
		})
	}
}

// reply: the identity being answered, for every form. The replier gets the rule's error, the asker is told (the
// prompt ends with the rule's error and no answer), and nothing is dropped silently.
func TestConfigAReply(t *testing.T) {
	w := newConfigAWorld(t)
	ctx, cancel := context.WithCancel(context.Background())
	srvID := w.f.addNewForm(t, "claude", "acme", "srv", newFormOpts{})
	// Provisioned before the replier opens, which reads the directory once: its askers are keyed by their records.
	// The asker with agent mash is minted the prompt permissions of a claude endpoint: server permissions would
	// otherwise stop a prompt across agent tokens before it reached the replier.
	askMash := w.f.addNewForm(t, "mash", "acme", "asker", newFormOpts{credsOf: "claude_acme_me"})
	cfg := w.f.cfgNF(srvID)
	cfg.NameRule = w.rule
	ep, err := Open(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	replyErr := make(chan error, 4)
	if err := ep.StartService(ctx, func(p PromptEvent) { replyErr <- ep.Reply(p.RequestID, "pong", true) }); err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	go func() { _ = ep.Run(ctx, (&events{}).deliver); close(done) }()
	t.Cleanup(func() { cancel(); <-done; ep.Close() })

	// A live prompt whose asker is each form: the asker key a prompt carries is what Reply checks.
	for i, form := range configAForms(w.f.e.Owner) {
		if form.want == "sap" {
			continue // no such asker exists: sap refuses the identity, so no endpoint is ever keyed by it
		}
		t.Run(form.name, func(t *testing.T) {
			reply := secretInbox(ep.nc)
			sub, err := ep.nc.SubscribeSync(reply)
			if err != nil {
				t.Fatal(err)
			}
			defer sub.Unsubscribe()
			id := fmt.Sprintf("req-%d", i)
			svc := ep.svc()
			svc.mu.Lock()
			svc.prompts[id] = &openPrompt{from: form.key, reply: reply, done: make(chan struct{})}
			svc.mu.Unlock()

			err = ep.Reply(id, "pong", true)
			wantConfigA(t, form, err, "reply")
			var got []*nats.Msg
			for {
				m, err := sub.NextMsg(2 * time.Second)
				if err != nil {
					t.Fatalf("the asker was left waiting: %v (got %d chunks)", err, len(got))
				}
				got = append(got, m)
				if len(m.Data) == 0 && len(m.Header) == 0 {
					break
				}
			}
			var answered, told bool
			for _, m := range got {
				if code := m.Header.Get("Nats-Service-Error-Code"); code == "403" &&
					strings.Contains(m.Header.Get("Nats-Service-Error"), `name rule "`+form.want+`"`) {
					told = true
				}
				if strings.Contains(string(m.Data), "pong") {
					answered = true
				}
			}
			if form.want == "" && (!answered || told) {
				t.Fatalf("the control was not answered cleanly: answered=%v told=%v", answered, told)
			}
			if form.want != "" && (answered || !told) {
				t.Fatalf("the refused asker: answered=%v told=%v (want told and not answered)", answered, told)
			}
		})
	}

	// End to end over the transport, for the askers that can prompt a three-token endpoint: the control is answered,
	// the asker with agent mash is told the rule and receives no answer.
	subj, err := DiscoverIdentity(ctx, w.f.clientNF(t, w.me).Conn(), srvID)
	if err != nil {
		t.Fatal(err)
	}
	for _, a := range []struct {
		name string
		id   sap.EndpointIdentity
		want string
	}{
		{"asker with agent mash", askMash, "no-mash"},
		{"control asker", w.me, ""},
	} {
		t.Run("transport/"+a.name, func(t *testing.T) {
			// A raw connection and identity from the asker's credentials: the asker is keyed by its own record.
			raw, err := os.ReadFile(w.f.creds[a.id.Flat()])
			if err != nil {
				t.Fatal(err)
			}
			ident, err := sap.IdentityFromCreds(raw)
			if err != nil {
				t.Fatal(err)
			}
			prefix := a.id.Flat()
			if a.id == askMash {
				prefix = "claude_acme_me" // the inbox prefix its minted permissions allow
			}
			nc := w.f.e.ConnAs(t, w.f.creds[a.id.Flat()], prefix)
			var out bytes.Buffer
			err = Prompt(ctx, nc, ident, a.id.Flat(), subj, "ping", &out)
			if a.want == "" {
				if err != nil || out.String() != "pong" {
					t.Fatalf("the control asker: %q %v", out.String(), err)
				}
				if err := <-replyErr; err != nil {
					t.Fatalf("an allowed reply failed: %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), `name rule "`+a.want+`"`) || !strings.Contains(err.Error(), configAMsgMash) {
				t.Fatalf("the asker was not told the rule: %v", err)
			}
			if out.Len() != 0 {
				t.Fatalf("an answer reached the refused asker: %q", out.String())
			}
			// The service refuses the asker on receipt (the receiving side of prompt), so the prompt never reaches the
			// replier: no reply is attempted at all.
			select {
			case err := <-replyErr:
				t.Fatalf("the refused asker's prompt reached the replier: %v", err)
			case <-time.After(300 * time.Millisecond):
			}
		})
	}
}

// the compatibility alias: a three-token endpoint's record can name a legacy alias, which is the identity
// (cc, owner, name). Configuration A refuses it with no-cc, naming the alias, in Open and in OpenClient; the same
// endpoint without the alias starts (the control). The alias is data of the endpoint's own record, so it is checked as
// soon as that record is read, before the rest of the directory.
func TestConfigAAlias(t *testing.T) {
	w := newConfigAWorld(t)
	setAlias := func(id sap.EndpointIdentity, a *store.Alias) {
		t.Helper()
		js, err := jetstream.New(w.f.e.Conn(t, w.f.e.AdminCreds))
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
		rec.Alias = a
		val, _ := json.Marshal(rec)
		if _, err := kv.Put(context.Background(), id.Flat(), val); err != nil {
			t.Fatal(err)
		}
	}
	cfg := w.f.cfgNF(w.me)
	cfg.NameRule = w.rule
	opens := map[string]func() (*Endpoint, error){
		"Open":       func() (*Endpoint, error) { return Open(context.Background(), cfg) },
		"OpenClient": func() (*Endpoint, error) { return OpenClient(context.Background(), cfg) },
	}
	for opener, open := range opens { // no alias: starts
		ep, err := open()
		if err != nil {
			t.Fatalf("%s without an alias: %v", opener, err)
		}
		ep.Close()
	}
	setAlias(w.me, &store.Alias{Owner: w.f.e.Owner, Name: "me"}) // the legacy identity (cc, owner, me)
	for opener, open := range opens {
		ep, err := open()
		if err == nil {
			ep.Close()
			t.Fatalf("%s started with a refused alias", opener)
		}
		wantRefusal(t, err, "no-cc", "the compatibility alias")
	}
	// An alias whose own identity is not refused is accepted: the rule refuses cc, and only an agent-mash or agent-cc
	// alias could differ, which a record cannot name (an alias is always a legacy identity).
	noRule := w.f.cfgNF(w.me)
	ep, err := OpenClient(context.Background(), noRule)
	if err != nil {
		t.Fatalf("with no rule the alias is not checked: %v", err)
	}
	ep.Close()
	// An endpoint whose own identity is refused is refused at start, before its alias matters.
	mcfg := w.f.cfgNF(w.mashMe)
	mcfg.NameRule = w.rule
	if _, err := OpenClient(context.Background(), mcfg); err == nil {
		t.Fatal("started")
	} else {
		wantRefusal(t, err, "no-mash", "start")
	}
}
