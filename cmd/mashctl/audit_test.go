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

// auditFixture is an account with endpoints, queued and unacknowledged mail, and one orphan of each kind.
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

// pullUnacked takes one message from the endpoint's consumer and leaves it unacknowledged.
func (f *auditFixture) pullUnacked(t *testing.T, a sap.Address) {
	t.Helper()
	c, err := f.js.Consumer(context.Background(), a.InboxStream(), store.Consumer)
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

func (f *auditFixture) ackAll(t *testing.T, a sap.Address) {
	t.Helper()
	c, err := f.js.Consumer(context.Background(), a.InboxStream(), store.Consumer)
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

// endpoint provisions the endpoint at a and keeps its creds file in the creds directory as owner.group.instance.creds.
func (f *auditFixture) endpoint(t *testing.T, a sap.Address) string {
	t.Helper()
	creds, u := f.e.Mint(t, a)
	if _, errb, code := ctlRun(t, f.e, "provision", a.String(), "--user", u, "--account", f.e.Account); code != 0 {
		t.Fatalf("provision %s: %d %q", a, code, errb)
	}
	f.users[u] = "USER-" + a.Instance
	f.putCreds(t, creds, a.String())
	return u
}

// The addresses of the full fixture.
var (
	addrAlpha = ad("alpha")
	addrBeta  = sap.Address{Owner: testenv.Owner, Group: "team2", Instance: "beta"}
	addrOps   = sap.Address{Owner: testenv.Owner, Group: "team1", Instance: "ops1"}
	addrGhost = ad("ghost")
)

// buildAuditAccount is the full fixture: (a) o.g.alpha with queued mail and a live lease, (b) o.team2.beta with queued
// mail, (c) o.team1.ops1 with an unacknowledged message and a live lease, (d) an orphan inbox stream, an orphan lease
// key and an orphan creds file.
func buildAuditAccount(t *testing.T) *auditFixture {
	f := newAuditEnv(t)

	f.endpoint(t, addrAlpha)
	f.publish(t, addrAlpha.Subjects().Mail, 3)
	f.lease(t, addrAlpha.Flat(), `{"instance_id":"i1","host":"h"}`)

	f.endpoint(t, addrBeta)
	f.publish(t, addrBeta.Subjects().Mail, 2)

	f.endpoint(t, addrOps)
	f.publish(t, addrOps.Subjects().Mail, 2)
	f.pullUnacked(t, addrOps)
	f.lease(t, addrOps.Flat(), `{"instance_id":"i2","host":"h"}`)

	// (d) orphans: a stream with no record, a lease key with no record, a creds file whose user matches no record.
	ctx := context.Background()
	if _, err := f.js.CreateStream(ctx, store.InboxConfigFor(addrGhost, store.DefaultLimits())); err != nil {
		t.Fatal(err)
	}
	f.publish(t, addrGhost.Subjects().Mail, 1)
	f.lease(t, "phantom", `{"instance_id":"i3"}`)
	strayCreds, strayUser := f.e.Mint(t, ad("stray"))
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
	s = strings.ReplaceAll(s, f.e.Account, "ACCOUNT") // the account's public key is random per run
	// message and consumer times are the real time of the run
	s = regexp.MustCompile(`"(last_time|delivered_last|ack_floor_last|created)": "[^"]*"`).ReplaceAllString(s, `"$1": "<TIME>"`)
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
			Address  string `json:"address"`
			Owner    string `json:"owner"`
			Group    string `json:"group"`
			Instance string `json:"instance"`
			Running  bool   `json:"running"`
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
		Orphans struct {
			Streams []struct{ Stream string } `json:"streams"`
			Leases  []struct{ Key string }    `json:"leases"`
			Creds   []struct{ File string }   `json:"creds"`
		} `json:"orphans"`
	}
	if err := json.Unmarshal([]byte(out), &r); err != nil {
		t.Fatal(err)
	}
	for _, gone := range []string{`"identity"`, `"form"`, `"alias"`, `"renames"`, `"agent_token"`, `"name"`} {
		if strings.Contains(out, gone) {
			t.Errorf("the report still carries %s", gone)
		}
	}
	if r.Version != auditVersion || r.Generated != "2026-11-10T12:00:00Z" {
		t.Fatalf("version/generated: %d %q", r.Version, r.Generated)
	}
	keys := []string{}
	byKey := map[string]int{}
	for i, rec := range r.Records {
		keys = append(keys, rec.Key)
		byKey[rec.Key] = i
	}
	if want := []string{"o_g_alpha", "o_team1_ops1", "o_team2_beta"}; strings.Join(keys, ",") != strings.Join(want, ",") {
		t.Fatalf("record keys %v, want sorted %v", keys, want)
	}
	alpha := r.Records[byKey["o_g_alpha"]]
	if alpha.Address != "o.g.alpha" || alpha.Owner != "o" || alpha.Group != "g" || alpha.Instance != "alpha" ||
		!alpha.Running || alpha.Inbox.Stream != "inbox-o_g_alpha" || alpha.Inbox.Msgs != 3 || alpha.Inbox.First != 1 || alpha.Inbox.Last != 3 || alpha.Inbox.Deliver.Pending != 3 {
		t.Errorf("alpha: %+v", alpha)
	}
	beta := r.Records[byKey["o_team2_beta"]]
	if beta.Address != "o.team2.beta" || beta.Group != "team2" || beta.Instance != "beta" || beta.Running || beta.Inbox.Msgs != 2 ||
		len(beta.Inbox.Subj) != 1 || beta.Inbox.Subj[0] != "agents.mail.o.team2.beta" {
		t.Errorf("beta: %+v", beta)
	}
	ops := r.Records[byKey["o_team1_ops1"]]
	if ops.Address != "o.team1.ops1" || !ops.Running || ops.Inbox.Msgs != 2 || ops.Inbox.Deliver.Pending != 1 || ops.Inbox.Deliver.AckPending != 1 {
		t.Errorf("ops1: %+v", ops)
	}
	if len(r.Orphans.Streams) != 1 || r.Orphans.Streams[0].Stream != "inbox-o_g_ghost" {
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
	// the text header prints the real time of the run (`generated <RFC3339>`): mask it, as for the JSON form
	genText := regexp.MustCompile(`generated \S+`)
	t1, t2 = genText.ReplaceAllString(t1, "generated <TIME>"), genText.ReplaceAllString(t2, "generated <TIME>")
	if t1 != t2 || t1 == "" || !strings.Contains(t1, "generated <TIME>") {
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
	for _, want := range []string{"o.g.alpha", "o.team1.ops1", "o.team2.beta", "inbox-o_g_ghost", "phantom", "stray.creds", "pending 1", "ack_pending 1"} {
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
		t.Fatalf("--strict with orphans: %d %q", code, errb)
	}

	// A clean account: one endpoint, all its mail read.
	g := newAuditEnv(t)
	g.endpoint(t, addrOps)
	g.publish(t, addrOps.Subjects().Mail, 2)
	g.ackAll(t, addrOps)
	if out, errb, code := g.run(t, "--strict"); code != 0 {
		t.Fatalf("--strict on a clean account: %d out=%q err=%q", code, out, errb)
	}
	// an orphan alone fails it
	if _, err := g.js.CreateStream(context.Background(), store.InboxConfigFor(addrGhost, store.DefaultLimits())); err != nil {
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
	// no server: exit 3 (could not complete)
	env := map[string]string{"HOME": t.TempDir(), "MASH_URL": "nats://127.0.0.1:1", "MASH_OWNER": "o", "MASH_ADMIN_CREDS": e.AdminCreds, "MASH_INSECURE_PLAINTEXT": "1"}
	var out, errb bytes.Buffer
	if code := runEnv([]string{"audit"}, func(k string) string { return env[k] }, &out, &errb); code != 3 {
		t.Errorf("unreachable server: %d %q", code, errb.String())
	}
	// an empty account audits cleanly
	if out, _, code := ctlRun(t, e, "audit", "--json"); code != 0 || !strings.Contains(out, `"records": []`) {
		t.Errorf("empty account: %d %q", code, out)
	}
}

// recreateConsumer replaces the endpoint's "deliver" consumer with one made from mutate(store.ConsumerConfig()).
func (f *auditFixture) recreateConsumer(t *testing.T, a sap.Address, mutate func(*jetstream.ConsumerConfig)) {
	t.Helper()
	ctx := context.Background()
	s, err := f.js.Stream(ctx, a.InboxStream())
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

// A consumer recreated so that it skips the stored messages shows pending 0 and ack_pending 0 while the messages are
// unread: that is not drained, so deprovisioning the record afterwards reads as a vanished inbox (mail may have been
// lost), not a retirement, and the report shows the policy that explains it.
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
			g.endpoint(t, addrOps)
			g.publish(t, addrOps.Subjects().Mail, 3)
			g.recreateConsumer(t, addrOps, tc.mutate)

			out, _, code := g.run(t, "--json")
			if code != 0 {
				t.Fatalf("exit %d", code)
			}
			var r struct {
				Records []struct {
					Inbox struct {
						Msgs    uint64 `json:"msgs"`
						Deliver struct {
							Pending       uint64 `json:"pending"`
							AckPending    int    `json:"ack_pending"`
							DeliverPolicy string `json:"deliver_policy"`
						} `json:"deliver"`
					} `json:"inbox"`
				} `json:"records"`
			}
			if err := json.Unmarshal([]byte(out), &r); err != nil || len(r.Records) != 1 {
				t.Fatalf("records: %v %s", err, out)
			}
			in := r.Records[0].Inbox
			if in.Msgs != 3 || in.Deliver.Pending != 0 || in.Deliver.AckPending != 0 {
				t.Fatalf("fixture: want 3 stored messages and zero counts, got msgs %d pending %d ack_pending %d", in.Msgs, in.Deliver.Pending, in.Deliver.AckPending)
			}
			if in.Deliver.DeliverPolicy != tc.policy {
				t.Errorf("deliver_policy %q, want %q", in.Deliver.DeliverPolicy, tc.policy)
			}
			if txt, _, _ := g.run(t); !strings.Contains(txt, "policy "+tc.policy) {
				t.Errorf("text form lacks the policy:\n%s", txt)
			}
			first := g.auditTo(t, "first.json")
			g.deprovisionAndForget(t, addrOps)
			c, code := g.compareJSON(t, first)
			if code != 1 || c.Compare.Vanished != 1 || c.Compare.Retired != 0 {
				t.Errorf("a consumer that skips stored messages is not drained: exit %d %+v", code, c.Compare)
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
		"gone":               {auditInbox{}, true},
		"covers all":         {in(3, 3, 5, ok), true},
		"empty stream":       {in(0, 6, 5, auditDeliver{Present: true, DeliverPolicy: "new", AckFloor: 5}), true},
		"no consumer, empty": {in(0, 6, 5, auditDeliver{}), true},
		"no consumer, msgs":  {in(1, 5, 5, auditDeliver{}), false},
		"pending":            {in(3, 3, 5, auditDeliver{Present: true, DeliverPolicy: "all", Pending: 1, AckFloor: 4}), false},
		"ack pending":        {in(3, 3, 5, auditDeliver{Present: true, DeliverPolicy: "all", AckPending: 1, AckFloor: 4}), false},
		"new":                {in(3, 3, 5, auditDeliver{Present: true, DeliverPolicy: "new", AckFloor: 5}), false},
		"start after first":  {in(3, 3, 5, auditDeliver{Present: true, DeliverPolicy: "by_start_sequence", StartSeq: 4, AckFloor: 5}), false},
		"start at first":     {in(3, 3, 5, auditDeliver{Present: true, DeliverPolicy: "by_start_sequence", StartSeq: 3, AckFloor: 5}), true},
		"start time":         {in(3, 3, 5, auditDeliver{Present: true, DeliverPolicy: "by_start_time", AckFloor: 5}), false},
		"last":               {in(3, 3, 5, auditDeliver{Present: true, DeliverPolicy: "last", AckFloor: 5}), false},
		"filter narrower":    {in(3, 3, 5, auditDeliver{Present: true, DeliverPolicy: "all", FilterSubjects: []string{"s.b"}, AckFloor: 5}), false},
		// A filter equal to the stream's CURRENT subjects can still skip stored messages from subjects the stream
		// captured earlier, so only an unfiltered consumer proves the drain.
		"filter same as subj":  {in(3, 3, 5, auditDeliver{Present: true, DeliverPolicy: "all", FilterSubjects: []string{"s.a"}, AckFloor: 5}), false},
		"filter two subjects":  {in(3, 3, 5, auditDeliver{Present: true, DeliverPolicy: "all", FilterSubjects: []string{"s.a", "s.b"}, AckFloor: 5}), false},
		"ack floor below last": {in(3, 3, 5, auditDeliver{Present: true, DeliverPolicy: "all", AckFloor: 4}), false},
	} {
		if got := tc.in.drained(); got != tc.want {
			t.Errorf("%s: drained %v, want %v", name, got, tc.want)
		}
	}
}

// The text form carries every field of the JSON that decides a finding: suspended and deliver.present for records and
// orphan streams, and the consumer's policy.
func TestAuditTextShowsSuspendedAndDeliver(t *testing.T) {
	susp := auditInbox{Stream: "inbox-s", Present: true, Msgs: 1, FirstSeq: 1, LastSeq: 1, Subjects: []string{"x"}, Suspended: true}
	live := auditInbox{Stream: "inbox-l", Present: true, Subjects: []string{"y"},
		Deliver: auditDeliver{Present: true, DeliverPolicy: "all", FilterSubjects: []string{"y"}}}
	rep := auditReport{Version: 1, Generated: "g", Owner: "o",
		Records: []auditRecord{{Key: "s", Address: "o.g.s", Inbox: susp}, {Key: "l", Address: "o.g.l", Inbox: live}},
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
	has("record s\t", "address o.g.s")
	has("orphan stream inbox-z", "suspended true")
	has("orphan stream inbox-q", "suspended false")
}

// A directory entry that does not parse is a problem, but it still exists: its stream, lease and creds user are
// not orphans.
func TestAuditBadRecordClaimsItsResources(t *testing.T) {
	f := newAuditEnv(t)
	ctx := context.Background()
	f.endpoint(t, ad("good")) // a valid record, for contrast
	broken := ad("broken")
	creds, user := f.e.Mint(t, broken)
	f.users[user] = "USER-broken"
	f.putCreds(t, creds, "broken")
	if _, err := f.js.CreateStream(ctx, store.InboxConfigFor(broken, store.DefaultLimits())); err != nil {
		t.Fatal(err)
	}
	f.lease(t, broken.Flat(), `{"instance_id":"i9"}`)
	// a valid key and user, an invalid mail subject
	val, _ := json.Marshal(store.Record{Owner: broken.Owner, Group: broken.Group, Instance: broken.Instance, Agent: "claude", Account: f.e.Account,
		User: user, MailSubject: "not.the.subject", Created: "2026-11-10T00:00:00Z"})
	kv, err := f.js.KeyValue(ctx, store.DirectoryBucket)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := kv.Create(ctx, broken.Flat(), val); err != nil {
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
	if len(r.Problems) != 1 || r.Problems[0].Item != broken.Flat() {
		t.Errorf("want the one problem for the entry: %+v", r.Problems)
	}
	if len(r.Orphans.Streams) != 0 || len(r.Orphans.Leases) != 0 || len(r.Orphans.Creds) != 0 {
		t.Errorf("a resource of an existing (unreadable) entry is reported as an orphan: %+v", r.Orphans)
	}
}

func (f *auditFixture) auditTo(t *testing.T, name string) string {
	t.Helper()
	out, errb, code := f.run(t, "--json")
	if code != 0 {
		t.Fatalf("audit --json: %d %q", code, errb)
	}
	p := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(p, []byte(out), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

type cmpOut struct {
	Compare *struct {
		Moved    int      `json:"moved"`
		Vanished int      `json:"vanished"`
		Retired  int      `json:"retired"`
		Removed  []string `json:"identities_removed"`
		Added    []string `json:"identities_added"`
		Streams  []struct {
			Stream         string `json:"stream"`
			LastSeqBefore  uint64 `json:"last_seq_before"`
			LastSeqNow     uint64 `json:"last_seq_now"`
			LastSeqMoved   bool   `json:"last_seq_moved"`
			DeliveredMoved bool   `json:"delivered_moved"`
			AckFloorMoved  bool   `json:"ack_floor_moved"`
			Vanished       bool   `json:"vanished"`
			Retired        bool   `json:"retired"`
		} `json:"streams"`
	} `json:"compare"`
}

// cleanAccount is one endpoint, o.g.alpha, whose mail is all read.
func cleanAccount(t *testing.T) *auditFixture {
	f := newAuditEnv(t)
	f.endpoint(t, ad("alpha"))
	f.publish(t, ad("alpha").Subjects().Mail, 2)
	f.ackAll(t, ad("alpha"))
	return f
}

func TestAuditCompareMovement(t *testing.T) {
	f := cleanAccount(t)
	first := f.auditTo(t, "first.json")
	if _, errb, code := f.run(t, "--strict"); code != 0 {
		t.Fatalf("baseline not strict clean: %d %q", code, errb)
	}
	// nothing moves: passes
	out, errb, code := f.run(t, "--strict", "--json", "--compare", first)
	if code != 0 {
		t.Fatalf("nothing moved, --strict --compare: %d %q\n%s", code, errb, out)
	}
	var c cmpOut
	if err := json.Unmarshal([]byte(out), &c); err != nil || c.Compare == nil || c.Compare.Moved != 0 || len(c.Compare.Streams) != 1 {
		t.Fatalf("compare object: %v %+v", err, c.Compare)
	}
	// one more message that is not read: last_seq moves, delivered does not
	f.publish(t, ad("alpha").Subjects().Mail, 1)
	out, errb, code = f.run(t, "--json", "--compare", first)
	if code != 0 {
		t.Fatalf("without --strict: %d %q", code, errb)
	}
	c = cmpOut{}
	if err := json.Unmarshal([]byte(out), &c); err != nil || c.Compare == nil || c.Compare.Moved != 1 {
		t.Fatalf("compare: %v %s", err, out)
	}
	s := c.Compare.Streams[0]
	if s.Stream != "inbox-o_g_alpha" || !s.LastSeqMoved || s.LastSeqBefore != 2 || s.LastSeqNow != 3 || s.DeliveredMoved {
		t.Errorf("stream compare: %+v", s)
	}
	// a live inbox takes mail: movement is shown, and is not a finding
	if _, errb, code := f.run(t, "--strict", "--compare", first); code != 0 {
		t.Fatalf("one more message is not a finding for --strict --compare: %d %q", code, errb)
	}
	// read and acknowledged: delivered and ack floor move too
	f.ackAll(t, ad("alpha"))
	out, _, _ = f.run(t, "--json", "--compare", first)
	c = cmpOut{}
	_ = json.Unmarshal([]byte(out), &c)
	if s := c.Compare.Streams[0]; !s.DeliveredMoved || !s.AckFloorMoved {
		t.Errorf("delivered and ack floor must show movement: %+v", s)
	}
	// text form
	txt, _, _ := f.run(t, "--compare", first)
	if !strings.Contains(txt, "compare") || !strings.Contains(txt, "inbox-o_g_alpha") || !strings.Contains(txt, "moved") {
		t.Errorf("text compare:\n%s", txt)
	}
	// compare is read only
	before := serverState(t, f.js)
	f.run(t, "--strict", "--compare", first)
	if serverState(t, f.js) != before {
		t.Error("compare changed the server")
	}
}

func TestAuditCompareIdentities(t *testing.T) {
	f := cleanAccount(t)
	first := f.auditTo(t, "first.json")
	f.endpoint(t, ad("gamma"))
	out, _, code := f.run(t, "--json", "--compare", first)
	var c cmpOut
	if code != 0 || json.Unmarshal([]byte(out), &c) != nil || c.Compare == nil || len(c.Compare.Added) != 1 || c.Compare.Added[0] != "o_g_gamma" || len(c.Compare.Removed) != 0 {
		t.Fatalf("added: %d %+v", code, c.Compare)
	}
	second := f.auditTo(t, "second.json")
	if _, errb, code := ctlRun(t, f.e, "deprovision", "o.g.gamma"); code != 0 {
		t.Fatalf("deprovision: %d %q", code, errb)
	}
	out, _, _ = f.run(t, "--json", "--compare", second)
	c = cmpOut{}
	_ = json.Unmarshal([]byte(out), &c)
	if c.Compare == nil || len(c.Compare.Removed) != 1 || c.Compare.Removed[0] != "o_g_gamma" {
		t.Fatalf("removed: %+v", c.Compare)
	}
}

func TestAuditCompareStrictFailsOnOrphanOrUndrained(t *testing.T) {
	f := cleanAccount(t)
	first := f.auditTo(t, "first.json")
	if _, err := f.js.CreateStream(context.Background(), store.InboxConfigFor(addrGhost, store.DefaultLimits())); err != nil {
		t.Fatal(err)
	}
	if _, _, code := f.run(t, "--strict", "--compare", first); code != 1 {
		t.Fatalf("orphan with --compare --strict: %d", code)
	}
}

func TestAuditCompareBadFile(t *testing.T) {
	f := cleanAccount(t)
	dir := t.TempDir()
	write := func(name, s string) string {
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, []byte(s), 0o600); err != nil {
			t.Fatal(err)
		}
		return p
	}
	for name, p := range map[string]string{
		"malformed": write("a.json", "{not json"),
		"version":   write("b.json", `{"version": 1, "records": []}`),
		"noversion": write("c.json", `{"records": []}`),
		"missing":   filepath.Join(dir, "nope.json"),
	} {
		out, errb, code := f.run(t, "--compare", p)
		if code != 3 || errb == "" || out != "" {
			t.Errorf("%s: exit %d out=%q err=%q, want 3 and nothing on stdout", name, code, out, errb)
		}
	}
	if _, errb, code := f.run(t, "--compare"); code != 2 || !strings.Contains(errb, "usage: mashctl audit") {
		t.Errorf("--compare without a value: %d %q", code, errb)
	}
}

func TestAuditStreamEvidenceFields(t *testing.T) {
	f := cleanAccount(t)
	out, _, _ := f.run(t, "--json")
	var r struct {
		Records []struct {
			Inbox struct {
				LastSeq  uint64 `json:"last_seq"`
				LastTime string `json:"last_time"`
				Deliver  struct {
					Delivered      uint64 `json:"delivered"`
					DeliveredCons  uint64 `json:"delivered_consumer_seq"`
					DeliveredLast  string `json:"delivered_last"`
					AckFloor       uint64 `json:"ack_floor"`
					AckFloorCons   uint64 `json:"ack_floor_consumer_seq"`
					AckFloorLast   string `json:"ack_floor_last"`
					NumRedelivered *int   `json:"num_redelivered"`
				} `json:"deliver"`
			} `json:"inbox"`
		} `json:"records"`
	}
	if err := json.Unmarshal([]byte(out), &r); err != nil || len(r.Records) != 1 {
		t.Fatal(err, out)
	}
	in := r.Records[0].Inbox
	d := in.Deliver
	if in.LastSeq != 2 || in.LastTime == "" || d.Delivered != 2 || d.DeliveredCons != 2 || d.DeliveredLast == "" || d.AckFloor != 2 || d.AckFloorCons != 2 || d.AckFloorLast == "" || d.NumRedelivered == nil {
		t.Errorf("evidence fields missing: %+v", in)
	}
	if _, err := time.Parse(time.RFC3339Nano, in.LastTime); err != nil {
		t.Errorf("last_time: %v", err)
	}
}

type notesOut struct {
	Notes   []string `json:"notes"`
	Orphans struct {
		Creds []struct {
			File   string `json:"file"`
			Staged bool   `json:"staged"`
		} `json:"creds"`
	} `json:"orphans"`
	Problems []struct{ What, Item, Error string } `json:"problems"`
}

func TestAuditNotesSymlinkAndMissingDir(t *testing.T) {
	f := cleanAccount(t)
	if err := os.Symlink(filepath.Join(f.credsDir, "o.g.alpha.creds"), filepath.Join(f.credsDir, "link.creds")); err != nil {
		t.Fatal(err)
	}
	out, errb, code := f.run(t, "--json", "--strict")
	var n notesOut
	if code != 0 || json.Unmarshal([]byte(out), &n) != nil {
		t.Fatalf("notes are not findings: %d %q", code, errb)
	}
	if len(n.Notes) != 1 || !strings.Contains(n.Notes[0], "link.creds") || !strings.Contains(n.Notes[0], "symbolic link") {
		t.Errorf("symlink note: %q", n.Notes)
	}
	txt, _, _ := f.run(t)
	if !strings.Contains(txt, "note") || !strings.Contains(txt, "link.creds") {
		t.Errorf("text form lacks the note:\n%s", txt)
	}
	// the default creds directory is missing (a fresh HOME): a note, exit 0
	out, errb, code = ctlRun(t, f.e, "audit", "--json")
	n = notesOut{}
	if code != 0 || json.Unmarshal([]byte(out), &n) != nil || len(n.Notes) == 0 || !strings.Contains(strings.Join(n.Notes, "\n"), "does not exist") {
		t.Errorf("missing default dir: %d %q notes=%q", code, errb, n.Notes)
	}
	if !sort.StringsAreSorted(n.Notes) {
		t.Errorf("notes not sorted: %q", n.Notes)
	}
}

func (f *auditFixture) putStaged(t *testing.T, src, name string) {
	t.Helper()
	raw, err := os.ReadFile(src)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(f.credsDir, name+".creds.next"), raw, 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestAuditStagedCreds(t *testing.T) {
	f := cleanAccount(t)
	// a staged file whose user matches a record is not an orphan; one that matches none is, with staged true
	alphaCreds := filepath.Join(f.credsDir, "o.g.alpha.creds")
	f.putStaged(t, alphaCreds, "o.g.alpha")
	out, _, code := f.run(t, "--json", "--strict")
	var n notesOut
	if code != 0 || json.Unmarshal([]byte(out), &n) != nil || len(n.Orphans.Creds) != 0 {
		t.Fatalf("matching staged file: %d %+v", code, n.Orphans)
	}
	strayCreds, _ := f.e.Mint(t, ad("stray"))
	f.putStaged(t, strayCreds, "stray")
	out, errb, code := f.run(t, "--json", "--strict")
	n = notesOut{}
	if code != 1 || errb == "" || json.Unmarshal([]byte(out), &n) != nil || len(n.Orphans.Creds) != 1 || !n.Orphans.Creds[0].Staged || filepath.Base(n.Orphans.Creds[0].File) != "stray.creds.next" {
		t.Fatalf("stray staged file: %d %q %+v", code, errb, n.Orphans)
	}
	noSecrets(t, out)
	// an unreadable staged file is a problem
	if err := os.WriteFile(filepath.Join(f.credsDir, "junk.creds.next"), []byte("junk"), 0o600); err != nil {
		t.Fatal(err)
	}
	out, _, code = f.run(t, "--json", "--strict")
	n = notesOut{}
	_ = json.Unmarshal([]byte(out), &n)
	found := false
	for _, p := range n.Problems {
		found = found || (p.What == "creds" && strings.HasSuffix(p.Item, "junk.creds.next"))
	}
	if code != 1 || !found {
		t.Errorf("junk staged file must be a problem: %d %+v", code, n.Problems)
	}
}

func TestAuditExitCodeThreeCouldNotComplete(t *testing.T) {
	f := cleanAccount(t)
	if _, errb, code := ctlRun(t, f.e, "audit", "--creds-dir", filepath.Join(f.credsDir, "nope")); code != 3 {
		t.Errorf("unreadable required creds directory: %d %q, want 3", code, errb)
	}
}

// vanishFixture is an account with one inbox ("alpha", with mail either queued or all acknowledged), its
// first audit saved, and the helpers the vanish tests share.
func vanishFixture(t *testing.T, drained bool) (*auditFixture, string) {
	f := newAuditEnv(t)
	f.endpoint(t, ad("alpha"))
	f.publish(t, ad("alpha").Subjects().Mail, 2)
	if drained {
		f.ackAll(t, ad("alpha"))
	}
	return f, f.auditTo(t, "first.json")
}

func (f *auditFixture) dropStream(t *testing.T, a sap.Address) {
	t.Helper()
	if err := f.js.DeleteStream(context.Background(), a.InboxStream()); err != nil {
		t.Fatal(err)
	}
}

func (f *auditFixture) deprovisionAndForget(t *testing.T, a sap.Address) {
	t.Helper()
	if _, errb, code := ctlRun(t, f.e, "deprovision", a.String()); code != 0 {
		t.Fatalf("deprovision: %d %q", code, errb)
	}
	if err := os.Remove(filepath.Join(f.credsDir, a.String()+".creds")); err != nil {
		t.Fatal(err)
	}
}

func (f *auditFixture) compareJSON(t *testing.T, earlier string) (cmpOut, int) {
	t.Helper()
	out, errb, code := f.run(t, "--strict", "--json", "--compare", earlier)
	var c cmpOut
	if code == 3 || json.Unmarshal([]byte(out), &c) != nil || c.Compare == nil {
		t.Fatalf("compare: %d %q\n%s", code, errb, out)
	}
	return c, code
}

func TestAuditCompareVanishedWithMailAndRecordGone(t *testing.T) {
	f, first := vanishFixture(t, false)
	f.deprovisionAndForget(t, ad("alpha"))
	c, code := f.compareJSON(t, first)
	if code != 1 || c.Compare.Vanished != 1 || c.Compare.Retired != 0 || len(c.Compare.Streams) != 1 || !c.Compare.Streams[0].Vanished || c.Compare.Streams[0].LastSeqBefore != 2 {
		t.Fatalf("queued mail gone with its record: exit %d %+v", code, c.Compare)
	}
	txt, _, _ := f.run(t, "--compare", first)
	if !strings.Contains(txt, "VANISHED") {
		t.Errorf("text form must show the vanished inbox:\n%s", txt)
	}
}

func TestAuditCompareVanishedStreamDeletedRecordKept(t *testing.T) {
	f, first := vanishFixture(t, false)
	f.dropStream(t, ad("alpha"))
	c, code := f.compareJSON(t, first)
	if code != 1 || c.Compare.Vanished != 1 || !c.Compare.Streams[0].Vanished {
		t.Fatalf("stream deleted, record kept: exit %d %+v", code, c.Compare)
	}
}

func TestAuditCompareRetiredWhenDrainedAndRecordGone(t *testing.T) {
	f, first := vanishFixture(t, true)
	f.deprovisionAndForget(t, ad("alpha"))
	c, code := f.compareJSON(t, first)
	if code != 0 || c.Compare.Retired != 1 || c.Compare.Vanished != 0 || c.Compare.Moved != 0 || !c.Compare.Streams[0].Retired || c.Compare.Streams[0].Vanished {
		t.Fatalf("drained inbox deprovisioned: exit %d %+v", code, c.Compare)
	}
	txt, _, _ := f.run(t, "--compare", first)
	if !strings.Contains(txt, "RETIRED") {
		t.Errorf("text form must show the retired inbox:\n%s", txt)
	}
}

func TestAuditCompareVanishedWhenDrainedButRecordKept(t *testing.T) {
	f, first := vanishFixture(t, true)
	f.dropStream(t, ad("alpha"))
	c, code := f.compareJSON(t, first)
	if code != 1 || c.Compare.Vanished != 1 || c.Compare.Retired != 0 || !c.Compare.Streams[0].Vanished {
		t.Fatalf("drained, only the stream gone: exit %d %+v", code, c.Compare)
	}
}

// recreateStream deletes the endpoint's inbox stream and makes it again from the same configuration, with its
// `deliver` consumer, refilled with n messages that are all read and acknowledged: the sequences come out as before.
func (f *auditFixture) recreateStream(t *testing.T, a sap.Address, n int) {
	t.Helper()
	ctx := context.Background()
	s, err := f.js.Stream(ctx, a.InboxStream())
	if err != nil {
		t.Fatal(err)
	}
	info, err := s.Info(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.js.DeleteStream(ctx, a.InboxStream()); err != nil {
		t.Fatal(err)
	}
	ns, err := f.js.CreateStream(ctx, info.Config)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ns.CreateOrUpdateConsumer(ctx, store.ConsumerConfig()); err != nil {
		t.Fatal(err)
	}
	f.publish(t, a.Subjects().Mail, n)
	f.ackAll(t, a)
}

type recreatedOut struct {
	Compare *struct {
		Recreated       int `json:"recreated"`
		CreationUnknown int `json:"creation_unknown"`
		Moved           int `json:"moved"`
		Streams         []struct {
			Recreated       bool   `json:"recreated"`
			CreationUnknown bool   `json:"creation_unknown"`
			CreatedBefore   string `json:"created_before"`
			CreatedNow      string `json:"created_now"`
			DeliverBefore   string `json:"deliver_created_before"`
			DeliverNow      string `json:"deliver_created_now"`
		} `json:"streams"`
	} `json:"compare"`
}

func (f *auditFixture) recreatedJSON(t *testing.T, earlier string) (recreatedOut, string, int) {
	t.Helper()
	out, errb, code := f.run(t, "--strict", "--json", "--compare", earlier)
	var c recreatedOut
	if code == 3 || json.Unmarshal([]byte(out), &c) != nil || c.Compare == nil || len(c.Compare.Streams) != 1 {
		t.Fatalf("compare: %d %q\n%s", code, errb, out)
	}
	return c, errb, code
}

func TestAuditCompareStreamRecreatedSameSequences(t *testing.T) {
	f, first := vanishFixture(t, true)
	f.recreateStream(t, ad("alpha"), 2)
	c, errb, code := f.recreatedJSON(t, first)
	s := c.Compare.Streams[0]
	if code != 1 || c.Compare.Recreated != 1 || !s.Recreated || s.CreatedBefore == "" || s.CreatedNow == "" || s.CreatedBefore == s.CreatedNow {
		t.Fatalf("recreated stream with equal sequences: exit %d err=%q %+v", code, errb, c.Compare)
	}
	if c.Compare.Moved != 0 {
		t.Errorf("sequences are equal, nothing moved: %+v", c.Compare)
	}
	if txt, _, _ := f.run(t, "--compare", first); !strings.Contains(txt, "RECREATED") {
		t.Errorf("text form must show RECREATED:\n%s", txt)
	}
}

func TestAuditCompareDeliverConsumerRecreated(t *testing.T) {
	f, first := vanishFixture(t, true)
	f.recreateConsumer(t, ad("alpha"), func(c *jetstream.ConsumerConfig) {})
	f.ackAll(t, ad("alpha"))
	c, errb, code := f.recreatedJSON(t, first)
	s := c.Compare.Streams[0]
	if code != 1 || c.Compare.Recreated != 1 || !s.Recreated || s.DeliverBefore == "" || s.DeliverNow == "" || s.DeliverBefore == s.DeliverNow || s.CreatedBefore != s.CreatedNow {
		t.Fatalf("only the deliver consumer recreated: exit %d err=%q %+v", code, errb, c.Compare)
	}
}

func TestAuditCompareNothingRecreatedPasses(t *testing.T) {
	f, first := vanishFixture(t, true)
	c, errb, code := f.recreatedJSON(t, first)
	if code != 0 || c.Compare.Recreated != 0 || c.Compare.CreationUnknown != 0 || c.Compare.Streams[0].Recreated {
		t.Fatalf("nothing recreated: exit %d err=%q %+v", code, errb, c.Compare)
	}
}

// deleteJSONPath removes a dotted path from a decoded JSON object.
func deleteJSONPath(m map[string]any, path string) {
	head, rest, nested := strings.Cut(path, ".")
	if !nested {
		delete(m, head)
		return
	}
	deleteJSONPath(m[head].(map[string]any), rest)
}

// Every field in earlierInboxFields is required of an earlier report: with just that field removed from a record's
// inbox, the report is refused (exit 3, nothing on stdout) and the message names the JSON path. The test iterates the
// table, so a later field cannot be missed.
func TestAuditCompareEarlierMissingAnyTableFieldIsRefused(t *testing.T) {
	f, first := vanishFixture(t, true)
	for _, fld := range earlierInboxFields {
		p := writeEdited(t, first, func(m map[string]any) {
			deleteJSONPath(m["records"].([]any)[0].(map[string]any)["inbox"].(map[string]any), fld.Path)
		})
		out, errb, code := f.run(t, "--strict", "--compare", p)
		want := "records[0].inbox." + fld.Path
		if code != 3 || out != "" || !strings.Contains(errb, "--compare") || !strings.Contains(errb, "lacks "+want) {
			t.Errorf("without %s: exit %d out=%q err=%q, want 3, nothing on stdout and %q named", fld.Path, code, out, errb, want)
		}
	}
}

// A null is not a value: a count of null is as missing as an absent key.
func TestAuditCompareEarlierNullFieldIsRefused(t *testing.T) {
	f, first := vanishFixture(t, true)
	p := writeEdited(t, first, func(m map[string]any) {
		m["records"].([]any)[0].(map[string]any)["inbox"].(map[string]any)["msgs"] = nil
	})
	if out, errb, code := f.run(t, "--strict", "--compare", p); code != 3 || out != "" || !strings.Contains(errb, "records[0].inbox.msgs") {
		t.Errorf("null msgs: %d %q %q", code, out, errb)
	}
}

// The table stays the single source: the docs list every field in it.
func TestAuditDocsListEveryEarlierField(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "..", "docs", "audit.md"))
	if err != nil {
		t.Fatal(err)
	}
	for _, fld := range earlierInboxFields {
		if !strings.Contains(string(raw), "| `"+fld.Path+"` |") {
			t.Errorf("docs/audit.md lacks the table row for %s", fld.Path)
		}
	}
}

// The fields below `present` are required only of a present inbox, and below `deliver.present` only of a present
// consumer: an earlier inbox that was absent needs just its stream and present.
func TestAuditCompareEarlierAbsentInboxNeedsOnlyStreamAndPresent(t *testing.T) {
	f, first := vanishFixture(t, true)
	p := writeEdited(t, first, func(m map[string]any) {
		in := m["records"].([]any)[0].(map[string]any)["inbox"].(map[string]any)
		stream := in["stream"]
		clear(in)
		in["stream"], in["present"] = stream, false
	})
	if out, errb, code := f.run(t, "--strict", "--compare", p); code == 3 {
		t.Errorf("absent earlier inbox with only stream and present: %d %q %q", code, out, errb)
	}
	p = writeEdited(t, first, func(m map[string]any) {
		in := m["records"].([]any)[0].(map[string]any)["inbox"].(map[string]any)
		in["deliver"] = map[string]any{"present": false}
	})
	if out, errb, code := f.run(t, "--strict", "--compare", p); code == 3 {
		t.Errorf("earlier inbox without a consumer: %d %q %q", code, out, errb)
	}
}

// An earlier inbox without msgs that later vanishes with its record must not read as a safe retirement: the missing
// count would be 0, which counts as drained. The report is refused instead.
func TestAuditCompareVanishedWithMsgsAbsentIsRefused(t *testing.T) {
	f, first := vanishFixture(t, false)
	p := writeEdited(t, first, func(m map[string]any) {
		delete(m["records"].([]any)[0].(map[string]any)["inbox"].(map[string]any), "msgs")
	})
	f.deprovisionAndForget(t, ad("alpha"))
	out, errb, code := f.run(t, "--strict", "--json", "--compare", p)
	if code != 3 || out != "" || !strings.Contains(errb, "records[0].inbox.msgs") || strings.Contains(out, "retired") {
		t.Fatalf("msgs absent, inbox and record gone: exit %d out=%q err=%q", code, out, errb)
	}
}

// msgs 0 is present, so an empty inbox that vanishes with its record is still a retirement.
func TestAuditCompareMsgsZeroPresentIsRetired(t *testing.T) {
	f := newAuditEnv(t)
	f.endpoint(t, ad("alpha"))
	first := f.auditTo(t, "first.json")
	var rep struct {
		Records []struct {
			Inbox struct {
				Msgs *uint64 `json:"msgs"`
			} `json:"inbox"`
		} `json:"records"`
	}
	raw, _ := os.ReadFile(first)
	if err := json.Unmarshal(raw, &rep); err != nil || rep.Records[0].Inbox.Msgs == nil || *rep.Records[0].Inbox.Msgs != 0 {
		t.Fatalf("fixture must write msgs 0 explicitly: %v %s", err, raw)
	}
	f.deprovisionAndForget(t, ad("alpha"))
	c, code := f.compareJSON(t, first)
	if code != 0 || c.Compare.Retired != 1 || c.Compare.Vanished != 0 {
		t.Fatalf("empty inbox with msgs 0 present: exit %d %+v", code, c.Compare)
	}
}

// An earlier audit made before the `created` fields cannot prove the stream was not recreated: it is refused and
// the message says to regenerate it.
func TestAuditCompareEarlierWithoutCreationTimesIsRefused(t *testing.T) {
	f, first := vanishFixture(t, true)
	p := writeEdited(t, first, func(m map[string]any) {
		in := m["records"].([]any)[0].(map[string]any)["inbox"].(map[string]any)
		delete(in, "created")
		delete(in["deliver"].(map[string]any), "created")
	})
	for _, args := range [][]string{{"--compare", p}, {"--strict", "--json", "--compare", p}} {
		out, errb, code := f.run(t, args...)
		if code != 3 || out != "" || !strings.Contains(errb, "created") || !strings.Contains(errb, "regenerate") {
			t.Errorf("%v: exit %d out=%q err=%q", args, code, out, errb)
		}
	}
}

// A consumer filtered to the stream's current subjects can skip stored messages from subjects the stream captured
// earlier, so zero pending and an ack floor at last_seq prove nothing: only an unfiltered consumer proves the drain.
func TestAuditDrainedFilteredConsumerIsNotDrained(t *testing.T) {
	g := newAuditEnv(t)
	g.endpoint(t, addrOps)
	g.publish(t, addrOps.Subjects().Mail, 3)
	// the filter is exactly the stream's current subjects
	st, err := g.js.Stream(context.Background(), addrOps.InboxStream())
	if err != nil {
		t.Fatal(err)
	}
	if len(st.CachedInfo().Config.Subjects) != 1 {
		t.Fatalf("stream subjects: %v", st.CachedInfo().Config.Subjects)
	}
	g.recreateConsumer(t, addrOps, func(c *jetstream.ConsumerConfig) { c.FilterSubject = st.CachedInfo().Config.Subjects[0] })
	g.ackAll(t, addrOps)

	first := g.auditTo(t, "first.json")
	raw, _ := os.ReadFile(first)
	var r struct {
		Records []struct {
			Inbox struct {
				Msgs    uint64 `json:"msgs"`
				LastSeq uint64 `json:"last_seq"`
				Deliver struct {
					Pending    uint64   `json:"pending"`
					AckPending int      `json:"ack_pending"`
					AckFloor   uint64   `json:"ack_floor"`
					Filters    []string `json:"filter_subjects"`
				} `json:"deliver"`
			} `json:"inbox"`
		} `json:"records"`
	}
	if err := json.Unmarshal(raw, &r); err != nil || len(r.Records) != 1 {
		t.Fatalf("report: %v %s", err, raw)
	}
	in := r.Records[0].Inbox
	if in.Msgs != 3 || in.Deliver.Pending != 0 || in.Deliver.AckPending != 0 || in.Deliver.AckFloor != in.LastSeq || len(in.Deliver.Filters) != 1 {
		t.Fatalf("fixture: want 3 messages, nothing pending, ack floor at last_seq and one filter, got %+v", in)
	}
	g.deprovisionAndForget(t, addrOps)
	c, code := g.compareJSON(t, first)
	if code != 1 || c.Compare.Vanished != 1 || c.Compare.Retired != 0 {
		t.Errorf("a filtered consumer is not drained: exit %d %+v", code, c.Compare)
	}
}

// writeEdited writes the report at path with edit applied to its decoded JSON, and returns the new path.
func writeEdited(t *testing.T, path string, edit func(m map[string]any)) string {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatal(err)
	}
	edit(m)
	b, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(t.TempDir(), "edited.json")
	if err := os.WriteFile(p, b, 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

// An earlier report must be a real audit of this deployment: a stub, another owner's or another account's report
// could not show that anything is unchanged.
func TestAuditCompareRefusesReportThatIsNotThisDeployments(t *testing.T) {
	f, first := vanishFixture(t, true)
	recInbox := func(m map[string]any) map[string]any {
		return m["records"].([]any)[0].(map[string]any)["inbox"].(map[string]any)
	}
	cases := map[string]struct {
		path string
		want string
	}{
		"stub":            {writeEdited(t, first, func(m map[string]any) { clear(m); m["version"] = auditVersion }), "generated"},
		"no generated":    {writeEdited(t, first, func(m map[string]any) { delete(m, "generated") }), "generated"},
		"empty generated": {writeEdited(t, first, func(m map[string]any) { m["generated"] = "" }), "generated"},
		"no owner":        {writeEdited(t, first, func(m map[string]any) { delete(m, "owner") }), "owner"},
		"no records":      {writeEdited(t, first, func(m map[string]any) { delete(m, "records") }), "records"},
		"no stream name":  {writeEdited(t, first, func(m map[string]any) { delete(recInbox(m), "stream") }), "stream"},
		"no last_seq":     {writeEdited(t, first, func(m map[string]any) { delete(recInbox(m), "last_seq") }), "last_seq"},
		"no delivered":    {writeEdited(t, first, func(m map[string]any) { delete(recInbox(m)["deliver"].(map[string]any), "delivered") }), "delivered"},
		"no ack_floor":    {writeEdited(t, first, func(m map[string]any) { delete(recInbox(m)["deliver"].(map[string]any), "ack_floor") }), "ack_floor"},
		"another owner":   {writeEdited(t, first, func(m map[string]any) { m["owner"] = "otherowner" }), "owner"},
		"another account": {writeEdited(t, first, func(m map[string]any) { m["account"] = "AOTHERACCOUNTKEY" }), "account"},
	}
	for name, tc := range cases {
		out, errb, code := f.run(t, "--strict", "--compare", tc.path)
		if code != 3 || out != "" || !strings.Contains(errb, "--compare") || !strings.Contains(errb, tc.want) {
			t.Errorf("%s: exit %d out=%q err=%q, want 3, nothing on stdout and %q named", name, code, out, errb, tc.want)
		}
	}
	// a valid earlier report (as written) is accepted, and carries this deployment's account
	var rep struct {
		Account string `json:"account"`
	}
	raw, _ := os.ReadFile(first)
	if err := json.Unmarshal(raw, &rep); err != nil || rep.Account != f.e.Account {
		t.Fatalf("report account %q, want %q (%v)", rep.Account, f.e.Account, err)
	}
	if out, errb, code := f.run(t, "--strict", "--compare", first); code != 0 {
		t.Errorf("valid earlier report: exit %d out=%q err=%q", code, out, errb)
	}
	// empty arrays are fine when the keys are present
	empty := writeEdited(t, first, func(m map[string]any) { m["records"] = []any{} })
	if _, errb, code := f.run(t, "--compare", empty); code != 0 {
		t.Errorf("empty arrays, keys present: exit %d %q", code, errb)
	}
}

// An earlier audit made before the account field cannot be bound to this deployment: non-strict shows a note,
// --strict fails and says to regenerate.
func TestAuditCompareEarlierWithoutAccount(t *testing.T) {
	f, first := vanishFixture(t, true)
	old := writeEdited(t, first, func(m map[string]any) { delete(m, "account") })
	out, errb, code := f.run(t, "--strict", "--json", "--compare", old)
	if code != 1 || !strings.Contains(errb, "account") || !strings.Contains(errb, "regenerate") {
		t.Fatalf("strict: exit %d err=%q", code, errb)
	}
	var c struct {
		Compare struct {
			AccountUnverified bool `json:"account_unverified"`
		} `json:"compare"`
	}
	if err := json.Unmarshal([]byte(out), &c); err != nil || !c.Compare.AccountUnverified {
		t.Errorf("account_unverified not set: %v\n%s", err, out)
	}
	out, errb, code = f.run(t, "--json", "--compare", old)
	if code != 0 || !strings.Contains(out, `"account_unverified": true`) {
		t.Errorf("non-strict: exit %d err=%q\n%s", code, errb, out)
	}
	if txt, _, _ := f.run(t, "--compare", old); !strings.Contains(txt, "account not verified") {
		t.Errorf("text form must say the account is not verified:\n%s", txt)
	}
}

// A redelivery leaves the delivered stream sequence alone and advances the consumer sequence: --compare must still
// show the mail was read again.
func TestAuditCompareRedeliveryMovesConsumerSeq(t *testing.T) {
	f := cleanAccount(t)
	f.publish(t, ad("alpha").Subjects().Mail, 1)
	fetchOne := func(nak bool) {
		t.Helper()
		c, err := f.js.Consumer(context.Background(), ad("alpha").InboxStream(), store.Consumer)
		if err != nil {
			t.Fatal(err)
		}
		b, err := c.Fetch(1, jetstream.FetchMaxWait(3*time.Second))
		if err != nil {
			t.Fatal(err)
		}
		n := 0
		for m := range b.Messages() {
			n++
			if nak {
				_ = m.Nak()
			}
		}
		if n != 1 {
			t.Fatalf("fetched %d messages, want 1", n)
		}
	}
	fetchOne(true) // delivered, then negatively acknowledged
	first := f.auditTo(t, "first.json")
	if out, errb, code := f.run(t, "--strict", "--json", "--compare", first); code != 0 {
		t.Fatalf("nothing moved: %d %q\n%s", code, errb, out)
	}
	fetchOne(false) // redelivered: stream sequence unchanged, consumer sequence advances
	out, errb, code := f.run(t, "--strict", "--json", "--compare", first)
	if code != 0 {
		t.Fatalf("a redelivery is shown, and is not a finding: %d %q", code, errb)
	}
	var c struct {
		Compare struct {
			Moved   int `json:"moved"`
			Streams []struct {
				DeliveredMoved    bool   `json:"delivered_moved"`
				ConsumerSeqBefore uint64 `json:"consumer_seq_before"`
				ConsumerSeqNow    uint64 `json:"consumer_seq_now"`
				ConsumerSeqMoved  bool   `json:"consumer_seq_moved"`
			} `json:"streams"`
		} `json:"compare"`
	}
	if err := json.Unmarshal([]byte(out), &c); err != nil || c.Compare.Moved != 1 || len(c.Compare.Streams) != 1 {
		t.Fatalf("compare: %v %s", err, out)
	}
	s := c.Compare.Streams[0]
	if s.DeliveredMoved || !s.ConsumerSeqMoved || s.ConsumerSeqNow <= s.ConsumerSeqBefore {
		t.Errorf("want consumer sequence moved and stream sequence not: %+v", s)
	}
	if txt, _, _ := f.run(t, "--compare", first); !strings.Contains(txt, "MOVED") || !strings.Contains(txt, "consumer_seq") {
		t.Errorf("text form:\n%s", txt)
	}
}

// A null consumer sequence is as missing as an absent key.
func TestAuditCompareEarlierNullConsumerSeqIsRefused(t *testing.T) {
	f, first := vanishFixture(t, true)
	p := writeEdited(t, first, func(m map[string]any) {
		m["records"].([]any)[0].(map[string]any)["inbox"].(map[string]any)["deliver"].(map[string]any)["delivered_consumer_seq"] = nil
	})
	if out, errb, code := f.run(t, "--strict", "--compare", p); code != 3 || out != "" || !strings.Contains(errb, "deliver.delivered_consumer_seq") {
		t.Errorf("null delivered_consumer_seq: %d %q %q", code, out, errb)
	}
}
