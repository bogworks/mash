package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nats-io/jwt/v2"
	"github.com/nats-io/nkeys"

	"github.com/bogworks/mash/internal/ctlkeys"
	"github.com/bogworks/mash/internal/testenv"
)

// otherOperatorStore moves e to a key store that holds the account key but another operator key, so the server's
// account JWT (issued by the original operator key) is from an issuer this store does not accept.
func otherOperatorStore(t *testing.T, e *applyEnv) *ctlkeys.Authority {
	t.Helper()
	dir := filepath.Join(e.root, "keys2")
	ks, err := ctlkeys.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(filepath.Join(e.keyDir, "account.nk"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "account.nk"), b, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := ks.InitOperator(); err != nil {
		t.Fatal(err)
	}
	e.env["MASH_KEYS_DIR"] = dir
	return ks
}

func TestApplyAndDriftRefuseForeignIssuer(t *testing.T) {
	t.Skip("TODO(Z2): applies a zoned policy, which ADR 0091 refuses before anything is pushed; delete with the policy stack")
	e := newApplyEnv(t, twoZones).withDirectory()
	ks := otherOperatorStore(t, e)
	_, opPub, _ := ks.Operator()
	realOp, _, _ := e.ks.Operator()
	realPub, _ := realOp.PublicKey()
	before := e.accountJWT()
	for _, args := range [][]string{{"policy", "apply"}, {"policy", "apply", "--dry-run"}, {"policy", "diff"}, {"policy", "repair"}, {"policy", "repair", "--dry-run"}} {
		out, errb, code := e.run(args...)
		if code != 1 || !strings.Contains(out+errb, "not an accepted issuer") || !strings.Contains(out+errb, realPub) || !strings.Contains(out+errb, opPub) {
			t.Fatalf("%v: exit %d\n%s\n%s", args, code, out, errb)
		}
		e.noSeeds(out + errb)
	}
	if e.accountJWT() != before {
		t.Fatal("the account JWT changed")
	}
	if z, _ := ks.Zones(); len(z) != 0 {
		t.Fatalf("zone keys were created: %v", z)
	}
	// drift cannot trust the JWT either
	out, _, code := e.run("policy", "drift", "--json")
	if code != 1 || !strings.Contains(out, "not an accepted issuer") {
		t.Fatalf("drift: %d\n%s", code, out)
	}
	// declaring the real operator as the identity of an operator JWT that lists our key makes the issuer acceptable
	oc := jwt.NewOperatorClaims(realPub)
	oc.SigningKeys.Add(opPub)
	tok, err := oc.Encode(realOp)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ks.InstallOperatorJWT(tok); err != nil {
		t.Fatal(err)
	}
	if out, errb, code := e.run("policy", "diff"); code != 0 {
		t.Fatalf("diff with operator.jwt: %d\n%s\n%s", code, out, errb)
	}
}

func TestAdoptRefusesForeignIssuerAndInstallsNoOperatorJWT(t *testing.T) {
	lookupWait = 1e9
	e := newApplyEnv(t, twoZones)
	a := newAdopter(t, e)
	realPub := mustPub(t, a.opSeed)

	// another operator key: the account JWT was issued by a key this operator key does not stand for
	other, _ := nkeys.CreateOperator()
	seed, _ := other.Seed()
	otherSeed := filepath.Join(a.root, "in", "other-op.seed")
	if err := os.WriteFile(otherSeed, append(seed, '\n'), 0o600); err != nil {
		t.Fatal(err)
	}
	otherPub, _ := other.PublicKey()
	a.refuses(1, []string{"not an accepted issuer", realPub, otherPub, "nothing was changed"}, "--operator-key", otherSeed)
	a.refuses(2, []string{"--operator-jwt"}, "--operator-key", otherSeed, "--operator-jwt", otherSeed)

	// the operator key that issued the JWT: accepted, and no operator.jwt is installed
	if out, errb, code := a.adopt(); code != 0 {
		t.Fatalf("adopt: %d\n%s\n%s", code, out, errb)
	}
	if _, err := os.Stat(filepath.Join(a.env["MASH_KEYS_DIR"], "operator.jwt")); err == nil {
		t.Fatal("adopt installed an operator.jwt")
	}
}

func TestAdminAndSystemConnectionsRequireTLS(t *testing.T) {
	t.Skip("TODO(Z2): applies a zoned policy, which ADR 0091 refuses before anything is pushed; delete with the policy stack")
	// a plain loopback server without the opt-in: every admin verb and the system connection are refused
	e := newApplyEnv(t, twoZones)
	e.env["MASH_INSECURE_LOOPBACK"] = ""
	e.env["MASH_ADMIN_CREDS"] = e.pl.Creds
	for _, args := range [][]string{{"policy", "diff"}, {"policy", "apply"}, {"policy", "drift"}, {"list"}, {"init"}} {
		out, errb, code := e.run(args...)
		if code == 0 || !strings.Contains(out+errb, "secure connection not available") {
			t.Fatalf("%v: %d\n%s\n%s", args, code, out, errb)
		}
	}
	// with the opt-in the same server works
	e.env["MASH_INSECURE_LOOPBACK"] = "1"
	if out, errb, code := e.run("policy", "diff"); code != 0 {
		t.Fatalf("with the opt-in: %d\n%s\n%s", code, out, errb)
	}
	// a non-loopback plain URL is refused before any connection, with or without the opt-in
	for _, optIn := range []string{"", "1"} {
		e.env["MASH_INSECURE_LOOPBACK"] = optIn
		e.env["MASH_URL"] = "nats://192.0.2.1:4222"
		for _, args := range [][]string{{"policy", "diff"}, {"list"}} {
			out, errb, code := e.run(args...)
			if code == 0 || !(strings.Contains(out+errb, "refusing a plain nats://") || strings.Contains(out+errb, "allows a plain connection only to localhost")) {
				t.Fatalf("%v opt-in %q: %d\n%s\n%s", args, optIn, code, out, errb)
			}
		}
	}
	// the same for adopt, which names its server on the command line
	a := newAdopter(t, newApplyEnv(t, twoZones))
	a.env["MASH_INSECURE_LOOPBACK"] = ""
	a.refuses(1, []string{"secure connection not available"})
	a.env["MASH_INSECURE_LOOPBACK"] = "1"
	a.refuses(1, []string{"allows a plain connection only to localhost"}, "--server", "nats://192.0.2.1:4222")
}

func TestAdminVerbsAcceptATLSServer(t *testing.T) {
	e := testenv.StartWith(t, testenv.Options{TLS: true})
	env := map[string]string{"HOME": t.TempDir(), "MASH_URL": e.URL, "MASH_OWNER": e.Owner, "MASH_ADMIN_CREDS": e.AdminCreds, "MASH_TLS_CA": e.CAFile}
	var out, errb bytes.Buffer
	if code := runEnv([]string{"init"}, func(k string) string { return env[k] }, &out, &errb); code != 0 {
		t.Fatalf("init over TLS without insecure_loopback: %d\n%s\n%s", code, out.String(), errb.String())
	}
	// the loopback opt-in does not turn a TLS connection off
	env["MASH_INSECURE_LOOPBACK"] = "1"
	out.Reset()
	errb.Reset()
	if code := runEnv([]string{"list"}, func(k string) string { return env[k] }, &out, &errb); code != 0 {
		t.Fatalf("list over TLS with the opt-in: %d\n%s\n%s", code, out.String(), errb.String())
	}
}
