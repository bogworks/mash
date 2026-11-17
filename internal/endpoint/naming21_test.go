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
	"github.com/bogworks/mash/internal/render"
	"github.com/bogworks/mash/internal/sap"
	"github.com/bogworks/mash/internal/store"
)

// In-process, end to end: every endpoint here is provisioned through Admin.Provision and runs on the credentials minted
// for it (never the admin credentials). The CLI half (who, inbox, header, trust line, wake) is in
// cmd/mash/naming21_cli_test.go.

// provision provisions (the fixture's owner, group, name) through Admin.Provision with the permissions the admin layer
// computes for it, and registers its credentials with the fixture.
func (f *fixture) provision(t *testing.T, group, name string) sap.Address {
	t.Helper()
	a, err := sap.NewAddress(f.e.Owner, group, name)
	if err != nil {
		t.Fatal(err)
	}
	creds, user := f.e.Mint(t, a)
	if _, err := f.adm.Provision(context.Background(), a, user, f.e.Account, "", admin.Meta{}); err != nil {
		t.Fatal(err)
	}
	f.creds[a.Flat()], f.users[a.Flat()] = creds, user
	return a
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

// (a) An endpoint with dashes in its instance, provisioned through Admin.Provision, sends, receives, replies, is prompted
// and woken, and its heartbeat is on exactly five tokens.
func TestAddressEndToEnd(t *testing.T) {
	f := newFixture(t)
	sdk := f.provision(t, "x", "sap-go-sdk")
	peer := f.provision(t, "x", "peer")
	key := sdk.Flat()
	rec := f.record(t, key)
	if rec.Instance != "sap-go-sdk" || rec.Group != "x" || rec.Owner != f.e.Owner || rec.Agent != sap.AgentName || rec.MailSubject != sdk.Subjects().Mail {
		t.Fatalf("record %+v", rec)
	}
	prompts := make(chan PromptEvent, 1)
	a, aev := f.serveAt(t, sdk, func(ep *Endpoint, p PromptEvent) {
		prompts <- p
		_ = ep.Reply(p.RequestID, "pong ", false)
		_ = ep.Reply(p.RequestID, "from sdk", true)
	})
	b, bev := f.serveAt(t, peer, noPrompt)
	if a.Key() != key || a.Name() != "sap-go-sdk" {
		t.Fatalf("key %q name %q", a.Key(), a.Name())
	}
	ctx := context.Background()

	// Receive: mail from the peer wakes the endpoint (the delivery event is what serve turns into the channel wake: a
	// one-line count naming the sender by its dotted address), is [verified], and reads back. The stored sender stays the
	// flat key; the trust label and body are on the rendered message, which shows the dotted form.
	seq, err := b.Send(ctx, key, "hello sdk", 0)
	if err != nil {
		t.Fatal(err)
	}
	got := aev.waitFor(t, 1, 5*time.Second)
	if !strings.HasPrefix(got[0], "1 message from "+peer.String()+" (") || strings.Contains(got[0], peer.Flat()) {
		t.Fatalf("wake event: %s", got[0])
	}
	m, err := a.Read(ctx, seq)
	if err != nil || m.From != peer.Flat() || m.Trust != sap.TrustVerified || string(m.Body) != "hello sdk" {
		t.Fatalf("read: %+v %v", m, err)
	}
	if one := render.One(m); !strings.Contains(one, "from "+peer.String()+" [verified]") || !strings.Contains(one, "> hello sdk") || strings.Contains(one, peer.Flat()) {
		t.Fatalf("rendered message: %s", one)
	}
	if ms, err := a.Inbox(ctx, 20); err != nil || len(ms) != 1 || ms[0].Seq != seq {
		t.Fatalf("inbox: %+v %v", ms, err)
	}
	ackFloor(t, f, sdk.InboxStream(), seq)

	// Reply: the answer reaches the peer, tied to the original.
	rseq, err := a.Send(ctx, peer.String(), "hello peer", seq)
	if err != nil {
		t.Fatal(err)
	}
	got = bev.waitFor(t, 1, 5*time.Second)
	if !strings.HasPrefix(got[0], "1 message from "+sdk.String()+" (") || strings.Contains(got[0], key) {
		t.Fatalf("reply event: %s", got[0])
	}
	if rm, err := b.Read(ctx, rseq); err != nil || rm.From != key || rm.Trust != sap.TrustVerified || rm.ReplyTo != seq || string(rm.Body) != "hello peer" {
		t.Fatalf("peer read (want from %s, verified, answers #%d, body %q): %+v %v", key, seq, "hello peer", rm, err)
	} else if one := render.One(rm); !strings.Contains(one, "from "+sdk.String()+" [verified]") || !strings.Contains(one, "> hello peer") {
		t.Fatalf("rendered reply: %s", one)
	}

	// Prompt round trip on the five-token prompt subject.
	subj, err := DiscoverRecipient(ctx, b.Conn(), key)
	if err != nil || subj != sdk.Subjects().Prompt {
		t.Fatalf("discover: %q %v", subj, err)
	}
	var out strings.Builder
	if err := Prompt(ctx, b.Conn(), b.Identity(), b.Key(), subj, "ping", &out); err != nil {
		t.Fatal(err)
	}
	if out.String() != "pong from sdk" {
		t.Fatalf("prompt reply %q", out.String())
	}
	if p := <-prompts; p.From != peer.Flat() || p.Trust != sap.TrustVerified || p.Text != "ping" {
		t.Fatalf("prompt event %+v", p)
	}

	// Heartbeat: exactly agents.hb.<owner>.x.sap-go-sdk. Subscribe and flush before it is published.
	hbSubj := sdk.Subjects().HB
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
	// The sender name is the flat key; its three tokens are byte-identical to the address's, and so are the three
	// tokens of the subject it was published on.
	if h.Name != sdk.Flat() {
		t.Fatalf("heartbeat sender %q, want %q", h.Name, sdk.Flat())
	}
	if tok := strings.Split(h.Name, "_"); len(tok) != 3 || tok[0] != sdk.Owner || tok[1] != sdk.Group || tok[2] != sdk.Instance {
		t.Fatalf("heartbeat sender tokens %q, want %q %q %q", tok, sdk.Owner, sdk.Group, sdk.Instance)
	}
	if tok := strings.Split(hm.Subject, "."); tok[2] != sdk.Owner || tok[3] != sdk.Group || tok[4] != sdk.Instance {
		t.Fatalf("heartbeat subject tokens %q", tok)
	}
}

// (c) Two addresses that differ only in group share an instance token and coexist: separate streams, directory keys,
// reply prefixes and NATS users, all keyed on the flat form, and mail reaches only its addressee.
func TestAddressesDifferingInGroupCoexist(t *testing.T) {
	f := newFixture(t)
	idx := f.provision(t, "x", "svc")
	idy := f.provision(t, "y", "svc")
	kx, ky := idx.Flat(), idy.Flat()
	if kx == ky {
		t.Fatalf("flat %q %q", kx, ky)
	}
	senderX := f.clientAt(t, f.provision(t, "x", "sender"))
	senderY := f.clientAt(t, f.provision(t, "y", "sender"))
	ex, xev := f.serveAt(t, idx, noPrompt)
	ey, yev := f.serveAt(t, idy, noPrompt)
	ctx := context.Background()

	// Distinct directory keys, each record with its own tokens.
	for a, want := range map[sap.Address]string{idx: "x", idy: "y"} {
		if r := f.record(t, a.Flat()); r.Group != want || r.Instance != "svc" || r.Owner != f.e.Owner || r.Agent != sap.AgentName {
			t.Fatalf("record %s: %+v", a.Flat(), r)
		}
	}
	// Distinct NATS users named by the flat form, and distinct reply prefixes keyed on it.
	if ux, uy := natsUserName(t, f.creds[kx]), natsUserName(t, f.creds[ky]); ux != kx || uy != ky || ux == uy {
		t.Fatalf("user names %q %q", ux, uy)
	}
	px, py := ex.Conn().NewRespInbox(), ey.Conn().NewRespInbox()
	if !strings.HasPrefix(px, idx.InboxPrefix()+".") || !strings.HasPrefix(py, idy.InboxPrefix()+".") || strings.HasPrefix(px, idy.InboxPrefix()) {
		t.Fatalf("reply prefixes %q %q", px, py)
	}
	if idx.InboxPrefix() == idy.InboxPrefix() || idx.InboxStream() == idy.InboxStream() {
		t.Fatal("prefix or stream name collide")
	}

	// Mail to each reaches only that one.
	sx, err := senderX.Send(ctx, kx, "for x svc", 0)
	if err != nil {
		t.Fatal(err)
	}
	sy, err := senderY.Send(ctx, ky, "for y svc", 0)
	if err != nil {
		t.Fatal(err)
	}
	xg, yg := xev.waitFor(t, 1, 5*time.Second), yev.waitFor(t, 1, 5*time.Second)
	time.Sleep(300 * time.Millisecond)
	xg, yg = xev.all(), yev.all()
	// The delivery text is the full batch, body included: the endpoint has no spool, so it is not hooked and deliverBatch
	// renders render.Batch, which shows every message in full while there are at most BatchMsgs (10) and BatchBytes
	// (16384) of them. So each text starts with its count line naming its own peer and carries only its own body.
	xs, ys := f.e.Owner+".x.sender", f.e.Owner+".y.sender"
	if len(xg) != 1 || !strings.HasPrefix(xg[0], "1 message from "+xs+" (") || strings.Contains(xg[0], ys) {
		t.Fatalf("x count line: %q", xg)
	}
	if len(yg) != 1 || !strings.HasPrefix(yg[0], "1 message from "+ys+" (") || strings.Contains(yg[0], xs) {
		t.Fatalf("y count line: %q", yg)
	}
	if !strings.Contains(xg[0], "for x svc") || strings.Contains(xg[0], "for y svc") {
		t.Fatalf("x got %q", xg)
	}
	if !strings.Contains(yg[0], "for y svc") || strings.Contains(yg[0], "for x svc") {
		t.Fatalf("y got %q", yg)
	}
	for stream, want := range map[string]uint64{idx.InboxStream(): 1, idy.InboxStream(): 1} {
		if n := streamMsgs(t, f, stream); n != want {
			t.Fatalf("%s holds %d, want %d", stream, n, want)
		}
	}
	if m, err := ex.Read(ctx, sx); err != nil || string(m.Body) != "for x svc" || m.Trust != sap.TrustVerified {
		t.Fatalf("x read: %+v %v", m, err)
	} else if one := render.One(m); !strings.Contains(one, "[verified]") || !strings.Contains(one, "> for x svc") || strings.Contains(one, "for y svc") {
		t.Fatalf("x rendered: %s", one)
	}
	if m, err := ey.Read(ctx, sy); err != nil || string(m.Body) != "for y svc" || m.Trust != sap.TrustVerified {
		t.Fatalf("y read: %+v %v", m, err)
	} else if one := render.One(m); !strings.Contains(one, "[verified]") || !strings.Contains(one, "> for y svc") || strings.Contains(one, "for x svc") {
		t.Fatalf("y rendered: %s", one)
	}
	// Leases are two keys; a second open of one address conflicts without touching the other.
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
	if _, err := Open(ctx, f.cfgAt(idx)); err == nil {
		t.Fatalf("second open of %s succeeded", idx)
	}
	// who lists both, under their flat keys.
	ps, err := senderX.WhoFresh(ctx)
	if err != nil {
		t.Fatal(err)
	}
	online := map[string]bool{}
	for _, p := range ps {
		online[flatOf(p.Record)] = p.Online
	}
	if _, ok := online[ky]; !ok || !online[kx] {
		t.Fatalf("who: %v", online)
	}
}
