package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"

	"github.com/bogworks/mash/internal/admin"
	"github.com/bogworks/mash/internal/cliframe"
	"github.com/bogworks/mash/internal/sap"
	"github.com/bogworks/mash/internal/store"
	"github.com/bogworks/mash/internal/testenv"
)

var updateInspectGolden = flag.Bool("update-inspect", false, "rewrite cmd/mashctl/testdata/endpoint_inbox.golden.json")

func TestEndpointInboxArgs(t *testing.T) {
	type want struct {
		addr  string
		json  bool
		after uint64
		set   bool
		limit int
	}
	ok := []struct {
		args []string
		want want
	}{
		{[]string{"a.b.c"}, want{addr: "a.b.c"}},
		{[]string{"a.b.c", "--json", "--after", "3", "--limit", "5"}, want{addr: "a.b.c", json: true, after: 3, set: true, limit: 5}},
		{[]string{"a.b.c", "--after=0"}, want{addr: "a.b.c", set: true}},
		{[]string{"--limit=2", "x.y.z"}, want{addr: "x.y.z", limit: 2}},
	}
	for _, c := range ok {
		got, err := parseEpInput(endpointInboxSpec, c.args)
		if err != nil || (want{got.addr.String(), got.json, got.after, got.afterSet, got.limit}) != c.want {
			t.Errorf("%v: %+v %v, want %+v", c.args, got, err, c.want)
		}
	}
	for _, bad := range [][]string{nil, {"--json"}, {"a.b.c", "d.e.f"}, {"a.b.c", "--after"}, {"a.b.c", "--after", "x"}, {"a.b.c", "--after", "-1"}, {"a.b.c", "--from", "2"}, {"a.b.c", "--to", "2"}, {"a.b.c", "--limit", "0"},
		{"a.b.c", "--limit", "-1"}, {"a.b.c", "--frob"}, {"a.b.c", "--json=1"}, {"a.b.c", "--legacy"}, {"a_b_c"}, {"a/b/c"}, {"a.b"}} {
		_, err := parseEpInput(endpointInboxSpec, bad)
		var ce *cliframe.Error
		if err == nil || !errors.As(err, &ce) || ce.Code != 2 {
			t.Errorf("%v: %v, want a usage error", bad, err)
		}
	}
}

// The top-level inbox verb is gone; endpoint inbox is the one form, and its usage errors exit 2.
func TestInboxUsageAndOldVerb(t *testing.T) {
	f := newAuditEnv(t)
	for _, args := range [][]string{{"inbox"}, {"inbox", "inspect", "o.g.a"}} {
		if out, _, code := ctlRun(t, f.e, args...); code != 2 || out != "" {
			t.Errorf("%v: %d %q, want exit 2 and no output (an unknown command)", args, code, out)
		}
	}
	for _, args := range [][]string{{"endpoint", "inbox"}, {"endpoint", "inbox", "o.g.a", "--limit", "0"}, {"endpoint", "inbox", "o.g.a", "--bogus"}} {
		if out, errb, code := ctlRun(t, f.e, args...); code != 2 || out != "" || !strings.HasPrefix(errb, "error: ") || !strings.Contains(errb, "hint: mashctl endpoint inbox --help") {
			t.Errorf("%v: %d %q %q", args, code, out, errb)
		}
	}
}

// TestEndpointInboxJSONGolden pins the bytes of the JSON with a fixed inspection.
func TestEndpointInboxJSONGolden(t *testing.T) {
	in := &admin.InboxInspection{
		Stream:   admin.InspectedStream{Name: "inbox-acme_team1_alpha", FirstSeq: 1, LastSeq: 4, Messages: 4, Bytes: 812},
		Consumer: &admin.InspectedConsumer{Name: "deliver", AckFloor: admin.Cursor{Stream: 1, Consumer: 1}, Delivered: admin.Cursor{Stream: 3, Consumer: 3}, NumPending: 1, NumRedelivered: 0, NumAckPending: 2},
	}
	ts := time.Date(2026, 11, 12, 10, 0, 0, 0, time.UTC)
	for i, tr := range []string{"verified", "claimed", "forged", "unknown-signer"} {
		seq := uint64(i + 1)
		in.Messages = append(in.Messages, admin.InspectedMessage{Seq: seq, MsgID: fmt.Sprintf("id%d", seq), Sender: "acme_team1_bob", Trust: tr,
			StoredAt: ts.Add(time.Duration(i) * time.Minute), Size: 10 * (i + 1), Kind: "message", State: admin.DeliveryState(seq, in.Consumer)})
	}
	var buf strings.Builder
	writeJSON(&buf, buildInboxJSON(sap.Address{Owner: "acme", Group: "team1", Instance: "alpha"}, in))
	got := buf.String()
	golden := filepath.Join("testdata", "endpoint_inbox.golden.json")
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
		t.Fatalf("endpoint inbox --json differs from %s (run with -update-inspect after checking the diff)\n got: %s\nwant: %s", golden, got, want)
	}
	// no consumer: every message is not delivered; the messages array is never null
	empty := buildInboxJSON(sap.Address{Owner: "o", Group: "g", Instance: "a"}, &admin.InboxInspection{Stream: admin.InspectedStream{Name: "inbox-o_g_a"}})
	eb, _ := json.Marshal(empty)
	for _, w := range []string{`"messages":[]`, `"address":"o.g.a"`, `"total":0`, `"undelivered":0`} {
		if !strings.Contains(string(eb), w) {
			t.Errorf("%s lacks %s", eb, w)
		}
	}
	for _, banned := range []string{"text", "body", "payload", "msg_id"} {
		if strings.Contains(got, `"`+banned+`"`) {
			t.Errorf("the JSON carries %q", banned)
		}
	}
}

// TestEndpointInboxTable pins the text table: the TRUST column, malformed shown as forged, and refused mail.
func TestEndpointInboxTable(t *testing.T) {
	addr := sap.Address{Owner: "acme", Group: "demo", Instance: "bob"}
	now := time.Date(2026, 11, 19, 12, 0, 0, 0, time.UTC)
	in := &admin.InboxInspection{
		Stream:   admin.InspectedStream{Name: "inbox-acme_demo_bob", Messages: 3},
		Consumer: &admin.InspectedConsumer{Name: "deliver", Delivered: admin.Cursor{Stream: 3, Consumer: 3}, NumPending: 0},
		Messages: []admin.InspectedMessage{
			{Seq: 1, Sender: "acme_demo_alice", Trust: "verified", StoredAt: now.Add(-2 * time.Minute), Size: 48, Kind: "task", State: admin.DeliveryState(1, nil)},
			{Seq: 2, Sender: "acme_lab_mallet", Trust: "forged", StoredAt: now.Add(-time.Minute), Size: 61, Kind: "task", State: admin.DeliveryState(2, nil)},
			{Seq: 3, Sender: "acme_lab_mallet", Trust: "malformed", StoredAt: now.Add(-time.Minute), Size: 5, Kind: "task", State: admin.DeliveryState(3, nil)},
			{Seq: 4, Sender: "acme_demo_alice", Trust: "", StoredAt: now.Add(-time.Minute), Size: 5, Kind: "task", State: admin.DeliveryState(4, in3())},
		},
	}
	rep := buildInboxJSON(addr, in)
	var buf strings.Builder
	if err := writeInboxTable(&buf, addr, rep.Messages, in.Messages, now); err != nil {
		t.Fatal(err)
	}
	want := "\n" +
		"NUMBER  FROM             WHEN    TRUST     DELIVERED  KIND  SIZE\n" +
		"1       alice            2m ago  verified  no         task  48 B\n" +
		"2       acme.lab.mallet  1m ago  forged    refused    task  61 B\n" +
		"3       acme.lab.mallet  1m ago  forged    refused    task  5 B\n" +
		"4       alice            1m ago  -         yes        task  5 B\n"
	if buf.String() != want {
		t.Errorf("table:\n%s\nwant:\n%s", buf.String(), want)
	}
	if rep.Messages[1].Trust != "forged" || rep.Messages[1].Delivered || rep.Messages[2].Trust != "forged" {
		t.Errorf("json rows: %+v", rep.Messages)
	}
}

// in3 is a consumer that delivered through message 4.
func in3() *admin.InspectedConsumer {
	return &admin.InspectedConsumer{Name: "deliver", Delivered: admin.Cursor{Stream: 4, Consumer: 4}}
}

func TestInboxWordsAndSizes(t *testing.T) {
	now := time.Date(2026, 11, 19, 12, 0, 0, 0, time.UTC)
	for d, want := range map[time.Duration]string{-time.Second: "0s ago", 40 * time.Second: "40s ago", 2 * time.Minute: "2m ago", 3 * time.Hour: "3h ago", 72 * time.Hour: "3d ago"} {
		if got := ago(now, now.Add(-d)); got != want {
			t.Errorf("ago(%v) = %q, want %q", d, got, want)
		}
	}
	for n, want := range map[int]string{0: "0 B", 48: "48 B", 1536: "1.5 KiB", 3 << 20: "3.0 MiB"} {
		if got := byteSize(n); got != want {
			t.Errorf("byteSize(%d) = %q, want %q", n, got, want)
		}
	}
	if senderAddress("acme_demo_alice") != "acme.demo.alice" || senderAddress("someone") != "someone" || senderAddress("?") != "?" {
		t.Error("senderAddress")
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

// inspectFixture: endpoint o.g.box holding six messages of every trust class; the deliver consumer has fetched
// four and acknowledged 1, 2 and 4 (so the ack floor is 2, and 3 and 4 are delivered above it).
// sndKey is the directory key the sender o.g.snd signs its messages with.
var sndKey = ad("snd").Flat()

func inspectFixture(t *testing.T) (*auditFixture, string) {
	f := newAuditEnv(t)
	f.endpoint(t, ad("box"))
	subject := ad("box").Subjects().Mail
	sndCreds, sndUser := f.e.Mint(t, ad("snd"))
	f.provision(t, ad("snd"), sndUser)
	snd := identityOf(t, sndCreds)
	ghostCreds, _ := f.e.Mint(t, ad("ghostsender")) // signs, but is not in the directory
	ghost := identityOf(t, ghostCreds)
	now := time.Now()

	p := func(s string) []byte { return []byte(s) }
	sendSigned(t, f.js, subject, p("one"), signedHdr(t, snd, subject, p("one"), sndKey, now, "msg-1", "message"))        // 1 verified
	sendSigned(t, f.js, subject, p("two!"), signedHdr(t, ghost, subject, p("two!"), sndKey, now, "msg-2", "reply"))      // 2 unknown-signer (name snd, other key)
	sendSigned(t, f.js, subject, p("three"), signedHdr(t, snd, subject, p("tampered"), sndKey, now, "msg-3", "message")) // 3 forged
	claimed := nats.Header{}
	claimed.Set(sap.HeaderName, `{"v":1,"account":"`+snd.Account+`","user":"`+snd.User+`","name":"someone"}`)
	claimed.Set(jetstream.MsgIDHeader, "msg-4")
	claimed.Set("Mash-Kind", "message")
	sendSigned(t, f.js, subject, p("four"), claimed)                                                                                // 4 claimed
	sendSigned(t, f.js, subject, p("five"), signedHdr(t, snd, subject, p("five"), sndKey, now.Add(-time.Hour), "msg-5", "message")) // 5 stale
	sendSigned(t, f.js, subject, p("six"), nats.Header{"Nats-Msg-Id": {"msg-6"}})                                                   // 6 no sender header

	c, err := f.js.Consumer(context.Background(), ad("box").InboxStream(), store.Consumer)
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
	return f, ad("box").InboxStream()
}

// inboxRows is the numbers in the first column of the table of `endpoint inbox`, in order.
func inboxRows(out string) []string {
	var nums []string
	row := regexp.MustCompile(`^(\d+)\s+\S+\s+\d+[smhd] ago\s`)
	for _, l := range strings.Split(out, "\n") {
		if m := row.FindStringSubmatch(l); m != nil {
			nums = append(nums, m[1])
		}
	}
	return nums
}

func TestEndpointInboxLive(t *testing.T) {
	f, stream := inspectFixture(t)
	before := snapInbox(t, f.js, stream)
	rec := record(t, f.nc)
	out, errb, code := ctlRun(t, f.e, "endpoint", "inbox", "o.g.box", "--json")
	subjects := rec.stop(t)
	if code != 0 {
		t.Fatalf("exit %d: %q", code, errb)
	}
	onlyReads(t, subjects)

	var r struct {
		Address     string `json:"address"`
		Total       int    `json:"total"`
		Undelivered int    `json:"undelivered"`
		Messages    []struct {
			Number    uint64 `json:"number"`
			From      string `json:"from"`
			Delivered bool   `json:"delivered"`
			Kind      string `json:"kind"`
			Size      int    `json:"size"`
		} `json:"messages"`
	}
	if err := json.Unmarshal([]byte(out), &r); err != nil {
		t.Fatalf("%v: %s", err, out)
	}
	if r.Address != "o.g.box" || r.Total != 6 || r.Undelivered != 2 || len(r.Messages) != 6 {
		t.Fatalf("header: %+v", r)
	}
	wantDelivered := []bool{true, true, false, true, false, false} // 3 is the tampered message: refused
	wantSize := []int{3, 4, 5, 4, 4, 3}
	wantKind := []string{"message", "reply", "message", "message", "message", ""}
	wantFrom := []string{"o.g.snd", "o.g.snd", "o.g.snd", "someone", "o.g.snd", "?"}
	for i, m := range r.Messages {
		if m.Number != uint64(i+1) || m.Delivered != wantDelivered[i] || m.Size != wantSize[i] || m.Kind != wantKind[i] || m.From != wantFrom[i] {
			t.Errorf("message %d: %+v", i+1, m)
		}
	}
	for _, banned := range []string{"one", "two!", "tampered", "three", "five", "six"} { // no message text
		if strings.Contains(out, `"`+banned+`"`) {
			t.Errorf("the output carries message text %q", banned)
		}
	}
	// nothing changed
	if after := snapInbox(t, f.js, stream); !reflect.DeepEqual(before, after) {
		t.Fatalf("inbox changed the inbox:\nbefore %+v\nafter  %+v", before, after)
	}

	// text form
	inboxNow = func() time.Time { return time.Now().Add(2 * time.Minute) }
	t.Cleanup(func() { inboxNow = time.Now })
	txt, errb, code := ctlRun(t, f.e, "endpoint", "inbox", "o.g.box")
	if code != 0 || errb != "" || !strings.HasPrefix(txt, "6 messages for o.g.box, 2 not delivered.\n\nNUMBER  FROM") ||
		strings.Join(inboxRows(txt), ",") != "1,2,3,4,5,6" || !strings.Contains(txt, "2m ago") {
		t.Errorf("text: %d %q\n%s", code, errb, txt)
	}
	if lines := strings.Split(txt, "\n"); !strings.HasPrefix(lines[3], "1       snd   ") || !strings.Contains(lines[3], "yes") || !strings.Contains(lines[3], " 3 B") {
		t.Errorf("first row %q: the sender is the name in the endpoint's own owner and group", lines[3])
	}
	for _, banned := range []string{"tampered", "msg-1", "three"} {
		if strings.Contains(txt, banned) {
			t.Errorf("the text carries %q", banned)
		}
	}

	if !strings.Contains(txt, "TRUST") || !strings.Contains(txt, "verified") || !strings.Contains(txt, "refused") {
		t.Errorf("the text lacks the TRUST column or the refused state:\n%s", txt)
	}

	// after and limit
	txt, _, code = ctlRun(t, f.e, "endpoint", "inbox", "o.g.box", "--after", "2", "--limit", "3")
	if code != 0 || strings.Join(inboxRows(txt), ",") != "3,4,5" {
		t.Errorf("--after 2 --limit 3: %d\n%s", code, txt)
	}
	for _, flag := range []string{"--from", "--to"} {
		if o, e, code := ctlRun(t, f.e, "endpoint", "inbox", "o.g.box", flag, "2"); code != 2 || o != "" || !strings.HasPrefix(e, "error: unknown ") {
			t.Errorf("%s: %d %q %q", flag, code, o, e)
		}
	}
	if o, e, code := ctlRun(t, f.e, "endpoint", "inbox", "o.g.box", "--after", "x"); code != 2 || o != "" || !strings.HasPrefix(e, "error: --after must be a whole number\nhint: mashctl endpoint inbox --help") {
		t.Errorf("--after x: %d %q %q", code, o, e)
	}
	out, _, _ = ctlRun(t, f.e, "endpoint", "inbox", "o.g.box", "--json", "--limit", "2")
	var r2 struct {
		Total    int `json:"total"`
		Messages []struct {
			Number uint64 `json:"number"`
		} `json:"messages"`
	}
	_ = json.Unmarshal([]byte(out), &r2)
	if r2.Total != 6 || len(r2.Messages) != 2 || r2.Messages[0].Number != 5 || r2.Messages[1].Number != 6 {
		t.Errorf("--limit 2 is the last two, oldest first, and total stays the stream's: %+v", r2)
	}
	if after := snapInbox(t, f.js, stream); !reflect.DeepEqual(before, after) {
		t.Fatalf("later runs changed the inbox")
	}
}

// What the CLI leaves out is still read: the sender's trust, the message id and the consumer's cursors, through the
// admin call the command uses.
func TestInspectInboxCallKeepsTrustAndCursors(t *testing.T) {
	f, stream := inspectFixture(t)
	adm, err := admin.New(f.e.Conn(t, f.e.AdminCreds), f.e.Owner)
	if err != nil {
		t.Fatal(err)
	}
	in, err := adm.InspectInbox(context.Background(), ad("box"), admin.InspectOptions{})
	if err != nil {
		t.Fatal(err)
	}
	wantTrust := []string{"verified", "unknown-signer", "forged", "claimed", "stale", "absent"}
	wantState := []string{admin.StateAcked, admin.StateAcked, admin.StateAckUnsure, admin.StateAckUnsure, admin.StateUndelivered, admin.StateUndelivered}
	if len(in.Messages) != 6 {
		t.Fatalf("%d messages", len(in.Messages))
	}
	for i, m := range in.Messages {
		if m.Seq != uint64(i+1) || m.Trust != wantTrust[i] || m.State != wantState[i] || m.MsgID != fmt.Sprintf("msg-%d", i+1) {
			t.Errorf("message %d: %+v, want trust %s state %s", i+1, m, wantTrust[i], wantState[i])
		}
		if m.StoredAt.IsZero() {
			t.Errorf("message %d has no store time", i+1)
		}
	}
	c := in.Consumer
	if c == nil || c.Name != "deliver" || c.NumAckPending != 1 || c.NumPending != 2 || c.NumRedelivered != 0 || c.AckFloor.Stream != 2 || c.Delivered.Stream != 4 || c.Delivered.Consumer != 4 {
		t.Errorf("consumer: %+v", c)
	}
	if in.Stream.Name != stream || in.Stream.FirstSeq != 1 || in.Stream.LastSeq != 6 || in.Stream.Messages != 6 || in.Stream.Bytes == 0 {
		t.Errorf("stream: %+v", in.Stream)
	}
}

func TestEndpointInboxNoConsumerAndRefusals(t *testing.T) {
	f := newAuditEnv(t)
	f.endpoint(t, ad("plain"))
	f.publish(t, ad("plain").Subjects().Mail, 2)
	if err := f.js.DeleteConsumer(context.Background(), ad("plain").InboxStream(), store.Consumer); err != nil {
		t.Fatal(err)
	}
	out, errb, code := ctlRun(t, f.e, "endpoint", "inbox", "o.g.plain", "--json")
	var r struct {
		Total, Undelivered int
		Messages           []struct{ Delivered bool }
	}
	if code != 0 || json.Unmarshal([]byte(out), &r) != nil || r.Total != 2 || r.Undelivered != 2 || len(r.Messages) != 2 || r.Messages[0].Delivered || r.Messages[1].Delivered {
		t.Fatalf("no consumer: %d %q %q", code, out, errb)
	}
	if txt, _, code := ctlRun(t, f.e, "endpoint", "inbox", "o.g.plain"); code != 0 || !strings.HasPrefix(txt, "2 messages for o.g.plain, 2 not delivered.\n") {
		t.Errorf("text without consumer: %d %q", code, txt)
	}
	for _, addr := range []string{"o.g.nobody", "no.such.endpoint"} {
		if out, errb, code := ctlRun(t, f.e, "endpoint", "inbox", addr); code != 1 || out != "" || !strings.HasPrefix(errb, "error: ") {
			t.Errorf("%q: %d %q %q, want exit 1 and a message", addr, code, out, errb)
		}
	}
	// not an address at all, or not the dotted form: usage, with the form to give
	for _, addr := range []string{"nobody", "has.two", "Bad Name", "o.g", "o_g_plain", "o/g/plain", "claude_o_nobody"} {
		if out, errb, code := ctlRun(t, f.e, "endpoint", "inbox", addr); code != 2 || out != "" || !strings.Contains(errb, "write owner.group.name") {
			t.Errorf("%q: %d %q %q, want exit 2 and the form to give", addr, code, out, errb)
		}
	}
	// a record without a stream is refused, not a connection failure
	if err := f.js.DeleteStream(context.Background(), ad("plain").InboxStream()); err != nil {
		t.Fatal(err)
	}
	if _, errb, code := ctlRun(t, f.e, "endpoint", "inbox", "o.g.plain"); code != 1 || !strings.Contains(errb, "no inbox stream") {
		t.Errorf("no stream: %d %q", code, errb)
	}
}

func TestEndpointInboxAddressesAreDistinct(t *testing.T) {
	f := newAuditEnv(t)
	ops := sap.Address{Owner: testenv.Owner, Group: "team1", Instance: "ops1"}
	f.endpoint(t, ops)
	f.publish(t, ops.Subjects().Mail, 2)
	f.endpoint(t, ad("ops1")) // the same instance token in another group is another endpoint
	f.publish(t, ad("ops1").Subjects().Mail, 1)

	count := func(addr string) (string, int) {
		out, errb, code := ctlRun(t, f.e, "endpoint", "inbox", addr, "--json")
		if code != 0 {
			return errb, code
		}
		var r struct {
			Address  string
			Messages []any
		}
		if err := json.Unmarshal([]byte(out), &r); err != nil {
			t.Fatal(err)
		}
		return fmt.Sprintf("%s/%d", r.Address, len(r.Messages)), code
	}
	for _, c := range []struct{ addr, want string }{{"o.team1.ops1", "o.team1.ops1/2"}, {"o.g.ops1", "o.g.ops1/1"}} {
		if got, code := count(c.addr); code != 0 || got != c.want {
			t.Errorf("%v: %q (%d), want %s", c.addr, got, code, c.want)
		}
	}
	// an address in another group or owner does not name the endpoint
	if _, code := count("o.team9.ops1"); code != 1 {
		t.Errorf("wrong group: exit %d", code)
	}
	if _, code := count("other.team1.ops1"); code != 1 {
		t.Errorf("wrong owner: exit %d", code)
	}
}

func TestEndpointInboxCannotComplete(t *testing.T) {
	f := newAuditEnv(t)
	env := map[string]string{"HOME": t.TempDir(), "MASH_SERVER": "nats://127.0.0.1:1", "MASH_ENDPOINT": f.e.Owner + ".dev." + "x", "MASH_ADMIN_CREDS": f.e.AdminCreds, "MASH_INSECURE_PLAINTEXT": "1"}
	var out, errb strings.Builder
	if code := runEnv([]string{"endpoint", "inbox", "o.g.x"}, func(k string) string { return env[k] }, &out, &errb); code != 1 || out.String() != "" || !strings.HasPrefix(errb.String(), "error: ") {
		t.Fatalf("exit %d, want 1: %q %q", code, out.String(), errb.String())
	}
}
