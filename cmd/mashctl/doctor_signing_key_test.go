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
	want := "signing key\n" + skPub + "\nis not in the key store"
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
		want := "signing key\n" + skPub + "\nis not listed in the account"
		if code != 1 || c.State != "fail" || !strings.HasPrefix(c.Detail, want) || c.Fix != nil {
			t.Fatalf("scoped=%v: %d %+v", scoped, code, c)
		}
		if strings.Contains(c.Detail, "every connected") { // the ok text of the row does not stay beside a failure
			t.Fatalf("an ok finding stays: %q", c.Detail)
		}
	}
}
