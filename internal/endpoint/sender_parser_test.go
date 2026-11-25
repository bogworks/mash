package endpoint

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"

	"github.com/bogworks/mash/internal/sap"
	"github.com/bogworks/mash/internal/sapsdk"
)

// A stored message with two Agent-Sender values is malformed to the SDK ("two sender headers"). viaOrigin must refuse
// it, and never declare the first value's name as the origin of a relay.
func TestViaOriginRefusesTwoSenderHeaders(t *testing.T) {
	f := newFixture(t, "a", "b")
	subj := taddr("a").Subjects().Mail
	nc := f.e.ConnAs(t, f.creds["b"], tkey("b"))
	id := identity(t, f.creds["b"])
	h, err := id.Sign(subj, []byte("two"), tkey("b"), time.Now(), "")
	if err != nil {
		t.Fatal(err)
	}
	m := nats.NewMsg(subj)
	m.Data = []byte("two")
	m.Header.Add(sap.HeaderName, h.Encode())
	m.Header.Add(sap.HeaderName, h.Encode())
	if _, err := nc.RequestMsg(m, 15*time.Second); err != nil {
		t.Fatal(err)
	}
	a := f.client(t, "a")
	s, err := a.inbox(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	raw, err := s.GetMsg(context.Background(), 1)
	if err != nil {
		t.Fatal(err)
	}
	if n := len(raw.Header.Values(sap.HeaderName)); n != 2 {
		t.Fatalf("stored message has %d Agent-Sender values, want 2", n)
	}
	if _, tr := sapsdk.VerifyStored(raw.Header.Values(sap.HeaderName), raw.Subject, raw.Data); tr != sap.TrustMalformed {
		t.Fatalf("SDK trust %v, want malformed", tr)
	}
	name, err := a.viaOrigin(context.Background(), 1)
	if err == nil || !strings.Contains(err.Error(), "names no sender that can be declared") || name != "" {
		t.Fatalf("viaOrigin = %q, %v; want the refusal and no name", name, err)
	}
}

// A signed header that spells "v":1.0 verifies under the SDK (the signature does not cover the spelling of v). A client
// that loads only the records it needs must still load that sender's record, so the mail is not refused as unknown-signer.
func TestLoadSendersReadsSenderOfV10Header(t *testing.T) {
	f := newFixture(t, "a", "b")
	subj := taddr("a").Subjects().Mail
	nc := f.e.ConnAs(t, f.creds["b"], tkey("b"))
	id := identity(t, f.creds["b"])
	h, err := id.Sign(subj, []byte("v one point oh"), tkey("b"), time.Now(), "")
	if err != nil {
		t.Fatal(err)
	}
	enc := h.Encode()
	spelled := strings.Replace(enc, `"v":1,`, `"v":1.0,`, 1)
	if spelled == enc {
		t.Fatalf("no v member to rewrite in %s", enc)
	}
	publishRaw(t, nc, subj, spelled, "v one point oh")

	c := f.leanClient(t, "a", ClientOptions{Inbox: true})
	s, err := c.inbox(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	raw, err := s.GetMsg(context.Background(), 1)
	if err != nil {
		t.Fatal(err)
	}
	if got := raw.Header.Get(sap.HeaderName); got != spelled {
		t.Fatalf("stored header %q, want %q", got, spelled)
	}
	sh, tr := sapsdk.VerifyStored(raw.Header.Values(sap.HeaderName), raw.Subject, raw.Data)
	if tr != sap.TrustVerified || sh.Name != tkey("b") {
		t.Fatalf("SDK trust %v name %q, want verified %q", tr, sh.Name, tkey("b"))
	}
	c.mu.RLock()
	_, had := c.dir[tkey("b")]
	c.mu.RUnlock()
	if had {
		t.Fatalf("the lean client already knows b's record; the test cannot tell a load from a prior one")
	}
	if err := c.loadSenders(context.Background(), []*jetstream.RawStreamMsg{raw}); err != nil {
		t.Fatal(err)
	}
	c.mu.RLock()
	_, ok := c.dir[tkey("b")]
	c.mu.RUnlock()
	if !ok {
		t.Fatalf("loadSenders did not load b's record for a v:1.0 header")
	}
	m, err := c.Read(context.Background(), 1)
	if err != nil || m.Trust != sap.TrustVerified {
		t.Fatalf("read: trust %v err %v, want verified", m.Trust, err)
	}
}
