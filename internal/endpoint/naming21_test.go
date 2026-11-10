package endpoint

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/nats-io/jwt/v2"
	"github.com/nats-io/nats.go/jetstream"

	"github.com/bogworks/mash/internal/admin"
	"github.com/bogworks/mash/internal/sap"
	"github.com/bogworks/mash/internal/store"
)

// Naming plan Phase 2.1 (AT-002), in-process: every endpoint here is provisioned through Admin.ProvisionIdentity and
// runs on the credentials minted for it (never the admin credentials). The CLI half (who, inbox, header, trust line,
// wake) is in cmd/mash/naming21_cli_test.go.

// naming21AliasPRsMerged gates the rename test, which needs the two-stream read and the alias lease grant.
const naming21AliasPRsMerged = false // flip when #346 and #356 are on main

// provisionNF provisions agent.owner.name through ProvisionIdentity (alias optional) with the permissions the admin
// layer computes for it, and registers its credentials with the fixture.
func (f *fixture) provisionNF(t *testing.T, agent, owner, name string, alias *store.Alias, extra ...string) sap.EndpointIdentity {
	t.Helper()
	id, err := sap.New(agent, owner, name)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	existing, err := f.adm.List(ctx)
	if err != nil {
		t.Fatal(err)
	}
	perms, err := admin.NewFormUserPermissions(id, alias, existing, extra)
	if err != nil {
		t.Fatal(err)
	}
	creds, user := f.e.MintRaw(t, id.Flat(), perms)
	if _, err := f.adm.ProvisionIdentity(ctx, id, alias, user, f.e.Account, "", admin.Meta{}); err != nil {
		t.Fatal(err)
	}
	f.creds[id.Flat()], f.users[id.Flat()] = creds, user
	return id
}

func (f *fixture) record(t *testing.T, key string) store.Record {
	t.Helper()
	js, err := jetstream.New(f.e.Conn(t, f.e.AdminCreds))
	if err != nil {
		t.Fatal(err)
	}
	kv, err := js.KeyValue(context.Background(), store.DirectoryBucket)
	if err != nil {
		t.Fatal(err)
	}
	r, err := store.GetRecord(context.Background(), kv, key)
	if err != nil {
		t.Fatalf("directory record %s: %v", key, err)
	}
	return r
}

// natsUserName is the name inside the user JWT of a creds file.
func natsUserName(t *testing.T, credsPath string) string {
	t.Helper()
	b, err := os.ReadFile(credsPath)
	if err != nil {
		t.Fatal(err)
	}
	tok, err := jwt.ParseDecoratedJWT(b)
	if err != nil {
		t.Fatal(err)
	}
	uc, err := jwt.DecodeUserClaims(tok)
	if err != nil {
		t.Fatal(err)
	}
	return uc.Name
}

func streamMsgs(t *testing.T, f *fixture, stream string) uint64 {
	t.Helper()
	js, err := jetstream.New(f.e.Conn(t, f.e.AdminCreds))
	if err != nil {
		t.Fatal(err)
	}
	s, err := js.Stream(context.Background(), stream)
	if err != nil {
		t.Fatal(err)
	}
	info, err := s.Info(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	return info.State.Msgs
}

// (a) A three-token endpoint with dashes in its name, provisioned through ProvisionIdentity, sends, receives, replies,
// is prompted and woken, and its heartbeat is on exactly five tokens.
func TestNaming21_ThreeTokenEndpointEndToEnd(t *testing.T) {
	f := newFixture(t)
	sdk := f.provisionNF(t, "bogworks", "x", "sap-go-sdk", nil)
	peer := f.provisionNF(t, "bogworks", "x", "peer", nil)
	const key = "bogworks_x_sap-go-sdk"
	if sdk.Flat() != key {
		t.Fatalf("flat %q", sdk.Flat())
	}
	rec := f.record(t, key)
	if rec.Name != "sap-go-sdk" || rec.Owner != "x" || rec.AgentToken != "bogworks" || rec.MailSubject != "agents.mail.bogworks.x.sap-go-sdk" {
		t.Fatalf("record %+v", rec)
	}
	prompts := make(chan PromptEvent, 1)
	a, aev := f.serveNF(t, sdk, func(ep *Endpoint, p PromptEvent) {
		prompts <- p
		_ = ep.Reply(p.RequestID, "pong ", false)
		_ = ep.Reply(p.RequestID, "from sdk", true)
	})
	b, bev := f.serveNF(t, peer, noPrompt)
	if a.Key() != key || a.Name() != "sap-go-sdk" {
		t.Fatalf("key %q name %q", a.Key(), a.Name())
	}
	ctx := context.Background()

	// Receive: mail from the peer wakes the endpoint (the delivery event is what serve turns into the channel wake),
	// is [verified], and reads back.
	seq, err := b.Send(ctx, key, "hello sdk", 0)
	if err != nil {
		t.Fatal(err)
	}
	got := aev.waitFor(t, 1, 5*time.Second)
	if !strings.Contains(got[0], "from bogworks_x_peer [verified]") || !strings.Contains(got[0], "hello sdk") {
		t.Fatalf("wake event: %s", got[0])
	}
	m, err := a.Read(ctx, seq)
	if err != nil || m.From != "bogworks_x_peer" || m.Trust != sap.TrustVerified || string(m.Body) != "hello sdk" {
		t.Fatalf("read: %+v %v", m, err)
	}
	if ms, err := a.Inbox(ctx, 20); err != nil || len(ms) != 1 || ms[0].Seq != seq {
		t.Fatalf("inbox: %+v %v", ms, err)
	}
	ackFloor(t, f, "inbox-"+key, seq)

	// Reply: the answer reaches the peer, tied to the original.
	rseq, err := a.Send(ctx, "bogworks_x_peer", "hello peer", seq)
	if err != nil {
		t.Fatal(err)
	}
	got = bev.waitFor(t, 1, 5*time.Second)
	if !strings.Contains(got[0], "from "+key+" [verified]") || !strings.Contains(got[0], "hello peer") {
		t.Fatalf("reply event: %s", got[0])
	}
	if rm, err := b.Read(ctx, rseq); err != nil || rm.From != key || rm.Trust != sap.TrustVerified || rm.ReplyTo != seq || string(rm.Body) != "hello peer" {
		t.Fatalf("peer read (want from %s, verified, answers #%d, body %q): %+v %v", key, seq, "hello peer", rm, err)
	}

	// Prompt round trip on the five-token prompt subject.
	subj, err := DiscoverRecipient(ctx, b.Conn(), "x", key)
	if err != nil || subj != "agents.prompt.bogworks.x.sap-go-sdk" {
		t.Fatalf("discover: %q %v", subj, err)
	}
	var out strings.Builder
	if err := Prompt(ctx, b.Conn(), b.Identity(), b.Key(), subj, "ping", &out); err != nil {
		t.Fatal(err)
	}
	if out.String() != "pong from sdk" {
		t.Fatalf("prompt reply %q", out.String())
	}
	if p := <-prompts; p.From != "bogworks_x_peer" || p.Trust != sap.TrustVerified || p.Text != "ping" {
		t.Fatalf("prompt event %+v", p)
	}

	// Heartbeat: exactly agents.hb.bogworks.x.sap-go-sdk. Subscribe and flush before it is published.
	const hbSubj = "agents.hb.bogworks.x.sap-go-sdk"
	sub, err := b.Conn().SubscribeSync(hbSubj)
	if err != nil {
		t.Fatal(err)
	}
	if err := b.Conn().Flush(); err != nil {
		t.Fatal(err)
	}
	a.publishHeartbeat()
	hm, err := sub.NextMsg(5 * time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if hm.Subject != hbSubj || len(strings.Split(hm.Subject, ".")) != 5 {
		t.Fatalf("heartbeat subject %q", hm.Subject)
	}
	h, tr := sap.VerifyStored(hm.Header.Get(sap.HeaderName), hm.Subject, hm.Data)
	if tr != sap.TrustVerified {
		t.Fatalf("heartbeat trust %v, want %v (header %+v)", tr, sap.TrustVerified, h)
	}
	// The sender name is the flat key; its three tokens are byte-identical to the identity's, and so are the three
	// tokens of the subject it was published on.
	if h.Name != sdk.Flat() {
		t.Fatalf("heartbeat sender %q, want %q", h.Name, sdk.Flat())
	}
	if tok := strings.Split(h.Name, "_"); len(tok) != 3 || tok[0] != sdk.Agent || tok[1] != sdk.Owner || tok[2] != sdk.Name {
		t.Fatalf("heartbeat sender tokens %q, want %q %q %q", tok, sdk.Agent, sdk.Owner, sdk.Name)
	}
	if tok := strings.Split(hm.Subject, "."); tok[2] != sdk.Agent || tok[3] != sdk.Owner || tok[4] != sdk.Name {
		t.Fatalf("heartbeat subject tokens %q", tok)
	}
}

// (c) Two identities that differ only in owner share a lane name and coexist: separate streams, directory keys, reply
// prefixes and NATS users, all keyed on the flat form, and mail reaches only its addressee.
func TestNaming21_TwoLeadsCoexist(t *testing.T) {
	f := newFixture(t)
	idx := f.provisionNF(t, "bogworks", "x", "lead", nil)
	idy := f.provisionNF(t, "bogworks", "y", "lead", nil)
	const kx, ky = "bogworks_x_lead", "bogworks_y_lead"
	if idx.Flat() != kx || idy.Flat() != ky {
		t.Fatalf("flat %q %q", idx.Flat(), idy.Flat())
	}
	// Each lead gets mail from a peer of its own herd (a cross-owner sender needs policy grants and is not [verified] by default).
	senderX := f.clientNF(t, f.provisionNF(t, "bogworks", "x", "sender", nil))
	senderY := f.clientNF(t, f.provisionNF(t, "bogworks", "y", "sender", nil))
	ex, xev := f.serveNF(t, idx, noPrompt)
	ey, yev := f.serveNF(t, idy, noPrompt)
	ctx := context.Background()

	// Distinct directory keys, each record with its own tokens.
	for key, want := range map[string]string{kx: "x", ky: "y"} {
		if r := f.record(t, key); r.Owner != want || r.Name != "lead" || r.AgentToken != "bogworks" {
			t.Fatalf("record %s: %+v", key, r)
		}
	}
	// Distinct NATS users named by the flat form, and distinct reply prefixes keyed on it.
	if ux, uy := natsUserName(t, f.creds[kx]), natsUserName(t, f.creds[ky]); ux != kx || uy != ky || ux == uy {
		t.Fatalf("user names %q %q", ux, uy)
	}
	px, py := ex.Conn().NewRespInbox(), ey.Conn().NewRespInbox()
	if !strings.HasPrefix(px, sap.InboxPrefix(kx)+".") || !strings.HasPrefix(py, sap.InboxPrefix(ky)+".") || strings.HasPrefix(px, sap.InboxPrefix(ky)) {
		t.Fatalf("reply prefixes %q %q", px, py)
	}
	if sap.InboxPrefix(kx) == sap.InboxPrefix(ky) || sap.InboxStream(kx) == sap.InboxStream(ky) {
		t.Fatal("prefix or stream name collide")
	}

	// Mail to each reaches only that one.
	sx, err := senderX.Send(ctx, kx, "for x lead", 0)
	if err != nil {
		t.Fatal(err)
	}
	sy, err := senderY.Send(ctx, ky, "for y lead", 0)
	if err != nil {
		t.Fatal(err)
	}
	xg, yg := xev.waitFor(t, 1, 5*time.Second), yev.waitFor(t, 1, 5*time.Second)
	time.Sleep(300 * time.Millisecond)
	xg, yg = xev.all(), yev.all()
	if len(xg) != 1 || !strings.Contains(xg[0], "for x lead") || strings.Contains(xg[0], "for y lead") {
		t.Fatalf("x lead got %q", xg)
	}
	if len(yg) != 1 || !strings.Contains(yg[0], "for y lead") || strings.Contains(yg[0], "for x lead") {
		t.Fatalf("y lead got %q", yg)
	}
	for stream, want := range map[string]uint64{sap.InboxStream(kx): 1, sap.InboxStream(ky): 1} {
		if n := streamMsgs(t, f, stream); n != want {
			t.Fatalf("%s holds %d, want %d", stream, n, want)
		}
	}
	if m, err := ex.Read(ctx, sx); err != nil || string(m.Body) != "for x lead" || m.Trust != sap.TrustVerified {
		t.Fatalf("x read: %+v %v", m, err)
	}
	if m, err := ey.Read(ctx, sy); err != nil || string(m.Body) != "for y lead" || m.Trust != sap.TrustVerified {
		t.Fatalf("y read: %+v %v", m, err)
	}
	// Leases are two keys; a second open of one identity conflicts without touching the other.
	js, _ := jetstream.New(f.e.Conn(t, f.e.AdminCreds))
	kv, err := js.KeyValue(ctx, store.LeaseBucket)
	if err != nil {
		t.Fatal(err)
	}
	for _, k := range []string{kx, ky} {
		if _, err := kv.Get(ctx, k); err != nil {
			t.Fatalf("lease %s: %v", k, err)
		}
	}
	if _, err := Open(ctx, f.cfgNF(idx)); err == nil {
		t.Fatal("second open of bogworks.x.lead succeeded")
	}
	// who lists both, under their flat keys.
	ps, err := senderX.WhoFresh(ctx)
	if err != nil {
		t.Fatal(err)
	}
	// (Presence comes from heartbeats, which a user receives only for its own owner and agent token: the x sender sees
	// the x lead online, and the y lead listed.)
	online := map[string]bool{}
	for _, p := range ps {
		online[p.Record.KeyOrName()] = p.Online
	}
	if _, ok := online[ky]; !ok || !online[kx] {
		t.Fatalf("who: %v", online)
	}
}

// (d) The rename: a legacy endpoint with signed mail queued before it becomes a three-token identity that carries the
// legacy identity as its alias. The renamed endpoint reads the queued mail exactly once ([verified]), not again after
// a restart, and mail sent to the old name afterwards also lands once.
func TestNaming21_RenameDeliversQueuedMailOnce(t *testing.T) {
	if !naming21AliasPRsMerged {
		t.Skip("needs #346 (two-stream read) and #356 (alias lease grant): flip naming21AliasPRsMerged when they are on main")
	}
	const old = "sap-go-sdk"
	f := newFixture(t, old, "peer") // legacy endpoints under f.e.Owner
	ctx := context.Background()

	// The legacy endpoint receives signed mail, then stops; more mail queues while it is down.
	lctx, lcancel := context.WithCancel(ctx)
	lep, err := Open(lctx, f.cfg(old))
	if err != nil {
		t.Fatal(err)
	}
	lev := &events{}
	ldone := make(chan struct{})
	go func() { _ = lep.Run(lctx, lev.deliver); close(ldone) }()
	peer := f.client(t, "peer")
	s1, err := peer.Send(ctx, old, "before-1", 0)
	if err != nil {
		t.Fatal(err)
	}
	lev.waitFor(t, 1, 5*time.Second)
	ackFloor(t, f, "inbox-"+old, s1)
	lcancel()
	<-ldone
	lep.Close()
	var lastQueued uint64
	for _, body := range []string{"queued-2", "queued-3"} {
		if lastQueued, err = peer.Send(ctx, old, body, 0); err != nil {
			t.Fatal(err)
		}
	}

	// The rename: provision the three-token identity with the legacy identity as its alias.
	id := f.provisionNF(t, "bogworks", "x", old, &store.Alias{Owner: f.e.Owner, Name: old})
	legacy, renamed := "inbox-"+old, sap.InboxStream(id.Flat())
	count := func(all []string, body string) int {
		n := 0
		for _, e := range all {
			n += strings.Count(e, body)
		}
		return n
	}
	runRenamed := func() (cancel func(), ev *events) {
		rctx, rcancel := context.WithCancel(ctx)
		ep, err := Open(rctx, f.cfgNF(id))
		if err != nil {
			rcancel()
			t.Fatal(err)
		}
		ev = &events{}
		done := make(chan struct{})
		go func() { _ = ep.Run(rctx, ev.deliver); close(done) }()
		return func() { rcancel(); <-done; ep.Close() }, ev
	}
	// idle polls (bounded) until each stream's consumer has nothing awaiting an ack and nothing left to deliver.
	idle := func(streams ...string) {
		t.Helper()
		js, err := jetstream.New(f.e.Conn(t, f.e.AdminCreds))
		if err != nil {
			t.Fatal(err)
		}
		deadline := time.Now().Add(10 * time.Second)
		for _, stream := range streams {
			for {
				c, err := js.Consumer(ctx, stream, store.Consumer)
				if err != nil {
					t.Fatalf("consumer on %s: %v", stream, err)
				}
				info, err := c.Info(ctx)
				if err == nil && info.NumAckPending == 0 && info.NumPending == 0 {
					break
				}
				if time.Now().After(deadline) {
					t.Fatalf("%s: consumer not idle (ack pending %d, pending %d)", stream, info.NumAckPending, info.NumPending)
				}
				time.Sleep(20 * time.Millisecond)
			}
		}
	}
	// restart runs the renamed endpoint again once the previous one is stopped, waits until it is ready (Open
	// returned, consumers idle), asserts nothing was redelivered, then proves it is live and ordered with a sentinel
	// sent to the old name: only the sentinel arrives, exactly once, so any replay (which would precede it) is caught.
	restart := func(sentinel string) (stop func()) {
		t.Helper()
		stop, ev := runRenamed()
		idle(legacy, renamed)
		if got := ev.all(); len(got) != 0 {
			t.Fatalf("redelivered after restart: %q", got)
		}
		seq, err := peer.Send(ctx, old, sentinel, 0)
		if err != nil {
			t.Fatal(err)
		}
		ev.waitFor(t, 1, 10*time.Second)
		ackFloor(t, f, legacy, seq)
		idle(legacy, renamed)
		if got := ev.all(); len(got) != 1 || count(got, sentinel) != 1 || count(got, "queued-") != 0 || count(got, "before-1") != 0 || count(got, "after-") != 1 {
			t.Fatalf("after restart, want only %s once, got %q", sentinel, got)
		}
		return stop
	}

	stop, ev := runRenamed()
	// Barrier: both queued messages delivered (an event may be a digest of several), then both acknowledged with
	// nothing pending, before anything is counted.
	deadline := time.Now().Add(10 * time.Second)
	for count(ev.all(), "queued-2") == 0 || count(ev.all(), "queued-3") == 0 {
		if time.Now().After(deadline) {
			t.Fatalf("queued mail not delivered: %q", ev.all())
		}
		time.Sleep(20 * time.Millisecond)
	}
	ackFloor(t, f, legacy, lastQueued)
	idle(legacy, renamed)
	all := ev.all()
	if count(all, "queued-2") != 1 || count(all, "queued-3") != 1 || count(all, "before-1") != 0 {
		t.Fatalf("queued mail delivered wrongly:\n%s", strings.Join(all, "\n"))
	}
	// Each queued message carries its own [verified] header, checked message by message.
	for _, body := range []string{"queued-2", "queued-3"} {
		var hdr string
		for _, e := range all {
			parts := strings.Split(e, "--- #")
			for _, part := range parts[1:] {
				if strings.Contains(part, "> "+body) {
					hdr, _, _ = strings.Cut(part, "\n")
				}
			}
		}
		if !strings.Contains(hdr, " from ") || !strings.Contains(hdr, "[verified]") || strings.Contains(hdr, "[claimed]") || strings.Contains(hdr, "[unknown-signer]") {
			t.Fatalf("%s header %q, want [verified]", body, hdr)
		}
	}
	stop()

	// A restart does not show it again, and mail to the old name after the rename lands once.
	stop = restart("after-4")
	stop()
	stop2 := restart("after-5")
	defer stop2()
}
