package main

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/nats-io/nats.go/jetstream"

	"github.com/bogworks/mash/internal/admin"
	"github.com/bogworks/mash/internal/sap"
	"github.com/bogworks/mash/internal/spool"
	"github.com/bogworks/mash/internal/store"
	"github.com/bogworks/mash/internal/testenv"
)

// provisionNewForm provisions a three-token endpoint directly (there is no mashctl for it yet), as the endpoint
// package's tests do, and returns the environment its mash commands run with.
func provisionNewForm(t *testing.T, e *testenv.Env, agent, owner, name string) (sap.EndpointIdentity, map[string]string) {
	t.Helper()
	id, err := sap.New(agent, owner, name)
	if err != nil {
		t.Fatal(err)
	}
	perms, err := admin.NewFormPermissions(id, nil)
	if err != nil {
		t.Fatal(err)
	}
	creds, user := e.MintRaw(t, id.Flat(), perms)
	ctx := context.Background()
	js, err := jetstream.New(e.Conn(t, e.AdminCreds))
	if err != nil {
		t.Fatal(err)
	}
	s, err := js.CreateStream(ctx, store.InboxConfigIdentity(id, store.DefaultLimits()))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.CreateOrUpdateConsumer(ctx, store.ConsumerConfig()); err != nil {
		t.Fatal(err)
	}
	kv, err := js.KeyValue(ctx, store.DirectoryBucket)
	if err != nil {
		t.Fatal(err)
	}
	rec := store.Record{Name: id.Name, Owner: id.Owner, Agent: sap.AgentName, AgentToken: id.Agent, Account: e.Account,
		User: user, MailSubject: id.Subjects().Mail, Created: time.Now().UTC().Format(time.RFC3339)}
	val, _ := json.Marshal(rec)
	if _, err := kv.Create(ctx, id.Flat(), val); err != nil {
		t.Fatal(err)
	}
	return id, map[string]string{"HOME": t.TempDir(), "MASH_NAME": id.Name, "MASH_OWNER": id.Owner, "MASH_AGENT_TOKEN": id.Agent,
		"MASH_URL": e.URL, "MASH_CREDS": creds, "MASH_SETTLE_MS": "20"}
}

func pendingOf(t *testing.T, e *testenv.Env, stream string) uint64 {
	t.Helper()
	js, err := jetstream.New(e.Conn(t, e.AdminCreds))
	if err != nil {
		t.Fatal(err)
	}
	c, err := js.Consumer(context.Background(), stream, store.Consumer)
	if err != nil {
		t.Fatal(err)
	}
	i, err := c.Info(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	return i.NumPending + uint64(i.NumAckPending)
}

func cliAs(t *testing.T, env map[string]string, args ...string) string {
	t.Helper()
	var out, errb strings.Builder
	if code := run(args, envFrom(env), strings.NewReader(""), &out, &errb); code != 0 {
		t.Fatalf("mash %v: exit %d: %s%s", args, code, out.String(), errb.String())
	}
	return out.String()
}

func cliAsQuiet(env map[string]string, args ...string) string {
	var out, errb strings.Builder
	run(args, envFrom(env), strings.NewReader(""), &out, &errb)
	return out.String()
}

// A live new-form serve and a live legacy serve with the same name token share one state directory: each spools to
// its own directory, each hook drains and shows only its own endpoint's mail, and each serve acks only its own.
func TestNewFormHookNextToLegacyEndpointOfTheSameName(t *testing.T) {
	e := testenv.Start(t)
	e.Owner = "legowner" // the legacy endpoints' owner, before anything is provisioned
	adm, err := admin.New(e.Conn(t, e.AdminCreds), e.Owner)
	if err != nil {
		t.Fatal(err)
	}
	if err := adm.Init(context.Background()); err != nil {
		t.Fatal(err)
	}
	state := filepath.Join(t.TempDir(), "shared-state")
	legacy := func(name string) map[string]string {
		c, u := e.Mint(t, name)
		if _, err := adm.Provision(context.Background(), name, u, e.Account, "", admin.Meta{}); err != nil {
			t.Fatal(err)
		}
		return map[string]string{"HOME": t.TempDir(), "MASH_NAME": name, "MASH_URL": e.URL, "MASH_OWNER": e.Owner,
			"MASH_CREDS": c, "MASH_SETTLE_MS": "20"}
	}
	legEnv, legSender := legacy("ops1"), legacy("lsender")
	nfID, nfEnv := provisionNewForm(t, e, "acme", "team1", "ops1")
	_, nfSender := provisionNewForm(t, e, "acme", "team1", "nsender")
	for _, env := range []map[string]string{legEnv, nfEnv} {
		env["MASH_STATE_DIR"] = state // one shared state directory: a collision would show
		env["MASH_IDLE_WAKE"] = "5s"
	}
	if nfID.Flat() != "acme_team1_ops1" || nfEnv["MASH_NAME"] != legEnv["MASH_NAME"] {
		t.Fatalf("setup: flat %q names %q %q", nfID.Flat(), nfEnv["MASH_NAME"], legEnv["MASH_NAME"])
	}

	legSpool := filepath.Join(state, "spool", "legowner", "ops1")
	nfSpool := filepath.Join(state, "spool", "team1", "acme_team1_ops1")

	ls, ns := startServe(t, legEnv), startServe(t, nfEnv)
	ls.initialize(t)
	ns.initialize(t)
	ls.tools(t)
	ns.tools(t)
	for _, d := range []string{legSpool, nfSpool} {
		d := d
		eventually(t, 5*time.Second, "serve made the spool "+d, func() bool { _, err := os.Stat(d); return err == nil })
	}
	eventually(t, 5*time.Second, "both online", func() bool {
		return strings.Contains(cliAsQuiet(legSender, "who"), "ops1\tonline") && strings.Contains(cliAsQuiet(nfSender, "who"), "acme.team1.ops1\tonline")
	})
	// Nothing else lives under the spool root: exactly the two distinct directories.
	for _, root := range []string{filepath.Join(state, "spool", "legowner"), filepath.Join(state, "spool", "team1")} {
		ents, err := os.ReadDir(root)
		if err != nil || len(ents) != 1 {
			t.Fatalf("%s: %v %v", root, ents, err)
		}
	}
	if legSpool == nfSpool {
		t.Fatal("spool directories collide")
	}
	if got := runHook(t, legEnv, "UserPromptSubmit"); got != "" {
		t.Fatalf("legacy empty spool printed %q", got)
	}
	if got := runHook(t, nfEnv, "UserPromptSubmit"); got != "" {
		t.Fatalf("new-form empty spool printed %q", got)
	}

	const legMsg, nfMsg = "LEGACY-ONLY payload alpha", "NEWFORM-ONLY payload beta"
	cliAs(t, legSender, "send", "ops1", legMsg)
	cliAs(t, nfSender, "send", "acme_team1_ops1", nfMsg)

	if ev := content(ls.await(t, 15*time.Second, isChannel)); ev != "1 message from lsender (task)" {
		t.Fatalf("legacy wake %q", ev)
	}
	if ev := content(ns.await(t, 15*time.Second, isChannel)); ev != "1 message from acme.team1.nsender (task)" {
		t.Fatalf("new-form wake %q", ev)
	}

	legOut := runHook(t, legEnv, "PostToolUse")
	nfOut := runHook(t, nfEnv, "PostToolUse")
	for _, want := range []string{"mash mail for ops1", "1 message from lsender (task)", "[verified]", "> " + legMsg} {
		if !strings.Contains(legOut, want) {
			t.Fatalf("legacy hook lacks %q:\n%s", want, legOut)
		}
	}
	for _, want := range []string{"1 message from acme.team1.nsender", "mash mail for acme.team1.ops1", "[verified]", "> " + nfMsg} {
		if !strings.Contains(nfOut, want) {
			t.Fatalf("new-form hook lacks %q:\n%s", want, nfOut)
		}
	}
	if strings.Contains(legOut, "NEWFORM") || strings.Contains(legOut, "nsender") || strings.Contains(legOut, "acme") {
		t.Fatalf("legacy hook shows new-form mail:\n%s", legOut)
	}
	if strings.Contains(nfOut, "LEGACY") || strings.Contains(nfOut, "lsender") || strings.Contains(nfOut, "legowner") {
		t.Fatalf("new-form hook shows legacy mail:\n%s", nfOut)
	}

	// Each serve acks only what its own hook showed, and each stream held only its own message.
	eventually(t, 5*time.Second, "legacy serve acks", func() bool { return pendingOf(t, e, sap.InboxStream("ops1")) == 0 })
	eventually(t, 5*time.Second, "new-form serve acks", func() bool { return pendingOf(t, e, "inbox-acme_team1_ops1") == 0 })
	js, err := jetstream.New(e.Conn(t, e.AdminCreds))
	if err != nil {
		t.Fatal(err)
	}
	for stream, subj := range map[string]string{"inbox-ops1": legMsg, "inbox-acme_team1_ops1": nfMsg} {
		s, err := js.Stream(context.Background(), stream)
		if err != nil {
			t.Fatal(err)
		}
		if info, err := s.Info(context.Background()); err != nil || info.State.Msgs != 1 {
			t.Fatalf("%s holds %+v %v, want exactly %q", stream, info, err, subj)
		}
	}

	// Re-running each hook shows nothing new, and the spool directories stay distinct on disk.
	if again := runHook(t, legEnv, "PostToolUse"); again != "" {
		t.Fatalf("legacy hook shows again: %q", again)
	}
	if again := runHook(t, nfEnv, "PostToolUse"); again != "" {
		t.Fatalf("new-form hook shows again: %q", again)
	}
	for _, d := range []string{legSpool, nfSpool} {
		if _, err := os.Stat(d); err != nil {
			t.Fatalf("spool %s: %v", d, err)
		}
	}
	if un := len(unlisted(spool.Spool{Dir: legSpool})); un != 0 {
		t.Fatalf("legacy spool has %d undrained entries", un)
	}
	if un := len(unlisted(spool.Spool{Dir: nfSpool})); un != 0 {
		t.Fatalf("new-form spool has %d undrained entries", un)
	}
}
