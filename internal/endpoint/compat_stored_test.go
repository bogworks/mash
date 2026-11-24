package endpoint

import (
	"context"
	"testing"

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
