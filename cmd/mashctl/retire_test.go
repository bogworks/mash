package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bogworks/mash/internal/admin"
)

// provisionDeclared makes a directory record for a declared endpoint and returns its user key.
func (h *addEnv) provisionDeclared(name string) string {
	h.t.Helper()
	_, u := h.e.Mint(h.t, name)
	if _, errb, code := h.run("provision", name, "--user", u, "--account", h.e.Account); code != 0 {
		h.t.Fatal(errb)
	}
	return u
}

func (h *addEnv) policyText() string {
	b, _ := os.ReadFile(h.policy)
	return string(b)
}

func TestRetireRemovesRecordInboxAndEntryKeepsCreds(t *testing.T) {
	h := newFleetEnv(t, addPolicy)
	if _, errb, code := h.run("add", "c", "--zone", "red"); code != 0 {
		t.Fatal(errb)
	}
	out, errb, code := h.run("retire", "c")
	if code != 0 || !strings.HasPrefix(out, "retired c\n") {
		t.Fatalf("%d %q %q", code, out, errb)
	}
	if !strings.Contains(out, "c's user is not revoked at the server: revocation is not implemented yet: delete the creds file and rotate the zone key if the user must be cut off") {
		t.Errorf("revocation note: %q", out)
	}
	noSecrets(t, out, errb)
	if r := h.records(); len(r) != 0 {
		t.Errorf("records %v", r)
	}
	if p := h.policyText(); strings.Contains(p, "  c:") || !strings.Contains(p, "# a keeps its comment") || !strings.Contains(p, "# team x may mail any tool") {
		t.Errorf("policy:\n%s", p)
	}
	if _, err := os.Stat(h.creds("c")); err != nil {
		t.Errorf("creds were removed without --delete-creds: %v", err)
	}
}

func TestRetireDeleteCredsAndSymlink(t *testing.T) {
	h := newFleetEnv(t, addPolicy)
	if _, errb, code := h.run("add", "c"); code != 0 {
		t.Fatal(errb)
	}
	out, errb, code := h.run("retire", "c", "--delete-creds")
	if code != 0 {
		t.Fatalf("%d %q %q", code, out, errb)
	}
	if _, err := os.Lstat(h.creds("c")); !os.IsNotExist(err) {
		t.Error("creds left")
	}
	// a missing creds file is fine
	h.provisionDeclared("a")
	if _, errb, code := h.run("retire", "a", "--delete-creds"); code != 0 {
		t.Fatalf("missing creds: %q", errb)
	}

	// a symlink is never followed
	target := filepath.Join(t.TempDir(), "precious")
	os.WriteFile(target, []byte("keep"), 0o600)
	h.provisionDeclared("b")
	if err := os.Symlink(target, h.creds("b")); err != nil {
		t.Fatal(err)
	}
	out, errb, code = h.run("retire", "b", "--delete-creds")
	if code != 1 || !strings.Contains(errb, "not a regular file") || !strings.HasPrefix(out, "retired b\n") {
		t.Fatalf("%d %q %q", code, out, errb)
	}
	if b, err := os.ReadFile(target); err != nil || string(b) != "keep" {
		t.Errorf("symlink target: %q %v", b, err)
	}
	if _, err := os.Lstat(h.creds("b")); err != nil {
		t.Error("the symlink itself was removed")
	}
}

const retirePolicyTests = `default: deny
endpoints:
  a: {labels: {zone: red, team: x}}
  b: {labels: {zone: red}}
rules:
  - {name: ab, action: allow, from: {team: x}, to: {zone: red}}
tests:
  - {from: a, to: b, expect: allow}
`

func TestRetireRefusesWhileTestsNameItForceOverrides(t *testing.T) {
	h := newFleetEnv(t, retirePolicyTests)
	h.provisionDeclared("b")
	before := h.policyText()
	out, errb, code := h.run("retire", "b")
	if code != 1 || !strings.Contains(errb, "tests still name b: tests[1] a->b") || !strings.Contains(errb, "remove them first") {
		t.Fatalf("%d %q %q", code, out, errb)
	}
	if h.policyText() != before || len(h.records()) != 1 {
		t.Error("a refused retire changed something")
	}
	out, errb, code = h.run("retire", "b", "--force")
	if code != 1 || !strings.Contains(errb, "removed from the server, but still declared") {
		t.Fatalf("%d %q %q", code, out, errb)
	}
	if h.policyText() != before {
		t.Error("the unparsable edit was saved")
	}
	if len(h.records()) != 0 {
		t.Error("record left after --force")
	}
}

func TestRetireUndeclaredNeedsForce(t *testing.T) {
	h := newFleetEnv(t, addPolicy)
	h.provisionDeclared("a")
	h.provisionDeclared("zz") // provisioned, never declared
	before := h.policyText()
	_, errb, code := h.run("retire", "zz")
	if code != 1 || !strings.Contains(errb, "not declared") {
		t.Fatalf("%d %q", code, errb)
	}
	if len(h.records()) != 2 {
		t.Error("refusal removed a record")
	}
	out, errb, code := h.run("retire", "zz", "--force")
	if code != 0 || !strings.HasPrefix(out, "retired zz\n") {
		t.Fatalf("%d %q %q", code, out, errb)
	}
	if h.policyText() != before {
		t.Error("policy changed for an undeclared endpoint")
	}
	if r := h.records(); len(r) != 1 || !strings.HasPrefix(r[0], "a/") {
		t.Errorf("records %v", r)
	}
	// with no record at all: refused, and --force says so
	if _, errb, code := h.run("retire", "a2"); code != 1 || !strings.Contains(errb, "no directory record") {
		t.Fatalf("%d %q", code, errb)
	}
	out, errb, code = h.run("retire", "a2", "--force")
	if code != 0 || !strings.Contains(errb, "no directory record on the server") || strings.Contains(h.policyText(), "  a2:") {
		t.Fatalf("%d %q %q\n%s", code, out, errb, h.policyText())
	}
}

func TestRetireLeavesAReplacedRecord(t *testing.T) {
	h := newFleetEnv(t, addPolicy)
	h.provisionDeclared("b")
	before := h.policyText()
	other := ""
	afterRetireRead = func() {
		adm, _ := admin.New(h.e.Conn(t, h.e.AdminCreds), h.e.Owner)
		ctx := context.Background()
		if err := adm.Deprovision(ctx, "b"); err != nil {
			t.Error(err)
		}
		_, other = h.e.Mint(t, "b2")
		if _, err := adm.Provision(ctx, "b", other, h.e.Account, "", admin.Meta{}); err != nil {
			t.Error(err)
		}
	}
	t.Cleanup(func() { afterRetireRead = nil })
	out, errb, code := h.run("retire", "b")
	if code != 1 || !strings.Contains(errb, "nothing was changed") {
		t.Fatalf("%d %q %q", code, out, errb)
	}
	if h.policyText() != before {
		t.Error("policy changed")
	}
	if r := h.records(); len(r) != 1 || !strings.HasSuffix(r[0], "/"+other) {
		t.Errorf("the replaced record was not left in place: %v", r)
	}
}

func TestRetireServerFailureChangesNothing(t *testing.T) {
	h := newFleetEnv(t, addPolicy)
	h.provisionDeclared("b")
	before := h.policyText()
	h.env["MASH_URL"] = "nats://127.0.0.1:1" // nothing listens
	out, errb, code := h.run("retire", "b", "--delete-creds")
	if code != 1 || strings.Contains(out, "retired") {
		t.Fatalf("%d %q %q", code, out, errb)
	}
	if h.policyText() != before {
		t.Error("policy changed")
	}
}

func TestRetireSavesNothingWhenPolicyChanged(t *testing.T) {
	h := newFleetEnv(t, addPolicy)
	h.provisionDeclared("b")
	afterRetireRead = func() {
		f, _ := os.OpenFile(h.policy, os.O_APPEND|os.O_WRONLY, 0)
		f.WriteString("# edited by hand\n")
		f.Close()
	}
	t.Cleanup(func() { afterRetireRead = nil })
	out, errb, code := h.run("retire", "b")
	if code != 1 || !strings.Contains(errb, "changed since it was loaded") || !strings.Contains(errb, "already removed from the server") ||
		!strings.Contains(errb, "edit the file by hand") || strings.Contains(out, "retired") {
		t.Fatalf("%d %q %q", code, out, errb)
	}
	if !strings.Contains(h.policyText(), "  b:") || len(h.records()) != 0 {
		t.Error("state after a changed-file failure")
	}
}

func TestRetireConsequencesAndUsage(t *testing.T) {
	h := newFleetEnv(t, addPolicy)
	h.provisionDeclared("b")
	// b is the last member of zone blue: the account JWT loses a key
	out, errb, code := h.run("retire", "b")
	if code != 0 || !strings.Contains(out, "account update needed: run mashctl policy apply") {
		t.Fatalf("%d %q %q", code, out, errb)
	}
	for _, args := range [][]string{{"retire"}, {"retire", "a", "b"}, {"retire", "a", "--bogus"}} {
		if _, _, code := h.run(args...); code != 2 {
			t.Errorf("%v: %d", args, code)
		}
	}
	u, _, _ := h.run("help")
	for _, v := range []string{"mashctl retire NAME", "mashctl label NAME", "mashctl unlabel NAME"} {
		if !strings.Contains(u, v) {
			t.Errorf("usage lacks %q", v)
		}
	}
}

// newFleetEnv is newAddEnv with the signing keys of the zones the policy already declares (an add into an existing
// zone needs its key to exist: only a new zone gets one made).
func newFleetEnv(t *testing.T, src string) *addEnv {
	t.Helper()
	h := newAddEnv(t, src)
	for _, z := range []string{"red", "blue"} {
		if _, err := h.keys().ZoneKey(z, true); err != nil {
			t.Fatal(err)
		}
	}
	return h
}

// failAfterPurge makes the guarded removal fail the way a broken lease fence does: the record and inbox are gone.
func failAfterPurge(t *testing.T) {
	deprovisionIfUser = func(a *admin.Admin, ctx context.Context, name, user string) error {
		if err := a.DeprovisionIfUser(ctx, name, user); err != nil {
			return err
		}
		return errors.New("lease fence: connection lost")
	}
	t.Cleanup(func() { deprovisionIfUser = (*admin.Admin).DeprovisionIfUser })
}

func TestRetirePartialFailureIsNotReportedAsUnchanged(t *testing.T) {
	h := newFleetEnv(t, addPolicy)
	h.provisionDeclared("b")
	before := h.policyText()
	failAfterPurge(t)
	out, errb, code := h.run("retire", "b")
	if code != 1 || strings.Contains(out, "retired") {
		t.Fatalf("%d %q %q", code, out, errb)
	}
	for _, w := range []string{"PARTIAL", "mashctl deprovision b", "retire b --force", "lease fence: connection lost"} {
		if !strings.Contains(errb, w) {
			t.Errorf("stderr lacks %q: %q", w, errb)
		}
	}
	if strings.Contains(errb, "nothing was changed") {
		t.Errorf("a partial failure said nothing was changed: %q", errb)
	}
	if h.policyText() != before {
		t.Error("policy changed")
	}

	// the record is gone: a retry refuses and points at deprovision and --force
	deprovisionIfUser = (*admin.Admin).DeprovisionIfUser
	_, errb, code = h.run("retire", "b")
	if code != 1 || !strings.Contains(errb, "no directory record") || !strings.Contains(errb, "mashctl deprovision b") || !strings.Contains(errb, "--force") {
		t.Fatalf("%d %q", code, errb)
	}
	if h.policyText() != before {
		t.Error("policy changed by the retry")
	}

	// after the operator's deprovision, --force removes the policy entry
	if _, errb, code := h.run("deprovision", "b"); code != 0 {
		t.Fatal(errb)
	}
	out, errb, code = h.run("retire", "b", "--force")
	if code != 0 || !strings.HasPrefix(out, "retired b\n") || strings.Contains(h.policyText(), "  b:") {
		t.Fatalf("%d %q %q\n%s", code, out, errb, h.policyText())
	}
}

func TestRetireRefusalStillSaysNothingWasChanged(t *testing.T) {
	h := newFleetEnv(t, addPolicy)
	h.provisionDeclared("b")
	afterRetireRead = func() {
		adm, _ := admin.New(h.e.Conn(t, h.e.AdminCreds), h.e.Owner)
		ctx := context.Background()
		adm.Deprovision(ctx, "b")
		_, other := h.e.Mint(t, "b2")
		adm.Provision(ctx, "b", other, h.e.Account, "", admin.Meta{})
	}
	t.Cleanup(func() { afterRetireRead = nil })
	_, errb, code := h.run("retire", "b")
	if code != 1 || !strings.Contains(errb, "nothing was changed") || strings.Contains(errb, "PARTIAL") {
		t.Fatalf("%d %q", code, errb)
	}
}
