package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Zones are removed (ADR 0091), so add and reissue no longer read the account JWT for a zone policy before they mint
// (ADR 0142): a zone policy of an older release left in the account JWT does not refuse them. unzone, which migrates a
// zoned record, keeps its refusal.
func TestAddAndReissueIgnoreAZonePolicyAndUnzoneKeepsItsRefusal(t *testing.T) {
	e := newXYEnv(t)
	e.applyZonePolicy()
	jwtBefore := e.accountJWT()

	if out, errb, code := e.run("add", "z"); code != 0 || !strings.Contains(out, "added z") {
		t.Fatalf("add: %d\n%s\n%s", code, out, errb)
	}
	if _, err := os.Lstat(e.credsPath("z")); err != nil {
		t.Fatalf("add wrote no creds file: %v", err)
	}
	if out, errb, code := e.run("reissue", "x"); code != 0 || !strings.Contains(out, "reissued x") {
		t.Fatalf("reissue: %d\n%s\n%s", code, out, errb)
	}
	if e.accountJWT() != jwtBefore {
		t.Fatal("add or an unchanged reissue changed the account JWT")
	}
	// unzone is unchanged: it still refuses while a zone policy is applied.
	if out, errb, code := e.run("unzone", "x"); code != 1 || !strings.Contains(errb, policyAppliedMsg) || strings.Contains(out, "unzoned") {
		t.Fatalf("unzone: %d\n%s\n%s", code, out, errb)
	}
}

// With no system credentials add still works: it reads nothing from the system account. reissue refuses for its own
// reason (it cannot tell whether the key is revoked) and unzone, which reads the account JWT for a zone policy, refuses
// and has no bypass.
func TestAddNeedsNoSysCredsReissueAndUnzoneDo(t *testing.T) {
	e := newXYEnv(t)
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

	if out, errb, code := e.run("add", "z"); code != 0 || !strings.Contains(out, "added z") {
		t.Fatalf("add: %d\n%s\n%s", code, out, errb)
	}
	// reissue still needs them for its own check, that the key is not revoked (a revoked key is never re-signed).
	if out, errb, code := e.run("reissue", "x"); code != 1 || !strings.Contains(errb, "cannot tell whether the user key") || strings.Contains(out, "reissued") {
		t.Fatalf("reissue: %d\n%s\n%s", code, out, errb)
	}
	if out, errb, code := e.run("unzone", "x"); code != 1 || !strings.Contains(errb, "cannot check whether a zone policy is applied") || strings.Contains(out, "unzoned") {
		t.Fatalf("unzone: %d\n%s\n%s", code, out, errb)
	}
}
