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

// provisionNewForm provisions an endpoint at owner.group.instance directly, as the endpoint package's tests do, and
// returns its address and the environment its mash commands run with.
func provisionNewForm(t *testing.T, e *testenv.Env, owner, group, instance string) (sap.Address, map[string]string) {
	t.Helper()
	id, err := sap.NewAddress(owner, group, instance)
	if err != nil {
		t.Fatal(err)
	}
	creds, user := e.MintRaw(t, id.Flat(), *admin.Permissions(id))
	ctx := context.Background()
	js, err := jetstream.New(e.Conn(t, e.AdminCreds))
	if err != nil {
		t.Fatal(err)
	}
	s, err := js.CreateStream(ctx, store.InboxConfigFor(id, store.DefaultLimits()))
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
	rec := store.Record{Owner: id.Owner, Group: id.Group, Instance: id.Instance, Agent: sap.AgentName, Account: e.Account,
		User: user, MailSubject: id.Subjects().Mail, Created: time.Now().UTC().Format(time.RFC3339)}
	val, _ := json.Marshal(rec)
	if _, err := kv.Create(ctx, id.Flat(), val); err != nil {
		t.Fatal(err)
	}
	return id, map[string]string{"HOME": t.TempDir(), "MASH_ENDPOINT": id.Owner + "." + id.Group + "." + id.Instance,
		"MASH_SERVER": e.URL, "MASH_CREDS": creds, "MASH_SETTLE_MS": "20",
		"MASH_INSECURE_PLAINTEXT": "1"} // REQ-014: these tests are not about TLS; the override is explicit
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

// Two live endpoints with the same instance in different groups share one state directory: each spools to its own
// directory, each hook drains and shows only its own endpoint's mail, and each serve acks only its own.
func TestHookNextToEndpointOfTheSameInstanceInAnotherGroup(t *testing.T) {
	e := testenv.Start(t)
	adm, err := admin.New(e.Conn(t, e.AdminCreds), e.Owner)
	if err != nil {
		t.Fatal(err)
	}
	if err := adm.Init(context.Background()); err != nil {
		t.Fatal(err)
	}
	state := filepath.Join(t.TempDir(), "shared-state")
	aID, aEnv := provisionNewForm(t, e, "acme", "team1", "ops1")
	bID, bEnv := provisionNewForm(t, e, "acme", "team2", "ops1")
	_, aSender := provisionNewForm(t, e, "acme", "team1", "asender")
	_, bSender := provisionNewForm(t, e, "acme", "team2", "bsender")
	for _, env := range []map[string]string{aEnv, bEnv} {
		env["MASH_STATE_DIR"] = state // one shared state directory: a collision would show
		env["MASH_IDLE_WAKE"] = "5s"
	}
	if aID.Flat() != "acme_team1_ops1" || bID.Flat() != "acme_team2_ops1" || endpointPart(aEnv, 2) != endpointPart(bEnv, 2) {
		t.Fatalf("setup: flat %q %q names %q %q", aID.Flat(), bID.Flat(), endpointPart(aEnv, 2), endpointPart(bEnv, 2))
	}

	aSpool := filepath.Join(state, "spool", "acme", "acme_team1_ops1")
	bSpool := filepath.Join(state, "spool", "acme", "acme_team2_ops1")

	as, bs := startServe(t, aEnv), startServe(t, bEnv)
	as.initialize(t)
	bs.initialize(t)
	as.tools(t)
	bs.tools(t)
	for _, d := range []string{aSpool, bSpool} {
		d := d
		eventually(t, 5*time.Second, "serve made the spool "+d, func() bool { _, err := os.Stat(d); return err == nil })
	}
	eventually(t, 5*time.Second, "both online", func() bool {
		return strings.Contains(cliAsQuiet(aSender, "who"), "acme.team1.ops1\tonline") && strings.Contains(cliAsQuiet(bSender, "who"), "acme.team2.ops1\tonline")
	})
	// Nothing else lives under the spool root: exactly the two distinct directories.
	ents, err := os.ReadDir(filepath.Join(state, "spool", "acme"))
	if err != nil || len(ents) != 2 {
		t.Fatalf("spool root: %v %v", ents, err)
	}
	if got := runHook(t, aEnv, "UserPromptSubmit"); got != "" {
		t.Fatalf("empty spool printed %q", got)
	}
	if got := runHook(t, bEnv, "UserPromptSubmit"); got != "" {
		t.Fatalf("empty spool printed %q", got)
	}

	const aMsg, bMsg = "TEAM-ONE-ONLY payload alpha", "TEAM-TWO-ONLY payload beta"
	cliAs(t, aSender, "send", "acme.team1.ops1", aMsg)
	cliAs(t, bSender, "send", "acme_team2_ops1", bMsg)

	if ev := content(as.await(t, 15*time.Second, isChannel)); ev != "1 message from acme.team1.asender (task)" {
		t.Fatalf("team1 wake %q", ev)
	}
	if ev := content(bs.await(t, 15*time.Second, isChannel)); ev != "1 message from acme.team2.bsender (task)" {
		t.Fatalf("team2 wake %q", ev)
	}

	aOut := runHook(t, aEnv, "PostToolUse")
	bOut := runHook(t, bEnv, "PostToolUse")
	for _, want := range []string{"mash mail for acme.team1.ops1", "1 message from acme.team1.asender", "[verified]", "> " + aMsg} {
		if !strings.Contains(aOut, want) {
			t.Fatalf("team1 hook lacks %q:\n%s", want, aOut)
		}
	}
	for _, want := range []string{"mash mail for acme.team2.ops1", "1 message from acme.team2.bsender", "[verified]", "> " + bMsg} {
		if !strings.Contains(bOut, want) {
			t.Fatalf("team2 hook lacks %q:\n%s", want, bOut)
		}
	}
	if strings.Contains(aOut, "TEAM-TWO") || strings.Contains(aOut, "bsender") || strings.Contains(aOut, "team2") {
		t.Fatalf("team1 hook shows team2 mail:\n%s", aOut)
	}
	if strings.Contains(bOut, "TEAM-ONE") || strings.Contains(bOut, "asender") || strings.Contains(bOut, "team1") {
		t.Fatalf("team2 hook shows team1 mail:\n%s", bOut)
	}

	// Each serve acks only what its own hook showed, and each stream held only its own message.
	eventually(t, 5*time.Second, "team1 serve acks", func() bool { return pendingOf(t, e, aID.InboxStream()) == 0 })
	eventually(t, 5*time.Second, "team2 serve acks", func() bool { return pendingOf(t, e, bID.InboxStream()) == 0 })
	js, err := jetstream.New(e.Conn(t, e.AdminCreds))
	if err != nil {
		t.Fatal(err)
	}
	for stream, subj := range map[string]string{aID.InboxStream(): aMsg, bID.InboxStream(): bMsg} {
		s, err := js.Stream(context.Background(), stream)
		if err != nil {
			t.Fatal(err)
		}
		if info, err := s.Info(context.Background()); err != nil || info.State.Msgs != 1 {
			t.Fatalf("%s holds %+v %v, want exactly %q", stream, info, err, subj)
		}
	}

	// Re-running each hook shows nothing new, and the spool directories stay distinct on disk.
	if again := runHook(t, aEnv, "PostToolUse"); again != "" {
		t.Fatalf("team1 hook shows again: %q", again)
	}
	if again := runHook(t, bEnv, "PostToolUse"); again != "" {
		t.Fatalf("team2 hook shows again: %q", again)
	}
	for _, d := range []string{aSpool, bSpool} {
		if un := len(unlisted(spool.Spool{Dir: d})); un != 0 {
			t.Fatalf("spool %s has %d undrained entries", d, un)
		}
	}
}
