package admin_test

import (
	"context"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/nats-io/jwt/v2"
	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"

	"github.com/bogworks/mash/internal/admin"
	"github.com/bogworks/mash/internal/sap"
	"github.com/bogworks/mash/internal/store"
	"github.com/bogworks/mash/internal/testenv"
)

func TestValidateGrant(t *testing.T) {
	for _, g := range []string{
		"agents.mail.bogworks.x.>", "agents.mail.bogworks.x.*", "agents.mail.bogworks.>", "agents.mail.bogworks.x.ops1",
	} {
		if err := admin.ValidateGrant(g); err != nil {
			t.Errorf("%q should be accepted: %v", g, err)
		}
	}
	for _, g := range []string{
		"agents.mail.*.x.*", "agents.mail.*.x.>", "agents.mail.>", "agents.*.*.x.*", "agents.>", ">",
		"agents.mail.bog*.x.y", "agents.mail.bogworks.>.y", "agents.mail. bogworks.x.y", "agents.mail..x.y",
		"agents.mail.bogworks.x.y.", "", "agents.*.bogworks.x.y", "*.mail.bogworks.x.y", "agents.mail.bogworks",
	} {
		err := admin.ValidateGrant(g)
		if err == nil || !strings.Contains(err.Error(), "REQ-027") {
			t.Errorf("%q should be refused naming REQ-027, got %v", g, err)
		}
	}
}

func newID(t *testing.T, agent, owner, name string) sap.EndpointIdentity {
	t.Helper()
	id, err := sap.New(agent, owner, name)
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func legID(t *testing.T, owner, name string) sap.EndpointIdentity {
	t.Helper()
	id, err := sap.Legacy(owner, name)
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func sorted(l jwt.StringList) []string { return slices.Sorted(slices.Values([]string(l))) }

func wantLines(t *testing.T, what string, got jwt.StringList, want []string) {
	t.Helper()
	w := slices.Sorted(slices.Values(want))
	if g := sorted(got); !slices.Equal(g, w) {
		t.Errorf("%s:\n got  %q\n want %q", what, g, w)
	}
}

func TestNewFormPermissionsGolden(t *testing.T) {
	id := newID(t, "acme", "team1", "ops1")
	p, err := admin.NewFormPermissions(id, nil)
	if err != nil {
		t.Fatal(err)
	}
	wantLines(t, "pub", p.Pub.Allow, []string{
		"agents.mail.acme.team1.*",
		"agents.prompt.acme.team1.*",
		"agents.status.acme.team1.*",
		"agents.hb.acme.team1.ops1",
		"_INBOX.>",
		"$SRV.>",
		"$JS.API.INFO",
		"$JS.API.STREAM.INFO.inbox-acme_team1_ops1",
		"$JS.API.CONSUMER.INFO.inbox-acme_team1_ops1.deliver",
		"$JS.API.CONSUMER.MSG.NEXT.inbox-acme_team1_ops1.deliver",
		"$JS.ACK.inbox-acme_team1_ops1.deliver.>",
		"$JS.API.DIRECT.GET.inbox-acme_team1_ops1",
		"$JS.API.DIRECT.GET.inbox-acme_team1_ops1.>",
		"$JS.API.STREAM.INFO.KV_mash-directory",
		"$JS.API.DIRECT.GET.KV_mash-directory.>",
		"$JS.API.STREAM.INFO.KV_mash-leases",
		"$KV.mash-leases.acme_team1_ops1",
		"$JS.API.DIRECT.GET.KV_mash-leases.$KV.mash-leases.acme_team1_ops1",
	})
	wantLines(t, "sub", p.Sub.Allow, []string{
		"_INBOX.acme_team1_ops1.>",
		"agents.prompt.acme.team1.ops1",
		"agents.status.acme.team1.ops1",
		"$SRV.>",
		"agents.hb.acme.team1.*",
	})
	if len(p.Pub.Deny) != 0 || len(p.Sub.Deny) != 0 || p.Resp != nil {
		t.Errorf("unexpected deny or response permission: %+v", p)
	}
}

func TestNewFormPermissionsRefusals(t *testing.T) {
	if _, err := admin.NewFormPermissions(legID(t, "acme", "ops1"), nil); err == nil {
		t.Error("legacy id accepted")
	}
	if _, err := admin.NewFormPermissions(sap.EndpointIdentity{}, nil); err == nil {
		t.Error("zero id accepted")
	}
	bad := sap.EndpointIdentity{Agent: "ACME", Owner: "team1", Name: "ops1"}
	if _, err := admin.NewFormPermissions(bad, nil); err == nil {
		t.Error("invalid id accepted")
	}
}

func TestNewFormPermissionsExtra(t *testing.T) {
	id := newID(t, "acme", "team1", "ops1")
	p, err := admin.NewFormPermissions(id, []string{"agents.mail.acme.team2.>", "agents.mail.acme.team3.*"})
	if err != nil {
		t.Fatal(err)
	}
	for _, g := range []string{"agents.mail.acme.team2.>", "agents.mail.acme.team3.*"} {
		if !slices.Contains([]string(p.Pub.Allow), g) {
			t.Errorf("extra %q missing from pub", g)
		}
		if slices.Contains([]string(p.Sub.Allow), g) {
			t.Errorf("extra %q must not be a subscribe grant", g)
		}
	}
	for _, bad := range [][]string{
		{"agents.mail.*.team2.*"},
		{"agents.mail.acme.team2.x", "agents.>"},
		{"$JS.API.>"}, {"other.subject.a.b"}, {">"},
	} {
		if _, err := admin.NewFormPermissions(id, bad); err == nil {
			t.Errorf("extra %q accepted", bad)
		}
	}
	_, err = admin.NewFormPermissions(id, []string{"agents.mail.*.x.*"})
	if err == nil || !strings.Contains(err.Error(), "REQ-027") {
		t.Errorf("want REQ-027 error, got %v", err)
	}
}

// An extra grant must not carry a heartbeat (or any verb but mail, prompt, status): a heartbeat is granted only on
// the endpoint's own exact subject, so a user cannot publish another endpoint's liveness.
func TestNewFormPermissionsExtraVerbs(t *testing.T) {
	id := newID(t, "acme", "team1", "ops1")
	for _, bad := range []string{
		"agents.hb.acme.team1.*", "agents.hb.acme.team1.ops1", "agents.hb.cc.x.y", "agents.foo.acme.team1.*",
	} {
		_, err := admin.NewFormPermissions(id, []string{bad})
		if err == nil {
			t.Errorf("extra %q accepted", bad)
			continue
		}
		if !strings.Contains(err.Error(), "heartbeat publication is granted only on the endpoint's own exact subjects") {
			t.Errorf("extra %q: error %q does not explain the heartbeat rule", bad, err)
		}
	}
	for _, ok := range []string{"agents.mail.acme.team1.*", "agents.mail.acme.>", "agents.prompt.acme.team1.peer", "agents.status.acme.team1.peer"} {
		if _, err := admin.NewFormPermissions(id, []string{ok}); err != nil {
			t.Errorf("extra %q refused: %v", ok, err)
		}
	}
}

func TestWindowBridge(t *testing.T) {
	id := newID(t, "acme", "team1", "ops1")
	alias := legID(t, "legowner", "ops1")
	p, err := admin.WindowBridge(id, alias)
	if err != nil {
		t.Fatal(err)
	}
	wantLines(t, "pub", p.Pub.Allow, []string{
		"agents.mail.cc.legowner.*",
		"agents.prompt.cc.legowner.*",
		"agents.status.cc.legowner.*",
		"agents.hb.cc.legowner.ops1",
	})
	wantLines(t, "sub", p.Sub.Allow, []string{
		"agents.prompt.cc.legowner.ops1",
		"agents.status.cc.legowner.ops1",
	})

	if _, err := admin.WindowBridge(id, legID(t, "team1", "ops1")); err == nil || !strings.Contains(err.Error(), "owner") {
		t.Errorf("same owner: %v", err)
	}
	if _, err := admin.WindowBridge(legID(t, "a", "b"), alias); err == nil {
		t.Error("legacy id accepted")
	}
	if _, err := admin.WindowBridge(id, newID(t, "acme", "team2", "ops1")); err == nil {
		t.Error("new-form alias accepted")
	}
	if _, err := admin.WindowBridge(sap.EndpointIdentity{}, alias); err == nil {
		t.Error("zero id accepted")
	}
}

func TestRefuseCompiledForNewForm(t *testing.T) {
	if err := admin.RefuseCompiledForNewForm(false); err != nil {
		t.Fatal(err)
	}
	err := admin.RefuseCompiledForNewForm(true)
	if err == nil || !strings.Contains(err.Error(), "three-token identity cannot take a policy-compiled user until the zone ruling") {
		t.Fatalf("got %v", err)
	}
}

// probe is a client whose async permission errors are recorded.
type probe struct {
	nc   *nats.Conn
	mu   sync.Mutex
	errs []string
}

func (p *probe) permErrors() []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return slices.Clone(p.errs)
}

func connectProbe(t *testing.T, e *testenv.Env, creds string) *probe {
	t.Helper()
	p := &probe{}
	nc := e.Conn(t, creds)
	nc.SetErrorHandler(func(_ *nats.Conn, _ *nats.Subscription, err error) {
		p.mu.Lock()
		p.errs = append(p.errs, err.Error())
		p.mu.Unlock()
	})
	p.nc = nc
	return p
}

func waitPermErr(t *testing.T, p *probe, n int) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if len(p.permErrors()) >= n {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("want %d permission errors, got %q", n, p.permErrors())
}

// observe subscribes with an unrestricted watcher on all agents subjects.
func observe(t *testing.T, e *testenv.Env) chan string {
	t.Helper()
	var perm jwt.Permissions
	perm.Sub.Allow.Add("agents.>")
	creds, _ := e.MintRaw(t, "watcher", perm)
	nc := e.Conn(t, creds)
	ch := make(chan string, 64)
	if _, err := nc.Subscribe("agents.>", func(m *nats.Msg) { ch <- m.Subject }); err != nil {
		t.Fatal(err)
	}
	if err := nc.Flush(); err != nil {
		t.Fatal(err)
	}
	return ch
}

func delivered(ch chan string, subject string) bool {
	timeout := time.After(500 * time.Millisecond)
	for {
		select {
		case s := <-ch:
			if s == subject {
				return true
			}
		case <-timeout:
			return false
		}
	}
}

func publishExpect(t *testing.T, p *probe, ch chan string, subject string, allowed bool) {
	t.Helper()
	before := len(p.permErrors())
	if err := p.nc.Publish(subject, []byte("x")); err != nil {
		t.Fatal(err)
	}
	if err := p.nc.Flush(); err != nil {
		t.Fatal(err)
	}
	got := delivered(ch, subject)
	if got != allowed {
		t.Errorf("publish %s: delivered=%v, want %v", subject, got, allowed)
	}
	if !allowed {
		waitPermErr(t, p, before+1)
		last := p.permErrors()[before]
		if !strings.Contains(strings.ToLower(last), "permissions violation") || !strings.Contains(last, subject) {
			t.Errorf("publish %s: error %q", subject, last)
		}
	}
}

func TestNewFormPermissionsOnServer(t *testing.T) {
	e := testenv.Start(t)
	id := newID(t, "acme", "team1", "ops1")
	perms, err := admin.NewFormPermissions(id, nil)
	if err != nil {
		t.Fatal(err)
	}
	creds, _ := e.MintRaw(t, "ops1", perms)
	ch := observe(t, e)
	p := connectProbe(t, e, creds)

	publishExpect(t, p, ch, "agents.hb.acme.team1.ops1", true)
	publishExpect(t, p, ch, "agents.mail.acme.team1.peer", true)
	publishExpect(t, p, ch, "agents.prompt.acme.team1.peer", true)
	publishExpect(t, p, ch, "agents.status.acme.team1.peer", true)
	// Same owner, different agent token: the agent position is literal.
	publishExpect(t, p, ch, "agents.mail.other.team1.x", false)
	// Different owner.
	publishExpect(t, p, ch, "agents.mail.acme.team2.ops1", false)
	// Another endpoint's heartbeat, and a legacy-form (cc) subject.
	publishExpect(t, p, ch, "agents.hb.acme.team1.other", false)
	publishExpect(t, p, ch, "agents.mail.cc.team1.x", false)

	if _, err := p.nc.Subscribe("agents.prompt.acme.team1.ops1", func(*nats.Msg) {}); err != nil {
		t.Fatal(err)
	}
	if _, err := p.nc.Subscribe("agents.hb.acme.team1.peer", func(*nats.Msg) {}); err != nil {
		t.Fatal(err)
	}
	if err := p.nc.Flush(); err != nil {
		t.Fatal(err)
	}
	if n := len(p.permErrors()); n != 4 { // the four refused publishes above, and nothing from these subscriptions
		t.Fatalf("own-subject subscriptions produced errors: %q", p.permErrors())
	}
	n := 4
	for _, s := range []string{"agents.prompt.acme.team2.ops1", "agents.prompt.acme.team1.peer", "agents.mail.acme.team1.ops1", "agents.hb.acme.team2.x"} {
		if _, err := p.nc.Subscribe(s, func(*nats.Msg) {}); err != nil {
			t.Fatal(err)
		}
		_ = p.nc.Flush()
		n++
		waitPermErr(t, p, n)
		if last := p.permErrors()[n-1]; !strings.Contains(strings.ToLower(last), "permissions violation") || !strings.Contains(last, s) {
			t.Errorf("subscribe %s: error %q", s, last)
		}
	}
}

// A user with a valid extra grant still cannot publish another endpoint's heartbeat.
func TestNewFormExtraCannotPublishOtherHeartbeat(t *testing.T) {
	e := testenv.Start(t)
	id := newID(t, "acme", "team1", "ops1")
	perms, err := admin.NewFormPermissions(id, []string{"agents.mail.acme.team2.>"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := admin.NewFormPermissions(id, []string{"agents.hb.acme.team1.*"}); err == nil {
		t.Fatal("heartbeat extra accepted")
	}
	creds, _ := e.MintRaw(t, "ops1", perms)
	ch := observe(t, e)
	p := connectProbe(t, e, creds)
	publishExpect(t, p, ch, "agents.mail.acme.team2.x", true)
	publishExpect(t, p, ch, "agents.hb.acme.team1.ops1", true)
	publishExpect(t, p, ch, "agents.hb.acme.team1.other", false)
	publishExpect(t, p, ch, "agents.hb.acme.team2.x", false)
	publishExpect(t, p, ch, "agents.hb.cc.legowner.ops1", false)
}

// denyRequest sends a request the allow list does not cover: no answer comes back and the server reports a
// permissions violation naming the subject.
func denyRequest(t *testing.T, p *probe, subj, body string) {
	t.Helper()
	before := len(p.permErrors())
	if m, err := p.nc.Request(subj, []byte(body), 400*time.Millisecond); err == nil {
		t.Errorf("%s: answered (%s)", subj, m.Data)
	}
	waitPermErr(t, p, before+1)
	if last := p.permErrors()[before]; !strings.Contains(strings.ToLower(last), "permissions violation") || !strings.Contains(last, subj) {
		t.Errorf("%s: error %q", subj, last)
	}
}

// The JetStream and KV denials the new-form allow list implies, seen with the new-form user's own credentials, and
// the positives it keeps: its own stream, its deliver consumer and its own lease key.
func TestNewFormJetStreamAndKVOnServer(t *testing.T) {
	e := testenv.Start(t)
	ctx := context.Background()
	anc := e.Conn(t, e.AdminCreds)
	a, err := admin.New(anc, e.Owner)
	if err != nil {
		t.Fatal(err)
	}
	if err := a.Init(ctx); err != nil {
		t.Fatal(err)
	}
	js, err := jetstream.New(anc)
	if err != nil {
		t.Fatal(err)
	}
	id := newID(t, "acme", "team1", "ops1")
	other := newID(t, "other", "team1", "x")
	own, oth := sap.InboxStream(id.Flat()), sap.InboxStream(other.Flat())
	mk := func(stream, subject string) {
		cfg := store.InboxConfig(e.Owner, "x")
		cfg.Name, cfg.Subjects = stream, []string{subject}
		s, err := js.CreateStream(ctx, cfg)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := s.CreateOrUpdateConsumer(ctx, store.ConsumerConfig()); err != nil {
			t.Fatal(err)
		}
		if _, err := js.Publish(ctx, subject, []byte("seed")); err != nil {
			t.Fatal(err)
		}
	}
	mk(own, id.Subjects().Mail)
	mk(oth, other.Subjects().Mail)
	mk("inbox-x", "agents.mail.cc."+e.Owner+".x")

	perms, err := admin.NewFormPermissions(id, nil)
	if err != nil {
		t.Fatal(err)
	}
	creds, _ := e.MintRaw(t, "ops1", perms)
	p := &probe{}
	p.nc = e.ConnAs(t, creds, id.Flat())
	p.nc.SetErrorHandler(func(_ *nats.Conn, _ *nats.Subscription, err error) {
		p.mu.Lock()
		p.errs = append(p.errs, err.Error())
		p.mu.Unlock()
	})

	// Positives: its own stream, the deliver consumer, an ack, and its own lease key.
	pjs, err := jetstream.New(p.nc)
	if err != nil {
		t.Fatal(err)
	}
	st, err := pjs.Stream(ctx, own)
	if err != nil {
		t.Fatalf("own STREAM.INFO: %v", err)
	}
	cons, err := st.Consumer(ctx, store.Consumer)
	if err != nil {
		t.Fatalf("own CONSUMER.INFO: %v", err)
	}
	batch, err := cons.Fetch(1, jetstream.FetchMaxWait(2*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	n := 0
	for m := range batch.Messages() {
		n++
		if err := m.DoubleAck(ctx); err != nil {
			t.Errorf("ack: %v", err)
		}
	}
	if n != 1 || batch.Error() != nil {
		t.Fatalf("own fetch: %d messages, err %v", n, batch.Error())
	}
	akv, err := js.KeyValue(ctx, store.LeaseBucket)
	if err != nil {
		t.Fatal(err)
	}
	pkv, err := pjs.KeyValue(ctx, store.LeaseBucket)
	if err != nil {
		t.Fatalf("lease bucket info: %v", err)
	}
	if _, err := pkv.Put(ctx, id.Flat(), []byte("held")); err != nil {
		t.Fatalf("own lease update: %v", err)
	}
	if ent, err := akv.Get(ctx, id.Flat()); err != nil || string(ent.Value()) != "held" {
		t.Fatalf("own lease not written: %v", err)
	}
	if errs := p.permErrors(); len(errs) != 0 {
		t.Fatalf("positives produced permission errors: %q", errs)
	}
	ast, err := js.Stream(ctx, own)
	if err != nil {
		t.Fatal(err)
	}
	ac, err := ast.Consumer(ctx, store.Consumer)
	if err != nil {
		t.Fatal(err)
	}
	if info, err := ac.Info(ctx); err != nil || info.AckFloor.Stream != 1 || info.NumAckPending != 0 {
		t.Fatalf("ack not recorded: %+v %v", info, err)
	}

	// Denials.
	denyRequest(t, p, "$JS.API.STREAM.INFO."+oth, "")
	denyRequest(t, p, "$JS.API.STREAM.INFO.inbox-x", "")
	denyRequest(t, p, "$JS.API.CONSUMER.INFO."+oth+".deliver", "")
	denyRequest(t, p, "$JS.API.CONSUMER.INFO.inbox-x.deliver", "")
	denyRequest(t, p, "$JS.API.CONSUMER.CREATE."+own+".evil", `{"stream_name":"`+own+`","config":{"durable_name":"evil","ack_policy":"explicit"}}`)
	denyRequest(t, p, "$JS.API.CONSUMER.DURABLE.CREATE."+own+".evil", `{"stream_name":"`+own+`","config":{"durable_name":"evil","ack_policy":"explicit"}}`)
	denyRequest(t, p, "$JS.API.STREAM.CREATE.evil", `{"name":"evil","subjects":["evil"]}`)
	denyRequest(t, p, "$JS.API.STREAM.DELETE."+own, "")
	denyRequest(t, p, "$JS.API.STREAM.DELETE."+oth, "")
	denyRequest(t, p, "$JS.API.STREAM.PURGE."+own, "{}")
	denyRequest(t, p, "$JS.API.STREAM.PURGE."+oth, "{}")
	denyRequest(t, p, "$JS.API.DIRECT.GET."+oth, `{"seq":1}`)
	denyRequest(t, p, "$JS.API.DIRECT.GET.inbox-x", `{"seq":1}`)
	otherLease := "$KV." + store.LeaseBucket + "." + other.Flat()
	before := len(p.permErrors())
	if err := p.nc.Publish(otherLease, []byte("stolen")); err != nil {
		t.Fatal(err)
	}
	_ = p.nc.Flush()
	waitPermErr(t, p, before+1)
	if last := p.permErrors()[before]; !strings.Contains(last, otherLease) {
		t.Errorf("lease publish: error %q", last)
	}

	// No effect, seen with the admin connection.
	if _, err := js.Stream(ctx, "evil"); err == nil {
		t.Error("stream evil was created")
	}
	for _, s := range []string{own, oth, "inbox-x"} {
		st, err := js.Stream(ctx, s)
		if err != nil {
			t.Fatalf("stream %s gone: %v", s, err)
		}
		if si := st.CachedInfo(); si.State.Msgs != 1 {
			t.Errorf("stream %s has %d messages, want 1", s, si.State.Msgs)
		}
	}
	if _, err := ast.Consumer(ctx, "evil"); err == nil {
		t.Errorf("consumer evil was created on %s", own)
	}
	if _, err := akv.Get(ctx, other.Flat()); err == nil {
		t.Error("another endpoint's lease key was written")
	}
}

func TestWindowBridgeOnServer(t *testing.T) {
	e := testenv.Start(t)
	id := newID(t, "acme", "team1", "ops1")
	alias := legID(t, "legowner", "ops1")
	perms, _ := admin.NewFormPermissions(id, nil)
	bridge, err := admin.WindowBridge(id, alias)
	if err != nil {
		t.Fatal(err)
	}
	perms.Pub.Allow.Add(bridge.Pub.Allow...)
	perms.Sub.Allow.Add(bridge.Sub.Allow...)
	creds, _ := e.MintRaw(t, "ops1", perms)
	ch := observe(t, e)
	p := connectProbe(t, e, creds)

	publishExpect(t, p, ch, "agents.mail.cc.legowner.lead", true)
	publishExpect(t, p, ch, "agents.prompt.cc.legowner.lead", true)
	publishExpect(t, p, ch, "agents.status.cc.legowner.lead", true)
	publishExpect(t, p, ch, "agents.hb.cc.legowner.ops1", true)
	publishExpect(t, p, ch, "agents.hb.cc.legowner.lead", false)
	publishExpect(t, p, ch, "agents.mail.cc.otherowner.lead", false)
	publishExpect(t, p, ch, "agents.mail.xx.legowner.lead", false)

	for _, s := range []string{"agents.prompt.cc.legowner.ops1", "agents.status.cc.legowner.ops1"} {
		if _, err := p.nc.Subscribe(s, func(*nats.Msg) {}); err != nil {
			t.Fatal(err)
		}
	}
	_ = p.nc.Flush()
	time.Sleep(200 * time.Millisecond)
	if errs := p.permErrors(); len(errs) != 3 { // only the three refused publishes above
		t.Fatalf("alias subscriptions refused: %q", errs)
	}
	for _, s := range []string{"agents.mail.cc.legowner.ops1", "agents.prompt.cc.legowner.lead", "agents.prompt.cc.legowner.*", "agents.hb.cc.legowner.ops1"} {
		before := len(p.permErrors())
		_, _ = p.nc.Subscribe(s, func(*nats.Msg) {})
		_ = p.nc.Flush()
		waitPermErr(t, p, before+1)
		if last := p.permErrors()[before]; !strings.Contains(last, "Subscription") || !strings.Contains(last, s) {
			t.Errorf("subscribe %s: error %q", s, last)
		}
	}
}

// A legacy user holds agents.mail.*.<owner>.*. It cannot reach a new-form subject of another owner, and can reach one
// of its own owner, which is why admission refuses that identity.
func TestLegacyUserOverlapWithNewForm(t *testing.T) {
	e := testenv.Start(t)
	ch := observe(t, e)
	creds, _ := e.Mint(t, "peer") // legacy owner e.Owner
	p := connectProbe(t, e, creds)

	other := newID(t, "acme", "team1", "ops1")
	publishExpect(t, p, ch, other.Subjects().Mail, false)
	publishExpect(t, p, ch, other.Subjects().Prompt, false)

	same := newID(t, "acme", e.Owner, "ops1")
	publishExpect(t, p, ch, same.Subjects().Mail, true)

	existing := []store.Record{{Name: "peer", Owner: e.Owner, Agent: "claude-code", AgentToken: "cc", Account: "A", User: "U",
		MailSubject: store.MailSubjectFor(e.Owner, "", "peer")}}
	if err := store.Admit(same, "", existing); err == nil {
		t.Error("Admit accepted a new-form identity whose owner equals a legacy owner")
	}
	if err := store.Admit(other, "", existing); err != nil {
		t.Errorf("Admit refused the disjoint identity: %v", err)
	}
}
