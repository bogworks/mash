package endpoint

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/nats-io/nats.go"

	"github.com/bogworks/mash/internal/config"
	"github.com/bogworks/mash/internal/kind"
	"github.com/bogworks/mash/internal/render"
	"github.com/bogworks/mash/internal/sap"
)

// rawMark publishes a message from "from" to "to" by hand, so a test can set any mark header. mutate may change the
// signed header before it is encoded and the headers after.
func rawMark(t *testing.T, f *fixture, from, to, body string, mutate func(id sap.Identity, h *sap.SenderHeader, m *nats.Msg)) {
	t.Helper()
	nc := f.e.ConnAs(t, f.creds[from], from)
	id := identity(t, f.creds[from])
	subj := sap.For(f.e.Owner, to).Mail
	h, err := id.Sign(subj, []byte(body), from, time.Now(), "")
	if err != nil {
		t.Fatal(err)
	}
	m := nats.NewMsg(subj)
	m.Data = []byte(body)
	mutate(id, &h, m)
	m.Header.Set(sap.HeaderName, h.Encode())
	if _, err := nc.RequestMsg(m, 2*time.Second); err != nil {
		t.Fatal(err)
	}
}

func TestReplyToIsSignedAndShown(t *testing.T) {
	f := newFixture(t, "a", "b")
	a, b := f.client(t, "a"), f.client(t, "b")
	ctx := context.Background()
	if _, err := b.SendWith(ctx, "a", "plain", SendOptions{}); err != nil {
		t.Fatal(err)
	}
	if _, err := b.SendWith(ctx, "a", "answer", SendOptions{InReplyTo: 7}); err != nil {
		t.Fatal(err)
	}
	m1, _ := a.Read(ctx, 1)
	m2, err := a.Read(ctx, 2)
	if err != nil || m1.ReplyTo != 0 || m2.ReplyTo != 7 || m2.Via != "" || m2.Trust != sap.TrustVerified {
		t.Fatalf("%+v / %+v %v", m1, m2, err)
	}
	if got := render.One(m2); !strings.Contains(got, "\nanswers #7\n") || strings.Contains(render.One(m1), "answers") {
		t.Fatalf("render %q", got)
	}
	// the wire value is the signed form
	raw := rawHeader(t, f, "a", 2, InReplyToHeader)
	if !strings.HasPrefix(raw, "7.") || len(raw) < 20 {
		t.Fatalf("wire value %q", raw)
	}
}

// rawHeader reads a header of message seq in name's inbox.
func rawHeader(t *testing.T, f *fixture, name string, seq uint64, header string) string {
	t.Helper()
	ep := f.client(t, name)
	s, err := ep.inbox(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	raw, err := s.GetMsg(context.Background(), seq)
	if err != nil {
		t.Fatal(err)
	}
	return raw.Header.Get(header)
}

func TestReplyToUnsignedOrForgedIsIgnored(t *testing.T) {
	f := newFixture(t, "a", "b")
	a := f.client(t, "a")
	ctx := context.Background()
	other := func(t *testing.T) sap.Identity { return identity(t, f.creds["a"]) } // another endpoint's key
	rawMark(t, f, "b", "a", "old", func(_ sap.Identity, _ *sap.SenderHeader, m *nats.Msg) {
		m.Header.Set(InReplyToHeader, "3") // the old bare decimal
	})
	rawMark(t, f, "b", "a", "forged", func(_ sap.Identity, h *sap.SenderHeader, m *nats.Msg) {
		v, _ := other(t).SignInReplyTo(*h, 3) // signed by a different key
		m.Header.Set(InReplyToHeader, v)
	})
	rawMark(t, f, "b", "a", "moved", func(id sap.Identity, h *sap.SenderHeader, m *nats.Msg) {
		h2 := *h
		h2.Nonce = "another-nonce"
		v, _ := id.SignInReplyTo(h2, 3) // signed for another message
		m.Header.Set(InReplyToHeader, v)
	})
	rawMark(t, f, "b", "a", "seq", func(id sap.Identity, h *sap.SenderHeader, m *nats.Msg) {
		v, _ := id.SignInReplyTo(*h, 3)
		m.Header.Set(InReplyToHeader, "4"+v[1:]) // another seq under the signature
	})
	rawMark(t, f, "b", "a", "junk", func(_ sap.Identity, _ *sap.SenderHeader, m *nats.Msg) {
		m.Header.Set(InReplyToHeader, "x.y")
		m.Header.Set(ViaHeader, "nonsense")
	})
	for seq := uint64(1); seq <= 5; seq++ {
		m, err := a.Read(ctx, seq)
		if err != nil || m.ReplyTo != 0 || m.Via != "" || m.Trust != sap.TrustVerified || len(m.Body) == 0 {
			t.Fatalf("#%d: %+v %v", seq, m, err)
		}
		if out := render.One(m); strings.Contains(out, "answers") || strings.Contains(out, "relayed") {
			t.Fatalf("#%d shows a mark: %s", seq, out)
		}
	}
}

func TestViaIsDeclaredAndShown(t *testing.T) {
	f := newFixture(t, "a", "b", "c")
	a, b, c := f.client(t, "a"), f.client(t, "b"), f.client(t, "c")
	ctx := context.Background()
	if _, err := c.SendWith(ctx, "b", "the original", SendOptions{}); err != nil {
		t.Fatal(err)
	}
	if _, err := b.SendWith(ctx, "a", "passing it on", SendOptions{Via: 1}); err != nil {
		t.Fatal(err)
	}
	m, err := a.Read(ctx, 1)
	if err != nil || m.Via != "c #1" || m.ReplyTo != 0 || m.Trust != sap.TrustVerified || m.From != "b" {
		t.Fatalf("%+v %v", m, err)
	}
	if out := render.One(m); !strings.Contains(out, "\nrelayed from c #1\n") {
		t.Fatalf("render %q", out)
	}
	// via and in_reply_to together
	if _, err := b.SendWith(ctx, "a", "both", SendOptions{Via: 1, InReplyTo: 9}); err != nil {
		t.Fatal(err)
	}
	m, _ = a.Read(ctx, 2)
	if m.Via != "c #1" || m.ReplyTo != 9 {
		t.Fatalf("both: %+v", m)
	}
	if out := render.One(m); !strings.Contains(out, "answers #9\nrelayed from c #1\n") {
		t.Fatalf("render %q", out)
	}
	// the wire form is origin#seq.sig
	if raw := rawHeader(t, f, "a", 1, ViaHeader); !strings.HasPrefix(raw, "c#1.") {
		t.Fatalf("wire value %q", raw)
	}
}

func TestViaToAMissingMessageErrorsWithoutSending(t *testing.T) {
	f := newFixture(t, "a", "b")
	a, b := f.client(t, "a"), f.client(t, "b")
	_, err := b.SendWith(context.Background(), "a", "x", SendOptions{Via: 5})
	if err == nil || err.Error() != "mash: no message #5 in your inbox to relay" {
		t.Fatalf("err %v", err)
	}
	if _, err := a.Read(context.Background(), 1); err == nil {
		t.Fatal("a refused send was delivered")
	}
}

// Relaying a message whose sender is claimed (so unverified) is still declared; a sender name that is not an endpoint
// name is refused.
func TestViaOfUnverifiedMail(t *testing.T) {
	f := newFixture(t, "a", "b", "c")
	a, b := f.client(t, "a"), f.client(t, "b")
	ctx := context.Background()
	rawMark(t, f, "c", "b", "claimed", func(_ sap.Identity, h *sap.SenderHeader, _ *nats.Msg) { h.Sig = "" })
	rawMark(t, f, "c", "b", "badname", func(id sap.Identity, h *sap.SenderHeader, _ *nats.Msg) {
		h.Name = "Not A Name"
		h.Sig = ""
	})
	if _, err := b.SendWith(ctx, "a", "relay", SendOptions{Via: 1}); err != nil {
		t.Fatal(err)
	}
	if m, _ := a.Read(ctx, 1); m.Via != "c #1" {
		t.Fatalf("%+v", m)
	}
	// b's #2 is the message with the unusable sender name
	if _, err := b.SendWith(ctx, "a", "relay 2", SendOptions{Via: 2}); err == nil {
		t.Fatal("relayed a message with an unusable sender name")
	}
}

// A mark from a claimed (unsigned) sender is not shown, however well the marks themselves are signed.
func TestMarksOfAClaimedSenderAreNotShown(t *testing.T) {
	f := newFixture(t, "a", "b")
	a := f.client(t, "a")
	rawMark(t, f, "b", "a", "claimed", func(id sap.Identity, h *sap.SenderHeader, m *nats.Msg) {
		r, _ := id.SignInReplyTo(*h, 3)
		v, _ := id.SignVia(*h, "c", 2)
		m.Header.Set(InReplyToHeader, r)
		m.Header.Set(ViaHeader, v)
		h.Sig = ""
	})
	m, err := a.Read(context.Background(), 1)
	if err != nil || m.Trust != sap.TrustClaimed || m.ReplyTo != 0 || m.Via != "" {
		t.Fatalf("%+v %v", m, err)
	}
}

// A mark never changes the trust label, the kind or the urgent handling, and wakes nothing by itself.
func TestMarksChangeNeitherTrustKindNorWake(t *testing.T) {
	f, ev, sp, b := kindFixtureSpool(t)
	f.add(t, "c")
	ctx := context.Background()
	if _, err := f.client(t, "c").SendWith(ctx, "b", "orig", SendOptions{Kind: kind.FYI}); err != nil {
		t.Fatal(err)
	}
	if _, err := b.SendWith(ctx, "a", "fyi with marks", SendOptions{Kind: kind.FYI, InReplyTo: 4, Via: 1}); err != nil {
		t.Fatal(err)
	}
	until(t, "spooled", func() bool { return len(waiting(t, sp)) == 1 })
	e := waiting(t, sp)[0]
	got := MailOf(e)
	if got.ReplyTo != 4 || got.Via != "c #1" || got.Trust != sap.TrustVerified || got.Urgent || got.Kind != kind.FYI {
		t.Fatalf("%+v", got)
	}
	time.Sleep(4 * f.idle)
	if all := ev.all(); len(all) != 0 {
		t.Fatalf("marks woke the session: %q", all)
	}
	// and on a task, with urgent, the handling is the ordinary one
	if _, err := b.SendWith(ctx, "a", "urgent with marks", SendOptions{Urgent: true, InReplyTo: 4, Via: 1}); err != nil {
		t.Fatal(err)
	}
	if w := ev.waitFor(t, 1, 3*time.Second); !strings.HasPrefix(w[0], "urgent, 2 messages from b") { // the held fyi is covered by the same wake
		t.Fatalf("wake %q", w[0])
	}
}

// The origin syntax in sap matches config.ValidName (sap cannot import config in a test).
func TestViaOriginSyntaxMatchesConfig(t *testing.T) {
	id := identity(t, newFixture(t, "a").creds["a"])
	h, err := id.Sign("agents.mail.cc.o.a", []byte("x"), "a", time.Now(), "")
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range []string{"a", "a-b", "a--b", "-a", "a-", "A", "1a", "a1", strings.Repeat("a", 48), strings.Repeat("a", 49), "", "a_b", "a b"} {
		_, err := id.SignVia(h, s, 1)
		if (err == nil) != config.ValidName(s) {
			t.Fatalf("%q: sap accepts %v, config %v", s, err == nil, config.ValidName(s))
		}
	}
}
