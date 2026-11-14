package policyserve

import (
	"context"
	"encoding/base64"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/nats-io/jwt/v2"
	"github.com/nats-io/nkeys"

	"github.com/bogworks/mash/internal/admin"
	"github.com/bogworks/mash/internal/listset"
	"github.com/bogworks/mash/internal/policyapply"
	"github.com/bogworks/mash/internal/policydoc"
	"github.com/bogworks/mash/internal/policyintake"
	"github.com/bogworks/mash/internal/sap"
)

func TestParseVarz(t *testing.T) {
	good := `{"server":{"name":"s1","id":"X"},"data":{"host":"0.0.0.0","port":4222,"max_payload":1048576,"max_control_line":4096}}`
	info, ok, err := parseVarz([]byte(good))
	if err != nil || !ok {
		t.Fatalf("%v %v", ok, err)
	}
	if info.Name != "s1" || info.Host != "" || info.Port != 4222 ||
		info.Limits != (listset.ServerLimits{Server: "s1", MaxConnectLine: 4096, MaxAccountJWT: 1048576}) {
		t.Errorf("%+v", info)
	}
	info, _, _ = parseVarz([]byte(strings.Replace(good, "0.0.0.0", "10.0.0.7", 1)))
	if info.Host != "10.0.0.7" {
		t.Errorf("host %s", info.Host)
	}
	// A reply that carries an error, no data or no limits does not count as a server that reported its limits.
	for _, bad := range []string{
		`{"server":{"name":"s1"},"error":{"code":500}}`,
		`{"server":{"name":"s1"}}`,
		`{"server":{"name":"s1"},"data":{"host":"h","port":1,"max_payload":0,"max_control_line":0}}`,
		`{"data":{"host":"h","port":1,"max_payload":1,"max_control_line":1}}`,
		`not json`,
	} {
		if _, ok, err := parseVarz([]byte(bad)); ok || err != nil {
			t.Errorf("%s: ok %v err %v", bad, ok, err)
		}
	}
	long := `{"server":{"name":"` + strings.Repeat("a", maxServerNameBytes+1) + `"},"data":{"host":"h","port":1,"max_payload":1,"max_control_line":1}}`
	if _, _, err := parseVarz([]byte(long)); err == nil {
		t.Error("a server name over the cap was taken")
	}
}

func TestLimitsAndURLs(t *testing.T) {
	s := []ServerInfo{{Name: "a", URL: "nats://a:1", Limits: listset.ServerLimits{Server: "a", MaxConnectLine: 9}}, {Name: "b", URL: "nats://b:2"}}
	if l := Limits(s); len(l) != 2 || l[0].MaxConnectLine != 9 {
		t.Errorf("%+v", l)
	}
	if u := URLs(s); u["a"] != "nats://a:1" || u["b"] != "nats://b:2" {
		t.Errorf("%+v", u)
	}
}

func jws(t *testing.T, kp nkeys.KeyPair, payload string) []byte {
	t.Helper()
	enc := base64.RawURLEncoding
	in := enc.EncodeToString([]byte(`{"alg":"EdDSA","typ":"JWT"}`)) + "." + enc.EncodeToString([]byte(payload))
	sig, err := kp.Sign([]byte(in))
	if err != nil {
		t.Fatal(err)
	}
	return []byte(in + "." + enc.EncodeToString(sig))
}

func TestDecodeClassifiesErrors(t *testing.T) {
	kp, _ := nkeys.CreateAccount()
	pub, _ := kp.PublicKey()
	other, _ := nkeys.CreateAccount()
	payload := `{"schema":"` + policydoc.DocumentSchema + `","account":"A","range":"foo","generation":1,"base_generation":0,` +
		`"issued_at":"2026-11-14T12:00:00Z","groups":{},"senders":{}}`
	d, err := Decode(jws(t, kp, payload), pub)
	if err != nil || !d.Verified || d.Account != "A" || d.Range != "foo" || d.Generation != 1 || d.BaseGeneration != 0 {
		t.Fatalf("%+v %v", d, err)
	}
	if _, err := Decode(jws(t, other, payload), pub); !errors.Is(err, policyintake.ErrSignature) {
		t.Errorf("wrong key: %v", err)
	}
	if _, err := Decode([]byte(payload), pub); !errors.Is(err, policyintake.ErrSignature) {
		t.Errorf("unsigned: %v", err)
	}
	if _, err := Decode(jws(t, kp, `{"schema":"x"}`), pub); !errors.Is(err, policyintake.ErrDocument) {
		t.Errorf("malformed: %v", err)
	}
}

func TestInForce(t *testing.T) {
	l := policyapply.NewMemLedger(listset.Record{})
	if got, err := InForce(l)(context.Background()); err != nil || got != "" {
		t.Fatalf("nothing in force: %q %v", got, err)
	}
	rec, _, err := listset.Accept(listset.Record{}, listset.ListSet{Range: "foo"}, []byte("raw"))
	if err != nil {
		t.Fatal(err)
	}
	if err := l.CompareAndCommit(0, rec); err != nil {
		t.Fatal(err)
	}
	got, err := InForce(l)(context.Background())
	if err != nil || len(got) != 64 || got != strings.ToLower(got) {
		t.Fatalf("%q %v", got, err)
	}
	if got != rec.Received.String()[len("sha256:"):] {
		t.Errorf("%q is not the received digest %s", got, rec.Received)
	}
}

type refusalSink struct{ got []policyapply.Status }

func (r *refusalSink) WriteRefusal(_ context.Context, st policyapply.Status) error {
	r.got = append(r.got, st)
	return nil
}

func TestReporterWritesARefusal(t *testing.T) {
	sink := &refusalSink{}
	at := time.Date(2026, 11, 14, 12, 0, 0, 0, time.UTC)
	err := Reporter{Ledger: sink}.Refused(context.Background(), policyintake.Refusal{
		Account: "A", Generation: 4, Digest: strings.Repeat("ab", 32), Stage: policyintake.StageSignature, Detail: "bad", At: at,
	})
	if err != nil || len(sink.got) != 1 {
		t.Fatalf("%v %d", err, len(sink.got))
	}
	st := sink.got[0]
	if st.State != policyapply.StateRefused || st.Generation != 4 || len(st.Refusals) != 1 || st.Refusals[0].Stage != "signature" ||
		st.Refusals[0].Reason != "bad" || st.UpdatedAt != "2026-11-14T12:00:00Z" || !strings.HasPrefix(st.Refusals[0].Entry, "document sha256:abab") {
		t.Errorf("%+v", st)
	}
}

func TestHeldKeysCollectsEveryKeyMashHolds(t *testing.T) {
	held := func() (string, map[string]string, error) {
		return "A", map[string]string{"O": "op", "S": "sys", "A": "acct"}, nil
	}
	groups := func() (map[string]string, error) { return map[string]string{"foo.c": "G"}, nil }
	got, err := HeldKeys(held, groups, "U")()
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(got, ",") != "A,G,O,S,U" {
		t.Errorf("%v", got)
	}
	boom := errors.New("unreadable")
	if _, err := HeldKeys(func() (string, map[string]string, error) { return "", nil, boom }, groups, "U")(); !errors.Is(err, boom) {
		t.Errorf("held error: %v", err)
	}
	if _, err := HeldKeys(held, func() (map[string]string, error) { return nil, boom }, "U")(); !errors.Is(err, boom) {
		t.Errorf("groups error: %v", err)
	}
}

type fakeGuard struct{ seen []string }

func (g *fakeGuard) Before(_ context.Context, what string) error {
	g.seen = append(g.seen, what)
	if what == "commit" {
		return errors.New("fenced")
	}
	return nil
}

func TestBeforeWriteCallsTheGuard(t *testing.T) {
	g := &fakeGuard{}
	f := BeforeWrite(g)
	if err := f(context.Background(), "push"); err != nil {
		t.Fatal(err)
	}
	if err := f(context.Background(), "commit"); err == nil || len(g.seen) != 2 {
		t.Errorf("%v %v", err, g.seen)
	}
}

// fakeInboxes is an InboxChecker over a fixed answer per address; an address it does not know has no inbox.
type fakeInboxes struct {
	answers map[string]admin.InboxCheck
	errs    map[string]error
	asked   []string
}

func (f *fakeInboxes) CheckInbox(_ context.Context, id sap.EndpointIdentity) (admin.InboxCheck, error) {
	f.asked = append(f.asked, id.String())
	return f.answers[id.String()], f.errs[id.String()]
}

func TestCreateInboxOnlyChecksAndNamesWhatIsMissing(t *testing.T) {
	ctx := context.Background()
	inb := &fakeInboxes{answers: map[string]admin.InboxCheck{
		"foo.c.here":  {Stream: "inbox-here", Consumer: true, Legacy: true},
		"foo.c.nocon": {Stream: "inbox-nocon"},
	}, errs: map[string]error{"foo.c.bad": errors.New("mash: inbox stream of bad has subjects x where mash needs y")}}
	p := Provisioner{Inboxes: inb}
	if err := p.CreateInbox(ctx, "foo.c.here"); err != nil {
		t.Errorf("an address with an inbox: %v", err)
	}
	if err := p.CreateInbox(ctx, "foo.c.here"); err != nil {
		t.Errorf("not idempotent: %v", err)
	}
	if err := p.CreateInbox(ctx, "foo.c.gone"); err == nil || !strings.Contains(err.Error(), "foo.c.gone has no inbox stream") {
		t.Errorf("no stream: %v", err)
	}
	if err := p.CreateInbox(ctx, "foo.c.nocon"); err == nil || !strings.Contains(err.Error(), "consumer") {
		t.Errorf("no consumer: %v", err)
	}
	if err := p.CreateInbox(ctx, "foo.c.bad"); err == nil || !strings.Contains(err.Error(), "subjects") {
		t.Errorf("a changed inbox: %v", err)
	}
	if err := p.CreateInbox(ctx, "not an address"); err == nil {
		t.Error("a malformed address was accepted")
	}
	if err := (Provisioner{}).CreateInbox(ctx, "foo.c.here"); err == nil {
		t.Error("no checker: the inbox was taken for existing")
	}
}

func TestNoInboxDetailIsPlainAndShort(t *testing.T) {
	one := NoInboxDetail([]string{"foo.c.core"})
	many := NoInboxDetail([]string{"foo.c.a", "foo.c.b", "foo.c.c", "foo.c.d", "foo.c.e"})
	for _, d := range []string{one, many} {
		if len(d) > 500 || !strings.Contains(d, "no inbox yet") || !strings.Contains(d, "credential") || !strings.Contains(d, "nothing was pushed") {
			t.Errorf("%d bytes: %s", len(d), d)
		}
	}
	if !strings.Contains(one, "foo.c.core") || !strings.Contains(many, "foo.c.c") || !strings.Contains(many, "2 more") {
		t.Errorf("%s | %s", one, many)
	}
}

func TestProductionStepsOnlyCheck(t *testing.T) {
	ctx := context.Background()
	if err := (FailClosedDeliverer{}).Deliver(ctx, "foo.c.x", "jwt"); err == nil {
		t.Error("Deliver succeeded")
	}
	c, err := listset.Compile(listset.ListSet{
		Range: "foo",
		Switches: []listset.SwitchEntry{{Group: "foo.c", Verb: listset.Mail, Switch: listset.Closed}, {Group: "foo.c", Verb: listset.Prompt, Switch: listset.Closed},
			{Group: "foo.c", Verb: listset.Status, Switch: listset.Closed}, {Group: "foo.c", Verb: listset.Heartbeat, Switch: listset.Closed}},
		Senders: []listset.SenderLists{{Sender: "foo.c.x"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	with := Provisioner{Keys: groupKeys{"foo.c": "AKEY"}, Sets: []*listset.Compiled{nil, c}}
	if err := with.IssueCredential(ctx, "foo.c.x"); err != nil {
		t.Errorf("an issuable address: %v", err)
	}
	if err := with.IssueCredential(ctx, "foo.c.x"); err != nil {
		t.Errorf("not idempotent: %v", err)
	}
	if err := with.IssueCredential(ctx, "foo.c.y"); err == nil {
		t.Error("an address in no list set was accepted")
	}
	if err := (Provisioner{Keys: groupKeys{}, Sets: []*listset.Compiled{c}}).IssueCredential(ctx, "foo.c.x"); err == nil {
		t.Error("an address whose group has no key was accepted")
	}
}

// groupKeys is a Keys that holds only the public keys of the owner groups.
type groupKeys map[string]string

func (groupKeys) Operator() (nkeys.KeyPair, string, error)     { return nil, "", errors.New("none") }
func (groupKeys) GroupKey(string, bool) (nkeys.KeyPair, error) { return nil, errors.New("none") }
func (g groupKeys) GroupPublics() (map[string]string, error)   { return g, nil }

type memKeys struct{ op nkeys.KeyPair }

func (m memKeys) Operator() (nkeys.KeyPair, string, error) {
	kp, err := nkeys.FromSeed(mustSeed(m.op))
	pub, _ := kp.PublicKey()
	return kp, pub, err
}
func (memKeys) GroupKey(string, bool) (nkeys.KeyPair, error) { return nil, errors.New("none") }
func (memKeys) GroupPublics() (map[string]string, error)     { return nil, nil }

func mustSeed(kp nkeys.KeyPair) []byte {
	s, err := kp.Seed()
	if err != nil {
		panic(err)
	}
	return s
}

func TestSignPreviousKeepsTheJWTAndAddsRevocations(t *testing.T) {
	op, _ := nkeys.CreateOperator()
	acct, _ := nkeys.CreateAccount()
	apub, _ := acct.PublicKey()
	ac := jwt.NewAccountClaims(apub)
	ac.Name = "MASH"
	user, _ := nkeys.CreateUser()
	upub, _ := user.PublicKey()
	prevTok, err := ac.Encode(op)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	iss := &Issuer{Ctx: context.Background(), Keys: memKeys{op}, Account: apub, Now: func() time.Time { return now.Add(2 * time.Second) }}
	tok, err := iss.SignPrevious(policyapply.PreviousClaims{JWT: prevTok}, []string{upub})
	if err != nil {
		t.Fatal(err)
	}
	c, err := jwt.DecodeAccountClaims(tok)
	if err != nil {
		t.Fatal(err)
	}
	if _, has := c.Revocations[upub]; !has || c.Name != "MASH" || c.Subject != apub {
		t.Errorf("claims %+v", c)
	}
	if _, err := iss.SignPrevious(policyapply.PreviousClaims{JWT: "garbage"}, nil); err == nil {
		t.Error("a JWT that does not decode was re-signed")
	}
}
