package apply

import (
	"bytes"
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nats-io/jwt/v2"
	"github.com/nats-io/nkeys"

	"github.com/bogworks/mash/internal/ctlkeys"
	"github.com/bogworks/mash/internal/policy"
)

// fake is an in-memory resolver.
type fake struct {
	token     string
	acks      []ServerAck
	pushErr   error
	applyPush bool // a push replaces token
	pushes    int
}

func (f *fake) Lookup(context.Context, string) (string, error) { return f.token, nil }
func (f *fake) Push(_ context.Context, tok string) ([]ServerAck, error) {
	f.pushes++
	if f.applyPush {
		f.token = tok
	}
	return f.acks, f.pushErr
}

func setup(t *testing.T) (*ctlkeys.Authority, *fake, *policy.Compiled) {
	t.Helper()
	ks, err := ctlkeys.Open(filepath.Join(t.TempDir(), "keys"))
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range []func() (string, bool, error){ks.InitOperator, ks.InitAccount} {
		if _, _, err := f(); err != nil {
			t.Fatal(err)
		}
	}
	op, _, _ := ks.Operator()
	_, apub, _ := ks.Account()
	tok, err := jwt.NewAccountClaims(apub).Encode(op)
	if err != nil {
		t.Fatal(err)
	}
	f, err := policy.Parse([]byte("default: deny\nendpoints:\n  x: {labels: {zone: a}}\n  y: {labels: {zone: b}}\nrules:\n  - {name: r, action: allow, from: {zone: a}, to: {zone: b}}\n"))
	if err != nil {
		t.Fatal(err)
	}
	c, err := policy.Compile(f, "alice")
	if err != nil {
		t.Fatal(err)
	}
	return ks, &fake{token: tok}, c
}

func TestBudgetRefusalChangesNothing(t *testing.T) {
	ks, fk, c := setup(t)
	var out bytes.Buffer
	code := Run(context.Background(), Params{Mode: Apply, Compiled: c, Keys: ks, Resolver: fk,
		Budget: func(*policy.Compiled) (policy.BudgetReport, error) {
			return policy.BudgetReport{}, errors.New("policy: the account JWT would be too big")
		}}, &out)
	if code != 1 || !strings.Contains(out.String(), "refusing") || !strings.Contains(out.String(), "too big") {
		t.Fatalf("%d %s", code, out.String())
	}
	if fk.pushes != 0 {
		t.Fatal("pushed")
	}
	if z, _ := ks.Zones(); len(z) != 0 {
		t.Fatalf("created keys %v", z)
	}
}

func TestPartialWhenNotAcknowledgedOrNotVerified(t *testing.T) {
	cases := []struct {
		name string
		fk   func(*fake)
		want string
	}{
		{"no ack", func(f *fake) { f.pushErr = errors.New("no server answered the claims update in time") }, "not acknowledged"},
		{"a server refuses", func(f *fake) {
			f.acks = []ServerAck{{Server: "s1", Code: 200}, {Server: "s2", Code: 500, Message: "boom"}}
		}, "refused by s2: code 500 boom"},
		{"acked but the resolver shows the old claims", func(f *fake) { f.acks = []ServerAck{{Server: "s1", Code: 200}} }, "missing signing keys"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ks, fk, c := setup(t)
			tc.fk(fk)
			var out bytes.Buffer
			code := Run(context.Background(), Params{Mode: Apply, Compiled: c, Keys: ks, Resolver: fk, VerifyTries: 2, VerifyWait: 1}, &out)
			s := out.String()
			if code != 1 || !strings.Contains(s, "PARTIAL") || !strings.Contains(s, tc.want) || !strings.Contains(s, "mashctl policy apply") || !strings.Contains(s, "zone keys created locally: a, b") {
				t.Fatalf("%d\n%s", code, s)
			}
		})
	}
}

func TestAckedAndVerifiedThenNoOp(t *testing.T) {
	ks, fk, c := setup(t)
	fk.acks, fk.applyPush = []ServerAck{{Server: "s1", Code: 200}}, true
	var out bytes.Buffer
	if code := Run(context.Background(), Params{Mode: Apply, Compiled: c, Keys: ks, Resolver: fk}, &out); code != 0 || fk.pushes != 1 {
		t.Fatalf("%d %d\n%s", code, fk.pushes, out.String())
	}
	out.Reset()
	if code := Run(context.Background(), Params{Mode: Apply, Compiled: c, Keys: ks, Resolver: fk}, &out); code != 0 || fk.pushes != 1 || !strings.Contains(out.String(), "already up to date") {
		t.Fatalf("%d %d\n%s", code, fk.pushes, out.String())
	}
}

func TestDryRunCreatesNoKeysAndPreservesOtherSigners(t *testing.T) {
	ks, fk, c := setup(t)
	// the account already has a plain signing key and an unrelated scoped one
	op, _, _ := ks.Operator()
	_, apub, _ := ks.Account()
	cl, _ := jwt.DecodeAccountClaims(fk.token)
	plain, _ := nkeys.CreateAccount()
	pp, _ := plain.PublicKey()
	cl.SigningKeys.Add(pp)
	other, _ := nkeys.CreateAccount()
	op2, _ := other.PublicKey()
	sc := jwt.NewUserScope()
	sc.Key, sc.Role = op2, "legacy"
	cl.SigningKeys.AddScopedSigner(sc)
	tok, err := cl.Encode(op)
	if err != nil {
		t.Fatal(err)
	}
	fk.token = tok
	_ = apub
	var out bytes.Buffer
	if code := Run(context.Background(), Params{Mode: DryRun, Compiled: c, Keys: ks, Resolver: fk}, &out); code != 0 {
		t.Fatal(out.String())
	}
	s := out.String()
	if !strings.Contains(s, "zone legacy has a key on the server but is not in the file") || !strings.Contains(s, "would create its signing key") {
		t.Fatalf("%s", s)
	}
	if z, _ := ks.Zones(); len(z) != 0 || fk.pushes != 0 {
		t.Fatal("dry run changed something")
	}
}
