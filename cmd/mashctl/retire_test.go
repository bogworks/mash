package main

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
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
	if err != nil || !strings.Contains(string(want), string(got)) || !strings.Contains(string(got), "BEGIN NATS USER JWT") {
		t.Fatalf("marker content: %v %q", err, got)
	}
	if strings.Contains(string(got), "SEED") {
		t.Errorf("the marker holds a seed: %q", got)
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
			if err := os.WriteFile(h.creds(ad("c")), []byte("-----BEGIN NATS USER JWT-----\neyJsecond\n------END NATS USER JWT------\n"), 0o600); err != nil {
				t.Fatal(err)
			}
		}
		b, _ := os.ReadFile(h.creds(ad("c")))
		seeds = append(seeds, string(jwtBlock(b)))
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

const markerSeed = "SUABCDEFGHIJKLMNOPQRSTUVWXYZ234567ABCDEFGHIJKLMNOPQRSTUV"

func markerCreds(jwt string) string {
	return "-----BEGIN NATS USER JWT-----\n" + jwt + "\n------END NATS USER JWT------\n\n************************* IMPORTANT *************************\nNKEY Seed printed below can be used to sign and prove identity.\n\n-----BEGIN USER NKEY SEED-----\n" + markerSeed + "\n------END USER NKEY SEED------\n\n*************************************************************\n"
}

func TestArchiveNoReplaceKeepsTheJWTAndNoSeed(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "n.creds")
	dest := src + ".deleted"
	if err := os.WriteFile(src, []byte(markerCreds("eyJhbGciOi.abc_d-e")), 0o640); err != nil {
		t.Fatal(err)
	}
	got, err := archiveNoReplace(src, dest)
	if err != nil || got != dest {
		t.Fatalf("%q %v", got, err)
	}
	fi, err := os.Lstat(got)
	if err != nil || !fi.Mode().IsRegular() {
		t.Fatalf("marker not a regular file: %v", err)
	}
	if fi.Mode().Perm() != 0o640 {
		t.Errorf("mode %v", fi.Mode())
	}
	b, _ := os.ReadFile(got)
	want := "-----BEGIN NATS USER JWT-----\neyJhbGciOi.abc_d-e\n------END NATS USER JWT------\n"
	if string(b) != want {
		t.Errorf("marker %q", b)
	}
	for _, bad := range []string{markerSeed, "SEED", "NKEY"} {
		if strings.Contains(string(b), bad) {
			t.Errorf("marker contains %q", bad)
		}
	}
	if _, err := os.Lstat(src); !os.IsNotExist(err) {
		t.Error("source remains")
	}
}

func TestArchiveNoReplaceNeverOverwrites(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "n.creds")
	dest := src + ".deleted"
	os.WriteFile(src, []byte(markerCreds("eyJnew")), 0o600)
	os.WriteFile(dest, []byte("old"), 0o600)
	got, err := archiveNoReplace(src, dest)
	if err != nil || got != dest+".1" {
		t.Fatalf("%q %v", got, err)
	}
	if b, _ := os.ReadFile(dest); string(b) != "old" {
		t.Errorf("existing marker overwritten: %q", b)
	}
	if b, _ := os.ReadFile(got); !strings.Contains(string(b), "eyJnew") || strings.Contains(string(b), markerSeed) {
		t.Errorf("content %q", b)
	}
}

func TestArchiveNoReplaceWithoutAJWTBlockIsEmpty(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "n.creds")
	dest := src + ".deleted"
	os.WriteFile(src, []byte("-----BEGIN USER NKEY SEED-----\n"+markerSeed+"\n------END USER NKEY SEED------\n"), 0o600)
	got, err := archiveNoReplace(src, dest)
	if err != nil {
		t.Fatal(err)
	}
	if fi, err := os.Stat(got); err != nil || fi.Size() != 0 {
		t.Errorf("marker %v %v", fi, err)
	}
	if _, err := os.Lstat(src); !os.IsNotExist(err) {
		t.Error("source remains")
	}
}

func TestJWTBlockIgnoresAnythingButTheBlock(t *testing.T) {
	if b := jwtBlock([]byte("-----BEGIN NATS USER JWT-----\neyJ\n" + markerSeed + " SEED\n------END NATS USER JWT------\n")); b != nil {
		t.Errorf("a block with a foreign line was copied: %q", b)
	}
	if b := jwtBlock([]byte("-----BEGIN NATS USER JWT-----\neyJ\n")); b != nil {
		t.Errorf("an unterminated block was copied: %q", b)
	}
}

func TestArchiveNoReplaceExhaustedLeavesSource(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "n.creds")
	dest := src + ".deleted"
	os.WriteFile(src, []byte(markerCreds("eyJnew")), 0o600)
	os.WriteFile(dest, []byte("old"), 0o600)
	for i := 1; i <= 100; i++ {
		os.WriteFile(dest+"."+strconv.Itoa(i), []byte("old"), 0o600)
	}
	_, err := archiveNoReplace(src, dest)
	if err == nil || !strings.Contains(err.Error(), "nothing was moved") {
		t.Fatalf("err %v", err)
	}
	if b, _ := os.ReadFile(src); string(b) != markerCreds("eyJnew") {
		t.Error("source changed or lost")
	}
}

func TestArchiveNoReplaceSyncFailureKeepsSource(t *testing.T) {
	old := syncFile
	syncFile = func(*os.File) error { return errors.New("disk on fire") }
	defer func() { syncFile = old }()
	dir := t.TempDir()
	src := filepath.Join(dir, "n.creds")
	dest := src + ".deleted"
	os.WriteFile(src, []byte(markerCreds("eyJnew")), 0o600)
	if _, err := archiveNoReplace(src, dest); err == nil {
		t.Fatal("expected error")
	}
	if b, _ := os.ReadFile(src); string(b) != markerCreds("eyJnew") {
		t.Error("source lost")
	}
	if _, err := os.Lstat(dest); !os.IsNotExist(err) {
		t.Error("half marker remains")
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

func stagingFiles(t *testing.T, src string) []string {
	t.Helper()
	m, _ := filepath.Glob(src + ".deleting.*")
	return m
}

func TestArchiveNoReplaceRaceReplacementSurvives(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "n.creds")
	dest := src + ".deleted"
	os.WriteFile(src, []byte(markerCreds("eyJold")), 0o600)
	archiveHook = func() { os.WriteFile(src, []byte(markerCreds("eyJreplacement")), 0o600) }
	defer func() { archiveHook = nil }()
	got, err := archiveNoReplace(src, dest)
	if err != nil || got != dest {
		t.Fatalf("%q %v", got, err)
	}
	if b, _ := os.ReadFile(src); string(b) != markerCreds("eyJreplacement") {
		t.Errorf("replacement lost or changed: %q", b)
	}
	if b, _ := os.ReadFile(got); !strings.Contains(string(b), "eyJold") || strings.Contains(string(b), "eyJreplacement") {
		t.Errorf("marker %q", b)
	}
	if g := stagingFiles(t, src); len(g) != 0 {
		t.Errorf(".deleting remains: %v", g)
	}
}

func TestArchiveNoReplaceFailureRestoresSource(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "n.creds")
	dest := src + ".deleted"
	os.WriteFile(src, []byte(markerCreds("eyJnew")), 0o600)
	for i := 0; i <= 100; i++ {
		d := dest
		if i > 0 {
			d += "." + strconv.Itoa(i)
		}
		os.WriteFile(d, []byte("old"), 0o600)
	}
	if _, err := archiveNoReplace(src, dest); err == nil || !strings.Contains(err.Error(), "nothing was moved") {
		t.Fatalf("err %v", err)
	}
	if b, _ := os.ReadFile(src); string(b) != markerCreds("eyJnew") {
		t.Error("source not restored")
	}
	if g := stagingFiles(t, src); len(g) != 0 {
		t.Errorf(".deleting remains: %v", g)
	}

	// A sync failure leaves no marker either.
	os.Remove(dest)
	old := syncFile
	syncFile = func(*os.File) error { return errors.New("disk on fire") }
	defer func() { syncFile = old }()
	if _, err := archiveNoReplace(src, dest); err == nil {
		t.Fatal("expected error")
	}
	if _, err := os.Lstat(dest); !os.IsNotExist(err) {
		t.Error("half marker remains")
	}
	if b, _ := os.ReadFile(src); string(b) != markerCreds("eyJnew") {
		t.Error("source not restored")
	}
	if g := stagingFiles(t, src); len(g) != 0 {
		t.Errorf(".deleting remains: %v", g)
	}
}

func TestArchiveNoReplaceFailureKeepsAReplacementAndDeleting(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "n.creds")
	dest := src + ".deleted"
	os.WriteFile(src, []byte(markerCreds("eyJold")), 0o600)
	archiveHook = func() { os.WriteFile(src, []byte(markerCreds("eyJreplacement")), 0o600) }
	old := syncFile
	syncFile = func(*os.File) error { return errors.New("disk on fire") }
	defer func() { archiveHook = nil; syncFile = old }()
	if _, err := archiveNoReplace(src, dest); err == nil {
		t.Fatal("expected error")
	}
	if b, _ := os.ReadFile(src); string(b) != markerCreds("eyJreplacement") {
		t.Errorf("replacement clobbered: %q", b)
	}
	if g := stagingFiles(t, src); len(g) != 1 {
		t.Errorf(".deleting lost: %v", g)
	} else if b, _ := os.ReadFile(g[0]); string(b) != markerCreds("eyJold") {
		t.Errorf(".deleting lost: %q", b)
	}
	if _, err := os.Lstat(dest); !os.IsNotExist(err) {
		t.Error("half marker remains")
	}
}

func TestArchiveNoReplaceArchivesALeftoverDeleting(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "n.creds")
	dest := src + ".deleted"
	own := src + ".deleting.12345.1.abcdef012345"
	os.WriteFile(own, []byte(markerCreds("eyJleft")), 0o600)
	got, err := archiveNoReplace(src, dest)
	if err != nil || got != dest {
		t.Fatalf("%q %v", got, err)
	}
	if b, _ := os.ReadFile(got); !strings.Contains(string(b), "eyJleft") {
		t.Errorf("marker %q", b)
	}
	if _, err := os.Lstat(own); !os.IsNotExist(err) {
		t.Error(".deleting remains")
	}

	// With src present too: both are archived and src's marker is returned.
	os.WriteFile(own, []byte(markerCreds("eyJleft2")), 0o600)
	os.WriteFile(src, []byte(markerCreds("eyJsrc")), 0o600)
	got, err = archiveNoReplace(src, dest)
	if err != nil || got != dest+".2" {
		t.Fatalf("%q %v", got, err)
	}
	if b, _ := os.ReadFile(dest + ".1"); !strings.Contains(string(b), "eyJleft2") {
		t.Errorf("leftover marker %q", b)
	}
	if b, _ := os.ReadFile(got); !strings.Contains(string(b), "eyJsrc") {
		t.Errorf("src marker %q", b)
	}
	for _, p := range []string{src, own} {
		if _, err := os.Lstat(p); !os.IsNotExist(err) {
			t.Errorf("%s remains", p)
		}
	}
}

func TestArchiveNoReplaceLeavesAnUnrelatedDeletingFileAlone(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "n.creds")
	dest := src + ".deleted"
	other := src + ".deleting.bak"
	os.WriteFile(src, []byte(markerCreds("eyJsrc")), 0o600)
	os.WriteFile(other, []byte("distinctive backup contents"), 0o600)
	got, err := archiveNoReplace(src, dest)
	if err != nil || got != dest {
		t.Fatalf("%q %v", got, err)
	}
	if b, _ := os.ReadFile(got); !strings.Contains(string(b), "eyJsrc") {
		t.Errorf("marker not made from src: %q", b)
	}
	if b, err := os.ReadFile(other); err != nil || string(b) != "distinctive backup contents" {
		t.Errorf("unrelated file touched: %q %v", b, err)
	}
	if _, err := os.Lstat(src); !os.IsNotExist(err) {
		t.Error("src remains")
	}
}

func TestArchiveNoReplaceUnlistableDirectoryFailsAndKeepsStagedFile(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root can list any directory")
	}
	dir := t.TempDir()
	src := filepath.Join(dir, "n.creds")
	staged := src + ".deleting.1.1.abcdef012345"
	os.WriteFile(staged, []byte(markerCreds("eyJstaged")), 0o600)
	t.Cleanup(func() { os.Chmod(dir, 0o700) })
	if err := os.Chmod(dir, 0o300); err != nil {
		t.Fatal(err)
	}
	got, err := archiveNoReplace(src, src+".deleted")
	if err == nil {
		t.Fatalf("archive reported success: %q", got)
	}
	if !strings.Contains(err.Error(), "cannot list") {
		t.Errorf("error does not name the cause: %v", err)
	}
	os.Chmod(dir, 0o700)
	if _, err := os.Lstat(staged); err != nil {
		t.Errorf("staged file gone: %v", err)
	}
}

func TestArchiveNoReplaceRefusesASymlink(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "target")
	os.WriteFile(target, []byte(markerCreds("eyJt")), 0o600)
	src := filepath.Join(dir, "n.creds")
	if err := os.Symlink(target, src); err != nil {
		t.Skip(err)
	}
	if _, err := archiveNoReplace(src, src+".deleted"); err == nil {
		t.Fatal("expected error")
	}
	if fi, err := os.Lstat(src); err != nil || fi.Mode()&os.ModeSymlink == 0 {
		t.Errorf("symlink not left in place: %v", err)
	}
	if g := stagingFiles(t, src); len(g) != 0 {
		t.Errorf(".deleting made: %v", g)
	}
	if _, err := os.Lstat(src + ".deleted"); !os.IsNotExist(err) {
		t.Error("marker made")
	}
}

func TestArchiveNoReplaceConcurrentLosesNothing(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "n.creds")
	dest := src + ".deleted"
	os.WriteFile(src, []byte(markerCreds("eyJoriginal")), 0o600)
	var once sync.Once
	archiveHook = func() {
		// The first attempt to stage puts a replacement at src while the other is racing to stage too.
		once.Do(func() { os.WriteFile(src, []byte(markerCreds("eyJreplacement")), 0o600) })
	}
	defer func() { archiveHook = nil }()
	var wg, ready sync.WaitGroup
	start := make(chan struct{})
	ready.Add(2)
	wg.Add(2)
	for i := 0; i < 2; i++ {
		go func() {
			defer wg.Done()
			ready.Done()
			<-start
			archiveNoReplace(src, dest) // an error from the loser is allowed; loss is not
		}()
	}
	ready.Wait()
	close(start)
	wg.Wait()
	markers, _ := filepath.Glob(dest + "*")
	orig, repl := 0, 0
	for _, m := range markers {
		b, _ := os.ReadFile(m)
		if strings.Contains(string(b), "eyJoriginal") {
			orig++
		}
		if strings.Contains(string(b), "eyJreplacement") {
			repl++
		}
	}
	for _, g := range stagingFiles(t, src) {
		if b, _ := os.ReadFile(g); strings.Contains(string(b), "eyJoriginal") {
			orig++
		}
	}
	// At least once; a second copy is possible when one attempt claims the staging file of the other after it
	// was read (a duplicate marker, never a loss).
	if orig < 1 {
		t.Errorf("original creds lost (markers %v)", markers)
	}
	// The replacement is either still at src, untouched, or (if the loser got to it) archived once; never lost.
	if b, err := os.ReadFile(src); err == nil {
		if string(b) != markerCreds("eyJreplacement") {
			t.Errorf("replacement changed: %q", b)
		}
	} else if repl < 1 {
		t.Errorf("replacement lost: archived %d times", repl)
	}
}

func TestRetireRetryFinishesACrashedDelete(t *testing.T) {
	h := newFleetEnv(t)
	h.seed(ad("c"))
	creds := h.creds(ad("c"))
	want, _ := os.ReadFile(creds)
	// The server side is gone and the creds file was renamed to its staging name, then the run crashed.
	aside := filepath.Join(t.TempDir(), "aside")
	if err := os.Rename(creds, aside); err != nil {
		t.Fatal(err)
	}
	if _, errb, code := h.run("endpoint", "delete", "o.g.c", "--yes"); code != 0 {
		t.Fatalf("first delete: %q", errb)
	}
	stage := creds + ".deleting.99.1.abcdef012345"
	if err := os.Rename(aside, stage); err != nil {
		t.Fatal(err)
	}
	out, errb, code := h.run("endpoint", "delete", "o.g.c", "--yes", "--detail")
	if code != 0 || strings.Contains(out+errb, "does not exist") {
		t.Fatalf("%d %q %q", code, out, errb)
	}
	if _, err := os.Lstat(stage); !os.IsNotExist(err) {
		t.Error("staging file remains")
	}
	got, err := os.ReadFile(creds + ".deleted")
	if err != nil || !strings.Contains(string(want), string(got)) || !strings.Contains(string(got), "BEGIN NATS USER JWT") || strings.Contains(string(got), "SEED") {
		t.Errorf("marker %q %v", got, err)
	}
	if !strings.Contains(out, "Deleted endpoint o.g.c") {
		t.Errorf("not reported as deleted: %q", out)
	}
}
