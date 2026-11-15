package issue

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"log/slog"
	"slices"
	"strconv"
	"strings"
	"sync"
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
	mu          sync.Mutex
	recs        map[string]store.Issued
	fail        bool
	beforeWrite func()
	afterLookup func()
}

func (f *fakeRecord) Lookup(_ context.Context, k string) (*store.Issued, error) {
	if f.afterLookup != nil {
		defer f.afterLookup()
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if r, ok := f.recs[k]; ok {
		return &r, nil
	}
	return nil, nil
}

// RecordChecked is the real arbiter's rule over a map: the conditions are tested at the write, under the fake's lock.
// beforeWrite, when set, runs first and stands for another process's write landing after the issuer's lookup.
func (f *fakeRecord) RecordChecked(_ context.Context, r store.Issued) error {
	if f.beforeWrite != nil {
		f.beforeWrite()
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.fail {
		return errors.New("down")
	}
	if cur, ok := f.recs[r.PublicKey]; ok {
		if cur.RevokedAt != "" {
			return store.ErrKeyRevoked
		}
		if cur.Address != r.Address {
			return store.ErrKeyBoundElsewhere
		}
	}
	f.recs[r.PublicKey] = r
	return nil
}

// fakeClaims is the account JWT the service verifies the signing key against.
type fakeClaims struct {
	mu sync.Mutex
	ac *jwt.AccountClaims
	n  int
	// err, when set, is what Fetch returns: the account JWT could not be read from every expected server.
	err error
}

func (f *fakeClaims) Fetch(context.Context) (*jwt.AccountClaims, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.n++
	if f.err != nil {
		return nil, f.err
	}
	return f.ac, nil
}

// publish sets the account JWT to carry pub as a scoped signer with the permissions p (nil p: an unscoped signer).
func (f *fakeClaims) publish(pub string, p *jwt.Permissions) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.ac = jwt.NewAccountClaims("A")
	if p == nil {
		f.ac.SigningKeys.Add(pub)
		return
	}
	us := jwt.NewUserScope()
	us.Key = pub
	us.Template.Permissions = *p
	f.ac.SigningKeys.AddScopedSigner(us)
}

type fixture struct {
	is       *Issuer
	claims   *fakeClaims
	tpl      jwt.Permissions
	gpub     string
	rec      *fakeRecord
	wrangler nkeys.KeyPair
	wpub     string
	now      time.Time
	logs     *bytes.Buffer
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	c, err := listset.Compile(listset.ListSet{
		Switches: []listset.SwitchEntry{
			{Group: "c", Verb: listset.Mail, Switch: listset.Closed}, {Group: "c", Verb: listset.Prompt, Switch: listset.Closed},
			{Group: "c", Verb: listset.Status, Switch: listset.Closed}, {Group: "c", Verb: listset.Heartbeat, Switch: listset.Closed},
		},
		Senders: []listset.SenderLists{{Sender: "c.core", Allow: []string{"c.lead"}}, {Sender: "c.lead"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	w, _ := nkeys.CreateUser()
	wpub, _ := w.PublicKey()
	acct, _ := nkeys.CreateAccount()
	apub, _ := acct.PublicKey()
	gk, _ := nkeys.CreateAccount()
	gpub, _ := gk.PublicKey()
	fc := &fakeClaims{}
	var tpl jwt.Permissions
	for _, t := range c.Templates {
		if t.Group == "c" {
			tpl = t.Permissions
		}
	}
	fc.publish(gpub, &tpl)
	f := &fixture{claims: fc, tpl: tpl, gpub: gpub, rec: &fakeRecord{recs: map[string]store.Issued{}}, wrangler: w, wpub: wpub, now: time.Now(), logs: &bytes.Buffer{}}
	f.is = &Issuer{
		Registry: fakeRegistry{"h|c": {wpub}}, ListSet: fakeSet{c}, Signer: fakeSigner{gk}, Record: f.rec, Claims: fc, AccountPub: apub,
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
	r := Request{Version: 1, Address: address, Endpoint: "claude-code." + address, PublicKey: pub, Host: "h", Nonce: base64.RawURLEncoding.EncodeToString(nb),
		Expires: f.now.Add(ttl).Unix(), Wrangler: f.wpub}
	var err error
	if r.Proof, err = SignProof(f.wrangler, r.Address, r.Endpoint, r.PublicKey, r.Host, r.Nonce, r.Expires); err != nil {
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
	got := string(CanonicalBytes("c.core", "claude-code.c.core", "UKEY", "h", "NONCE", 1700000000))
	if want := "mash-issue-v2\nc.core\nclaude-code.c.core\nUKEY\nh\nNONCE\n1700000000"; got != want {
		t.Fatalf("%q", got)
	}
}

func TestIssueSignsWithTheTableAndRecordsFirst(t *testing.T) {
	f := newFixture(t)
	r := f.req(t, "c.core", time.Minute)
	out, err := f.is.Issue(context.Background(), r, store.PurposeRotation)
	if err != nil {
		t.Fatal(err)
	}
	uc, err := jwt.DecodeUserClaims(out.JWT)
	if err != nil || len(uc.Tags) == 0 {
		t.Fatalf("%v %v", uc, err)
	}
	rec := f.rec.recs[r.PublicKey]
	if rec.Purpose != store.PurposeRotation || rec.Generation != 7 || rec.Address != "c.core" {
		t.Errorf("%+v", rec)
	}
	if _, err := f.is.Issue(context.Background(), f.req(t, "c.lead", time.Minute), "bogus"); code(err) != CodeBadPurpose {
		t.Errorf("a bad purpose: %v", err)
	}
}

func TestNothingIsReturnedWhenTheRecordFails(t *testing.T) {
	f := newFixture(t)
	f.rec.fail = true
	out, err := f.is.Issue(context.Background(), f.req(t, "c.core", time.Minute), store.PurposeIssue)
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
	r := f.req(t, "c.core", time.Minute)
	ctx := context.Background()
	if _, err := f.is.Issue(ctx, r, store.PurposeIssue); err != nil {
		t.Fatal(err)
	}
	if _, err := f.is.Issue(ctx, r, store.PurposeIssue); code(err) != CodeReplay {
		t.Fatalf("same service: %v", err)
	}
	restart := func() *Issuer {
		return &Issuer{Registry: f.is.Registry, ListSet: f.is.ListSet, Signer: f.is.Signer, Record: f.is.Record, Claims: f.is.Claims,
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
	r := f.req(t, "c.core", time.Minute)
	if _, err := f.is.Issue(ctx, r, store.PurposeIssue); err != nil {
		t.Fatal(err)
	}
	if f.is.nonces.len() != 1 {
		t.Fatalf("%d", f.is.nonces.len())
	}
	f.now = f.now.Add(2 * time.Minute)
	if _, err := f.is.Issue(ctx, f.req(t, "c.lead", time.Minute), store.PurposeIssue); err != nil || f.is.nonces.len() != 1 {
		t.Fatalf("%v %d", err, f.is.nonces.len())
	}
	exp := f.now.Add(time.Minute)
	for i := 0; f.is.nonces.len() < MaxNonces; i++ {
		f.is.nonces.take("fill"+strconv.Itoa(i), exp, f.now)
	}
	if _, err := f.is.Issue(ctx, f.req(t, "c.core", time.Minute), store.PurposeIssue); code(err) != CodeBusy {
		t.Fatalf("full set: %v", err)
	}
}

// A forged request does not use up the nonce of a real one.
func TestAForgedRequestDoesNotBurnTheNonce(t *testing.T) {
	f := newFixture(t)
	r := f.req(t, "c.core", time.Minute)
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
	r := f.req(t, "c.core", time.Minute)
	good := `{"version":1,"address":"` + r.Address + `","endpoint":"` + r.Endpoint + `","public_key":"` + r.PublicKey + `","host":"h","nonce":"` + r.Nonce +
		`","expires":` + "1" + `,"proof":"` + r.Proof + `","wrangler":"` + r.Wrangler + `","tags":["x"],"permissions":{"pub":{"allow":[">"]}}}`
	got, rf := Parse([]byte(good))
	if rf != nil || got.Address != "c.core" {
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
	r := f.req(t, "c.core", time.Minute)
	r.PublicKey, r.Address, r.Host = string(seed), string(seed), string(seed)
	if _, err := f.is.Issue(context.Background(), r, store.PurposeIssue); err == nil {
		t.Fatal("accepted")
	}
	if strings.Contains(f.logs.String(), string(seed)) || !strings.Contains(f.logs.String(), "code=") {
		t.Fatalf("%s", f.logs.String())
	}
}

// Every credential the issuer signs under a scoped group key carries the endpoint tag, built from mash's own function
// over the endpoint of the request, and the record keeps the tags that were signed.
func TestIssuedCredentialCarriesTheEndpointTag(t *testing.T) {
	f := newFixture(t)
	r := f.req(t, "c.core", time.Minute)
	out, err := f.is.Issue(context.Background(), r, store.PurposeIssue)
	if err != nil {
		t.Fatal(err)
	}
	uc, err := jwt.DecodeUserClaims(out.JWT)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"allow:c.lead", "ep:claude-code_c_core"}
	if !slices.Equal([]string(uc.Tags), want) || !slices.Equal(out.Record.Tags, want) {
		t.Fatalf("tags %v, record %v, want %v", uc.Tags, out.Record.Tags, want)
	}
}

// A request never supplies tags, and an endpoint tag in a request is dropped unread like any other.
func TestARequestCannotSupplyTheEndpointTag(t *testing.T) {
	f := newFixture(t)
	r := f.req(t, "c.core", time.Minute)
	raw, _ := json.Marshal(map[string]any{
		"version": r.Version, "address": r.Address, "endpoint": r.Endpoint, "public_key": r.PublicKey, "host": r.Host,
		"nonce": r.Nonce, "expires": r.Expires, "proof": r.Proof, "wrangler": r.Wrangler, "tags": []string{"ep:cc_c_lead"},
	})
	got, rf := Parse(raw)
	if rf != nil {
		t.Fatal(rf)
	}
	out, err := f.is.Issue(context.Background(), got, store.PurposeIssue)
	if err != nil {
		t.Fatal(err)
	}
	uc, _ := jwt.DecodeUserClaims(out.JWT)
	if slices.Contains([]string(uc.Tags), "ep:cc_c_lead") || !slices.Contains([]string(uc.Tags), "ep:claude-code_c_core") {
		t.Fatalf("tags %v", uc.Tags)
	}
}

// The issuer refuses to sign when the endpoint's owner and name are not the address's: a credential cannot point at
// another address's inbox. Nothing is recorded and the nonce is not burned.
func TestIssuerRefusesAnEndpointTagForAnotherAddress(t *testing.T) {
	f := newFixture(t)
	for _, ep := range []string{"claude-code.c.lead", "claude-code.d.core", "claude-code.c.other"} {
		r := f.req(t, "c.core", time.Minute)
		r.Endpoint = ep
		var err error
		if r.Proof, err = SignProof(f.wrangler, r.Address, r.Endpoint, r.PublicKey, r.Host, r.Nonce, r.Expires); err != nil {
			t.Fatal(err)
		}
		out, err := f.is.Issue(context.Background(), r, store.PurposeIssue)
		if out != nil || code(err) != CodeEndpoint {
			t.Fatalf("endpoint %s: %v %v", ep, out, err)
		}
		if f.is.nonces.len() != 0 || len(f.rec.recs) != 0 {
			t.Fatalf("endpoint %s: a nonce or a record was kept", ep)
		}
	}
	// The endpoint is inside the proof: changing it after signing breaks the proof.
	r := f.req(t, "c.core", time.Minute)
	r.Endpoint = "other.c.core"
	if _, err := f.is.Issue(context.Background(), r, store.PurposeIssue); code(err) != CodeBadProof {
		t.Fatalf("%v", err)
	}
	// A legacy form is no endpoint of a policy address.
	r = f.req(t, "c.core", time.Minute)
	r.Endpoint = "core"
	if _, err := f.is.Issue(context.Background(), r, store.PurposeIssue); code(err) != CodeMalformed {
		t.Fatalf("%v", err)
	}
}
