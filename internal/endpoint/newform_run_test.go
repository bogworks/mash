package endpoint

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/nats-io/nats.go/jetstream"

	"github.com/bogworks/mash/internal/admin"
	"github.com/bogworks/mash/internal/config"
	"github.com/bogworks/mash/internal/sap"
	"github.com/bogworks/mash/internal/store"
)

// newFormOpts says what addNewForm leaves out or breaks, for the fail-closed tests.
type newFormOpts struct {
	noRecord   bool   // no directory record
	badRecord  bool   // a record under the key that is not a valid new-form record
	credsOf    string // mint the user with the permissions of this other identity's flat key (a wrong-endpoint credential)
	legacyName string // mint the user as the legacy endpoint of this name (the bare-name credential)
}

// addNewForm provisions a three-token endpoint directly (there is no mashctl for it yet): the inbox stream
// inbox-<flat> filtered on the identity's own mail subject with production limits, the durable consumer, a user named
// by the flat key with the default new-form permissions, and the directory record under the flat key.
func (f *fixture) addNewForm(t *testing.T, agent, owner, name string, o newFormOpts) sap.EndpointIdentity {
	t.Helper()
	id, err := sap.New(agent, owner, name)
	if err != nil {
		t.Fatal(err)
	}
	permsFor := id
	if o.credsOf != "" {
		if permsFor, err = sap.FromFlat(o.credsOf); err != nil {
			t.Fatal(err)
		}
	}
	perms, err := admin.NewFormPermissions(permsFor, nil)
	if err != nil {
		t.Fatal(err)
	}
	if o.legacyName != "" {
		perms = admin.Permissions(f.e.Owner, o.legacyName)
	}
	creds, user := f.e.MintRaw(t, id.Flat(), perms)
	ctx := context.Background()
	js, err := jetstream.New(f.e.Conn(t, f.e.AdminCreds))
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
	if !o.noRecord {
		kv, err := js.KeyValue(ctx, store.DirectoryBucket)
		if err != nil {
			t.Fatal(err)
		}
		rec := store.Record{Name: id.Name, Owner: id.Owner, Agent: sap.AgentName, AgentToken: id.Agent,
			Account: f.e.Account, User: user, MailSubject: id.Subjects().Mail, Created: time.Now().UTC().Format(time.RFC3339)}
		if o.badRecord {
			rec.MailSubject = "agents.mail.cc.somewhere.else"
		}
		val, _ := json.Marshal(rec)
		if _, err := kv.Create(ctx, id.Flat(), val); err != nil {
			t.Fatal(err)
		}
	}
	f.creds[id.Flat()], f.users[id.Flat()] = creds, user
	return id
}

func (f *fixture) cfgNF(id sap.EndpointIdentity) config.Config {
	return config.Config{URL: f.e.URL, Owner: id.Owner, Name: id.Name, AgentToken: id.Agent, Creds: f.creds[id.Flat()],
		SettleMS: 50, BatchMsgs: 10, BatchBytes: 16384}
}

// serveNF runs the new-form endpoint id with its lease, its SAP service and its mail loop until the test ends.
func (f *fixture) serveNF(t *testing.T, id sap.EndpointIdentity, onPrompt func(*Endpoint, PromptEvent)) (*Endpoint, *events) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	ep, err := Open(ctx, f.cfgNF(id))
	if err != nil {
		cancel()
		t.Fatal(err)
	}
	if err := ep.StartService(ctx, func(p PromptEvent) { onPrompt(ep, p) }); err != nil {
		cancel()
		t.Fatal(err)
	}
	ev := &events{}
	done := make(chan struct{})
	go func() { _ = ep.Run(ctx, ev.deliver); close(done) }()
	t.Cleanup(func() { cancel(); <-done; ep.Close() })
	return ep, ev
}

func (f *fixture) clientNF(t *testing.T, id sap.EndpointIdentity) *Endpoint {
	t.Helper()
	ep, err := OpenClient(context.Background(), f.cfgNF(id))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(ep.Close)
	return ep
}

// ackFloor waits until stream's durable consumer has acknowledged everything up to seq.
func ackFloor(t *testing.T, f *fixture, stream string, seq uint64) {
	t.Helper()
	js, err := jetstream.New(f.e.Conn(t, f.e.AdminCreds))
	if err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		c, err := js.Consumer(context.Background(), stream, store.Consumer)
		if err != nil {
			t.Fatal(err)
		}
		if info, err := c.Info(context.Background()); err == nil && info.AckFloor.Stream >= seq && info.NumAckPending == 0 {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("%s: mail up to #%d was not acknowledged", stream, seq)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// (1) Two three-token endpoints of one agent and owner exchange mail both ways: delivered, [verified], from the sender's
// key, read and inbox work, acknowledged, and both show online in who.
func TestNewFormMailBothWays(t *testing.T) {
	f := newFixture(t)
	ida := f.addNewForm(t, "ag", "team", "a", newFormOpts{})
	idb := f.addNewForm(t, "ag", "team", "b", newFormOpts{})
	a, aev := f.serveNF(t, ida, noPrompt)
	b, bev := f.serveNF(t, idb, noPrompt)
	if a.Key() != "ag_team_a" || b.Key() != "ag_team_b" || a.Name() != "a" {
		t.Fatalf("keys %q %q name %q", a.Key(), b.Key(), a.Name())
	}
	ctx := context.Background()
	seq, err := b.Send(ctx, a.Key(), "hello a", 0)
	if err != nil {
		t.Fatal(err)
	}
	got := aev.waitFor(t, 1, 5*time.Second)
	if !strings.Contains(got[0], "from ag_team_b [verified]") || !strings.Contains(got[0], "hello a") {
		t.Fatalf("event: %s", got[0])
	}
	m, err := a.Read(ctx, seq)
	if err != nil || m.From != "ag_team_b" || m.Trust != sap.TrustVerified || string(m.Body) != "hello a" {
		t.Fatalf("read: %+v %v", m, err)
	}
	if ms, err := a.Inbox(ctx, 20); err != nil || len(ms) != 1 || ms[0].Seq != seq || ms[0].From != "ag_team_b" {
		t.Fatalf("inbox: %+v %v", ms, err)
	}
	ackFloor(t, f, "inbox-ag_team_a", seq)

	rseq, err := a.Send(ctx, b.Key(), "hello b", 0)
	if err != nil {
		t.Fatal(err)
	}
	got = bev.waitFor(t, 1, 5*time.Second)
	if !strings.Contains(got[0], "from ag_team_a [verified]") || !strings.Contains(got[0], "hello b") {
		t.Fatalf("event: %s", got[0])
	}
	if m, err := b.Read(ctx, rseq); err != nil || m.From != "ag_team_a" || m.Trust != sap.TrustVerified {
		t.Fatalf("read: %+v %v", m, err)
	}
	ackFloor(t, f, "inbox-ag_team_b", rseq)

	// A one-shot client (the CLI's shape) reads the same inbox and sends under the same key.
	c := f.clientNF(t, ida)
	if ms, err := c.Inbox(ctx, 20); err != nil || len(ms) != 1 || ms[0].From != "ag_team_b" || ms[0].Trust != sap.TrustVerified {
		t.Fatalf("client inbox: %+v %v", ms, err)
	}
	if _, err := c.Send(ctx, b.Key(), "from the shell", 0); err != nil {
		t.Fatal(err)
	}
	bev.waitFor(t, 2, 5*time.Second)
	if ms, _ := b.Inbox(ctx, 20); len(ms) != 2 || ms[1].From != "ag_team_a" || ms[1].Trust != sap.TrustVerified {
		t.Fatalf("shell send: %+v", ms)
	}

	// who: both are online, keyed by their flat identity.
	ps, err := a.WhoFresh(ctx)
	if err != nil {
		t.Fatal(err)
	}
	online := map[string]bool{}
	for _, p := range ps {
		online[p.Record.KeyOrName()] = p.Online
	}
	if len(ps) != 2 || !online["ag_team_a"] || !online["ag_team_b"] {
		t.Fatalf("who: %+v", online)
	}

	// The heartbeat goes out on the identity's own subject, signed as its key.
	sub, err := b.Conn().SubscribeSync("agents.hb.ag.team.a")
	if err != nil {
		t.Fatal(err)
	}
	a.publishHeartbeat()
	hm, err := sub.NextMsg(5 * time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if h, tr := sap.VerifyStored(hm.Header.Get(sap.HeaderName), hm.Subject, hm.Data); h.Name != "ag_team_a" || tr == sap.TrustForged || tr == sap.TrustMalformed {
		t.Fatalf("heartbeat header %+v %v", h, tr)
	}
}

// A recipient that is neither a legacy name nor a key is refused with a clear error, not looked up.
func TestNewFormRecipientMustBeAKey(t *testing.T) {
	f := newFixture(t)
	ida := f.addNewForm(t, "ag", "team", "a", newFormOpts{})
	a := f.clientNF(t, ida)
	_, err := a.Send(context.Background(), "ag.team.b", "hi", 0)
	if err == nil || !strings.Contains(err.Error(), "not an endpoint key") {
		t.Fatalf("send to a display form: %v", err)
	}
	if _, err := a.Send(context.Background(), "ag_team_nobody", "hi", 0); err == nil || !strings.Contains(err.Error(), "no endpoint named") {
		t.Fatalf("send to an unknown key: %v", err)
	}
}

// (2) A three-token endpoint and a legacy one with the same name token (and a different owner) share one directory and
// never see each other's mail.
func TestNewFormAndLegacyCoexistWithoutCrosstalk(t *testing.T) {
	f := newFixture(t, "a", "b") // legacy, owner f.e.Owner
	nfa := f.addNewForm(t, "ag", "team", "a", newFormOpts{})
	nfb := f.addNewForm(t, "ag", "team", "b", newFormOpts{})
	_, lev := f.run(t, "a")
	_, nev := f.serveNF(t, nfa, noPrompt)
	lb := f.client(t, "b")
	nb := f.clientNF(t, nfb)
	ctx := context.Background()
	if _, err := lb.Send(ctx, "a", "to legacy a", 0); err != nil {
		t.Fatal(err)
	}
	if _, err := nb.Send(ctx, "ag_team_a", "to new-form a", 0); err != nil {
		t.Fatal(err)
	}
	lg := lev.waitFor(t, 1, 5*time.Second)
	ng := nev.waitFor(t, 1, 5*time.Second)
	time.Sleep(300 * time.Millisecond)
	lg, ng = lev.all(), nev.all()
	if len(lg) != 1 || !strings.Contains(lg[0], "to legacy a") || !strings.Contains(lg[0], "from b [verified]") || strings.Contains(lg[0], "new-form") {
		t.Fatalf("legacy a got %q", lg)
	}
	if len(ng) != 1 || !strings.Contains(ng[0], "to new-form a") || !strings.Contains(ng[0], "from ag_team_b [verified]") || strings.Contains(ng[0], "legacy") {
		t.Fatalf("new-form a got %q", ng)
	}
	// Each stream holds exactly its own message.
	js, err := jetstream.New(f.e.Conn(t, f.e.AdminCreds))
	if err != nil {
		t.Fatal(err)
	}
	for stream, want := range map[string]uint64{"inbox-a": 1, "inbox-ag_team_a": 1, "inbox-b": 0, "inbox-ag_team_b": 0} {
		s, err := js.Stream(ctx, stream)
		if err != nil {
			t.Fatal(err)
		}
		if info, err := s.Info(ctx); err != nil || info.State.Msgs != want {
			t.Fatalf("%s: %+v %v", stream, info, err)
		}
	}
	// Both are in the directory, the new-form one under its flat key.
	ps := nb.Who()
	var keys []string
	for _, p := range ps {
		keys = append(keys, p.Record.KeyOrName())
	}
	if strings.Join(keys, ",") != "a,ag_team_a,ag_team_b,b" {
		t.Fatalf("directory keys %v", keys)
	}
}

// (3) A prompt round trip between two three-token endpoints.
func TestNewFormPromptRoundTrip(t *testing.T) {
	f := newFixture(t)
	ida := f.addNewForm(t, "ag", "team", "a", newFormOpts{})
	idb := f.addNewForm(t, "ag", "team", "b", newFormOpts{})
	got := make(chan PromptEvent, 1)
	f.serveNF(t, ida, func(ep *Endpoint, p PromptEvent) {
		got <- p
		_ = ep.Reply(p.RequestID, "pong ", false)
		_ = ep.Reply(p.RequestID, "from a", true)
	})
	b := f.clientNF(t, idb)
	ctx := context.Background()
	subj, err := DiscoverRecipient(ctx, b.Conn(), "team", "ag_team_a")
	if err != nil || subj != "agents.prompt.ag.team.a" {
		t.Fatalf("discover: %q %v", subj, err)
	}
	if _, err := DiscoverRecipient(ctx, b.Conn(), "team", "ag.team.a"); err == nil || !strings.Contains(err.Error(), "not an endpoint key") {
		t.Fatalf("a display form: %v", err)
	}
	var out bytes.Buffer
	if err := Prompt(ctx, b.Conn(), b.Identity(), b.Key(), subj, "ping", &out); err != nil {
		t.Fatal(err)
	}
	if out.String() != "pong from a" {
		t.Fatalf("reply %q", out.String())
	}
	if p := <-got; p.From != "ag_team_b" || p.Trust != sap.TrustVerified || p.Text != "ping" {
		t.Fatalf("event %+v", p)
	}
}

// (4) The open fails closed, with a clear error, when the endpoint's own directory record is absent or not a usable
// record, and when the credentials are for another endpoint (not for the flat key).
func TestNewFormOpenFailsClosed(t *testing.T) {
	f := newFixture(t, "a")
	noRec := f.addNewForm(t, "ag", "team", "norec", newFormOpts{noRecord: true})
	bad := f.addNewForm(t, "ag", "team", "bad", newFormOpts{badRecord: true})
	other := f.addNewForm(t, "ag", "team", "other", newFormOpts{})
	wrong := f.addNewForm(t, "ag", "team", "wrong", newFormOpts{credsOf: other.Flat()})
	bare := f.addNewForm(t, "ag", "team", "bare", newFormOpts{legacyName: "a"})
	for _, tc := range []struct {
		name string
		id   sap.EndpointIdentity
		want string
	}{
		{"no record", noRec, "is not provisioned"},
		{"unusable record", bad, "is not provisioned"},
		{"credentials of another new-form endpoint", wrong, "do not match endpoint ag_team_wrong"},
		{"credentials of the bare name", bare, "do not match endpoint ag_team_bare"},
	} {
		for opener, open := range map[string]func() error{
			"Open":       func() error { _, err := Open(context.Background(), f.cfgNF(tc.id)); return err },
			"OpenClient": func() error { _, err := OpenClient(context.Background(), f.cfgNF(tc.id)); return err },
		} {
			if err := open(); err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Errorf("%s via %s: %v (want %q)", tc.name, opener, err, tc.want)
			}
		}
	}
}

// (5) The lease is keyed by the flat identity: a second open of the same three-token identity conflicts, and a legacy
// endpoint with the same name token is not affected.
func TestNewFormLeaseConflictAndLegacyIndependence(t *testing.T) {
	f := newFixture(t, "a")
	ida := f.addNewForm(t, "ag", "team", "a", newFormOpts{})
	ctx := context.Background()
	first, err := Open(ctx, f.cfgNF(ida))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(first.Close)
	_, err = Open(ctx, f.cfgNF(ida))
	var held *LeaseHeldError
	if !errors.As(err, &held) || held.Name != "ag_team_a" {
		t.Fatalf("second open: %v", err)
	}
	legacy, err := Open(ctx, f.cfg("a"))
	if err != nil {
		t.Fatalf("legacy open with the same name token: %v", err)
	}
	t.Cleanup(legacy.Close)
	// The two leases are two keys.
	js, _ := jetstream.New(f.e.Conn(t, f.e.AdminCreds))
	kv, err := js.KeyValue(ctx, store.LeaseBucket)
	if err != nil {
		t.Fatal(err)
	}
	for _, k := range []string{"a", "ag_team_a"} {
		if _, err := kv.Get(ctx, k); err != nil {
			t.Fatalf("lease %s: %v", k, err)
		}
	}
	// Closing the new-form holder frees only its own key: the same identity can open again.
	first.Close()
	again, err := Open(ctx, f.cfgNF(ida))
	if err != nil {
		t.Fatalf("reopen after close: %v", err)
	}
	again.Close()
}
