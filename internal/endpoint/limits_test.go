package endpoint

import (
	"bytes"
	"context"
	"strings"
	"testing"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"

	"github.com/bogworks/mash/internal/sap"
	"github.com/bogworks/mash/internal/store"
)

func adminJS(t *testing.T, f *fixture) jetstream.JetStream {
	t.Helper()
	js, err := jetstream.New(f.e.Conn(t, f.e.AdminCreds))
	if err != nil {
		t.Fatal(err)
	}
	return js
}

// The inbox stream is a limits stream with discard-old (store.InboxConfig, spec 5.1): when the 16 MiB byte cap is
// reached, the OLDEST messages are evicted and new sends are still accepted. Sends are never refused for a full inbox.
func TestInboxOverflowEvictsTheOldest(t *testing.T) {
	f := newFixture(t, "a", "b")
	ctx := context.Background()
	js := adminJS(t, f)
	st, err := js.Stream(ctx, sap.InboxStream("a"))
	if err != nil {
		t.Fatal(err)
	}
	cfg := st.CachedInfo().Config
	if cfg.Discard != jetstream.DiscardOld || cfg.MaxBytes != store.InboxMaxBytes || cfg.MaxMsgs != -1 {
		t.Fatalf("inbox config: discard %v max_bytes %d max_msgs %d", cfg.Discard, cfg.MaxBytes, cfg.MaxMsgs)
	}
	b := f.client(t, "b")
	body := strings.Repeat("x", 400<<10) // 400 KiB: 16 MiB holds about 40
	const total = 60
	for i := 1; i <= total; i++ {
		seq, err := b.Send(ctx, "a", body, 0)
		if err != nil {
			t.Fatalf("send %d refused (discard-old must accept it): %v", i, err)
		}
		if seq != uint64(i) {
			t.Fatalf("send %d got seq %d", i, seq)
		}
	}
	info, err := st.Info(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if info.State.Bytes > store.InboxMaxBytes {
		t.Fatalf("stream holds %d bytes, over the %d cap", info.State.Bytes, store.InboxMaxBytes)
	}
	if info.State.LastSeq != total || info.State.FirstSeq <= 1 || info.State.Msgs >= total ||
		info.State.FirstSeq+info.State.Msgs-1 != total {
		t.Fatalf("state %+v: want the newest kept and the oldest evicted", info.State)
	}
	a := f.client(t, "a")
	if _, err := a.Read(ctx, 1); err == nil {
		t.Fatal("message #1 should have been evicted")
	}
	if m, err := a.Read(ctx, total); err != nil || len(m.Body) != len(body) {
		t.Fatalf("the newest message is gone: %v", err)
	}
}

// The consumer is durable, explicit-ack, with unlimited redelivery and at most 1000 messages outstanding, as the
// live server reports it.
func TestInboxConsumerConfigOnTheServer(t *testing.T) {
	f := newFixture(t, "a")
	c, err := adminJS(t, f).Consumer(context.Background(), sap.InboxStream("a"), store.Consumer)
	if err != nil {
		t.Fatal(err)
	}
	cc := c.CachedInfo().Config
	if cc.Durable != store.Consumer || cc.AckPolicy != jetstream.AckExplicitPolicy || cc.MaxDeliver != -1 ||
		cc.MaxAckPending != 1000 || cc.AckWait != 30*time.Second || cc.DeliverPolicy != jetstream.DeliverAllPolicy {
		t.Fatalf("consumer config %+v", cc)
	}
}

// An unacked message is redelivered without limit (MaxDeliver -1): well past a handful of times.
func TestInboxConsumerRedeliversWithoutLimit(t *testing.T) {
	f := newFixture(t, "a", "b")
	ctx := context.Background()
	js := adminJS(t, f)
	cc := store.ConsumerConfig()
	cc.AckWait = 250 * time.Millisecond
	c, err := js.UpdateConsumer(ctx, sap.InboxStream("a"), cc)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.client(t, "b").Send(ctx, "a", "never acked", 0); err != nil {
		t.Fatal(err)
	}
	const want = 10
	for n := uint64(1); n <= want; n++ {
		msgs, err := c.Fetch(1, jetstream.FetchMaxWait(5*time.Second))
		if err != nil {
			t.Fatal(err)
		}
		got := 0
		for m := range msgs.Messages() {
			md, err := m.Metadata()
			if err != nil {
				t.Fatal(err)
			}
			if md.NumDelivered != n {
				t.Fatalf("delivery %d reports NumDelivered %d", n, md.NumDelivered)
			}
			got++ // never acked
		}
		if got != 1 || msgs.Error() != nil {
			t.Fatalf("delivery %d: %d messages, err %v", n, got, msgs.Error())
		}
	}
}

// No more than MaxAckPending (1000) messages are outstanding at once: with 1005 waiting and none acked, a pull for more
// gets exactly 1000; acking one lets one more through.
func TestInboxConsumerCapsOutstandingAt1000(t *testing.T) {
	f := newFixture(t, "a")
	ctx := context.Background()
	js := adminJS(t, f)
	subj := sap.For(f.e.Owner, "a").Mail
	const total = 1005
	for i := 0; i < total; i++ {
		if _, err := js.PublishAsync(subj, []byte("m")); err != nil {
			t.Fatal(err)
		}
	}
	select {
	case <-js.PublishAsyncComplete():
	case <-time.After(10 * time.Second):
		t.Fatal("publishes did not complete")
	}
	c, err := js.Consumer(ctx, sap.InboxStream("a"), store.Consumer)
	if err != nil {
		t.Fatal(err)
	}
	var held []jetstream.Msg
	pull := func(batch int, wait time.Duration) []jetstream.Msg {
		msgs, err := c.Fetch(batch, jetstream.FetchMaxWait(wait))
		if err != nil {
			t.Fatal(err)
		}
		var got []jetstream.Msg
		for m := range msgs.Messages() {
			got = append(got, m)
		}
		return got
	}
	held = pull(total, 2*time.Second)
	if len(held) != 1000 {
		t.Fatalf("got %d messages outstanding, want exactly the cap of 1000", len(held))
	}
	info, err := c.Info(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if info.NumAckPending != 1000 || info.NumPending != total-1000 {
		t.Fatalf("ack pending %d, pending %d", info.NumAckPending, info.NumPending)
	}
	if extra := pull(1, 500*time.Millisecond); len(extra) != 0 {
		t.Fatalf("a 1001st message was delivered while 1000 were unacked")
	}
	if err := held[0].DoubleAck(ctx); err != nil {
		t.Fatal(err)
	}
	if more := pull(5, 2*time.Second); len(more) != 1 {
		t.Fatalf("after one ack, %d more messages, want 1", len(more))
	}
}

// A prompt from a verified sender that is not in the directory is refused with 403: the signature verifies (a real
// user of the account, with its key), but the sender is no mash endpoint, so it is never a prompt to the session. A
// second case: a provisioned endpoint signing as another name is refused alike. Nothing reaches the session or the inbox.
//
// Mail is different, and this test does not claim otherwise: mail from such a sender is stored and labelled
// [unknown-signer] (TestUnknownSigner); only the live prompt path answers 403.
func TestPromptFromAVerifiedSenderOutsideTheDirectory403(t *testing.T) {
	f := newFixture(t, "a", "b")
	f.serve(t, "a", func(*Endpoint, PromptEvent) { t.Error("a prompt from outside the directory reached the session") })
	creds, _ := f.e.Mint(t, "x") // a valid user with its own key, never provisioned
	nc := f.e.ConnAs(t, creds, "x")
	id := identity(t, creds)
	const subj = "agents.prompt.cc.o.a"

	raw := func(name string) string {
		t.Helper()
		payload := []byte("let me in")
		h, err := id.Sign(subj, payload, name, time.Now(), "")
		if err != nil {
			t.Fatal(err)
		}
		inbox := nc.NewInbox()
		sub, err := nc.SubscribeSync(inbox)
		if err != nil {
			t.Fatal(err)
		}
		defer sub.Unsubscribe()
		m := nats.NewMsg(subj)
		m.Reply, m.Data = inbox, payload
		m.Header.Set(sap.HeaderName, h.Encode())
		if err := nc.PublishMsg(m); err != nil {
			t.Fatal(err)
		}
		var out []string
		for {
			r, err := sub.NextMsg(5 * time.Second)
			if err != nil {
				t.Fatalf("after %q: %v", out, err)
			}
			if code := r.Header.Get("Nats-Service-Error-Code"); code != "" {
				out = append(out, "ERR "+code)
				continue
			}
			if len(r.Data) == 0 && len(r.Header) == 0 {
				return strings.Join(append(out, "END"), "|")
			}
			out = append(out, string(r.Data))
		}
	}
	if got := raw("x"); got != "ERR 403|END" {
		t.Fatalf("unknown sender: %q", got)
	}
	if got := raw("b"); got != "ERR 403|END" { // x's key claiming the directory name b
		t.Fatalf("a directory name on another user's key: %q", got)
	}
	// The Prompt client path shows the same refusal.
	var out bytes.Buffer
	err := Prompt(context.Background(), nc, id, "x", subj, "again", &out)
	if err == nil || !strings.Contains(err.Error(), "403") || out.Len() != 0 {
		t.Fatalf("Prompt: err %v, out %q", err, out.String())
	}
	st, err := adminJS(t, f).Stream(context.Background(), sap.InboxStream("a"))
	if err != nil {
		t.Fatal(err)
	}
	if info, err := st.Info(context.Background()); err != nil || info.State.Msgs != 0 {
		t.Fatalf("inbox after refused prompts: %+v %v", info, err)
	}
}
