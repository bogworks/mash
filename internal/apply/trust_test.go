package apply

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/nats-io/jwt/v2"
	"github.com/nats-io/nkeys"

	"github.com/bogworks/mash/internal/ctlkeys"
	"github.com/bogworks/mash/internal/policy"
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

// refusedEverywhere runs every mode and AccountFindings: each must refuse (exit 1 or an error), push nothing and
// create no zone key.
func refusedEverywhere(t *testing.T, ks *ctlkeys.Authority, fk *fake, c Params, want string) {
	t.Helper()
	for _, mode := range []Mode{Apply, DryRun, Diff} {
		var out bytes.Buffer
		p := c
		p.Mode, p.Keys, p.Resolver = mode, ks, fk
		if code := Run(context.Background(), p, &out); code != 1 || !strings.Contains(out.String(), want) {
			t.Fatalf("%s: exit %d, want a refusal containing %q:\n%s", mode, code, want, out.String())
		}
	}
	p := c
	p.Keys, p.Resolver = ks, fk
	if _, err := AccountFindings(context.Background(), p); err == nil || !strings.Contains(err.Error(), want) {
		t.Fatalf("AccountFindings: %v, want an error containing %q", err, want)
	}
	if fk.pushes != 0 {
		t.Fatal("something was pushed")
	}
	if z, _ := ks.Zones(); len(z) != 0 {
		t.Fatalf("zone keys were created: %v", z)
	}
}

func TestApplyRefusesForeignIssuer(t *testing.T) {
	ks, fk, c := setup(t)
	foreign, _ := nkeys.CreateOperator()
	resolverToken(t, fk, ks, foreign, nil)
	refusedEverywhere(t, ks, fk, Params{Compiled: c}, "not an accepted issuer")
	if !strings.Contains(fkErr(t, ks, fk, c), pubOf(t, foreign)) {
		t.Fatal("the refusal does not name the issuer")
	}
}

func fkErr(t *testing.T, ks *ctlkeys.Authority, fk *fake, c *policy.Compiled) string {
	t.Helper()
	var out bytes.Buffer
	Run(context.Background(), Params{Mode: DryRun, Compiled: c, Keys: ks, Resolver: fk}, &out)
	return out.String()
}

func TestApplyRefusesForeignSubject(t *testing.T) {
	ks, fk, c := setup(t)
	op, _, _ := ks.Operator()
	other, _ := nkeys.CreateAccount()
	tok, err := jwt.NewAccountClaims(pubOf(t, other)).Encode(op)
	if err != nil {
		t.Fatal(err)
	}
	fk.token = tok
	refusedEverywhere(t, ks, fk, Params{Compiled: c}, "is for account")
}

func TestApplyRefusesTamperedJWT(t *testing.T) {
	ks, fk, c := setup(t)
	parts := strings.Split(fk.token, ".")
	sig := []byte(parts[2])
	i := len(sig) / 2
	if sig[i] == 'A' {
		sig[i] = 'B'
	} else {
		sig[i] = 'A'
	}
	fk.token = strings.Join([]string{parts[0], parts[1], string(sig)}, ".")
	refusedEverywhere(t, ks, fk, Params{Compiled: c}, "valid signature")
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
	ks, fk, c := setup(t)
	sk, _ := nkeys.CreateOperator()
	skPub := pubOf(t, sk)
	resolverToken(t, fk, ks, sk, nil)
	// without the operator JWT the signing key is not an accepted issuer
	var out bytes.Buffer
	if code := Run(context.Background(), Params{Mode: DryRun, Compiled: c, Keys: ks, Resolver: fk}, &out); code != 1 {
		t.Fatalf("exit %d\n%s", code, out.String())
	}
	if _, err := ks.InstallOperatorJWT(operatorJWT(t, ks, skPub)); err != nil {
		t.Fatal(err)
	}
	out.Reset()
	if code := Run(context.Background(), Params{Mode: DryRun, Compiled: c, Keys: ks, Resolver: fk}, &out); code != 0 {
		t.Fatalf("exit %d\n%s", code, out.String())
	}
}

func TestOperatorJWTWithoutOurKeyIsRefused(t *testing.T) {
	ks, fk, c := setup(t)
	other, _ := nkeys.CreateOperator()
	oc := jwt.NewOperatorClaims(pubOf(t, other))
	tok, err := oc.Encode(other)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ks.InstallOperatorJWT(tok); err != nil {
		t.Fatal(err)
	}
	refusedEverywhere(t, ks, fk, Params{Compiled: c}, "do not belong together")
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

// unknownSetup puts one unknown scoped and one unknown plain signing key into the resolver's JWT.
func unknownSetup(t *testing.T) (ks *ctlkeys.Authority, fk *fake, c Params, scoped, plain string, claims string) {
	t.Helper()
	ks, fk, comp := setup(t)
	op, _, _ := ks.Operator()
	sk, _ := nkeys.CreateAccount()
	pk, _ := nkeys.CreateAccount()
	scoped, plain = pubOf(t, sk), pubOf(t, pk)
	resolverToken(t, fk, ks, op, func(cl *jwt.AccountClaims) {
		sc := jwt.NewUserScope()
		sc.Key, sc.Role = scoped, "legacy"
		cl.SigningKeys.AddScopedSigner(sc)
		cl.SigningKeys.Add(plain)
	})
	return ks, fk, Params{Compiled: comp}, scoped, plain, fk.token
}

func TestApplyRefusesUnknownSigningKeys(t *testing.T) {
	ks, fk, c, scoped, plain, _ := unknownSetup(t)
	fk.acks, fk.applyPush = []ServerAck{{Server: "s1", Code: 200}}, true
	for _, mode := range []Mode{Apply, DryRun} {
		var out bytes.Buffer
		p := c
		p.Mode, p.Keys, p.Resolver = mode, ks, fk
		s := ""
		code := Run(context.Background(), p, &out)
		s = out.String()
		for _, w := range []string{scoped, "role legacy", plain, "plain signing key", "will not carry signing keys it did not create", "tool that added it"} {
			if !strings.Contains(s, w) {
				t.Fatalf("%s: missing %q:\n%s", mode, w, s)
			}
		}
		if code != 1 {
			t.Fatalf("%s: exit %d", mode, code)
		}
	}
	if fk.pushes != 0 {
		t.Fatal("pushed")
	}
	if z, _ := ks.Zones(); len(z) != 0 {
		t.Fatalf("created zone keys %v", z)
	}
	// diff refuses with exit 1; drift's findings carry the same text
	var out bytes.Buffer
	p := c
	p.Mode, p.Keys, p.Resolver = Diff, ks, fk
	if code := Run(context.Background(), p, &out); code != 1 || !strings.Contains(out.String(), scoped) || !strings.Contains(out.String(), plain) {
		t.Fatalf("diff: %d\n%s", code, out.String())
	}
	p.Mode = 0
	fs, err := AccountFindings(context.Background(), p)
	if err != nil {
		t.Fatal(err)
	}
	n := 0
	for _, f := range fs {
		if f.Kind == KindUnknownKey {
			n++
			if !strings.Contains(f.Detail, "will not carry signing keys it did not create") {
				t.Fatalf("finding text: %q", f.Detail)
			}
		}
	}
	if n != 2 {
		t.Fatalf("%d unknown-key findings: %+v", n, fs)
	}
}

func TestOutputHoldsNoSeeds(t *testing.T) {
	ks, fk, c, _, _, _ := unknownSetup(t)
	fk.acks, fk.applyPush = []ServerAck{{Server: "s1", Code: 200}}, true
	var out bytes.Buffer
	p := c
	p.Mode, p.Keys, p.Resolver = Apply, ks, fk
	Run(context.Background(), p, &out)
	for _, get := range []func() (nkeys.KeyPair, string, error){ks.Operator, ks.Account} {
		kp, _, _ := get()
		seed, _ := kp.Seed()
		if bytes.Contains(out.Bytes(), seed) {
			t.Fatal("a seed is in the output")
		}
	}
}
