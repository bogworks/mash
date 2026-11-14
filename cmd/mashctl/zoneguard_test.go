package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A zone policy of an older release is applied (the account JWT carries a scoped signing key): add and reissue would mint
// owner-wide permissions that bypass its mail restrictions, so they refuse and change nothing (ADR 0091).
func TestAddAndReissueRefuseWhileAZonePolicyIsApplied(t *testing.T) {
	e := newXYEnv(t)
	adm, ctx, closeAdm := e.admin()
	defer closeAdm()
	issuedBefore, _, err := adm.Issued(ctx)
	if err != nil {
		t.Fatal(err)
	}
	credsBefore, err := os.ReadFile(e.credsPath("x"))
	if err != nil {
		t.Fatal(err)
	}
	recsBefore, _ := adm.List(ctx)
	e.applyZonePolicy()
	jwtBefore := e.accountJWT()

	out, errb, code := e.run("add", "z")
	if code != 1 || !strings.Contains(errb, "a zone policy is applied") || !strings.Contains(errb, "nothing was changed") || strings.Contains(out, "added") {
		t.Fatalf("add: %d\n%s\n%s", code, out, errb)
	}
	if _, err := os.Lstat(e.credsPath("z")); err == nil {
		t.Fatal("a refused add wrote a creds file")
	}
	for _, args := range [][]string{{"reissue", "x"}, {"reissue", "--all", "--yes"}, {"reissue", "--changed"}} {
		out, errb, code = e.run(args...)
		if args[1] == "--changed" { // nothing is stale and no key is revoked: nothing would be minted
			if code != 0 || !strings.Contains(out, "nothing to reissue") {
				t.Fatalf("%v: %d\n%s\n%s", args, code, out, errb)
			}
			continue
		}
		if code != 1 || !strings.Contains(errb, "a zone policy is applied") || strings.Contains(out, "reissued") {
			t.Fatalf("%v: %d\n%s\n%s", args, code, out, errb)
		}
	}
	credsAfter, _ := os.ReadFile(e.credsPath("x"))
	if string(credsAfter) != string(credsBefore) {
		t.Fatal("a refused reissue changed the creds file")
	}
	if _, err := os.Lstat(e.credsPath("x") + ".next"); err == nil {
		t.Fatal("a refused reissue staged a rotation")
	}
	issuedAfter, _, _ := adm.Issued(ctx)
	if len(issuedAfter) != len(issuedBefore) {
		t.Fatalf("issue record: %d entries, was %d", len(issuedAfter), len(issuedBefore))
	}
	recsAfter, _ := adm.List(ctx)
	if len(recsAfter) != len(recsBefore) {
		t.Fatalf("directory: %d records, was %d", len(recsAfter), len(recsBefore))
	}
	if e.accountJWT() != jwtBefore {
		t.Fatal("a refused reissue changed the account JWT (a revocation)")
	}
}

// With no system credentials the check cannot be made: add and reissue refuse, change nothing and have no bypass.
func TestAddAndReissueWithoutSysCredsRefuse(t *testing.T) {
	e := newXYEnv(t)
	credsBefore, _ := os.ReadFile(e.credsPath("x"))
	// admin creds outside the server directory, so no default sys.creds is found next to them
	raw, err := os.ReadFile(e.pl.Creds)
	if err != nil {
		t.Fatal(err)
	}
	alone := filepath.Join(t.TempDir(), "admin.creds")
	if err := os.WriteFile(alone, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	e.env["MASH_SYS_CREDS"], e.env["MASH_ADMIN_CREDS"] = "", alone

	out, errb, code := e.run("add", "z")
	if code != 1 || !strings.Contains(errb, "cannot check whether a zone policy is applied") || !strings.Contains(errb, "nothing was changed") || strings.Contains(out, "added") {
		t.Fatalf("add: %d\n%s\n%s", code, out, errb)
	}
	if _, err := os.Lstat(e.credsPath("z")); err == nil {
		t.Fatal("a refused add wrote a creds file")
	}
	out, errb, code = e.run("reissue", "x")
	if code != 1 || !strings.Contains(errb, "cannot check whether a zone policy is applied") || strings.Contains(out, "reissued") {
		t.Fatalf("reissue: %d\n%s\n%s", code, out, errb)
	}
	if after, _ := os.ReadFile(e.credsPath("x")); string(after) != string(credsBefore) {
		t.Fatal("a refused reissue changed the creds file")
	}
	if _, errb, code = e.run("add", "z", "--no-policy-check"); code != 2 || !strings.Contains(errb, "unknown") {
		t.Fatalf("--no-policy-check: %d %s", code, errb)
	}
}
