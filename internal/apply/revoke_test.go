package apply

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/nats-io/jwt/v2"
	"github.com/nats-io/nkeys"
)

func userKey(t *testing.T) string {
	t.Helper()
	kp, err := nkeys.CreateUser()
	if err != nil {
		t.Fatal(err)
	}
	pub, _ := kp.PublicKey()
	return pub
}

func revokedIn(t *testing.T, tok, user string) bool {
	t.Helper()
	c, err := jwt.DecodeAccountClaims(tok)
	if err != nil {
		t.Fatal(err)
	}
	_, ok := c.Revocations[user]
	return ok
}

var revokeAt = time.Unix(1_700_000_000, 0)

func TestRevokeUsersRevokesAndReadsBack(t *testing.T) {
	ks, fk, _ := setup(t)
	fk.acks, fk.applyPush = []ServerAck{{Server: "s1", Code: 200}}, true
	u1, u2 := userKey(t), userKey(t)
	res, err := RevokeUsers(context.Background(), RevokeParams{Keys: ks, Resolver: fk, Now: func() time.Time { return revokeAt }}, []string{u2, u1, u1})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Revoked) != 2 || len(res.Already) != 0 || res.Acked != 1 || !res.Complete || fk.pushes != 1 {
		t.Fatalf("%+v pushes %d", res, fk.pushes)
	}
	c, _ := jwt.DecodeAccountClaims(fk.token)
	if c.Revocations[u1] != revokeAt.Unix() || !revokedIn(t, fk.token, u2) {
		t.Fatalf("revocations %v", c.Revocations)
	}
	if !c.Revocations.IsRevoked(u1, revokeAt.Add(-time.Hour)) || c.Revocations.IsRevoked(u1, revokeAt.Add(time.Hour)) {
		t.Fatal("revocation time")
	}
}

func TestRevokeUsersIdempotentPushesNothing(t *testing.T) {
	ks, fk, _ := setup(t)
	fk.acks, fk.applyPush = []ServerAck{{Server: "s1", Code: 200}}, true
	u := userKey(t)
	p := RevokeParams{Keys: ks, Resolver: fk, Now: func() time.Time { return revokeAt }}
	if _, err := RevokeUsers(context.Background(), p, []string{u}); err != nil {
		t.Fatal(err)
	}
	// a call in the same second, or earlier, never lowers or moves the revocation
	p.Now = func() time.Time { return revokeAt.Add(500 * time.Millisecond) }
	res, err := RevokeUsers(context.Background(), p, []string{u})
	if err != nil || fk.pushes != 2 || len(res.Already) != 1 || res.Already[0] != u || len(res.Revoked) != 0 || !res.Complete || res.Acked != 1 {
		t.Fatalf("%+v %v pushes %d", res, err, fk.pushes)
	}
	if fk.lastPush != fk.token {
		t.Fatal("the already-revoked call must push the unchanged current token")
	}
	if c, _ := jwt.DecodeAccountClaims(fk.token); c.Revocations[u] != revokeAt.Unix() {
		t.Fatal("revocation time changed")
	}
}

func TestRevokeUsersAlreadyRevokedNeedsAcks(t *testing.T) {
	ks, fk, _ := setup(t)
	fk.acks, fk.applyPush = []ServerAck{{Server: "s1", Code: 200}}, true
	u := userKey(t)
	p := RevokeParams{Keys: ks, Resolver: fk, VerifyTries: 2, VerifyWait: 1, Now: func() time.Time { return revokeAt }}
	if _, err := RevokeUsers(context.Background(), p, []string{u}); err != nil {
		t.Fatal(err)
	}
	before := fk.token
	// a retry where a server does not acknowledge is not a success, though the resolver already holds the revocation
	fk.pushErr = errors.New("no server answered the claims update in time")
	res, err := RevokeUsers(context.Background(), p, []string{u})
	if !errors.Is(err, ErrNotConfirmed) || len(res.Already) != 1 {
		t.Fatalf("%+v %v", res, err)
	}
	fk.pushErr = nil
	fk.servers = ServerSet{Names: []string{"s1"}, Expected: 2}
	if _, err := RevokeUsers(context.Background(), p, []string{u}); !errors.Is(err, ErrNotConfirmed) {
		t.Fatalf("incomplete set: %v", err)
	}
	if fk.token != before {
		t.Fatal("the token was re-signed")
	}
}

func TestRevokeUsersInvalidKeyBeforeAnyIO(t *testing.T) {
	ks, fk, _ := setup(t)
	good := userKey(t)
	acct, _ := nkeys.CreateAccount()
	apub, _ := acct.PublicKey()
	for _, bad := range []string{"", "notakey", apub} {
		_, err := RevokeUsers(context.Background(), RevokeParams{Keys: ks, Resolver: &countFake{fake: fk}}, []string{good, bad})
		if err == nil || !strings.Contains(err.Error(), "not a NATS user public key") || !strings.Contains(err.Error(), `"`+bad+`"`) {
			t.Fatalf("%q: %v", bad, err)
		}
	}
	if _, err := RevokeUsers(context.Background(), RevokeParams{Keys: ks, Resolver: &countFake{fake: fk}}, nil); err == nil {
		t.Fatal("empty list accepted")
	}
	if fk.lookups != 0 || fk.pushes != 0 {
		t.Fatalf("I/O happened: lookups %d pushes %d", fk.lookups, fk.pushes)
	}
}

// countFake counts lookups on the shared fake.
type countFake struct{ *fake }

func (c *countFake) LookupAll(ctx context.Context, a string, n int) ([]string, error) {
	c.fake.mu.Lock()
	c.fake.lookups++
	c.fake.mu.Unlock()
	return c.fake.LookupAll(ctx, a, n)
}

func TestRevokeUsersRefusesSubjectMismatch(t *testing.T) {
	ks, fk, _ := setup(t)
	op, _, _ := ks.Operator()
	other, _ := nkeys.CreateAccount()
	opub, _ := other.PublicKey()
	tok, err := jwt.NewAccountClaims(opub).Encode(op)
	if err != nil {
		t.Fatal(err)
	}
	fk.token = tok
	_, err = RevokeUsers(context.Background(), RevokeParams{Keys: ks, Resolver: fk}, []string{userKey(t)})
	if err == nil || !strings.Contains(err.Error(), "not ") || fk.pushes != 0 {
		t.Fatalf("%v pushes %d", err, fk.pushes)
	}
}

func TestRevokeUsersNotAckedIsNotConfirmed(t *testing.T) {
	cases := []struct {
		name string
		fk   func(*fake)
		want string
	}{
		{"no ack", func(f *fake) { f.pushErr = errors.New("no server answered the claims update in time") }, "not acknowledged"},
		{"refused", func(f *fake) { f.acks = []ServerAck{{Server: "s1", Code: 500, Message: "boom"}} }, "refused by s1: code 500 boom"},
		{"acked but the resolver shows the old claims", func(f *fake) { f.acks = []ServerAck{{Server: "s1", Code: 200}} }, "does not show the revocation"},
		{"incomplete set", func(f *fake) {
			f.applyPush = true
			f.servers = ServerSet{Names: []string{"s1"}, Expected: 2}
			f.acks = []ServerAck{{Server: "s1", Code: 200}}
		}, "server set could not be verified"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ks, fk, _ := setup(t)
			tc.fk(fk)
			u := userKey(t)
			_, err := RevokeUsers(context.Background(), RevokeParams{Keys: ks, Resolver: fk, VerifyTries: 2, VerifyWait: 1}, []string{u})
			if !errors.Is(err, ErrNotConfirmed) || !strings.Contains(err.Error(), tc.want) || !strings.Contains(err.Error(), "safe") {
				t.Fatalf("%v", err)
			}
			if tc.name != "incomplete set" && revokedIn(t, fk.token, u) {
				t.Fatal("the revocation is assumed on the resolver")
			}
		})
	}
}

// changing swaps the stored token once, on the second LookupAll, to model another writer.
type changing struct {
	*fake
	swap  string
	swap2 string
	calls int
}

func (c *changing) LookupAll(ctx context.Context, a string, n int) ([]string, error) {
	c.fake.mu.Lock()
	c.calls++
	if c.calls == 2 || (c.swap2 != "" && c.calls == 4) {
		if c.calls == 2 {
			c.fake.token = c.swap
		} else {
			c.fake.token = c.swap2
		}
	}
	c.fake.mu.Unlock()
	return c.fake.LookupAll(ctx, a, n)
}

func TestRevokeUsersRetriesOnceWhenJWTChanges(t *testing.T) {
	ks, fk, _ := setup(t)
	fk.acks, fk.applyPush = []ServerAck{{Server: "s1", Code: 200}}, true
	op, _, _ := ks.Operator()
	_, apub, _ := ks.Account()
	cl, _ := jwt.DecodeAccountClaims(fk.token)
	pp := userKey(t) // another writer revokes a different user
	cl.Revocations = jwt.RevocationList{}
	cl.Revocations.Revoke(pp, revokeAt)
	swap, err := cl.Encode(op)
	if err != nil {
		t.Fatal(err)
	}
	_ = apub
	ch := &changing{fake: fk, swap: swap}
	u := userKey(t)
	res, err := RevokeUsers(context.Background(), RevokeParams{Keys: ks, Resolver: ch}, []string{u})
	if err != nil || len(res.Revoked) != 1 {
		t.Fatalf("%+v %v", res, err)
	}
	got, _ := jwt.DecodeAccountClaims(fk.token)
	if _, ok := got.Revocations[pp]; !ok || !revokedIn(t, fk.token, u) {
		t.Fatal("the push lost the other writer's change or the revocation")
	}
	if fk.pushes != 1 {
		t.Fatalf("pushes %d", fk.pushes)
	}
}

func TestRevokeUsersGivesUpAfterTwoChanges(t *testing.T) {
	ks, fk, _ := setup(t)
	op, _, _ := ks.Operator()
	mk := func() string {
		cl, _ := jwt.DecodeAccountClaims(fk.token)
		cl.Tags.Add(userKey(t))
		tok, err := cl.Encode(op)
		if err != nil {
			t.Fatal(err)
		}
		return tok
	}
	ch := &changing{fake: fk, swap: mk(), swap2: mk()}
	_, err := RevokeUsers(context.Background(), RevokeParams{Keys: ks, Resolver: ch}, []string{userKey(t)})
	if err == nil || !strings.Contains(err.Error(), "changed twice") || fk.pushes != 0 {
		t.Fatalf("%v pushes %d", err, fk.pushes)
	}
}

func TestRevocationSurvivesLaterPolicyApply(t *testing.T) {
	ks, fk, c := setup(t)
	fk.acks, fk.applyPush = []ServerAck{{Server: "s1", Code: 200}}, true
	u := userKey(t)
	if _, err := RevokeUsers(context.Background(), RevokeParams{Keys: ks, Resolver: fk}, []string{u}); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if code := Run(context.Background(), Params{Mode: Apply, Compiled: c, Keys: ks, Resolver: fk}, &out); code != 0 {
		t.Fatalf("%d\n%s", code, out.String())
	}
	got, _ := jwt.DecodeAccountClaims(fk.token)
	if len(got.SigningKeys) == 0 || !revokedIn(t, fk.token, u) {
		t.Fatal("apply dropped the revocation")
	}
}

func TestRevokeUsersRaisesAnOlderRevocation(t *testing.T) {
	ks, fk, _ := setup(t)
	fk.acks, fk.applyPush = []ServerAck{{Server: "s1", Code: 200}}, true
	u := userKey(t)
	p := RevokeParams{Keys: ks, Resolver: fk, Now: func() time.Time { return revokeAt }}
	if _, err := RevokeUsers(context.Background(), p, []string{u}); err != nil {
		t.Fatal(err)
	}
	first := fk.token
	t2 := revokeAt.Add(time.Hour)
	p.Now = func() time.Time { return t2 }
	res, err := RevokeUsers(context.Background(), p, []string{u})
	if err != nil || fk.pushes != 2 || len(res.Revoked) != 1 || len(res.Raised) != 1 || len(res.Already) != 0 || !res.Complete {
		t.Fatalf("%+v %v pushes %d", res, err, fk.pushes)
	}
	if fk.token == first {
		t.Fatal("a raised revocation must push a new token")
	}
	if c, _ := jwt.DecodeAccountClaims(fk.token); c.Revocations[u] != t2.Unix() {
		t.Fatalf("revocation time %d, want %d", c.Revocations[u], t2.Unix())
	}
}

func TestRevokeUsersNeverLowersAndResendsUnchanged(t *testing.T) {
	ks, fk, _ := setup(t)
	fk.acks, fk.applyPush = []ServerAck{{Server: "s1", Code: 200}}, true
	u := userKey(t)
	late := revokeAt.Add(time.Hour)
	p := RevokeParams{Keys: ks, Resolver: fk, Now: func() time.Time { return late }}
	if _, err := RevokeUsers(context.Background(), p, []string{u}); err != nil {
		t.Fatal(err)
	}
	for _, at := range []time.Time{revokeAt, late} {
		at := at
		p.Now = func() time.Time { return at }
		res, err := RevokeUsers(context.Background(), p, []string{u})
		if err != nil || len(res.Already) != 1 || len(res.Revoked) != 0 || len(res.Raised) != 0 {
			t.Fatalf("at %v: %+v %v", at, res, err)
		}
		if fk.lastPush != fk.token {
			t.Fatal("must re-send the unchanged token")
		}
		if c, _ := jwt.DecodeAccountClaims(fk.token); c.Revocations[u] != late.Unix() {
			t.Fatal("revocation was lowered")
		}
	}
}

func TestRevokeUsersRefusesForeignIssuer(t *testing.T) {
	ks, fk, _ := setup(t)
	foreign, _ := nkeys.CreateOperator()
	resolverToken(t, fk, ks, foreign, nil)
	before := fk.token
	_, err := RevokeUsers(context.Background(), RevokeParams{Keys: ks, Resolver: fk}, []string{userKey(t)})
	if err == nil || !strings.Contains(err.Error(), "not an accepted issuer") || !strings.Contains(err.Error(), pubOf(t, foreign)) || !strings.Contains(err.Error(), "nothing was changed") {
		t.Fatalf("%v", err)
	}
	if fk.pushes != 0 || fk.token != before {
		t.Fatal("something was pushed")
	}
}

func TestRevokeUsersRefusesUnknownSigningKeys(t *testing.T) {
	ks, fk, _, scoped, plain, _ := unknownSetup(t)
	fk.acks, fk.applyPush = []ServerAck{{Server: "s1", Code: 200}}, true
	_, err := RevokeUsers(context.Background(), RevokeParams{Keys: ks, Resolver: fk}, []string{userKey(t)})
	if err == nil {
		t.Fatal("want a refusal")
	}
	for _, w := range []string{scoped, "role legacy", plain, "plain signing key", "will not carry signing keys it did not create", "tool that added it", "mashctl policy apply", "nothing was changed"} {
		if !strings.Contains(err.Error(), w) {
			t.Fatalf("missing %q in %v", w, err)
		}
	}
	if fk.pushes != 0 {
		t.Fatal("something was pushed")
	}
}

func TestRevokeUsersAcceptsAnOperatorSigningKeyIssuer(t *testing.T) {
	ks, fk, _ := setup(t)
	fk.acks, fk.applyPush = []ServerAck{{Server: "s1", Code: 200}}, true
	sk, _ := nkeys.CreateOperator()
	resolverToken(t, fk, ks, sk, nil)
	u := userKey(t)
	if _, err := RevokeUsers(context.Background(), RevokeParams{Keys: ks, Resolver: fk}, []string{u}); err == nil {
		t.Fatal("an undeclared signing key must be refused")
	}
	if _, err := ks.InstallOperatorJWT(operatorJWT(t, ks, pubOf(t, sk))); err != nil {
		t.Fatal(err)
	}
	if _, err := RevokeUsers(context.Background(), RevokeParams{Keys: ks, Resolver: fk}, []string{u}); err != nil {
		t.Fatal(err)
	}
	if !revokedIn(t, fk.token, u) {
		t.Fatal("not revoked")
	}
}

// launder is a resolver that, once something was pushed, answers with a JWT issued by a foreign key.
type launder struct {
	*fake
	foreign string
}

func (l *launder) LookupAll(ctx context.Context, a string, n int) ([]string, error) {
	toks, err := l.fake.LookupAll(ctx, a, n)
	l.fake.mu.Lock()
	pushed := l.fake.pushes > 0
	l.fake.mu.Unlock()
	if pushed {
		return []string{l.foreign}, err
	}
	return toks, err
}

func TestRevokeUsersReadBackVerifiesTheIssuer(t *testing.T) {
	ks, fk, _ := setup(t)
	fk.acks, fk.applyPush = []ServerAck{{Server: "s1", Code: 200}}, true
	u := userKey(t)
	// a foreign-issued JWT that does show the revocation: the read-back must still not accept it
	foreign, _ := nkeys.CreateOperator()
	cl, _ := jwt.DecodeAccountClaims(fk.token)
	cl.Revocations = jwt.RevocationList{}
	cl.Revocations.Revoke(u, revokeAt)
	ftok, err := cl.Encode(foreign)
	if err != nil {
		t.Fatal(err)
	}
	r := &launder{fake: fk, foreign: ftok}
	_, err = RevokeUsers(context.Background(), RevokeParams{Keys: ks, Resolver: r, VerifyTries: 1, Now: func() time.Time { return revokeAt }}, []string{u})
	if !errors.Is(err, ErrNotConfirmed) || !strings.Contains(err.Error(), "not an accepted issuer") {
		t.Fatalf("%v", err)
	}
}
