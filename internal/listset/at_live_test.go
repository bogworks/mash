package listset

import (
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/nats-io/jwt/v2"
	"github.com/nats-io/nats-server/v2/server"
	"github.com/nats-io/nats.go"
	"github.com/nats-io/nkeys"
)

// Live tests of REQ-020 (AT-003 second half, AT-005, AT-020's open subjects). Each runs an in-process nats-server in
// operator mode and drives it with only a sender's own credentials (the signed user JWT of the compile: tags and no
// permissions) and a plain nats.go client. Where a list set changes, the account JWT is pushed as mashctl does, a
// claims update on the system account; the push closes every connection under the key (ADR 0097), so each phase
// connects afresh.

// pushEnv is a liveEnv whose account JWT can be replaced in a running server.
type pushEnv struct {
	*liveEnv
	op  nkeys.KeyPair
	sys *nats.Conn
}

const pushWait = 10 * time.Second

// must returns v, or panics: a setup failure aborts the test and the panic names it.
func must[T any](v T, err error) T {
	if err != nil {
		panic("live test setup: " + err.Error())
	}
	return v
}

func (e *pushEnv) accountJWT(c *Compiled) string {
	ac := jwt.NewAccountClaims(e.accPub)
	ac.Name = c.Range
	for _, tpl := range c.Templates {
		sk, ok := e.scoped[tpl.Group]
		if !ok {
			sk = must(nkeys.CreateAccount())
			e.scoped[tpl.Group] = sk
		}
		us := jwt.NewUserScope()
		us.Key = must(sk.PublicKey())
		us.Template.Permissions = tpl.Permissions
		ac.SigningKeys.AddScopedSigner(us)
	}
	return must(ac.Encode(e.op))
}

func newPushEnv(t *testing.T, c *Compiled) *pushEnv {
	t.Helper()
	op := must(nkeys.CreateOperator())
	opPub := must(op.PublicKey())
	oc := jwt.NewOperatorClaims(opPub)
	oc.Name = "listset-live"
	opJWT := must(oc.Encode(op))
	opClaims := must(jwt.DecodeOperatorClaims(opJWT))
	acc := must(nkeys.CreateAccount())
	e := &pushEnv{op: op, liveEnv: &liveEnv{t: t, acc: acc, accPub: must(acc.PublicKey()), scoped: map[string]nkeys.KeyPair{}}}

	sysKP := must(nkeys.CreateAccount())
	sysPub := must(sysKP.PublicKey())
	sc := jwt.NewAccountClaims(sysPub)
	sc.Name = "SYS"
	resolver := must(server.NewDirAccResolver(t.TempDir(), 0, 5*time.Minute, server.NoDelete))
	if err := resolver.Store(sysPub, must(sc.Encode(op))); err != nil {
		t.Fatal(err)
	}
	if err := resolver.Store(e.accPub, e.accountJWT(c)); err != nil {
		t.Fatal(err)
	}
	s := must(server.NewServer(&server.Options{
		Host: "127.0.0.1", Port: -1, NoLog: true, NoSigs: true,
		TrustedOperators: []*jwt.OperatorClaims{opClaims}, AccountResolver: resolver, SystemAccount: sysPub,
	}))
	go s.Start()
	if !s.ReadyForConnections(pushWait) {
		t.Fatal("nats-server not ready")
	}
	t.Cleanup(func() { s.Shutdown(); s.WaitForShutdown() })
	e.url = s.ClientURL()

	su := must(nkeys.CreateUser())
	tok := must(jwt.NewUserClaims(must(su.PublicKey())).Encode(sysKP))
	e.sys = must(nats.Connect(e.url, nats.UserJWT(
		func() (string, error) { return tok, nil }, func(n []byte) ([]byte, error) { return su.Sign(n) })))
	t.Cleanup(e.sys.Close)
	return e
}

// push replaces the account JWT with one carrying c's templates and waits for the server's answer.
func (e *pushEnv) push(c *Compiled) {
	e.t.Helper()
	msg, err := e.sys.Request("$SYS.REQ.CLAIMS.UPDATE", []byte(e.accountJWT(c)), pushWait)
	if err != nil {
		e.t.Fatalf("claims update: %v", err)
	}
	if strings.Contains(string(msg.Data), `"error"`) {
		e.t.Fatalf("claims update refused: %s", msg.Data)
	}
}

// observer is an account-key connection that sees every subject, and publishes for the subscribe probes.
func (e *liveEnv) observer() (*conn, *nats.Subscription) {
	e.t.Helper()
	admin, err := e.connect(e.acc, "admin", nil, false)
	if err != nil {
		e.t.Fatal(err)
	}
	e.t.Cleanup(admin.nc.Close)
	obs := must(admin.nc.SubscribeSync(">"))
	if err := admin.nc.Flush(); err != nil {
		e.t.Fatal(err)
	}
	return admin, obs
}

// user connects the sender u with the creds of the compile c: its tags, signed by its owner group's key.
func (e *liveEnv) user(c *Compiled, sender string) (*conn, User, error) {
	for _, u := range c.Users {
		if u.Sender == sender {
			a, _ := parseAddress(u.Sender)
			sc, err := e.connect(e.scoped[a.group()], u.Name, u.Tags, true)
			if err == nil {
				e.t.Cleanup(sc.nc.Close)
			}
			return sc, u, err
		}
	}
	e.t.Fatalf("no sender %s in the compile", sender)
	return nil, User{}, nil
}

// subscribeReaches subscribes c to filter, has the observer publish to subject, and reports delivery; otherwise the
// server's violation error naming filter must confirm the refusal.
func subscribeReaches(t *testing.T, c *conn, obs *nats.Conn, filter, subject string) bool {
	t.Helper()
	sub, err := c.nc.SubscribeSync(filter)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = sub.Unsubscribe() }()
	if err := c.nc.Flush(); err != nil {
		t.Fatal(err)
	}
	if err := obs.Publish(subject, []byte("x")); err != nil {
		t.Fatal(err)
	}
	if err := obs.Flush(); err != nil {
		t.Fatal(err)
	}
	if _, err := sub.NextMsg(150 * time.Millisecond); err == nil {
		return true
	}
	if !c.violation("Subscription", filter, 2*time.Second) {
		t.Errorf("subscribe to %s: neither delivered nor refused", filter)
	}
	return false
}

func flat(addr string) string { return strings.ReplaceAll(addr, ".", "_") }

// ownSubjects is what every sender keeps whatever its lists say: the fixed lines of the template for its own address.
// Each is published (or subscribed) and observed.
func assertOwnSubjectsReach(t *testing.T, c *conn, admin *conn, obs *nats.Subscription, self string) {
	t.Helper()
	f := flat(self)
	for _, s := range []string{
		subj(self, Heartbeat),
		"_INBOX." + f + ".reply",
		"$JS.API.STREAM.INFO.inbox-" + f,
		"$JS.API.CONSUMER.MSG.NEXT.inbox-" + f + ".deliver",
		"$SRV.INFO.agents",
	} {
		if !canPublish(t, c, obs, s) {
			t.Errorf("%s cannot publish its own fixed subject %s", self, s)
		}
	}
	for _, v := range []Verb{Prompt, Status} {
		if !subscribeReaches(t, c, admin.nc, subj(self, v), subj(self, v)) {
			t.Errorf("%s does not receive its own %s subject", self, v)
		}
	}
	if !subscribeReaches(t, c, admin.nc, "_INBOX."+f+".>", "_INBOX."+f+".answer") {
		t.Errorf("%s does not receive on its own reply prefix", self)
	}
}

// assertDenyAll: sender publishes mail, prompt and status to every address of the set (itself, a receiver in its owner
// group, one in another owner group), to addresses nobody lists, and to wildcard-shaped subjects; every one is refused.
// It cannot watch any other address's heartbeat nor publish one. Its own fixed subjects still reach.
func assertDenyAll(t *testing.T, e *liveEnv, c *Compiled, self string, named []string) {
	t.Helper()
	sc, _, err := e.user(c, self)
	if err != nil {
		t.Fatalf("%s cannot connect: %v", self, err)
	}
	admin, obs := e.observer()

	targets := append([]string{}, named...)
	targets = append(targets, "foo.c.ghost", "foo.x.ghost", "bar.c.lead")
	var refused []string
	for _, v := range []Verb{Mail, Prompt, Status} {
		for _, a := range targets {
			refused = append(refused, subj(a, v))
		}
	}
	for _, v := range []Verb{Mail, Prompt, Status} {
		refused = append(refused, subj("foo.c.*", v), subj("foo.*.*", v), "agents."+v.subject()+".>", "agents."+v.subject()+".foo.>")
	}
	for _, a := range targets {
		if a != self {
			refused = append(refused, subj(a, Heartbeat))
		}
	}
	for _, s := range refused {
		if canPublish(t, sc, obs, s) {
			t.Errorf("%s published %s: a deny-all sender must be refused", self, s)
		}
	}
	for _, a := range targets {
		if a != self && subscribeReaches(t, sc, admin.nc, subj(a, Heartbeat), subj(a, Heartbeat)) {
			t.Errorf("%s watches the heartbeat of %s", self, a)
		}
	}
	assertOwnSubjectsReach(t, sc, admin, obs, self)
}

func denyAllSet(rng string, tagged bool) ListSet {
	ls := ListSet{
		Range: rng,
		Switches: concat(
			switches("foo.c", Closed, Closed, Closed, Closed),
			switches("foo.d", Closed, Closed, Closed, Closed)),
		Senders: []SenderLists{{Sender: "foo.c.core"}, {Sender: "foo.c.lead"}, {Sender: "foo.d.solo"}},
	}
	if tagged {
		ls.Senders[0].Allow = []string{"foo.c.lead"}
	}
	return ls
}

// TestAT005DenyAllSenderIsRefused is AT-005 (REQ-020): a sender with no allow tag under a group with mail, prompt and
// status closed, and a sender whose tags were all removed by a new list set, are refused everywhere outside their own
// fixed subjects, and still reach those.
func TestAT005DenyAllSenderIsRefused(t *testing.T) {
	if testing.Short() {
		t.Skip("live nats-server test")
	}
	named := []string{"foo.c.core", "foo.c.lead", "foo.d.solo"}

	t.Run("no allow entry anywhere", func(t *testing.T) {
		t.Parallel()
		ls := denyAllSet("foo", false)
		c, err := Compile(ls)
		if err != nil {
			t.Fatal(err)
		}
		for _, u := range c.Users {
			if len(u.Tags) != 0 {
				t.Fatalf("%s carries tags %v", u.Sender, u.Tags)
			}
		}
		env := newLiveEnv(t, c)
		for _, self := range named {
			assertDenyAll(t, env, c, self, named)
		}
	})

	t.Run("tags removed by a new list set", func(t *testing.T) {
		t.Parallel()
		first := denyAllSet("foo", true)
		c1, err := Compile(first)
		if err != nil {
			t.Fatal(err)
		}
		env := newPushEnv(t, c1)

		// Before: core holds an allow tag, and its mail to lead passes (so the later refusal is the tag's absence).
		sc, u, err := env.user(c1, "foo.c.core")
		if err != nil {
			t.Fatal(err)
		}
		if !slices.Equal(u.Tags, []string{"allow:c.lead"}) {
			t.Fatalf("first set: core tags %v", u.Tags)
		}
		_, obs := env.observer()
		if !canPublish(t, sc, obs, subj("foo.c.lead", Mail)) {
			t.Fatal("first set: core cannot mail lead")
		}
		if canPublish(t, sc, obs, subj("foo.d.solo", Mail)) {
			t.Fatal("first set: core mailed solo")
		}

		// After: no allow entry in the set; its tags are gone; the template is pushed and the sender connects afresh.
		c2, err := Compile(denyAllSet("foo", false))
		if err != nil {
			t.Fatal(err)
		}
		env.push(c2)
		for _, self := range named {
			assertDenyAll(t, env.liveEnv, c2, self, named)
		}
	})
}

// TestAT003OwnerLineDroppedAndDenyHolds is the second half of AT-003 (REQ-020; the existing 'deny key (AT-003)' case
// of TestLiveMatchesCheck is the first): the owner line is dropped when mail is closed, and sharing restores it; then
// with mail shared, foo.c.core denying foo.c.sdk and every user under the key carrying a deny tag, core's mail to lead
// passes and to sdk is refused.
func TestAT003OwnerLineDroppedAndDenyHolds(t *testing.T) {
	if testing.Short() {
		t.Skip("live nats-server test")
	}
	set := func(mail Switch, coreDeny ...string) ListSet {
		return ListSet{
			Range:    "foo",
			Switches: switches("foo.c", mail, Closed, Closed, Closed),
			Senders: []SenderLists{
				{Sender: "foo.c.core", Deny: coreDeny},
				{Sender: "foo.c.lead"},
				{Sender: "foo.c.sdk"},
			},
		}
	}
	shared, closed, denied := set(Shared), set(Closed), set(Shared, "foo.c.sdk")
	cs := map[string]*Compiled{}
	for name, ls := range map[string]ListSet{"shared": shared, "closed": closed, "denied": denied} {
		c, err := Compile(ls)
		if err != nil {
			t.Fatal(err)
		}
		cs[name] = c
	}
	// The compile adds the sentinel deny tag to every user of the deny key that denies nobody.
	for _, u := range cs["denied"].Users {
		want := []string{"deny:c.sdk"}
		if u.Sender != "foo.c.core" {
			want = []string{NoDenyTag}
		}
		if !slices.Equal(u.Tags, want) {
			t.Fatalf("denied set: %s carries %v, want %v", u.Sender, u.Tags, want)
		}
	}

	env := newPushEnv(t, cs["shared"])
	// reach reports, for each mail target, whether core's mail passes, with a connection opened after the last push.
	reach := func(phase string, c *Compiled, want map[string]bool) {
		t.Helper()
		sc, _, err := env.user(c, "foo.c.core")
		if err != nil {
			t.Fatalf("%s: core cannot connect: %v", phase, err)
		}
		_, obs := env.observer()
		for _, target := range []string{"foo.c.lead", "foo.c.sdk"} {
			if got := canPublish(t, sc, obs, subj(target, Mail)); got != want[target] {
				t.Errorf("%s: core mails %s: %v, want %v", phase, target, got, want[target])
			}
		}
	}
	reach("mail shared", cs["shared"], map[string]bool{"foo.c.lead": true, "foo.c.sdk": true})

	env.push(cs["closed"])
	reach("mail closed, no entry", cs["closed"], map[string]bool{"foo.c.lead": false, "foo.c.sdk": false})

	env.push(cs["shared"])
	reach("mail shared again", cs["shared"], map[string]bool{"foo.c.lead": true, "foo.c.sdk": true})

	env.push(cs["denied"])
	reach("shared, core denies sdk", cs["denied"], map[string]bool{"foo.c.lead": true, "foo.c.sdk": false})
	// The deny is core's alone: lead still mails sdk.
	lead, _, err := env.user(cs["denied"], "foo.c.lead")
	if err != nil {
		t.Fatal(err)
	}
	_, obs := env.observer()
	if !canPublish(t, lead, obs, subj("foo.c.sdk", Mail)) {
		t.Error("shared, core denies sdk: lead cannot mail sdk")
	}
}

// openRow is one subject a raw client publishes on, with what the server did with it.
type openRow struct {
	subject string
	// permitted: the server accepted the publish (the observer saw it); false: refused with a permissions violation.
	permitted bool
	// reach: the addresses, other than the sender, whose own connection received it. Peers are foo.c.lead and
	// foo.d.solo, each subscribed to what its own template allows (its reply prefix, $SRV.>).
	reach []string
	why   string
}

// openSubjects is the pinned result of AT-020's open-subject half, for the raw client of foo.c.core: every fixed line
// of the template that is not the sender's own policy, and the neighbouring subjects it must not reach. The table in
// docs/security/threat-model.md section 5.3.1 is this table; change one and change the other. A widening or a
// narrowing fails the test, and the maintainer who changes the table updates the document with it.
func openSubjects() []openRow {
	const doc = "ADR 0007, 0009"
	return []openRow{
		{"_INBOX.foo_c_lead.nuid1", true, []string{"foo.c.lead"}, "replies may go to any caller's reply prefix: no response permission, an answer is a plain publish (ADR 0007); the receiver accepts only verified callers"},
		{"_INBOX.foo_d_solo.nuid1", true, []string{"foo.d.solo"}, "the same in another owner group: the reply prefix is not part of an owner line"},
		{"$SRV.INFO.agents", true, []string{"foo.c.lead", "foo.d.solo"}, "service discovery and live-prompt requests go to every endpoint (ADR 0007)"},
		{"$SRV.PING", true, []string{"foo.c.lead", "foo.d.solo"}, "the same: every sender subscribes to $SRV.> to answer discovery"},
		{"_INBOX.foo_c_core.nuid1", true, nil, "its own reply prefix: no other address receives it"},
		{subj("foo.c.core", Heartbeat), true, nil, "its own heartbeat, a fixed line; nobody here subscribed to it"},
		{"$JS.API.INFO", true, nil, "JetStream account info: a server API call, reaches no address"},
		{"$JS.API.STREAM.INFO.KV_mash-directory", true, nil, "reads the directory: it is public to the range by design (" + doc + ")"},
		{"$JS.API.DIRECT.GET.KV_mash-directory.$KV.mash-directory.foo_c_lead", true, nil, "reads another address's directory record: public to the range, read only"},
		{"$JS.API.STREAM.INFO.KV_mash-leases", true, nil, "reads the lease bucket's state"},
		{"$KV.mash-leases.foo_c_core", true, nil, "its own lease key"},
		{"$JS.API.CONSUMER.MSG.NEXT.inbox-foo_c_core.deliver", true, nil, "its own inbox consumer"},
		// Refused: nothing is delivered.
		{"$KV.mash-leases.foo_c_lead", false, nil, "another address's lease: refused"},
		{"$JS.API.DIRECT.GET.KV_mash-leases.$KV.mash-leases.foo_c_lead", false, nil, "reading another address's lease: refused"},
		{"$KV.mash-directory.foo_c_core", false, nil, "writing the directory: refused to every sender (admin only)"},
		{"$JS.API.STREAM.INFO.inbox-foo_c_lead", false, nil, "another inbox stream: refused"},
		{"$JS.API.CONSUMER.INFO.inbox-foo_c_lead.deliver", false, nil, "another inbox consumer's state: refused"},
		{"$JS.API.CONSUMER.CREATE.inbox-foo_c_lead", false, nil, "creating a consumer on another inbox: refused"},
		{"$JS.API.CONSUMER.DURABLE.CREATE.inbox-foo_c_lead.deliver", false, nil, "creating a durable consumer on another inbox: refused"},
		{"$JS.API.CONSUMER.MSG.NEXT.inbox-foo_c_lead.deliver", false, nil, "fetching from another inbox: refused"},
		{"$JS.ACK.inbox-foo_c_lead.deliver.1.1.1.1.1", false, nil, "acknowledging another inbox's message: refused"},
		{"$JS.API.STREAM.MSG.GET.inbox-foo_c_lead", false, nil, "reading a stored message of another inbox: refused"},
		{"$JS.API.DIRECT.GET.inbox-foo_c_lead", false, nil, "direct read of another inbox: refused"},
		{"$JS.API.DIRECT.GET.inbox-foo_d_solo.x", false, nil, "direct read of an inbox in another owner group: refused"},
		{"$JS.API.STREAM.DELETE.inbox-foo_c_lead", false, nil, "deleting another inbox: refused"},
		{"$JS.API.STREAM.PURGE.inbox-foo_c_lead", false, nil, "purging another inbox: refused"},
		{subj("foo.c.lead", Heartbeat), false, nil, "another address's heartbeat: refused"},
	}
}

// TestAT020OpenReplyAndPlumbingSubjectsArePinned records what a raw client (the creds file of foo.c.core and a plain
// NATS client) reaches on the always-open reply and plumbing subjects, and pins it to openSubjects (documented in
// docs/security/threat-model.md section 5.3.1). It also asserts the contract's positive half: another address's inbox,
// prompt and status subjects cannot be subscribed, and its inbox stream cannot be consumed (the rows above).
func TestAT020OpenReplyAndPlumbingSubjectsArePinned(t *testing.T) {
	if testing.Short() {
		t.Skip("live nats-server test")
	}
	ls := denyAllSet("foo", false)
	c, err := Compile(ls)
	if err != nil {
		t.Fatal(err)
	}
	env := newLiveEnv(t, c)
	admin, obs := env.observer()
	core, _, err := env.user(c, "foo.c.core")
	if err != nil {
		t.Fatal(err)
	}

	// Peers listen where their own templates allow them to: their reply prefix and $SRV.>.
	var mu sync.Mutex
	got := map[string][]string{} // subject -> peers that received it
	for _, p := range []string{"foo.c.lead", "foo.d.solo"} {
		pc, _, err := env.user(c, p)
		if err != nil {
			t.Fatal(err)
		}
		for _, filter := range []string{"_INBOX." + flat(p) + ".>", "$SRV.>"} {
			if _, err := pc.nc.Subscribe(filter, func(m *nats.Msg) {
				mu.Lock()
				got[m.Subject] = append(got[m.Subject], p)
				mu.Unlock()
			}); err != nil {
				t.Fatal(err)
			}
		}
		if err := pc.nc.Flush(); err != nil {
			t.Fatal(err)
		}
	}

	for _, r := range openSubjects() {
		if canPublish(t, core, obs, r.subject) != r.permitted {
			t.Errorf("%s: permitted=%v, pinned %v (%s)", r.subject, !r.permitted, r.permitted, r.why)
		}
		time.Sleep(120 * time.Millisecond)
		mu.Lock()
		reached := slices.Clone(got[r.subject])
		mu.Unlock()
		slices.Sort(reached)
		if want := slices.Clone(r.reach); !slices.Equal(reached, want) && !(len(reached) == 0 && len(want) == 0) {
			t.Errorf("%s: delivered to %v, pinned %v (%s)", r.subject, reached, want, r.why)
		}
	}

	// Subscriptions: no other address's inbox, prompt, status or mail subject, nor a reply prefix but its own.
	for _, peer := range []string{"foo.c.lead", "foo.d.solo"} {
		f := flat(peer)
		for _, filter := range []string{
			subj(peer, Mail), subj(peer, Prompt), subj(peer, Status),
			"_INBOX." + f + ".>", "$JS.API.CONSUMER.MSG.NEXT.inbox-" + f + ".deliver",
		} {
			publish := strings.TrimSuffix(filter, ">") + "x"
			if !strings.HasSuffix(filter, ">") {
				publish = filter
			}
			if subscribeReaches(t, core, admin.nc, filter, publish) {
				t.Errorf("core subscribed to %s", filter)
			}
		}
	}
	for _, filter := range []string{"_INBOX.>", "agents.>", "agents.prompt.>", "agents.status.>", "agents.mail.>", "$JS.API.>"} {
		if subscribeReaches(t, core, admin.nc, filter, strings.TrimSuffix(filter, ">")+"x") {
			t.Errorf("core subscribed to %s", filter)
		}
	}
}
