package endpoint

import (
	"context"
	"strings"
	"testing"
	"time"

	sdkid "github.com/bogworks/synadia-agents.go/identity"
	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"

	"github.com/bogworks/mash/internal/kind"
	"github.com/bogworks/mash/internal/sap"
)

// Compatibility gate on live data: real mails sent between fixture endpoints, read straight from the recipient's
// JetStream inbox stream, are classified by mash's sap.VerifyStored and by the SAP Go SDK's sdkid.VerifyStored. The
// two must give the same label for every stored message, and none may be forged or malformed. See
// internal/sap/compat_corpus_test.go for the hand-made headers on which they differ.

// compatStoredLabels reads every message of stream and returns both classifiers' labels for each.
func compatStoredLabels(t *testing.T, f *fixture, stream string, want uint64) (mash, sdk []string) {
	t.Helper()
	js, err := jetstream.New(f.e.Conn(t, f.e.AdminCreds))
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	s, err := js.Stream(ctx, stream)
	if err != nil {
		t.Fatal(err)
	}
	if n := s.CachedInfo().State.Msgs; n != want {
		t.Fatalf("%s holds %d messages, want %d", stream, n, want)
	}
	for seq := uint64(1); seq <= want; seq++ {
		raw, err := s.GetMsg(ctx, seq)
		if err != nil {
			t.Fatal(err)
		}
		_, m := sap.VerifyStored(raw.Header.Get(sap.HeaderName), raw.Subject, raw.Data)
		snd, err := sdkid.VerifyStored(sdkid.Message{Subject: raw.Subject, Data: raw.Data, Header: nats.Header(raw.Header)},
			sdkid.StoredOptions{})
		d := string(sdkid.TrustOf(snd))
		if err != nil {
			d = "error: " + err.Error()
		}
		mash, sdk = append(mash, string(m)), append(sdk, d)
	}
	return mash, sdk
}

func requireCompatVerified(t *testing.T, mash, sdk []string) {
	t.Helper()
	for i := range mash {
		if mash[i] != string(sap.TrustVerified) || sdk[i] != mash[i] {
			t.Errorf("stored #%d: mash %q, SDK %q, want both verified", i+1, mash[i], sdk[i])
		}
	}
}

func TestCompatStoredLegacyInbox(t *testing.T) {
	f := newFixture(t, "a", "b", "c")
	b, c := f.client(t, "b"), f.client(t, "c")
	ctx := context.Background()
	if _, err := c.SendWith(ctx, tdot("b"), "the original", SendOptions{}); err != nil {
		t.Fatal(err)
	}
	for _, o := range []struct {
		body string
		opts SendOptions
	}{
		{"plain", SendOptions{}},
		{"a question", SendOptions{Kind: kind.Question}},
		{"urgent", SendOptions{Urgent: true}},
		{"answers", SendOptions{InReplyTo: 7}},
		{"relay", SendOptions{Via: 1}},
		{"all marks", SendOptions{Kind: kind.Task, Urgent: true, InReplyTo: 4, Via: 1}},
		{"héllo \"quoted\" <tag> & 日本", SendOptions{}},
	} {
		if _, err := b.SendWith(ctx, tdot("a"), o.body, o.opts); err != nil {
			t.Fatal(err)
		}
	}
	mash, sdk := compatStoredLabels(t, f, sap.InboxStream(taddr("a").Flat()), 7)
	requireCompatVerified(t, mash, sdk)
}

func TestCompatStoredNewFormInbox(t *testing.T) {
	f := newFixture(t)
	rcv := f.provision(t, "team", "rcv")
	snd := f.provision(t, "team", "snd")
	s := f.clientAt(t, snd)
	ctx := context.Background()
	for _, o := range []struct {
		body string
		opts SendOptions
	}{
		{"plain", SendOptions{}},
		{"a fyi", SendOptions{Kind: kind.FYI}},
		{"urgent", SendOptions{Urgent: true}},
		{"answers", SendOptions{InReplyTo: 3}},
	} {
		if _, err := s.SendWith(ctx, rcv.Flat(), o.body, o.opts); err != nil {
			t.Fatal(err)
		}
	}
	mash, sdk := compatStoredLabels(t, f, sap.InboxStream(rcv.Flat()), 4)
	requireCompatVerified(t, mash, sdk)
}

// Forged and malformed mail is never shown (ADR 0019). They are published raw to a's inbox between two real mails and
// read back through the endpoint's own classification (toMail, which ReadInbox uses to build every listed row): the
// forged and the malformed message are dropped, the two real ones are listed, and neither text is in what is listed.
func TestCompatForgedAndMalformedAreDropped(t *testing.T) {
	f := newFixture(t, "a", "b")
	ctx := context.Background()
	b := f.client(t, "b")
	subj := mustSubj(taddr("a")).Mail
	nc := f.e.ConnAs(t, f.creds["b"], tkey("b"))
	id := identity(t, f.creds["b"])

	if _, err := b.Send(ctx, tdot("a"), "hello verified one", 0); err != nil {
		t.Fatal(err)
	}
	h, _ := id.Sign(subj, []byte("hello forged"), tkey("b"), time.Now(), "")
	h.Sig = strings.Repeat("A", len(h.Sig))
	publishRaw(t, nc, subj, h.Encode(), "hello forged")
	publishRaw(t, nc, subj, "not json", "hello malformed")
	if _, err := b.Send(ctx, tdot("a"), "hello verified two", 0); err != nil {
		t.Fatal(err)
	}

	a := f.client(t, "a")
	for seq, want := range map[uint64]sap.Trust{2: sap.TrustForged, 3: sap.TrustMalformed} {
		raw, err := mustStream(t, a).GetMsg(ctx, seq)
		if err != nil {
			t.Fatal(err)
		}
		if m, ok := a.toMail(raw); ok || m.Trust != want {
			t.Errorf("#%d: shown=%v trust %q, want dropped as %q", seq, ok, m.Trust, want)
		}
	}
	r, err := a.ReadInbox(ctx, InboxQuery{Limit: 20})
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Mail) != 2 || r.Mail[0].Seq != 1 || r.Mail[1].Seq != 4 {
		t.Fatalf("listed: %+v", r.Mail)
	}
	for _, m := range r.Mail {
		if m.Trust != sap.TrustVerified || strings.Contains(string(m.Body), "forged") || strings.Contains(string(m.Body), "malformed") {
			t.Errorf("listed #%d: %q %q", m.Seq, m.Trust, m.Body)
		}
	}
}

func mustStream(t *testing.T, e *Endpoint) jetstream.Stream {
	t.Helper()
	s, err := e.inbox(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	return s
}
