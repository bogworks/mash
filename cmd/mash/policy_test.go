package main

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"sync"
	"testing"

	"github.com/nats-io/jwt/v2"
	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"

	"github.com/bogworks/mash/internal/admin"
	"github.com/bogworks/mash/internal/listset"
	"github.com/bogworks/mash/internal/policyapply"
	"github.com/bogworks/mash/internal/policyview"
	"github.com/bogworks/mash/internal/sap"
	"github.com/bogworks/mash/internal/store"
	"github.com/bogworks/mash/internal/testenv"
)

type policyStore struct {
	ls     listset.ListSet
	status policyapply.Status
}

func (p policyStore) InForceView() (listset.Record, listset.ListSet, bool, error) {
	return listset.Record{Generation: p.status.Generation}, p.ls, true, nil
}
func (p policyStore) LatestStatus(context.Context) (policyapply.Status, bool, error) {
	return p.status, true, nil
}

func policySwitches(group string, mail, prompt, status, hb listset.Switch) []listset.SwitchEntry {
	return []listset.SwitchEntry{
		{Group: group, Verb: listset.Mail, Switch: mail},
		{Group: group, Verb: listset.Prompt, Switch: prompt},
		{Group: group, Verb: listset.Status, Switch: status},
		{Group: group, Verb: listset.Heartbeat, Switch: hb},
	}
}

// policyEndpoint provisions agent.owner.name with a list-set credential (no permission list of its own, so the test
// server, which has no scoped template, lets it publish to the policy responder as a scoped template would) and
// returns the environment its commands run with.
func policyEndpoint(t *testing.T, e *testenv.Env, adm *admin.Admin, agent, owner, name string) map[string]string {
	t.Helper()
	return policyEndpointWith(t, e, adm, agent, owner, name, true)
}

// policyEndpointWith is policyEndpoint; with listSetCred false the credential is an owner-line one: its own
// permission list, with no grant to the policy subject.
func policyEndpointWith(t *testing.T, e *testenv.Env, adm *admin.Admin, agent, owner, name string, listSetCred bool) map[string]string {
	t.Helper()
	id, err := sap.New(agent, owner, name)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	existing, err := adm.List(ctx)
	if err != nil {
		t.Fatal(err)
	}
	perms, err := admin.NewFormUserPermissions(id, nil, existing, nil)
	if err != nil {
		t.Fatal(err)
	}
	if listSetCred {
		perms = jwt.Permissions{}
	}
	creds, user := e.MintRaw(t, id.Flat(), perms)
	if _, err := adm.ProvisionIdentity(ctx, id, nil, user, e.Account, "", admin.Meta{}); err != nil {
		t.Fatal(err)
	}
	return map[string]string{"HOME": t.TempDir(), "MASH_NAME": id.Name, "MASH_OWNER": id.Owner, "MASH_AGENT_TOKEN": id.Agent,
		"MASH_URL": e.URL, "MASH_CREDS": creds, "MASH_SETTLE_MS": "20"}
}

func policyRun(env map[string]string, args ...string) (string, string, int) {
	var out, errb bytes.Buffer
	code := run(args, envFrom(env), strings.NewReader(""), &out, &errb)
	return out.String(), errb.String(), code
}

func policyWorld(t *testing.T) (*testenv.Env, *admin.Admin) {
	t.Helper()
	e := testenv.Start(t)
	adm, err := admin.New(e.Conn(t, e.AdminCreds), e.Owner)
	if err != nil {
		t.Fatal(err)
	}
	if err := adm.Init(context.Background()); err != nil {
		t.Fatal(err)
	}
	return e, adm
}

func TestPolicyShowAndExplainAgainstAResponder(t *testing.T) {
	e, adm := policyWorld(t)
	a := policyEndpoint(t, e, adm, "r", "o", "a")
	policyEndpoint(t, e, adm, "r", "o", "b")
	policyEndpoint(t, e, adm, "r", "p", "x")

	ls := listset.ListSet{
		Range:    "r",
		Switches: append(policySwitches("r.o", listset.Shared, listset.Closed, listset.Closed, listset.Closed), policySwitches("r.p", listset.Closed, listset.Closed, listset.Closed, listset.Closed)...),
		Senders: []listset.SenderLists{
			{Sender: "r.o.a", Allow: []string{"r.p.x"}},
			{Sender: "r.o.b"},
			{Sender: "r.p.x", Allow: []string{"r.o.b"}},
		},
	}
	js, err := jetstream.New(e.Conn(t, e.AdminCreds))
	if err != nil {
		t.Fatal(err)
	}
	kv, err := js.KeyValue(context.Background(), store.DirectoryBucket)
	if err != nil {
		t.Fatal(err)
	}
	stop, err := policyview.Mount(e.Conn(t, e.AdminCreds), policyview.Options{
		Range:          "r",
		Store:          policyStore{ls: ls, status: policyapply.Status{Generation: 2, State: policyapply.StateApplied, InForce: true}},
		ResolveAddress: policyview.DirectoryResolver(kv, 0),
	})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = stop() }()

	out, errs, code := policyRun(a, "policy", "show")
	if code != 0 {
		t.Fatalf("show: %d %s %s", code, out, errs)
	}
	for _, want := range []string{"sender: r.o.a", "list set: generation 2, applied, in force", "mail: r.o.a, r.o.b, r.p.x", "prompt: r.p.x", "agents.policy.r", "a raw client can send data"} {
		if !strings.Contains(out, want) {
			t.Errorf("show output lacks %q:\n%s", want, out)
		}
	}

	out, _, code = policyRun(a, "policy", "show", "--json")
	var v policyview.ShowReply
	if code != 0 || json.Unmarshal([]byte(out), &v) != nil || v.Sender != "r.o.a" || v.Generation != 2 || len(v.Reach) != 4 {
		t.Fatalf("show --json: %d %s", code, out)
	}
	if strings.Contains(out, "r.p.x") == false || strings.Contains(out, `"senders"`) {
		t.Errorf("show --json: %s", out)
	}

	out, _, code = policyRun(a, "policy", "explain", "r.o.b")
	if code != 0 || !strings.Contains(out, "receiver: r.o.b") || !strings.Contains(out, "mail: allowed (owner_line)") || !strings.Contains(out, "prompt: refused (no_entry)") {
		t.Fatalf("explain: %d\n%s", code, out)
	}
	out, _, code = policyRun(a, "policy", "explain", "r.p.x", "--json")
	var x policyview.ExplainReply
	if code != 0 || json.Unmarshal([]byte(out), &x) != nil || x.Receiver != "r.p.x" || len(x.Verbs) != 4 {
		t.Fatalf("explain --json: %d %s", code, out)
	}

	// An error reply exits 1.
	if out, errs, code := policyRun(a, "policy", "explain", "r.o"); code != 1 || !strings.Contains(errs, "receiver") {
		t.Fatalf("bad receiver: %d %s %s", code, out, errs)
	}
}

func TestPolicyListSetCredentialWithNoResponderIsAnError(t *testing.T) {
	e, adm := policyWorld(t)
	a := policyEndpoint(t, e, adm, "r", "o", "a")
	for _, args := range [][]string{{"policy", "show"}, {"policy", "show", "--json"}, {"policy", "explain", "r.o.b"}} {
		out, errs, code := policyRun(a, args...)
		if code != 1 || out != "" || !strings.Contains(errs, "policy view unavailable: no responder on agents.policy.r") {
			t.Fatalf("%v: %d %q %q", args, code, out, errs)
		}
	}
}

func TestPolicyOwnerLineCredentialAsksNothing(t *testing.T) {
	e, adm := policyWorld(t)
	a := policyEndpointWith(t, e, adm, "r", "o", "a", false)
	var mu sync.Mutex
	seen := 0
	sub, err := e.Conn(t, e.AdminCreds).Subscribe(listset.PolicySubject("r"), func(m *nats.Msg) {
		mu.Lock()
		seen++
		mu.Unlock()
		_ = m.Respond([]byte(`{}`))
	})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = sub.Unsubscribe() }()
	if err := e.Conn(t, e.AdminCreds).Flush(); err != nil {
		t.Fatal(err)
	}
	const want = "open: owner line only, no list set in force"
	out, errs, code := policyRun(a, "policy", "show")
	if code != 0 || strings.TrimSpace(out) != want {
		t.Fatalf("show: %d %q %q", code, out, errs)
	}
	out, _, code = policyRun(a, "policy", "show", "--json")
	var v policyview.ShowReply
	if code != 0 || json.Unmarshal([]byte(out), &v) != nil || v.State != "none" || !strings.Contains(v.Notice, "owner line only") {
		t.Fatalf("show --json: %d %s", code, out)
	}
	out, _, code = policyRun(a, "policy", "explain", "r.o.b")
	if code != 0 || !strings.Contains(out, "receiver: r.o.b") || !strings.Contains(out, "mail: unknown (no_list_set): "+want) {
		t.Fatalf("explain: %d %q", code, out)
	}
	out, _, code = policyRun(a, "policy", "explain", "r.o.b", "--json")
	var x policyview.ExplainReply
	if code != 0 || json.Unmarshal([]byte(out), &x) != nil || x.Receiver != "r.o.b" || x.State != "none" || len(x.Verbs) != 4 ||
		x.Verbs[0].Reason != "no_list_set" || x.Verbs[0].Allowed || !strings.Contains(x.Verbs[0].Detail, "owner line only") ||
		strings.Contains(out, `"reach"`) {
		t.Fatalf("explain --json: %d %s", code, out)
	}
	mu.Lock()
	defer mu.Unlock()
	if seen != 0 {
		t.Fatalf("the responder saw %d requests from an owner-line credential", seen)
	}
}

func TestPolicyListSetCredentialIsAnswered(t *testing.T) {
	e, adm := policyWorld(t)
	a := policyEndpoint(t, e, adm, "r", "o", "a")
	var mu sync.Mutex
	seen := 0
	sub, err := e.Conn(t, e.AdminCreds).Subscribe(listset.PolicySubject("r"), func(m *nats.Msg) {
		mu.Lock()
		seen++
		mu.Unlock()
		_ = m.Respond([]byte(`{"describes":"x","state":"applied","sender":"r.o.a"}`))
	})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = sub.Unsubscribe() }()
	if err := e.Conn(t, e.AdminCreds).Flush(); err != nil {
		t.Fatal(err)
	}
	out, errs, code := policyRun(a, "policy", "show", "--json")
	if code != 0 || !strings.Contains(out, `"state":"applied"`) {
		t.Fatalf("show: %d %q %q", code, out, errs)
	}
	mu.Lock()
	defer mu.Unlock()
	if seen != 1 {
		t.Fatalf("the responder saw %d requests, want 1", seen)
	}
}

func TestPolicyUsage(t *testing.T) {
	env := map[string]string{"HOME": t.TempDir(), "MASH_NAME": "a", "MASH_URL": "nats://127.0.0.1:1", "MASH_OWNER": "o"}
	for _, args := range [][]string{{"policy"}, {"policy", "bogus"}, {"policy", "explain"}, {"policy", "show", "extra"}} {
		if _, errs, code := policyRun(env, args...); code != 2 || !strings.Contains(errs, "usage: mash policy") {
			t.Errorf("%v: %d %q", args, code, errs)
		}
	}
	if out, _, code := policyRun(env, "policy", "--help"); code != 0 || !strings.Contains(out, "usage: mash policy show") {
		t.Errorf("--help: %d %q", code, out)
	}
}
