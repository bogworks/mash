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

// addOpts says what addAt leaves out or breaks, for the fail-closed tests.
type addOpts struct {
	noRecord  bool   // no directory record
	badRecord bool   // a record under the key that is not a valid record of this release
	credsOf   string // mint the user with the permissions of this other address's flat key (a wrong-endpoint credential)
}

// addAt provisions the endpoint at (the fixture's owner, group, name) directly, so a test can leave out or break one
// part: the inbox stream inbox-<flat> filtered on its own mail subject with production limits, the durable consumer, a
// user named by the flat key with the default permissions, and the directory record under the flat key.
func (f *fixture) addAt(t *testing.T, group, name string, o addOpts) sap.Address {
	t.Helper()
	a, err := sap.NewAddress(f.e.Owner, group, name)
	if err != nil {
		t.Fatal(err)
	}
	permsFor := a
	if o.credsOf != "" {
		if permsFor, err = sap.AddressFromFlat(o.credsOf); err != nil {
			t.Fatal(err)
		}
	}
	creds, user := f.e.MintRaw(t, a.Flat(), *admin.Permissions(permsFor))
	ctx := context.Background()
	js, err := jetstream.New(f.e.Conn(t, f.e.AdminCreds))
	if err != nil {
		t.Fatal(err)
	}
	s, err := js.CreateStream(ctx, store.InboxConfigFor(a, store.DefaultLimits()))
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
		rec := store.Record{Owner: a.Owner, Group: a.Group, Instance: a.Instance, Agent: sap.AgentName,
			Account: f.e.Account, User: user, MailSubject: a.MailSubject(), Created: time.Now().UTC().Format(time.RFC3339)}
		if o.badRecord {
			rec.MailSubject = "agents.mail.somewhere.else.entirely"
		}
		val, _ := json.Marshal(rec)
		if _, err := kv.Create(ctx, a.Flat(), val); err != nil {
			t.Fatal(err)
		}
	}
	f.creds[a.Flat()], f.users[a.Flat()] = creds, user
	return a
}

func (f *fixture) cfgAt(a sap.Address) config.Config {
	// REQ-014: these tests are not about TLS; the override is explicit.
	return config.Config{URL: f.e.URL, SkipEncryption: true, Owner: a.Owner, Group: a.Group, Name: a.Instance, Creds: f.creds[a.Flat()], AcceptUnverified: true,
		SettleMS: 50, BatchMsgs: 10, BatchBytes: 16384}
}

// serveAt runs the endpoint a with its lease, its SAP service and its mail loop until the test ends.
func (f *fixture) serveAt(t *testing.T, a sap.Address, onPrompt func(*Endpoint, PromptEvent)) (*Endpoint, *events) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	ep, err := Open(ctx, f.cfgAt(a))
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

func (f *fixture) clientAt(t *testing.T, a sap.Address) *Endpoint {
	t.Helper()
	ep, err := OpenClient(context.Background(), f.cfgAt(a))
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

// (1) Two endpoints of one owner and group exchange mail both ways: delivered, [verified], from the sender's key, read
// and inbox work, acknowledged, and both show online in who.
func TestAddressMailBothWays(t *testing.T) {
	f := newFixture(t)
	ida := f.addAt(t, "team", "a", addOpts{})
	idb := f.addAt(t, "team", "b", addOpts{})
	a, aev := f.serveAt(t, ida, noPrompt)
	b, bev := f.serveAt(t, idb, noPrompt)
	if a.Key() != ida.Flat() || b.Key() != idb.Flat() || a.Name() != "a" || a.Display() != ida.String() {
		t.Fatalf("keys %q %q name %q display %q", a.Key(), b.Key(), a.Name(), a.Display())
	}
	ctx := context.Background()
	seq, err := b.Send(ctx, a.Key(), "hello a", 0)
	if err != nil {
		t.Fatal(err)
	}
	got := aev.waitFor(t, 1, 5*time.Second)
	if !strings.Contains(got[0], "from "+idb.String()+" [verified]") || !strings.Contains(got[0], "hello a") {
		t.Fatalf("event: %s", got[0])
	}
	m, err := a.Read(ctx, seq)
	if err != nil || m.From != idb.Flat() || m.Trust != sap.TrustVerified || string(m.Body) != "hello a" {
		t.Fatalf("read: %+v %v", m, err)
	}
	if ms, err := a.Inbox(ctx, 20); err != nil || len(ms) != 1 || ms[0].Seq != seq || ms[0].From != idb.Flat() {
		t.Fatalf("inbox: %+v %v", ms, err)
	}
	ackFloor(t, f, ida.InboxStream(), seq)

	rseq, err := a.Send(ctx, b.Key(), "hello b", 0)
	if err != nil {
		t.Fatal(err)
	}
	got = bev.waitFor(t, 1, 5*time.Second)
	if !strings.Contains(got[0], "from "+ida.String()+" [verified]") || !strings.Contains(got[0], "hello b") {
		t.Fatalf("event: %s", got[0])
	}
	if m, err := b.Read(ctx, rseq); err != nil || m.From != ida.Flat() || m.Trust != sap.TrustVerified {
		t.Fatalf("read: %+v %v", m, err)
	}
	ackFloor(t, f, idb.InboxStream(), rseq)

	// A one-shot client (the CLI's shape) reads the same inbox and sends under the same key.
	c := f.clientAt(t, ida)
	if ms, err := c.Inbox(ctx, 20); err != nil || len(ms) != 1 || ms[0].From != idb.Flat() || ms[0].Trust != sap.TrustVerified {
		t.Fatalf("client inbox: %+v %v", ms, err)
	}
	if _, err := c.Send(ctx, b.Key(), "from the shell", 0); err != nil {
		t.Fatal(err)
	}
	bev.waitFor(t, 2, 5*time.Second)
	if ms, _ := b.Inbox(ctx, 20); len(ms) != 2 || ms[1].From != ida.Flat() || ms[1].Trust != sap.TrustVerified {
		t.Fatalf("shell send: %+v", ms)
	}

	// who: both are online, keyed by their flat key.
	ps, err := a.WhoFresh(ctx)
	if err != nil {
		t.Fatal(err)
	}
	online := map[string]bool{}
	for _, p := range ps {
		online[flatOf(p.Record)] = p.Online
	}
	if len(ps) != 2 || !online[ida.Flat()] || !online[idb.Flat()] {
		t.Fatalf("who: %+v", online)
	}

	// The heartbeat goes out on the address's own subject, signed as its key.
	sub, err := b.Conn().SubscribeSync("agents.hb." + ida.String())
	if err != nil {
		t.Fatal(err)
	}
	// The server must know the subscription before a publishes, or the heartbeat is missed (a flake on a slow runner).
	if err := b.Conn().Flush(); err != nil {
		t.Fatal(err)
	}
	a.publishHeartbeat()
	hm, err := sub.NextMsg(5 * time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if h, tr := sap.VerifyStored(hm.Header.Get(sap.HeaderName), hm.Subject, hm.Data); h.Name != ida.Flat() || tr == sap.TrustForged || tr == sap.TrustMalformed {
		t.Fatalf("heartbeat header %+v %v", h, tr)
	}
}

// A recipient may be written dotted or as the flat key; anything else, a bare instance included, is refused with an
// error that names owner.group.instance, not looked up.
func TestRecipientMayBeDottedOrFlat(t *testing.T) {
	f := newFixture(t)
	ida := f.addAt(t, "team", "a", addOpts{})
	idb := f.addAt(t, "team", "b", addOpts{})
	a := f.clientAt(t, ida)
	if idb.Flat() != f.e.Owner+"_team_b" || idb.String() != f.e.Owner+".team.b" {
		t.Fatalf("setup %q %q", idb.Flat(), idb.String())
	}
	for _, to := range []string{idb.String(), idb.Flat()} {
		if _, err := a.Send(context.Background(), to, "hi", 0); err != nil {
			t.Fatalf("send to %q: %v", to, err)
		}
	}
	for _, bad := range []string{"b", f.e.Owner + ".team", f.e.Owner + ".team.b.c", f.e.Owner + ".team_b", "O.team.b", "team.b"} {
		_, err := a.Send(context.Background(), bad, "hi", 0)
		if err == nil || !strings.Contains(err.Error(), "is not an endpoint address") || !strings.Contains(err.Error(), "owner.group.name") {
			t.Fatalf("send to %q: %v", bad, err)
		}
	}
	if _, err := a.Send(context.Background(), f.e.Owner+"_team_nobody", "hi", 0); err == nil || !strings.Contains(err.Error(), "no endpoint named") {
		t.Fatalf("send to an unknown key: %v", err)
	}
	if _, err := a.Send(context.Background(), f.e.Owner+".team.nobody", "hi", 0); err == nil || !strings.Contains(err.Error(), "no endpoint named") {
		t.Fatalf("send to an unknown dotted address: %v", err)
	}
}

// (2) Two endpoints with the same instance token in two groups share one directory and never see each other's mail.
func TestSameInstanceInTwoGroupsWithoutCrosstalk(t *testing.T) {
	f := newFixture(t, "a", "b") // group dev
	ta := f.addAt(t, "team", "a", addOpts{})
	tb := f.addAt(t, "team", "b", addOpts{})
	_, dev := f.run(t, "a")
	_, tev := f.serveAt(t, ta, noPrompt)
	db := f.client(t, "b")
	tbc := f.clientAt(t, tb)
	ctx := context.Background()
	if _, err := db.Send(ctx, tdot("a"), "to dev a", 0); err != nil {
		t.Fatal(err)
	}
	if _, err := tbc.Send(ctx, ta.Flat(), "to team a", 0); err != nil {
		t.Fatal(err)
	}
	dev.waitFor(t, 1, 5*time.Second)
	tev.waitFor(t, 1, 5*time.Second)
	time.Sleep(300 * time.Millisecond)
	dg, tg := dev.all(), tev.all()
	if len(dg) != 1 || !strings.Contains(dg[0], "to dev a") || !strings.Contains(dg[0], "from "+tdot("b")+" [verified]") || strings.Contains(dg[0], "to team") {
		t.Fatalf("dev a got %q", dg)
	}
	if len(tg) != 1 || !strings.Contains(tg[0], "to team a") || !strings.Contains(tg[0], "from "+tb.String()+" [verified]") || strings.Contains(tg[0], "to dev") {
		t.Fatalf("team a got %q", tg)
	}
	// Each stream holds exactly its own message.
	js, err := jetstream.New(f.e.Conn(t, f.e.AdminCreds))
	if err != nil {
		t.Fatal(err)
	}
	for stream, want := range map[string]uint64{taddr("a").InboxStream(): 1, ta.InboxStream(): 1, taddr("b").InboxStream(): 0, tb.InboxStream(): 0} {
		s, err := js.Stream(ctx, stream)
		if err != nil {
			t.Fatal(err)
		}
		if info, err := s.Info(ctx); err != nil || info.State.Msgs != want {
			t.Fatalf("%s: %+v %v", stream, info, err)
		}
	}
	// All four are in the directory, under their flat keys.
	var keys []string
	for _, p := range tbc.Who() {
		keys = append(keys, flatOf(p.Record))
	}
	if want := strings.Join([]string{tkey("a"), tkey("b"), ta.Flat(), tb.Flat()}, ","); strings.Join(keys, ",") != want {
		t.Fatalf("directory keys %v, want %s", keys, want)
	}
}

// (3) A prompt round trip between two endpoints.
func TestPromptRoundTrip(t *testing.T) {
	f := newFixture(t)
	ida := f.addAt(t, "team", "a", addOpts{})
	idb := f.addAt(t, "team", "b", addOpts{})
	got := make(chan PromptEvent, 1)
	f.serveAt(t, ida, func(ep *Endpoint, p PromptEvent) {
		got <- p
		_ = ep.Reply(p.RequestID, "pong ", false)
		_ = ep.Reply(p.RequestID, "from a", true)
	})
	b := f.clientAt(t, idb)
	ctx := context.Background()
	subj, err := DiscoverRecipient(ctx, b.Conn(), ida.Flat())
	if err != nil || subj != mustSubj(ida).Prompt {
		t.Fatalf("discover: %q %v", subj, err)
	}
	if s2, err := DiscoverRecipient(ctx, b.Conn(), ida.String()); err != nil || s2 != subj {
		t.Fatalf("the dotted form names the same endpoint: %q %v", s2, err)
	}
	for _, bad := range []string{"a", f.e.Owner + ".team"} {
		if _, err := DiscoverRecipient(ctx, b.Conn(), bad); err == nil || !strings.Contains(err.Error(), "owner.group.name") {
			t.Fatalf("%q: %v", bad, err)
		}
	}
	var out bytes.Buffer
	if err := Prompt(ctx, b.Conn(), b.Identity(), b.Key(), subj, "ping", &out); err != nil {
		t.Fatal(err)
	}
	if out.String() != "pong from a" {
		t.Fatalf("reply %q", out.String())
	}
	if p := <-got; p.From != idb.Flat() || p.Trust != sap.TrustVerified || p.Text != "ping" {
		t.Fatalf("event %+v", p)
	}
}

// (4) The open fails closed, with a clear error, when the endpoint's own directory record is absent or not a usable
// record, and when the credentials are for another endpoint.
func TestOpenFailsClosed(t *testing.T) {
	f := newFixture(t, "a")
	noRec := f.addAt(t, "team", "norec", addOpts{noRecord: true})
	bad := f.addAt(t, "team", "bad", addOpts{badRecord: true})
	other := f.addAt(t, "team", "other", addOpts{})
	wrong := f.addAt(t, "team", "wrong", addOpts{credsOf: other.Flat()})
	for _, tc := range []struct {
		name string
		id   sap.Address
		want string
	}{
		{"no record", noRec, "is not provisioned"},
		{"unusable record", bad, "is not provisioned"},
		{"credentials of another endpoint", wrong, "do not match endpoint " + wrong.String()},
	} {
		for opener, open := range map[string]func() error{
			"Open":       func() error { _, err := Open(context.Background(), f.cfgAt(tc.id)); return err },
			"OpenClient": func() error { _, err := OpenClient(context.Background(), f.cfgAt(tc.id)); return err },
		} {
			if err := open(); err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Errorf("%s via %s: %v (want %q)", tc.name, opener, err, tc.want)
			}
		}
	}
}

// (5) The lease is keyed by the flat key: a second open of the same address conflicts, and an endpoint with the same
// instance token in another group is not affected.
func TestLeaseConflictAndGroupIndependence(t *testing.T) {
	f := newFixture(t, "a")
	ida := f.addAt(t, "team", "a", addOpts{})
	ctx := context.Background()
	first, err := Open(ctx, f.cfgAt(ida))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(first.Close)
	_, err = Open(ctx, f.cfgAt(ida))
	var held *LeaseHeldError
	if !errors.As(err, &held) || held.Name != ida.Flat() {
		t.Fatalf("second open: %v", err)
	}
	other, err := Open(ctx, f.cfg("a"))
	if err != nil {
		t.Fatalf("open with the same instance token in another group: %v", err)
	}
	t.Cleanup(other.Close)
	// The two leases are two keys.
	js, _ := jetstream.New(f.e.Conn(t, f.e.AdminCreds))
	kv, err := js.KeyValue(ctx, store.LeaseBucket)
	if err != nil {
		t.Fatal(err)
	}
	for _, k := range []string{tkey("a"), ida.Flat()} {
		if _, err := kv.Get(ctx, k); err != nil {
			t.Fatalf("lease %s: %v", k, err)
		}
	}
	// Closing the holder frees only its own key: the same address can open again.
	first.Close()
	again, err := Open(ctx, f.cfgAt(ida))
	if err != nil {
		t.Fatalf("reopen after close: %v", err)
	}
	again.Close()
}
