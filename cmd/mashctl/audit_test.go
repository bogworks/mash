package main

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/nats-io/jwt/v2"
	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"

	"github.com/bogworks/mash/internal/sap"
	"github.com/bogworks/mash/internal/store"
	"github.com/bogworks/mash/internal/testenv"
)

var updateAuditGolden = flag.Bool("update-audit", false, "rewrite cmd/mashctl/testdata/audit.golden.json")

// auditFixture is an account with legacy and new-form identities, an alias with undrained legacy mail, and one
// orphan of each kind.
type auditFixture struct {
	e        *testenv.Env
	js       jetstream.JetStream
	nc       *nats.Conn
	credsDir string
	users    map[string]string // public user key -> stable label, for the golden file
	seeds    []string          // every seed and JWT in the fixture's creds files: none may appear in any output
}

func (f *auditFixture) run(t *testing.T, args ...string) (string, string, int) {
	t.Helper()
	return ctlRun(t, f.e, append([]string{"audit", "--creds-dir", f.credsDir}, args...)...)
}

func (f *auditFixture) publish(t *testing.T, subject string, n int) {
	t.Helper()
	for i := 0; i < n; i++ {
		if _, err := f.js.Publish(context.Background(), subject, []byte(fmt.Sprintf("m%d", i))); err != nil {
			t.Fatalf("publish %s: %v", subject, err)
		}
	}
}

// putCreds copies a minted creds file into the creds directory as name.creds (0600) and records its secrets.
func (f *auditFixture) putCreds(t *testing.T, src, name string) {
	t.Helper()
	raw, err := os.ReadFile(src)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(f.credsDir, name+".creds"), raw, 0o600); err != nil {
		t.Fatal(err)
	}
	kp, err := jwt.ParseDecoratedNKey(raw)
	if err != nil {
		t.Fatal(err)
	}
	seed, _ := kp.Seed()
	tok, _ := jwt.ParseDecoratedJWT(raw)
	f.seeds = append(f.seeds, string(seed), tok)
}

// newForm builds a new-form directory record, its inbox stream and consumer directly through the store package:
// provisioning a three-token identity is step 1.6 and mashctl and admin still refuse it.
func (f *auditFixture) newForm(t *testing.T, agent, owner, name, user string, alias *store.Alias) sap.EndpointIdentity {
	t.Helper()
	ctx := context.Background()
	id, err := sap.New(agent, owner, name)
	if err != nil {
		t.Fatal(err)
	}
	s, err := f.js.CreateStream(ctx, store.InboxConfigIdentity(id, store.DefaultLimits()))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.CreateOrUpdateConsumer(ctx, store.ConsumerConfig()); err != nil {
		t.Fatal(err)
	}
	rec := store.Record{Name: name, Owner: owner, Agent: sap.AgentName, AgentToken: agent, Account: f.e.Account, User: user,
		MailSubject: id.Subjects().Mail, Created: "2026-11-10T00:00:00Z", Alias: alias}
	val, _ := json.Marshal(rec)
	kv, err := f.js.KeyValue(ctx, store.DirectoryBucket)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := kv.Create(ctx, id.Flat(), val); err != nil {
		t.Fatal(err)
	}
	return id
}

func (f *auditFixture) lease(t *testing.T, key, val string) {
	t.Helper()
	kv, err := f.js.KeyValue(context.Background(), store.LeaseBucket)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := kv.Put(context.Background(), key, []byte(val)); err != nil {
		t.Fatal(err)
	}
}

// pullUnacked takes one message from name's legacy consumer and leaves it unacknowledged.
func (f *auditFixture) pullUnacked(t *testing.T, name string) {
	t.Helper()
	c, err := f.js.Consumer(context.Background(), sap.InboxStream(name), store.Consumer)
	if err != nil {
		t.Fatal(err)
	}
	b, err := c.Fetch(1, jetstream.FetchMaxWait(5*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	n := 0
	for range b.Messages() {
		n++
	}
	if n != 1 {
		t.Fatalf("fetched %d", n)
	}
}

func (f *auditFixture) ackAll(t *testing.T, name string) {
	t.Helper()
	c, err := f.js.Consumer(context.Background(), sap.InboxStream(name), store.Consumer)
	if err != nil {
		t.Fatal(err)
	}
	for {
		b, err := c.Fetch(10, jetstream.FetchMaxWait(1500*time.Millisecond))
		if err != nil {
			t.Fatal(err)
		}
		n := 0
		for m := range b.Messages() {
			n++
			_ = m.DoubleAck(context.Background())
		}
		if n == 0 {
			return
		}
	}
}

func newAuditEnv(t *testing.T) *auditFixture {
	t.Helper()
	e := testenv.Start(t)
	if _, errb, code := ctlRun(t, e, "init"); code != 0 {
		t.Fatalf("init: %d %q", code, errb)
	}
	nc := e.Conn(t, e.AdminCreds)
	js, err := jetstream.New(nc)
	if err != nil {
		t.Fatal(err)
	}
	f := &auditFixture{e: e, js: js, nc: nc, credsDir: privateDir(t), users: map[string]string{}}
	adminUser, err := credsUserKey(e.AdminCreds)
	if err != nil {
		t.Fatal(err)
	}
	f.users[adminUser] = "USER-admin"
	return f
}

// legacy provisions a legacy endpoint and keeps its creds file in the creds directory.
func (f *auditFixture) legacy(t *testing.T, name string) string {
	t.Helper()
	creds, u := f.e.Mint(t, name)
	if _, errb, code := ctlRun(t, f.e, "provision", name, "--user", u, "--account", f.e.Account); code != 0 {
		t.Fatalf("provision %s: %d %q", name, code, errb)
	}
	f.users[u] = "USER-" + name
	f.putCreds(t, creds, name)
	return u
}

// buildAuditAccount is the full fixture: (a) legacy "alpha" with queued mail and a live lease, (b) new-form
// claude.team2.beta, (c) new-form acme.team1.ops1 with an alias to legacy "ops1" whose inbox still has an unacked
// message, (d) an orphan inbox stream, an orphan lease key and an orphan creds file.
func buildAuditAccount(t *testing.T) *auditFixture {
	f := newAuditEnv(t)
	owner := f.e.Owner

	f.legacy(t, "alpha")
	f.publish(t, sap.For(owner, "alpha").Mail, 3)
	f.lease(t, "alpha", `{"instance_id":"i1","host":"h"}`)

	betaCreds, betaUser := f.e.Mint(t, "beta")
	f.users[betaUser] = "USER-beta"
	f.putCreds(t, betaCreds, "claude_team2_beta")
	beta := f.newForm(t, "claude", "team2", "beta", betaUser, nil)
	f.publish(t, beta.Subjects().Mail, 2)

	f.legacy(t, "ops1")
	f.publish(t, sap.For(owner, "ops1").Mail, 2)
	f.pullUnacked(t, "ops1")
	_, opsUser := f.e.Mint(t, "ops1new")
	f.users[opsUser] = "USER-ops1new"
	ops := f.newForm(t, "acme", "team1", "ops1", opsUser, &store.Alias{Owner: owner, Name: "ops1"})
	f.publish(t, ops.Subjects().Mail, 1)
	f.lease(t, ops.Flat(), `{"instance_id":"i2","host":"h"}`)

	// (d) orphans: a stream with no record, a lease key with no record, a creds file whose user matches no record.
	ctx := context.Background()
	if _, err := f.js.CreateStream(ctx, store.InboxConfigFor(owner, "ghost", store.DefaultLimits())); err != nil {
		t.Fatal(err)
	}
	f.publish(t, sap.For(owner, "ghost").Mail, 1)
	f.lease(t, "phantom", `{"instance_id":"i3"}`)
	strayCreds, strayUser := f.e.Mint(t, "stray")
	f.users[strayUser] = "USER-stray"
	f.putCreds(t, strayCreds, "stray")
	// The admin's own creds file in the directory is not an endpoint's and is never an orphan.
	f.putCreds(t, f.e.AdminCreds, "admin")
	return f
}

// serverState is every stream (state, config, the legacy consumer's counts) and every KV entry with its revision.
func serverState(t *testing.T, js jetstream.JetStream) string {
	t.Helper()
	ctx := context.Background()
	var lines []string
	names := js.StreamNames(ctx)
	for n := range names.Name() {
		s, err := js.Stream(ctx, n)
		if err != nil {
			t.Fatal(err)
		}
		info, _ := s.Info(ctx)
		cfg, _ := json.Marshal(info.Config)
		lines = append(lines, fmt.Sprintf("stream %s msgs=%d first=%d last=%d cfg=%s", n, info.State.Msgs, info.State.FirstSeq, info.State.LastSeq, cfg))
		if c, err := s.Consumer(ctx, store.Consumer); err == nil {
			ci, _ := c.Info(ctx)
			lines = append(lines, fmt.Sprintf("consumer %s delivered=%d ackfloor=%d pending=%d ackpending=%d", n, ci.Delivered.Stream, ci.AckFloor.Stream, ci.NumPending, ci.NumAckPending))
		}
	}
	if err := names.Err(); err != nil {
		t.Fatal(err)
	}
	for _, b := range []string{store.DirectoryBucket, store.LeaseBucket} {
		kv, err := js.KeyValue(ctx, b)
		if err != nil {
			t.Fatal(err)
		}
		keys, _ := kv.Keys(ctx)
		for _, k := range keys {
			e, err := kv.Get(ctx, k)
			if err != nil {
				continue
			}
			lines = append(lines, fmt.Sprintf("kv %s %s rev=%d %s", b, k, e.Revision(), e.Value()))
		}
	}
	sort.Strings(lines)
	return strings.Join(lines, "\n")
}

// normalize replaces what differs between runs of the fixture (random user keys, the temp creds directory) with
// stable labels, so the output can be compared with the golden file.
func (f *auditFixture) normalize(s string) string {
	s = strings.ReplaceAll(s, f.credsDir, "<CREDS-DIR>")
	for k, v := range f.users {
		s = strings.ReplaceAll(s, k, v)
	}
	return s
}

func fixAuditClock(t *testing.T) {
	t.Helper()
	old := auditNow
	auditNow = func() time.Time { return time.Date(2026, 11, 10, 12, 0, 0, 0, time.UTC) }
	t.Cleanup(func() { auditNow = old })
}

func TestAuditJSONGolden(t *testing.T) {
	fixAuditClock(t)
	f := buildAuditAccount(t)
	out, errb, code := f.run(t, "--json")
	if code != 0 || errb != "" {
		t.Fatalf("audit --json: %d out=%q err=%q", code, out, errb)
	}
	got := f.normalize(out)
	golden := filepath.Join("testdata", "audit.golden.json")
	if *updateAuditGolden {
		if err := os.WriteFile(golden, []byte(got), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(golden)
	if err != nil {
		t.Fatalf("golden file: %v", err)
	}
	if got != string(want) {
		t.Fatalf("audit --json differs from %s (run with -update-audit to rewrite it after checking the diff)\n got: %s\nwant: %s", golden, got, want)
	}
}

// The golden file pins the bytes; these checks pin the meaning, so a wrong golden cannot pass for the right reason.
func TestAuditJSONContent(t *testing.T) {
	fixAuditClock(t)
	f := buildAuditAccount(t)
	out, _, code := f.run(t, "--json")
	if code != 0 {
		t.Fatalf("exit %d", code)
	}
	var r struct {
		Version   int    `json:"version"`
		Generated string `json:"generated"`
		Records   []struct {
			Key      string `json:"key"`
			Form     string `json:"form"`
			Running  bool   `json:"running"`
			Identity struct{ Agent, Owner, Name string }
			Alias    *struct{ Owner, Name string }
			Inbox    struct {
				Stream  string   `json:"stream"`
				Present bool     `json:"present"`
				Msgs    uint64   `json:"msgs"`
				First   uint64   `json:"first_seq"`
				Last    uint64   `json:"last_seq"`
				Subj    []string `json:"subjects"`
				Deliver struct {
					Pending    uint64 `json:"pending"`
					AckPending int    `json:"ack_pending"`
				} `json:"deliver"`
			} `json:"inbox"`
		} `json:"records"`
		Renames []struct {
			Identity    string `json:"identity"`
			TrustLabel  string `json:"trust_label"`
			Drained     bool   `json:"drained"`
			LegacyInbox struct {
				Stream  string `json:"stream"`
				Msgs    uint64 `json:"msgs"`
				Last    uint64 `json:"last_seq"`
				Deliver struct {
					Pending    uint64 `json:"pending"`
					AckPending int    `json:"ack_pending"`
				} `json:"deliver"`
			} `json:"legacy_inbox"`
			NewInbox struct {
				Stream string `json:"stream"`
				Msgs   uint64 `json:"msgs"`
				Last   uint64 `json:"last_seq"`
			} `json:"new_inbox"`
		} `json:"renames"`
		Orphans struct {
			Streams []struct{ Stream string } `json:"streams"`
			Leases  []struct{ Key string }    `json:"leases"`
			Creds   []struct{ File string }   `json:"creds"`
		} `json:"orphans"`
	}
	if err := json.Unmarshal([]byte(out), &r); err != nil {
		t.Fatal(err)
	}
	if r.Version != 1 || r.Generated != "2026-11-10T12:00:00Z" {
		t.Fatalf("version/generated: %d %q", r.Version, r.Generated)
	}
	keys := []string{}
	byKey := map[string]int{}
	for i, rec := range r.Records {
		keys = append(keys, rec.Key)
		byKey[rec.Key] = i
	}
	if want := []string{"acme_team1_ops1", "alpha", "claude_team2_beta", "ops1"}; strings.Join(keys, ",") != strings.Join(want, ",") {
		t.Fatalf("record keys %v, want sorted %v", keys, want)
	}
	alpha := r.Records[byKey["alpha"]]
	if alpha.Form != "legacy" || !alpha.Running || alpha.Inbox.Stream != "inbox-alpha" || alpha.Inbox.Msgs != 3 || alpha.Inbox.First != 1 || alpha.Inbox.Last != 3 || alpha.Inbox.Deliver.Pending != 3 {
		t.Errorf("alpha: %+v", alpha)
	}
	beta := r.Records[byKey["claude_team2_beta"]]
	if beta.Form != "new" || beta.Running || beta.Identity.Agent != "claude" || beta.Identity.Owner != "team2" || beta.Identity.Name != "beta" || beta.Inbox.Msgs != 2 ||
		len(beta.Inbox.Subj) != 1 || beta.Inbox.Subj[0] != "agents.mail.claude.team2.beta" {
		t.Errorf("beta: %+v", beta)
	}
	if len(r.Renames) != 1 {
		t.Fatalf("renames: %+v", r.Renames)
	}
	rn := r.Renames[0]
	if rn.Identity != "acme.team1.ops1" || rn.TrustLabel != "not-available" || rn.Drained ||
		rn.LegacyInbox.Stream != "inbox-ops1" || rn.LegacyInbox.Msgs != 2 || rn.LegacyInbox.Last != 2 || rn.LegacyInbox.Deliver.Pending != 1 || rn.LegacyInbox.Deliver.AckPending != 1 ||
		rn.NewInbox.Stream != "inbox-acme_team1_ops1" || rn.NewInbox.Msgs != 1 || rn.NewInbox.Last != 1 {
		t.Errorf("rename: %+v", rn)
	}
	if len(r.Orphans.Streams) != 1 || r.Orphans.Streams[0].Stream != "inbox-ghost" {
		t.Errorf("orphan streams: %+v", r.Orphans.Streams)
	}
	if len(r.Orphans.Leases) != 1 || r.Orphans.Leases[0].Key != "phantom" {
		t.Errorf("orphan leases: %+v", r.Orphans.Leases)
	}
	if len(r.Orphans.Creds) != 1 || filepath.Base(r.Orphans.Creds[0].File) != "stray.creds" {
		t.Errorf("orphan creds (the admin's and the matching files must not be listed): %+v", r.Orphans.Creds)
	}
}

func TestAuditDeterministicAndWritesNothing(t *testing.T) {
	f := buildAuditAccount(t) // the real clock: only "generated" may differ
	before := serverState(t, f.js)
	var outs [2]string
	for i := range outs {
		o, errb, code := f.run(t, "--json")
		if code != 0 || errb != "" {
			t.Fatalf("run %d: %d %q", i, code, errb)
		}
		outs[i] = o
	}
	gen := regexp.MustCompile(`"generated": "[^"]*"`)
	a, b := gen.ReplaceAllString(outs[0], ""), gen.ReplaceAllString(outs[1], "")
	if a != b {
		t.Fatalf("two runs differ:\n%s\n%s", outs[0], outs[1])
	}
	if !gen.MatchString(outs[0]) {
		t.Fatal("no generated field")
	}
	// the text form is deterministic too, and a --strict run changes nothing either
	t1, _, _ := f.run(t)
	t2, _, _ := f.run(t)
	if t1 != t2 || t1 == "" {
		t.Fatalf("text form differs or is empty:\n%s\n%s", t1, t2)
	}
	f.run(t, "--strict")
	if after := serverState(t, f.js); after != before {
		t.Fatalf("audit changed the server:\nbefore:\n%s\nafter:\n%s", before, after)
	}
}

func TestAuditTextForm(t *testing.T) {
	f := buildAuditAccount(t)
	out, _, code := f.run(t)
	if code != 0 {
		t.Fatalf("exit %d", code)
	}
	for _, want := range []string{"alpha", "acme.team1.ops1", "legacy", "new", "inbox-ghost", "phantom", "stray.creds", "not-available", "pending 1", "ack_pending 1"} {
		if !strings.Contains(out, want) {
			t.Errorf("text form lacks %q:\n%s", want, out)
		}
	}
}

func TestAuditNoSecrets(t *testing.T) {
	f := buildAuditAccount(t)
	var all string
	for _, args := range [][]string{{"--json"}, {}, {"--strict"}, {"--json", "--strict"}} {
		o, e, _ := f.run(t, args...)
		all += o + e
	}
	noSecrets(t, all)
	for _, s := range f.seeds {
		if s != "" && strings.Contains(all, s) {
			t.Fatal("output holds a seed or JWT from a creds file")
		}
	}
}

func TestAuditStrictExitCodes(t *testing.T) {
	fixAuditClock(t)
	f := buildAuditAccount(t)
	if _, _, code := f.run(t); code != 0 {
		t.Fatalf("exit without --strict: %d, want 0 even with orphans", code)
	}
	if _, errb, code := f.run(t, "--strict"); code != 1 || errb == "" {
		t.Fatalf("--strict with orphans and an undrained alias: %d %q", code, errb)
	}

	// A clean account: one legacy endpoint, one new-form with an alias whose legacy inbox is drained.
	g := newAuditEnv(t)
	g.legacy(t, "ops1")
	g.publish(t, sap.For(g.e.Owner, "ops1").Mail, 2)
	_, u := g.e.Mint(t, "n")
	g.users[u] = "USER-n"
	g.newForm(t, "acme", "team1", "ops1", u, &store.Alias{Owner: g.e.Owner, Name: "ops1"})
	if _, _, code := g.run(t, "--strict"); code != 1 {
		t.Fatalf("--strict with two unread legacy messages: %d, want 1", code)
	}
	g.ackAll(t, "ops1")
	if out, errb, code := g.run(t, "--strict"); code != 0 {
		t.Fatalf("--strict on a clean, drained account: %d out=%q err=%q", code, out, errb)
	}
	// an orphan alone fails it
	if _, err := g.js.CreateStream(context.Background(), store.InboxConfigFor(g.e.Owner, "ghost", store.DefaultLimits())); err != nil {
		t.Fatal(err)
	}
	if _, _, code := g.run(t, "--strict"); code != 1 {
		t.Fatalf("--strict with an orphan stream: %d, want 1", code)
	}
}

func TestAuditUsageAndErrors(t *testing.T) {
	e := testenv.Start(t)
	ctlRun(t, e, "init")
	for _, args := range [][]string{{"audit", "extra"}, {"audit", "--bogus"}, {"audit", "--creds-dir"}} {
		if _, errb, code := ctlRun(t, e, args...); code != 2 || !strings.Contains(errb, "usage: mashctl audit") {
			t.Errorf("%v: %d %q", args, code, errb)
		}
	}
	// no server: exit 1
	env := map[string]string{"HOME": t.TempDir(), "MASH_URL": "nats://127.0.0.1:1", "MASH_OWNER": "o", "MASH_ADMIN_CREDS": e.AdminCreds, "MASH_INSECURE_LOOPBACK": "1"}
	var out, errb bytes.Buffer
	if code := runEnv([]string{"audit"}, func(k string) string { return env[k] }, &out, &errb); code != 1 {
		t.Errorf("unreachable server: %d %q", code, errb.String())
	}
	// an empty account audits cleanly
	if out, _, code := ctlRun(t, e, "audit", "--json"); code != 0 || !strings.Contains(out, `"records": []`) {
		t.Errorf("empty account: %d %q", code, out)
	}
}

// recreateLegacyConsumer replaces name's legacy "deliver" consumer with one made from mutate(store.ConsumerConfig()).
func (f *auditFixture) recreateLegacyConsumer(t *testing.T, name string, mutate func(*jetstream.ConsumerConfig)) {
	t.Helper()
	ctx := context.Background()
	s, err := f.js.Stream(ctx, sap.InboxStream(name))
	if err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteConsumer(ctx, store.Consumer); err != nil {
		t.Fatal(err)
	}
	cfg := store.ConsumerConfig()
	mutate(&cfg)
	if _, err := s.CreateOrUpdateConsumer(ctx, cfg); err != nil {
		t.Fatal(err)
	}
}

// A legacy consumer recreated so that it skips the stored messages shows pending 0 and ack_pending 0 while the
// messages are unread: that is not drained, and the report shows the policy that explains it.
func TestAuditDrainedRequiresConsumerToCoverStoredMessages(t *testing.T) {
	cases := map[string]struct {
		mutate func(*jetstream.ConsumerConfig)
		policy string
	}{
		"deliver new": {func(c *jetstream.ConsumerConfig) { c.DeliverPolicy = jetstream.DeliverNewPolicy }, "new"},
		"start sequence": {func(c *jetstream.ConsumerConfig) {
			c.DeliverPolicy = jetstream.DeliverByStartSequencePolicy
			c.OptStartSeq = 4
		}, "by_start_sequence"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			g := newAuditEnv(t)
			g.legacy(t, "ops1")
			g.publish(t, sap.For(g.e.Owner, "ops1").Mail, 3)
			_, u := g.e.Mint(t, "n")
			g.users[u] = "USER-n"
			g.newForm(t, "acme", "team1", "ops1", u, &store.Alias{Owner: g.e.Owner, Name: "ops1"})
			g.recreateLegacyConsumer(t, "ops1", tc.mutate)

			out, _, code := g.run(t, "--json")
			if code != 0 {
				t.Fatalf("exit %d", code)
			}
			var r struct {
				Renames []struct {
					Drained     bool `json:"drained"`
					LegacyInbox struct {
						Msgs    uint64 `json:"msgs"`
						Deliver struct {
							Pending       uint64   `json:"pending"`
							AckPending    int      `json:"ack_pending"`
							DeliverPolicy string   `json:"deliver_policy"`
							StartSeq      uint64   `json:"start_seq"`
							FilterSubject []string `json:"filter_subjects"`
						} `json:"deliver"`
					} `json:"legacy_inbox"`
				} `json:"renames"`
			}
			if err := json.Unmarshal([]byte(out), &r); err != nil {
				t.Fatal(err)
			}
			if len(r.Renames) != 1 {
				t.Fatalf("renames: %s", out)
			}
			d := r.Renames[0].LegacyInbox.Deliver
			if r.Renames[0].LegacyInbox.Msgs != 3 || d.Pending != 0 || d.AckPending != 0 {
				t.Fatalf("fixture: want 3 stored messages and zero counts, got msgs %d pending %d ack_pending %d", r.Renames[0].LegacyInbox.Msgs, d.Pending, d.AckPending)
			}
			if r.Renames[0].Drained {
				t.Error("drained is true although the consumer skips stored messages")
			}
			if d.DeliverPolicy != tc.policy {
				t.Errorf("deliver_policy %q, want %q", d.DeliverPolicy, tc.policy)
			}
			if _, errb, code := g.run(t, "--strict"); code != 1 {
				t.Errorf("--strict exit %d (%q), want 1", code, errb)
			}
			if txt, _, _ := g.run(t); !strings.Contains(txt, "NOT drained") || !strings.Contains(txt, "policy "+tc.policy) {
				t.Errorf("text form lacks NOT drained or the policy:\n%s", txt)
			}
		})
	}
}

// The rule itself, for the cases a server fixture cannot build cheaply.
func TestAuditInboxDrainedRule(t *testing.T) {
	in := func(msgs, first, last uint64, d auditDeliver) auditInbox {
		return auditInbox{Present: true, Msgs: msgs, FirstSeq: first, LastSeq: last, Subjects: []string{"s.a"}, Deliver: d}
	}
	ok := auditDeliver{Present: true, DeliverPolicy: "all", AckFloor: 5}
	for name, tc := range map[string]struct {
		in   auditInbox
		want bool
	}{
		"gone":                 {auditInbox{}, true},
		"covers all":           {in(3, 3, 5, ok), true},
		"empty stream":         {in(0, 6, 5, auditDeliver{Present: true, DeliverPolicy: "new", AckFloor: 5}), true},
		"no consumer, empty":   {in(0, 6, 5, auditDeliver{}), true},
		"no consumer, msgs":    {in(1, 5, 5, auditDeliver{}), false},
		"pending":              {in(3, 3, 5, auditDeliver{Present: true, DeliverPolicy: "all", Pending: 1, AckFloor: 4}), false},
		"ack pending":          {in(3, 3, 5, auditDeliver{Present: true, DeliverPolicy: "all", AckPending: 1, AckFloor: 4}), false},
		"new":                  {in(3, 3, 5, auditDeliver{Present: true, DeliverPolicy: "new", AckFloor: 5}), false},
		"start after first":    {in(3, 3, 5, auditDeliver{Present: true, DeliverPolicy: "by_start_sequence", StartSeq: 4, AckFloor: 5}), false},
		"start at first":       {in(3, 3, 5, auditDeliver{Present: true, DeliverPolicy: "by_start_sequence", StartSeq: 3, AckFloor: 5}), true},
		"start time":           {in(3, 3, 5, auditDeliver{Present: true, DeliverPolicy: "by_start_time", AckFloor: 5}), false},
		"last":                 {in(3, 3, 5, auditDeliver{Present: true, DeliverPolicy: "last", AckFloor: 5}), false},
		"filter narrower":      {in(3, 3, 5, auditDeliver{Present: true, DeliverPolicy: "all", FilterSubjects: []string{"s.b"}, AckFloor: 5}), false},
		"filter same as subj":  {in(3, 3, 5, auditDeliver{Present: true, DeliverPolicy: "all", FilterSubjects: []string{"s.a"}, AckFloor: 5}), true},
		"ack floor below last": {in(3, 3, 5, auditDeliver{Present: true, DeliverPolicy: "all", AckFloor: 4}), false},
	} {
		if got := tc.in.drained(); got != tc.want {
			t.Errorf("%s: drained %v, want %v", name, got, tc.want)
		}
	}
}

// The text form carries every field of the JSON that decides a finding: suspended and deliver.present for records,
// renames and orphan streams, and the consumer's policy.
func TestAuditTextShowsSuspendedAndDeliver(t *testing.T) {
	susp := auditInbox{Stream: "inbox-s", Present: true, Msgs: 1, FirstSeq: 1, LastSeq: 1, Subjects: []string{"x"}, Suspended: true}
	live := auditInbox{Stream: "inbox-l", Present: true, Subjects: []string{"y"},
		Deliver: auditDeliver{Present: true, DeliverPolicy: "all", FilterSubjects: []string{"y"}}}
	rep := auditReport{Version: 1, Generated: "g", Owner: "o",
		Records: []auditRecord{{Key: "s", Form: "legacy", Inbox: susp}, {Key: "l", Form: "legacy", Inbox: live}},
		Renames: []auditRename{{Identity: "a.b.c", Alias: auditAlias{Owner: "o", Name: "s"}, LegacyInbox: susp, NewInbox: live}},
		Orphans: auditOrphans{Streams: []auditOrphanStream{{Stream: "inbox-z", Suspended: true}, {Stream: "inbox-q"}}},
	}
	var b strings.Builder
	writeAuditText(&b, rep)
	lines := strings.Split(b.String(), "\n")
	has := func(prefix string, subs ...string) {
		t.Helper()
		for _, l := range lines {
			if strings.HasPrefix(strings.TrimSpace(l), prefix) {
				for _, s := range subs {
					if !strings.Contains(l, s) {
						t.Errorf("line %q lacks %q", l, s)
					}
				}
				return
			}
		}
		t.Errorf("no line starting %q in:\n%s", prefix, b.String())
	}
	has("record s\t", "suspended true", "deliver false")
	has("record l\t", "suspended false", "deliver true", "policy all", "filter y")
	has("before", "suspended true", "deliver false")
	has("after", "suspended false", "deliver true")
	has("orphan stream inbox-z", "suspended true")
	has("orphan stream inbox-q", "suspended false")
}

// A directory entry that does not parse is a problem, but it still exists: its stream, lease and creds user are
// not orphans.
func TestAuditBadRecordClaimsItsResources(t *testing.T) {
	f := newAuditEnv(t)
	ctx := context.Background()
	owner := f.e.Owner
	u := f.legacy(t, "good") // a valid legacy record, for contrast
	_ = u
	creds, user := f.e.Mint(t, "broken")
	f.users[user] = "USER-broken"
	f.putCreds(t, creds, "broken")
	if _, err := f.js.CreateStream(ctx, store.InboxConfigFor(owner, "broken", store.DefaultLimits())); err != nil {
		t.Fatal(err)
	}
	f.lease(t, "broken", `{"instance_id":"i9"}`)
	// a valid key and user, an invalid mail subject
	val, _ := json.Marshal(store.Record{Name: "broken", Owner: owner, Agent: sap.AgentName, AgentToken: "cc", Account: f.e.Account,
		User: user, MailSubject: "not.the.subject", Created: "2026-11-10T00:00:00Z"})
	kv, err := f.js.KeyValue(ctx, store.DirectoryBucket)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := kv.Create(ctx, "broken", val); err != nil {
		t.Fatal(err)
	}

	out, _, code := f.run(t, "--json")
	if code != 0 {
		t.Fatalf("exit %d", code)
	}
	var r struct {
		Problems []struct{ What, Item string }
		Orphans  struct {
			Streams []struct{ Stream string }
			Leases  []struct{ Key string }
			Creds   []struct{ File string }
		}
	}
	if err := json.Unmarshal([]byte(out), &r); err != nil {
		t.Fatal(err)
	}
	if len(r.Problems) != 1 || r.Problems[0].Item != "broken" {
		t.Errorf("want the one problem for the entry: %+v", r.Problems)
	}
	if len(r.Orphans.Streams) != 0 || len(r.Orphans.Leases) != 0 || len(r.Orphans.Creds) != 0 {
		t.Errorf("a resource of an existing (unreadable) entry is reported as an orphan: %+v", r.Orphans)
	}
}
