package main

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

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

func TestRetireRemovesRecordAndInboxAndSetsTheCredsAside(t *testing.T) {
	h := newFleetEnv(t)
	if _, errb, code := h.run("add", "c", "--label", "net=red"); code != 0 {
		t.Fatal(errb)
	}
	out, errb, code := h.run("retire", "c")
	if code != 0 || !strings.HasPrefix(out, "retired c\n") {
		t.Fatalf("%d %q %q", code, out, errb)
	}
	if !strings.Contains(out, "revoked in the account JWT") {
		t.Errorf("revocation note: %q", out)
	}
	noSecrets(t, out, errb)
	if r := h.records(); len(r) != 0 {
		t.Errorf("records %v", r)
	}
	if _, err := os.Lstat(h.creds("c")); !os.IsNotExist(err) {
		t.Errorf("creds still at the old name: %v", err)
	}
}

func TestRetireRenamesCredsByDefault(t *testing.T) {
	h := newFleetEnv(t)
	if _, errb, code := h.run("add", "c"); code != 0 {
		t.Fatal(errb)
	}
	want, _ := os.ReadFile(h.creds("c"))
	retireNow = func() time.Time { return time.Date(2026, 11, 2, 3, 4, 5, 0, time.UTC) }
	t.Cleanup(func() { retireNow = time.Now })
	out, errb, code := h.run("retire", "c")
	if code != 0 {
		t.Fatalf("%d %q %q", code, out, errb)
	}
	moved := h.creds("c") + ".retired-20261102T030405Z"
	if !strings.Contains(out, "creds file renamed to "+moved) {
		t.Errorf("report: %q", out)
	}
	got, err := os.ReadFile(moved)
	if err != nil || string(got) != string(want) {
		t.Fatalf("renamed content: %v", err)
	}
	if fi, _ := os.Stat(moved); fi.Mode().Perm() != 0o600 {
		t.Errorf("mode %v", fi.Mode())
	}
	if _, err := os.Lstat(h.creds("c")); !os.IsNotExist(err) {
		t.Error("old name remains")
	}
	if _, errb, code := h.run("add", "c"); code != 0 {
		t.Fatalf("re-add: %q", errb)
	}
}

func TestRetireNoCredsFileIsFine(t *testing.T) {
	h := newFleetEnv(t)
	h.provisionDeclared("a")
	out, errb, code := h.run("retire", "a")
	if code != 0 || strings.Contains(out, "renamed") {
		t.Fatalf("%d %q %q", code, out, errb)
	}
}

func TestRetireWithNoRecordSaysSo(t *testing.T) {
	h := newFleetEnv(t)
	_, errb, code := h.run("retire", "nothing-here")
	if code != 1 || !strings.Contains(errb, "no directory record on the server") || !strings.Contains(errb, "mashctl deprovision nothing-here") {
		t.Fatalf("%d %q", code, errb)
	}
}

func TestRetireDeleteCredsAndSymlink(t *testing.T) {
	h := newFleetEnv(t)
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

	// a symlink is not removed, and its target is left as it was
	targetPath, u := h.e.Mint(t, "b")
	if _, errb, code := h.run("provision", "b", "--user", u, "--account", h.e.Account); code != 0 {
		t.Fatal(errb)
	}
	target := filepath.Join(t.TempDir(), "precious")
	want, err := os.ReadFile(targetPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(target, want, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, h.creds("b")); err != nil {
		t.Fatal(err)
	}
	out, errb, code = h.run("retire", "b", "--delete-creds")
	if code != 1 || !strings.Contains(errb, "not a regular file") || !strings.HasPrefix(out, "retired b\n") {
		t.Fatalf("%d %q %q", code, out, errb)
	}
	if b, err := os.ReadFile(target); err != nil || !bytes.Equal(b, want) {
		t.Errorf("symlink target: %q %v", b, err)
	}
	if _, err := os.Lstat(h.creds("b")); err != nil {
		t.Error("the symlink itself was removed")
	}
}

// With no directory record, --force handles the creds file only: nothing is revoked, and it says so.
func TestRetireForceWithNoRecordHandlesCredsOnly(t *testing.T) {
	h := newFleetEnv(t)
	if err := os.WriteFile(h.creds("zz"), []byte("old"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, errb, code := h.run("retire", "zz"); code != 1 || !strings.Contains(errb, "no directory record") {
		t.Fatalf("%d %q", code, errb)
	}
	if _, err := os.Lstat(h.creds("zz")); err != nil {
		t.Error("a refusal moved the creds file")
	}
	out, errb, code := h.run("retire", "zz", "--force", "--delete-creds")
	if code != 0 || !strings.Contains(errb, "no directory record on the server") || !strings.HasPrefix(out, "retired zz\n") {
		t.Fatalf("%d %q %q", code, out, errb)
	}
	if _, err := os.Lstat(h.creds("zz")); !os.IsNotExist(err) {
		t.Error("creds left")
	}
}

// A record is retired whatever its labels say, and the others are left alone.
func TestRetireRemovesOnlyThatRecord(t *testing.T) {
	h := newFleetEnv(t)
	h.provisionDeclared("a")
	h.provisionDeclared("zz")
	out, errb, code := h.run("retire", "zz")
	if code != 0 || !strings.HasPrefix(out, "retired zz\n") {
		t.Fatalf("%d %q %q", code, out, errb)
	}
	if r := h.records(); len(r) != 1 || !strings.HasPrefix(r[0], "a/") {
		t.Errorf("records %v", r)
	}
}

func TestRetireLeavesAReplacedRecord(t *testing.T) {
	h := newFleetEnv(t)
	h.provisionDeclared("b")
	rs := stubRevoke(t)
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
	if len(rs.calls) != 0 {
		t.Errorf("the replaced record's endpoint had a key revoked: %v", rs.calls)
	}
	if r := h.records(); len(r) != 1 || !strings.HasSuffix(r[0], "/"+other) {
		t.Errorf("the replaced record was not left in place: %v", r)
	}
}

func TestRetireServerFailureChangesNothing(t *testing.T) {
	h := newFleetEnv(t)
	h.provisionDeclared("b")
	h.env["MASH_URL"] = "nats://127.0.0.1:1" // nothing listens
	out, errb, code := h.run("retire", "b", "--delete-creds")
	if code != 1 || strings.Contains(out, "retired") {
		t.Fatalf("%d %q %q", code, out, errb)
	}
}

func TestRetireConsequencesAndUsage(t *testing.T) {
	h := newFleetEnv(t)
	h.provisionDeclared("b")
	out, errb, code := h.run("retire", "b")
	if code != 0 || strings.Contains(out, "needed") {
		t.Fatalf("%d %q %q", code, out, errb)
	}
	for _, args := range [][]string{{"retire"}, {"retire", "a", "b"}, {"retire", "a", "--bogus"}, {"retire", "a", "--policy", "p.yaml"}} {
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

// newFleetEnv is newAddEnv: there are no zone signing keys to make any more (ADR 0091).
func newFleetEnv(t *testing.T) *addEnv {
	t.Helper()
	return newAddEnv(t)
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
	h := newFleetEnv(t)
	h.provisionDeclared("b")
	failAfterPurge(t)
	out, errb, code := h.run("retire", "b")
	if code != 1 || strings.Contains(out, "retired") {
		t.Fatalf("%d %q %q", code, out, errb)
	}
	for _, w := range []string{"PARTIAL", "mashctl deprovision b", "lease fence: connection lost"} {
		if !strings.Contains(errb, w) {
			t.Errorf("stderr lacks %q: %q", w, errb)
		}
	}
	if strings.Contains(errb, "nothing was changed") {
		t.Errorf("a partial failure said nothing was changed: %q", errb)
	}

	// the record is gone: a retry refuses and points at deprovision and --force
	deprovisionIfUser = (*admin.Admin).DeprovisionIfUser
	_, errb, code = h.run("retire", "b")
	if code != 1 || !strings.Contains(errb, "no directory record") || !strings.Contains(errb, "mashctl deprovision b") || !strings.Contains(errb, "--force") {
		t.Fatalf("%d %q", code, errb)
	}

	// the stand-in removed everything, so deprovision has nothing left to finish and says so
	if _, errb, code := h.run("deprovision", "b"); code != 1 || !strings.Contains(errb, "no endpoint named b") {
		t.Fatal(code, errb)
	}
	out, errb, code = h.run("retire", "b", "--force")
	if code != 0 || !strings.HasPrefix(out, "retired b\n") {
		t.Fatalf("%d %q %q", code, out, errb)
	}
}

func TestRetireRefusalStillSaysNothingWasChanged(t *testing.T) {
	h := newFleetEnv(t)
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

func TestRetireTwiceSameSecondKeepsBothSeeds(t *testing.T) {
	h := newFleetEnv(t)
	retireNow = func() time.Time { return time.Date(2026, 11, 2, 3, 4, 5, 0, time.UTC) }
	t.Cleanup(func() { retireNow = time.Now })
	var seeds []string
	for i := 0; i < 2; i++ {
		if _, errb, code := h.run("add", "c"); code != 0 {
			t.Fatal(errb)
		}
		b, _ := os.ReadFile(h.creds("c"))
		seeds = append(seeds, string(b))
		if out, errb, code := h.run("retire", "c"); code != 0 {
			t.Fatalf("%d %q %q", code, out, errb)
		}
	}
	first := h.creds("c") + ".retired-20261102T030405Z"
	for i, p := range []string{first, first + ".1"} {
		got, err := os.ReadFile(p)
		if err != nil || string(got) != seeds[i] {
			t.Errorf("%s: %v (content kept: %v)", p, err, string(got) == seeds[i])
		}
	}
	if seeds[0] == seeds[1] {
		t.Error("seeds identical; test proves nothing")
	}
}

func TestArchiveNoReplaceNeverOverwritesAndKeepsMode(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "n.creds")
	dest := src + ".retired-x"
	if err := os.WriteFile(src, []byte("new"), 0o640); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(dest, []byte("old"), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := archiveNoReplace(src, dest)
	if err != nil || got != dest+".1" {
		t.Fatalf("%q %v", got, err)
	}
	if b, _ := os.ReadFile(dest); string(b) != "old" {
		t.Errorf("existing archive overwritten: %q", b)
	}
	if b, _ := os.ReadFile(got); string(b) != "new" {
		t.Errorf("content %q", b)
	}
	if fi, _ := os.Stat(got); fi.Mode().Perm() != 0o640 {
		t.Errorf("mode %v", fi.Mode())
	}
	if _, err := os.Lstat(src); !os.IsNotExist(err) {
		t.Error("source remains")
	}
}

func TestArchiveNoReplaceExhaustedLeavesSource(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "n.creds")
	dest := src + ".retired-x"
	os.WriteFile(src, []byte("new"), 0o600)
	os.WriteFile(dest, []byte("old"), 0o600)
	for i := 1; i <= 100; i++ {
		os.WriteFile(dest+"."+strconv.Itoa(i), []byte("old"), 0o600)
	}
	_, err := archiveNoReplace(src, dest)
	if err == nil || !strings.Contains(err.Error(), "nothing was moved") {
		t.Fatalf("err %v", err)
	}
	if b, _ := os.ReadFile(src); string(b) != "new" {
		t.Error("source lost")
	}
}

func TestCopyAndRemoveKeepsContent(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "n.creds")
	dest := src + ".retired-x"
	os.WriteFile(src, []byte("seed"), 0o640)
	out, err := os.OpenFile(dest, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o640)
	if err != nil {
		t.Fatal(err)
	}
	if err := copyAndRemove(src, out, 0o640); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(dest); string(b) != "seed" {
		t.Errorf("archive %q", b)
	}
	if _, err := os.Lstat(src); !os.IsNotExist(err) {
		t.Error("source remains")
	}
}

func TestCopyAndRemoveSyncFailureKeepsSource(t *testing.T) {
	old := syncFile
	syncFile = func(*os.File) error { return errors.New("disk on fire") }
	defer func() { syncFile = old }()
	dir := t.TempDir()
	src := filepath.Join(dir, "n.creds")
	dest := src + ".retired-x"
	os.WriteFile(src, []byte("seed"), 0o600)
	out, err := os.OpenFile(dest, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	if err := copyAndRemove(src, out, 0o600); err == nil {
		t.Fatal("expected error")
	}
	if b, _ := os.ReadFile(src); string(b) != "seed" {
		t.Error("source lost")
	}
	if _, err := os.Lstat(dest); !os.IsNotExist(err) {
		t.Error("half archive remains")
	}
}
