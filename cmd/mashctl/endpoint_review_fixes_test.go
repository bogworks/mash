package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nats-io/nkeys"

	"github.com/bogworks/mash/internal/apply"
	"github.com/bogworks/mash/internal/config"
)

// unsafeDir is a folder every user can write in and that has no sticky bit.
func unsafeDir(t *testing.T) string {
	t.Helper()
	d := filepath.Join(t.TempDir(), "shared")
	if err := os.Mkdir(d, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(d, 0o777); err != nil {
		t.Fatal(err)
	}
	return d
}

// --out into a folder others can write in is refused on every path that writes a join file or its .pending file, and
// nothing is written.
func TestEndpointOutFolderCheckedOnEveryPath(t *testing.T) {
	h := newFleetEnv(t)
	fakeRevoke(t, nil)
	_, pubFile, _ := epKey(t, t.TempDir(), "k")
	bad := unsafeDir(t)
	h.seed(ad("have"))
	h.seed(ad("have2"))
	before := strings.Join(h.records(), ",")
	cases := map[string][]string{
		"create":               {"endpoint", "create", ad("n1").String(), "--out", filepath.Join(bad, "n1.join")},
		"create --public-key":  {"endpoint", "create", ad("n2").String(), "--out", filepath.Join(bad, "n2.join"), "--public-key", pubFile},
		"reissue":              {"endpoint", "reissue", ad("have").String(), "--out", filepath.Join(bad, "r1.join")},
		"reissue --public-key": {"endpoint", "reissue", ad("have2").String(), "--out", filepath.Join(bad, "r2.join"), "--public-key", pubFile},
		"create --dry-run":     {"endpoint", "create", ad("n3").String(), "--out", filepath.Join(bad, "n3.join"), "--dry-run"},
	}
	for name, args := range cases {
		so, se, code := h.run(args...)
		if code != 1 || so != "" || !strings.HasPrefix(se, "error: the folder "+bad+" is not usable: ") || !strings.Contains(se, "writable by every user") {
			t.Errorf("%s: %d %q %q", name, code, so, se)
		}
	}
	if ents, err := os.ReadDir(bad); err != nil || len(ents) != 0 {
		t.Errorf("files were written in the folder: %v %v", ents, err)
	}
	if strings.Join(h.records(), ",") != before {
		t.Error("a refused run changed the directory")
	}
}

// The CA file keeps its certificates and nothing else: a private key in the same file is refused.
func TestJoinFileCAKeepsOnlyCertificates(t *testing.T) {
	h := newFleetEnv(t)
	dir := t.TempDir()
	c1, c2 := testCAPEM(t), testCAPEM(t)
	kp, _ := nkeys.CreateUser()
	seed, _ := kp.Seed()
	keyBlock := "-----BEGIN PRIVATE KEY-----\n" + string(seed) + "\n-----END PRIVATE KEY-----\n"
	write := func(name, text string) string {
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, []byte(text), 0o600); err != nil {
			t.Fatal(err)
		}
		return p
	}
	bundle := write("bundle.pem", "# two roots\n"+c1+"\n"+c2)
	b, err := joinBaseOf(config.Config{URL: "tls://a:4222", TLSCA: bundle})
	if err != nil || b.ca == nil || *b.ca != c1+c2 || strings.Count(*b.ca, "BEGIN CERTIFICATE") != 2 {
		t.Errorf("bundle: %v", err)
	}
	for name, text := range map[string]string{
		"combined.pem": c1 + keyBlock,
		"keyfirst.pem": keyBlock + c1,
		"trailing.pem": c1 + "not pem at all\n",
		"empty.pem":    "",
		"text.pem":     "hello\n",
	} {
		p := write(name, text)
		if _, err := joinBaseOf(config.Config{URL: "tls://a:4222", TLSCA: p}); err == nil || !strings.HasSuffix(err.Error(), p+" must hold only certificates") {
			t.Errorf("%s: %v", name, err)
		}
	}
	// through the command: exit 1, the message alone, nothing written
	combined := write("c.pem", c1+keyBlock)
	h.env["MASH_CA"] = combined
	jf := filepath.Join(dir, "x.join")
	so, se, code := h.run("endpoint", "create", ad("x").String(), "--out", jf)
	if code != 1 || so != "" || se != "error: "+combined+" must hold only certificates\n" || len(h.records()) != 0 || !gone(t, jf) {
		t.Errorf("command: %d %q %q", code, so, se)
	}
	if strings.Contains(se, string(seed)) {
		t.Error("the message holds the key")
	}
}

// A key cancelled on only some servers deletes nothing and says how many did not confirm.
func TestEndpointDeleteCountsServersThatDidNotConfirm(t *testing.T) {
	h := newFleetEnv(t)
	h.provisionDeclared("a")
	s := stubRevoke(t)
	s.err = &apply.ConfirmError{Confirmed: 1, Listed: 2, Msg: "revoke: the revocation was pushed but not confirmed: one server"}
	_, errb, code := h.run("endpoint", "delete", "o.g.a", "--yes")
	want := "error: o.g.a was not deleted: 1 of 2 servers did not confirm its key\n       was cancelled\nhint: mashctl endpoint delete o.g.a\n"
	if code != 1 || !strings.HasSuffix(errb, want) {
		t.Errorf("%d %q", code, errb)
	}
	if len(h.records()) != 1 {
		t.Error("the record changed")
	}
}

// --dry-run --json is the real run's object with dry_run added.
func TestEndpointDeleteDryRunJSON(t *testing.T) {
	h := newFleetEnv(t)
	a := ad("q")
	h.seed(a)
	h.putMessages(a, 2)
	so, se, code := h.run("endpoint", "delete", "o.g.q", "--dry-run", "--json")
	var doc map[string]any
	if code != 0 || json.Unmarshal([]byte(so), &doc) != nil || !sameKeys(doc, []string{"address", "deleted", "messages_deleted", "dry_run"}) ||
		doc["deleted"] != true || doc["messages_deleted"] != float64(2) || doc["dry_run"] != true {
		t.Errorf("%d %q %q", code, so, se)
	}
}

// A creds file set aside by delete is named <address>.creds.deleted.
func TestEndpointDeleteSetsCredsAsideAsDeleted(t *testing.T) {
	h := newFleetEnv(t)
	h.seed(ad("c"))
	if _, se, code := h.run("endpoint", "delete", "o.g.c", "--yes"); code != 0 {
		t.Fatalf("%q", se)
	}
	if _, err := os.Lstat(h.creds(ad("c")) + ".deleted"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(h.creds(ad("c"))); err == nil {
		t.Error("the creds file is still installed")
	}
}
