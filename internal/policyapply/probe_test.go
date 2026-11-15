package policyapply

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/nats-io/jwt/v2"
	"github.com/nats-io/nats.go"
	"github.com/nats-io/nkeys"

	"github.com/bogworks/mash/internal/policy"
)

func TestFoldAccountz(t *testing.T) {
	out := map[string]string{}
	for _, reply := range []string{
		`{"server":{"name":"s1","id":"A"},"data":{"server_id":"A","account_detail":{"account_name":"X","jwt":"tok1"}}}`,
		`{"server":{"name":"s2","id":"B"},"error":{"code":500,"description":"Account X does not exist"}}`,
		`{"server":{"name":"","id":"C"},"data":{"account_detail":{"jwt":"tok3"}}}`,
		`{"server":{"name":"s1","id":"A"},"data":{"account_detail":{"jwt":"repeat"}}}`,
		`not json`,
		`{"server":{},"data":{"account_detail":{"jwt":"anon"}}}`,
	} {
		if err := foldAccountz(out, []byte(reply)); err != nil {
			t.Fatal(err)
		}
	}
	want := map[string]string{"s1": "tok1", "s2": "", "C": "tok3"}
	if len(out) != len(want) {
		t.Fatalf("out = %v", out)
	}
	for k, v := range want {
		if got, ok := out[k]; !ok || got != v {
			t.Errorf("%s = %q (%v), want %q", k, got, ok, v)
		}
	}
}

func TestFoldAccountzRefusesNeverTruncates(t *testing.T) {
	out := map[string]string{}
	big := `{"server":{"name":"s1"},"data":{"account_detail":{"jwt":"` + strings.Repeat("a", policy.AccountJWTLimit) + `"}}}`
	if err := foldAccountz(out, []byte(big)); err != nil || out["s1"] == "" {
		t.Errorf("a JWT at the limit in a larger reply: err = %v", err)
	}
	delete(out, "s1")
	over := `{"server":{"name":"s1"},"data":{"account_detail":{"jwt":"` + strings.Repeat("a", policy.AccountJWTLimit+1) + `"}}}`
	if err := foldAccountz(out, []byte(over)); err == nil || len(out) != 0 {
		t.Errorf("JWT over the limit: err = %v, out = %v", err, out)
	}
	huge := `{"server":{"name":"s1"},"data":{"account_detail":{"jwt":"x"},"pad":"` + strings.Repeat("a", maxAccountzReply) + `"}}`
	if err := foldAccountz(out, []byte(huge)); err == nil || len(out) != 0 {
		t.Errorf("reply over the bound: err = %v, out = %v", err, out)
	}
	long := `{"server":{"name":"` + strings.Repeat("n", maxServerNameBytes+1) + `"},"data":{}}`
	if err := foldAccountz(out, []byte(long)); err == nil || len(out) != 0 {
		t.Errorf("err = %v, out = %v", err, out)
	}
}

type scopedSigner map[string]nkeys.KeyPair

func (s scopedSigner) SignUser(group string, uc *jwt.UserClaims) (string, error) {
	kp, ok := s[group]
	if !ok {
		return "", errors.New("no key for group " + group)
	}
	return uc.Encode(kp)
}

func TestProbeMinterMint(t *testing.T) {
	acct, _ := nkeys.CreateAccount()
	acctPub, _ := acct.PublicKey()
	sk, _ := nkeys.CreateAccount()
	rec := &MemIssueRecord{}
	now := time.Date(2026, 11, 12, 10, 0, 0, 0, time.UTC)
	m := &ProbeMinter{Signer: scopedSigner{"c": sk}, Account: acctPub, Record: rec, Now: func() time.Time { return now }}
	p := ProbeCred{Sender: "c.core", Name: "core", Tags: []string{"allow:c.docs"}}
	creds, pub, err := m.Mint(context.Background(), p)
	if err != nil {
		t.Fatal(err)
	}
	tok, err := jwt.ParseDecoratedJWT(creds)
	if err != nil {
		t.Fatal(err)
	}
	uc, err := jwt.DecodeUserClaims(tok)
	if err != nil {
		t.Fatal(err)
	}
	skPub, _ := sk.PublicKey()
	if uc.Subject != pub || uc.Name != "core" || uc.IssuerAccount != acctPub || uc.Issuer != skPub {
		t.Errorf("claims = %+v", uc)
	}
	// With no Endpoint the probe's endpoint tag is that of the probe endpoint "probe.c.core".
	if want := []string{"allow:c.docs", "ep:probe_c_core"}; !slices.Equal([]string(uc.Tags), want) {
		t.Errorf("tags = %v, want %v", uc.Tags, want)
	}
	if uc.Pub.Allow != nil || uc.Sub.Allow != nil || uc.Pub.Deny != nil || uc.Sub.Deny != nil || uc.Resp != nil {
		t.Errorf("a probe carries permissions: %+v", uc.UserPermissionLimits)
	}
	if uc.Expires != now.Add(DefaultProbeTTL).Unix() {
		t.Errorf("expires = %d", uc.Expires)
	}
	es := rec.Entries()
	if len(es) != 1 || es[0].Purpose != PurposeProbe || es[0].PublicKey != pub || es[0].Address != "c.core" || es[0].RevokedAt != nil {
		t.Errorf("record = %+v", es)
	}
	_, pub2, _ := m.Mint(context.Background(), p)
	if pub2 == pub {
		t.Error("probe keys repeat")
	}
	// An Endpoint gives its own tag; an endpoint at another address is refused.
	p.Endpoint = "claude.c.core"
	creds2, _, err := m.Mint(context.Background(), p)
	if err != nil {
		t.Fatal(err)
	}
	tok2, _ := jwt.ParseDecoratedJWT(creds2)
	uc2, _ := jwt.DecodeUserClaims(tok2)
	if want := []string{"allow:c.docs", "ep:claude_c_core"}; !slices.Equal([]string(uc2.Tags), want) {
		t.Errorf("tags with an endpoint = %v, want %v", uc2.Tags, want)
	}
	p.Endpoint = "claude.c.docs"
	if _, _, err := m.Mint(context.Background(), p); err == nil {
		t.Error("a probe for an endpoint at another address was minted")
	}
	p.Endpoint = ""
}

func TestProbeMinterRefusals(t *testing.T) {
	acct, _ := nkeys.CreateAccount()
	acctPub, _ := acct.PublicKey()
	sk, _ := nkeys.CreateAccount()
	rec := &MemIssueRecord{}
	m := &ProbeMinter{Signer: scopedSigner{"c": sk}, Account: acctPub, Record: rec}
	ctx := context.Background()
	for name, p := range map[string]ProbeCred{
		"no name":       {Sender: "c.core"},
		"bad address":   {Sender: "core", Name: "core"},
		"upper-case":    {Sender: "c.core", Name: "core", Tags: []string{"Allow:c.docs"}},
		"duplicate tag": {Sender: "c.core", Name: "core", Tags: []string{"a:b", "a:b"}},
		"unknown group": {Sender: "z.core", Name: "core"},
	} {
		if _, _, err := m.Mint(ctx, p); err == nil {
			t.Errorf("%s: minted", name)
		}
	}
	// The unknown group is refused by the signer after it was recorded; every other refusal happens before.
	if n := len(rec.Entries()); n != 1 {
		t.Errorf("entries = %d", n)
	}
}

type failingRecord struct{ MemIssueRecord }

func (*failingRecord) Add(context.Context, IssueEntry) error { return errors.New("down") }

func TestProbeMinterRecordsBeforeReturning(t *testing.T) {
	acct, _ := nkeys.CreateAccount()
	acctPub, _ := acct.PublicKey()
	sk, _ := nkeys.CreateAccount()
	m := &ProbeMinter{Signer: scopedSigner{"c": sk}, Account: acctPub, Record: &failingRecord{}}
	creds, pub, err := m.Mint(context.Background(), ProbeCred{Sender: "c.core", Name: "core"})
	if err == nil || creds != nil || pub != "" {
		t.Errorf("minted although the record failed: %v", err)
	}
}

type seedLeakSigner struct{ seed string }

func (s seedLeakSigner) SignUser(string, *jwt.UserClaims) (string, error) {
	return "", errors.New("bad key " + s.seed)
}

func TestProbeMinterErrorHasNoSeed(t *testing.T) {
	acct, _ := nkeys.CreateAccount()
	acctPub, _ := acct.PublicKey()
	kp, _ := nkeys.CreateUser()
	seed, _ := kp.Seed()
	m := &ProbeMinter{Signer: seedLeakSigner{string(seed)}, Account: acctPub, Record: &MemIssueRecord{}}
	_, _, err := m.Mint(context.Background(), ProbeCred{Sender: "c.core", Name: "core"})
	if err == nil || strings.Contains(err.Error(), string(seed)) {
		t.Errorf("err = %v", err)
	}
}

type fakeRevoker struct {
	got []string
	err error
}

func (f *fakeRevoker) Revoke(_ context.Context, pubs []string) (string, error) {
	f.got = append(f.got, pubs...)
	return "", f.err
}

func TestRevokeProbes(t *testing.T) {
	rec := &MemIssueRecord{}
	ctx := context.Background()
	for _, k := range []string{"UA", "UB"} {
		_ = rec.Add(ctx, IssueEntry{PublicKey: k, Purpose: PurposeProbe})
	}
	at := time.Date(2026, 11, 12, 10, 1, 0, 0, time.UTC)
	rv := &fakeRevoker{err: errors.New("push failed")}
	if err := RevokeProbes(ctx, rv, rec, []string{"UB", "UA", "UA"}, at); err == nil {
		t.Fatal("no error")
	}
	for _, e := range rec.Entries() {
		if e.RevokedAt != nil {
			t.Errorf("marked revoked although the revocation failed: %+v", e)
		}
	}
	rv.err, rv.got = nil, nil
	if err := RevokeProbes(ctx, rv, rec, []string{"UB", "UA", "UA"}, at); err != nil {
		t.Fatal(err)
	}
	if len(rv.got) != 2 {
		t.Errorf("revoked %v", rv.got)
	}
	for _, e := range rec.Entries() {
		if e.RevokedAt == nil || !e.RevokedAt.Equal(at) {
			t.Errorf("not marked: %+v", e)
		}
	}
	if err := RevokeProbes(ctx, rv, rec, nil, at); err != nil {
		t.Error(err)
	}
}

func TestWitnessClosedByPush(t *testing.T) {
	exp := time.Unix(1000, 0)
	for _, c := range []struct {
		name    string
		at, exp time.Time
		want    bool
	}{
		{"before expiry", exp.Add(-time.Second), exp, true},
		{"at expiry", exp, exp, false},
		{"after expiry", exp.Add(time.Second), exp, false},
		{"unknown expiry", exp.Add(time.Hour), time.Time{}, true},
	} {
		if got := witnessClosedByPush(c.at, c.exp); got != c.want {
			t.Errorf("%s: got %v, want %v", c.name, got, c.want)
		}
	}
}

var errOptionSeen = errors.New("the connection option was applied")

// The options of a LiveProber and of LiveWitnesses reach every connection they make: here an option that refuses the
// connection stands for the TLS options of the deployment.
func TestLiveTransportsPassTheirOptions(t *testing.T) {
	acct, _ := nkeys.CreateAccount()
	acctPub, _ := acct.PublicKey()
	sk, _ := nkeys.CreateAccount()
	m := &ProbeMinter{Signer: scopedSigner{"c": sk}, Account: acctPub, Record: &MemIssueRecord{}}
	p := ProbeCred{Sender: "c.core", Name: "core", Tags: []string{"allow:c.docs"}}
	opt := func(*nats.Options) error { return errOptionSeen }
	urls := map[string]string{"s1": "nats://127.0.0.1:1"}

	prober := &LiveProber{URLs: urls, Mint: m, Options: []nats.Option{opt}}
	if _, err := prober.Refused(context.Background(), "s1", p, OpPub, "x.y"); !errors.Is(err, errOptionSeen) {
		t.Errorf("the prober did not pass its options: %v", err)
	}
	w := &LiveWitnesses{Options: []nats.Option{opt}}
	if err := w.Open(context.Background(), urls, m, []ProbeCred{p}); !errors.Is(err, errOptionSeen) {
		t.Errorf("the witnesses did not pass their options: %v", err)
	}
}
