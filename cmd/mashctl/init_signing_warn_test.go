package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The init warning of #1255 (REQ-038): the account JWT lists signing keys, the setup file has no signing_key and none
// is stored. init cannot read the operator's strict_signing_key_usage, so the listed keys are its only sign.
// warningFor is the line for a setup file at path (init names the file as it was given).
func warningFor(path string) string {
	return "warning: no signing_key in " + path + ", but the account lists signing keys\n"
}

// strictInitWith runs init on the strict server (its account lists a signing key). The admin user is signed by that
// signing key, which the strict server needs. withSigning gives the setup file signing_key.
func strictInitWith(t *testing.T, withSigning bool) (a *initer, out, errb string, code int) {
	t.Helper()
	e, sk, osk := strictEnv(t)
	a = newIniter(t, e)
	if err := os.WriteFile(a.opSeed, mustRead(t, seedFile(t, t.TempDir(), "op", osk)), 0o600); err != nil {
		t.Fatal(err)
	}
	sc := filepath.Join(a.root, "in", "signer.creds")
	signingKeyCreds(t, e.acctPub, sk, sc)
	a.vals["admin_creds"] = sc
	if withSigning {
		a.vals["signing_key"] = seedFile(t, filepath.Join(a.root, "in"), "signing.seed", sk)
	}
	out, errb, code = a.init()
	return a, out, errb, code
}

func TestInitWarnsNoSigningKeyWhenAccountListsOne(t *testing.T) {
	a, out, errb, code := strictInitWith(t, false)
	if code != 0 || errb != warningFor(a.setupPath()) || !strings.HasPrefix(out, "Initialized mash on ") {
		t.Fatalf("init: %d\n%q\n%q", code, out, errb)
	}
	// a second run is a rerun with nothing stored: it says it once more, and only once
	if _, errb, code := a.init(); code != 0 || strings.Count(errb, "no signing_key") != 1 {
		t.Errorf("rerun: %d %q", code, errb)
	}
}

func TestInitNoWarnWithSigningKey(t *testing.T) {
	_, out, errb, code := strictInitWith(t, true)
	if code != 0 || errb != "" || !strings.HasPrefix(out, "Initialized mash on ") {
		t.Fatalf("init: %d\n%q\n%q", code, out, errb)
	}
}

func TestInitNoWarnWhenAccountListsNoSigningKey(t *testing.T) {
	a := newIniter(t, newApplyEnv(t))
	if out, errb, code := a.init(); code != 0 || errb != "" {
		t.Fatalf("init: %d\n%q\n%q", code, out, errb)
	}
}

func TestInitNoWarnWithStoredSigningKey(t *testing.T) {
	a, out, errb, code := strictInitWith(t, true)
	if code != 0 || errb != "" {
		t.Fatalf("first init: %d\n%q\n%q", code, out, errb)
	}
	a.vals["signing_key"] = nil
	if out, errb, code := a.init(); code != 0 || errb != "" || !strings.HasPrefix(out, "Already initialized") {
		t.Fatalf("rerun without signing_key: %d\n%q\n%q", code, out, errb)
	}
}
