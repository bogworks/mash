package issue

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/nats-io/jwt/v2"
	"github.com/nats-io/nkeys"

	"github.com/bogworks/mash/internal/listset"
	"github.com/bogworks/mash/internal/store"
)

type fakeRegistry map[string][]string // host + "|" + group -> keys

func (f fakeRegistry) Lookup(host, group string) []string { return f[host+"|"+group] }

type fakeSet struct{ c *listset.Compiled }

func (f fakeSet) InForce(context.Context) (*listset.Compiled, int, error) { return f.c, 7, nil }

type fakeSigner struct{ kp nkeys.KeyPair }

func (f fakeSigner) GroupKey(string) (nkeys.KeyPair, error) {
	seed, _ := f.kp.Seed()
	return nkeys.FromSeed(seed)
}

type fakeRecord struct {
	recs map[string]store.Issued
	fail bool
}

func (f *fakeRecord) Lookup(_ context.Context, k string) (*store.Issued, error) {
	if r, ok := f.recs[k]; ok {
		return &r, nil
	}
	return nil, nil
}

func (f *fakeRecord) Record(_ context.Context, r store.Issued) error {
	if f.fail {
		return errors.New("down")
	}
	f.recs[r.PublicKey] = r
	return nil
}

type fixture struct {
	is       *Issuer
	rec      *fakeRecord
	wrangler nkeys.KeyPair
	wpub     string
	now      time.Time
	logs     *bytes.Buffer
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	c, err := listset.Compile(listset.ListSet{
		Range: "foo",
		Switches: []listset.SwitchEntry{
			{Group: "foo.c", Verb: listset.Mail, Switch: listset.Closed}, {Group: "foo.c", Verb: listset.Prompt, Switch: listset.Closed},
			{Group: "foo.c", Verb: listset.Status, Switch: listset.Closed}, {Group: "foo.c", Verb: listset.Heartbeat, Switch: listset.Closed},
		},
		Senders: []listset.SenderLists{{Sender: "foo.c.core", Allow: []string{"foo.c.lead"}}, {Sender: "foo.c.lead"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	w, _ := nkeys.CreateUser()
	wpub, _ := w.PublicKey()
	acct, _ := nkeys.CreateAccount()
	apub, _ := acct.PublicKey()
	gk, _ := nkeys.CreateAccount()
	f := &fixture{rec: &fakeRecord{recs: map[string]store.Issued{}}, wrangler: w, wpub: wpub, now: time.Now(), logs: &bytes.Buffer{}}
	f.is = &Issuer{
		Registry: fakeRegistry{"h|foo.c": {wpub}}, ListSet: fakeSet{c}, Signer: fakeSigner{gk}, Record: f.rec, AccountPub: apub,
		Log: slog.New(slog.NewTextHandler(f.logs, nil)), Now: func() time.Time { return f.now },
	}
	return f
}

func (f *fixture) req(t *testing.T, address string, ttl time.Duration) Request {
	t.Helper()
	u, _ := nkeys.CreateUser()
	pub, _ := u.PublicKey()
	nb := make([]byte, MinNonceBytes)
	_, _ = rand.Read(nb)
	r := Request{Version: 1, Address: address, PublicKey: pub, Host: "h", Nonce: base64.RawURLEncoding.EncodeToString(nb),
		Expires: f.now.Add(ttl).Unix(), Wrangler: f.wpub}
	var err error
	if r.Proof, err = SignProof(f.wrangler, r.Address, r.PublicKey, r.Host, r.Nonce, r.Expires); err != nil {
		t.Fatal(err)
	}
	return r
}

func code(err error) string {
	var r *Refusal
	if errors.As(err, &r) {
		return r.Code
	}
	return ""
}

func TestCanonicalBytesAreTheDocumentedForm(t *testing.T) {
	got := string(CanonicalBytes("foo.c.core", "UKEY", "h", "NONCE", 1700000000))
	if want := "mash-issue-v1\nfoo.c.core\nUKEY\nh\nNONCE\n1700000000"; got != want {
		t.Fatalf("%q", got)
	}
}

func TestIssueSignsWithTheTableAndRecordsFirst(t *testing.T) {
	f := newFixture(t)
	r := f.req(t, "foo.c.core", time.Minute)
	out, err := f.is.Issue(context.Background(), r, store.PurposeRotation)
	if err != nil {
		t.Fatal(err)
	}
	uc, err := jwt.DecodeUserClaims(out.JWT)
	if err != nil || len(uc.Tags) == 0 {
		t.Fatalf("%v %v", uc, err)
	}
	rec := f.rec.recs[r.PublicKey]
	if rec.Purpose != store.PurposeRotation || rec.Generation != 7 || rec.Address != "foo.c.core" {
		t.Errorf("%+v", rec)
	}
	if _, err := f.is.Issue(context.Background(), f.req(t, "foo.c.lead", time.Minute), "bogus"); code(err) != CodeBadPurpose {
		t.Errorf("a bad purpose: %v", err)
	}
}

func TestNothingIsReturnedWhenTheRecordFails(t *testing.T) {
	f := newFixture(t)
	f.rec.fail = true
	out, err := f.is.Issue(context.Background(), f.req(t, "foo.c.core", time.Minute), store.PurposeIssue)
	if out != nil || code(err) != CodeRecordFailed {
		t.Fatalf("%v %v", out, err)
	}
	if len(f.rec.recs) != 0 {
		t.Error("recorded")
	}
}

// A restart forgets the nonces: the same request is accepted again while it is unexpired, and refused by its expiry once
// it has passed. The expiry, at most MaxWindow ahead, is the bound on a replay across a restart.
func TestRestartForgetsNoncesAndExpiryBoundsTheReplay(t *testing.T) {
	f := newFixture(t)
	r := f.req(t, "foo.c.core", time.Minute)
	ctx := context.Background()
	if _, err := f.is.Issue(ctx, r, store.PurposeIssue); err != nil {
		t.Fatal(err)
	}
	if _, err := f.is.Issue(ctx, r, store.PurposeIssue); code(err) != CodeReplay {
		t.Fatalf("same service: %v", err)
	}
	restart := func() *Issuer {
		return &Issuer{Registry: f.is.Registry, ListSet: f.is.ListSet, Signer: f.is.Signer, Record: f.is.Record,
			AccountPub: f.is.AccountPub, Log: f.is.Log, Now: f.is.Now}
	}
	restarted := restart()
	if _, err := restarted.Issue(ctx, r, store.PurposeIssue); err != nil {
		t.Fatalf("a restarted service has forgotten the nonce and, before expiry, accepts it: %v", err)
	}
	f.now = f.now.Add(time.Minute + time.Second)
	restarted = restart()
	if _, err := restarted.Issue(ctx, r, store.PurposeIssue); code(err) != CodeExpired {
		t.Fatalf("replay after expiry: %v", err)
	}
}

func TestNoncesAreDroppedAtExpiryAndCapped(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	r := f.req(t, "foo.c.core", time.Minute)
	if _, err := f.is.Issue(ctx, r, store.PurposeIssue); err != nil {
		t.Fatal(err)
	}
	if len(f.is.nonces) != 1 {
		t.Fatalf("%d", len(f.is.nonces))
	}
	f.now = f.now.Add(2 * time.Minute)
	if _, err := f.is.Issue(ctx, f.req(t, "foo.c.lead", time.Minute), store.PurposeIssue); err != nil || len(f.is.nonces) != 1 {
		t.Fatalf("%v %d", err, len(f.is.nonces))
	}
	exp := f.now.Add(time.Minute)
	for i := 0; len(f.is.nonces) < MaxNonces; i++ {
		f.is.nonces[strings.Repeat("a", 5)+string(rune('A'+i%26))+time.Duration(i).String()] = exp
	}
	if _, err := f.is.Issue(ctx, f.req(t, "foo.c.core", time.Minute), store.PurposeIssue); code(err) != CodeBusy {
		t.Fatalf("full set: %v", err)
	}
}

// A forged request does not use up the nonce of a real one.
func TestAForgedRequestDoesNotBurnTheNonce(t *testing.T) {
	f := newFixture(t)
	r := f.req(t, "foo.c.core", time.Minute)
	forged := r
	forged.Proof = base64.RawURLEncoding.EncodeToString(make([]byte, 64))
	if _, err := f.is.Issue(context.Background(), forged, store.PurposeIssue); code(err) != CodeBadProof {
		t.Fatal(err)
	}
	if _, err := f.is.Issue(context.Background(), r, store.PurposeIssue); err != nil {
		t.Fatalf("the real request was refused after a forged one: %v", err)
	}
}

func TestParseDropsUnknownFieldsAndCapsSize(t *testing.T) {
	f := newFixture(t)
	r := f.req(t, "foo.c.core", time.Minute)
	good := `{"version":1,"address":"` + r.Address + `","public_key":"` + r.PublicKey + `","host":"h","nonce":"` + r.Nonce +
		`","expires":` + "1" + `,"proof":"` + r.Proof + `","wrangler":"` + r.Wrangler + `","tags":["x"],"permissions":{"pub":{"allow":[">"]}}}`
	got, rf := Parse([]byte(good))
	if rf != nil || got.Address != "foo.c.core" {
		t.Fatalf("%+v %v", got, rf)
	}
	if _, rf := Parse(make([]byte, MaxRequestBytes+1)); rf == nil || rf.Code != CodeTooLarge {
		t.Fatalf("%v", rf)
	}
	if _, rf := Parse([]byte(`[]`)); rf == nil || rf.Code != CodeMalformed {
		t.Fatalf("%v", rf)
	}
}

func TestEveryFieldOfARefusalLogLineIsShapeChecked(t *testing.T) {
	f := newFixture(t)
	u, _ := nkeys.CreateUser()
	seed, _ := u.Seed()
	r := f.req(t, "foo.c.core", time.Minute)
	r.PublicKey, r.Address, r.Host = string(seed), string(seed), string(seed)
	if _, err := f.is.Issue(context.Background(), r, store.PurposeIssue); err == nil {
		t.Fatal("accepted")
	}
	if strings.Contains(f.logs.String(), string(seed)) || !strings.Contains(f.logs.String(), "code=") {
		t.Fatalf("%s", f.logs.String())
	}
}
