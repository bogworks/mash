package main

import (
	"bytes"
	"fmt"
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

func TestIssuedCheckRefusesForeignIssuer(t *testing.T) {
	e := newApplyEnv(t).withDirectory()
	ks := otherOperatorStore(t, e)
	_, opPub, _ := ks.Operator()
	realOp, _, _ := e.ks.Operator()
	realPub, _ := realOp.PublicKey()
	e.endpoint(xy("x"), nil)
	revokeUsers = revokeUserKeys // the real revocation reads the account JWT: the stub of the package would not
	t.Cleanup(func() { revokeUsers = (&revokeStub{}).fn })
	before := e.accountJWT()
	for _, args := range [][]string{{"endpoint", "delete", "alice.g.x", "--yes"}} {
		out, errb, code := e.run(args...)
		if code != 1 || !strings.Contains(out+errb, "not an accepted issuer") || !strings.Contains(out+errb, realPub) || !strings.Contains(out+errb, opPub) {
			t.Fatalf("%v: exit %d\n%s\n%s", args, code, out, errb)
		}
		e.noSeeds(out + errb)
	}
	if e.accountJWT() != before {
		t.Fatal("the account JWT changed")
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
	if out, errb, code := e.run("issued", "check"); code == 1 && strings.Contains(out+errb, "not an accepted issuer") {
		t.Fatalf("issued check with operator.jwt: %d\n%s\n%s", code, out, errb)
	}
}

func TestAdoptRefusesForeignIssuerAndInstallsNoOperatorJWT(t *testing.T) {
	lookupWait = 1e9
	e := newApplyEnv(t)
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
	refused := func(t *testing.T, label string, code int, out, errb string) {
		t.Helper()
		all := out + errb
		if code != 2 || !strings.Contains(all, "refusing a plain nats://") || !strings.Contains(all, "insecure_plaintext") || !strings.Contains(all, "MASH_INSECURE_PLAINTEXT=1") || !strings.Contains(all, "tls://") {
			t.Fatalf("%s: exit %d, want 2 naming the override and tls://\n%s\n%s", label, code, out, errb)
		}
	}
	// a plain loopback server without the override: every admin verb and the system connection are refused
	e := newApplyEnv(t).withDirectory()
	e.env["MASH_INSECURE_PLAINTEXT"] = ""
	e.env["MASH_ADMIN_CREDS"] = e.pl.Creds
	for _, args := range [][]string{{"issued", "check"}, {"endpoint", "list"}, {"init"}} {
		out, errb, code := e.run(args...)
		refused(t, fmt.Sprint(args), code, out, errb)
	}
	// with the override the same server works, and stderr carries the warning
	e.env["MASH_INSECURE_PLAINTEXT"] = "1"
	if out, errb, code := runWithProcessStderr(e.t, e.run, "endpoint", "list"); code != 0 || !strings.Contains(errb, "mash: WARNING: plain-text connection to") {
		t.Fatalf("with the override: %d\n%s\n%s", code, out, errb)
	}
	// a non-loopback plain URL is refused without the override, before any connection; with it, it is allowed (and
	// warned about), so the failure is a connection failure, not a refusal
	e.env["MASH_SERVER"] = "nats://192.0.2.1:4222"
	for _, args := range [][]string{{"issued", "check"}, {"endpoint", "list"}} {
		e.env["MASH_INSECURE_PLAINTEXT"] = ""
		out, errb, code := e.run(args...)
		refused(t, fmt.Sprint(args)+" non-loopback", code, out, errb)
		e.env["MASH_INSECURE_PLAINTEXT"] = "1"
		out, errb, code = runWithProcessStderr(e.t, e.run, args...)
		if code == 0 || strings.Contains(out+errb, "refusing a plain") || !strings.Contains(errb, "mash: WARNING: plain-text connection to 192.0.2.1") {
			t.Fatalf("%v non-loopback with the override: %d\n%s\n%s", args, code, out, errb)
		}
	}
	// the same for adopt, which names its server on the command line
	a := newAdopter(t, newApplyEnv(t))
	a.env["MASH_INSECURE_PLAINTEXT"] = ""
	a.refuses(2, []string{"refusing a plain nats://", "insecure_plaintext", "MASH_INSECURE_PLAINTEXT=1", "tls://"})
	a.refuses(2, []string{"refusing a plain nats://", "MASH_INSECURE_PLAINTEXT=1"}, "--server", "nats://192.0.2.1:4222")
	a.env["MASH_INSECURE_PLAINTEXT"] = "1"
	if out, errb, code := runWithProcessStderr(a.t, a.adopt, "--server", "nats://192.0.2.1:4222", "--dry-run"); code == 0 || !strings.Contains(errb, "mash: WARNING: plain-text connection to 192.0.2.1") {
		t.Fatalf("adopt non-loopback with the override: %d\n%s\n%s", code, out, errb)
	}
}

// runWithProcessStderr runs a verb and returns its stderr together with what it wrote to the process's own stderr,
// where the plain-text warning goes.
func runWithProcessStderr(t *testing.T, run func(args ...string) (string, string, int), args ...string) (string, string, int) {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	old := os.Stderr
	os.Stderr = w
	out, errb, code := run(args...)
	os.Stderr = old
	w.Close()
	var warn bytes.Buffer
	warn.ReadFrom(r)
	r.Close()
	return out, errb + warn.String(), code
}

func TestAdminVerbsAcceptATLSServer(t *testing.T) {
	e := testenv.StartWith(t, testenv.Options{TLS: true})
	env := map[string]string{"HOME": t.TempDir(), "MASH_SERVER": e.URL, "MASH_ENDPOINT": e.Owner + ".dev." + "x", "MASH_ADMIN_CREDS": e.AdminCreds, "MASH_CA": e.CAFile}
	var out, errb bytes.Buffer
	if code := runEnv([]string{"init"}, func(k string) string { return env[k] }, &out, &errb); code != 0 {
		t.Fatalf("init over TLS without insecure_plaintext: %d\n%s\n%s", code, out.String(), errb.String())
	}
	// the override does not turn a TLS connection off
	env["MASH_INSECURE_PLAINTEXT"] = "1"
	out.Reset()
	errb.Reset()
	if code := runEnv([]string{"endpoint", "list"}, func(k string) string { return env[k] }, &out, &errb); code != 0 {
		t.Fatalf("endpoint list over TLS with the opt-in: %d\n%s\n%s", code, out.String(), errb.String())
	}
}

// TestPlainURLRefusalExitsTwoEverywhere: every verb that connects exits 2, naming the override and tls://, when it
// is given a plain nats:// URL without the override, whatever exit code its own error path would have used. An
// ordinary connection failure with the override keeps its own exit code.
func TestPlainURLRefusalExitsTwoEverywhere(t *testing.T) {
	e := newApplyEnv(t).withDirectory()
	e.env["MASH_INSECURE_PLAINTEXT"] = ""
	for _, args := range [][]string{
		{"endpoint", "list"},
		{"endpoint", "list", "--json"},
		{"endpoint", "show", "o.g.a"},
		{"endpoint", "show", "o.g.a", "--json"},
		{"endpoint", "create", "o.g.a"},
		{"endpoint", "reissue", "o.g.a"},
		{"endpoint", "delete", "o.g.a", "--yes"},
		{"issued", "check"},
		{"audit"},

		{"init"},
	} {
		out, errb, code := e.run(args...)
		all := out + errb
		if code != 2 || !strings.Contains(all, "insecure_plaintext") || !strings.Contains(all, "tls://") {
			t.Errorf("%v: exit %d, want 2 naming insecure_plaintext and tls://\n%s\n%s", args, code, out, errb)
		}
	}
	// with the override and an address nothing listens on, the failure is an ordinary one, not a refusal
	e.env["MASH_INSECURE_PLAINTEXT"] = "1"
	e.env["MASH_SERVER"] = "nats://127.0.0.1:1"
	out, errb, code := e.run("endpoint", "list")
	if code != 1 || strings.Contains(out+errb, "refusing a plain") {
		t.Fatalf("unreachable server with the override: exit %d, want its ordinary 6\n%s\n%s", code, out, errb)
	}
	t.Logf("endpoint list against a down server with the override exits %d", code)
}
