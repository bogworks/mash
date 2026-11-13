package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"

	"github.com/bogworks/mash/internal/admin"
	"github.com/bogworks/mash/internal/sap"
	"github.com/bogworks/mash/internal/store"
)

var updateInspectGolden = flag.Bool("update-inspect", false, "rewrite cmd/mashctl/testdata/inbox_inspect.golden.json")

func TestInboxInspectArgs(t *testing.T) {
	ok := []struct {
		args []string
		want inboxInspectOpts
	}{
		{[]string{"a"}, inboxInspectOpts{address: "a"}},
		{[]string{"a", "--json", "--from", "3", "--to=9", "--limit", "5", "--legacy"}, inboxInspectOpts{address: "a", json: true, legacy: true, from: 3, to: 9, limit: 5}},
		{[]string{"--limit=2", "x.y.z"}, inboxInspectOpts{address: "x.y.z", limit: 2}},
	}
	for _, c := range ok {
		got, err := parseInboxInspectArgs(c.args)
		if err != nil || got != c.want {
			t.Errorf("%v: %+v %v, want %+v", c.args, got, err, c.want)
		}
	}
	for _, bad := range [][]string{nil, {"--json"}, {"a", "b"}, {"a", "--from"}, {"a", "--from", "0"}, {"a", "--from", "x"}, {"a", "--limit", "0"},
		{"a", "--limit", "-1"}, {"a", "--from", "5", "--to", "4"}, {"a", "--frob"}, {"a", "--json=1"}} {
		if _, err := parseInboxInspectArgs(bad); err == nil {
			t.Errorf("%v accepted", bad)
		}
	}
}

func TestInboxUsageAndSubverb(t *testing.T) {
	f := newAuditEnv(t)
	for _, args := range [][]string{{"inbox"}, {"inbox", "frob"}, {"inbox", "inspect"}, {"inbox", "inspect", "a", "--limit", "0"}} {
		if out, errb, code := ctlRun(t, f.e, args...); code != 2 || out != "" || !strings.Contains(errb, "usage: mashctl inbox inspect <address> [--json] [--legacy] [--from SEQ] [--to SEQ] [--limit N]") {
			t.Errorf("%v: %d %q %q", args, code, out, errb)
		}
	}
}

// TestInboxInspectJSONGolden pins the bytes of the JSON contract with a fixed inspection.
func TestInboxInspectJSONGolden(t *testing.T) {
	in := &admin.InboxInspection{
		Stream:   admin.InspectedStream{Name: "inbox-alpha", FirstSeq: 1, LastSeq: 4, Messages: 4, Bytes: 812},
		Consumer: &admin.InspectedConsumer{Name: "deliver", AckFloor: admin.Cursor{Stream: 1, Consumer: 1}, Delivered: admin.Cursor{Stream: 3, Consumer: 3}, NumPending: 1, NumRedelivered: 0, NumAckPending: 2},
	}
	ts := time.Date(2026, 11, 12, 10, 0, 0, 0, time.UTC)
	for i, tr := range []string{"verified", "claimed", "forged", "unknown-signer"} {
		seq := uint64(i + 1)
		in.Messages = append(in.Messages, admin.InspectedMessage{Seq: seq, MsgID: fmt.Sprintf("id%d", seq), Sender: "bob", Trust: tr,
			StoredAt: ts.Add(time.Duration(i) * time.Minute), Size: 10 * (i + 1), Kind: "message", State: admin.DeliveryState(seq, in.Consumer)})
	}
	b, err := json.MarshalIndent(buildInspectReport("alpha", in), "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	got := string(b) + "\n"
	golden := filepath.Join("testdata", "inbox_inspect.golden.json")
	if *updateInspectGolden {
		if err := os.WriteFile(golden, []byte(got), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(golden)
	if err != nil {
		t.Fatal(err)
	}
	if got != string(want) {
		t.Fatalf("inbox inspect --json differs from %s (run with -update-inspect after checking the diff)\n got: %s\nwant: %s", golden, got, want)
	}
	// no consumer: null, not absent; the messages array is never null
	empty := buildInspectReport("a", &admin.InboxInspection{Stream: admin.InspectedStream{Name: "inbox-a"}})
	eb, _ := json.Marshal(empty)
	for _, w := range []string{`"consumer":null`, `"messages":[]`, `"schema":"mash-inbox-inspect@1"`} {
		if !strings.Contains(string(eb), w) {
			t.Errorf("%s lacks %s", eb, w)
		}
	}
}

const apiAdvisory = "$JS.EVENT.ADVISORY.API"

// recorder sees every subject published in the account by other connections.
type recorder struct {
	mu   sync.Mutex
	subj []string
	sub  *nats.Subscription
	nc   *nats.Conn
}

func record(t *testing.T, nc *nats.Conn) *recorder {
	t.Helper()
	r := &recorder{nc: nc}
	sub, err := nc.Subscribe(">", func(m *nats.Msg) {
		r.mu.Lock()
		defer r.mu.Unlock()
		r.subj = append(r.subj, m.Subject)
		if m.Subject == apiAdvisory { // the server's record of an API request: the request's own subject is what it did
			var adv struct {
				Subject string `json:"subject"`
			}
			if json.Unmarshal(m.Data, &adv) == nil && adv.Subject != "" {
				r.subj = append(r.subj, adv.Subject)
			}
		}
	})
	if err != nil {
		t.Fatal(err)
	}
	r.sub = sub
	if err := nc.Flush(); err != nil {
		t.Fatal(err)
	}
	return r
}

func (r *recorder) stop(t *testing.T) []string {
	t.Helper()
	_ = r.nc.Flush()
	time.Sleep(300 * time.Millisecond)
	_ = r.sub.Unsubscribe()
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string{}, r.subj...)
}

// onlyReads fails on any published subject that is not one of the read APIs the inspection is allowed.
func onlyReads(t *testing.T, subjects []string) {
	t.Helper()
	var info, get, cinfo int
	for _, s := range subjects {
		switch {
		case strings.HasPrefix(s, "_INBOX."), s == apiAdvisory:
		case strings.HasPrefix(s, "$JS.API.STREAM.INFO."):
			info++
		case strings.HasPrefix(s, "$JS.API.DIRECT.GET.") || strings.HasPrefix(s, "$JS.API.STREAM.MSG.GET."):
			get++
		case strings.HasPrefix(s, "$JS.API.CONSUMER.INFO.") && strings.HasSuffix(s, "."+store.Consumer):
			cinfo++
		default:
			t.Errorf("the inspection published %q: only STREAM.INFO, DIRECT.GET and CONSUMER.INFO of deliver are allowed", s)
		}
	}
	if info == 0 || get == 0 || cinfo == 0 {
		t.Errorf("the recorder saw info=%d get=%d consumer_info=%d: it is not recording", info, get, cinfo)
	}
}

type inboxSnap struct {
	State                                     jetstream.StreamState
	Config                                    jetstream.StreamConfig
	CFG                                       jetstream.ConsumerConfig
	Delivered, AckFloor                       jetstream.SequenceInfo
	NumAckPending, NumRedelivered, NumWaiting int
	NumPending                                uint64
	Consumers                                 int
}

func snapInbox(t *testing.T, js jetstream.JetStream, stream string) inboxSnap {
	t.Helper()
	ctx := context.Background()
	s, err := js.Stream(ctx, stream)
	if err != nil {
		t.Fatal(err)
	}
	si, err := s.Info(ctx)
	if err != nil {
		t.Fatal(err)
	}
	c, err := s.Consumer(ctx, store.Consumer)
	if err != nil {
		t.Fatal(err)
	}
	ci, err := c.Info(ctx)
	if err != nil {
		t.Fatal(err)
	}
	st := si.State
	st.LastTime = time.Time{}
	return inboxSnap{State: st, Config: si.Config, CFG: ci.Config, Delivered: ci.Delivered, AckFloor: ci.AckFloor,
		NumAckPending: ci.NumAckPending, NumRedelivered: ci.NumRedelivered, NumWaiting: ci.NumWaiting, NumPending: ci.NumPending, Consumers: si.State.Consumers}
}

func sendSigned(t *testing.T, js jetstream.JetStream, subject string, payload []byte, hdr nats.Header) {
	t.Helper()
	m := &nats.Msg{Subject: subject, Data: payload, Header: hdr}
	if _, err := js.PublishMsg(context.Background(), m); err != nil {
		t.Fatalf("publish: %v", err)
	}
}

func identityOf(t *testing.T, creds string) sap.Identity {
	t.Helper()
	raw, err := os.ReadFile(creds)
	if err != nil {
		t.Fatal(err)
	}
	id, err := sap.IdentityFromCredsAt(creds, raw)
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func signedHdr(t *testing.T, id sap.Identity, subject string, payload []byte, name string, at time.Time, msgID, kind string) nats.Header {
	t.Helper()
	h, err := id.Sign(subject, payload, name, at, msgID)
	if err != nil {
		t.Fatal(err)
	}
	hdr := nats.Header{}
	hdr.Set(sap.HeaderName, h.Encode())
	hdr.Set(jetstream.MsgIDHeader, msgID)
	hdr.Set("Mash-Kind", kind)
	return hdr
}

// inspectFixture: legacy endpoint "box" holding six messages of every trust class; the deliver consumer has fetched
// four and acknowledged 1, 2 and 4 (so the ack floor is 2, and 3 and 4 are delivered above it).
func inspectFixture(t *testing.T) (*auditFixture, string) {
	f := newAuditEnv(t)
	f.legacy(t, "box")
	subject := sap.For(f.e.Owner, "box").Mail
	sndCreds, sndUser := f.e.Mint(t, "snd")
	if _, errb, code := ctlRun(t, f.e, "provision", "snd", "--user", sndUser, "--account", f.e.Account); code != 0 {
		t.Fatalf("provision snd: %d %q", code, errb)
	}
	snd := identityOf(t, sndCreds)
	ghostCreds, _ := f.e.Mint(t, "ghostsender") // signs, but is not in the directory
	ghost := identityOf(t, ghostCreds)
	now := time.Now()

	p := func(s string) []byte { return []byte(s) }
	sendSigned(t, f.js, subject, p("one"), signedHdr(t, snd, subject, p("one"), "snd", now, "msg-1", "message"))        // 1 verified
	sendSigned(t, f.js, subject, p("two!"), signedHdr(t, ghost, subject, p("two!"), "snd", now, "msg-2", "reply"))      // 2 unknown-signer (name snd, other key)
	sendSigned(t, f.js, subject, p("three"), signedHdr(t, snd, subject, p("tampered"), "snd", now, "msg-3", "message")) // 3 forged
	claimed := nats.Header{}
	claimed.Set(sap.HeaderName, `{"v":1,"account":"`+snd.Account+`","user":"`+snd.User+`","name":"someone"}`)
	claimed.Set(jetstream.MsgIDHeader, "msg-4")
	claimed.Set("Mash-Kind", "message")
	sendSigned(t, f.js, subject, p("four"), claimed)                                                                               // 4 claimed
	sendSigned(t, f.js, subject, p("five"), signedHdr(t, snd, subject, p("five"), "snd", now.Add(-time.Hour), "msg-5", "message")) // 5 stale
	sendSigned(t, f.js, subject, p("six"), nats.Header{"Nats-Msg-Id": {"msg-6"}})                                                  // 6 no sender header

	c, err := f.js.Consumer(context.Background(), sap.InboxStream("box"), store.Consumer)
	if err != nil {
		t.Fatal(err)
	}
	b, err := c.Fetch(4, jetstream.FetchMaxWait(5*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	for m := range b.Messages() {
		md, _ := m.Metadata()
		if md.Sequence.Stream == 3 {
			continue // left unacknowledged
		}
		if err := m.DoubleAck(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
	return f, sap.InboxStream("box")
}

type inspectMsgOut struct {
	Seq           uint64  `json:"seq"`
	MsgID         string  `json:"msg_id"`
	Sender        string  `json:"sender"`
	Trust         string  `json:"trust"`
	StoredAt      string  `json:"stored_at"`
	Size          int     `json:"size"`
	Kind          string  `json:"kind"`
	State         string  `json:"state"`
	DeliveryCount *int    `json:"delivery_count"`
	AckTime       *string `json:"ack_time"`
}

type inspectOut struct {
	Schema   string          `json:"schema"`
	Address  string          `json:"address"`
	Stream   map[string]any  `json:"stream"`
	Consumer map[string]any  `json:"consumer"`
	Messages []inspectMsgOut `json:"messages"`
	Limits   []string        `json:"limits"`
}

func TestInboxInspectLive(t *testing.T) {
	f, stream := inspectFixture(t)
	before := snapInbox(t, f.js, stream)
	rec := record(t, f.nc)
	out, errb, code := ctlRun(t, f.e, "inbox", "inspect", "box", "--json")
	subjects := rec.stop(t)
	if code != 0 {
		t.Fatalf("exit %d: %q", code, errb)
	}
	onlyReads(t, subjects)

	var r inspectOut
	if err := json.Unmarshal([]byte(out), &r); err != nil {
		t.Fatalf("%v: %s", err, out)
	}
	if r.Schema != "mash-inbox-inspect@1" || r.Address != "box" || len(r.Limits) == 0 || !strings.Contains(r.Limits[0], "num_redelivered") {
		t.Fatalf("header: %+v", r)
	}
	wantTrust := []string{"verified", "unknown-signer", "forged", "claimed", "stale", "absent"}
	wantState := []string{"acked", "acked", admin.StateAckUnsure, admin.StateAckUnsure, admin.StateUndelivered, admin.StateUndelivered}
	wantSize := []int{3, 4, 5, 4, 4, 3}
	if len(r.Messages) != 6 {
		t.Fatalf("%d messages: %s", len(r.Messages), out)
	}
	for i, m := range r.Messages {
		if m.Seq != uint64(i+1) || m.Trust != wantTrust[i] || m.State != wantState[i] || m.Size != wantSize[i] || m.MsgID != fmt.Sprintf("msg-%d", i+1) {
			t.Errorf("message %d: %+v, want trust %s state %s size %d", i+1, m, wantTrust[i], wantState[i], wantSize[i])
		}
		if m.DeliveryCount != nil || m.AckTime != nil {
			t.Errorf("message %d: delivery_count and ack_time must be null: %+v", i+1, m)
		}
		if _, err := time.Parse(time.RFC3339, m.StoredAt); err != nil || !strings.HasSuffix(m.StoredAt, "Z") {
			t.Errorf("stored_at %q", m.StoredAt)
		}
	}
	if r.Messages[0].Sender != "snd" || r.Messages[0].Kind != "message" || r.Messages[1].Kind != "reply" || r.Messages[3].Sender != "someone" || r.Messages[5].Sender != "?" {
		t.Errorf("senders or kinds: %+v", r.Messages)
	}
	c := r.Consumer
	if c == nil || c["name"] != "deliver" || c["num_ack_pending"].(float64) != 1 || c["num_pending"].(float64) != 2 || c["num_redelivered"].(float64) != 0 ||
		c["ack_floor"].(map[string]any)["stream_seq"].(float64) != 2 || c["delivered"].(map[string]any)["stream_seq"].(float64) != 4 ||
		c["delivered"].(map[string]any)["consumer_seq"].(float64) != 4 {
		t.Errorf("consumer: %+v", c)
	}
	if r.Stream["name"] != stream || r.Stream["first_seq"].(float64) != 1 || r.Stream["last_seq"].(float64) != 6 || r.Stream["messages"].(float64) != 6 || r.Stream["bytes"].(float64) <= 0 {
		t.Errorf("stream: %+v", r.Stream)
	}

	// nothing changed
	if after := snapInbox(t, f.js, stream); !reflect.DeepEqual(before, after) {
		t.Fatalf("inspect changed the inbox:\nbefore %+v\nafter  %+v", before, after)
	}

	// text form, range and limit
	txt, _, code := ctlRun(t, f.e, "inbox", "inspect", "box", "--from", "2", "--to", "9", "--limit", "3")
	if code != 0 || !strings.Contains(txt, "delivered, ack not confirmed") || !strings.Contains(txt, "msg-2") || !strings.Contains(txt, "msg-4") ||
		strings.Contains(txt, "msg-1 ") || strings.Contains(txt, "msg-5") || !strings.Contains(txt, "note: per-message delivery count and ack time are not kept by JetStream or mash") ||
		!strings.Contains(txt, "ack_floor stream 2 consumer 2") {
		t.Errorf("text: %d\n%s", code, txt)
	}
	out, _, _ = ctlRun(t, f.e, "inbox", "inspect", "box", "--json", "--limit", "2")
	var r2 inspectOut
	_ = json.Unmarshal([]byte(out), &r2)
	if len(r2.Messages) != 2 || r2.Messages[0].Seq != 5 || r2.Messages[1].Seq != 6 {
		t.Errorf("default window is the last N, oldest first: %+v", r2.Messages)
	}
	if after := snapInbox(t, f.js, stream); !reflect.DeepEqual(before, after) {
		t.Fatalf("later runs changed the inbox")
	}
}

func TestInboxInspectNoConsumerAndRefusals(t *testing.T) {
	f := newAuditEnv(t)
	f.legacy(t, "plain")
	f.publish(t, sap.For(f.e.Owner, "plain").Mail, 2)
	if err := f.js.DeleteConsumer(context.Background(), sap.InboxStream("plain"), store.Consumer); err != nil {
		t.Fatal(err)
	}
	out, errb, code := ctlRun(t, f.e, "inbox", "inspect", "plain", "--json")
	var r inspectOut
	if code != 0 || json.Unmarshal([]byte(out), &r) != nil || r.Consumer != nil || len(r.Messages) != 2 || r.Messages[0].State != "undelivered" || r.Messages[1].State != "undelivered" {
		t.Fatalf("no consumer: %d %q %q", code, out, errb)
	}
	if !strings.Contains(out, `"consumer": null`) {
		t.Errorf("consumer is not null: %s", out)
	}
	if txt, _, code := ctlRun(t, f.e, "inbox", "inspect", "plain"); code != 0 || !strings.Contains(txt, "consumer: none") {
		t.Errorf("text without consumer: %d %q", code, txt)
	}
	for _, addr := range []string{"nobody", "no.such.endpoint", "claude_o_nobody", "has.two", "Bad Name"} {
		if out, errb, code := ctlRun(t, f.e, "inbox", "inspect", addr); code != 1 || out != "" || errb == "" {
			t.Errorf("%q: %d %q %q, want exit 1 and a message", addr, code, out, errb)
		}
	}
	// a record without a stream is refused, not a connection failure
	if err := f.js.DeleteStream(context.Background(), sap.InboxStream("plain")); err != nil {
		t.Fatal(err)
	}
	if _, errb, code := ctlRun(t, f.e, "inbox", "inspect", "plain"); code != 1 || !strings.Contains(errb, "no inbox stream") {
		t.Errorf("no stream: %d %q", code, errb)
	}
	// --legacy on a record with no alias
	f.legacy(t, "other")
	if _, errb, code := ctlRun(t, f.e, "inbox", "inspect", "other", "--legacy"); code != 1 || !strings.Contains(errb, "no alias") {
		t.Errorf("--legacy without alias: %d %q", code, errb)
	}
}

func TestInboxInspectAddressForms(t *testing.T) {
	f := newAuditEnv(t)
	f.legacy(t, "ops1")
	f.publish(t, sap.For(f.e.Owner, "ops1").Mail, 2)
	_, u := f.e.Mint(t, "ops1new")
	ops := f.newForm(t, "acme", "team1", "ops1", u, &store.Alias{Owner: f.e.Owner, Name: "ops1"})
	f.publish(t, ops.Subjects().Mail, 1)

	stream := func(args ...string) (string, int) {
		out, errb, code := ctlRun(t, f.e, append([]string{"inbox", "inspect", "--json"}, args...)...)
		var r inspectOut
		if code != 0 {
			return errb, code
		}
		if err := json.Unmarshal([]byte(out), &r); err != nil {
			t.Fatal(err)
		}
		return fmt.Sprintf("%v/%d", r.Stream["name"], len(r.Messages)), code
	}
	for _, c := range []struct {
		args []string
		want string
	}{
		{[]string{"acme_team1_ops1"}, "inbox-acme_team1_ops1/1"},
		{[]string{"acme.team1.ops1"}, "inbox-acme_team1_ops1/1"},
		{[]string{"ops1"}, "inbox-ops1/2"},
		{[]string{"cc." + f.e.Owner + ".ops1"}, "inbox-ops1/2"},
		{[]string{"acme.team1.ops1", "--legacy"}, "inbox-ops1/2"}, // the alias's legacy stream
		{[]string{"acme_team1_ops1", "--legacy"}, "inbox-ops1/2"},
	} {
		if got, code := stream(c.args...); code != 0 || got != c.want {
			t.Errorf("%v: %q (%d), want %s", c.args, got, code, c.want)
		}
	}
	// a dotted address with another owner does not name the endpoint
	if _, code := stream("acme.team9.ops1"); code != 1 {
		t.Errorf("wrong owner: exit %d", code)
	}
	if _, code := stream("cc.other.ops1"); code != 1 {
		t.Errorf("wrong legacy owner: exit %d", code)
	}
}

func TestInboxInspectCannotComplete(t *testing.T) {
	f := newAuditEnv(t)
	env := map[string]string{"HOME": t.TempDir(), "MASH_URL": "nats://127.0.0.1:1", "MASH_OWNER": f.e.Owner, "MASH_ADMIN_CREDS": f.e.AdminCreds, "MASH_INSECURE_LOOPBACK": "1"}
	var out, errb strings.Builder
	if code := runEnv([]string{"inbox", "inspect", "x"}, func(k string) string { return env[k] }, &out, &errb); code != exitInspectIncomplete {
		t.Fatalf("exit %d, want 3: %q", code, errb.String())
	}
}
