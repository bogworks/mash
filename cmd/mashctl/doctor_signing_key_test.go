package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nats-io/jwt/v2"
	"github.com/nats-io/nkeys"
)

// doctorKeysRowOf runs `mashctl doctor --json` on a and returns the keys row and the exit code.
func doctorKeysRowOf(t *testing.T, a *initer) (doctorCheck, int) {
	t.Helper()
	out, errb, code := a.run("doctor", "--json")
	var rep doctorReport
	if err := json.Unmarshal([]byte(out), &rep); err != nil {
		t.Fatalf("doctor --json: %v\n%s\n%s", err, out, errb)
	}
	for _, c := range rep.Checks {
		if c.Name == "keys" {
			return c, code
		}
	}
	t.Fatalf("no keys row: %s", out)
	return doctorCheck{}, 0
}

// A configured signing key that the key store and the account both have leaves the keys row as it was.
func TestDoctorKeysSigningKeyOK(t *testing.T) {
	e, sk, osk := strictEnv(t)
	a := initStrict(t, e, sk, osk, true)
	c, code := doctorKeysRowOf(t, a)
	if code != 0 || c.State != "ok" || c.Detail != "every connected user was issued by mashctl" || c.Fix != nil {
		t.Fatalf("%d %+v", code, c)
	}
}

// No signing_key_pub in the configuration: the check is skipped, without a word.
func TestDoctorKeysNoSigningKeyPubIsSilent(t *testing.T) {
	e, sk, osk := strictEnv(t)
	a := initStrict(t, e, sk, osk, false)
	c, code := doctorKeysRowOf(t, a)
	if code != 0 || c.State != "ok" || c.Detail != "every connected user was issued by mashctl" {
		t.Fatalf("%d %+v", code, c)
	}
}

// signing.nk and signing.pub gone from the key store: the row fails, with the init hint.
func TestDoctorKeysSigningKeyNotInTheKeyStore(t *testing.T) {
	e, sk, osk := strictEnv(t)
	skPub, _ := sk.PublicKey()
	a := initStrict(t, e, sk, osk, true)
	keys := a.env["MASH_KEYS_DIR"]
	for _, f := range []string{"signing.nk", "signing.pub"} {
		if err := os.Remove(filepath.Join(keys, f)); err != nil {
			t.Fatal(err)
		}
	}
	c, code := doctorKeysRowOf(t, a)
	want := "the signing key is not in the key store"
	if out, _, _ := a.run("doctor", "--json"); strings.Contains(out, skPub) || strings.Contains(out, "signing_key") {
		t.Fatalf("--json names the signing key: %s", out)
	}
	if code != 1 || c.State != "fail" || c.Detail != want || c.Fix == nil || *c.Fix != "mashctl init --config <file>" {
		t.Fatalf("%d %+v", code, c)
	}
}

// The account JWT no longer lists the key, or lists it as a scoped key: the row fails, with no hint.
func TestDoctorKeysSigningKeyNotListedInTheAccount(t *testing.T) {
	for _, scoped := range []bool{false, true} {
		e, sk, osk := strictEnv(t)
		skPub, _ := sk.PublicKey()
		a := initStrict(t, e, sk, osk, true)
		// the admin creds are signed by another plain signing key of the account, so that sign-in still works
		fk, _ := nkeys.CreateAccount()
		fkPub, _ := fk.PublicKey()
		fkCreds := filepath.Join(a.root, "in", "other.creds")
		signingKeyCreds(t, e.acctPub, fk, fkCreds)
		a.env["MASH_ADMIN_CREDS"] = fkCreds
		ac := e.claims()
		ac.SigningKeys.Add(fkPub)
		delete(ac.SigningKeys, skPub)
		if scoped {
			sc := jwt.NewUserScope()
			sc.Key, sc.Role = skPub, "legacy"
			ac.SigningKeys.AddScopedSigner(sc)
		}
		tok, err := ac.Encode(osk)
		if err != nil {
			t.Fatal(err)
		}
		pushAccountJWT(t, e, tok)
		c, code := doctorKeysRowOf(t, a)
		want := "the signing key is not listed in the account"
		if code != 1 || c.State != "fail" || !strings.HasPrefix(c.Detail, want+"\n") || c.Fix != nil {
			t.Fatalf("scoped=%v: %d %+v", scoped, code, c)
		}
		if strings.Contains(c.Detail, "every connected") { // the ok text of the row does not stay beside a failure
			t.Fatalf("an ok finding stays: %q", c.Detail)
		}
	}
}

// The account JWT cannot be read (here the account key is gone from the key store, so the lookup cannot start): the
// keys row fails rather than passing unchecked, with no hint.
func TestDoctorKeysSigningKeyAccountUnreadable(t *testing.T) {
	e, sk, osk := strictEnv(t)
	a := initStrict(t, e, sk, osk, true)
	if err := os.Remove(filepath.Join(a.env["MASH_KEYS_DIR"], "account.nk")); err != nil {
		t.Fatal(err)
	}
	c, code := doctorKeysRowOf(t, a)
	if code != 1 || c.State != "fail" || !strings.HasPrefix(c.Detail, "the account could not be read: ") || c.Fix != nil {
		t.Fatalf("%d %+v", code, c)
	}
}

// withoutSysCreds drops system_creds from the configuration a holds, so the keys row cannot read anything from the
// server's system account.
func withoutSysCreds(t *testing.T, a *initer) {
	t.Helper()
	path := filepath.Join(a.env["MASH_CONFIG_DIR"], "config.json")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatal(err)
	}
	if _, ok := m["system_creds"]; !ok {
		t.Fatalf("no system_creds in %s", raw)
	}
	delete(m, "system_creds")
	raw, _ = json.Marshal(m)
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		t.Fatal(err)
	}
}

// No system_creds: the key store part of the check still runs. A lost signing key fails the row with the init hint,
// followed by what the row found (it cannot read connected keys, and says so), and --json does not name the key.
func TestDoctorKeysSigningKeyNotInTheKeyStoreWithoutSysCreds(t *testing.T) {
	e, sk, osk := strictEnv(t)
	skPub, _ := sk.PublicKey()
	a := initStrict(t, e, sk, osk, true)
	withoutSysCreds(t, a)
	for _, f := range []string{"signing.nk", "signing.pub"} {
		if err := os.Remove(filepath.Join(a.env["MASH_KEYS_DIR"], f)); err != nil {
			t.Fatal(err)
		}
	}
	c, code := doctorKeysRowOf(t, a)
	want := "the signing key is not in the key store\nno system_creds in the configuration; connected keys were not checked"
	if code != 1 || c.State != "fail" || c.Detail != want || c.Fix == nil || *c.Fix != "mashctl init --config <file>" {
		t.Fatalf("%d %+v", code, c)
	}
	if out, _, _ := a.run("doctor", "--json"); strings.Contains(out, skPub) {
		t.Fatalf("--json names the signing key: %s", out)
	}
}

// No system_creds and the signing key in the key store: nothing to add, so the row is what it was (the warning, or
// under --audit-users the failure), and the account is not read.
func TestDoctorKeysSigningKeyInTheKeyStoreWithoutSysCreds(t *testing.T) {
	e, sk, osk := strictEnv(t)
	a := initStrict(t, e, sk, osk, true)
	withoutSysCreds(t, a)
	c, code := doctorKeysRowOf(t, a)
	if code != 0 || c.State != "warn" || c.Detail != "no system_creds in the configuration; connected keys were not checked" {
		t.Fatalf("%d %+v", code, c)
	}
	out, errb, code := a.run("doctor", "--audit-users", "--json")
	var rep doctorReport
	if err := json.Unmarshal([]byte(out), &rep); err != nil {
		t.Fatalf("%v\n%s\n%s", err, out, errb)
	}
	var got doctorCheck
	for _, ck := range rep.Checks {
		if ck.Name == "keys" {
			got = ck
		}
	}
	if code != 1 || got.State != "fail" || got.Detail != "no system_creds: connected users cannot be audited" {
		t.Fatalf("%d %+v", code, got)
	}
}
