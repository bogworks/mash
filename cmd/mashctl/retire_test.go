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

	"github.com/nats-io/nats.go/jetstream"

	"github.com/bogworks/mash/internal/admin"
	"github.com/bogworks/mash/internal/sap"
	"github.com/bogworks/mash/internal/store"
)

// provisionDeclared makes a directory record for a declared endpoint and returns its user key.
func (h *fleetEnv) provisionDeclared(instance string) string {
	h.t.Helper()
	return provisionAs(h.t, h.e, instance, admin.Meta{})
}

func TestRetireRemovesRecordAndInboxAndSetsTheCredsAside(t *testing.T) {
	h := newFleetEnv(t)
	h.seedMeta(ad("c"), admin.Meta{Labels: map[string]string{"net": "red"}}, nil)
	out, errb, code := h.run("endpoint", "delete", "o.g.c", "--yes")
	if code != 0 || out != "Deleted endpoint o.g.c\n" || errb != "" {
		t.Fatalf("%d %q %q", code, out, errb)
	}
	noSecrets(t, out, errb)
	if r := h.records(); len(r) != 0 {
		t.Errorf("records %v", r)
	}
	if _, err := os.Lstat(h.creds(ad("c"))); !os.IsNotExist(err) {
		t.Errorf("creds still at the old name: %v", err)
	}
}

func TestRetireRenamesCredsByDefault(t *testing.T) {
	h := newFleetEnv(t)
	h.seed(ad("c"))
	want, _ := os.ReadFile(h.creds(ad("c")))
	out, errb, code := h.run("endpoint", "delete", "o.g.c", "--yes", "--detail")
	if code != 0 {
		t.Fatalf("%d %q %q", code, out, errb)
	}
	moved := h.creds(ad("c")) + ".deleted"
	if out != "Deleted endpoint o.g.c\n" {
		t.Errorf("report: %q", out)
	}
	got, err := os.ReadFile(moved)
	if err != nil || string(got) != string(want) {
		t.Fatalf("renamed content: %v", err)
	}
	if fi, _ := os.Stat(moved); fi.Mode().Perm() != 0o600 {
		t.Errorf("mode %v", fi.Mode())
	}
	if _, err := os.Lstat(h.creds(ad("c"))); !os.IsNotExist(err) {
		t.Error("old name remains")
	}
}

func TestRetireNoCredsFileIsFine(t *testing.T) {
	h := newFleetEnv(t)
	h.provisionDeclared("a")
	out, errb, code := h.run("endpoint", "delete", "o.g.a", "--yes")
	if code != 0 || strings.Contains(out, "renamed") || out != "Deleted endpoint o.g.a\n" {
		t.Fatalf("%d %q %q", code, out, errb)
	}
}

func TestRetireWithNoRecordSaysSo(t *testing.T) {
	h := newFleetEnv(t)
	_, errb, code := h.run("endpoint", "delete", "o.g.nothing-here", "--yes")
	if code != 1 || errb != "error: o.g.nothing-here does not exist\nhint: mashctl endpoint list\n" {
		t.Fatalf("%d %q", code, errb)
	}
}

func TestRetireMissingCredsFileIsFineAndSymlinkIsLeft(t *testing.T) {
	h := newFleetEnv(t)
	// a missing creds file is fine
	h.provisionDeclared("a")
	if _, errb, code := h.run("endpoint", "delete", "o.g.a", "--yes"); code != 0 {
		t.Fatalf("missing creds: %q", errb)
	}

	// a symlink is not touched, and its target is left as it was; the endpoint is deleted and the command says what is left
	targetPath, u := h.e.Mint(t, ad("b"))
	if _, err := h.adminOf().Provision(context.Background(), ad("b"), u, h.e.Account, "", admin.Meta{}); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(t.TempDir(), "precious")
	want, err := os.ReadFile(targetPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(target, want, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, h.creds(ad("b"))); err != nil {
		t.Fatal(err)
	}
	out, errb, code := h.run("endpoint", "delete", "o.g.b", "--yes")
	if code != 1 || out != "" || !strings.Contains(errb, "\nerror: o.g.b is partly deleted: its creds path is not a file\nhint: mashctl endpoint delete o.g.b\n") {
		t.Fatalf("%d %q %q", code, out, errb)
	}
	if b, err := os.ReadFile(target); err != nil || !bytes.Equal(b, want) {
		t.Errorf("symlink target: %q %v", b, err)
	}
	if _, err := os.Lstat(h.creds(ad("b"))); err != nil {
		t.Error("the symlink itself was removed")
	}
}

// With no directory record and no retirement, a creds file alone is not handled: the address does not exist and the file stays.
func TestRetireNoRecordLeavesCredsFile(t *testing.T) {
	h := newFleetEnv(t)
	if err := os.WriteFile(h.creds(ad("zz")), []byte("old"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, errb, code := h.run("endpoint", "delete", "o.g.zz", "--yes"); code != 1 || !strings.Contains(errb, "o.g.zz does not exist") {
		t.Fatalf("%d %q", code, errb)
	}
	if _, err := os.Lstat(h.creds(ad("zz"))); err != nil {
		t.Error("a refusal moved the creds file")
	}
}

// A record is retired whatever its labels say, and the others are left alone.
func TestRetireRemovesOnlyThatRecord(t *testing.T) {
	h := newFleetEnv(t)
	h.provisionDeclared("a")
	h.provisionDeclared("zz")
	out, errb, code := h.run("endpoint", "delete", "o.g.zz", "--yes")
	if code != 0 || out != "Deleted endpoint o.g.zz\n" {
		t.Fatalf("%d %q %q", code, out, errb)
	}
	if r := h.records(); len(r) != 1 || !strings.HasPrefix(r[0], "o.g.a//") {
		t.Errorf("records %v", r)
	}
}

func TestRetireLeavesAReplacedRecord(t *testing.T) {
	h := newFleetEnv(t)
	first := h.provisionDeclared("b")
	rs := stubRevoke(t)
	other := ""
	afterRetireRead = func() {
		// someone gives the record another user key between the read and the guarded delete
		adm, _ := admin.New(h.e.Conn(t, h.e.AdminCreds), h.e.Owner)
		_, other = h.e.Mint(t, ad("b2"))
		if _, err := adm.SetUser(context.Background(), ad("b"), h.e.Account, first, other); err != nil {
			t.Error(err)
		}
	}
	t.Cleanup(func() { afterRetireRead = nil })
	out, errb, code := h.run("endpoint", "delete", "o.g.b", "--yes")
	if code != 1 || !strings.Contains(errb, "error: ") || !strings.Contains(errb, "nothing was changed") {
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
	h.env["MASH_SERVER"] = "nats://127.0.0.1:1" // nothing listens
	out, errb, code := h.run("endpoint", "delete", "o.g.b", "--yes")
	if code != 1 || strings.Contains(out, "Deleted") {
		t.Fatalf("%d %q %q", code, out, errb)
	}
}

func TestRetireConsequencesAndUsage(t *testing.T) {
	h := newFleetEnv(t)
	h.provisionDeclared("b")
	out, errb, code := h.run("endpoint", "delete", "o.g.b", "--yes")
	if code != 0 || strings.Contains(out, "needed") {
		t.Fatalf("%d %q %q", code, out, errb)
	}
	for _, args := range [][]string{{"endpoint", "delete"}, {"endpoint", "delete", "o.g.a", "--yes", "b"}, {"endpoint", "delete", "o.g.a", "--yes", "--bogus"}, {"endpoint", "delete", "o.g.a", "--yes", "--policy", "p.yaml"}} {
		_, errb, code := h.run(args...)
		if code != 2 || !strings.HasPrefix(errb, "error: ") || !strings.HasSuffix(errb, "\nhint: mashctl endpoint delete --help\n") {
			t.Errorf("%v: %d %q", args, code, errb)
		}
	}
	u, _, _ := h.run("help")
	for _, v := range []string{"mashctl endpoint delete ADDRESS", "mashctl endpoint show ADDRESS", "mashctl endpoint set ADDRESS key=value...", "mashctl endpoint unset ADDRESS key...", "mashctl endpoint suspend ADDRESS", "mashctl endpoint resume ADDRESS", "mashctl endpoint inbox ADDRESS", "mashctl endpoint permissions ADDRESS", "mashctl version"} {
		if !strings.Contains(u, v) {
			t.Errorf("usage lacks %q", v)
		}
	}
}

// failAfterPurge makes the guarded removal fail the way a broken lease fence does: the record and inbox are gone.
func failAfterPurge(t *testing.T) {
	deprovisionIfUser = func(a *admin.Admin, ctx context.Context, addr sap.Address, user string) error {
		if err := a.DeprovisionIfUser(ctx, addr, user); err != nil {
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
	out, errb, code := h.run("endpoint", "delete", "o.g.b", "--yes")
	if code != 1 || out != "" {
		t.Fatalf("%d %q %q", code, out, errb)
	}
	if !strings.HasSuffix(errb, "\nerror: o.g.b is partly deleted: it could not be finished\nhint: mashctl endpoint delete o.g.b\n") || strings.Contains(errb, "connection lost") {
		t.Errorf("stderr: %q", errb)
	}
	if strings.Contains(errb, "nothing was changed") {
		t.Errorf("a partial failure said nothing was changed: %q", errb)
	}

	// the record is gone and the stand-in finished everything else: a retry finds the final marker and nothing left,
	// so the address does not exist
	deprovisionIfUser = (*admin.Admin).DeprovisionIfUser
	out, errb, code = h.run("endpoint", "delete", "o.g.b", "--yes")
	if code != 1 || out != "" || !strings.Contains(errb, "o.g.b does not exist") {
		t.Fatalf("%d %q %q", code, out, errb)
	}
}

func TestRetireRefusalStillSaysNothingWasChanged(t *testing.T) {
	h := newFleetEnv(t)
	first := h.provisionDeclared("b")
	afterRetireRead = func() {
		adm, _ := admin.New(h.e.Conn(t, h.e.AdminCreds), h.e.Owner)
		_, other := h.e.Mint(t, ad("b2"))
		adm.SetUser(context.Background(), ad("b"), h.e.Account, first, other)
	}
	t.Cleanup(func() { afterRetireRead = nil })
	_, errb, code := h.run("endpoint", "delete", "o.g.b", "--yes")
	if code != 1 || !strings.Contains(errb, "nothing was changed") || strings.Contains(errb, "deleted o.g.b") {
		t.Fatalf("%d %q", code, errb)
	}
}

func TestRetireTwiceSameSecondKeepsBothSeeds(t *testing.T) {
	h := newFleetEnv(t)
	var seeds []string
	h.seed(ad("c"))
	for i := 0; i < 2; i++ {
		if i == 1 { // a retired address cannot be made again: put another creds file at the old path
			if err := os.WriteFile(h.creds(ad("c")), []byte("second seed"), 0o600); err != nil {
				t.Fatal(err)
			}
		}
		b, _ := os.ReadFile(h.creds(ad("c")))
		seeds = append(seeds, string(b))
		if out, errb, code := h.run("endpoint", "delete", "o.g.c", "--yes"); code != 0 {
			t.Fatalf("%d %q %q", code, out, errb)
		}
	}
	first := h.creds(ad("c")) + ".deleted"
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

func TestRetireDirectoryReadFailureSaysNothingWasRevoked(t *testing.T) {
	h := newFleetEnv(t)
	h.provisionDeclared("b")
	js, err := jetstream.New(h.e.Conn(h.t, h.e.AdminCreds))
	if err != nil {
		t.Fatal(err)
	}
	if err := js.DeleteKeyValue(context.Background(), store.DirectoryBucket); err != nil {
		t.Fatal(err)
	}
	out, errb, code := h.run("endpoint", "delete", "o.g.b", "--yes")
	if code != 1 || out != "" {
		t.Fatalf("%d %q %q", code, out, errb)
	}
	if !strings.HasSuffix(strings.TrimSpace(errb), "nothing was revoked; the key is still valid") {
		t.Errorf("does not end with the not-revoked line: %q", errb)
	}
}
