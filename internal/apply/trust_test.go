package apply

import (
	"context"
	"strings"
	"testing"

	"github.com/nats-io/jwt/v2"
	"github.com/nats-io/nkeys"

	"github.com/bogworks/mash/internal/ctlkeys"
)

func pubOf(t *testing.T, kp nkeys.KeyPair) string {
	t.Helper()
	p, err := kp.PublicKey()
	if err != nil {
		t.Fatal(err)
	}
	return p
}

// resolverToken sets the fake's JWT to claims after mutate, signed by signer.
func resolverToken(t *testing.T, fk *fake, ks *ctlkeys.Authority, signer nkeys.KeyPair, mutate func(*jwt.AccountClaims)) {
	t.Helper()
	cl, err := jwt.DecodeAccountClaims(fk.token)
	if err != nil {
		t.Fatal(err)
	}
	if mutate != nil {
		mutate(cl)
	}
	if fk.token, err = cl.Encode(signer); err != nil {
		t.Fatal(err)
	}
}

// refusedEverywhere runs every reader of the account JWT: AccountFindings, AppliedZonePolicy and RevokeUsers must each
// refuse (an error), push nothing and create nothing.
func refusedEverywhere(t *testing.T, ks *ctlkeys.Authority, fk *fake, want string) {
	t.Helper()
	ctx := context.Background()
	if _, err := AccountFindings(ctx, fk, ks, 0); err == nil || !strings.Contains(err.Error(), want) {
		t.Fatalf("AccountFindings: %v, want an error containing %q", err, want)
	}
	if _, err := AppliedZonePolicy(ctx, fk, ks, 0); err == nil || !strings.Contains(err.Error(), want) {
		t.Fatalf("AppliedZonePolicy: %v, want an error containing %q", err, want)
	}
	if _, err := RevokeUsers(ctx, RevokeParams{Keys: ks, Resolver: fk}, []string{userKey(t)}); err == nil || !strings.Contains(err.Error(), want) {
		t.Fatalf("RevokeUsers: %v, want an error containing %q", err, want)
	}
	if fk.pushes != 0 {
		t.Fatal("something was pushed")
	}
}

func TestReadersRefuseForeignIssuer(t *testing.T) {
	ks, fk := setup(t)
	foreign, _ := nkeys.CreateOperator()
	resolverToken(t, fk, ks, foreign, nil)
	refusedEverywhere(t, ks, fk, "not an accepted issuer")
	_, err := AccountFindings(context.Background(), fk, ks, 0)
	if err == nil || !strings.Contains(err.Error(), pubOf(t, foreign)) {
		t.Fatalf("the refusal does not name the issuer: %v", err)
	}
}

func TestReadersRefuseForeignSubject(t *testing.T) {
	ks, fk := setup(t)
	op, _, _ := ks.Operator()
	other, _ := nkeys.CreateAccount()
	tok, err := jwt.NewAccountClaims(pubOf(t, other)).Encode(op)
	if err != nil {
		t.Fatal(err)
	}
	fk.token = tok
	refusedEverywhere(t, ks, fk, "is for account")
}

func TestReadersRefuseTamperedJWT(t *testing.T) {
	ks, fk := setup(t)
	parts := strings.Split(fk.token, ".")
	sig := []byte(parts[2])
	i := len(sig) / 2
	if sig[i] == 'A' {
		sig[i] = 'B'
	} else {
		sig[i] = 'A'
	}
	fk.token = strings.Join([]string{parts[0], parts[1], string(sig)}, ".")
	refusedEverywhere(t, ks, fk, "valid signature")
}

func operatorJWT(t *testing.T, ks *ctlkeys.Authority, signing ...string) string {
	t.Helper()
	op, opPub, _ := ks.Operator()
	oc := jwt.NewOperatorClaims(opPub)
	oc.SigningKeys.Add(signing...)
	tok, err := oc.Encode(op)
	if err != nil {
		t.Fatal(err)
	}
	return tok
}

func TestIssuerMayBeAnOperatorSigningKey(t *testing.T) {
	ks, fk := setup(t)
	sk, _ := nkeys.CreateOperator()
	skPub := pubOf(t, sk)
	resolverToken(t, fk, ks, sk, nil)
	// without the operator JWT the signing key is not an accepted issuer
	if _, err := AccountFindings(context.Background(), fk, ks, 0); err == nil {
		t.Fatal("a signing key that the operator JWT does not list was accepted")
	}
	if _, err := ks.InstallOperatorJWT(operatorJWT(t, ks, skPub)); err != nil {
		t.Fatal(err)
	}
	if _, err := AccountFindings(context.Background(), fk, ks, 0); err != nil {
		t.Fatalf("a listed operator signing key was refused: %v", err)
	}
}

func TestOperatorJWTWithoutOurKeyIsRefused(t *testing.T) {
	ks, fk := setup(t)
	other, _ := nkeys.CreateOperator()
	oc := jwt.NewOperatorClaims(pubOf(t, other))
	tok, err := oc.Encode(other)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ks.InstallOperatorJWT(tok); err != nil {
		t.Fatal(err)
	}
	refusedEverywhere(t, ks, fk, "do not belong together")
}

func TestVerifyAccountJWT(t *testing.T) {
	op, _ := nkeys.CreateOperator()
	opPub := pubOf(t, op)
	acct, _ := nkeys.CreateAccount()
	acctPub := pubOf(t, acct)
	tok, err := jwt.NewAccountClaims(acctPub).Encode(op)
	if err != nil {
		t.Fatal(err)
	}
	trust, err := NewTrust(opPub, "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := VerifyAccountJWT(tok, acctPub, trust); err != nil {
		t.Fatal(err)
	}
	if _, err := VerifyAccountJWT(tok, opPub, trust); err == nil {
		t.Fatal("a different subject was accepted")
	}
	if _, err := VerifyAccountJWT("not.a.jwt", acctPub, trust); err == nil {
		t.Fatal("garbage was accepted")
	}
	otherTrust, _ := NewTrust(pubOf(t, func() nkeys.KeyPair { k, _ := nkeys.CreateOperator(); return k }()), "")
	if _, err := VerifyAccountJWT(tok, acctPub, otherTrust); err == nil || !strings.Contains(err.Error(), opPub) {
		t.Fatalf("a foreign issuer was accepted or not named: %v", err)
	}
}

// TestRevokeRefusesUnknownSigningKeys: RevokeUsers does not carry a signing key mashctl did not create, a scoped one
// (a zone policy of an older release) or a plain one, and names both.
func TestRevokeRefusesUnknownSigningKeys(t *testing.T) {
	ks, fk := setup(t)
	op, _, _ := ks.Operator()
	sk, _ := nkeys.CreateAccount()
	pk, _ := nkeys.CreateAccount()
	scoped, plain := pubOf(t, sk), pubOf(t, pk)
	resolverToken(t, fk, ks, op, func(cl *jwt.AccountClaims) {
		sc := jwt.NewUserScope()
		sc.Key, sc.Role = scoped, "legacy"
		cl.SigningKeys.AddScopedSigner(sc)
		cl.SigningKeys.Add(plain)
	})
	fk.acks, fk.applyPush = []ServerAck{{Server: "s1", Code: 200}}, true
	_, err := RevokeUsers(context.Background(), RevokeParams{Keys: ks, Resolver: fk}, []string{userKey(t)})
	if err == nil {
		t.Fatal("revoked over unknown signing keys")
	}
	for _, w := range []string{scoped, "role legacy", plain, "plain signing key", "will not carry signing keys it did not create", "ADR 0091"} {
		if !strings.Contains(err.Error(), w) {
			t.Fatalf("missing %q: %v", w, err)
		}
	}
	if fk.pushes != 0 {
		t.Fatal("pushed")
	}
	fs, err := AccountFindings(context.Background(), fk, ks, 0)
	if err != nil {
		t.Fatal(err)
	}
	kinds := map[string]int{}
	for _, f := range fs {
		kinds[f.Kind]++
	}
	if kinds[KindZonePolicyApplied] != 1 || kinds[KindUnknownKey] != 1 {
		t.Fatalf("findings %+v", fs)
	}
}
