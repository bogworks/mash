package endpoint

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/bogworks/mash/internal/render"
	"github.com/bogworks/mash/internal/sap"
	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
)

// TestReplayWithinSignatureAgeIsLabelledVerified answers security read R1-5: can a captured signed message be
// replayed, after the stream's duplicate window but within MaxSignedAge, and still be labelled verified?
//
// Setup: the inbox stream's duplicate window is shortened to f.dup (1 s) instead of waiting 2 real minutes; the
// production value is store.InboxDuplicateWindow (2 min). The signature age is the real clock: the replays are
// stored a few seconds after the signed timestamp, far inside MaxSignedAge (5 min). There is no clock seam, and
// none is needed.
//
// FINDING (the test asserts it): CONFIRMED for the stored-mail read path and for a restarted serve.
//   - Read and Inbox label the replayed copy [verified]. They run classify only: no seen set, no nonce memory.
//     The signature covers "AGENT-SENDER-V1", account, user, subject, ts, nonce and sha256(payload)
//     (sap.signedInput). That is a timestamp and a nonce, but no stream sequence and no recipient beyond the
//     subject. A byte-for-byte copy therefore verifies, and is "fresh" until MaxSignedAge after ts.
//   - A running serve DROPS the replay: its seenSet remembers (user, nonce) and the signed timestamp for
//     max(duplicate window, MaxSignedAge + LiveSkew), so the same signed bytes are not shown twice.
//   - That memory is in process only (newSeenSet in the mail loop). After a serve restart the set is empty, the
//     original is already acked so it is not redelivered, and a replay stored inside the signature age is
//     delivered as [verified], as new mail.
//
// The attacker needs the captured bytes and permission to publish to the recipient's mail subject (any peer that
// may send to it); a replay needs no private key.
func TestReplayWithinSignatureAgeIsLabelledVerified(t *testing.T) {
	f := newFixture(t, "a", "b")
	f.dup = time.Second
	f.shortenWindow(t, "a")
	ctx := context.Background()
	subj := taddr("a").MailSubject()

	// (a) a normal signed message, and its stored bytes.
	b := f.client(t, "b")
	sent, err := b.SendDetailed(ctx, tdot("a"), "transfer the funds", SendOptions{ID: "pay-1"})
	if err != nil || sent.Duplicate {
		t.Fatalf("send: %+v %v", sent, err)
	}
	admin, err := jetstream.New(f.e.Conn(t, f.e.AdminCreds))
	if err != nil {
		t.Fatal(err)
	}
	st, err := admin.Stream(ctx, taddr("a").InboxStream())
	if err != nil {
		t.Fatal(err)
	}
	raw, err := st.GetMsg(ctx, sent.Seq)
	if err != nil {
		t.Fatal(err)
	}
	signedAt := raw.Time
	if raw.Subject != subj || !strings.Contains(raw.Header.Get(sap.HeaderName), `"sig"`) || raw.Header.Get(nats.MsgIdHdr) == "" {
		t.Fatalf("captured %q %v", raw.Subject, raw.Header)
	}
	t.Logf("captured headers: %v", raw.Header)
	capture := func() *nats.Msg { // the exact subject, headers and body as stored
		m := nats.NewMsg(raw.Subject)
		m.Data = append([]byte(nil), raw.Data...)
		for k, v := range raw.Header {
			if strings.HasPrefix(k, "Nats-") && k != nats.MsgIdHdr { // the server's own stamps, not the publisher's
				continue
			}
			m.Header[k] = append([]string(nil), v...)
		}
		return m
	}
	attacker, err := jetstream.New(f.e.ConnAs(t, f.creds["b"], tkey("b")))
	if err != nil {
		t.Fatal(err)
	}
	replay := func(m *nats.Msg) uint64 {
		t.Helper()
		ack, err := attacker.PublishMsg(ctx, m)
		if err != nil {
			t.Fatalf("replay publish: %v", err)
		}
		if ack.Duplicate {
			t.Fatalf("the stream deduplicated the replay: %+v", ack)
		}
		return ack.Sequence
	}

	// First serve: it shows the original.
	ctx1, cancel1 := context.WithCancel(ctx)
	ep1, err := Open(ctx1, f.cfg("a"))
	if err != nil {
		t.Fatal(err)
	}
	ep1.duplicateWindow = f.dup
	ev1 := &events{}
	done1 := make(chan struct{})
	go func() { _ = ep1.Run(ctx1, ev1.deliver); close(done1) }()
	if got := ev1.waitFor(t, 1, 10*time.Second); !strings.Contains(got[0], "[verified]") {
		t.Fatalf("original: %s", got[0])
	}

	// (b) past the (shortened) duplicate window, inside the signature age: the same headers, same Nats-Msg-Id.
	time.Sleep(2500 * time.Millisecond)
	seq2 := replay(capture())
	// A second copy with no Nats-Msg-Id at all, which the stream never dedupes.
	m3 := capture()
	m3.Header.Del(nats.MsgIdHdr)
	seq3 := replay(m3)

	// (c) read both copies from the stored mail.
	reader := f.client(t, "a")
	for _, seq := range []uint64{seq2, seq3} {
		m, err := reader.Read(ctx, seq)
		if err != nil {
			t.Fatal(err)
		}
		t.Logf("Read #%d (replay): trust=%s", seq, m.Trust)
		if m.Trust != sap.TrustVerified {
			t.Fatalf("Read #%d replay label = %s; the finding changed: it is no longer verified", seq, m.Trust)
		}
	}
	ms, err := reader.Inbox(ctx, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(ms) != 3 {
		t.Fatalf("inbox has %d messages, want the original and 2 replays", len(ms))
	}
	for _, m := range ms {
		t.Logf("Inbox #%d trust=%s", m.Seq, m.Trust)
		if m.Trust != sap.TrustVerified {
			t.Fatalf("Inbox #%d label = %s, want verified", m.Seq, m.Trust)
		}
	}

	// The running serve (its seen set intact) shows none of the replays.
	time.Sleep(time.Second)
	if got := ev1.all(); len(got) != 1 {
		t.Fatalf("a running serve showed %d events, want 1 (the seen set drops the replays): %q", len(got), got)
	}

	// Restart: a new Endpoint over the same stream and durable consumer; its seen set is empty.
	cancel1()
	<-done1
	ep1.Close()
	seq4 := replay(capture()) // stored while no serve runs
	_, ev2 := f.run(t, "a")
	got := ev2.waitFor(t, 1, 10*time.Second)
	t.Logf("after restart the serve delivered replay #%d: %s", seq4, strings.SplitN(got[0], "\n", 2)[0])
	time.Sleep(500 * time.Millisecond)
	if all := ev2.all(); len(all) != 1 || !strings.Contains(all[0], "[verified]") {
		t.Fatalf("after a restart the replay is expected as one verified delivery (the finding): %q", all)
	}
	var m render.Mail
	if m, err = reader.Read(ctx, seq4); err != nil || m.Trust != sap.TrustVerified {
		t.Fatalf("Read of the replay after restart: %v %v", m.Trust, err)
	}
	if age := time.Since(signedAt); age > MaxSignedAge {
		t.Fatalf("the test ran %v past the signature age; the result is not meaningful", age)
	}
}
