package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/nats-io/jwt/v2"

	"github.com/bogworks/mash/internal/config"
)

// jsonObject parses one JSON document into a map.
func jsonObject(t *testing.T, s string) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal([]byte(s), &m); err != nil {
		t.Fatalf("not one JSON document: %v\n%q", err, s)
	}
	return m
}

func TestEndpointCreateIgnoresTheEndpointGroup(t *testing.T) {
	h := newFleetEnv(t)
	_, pubFile, _ := epKey(t, t.TempDir(), "k")
	h.env["MASH_ENDPOINT"] = h.e.Owner + ".dev.x" // only its owner part counts to mashctl
	so, se, code := h.run("endpoint", "create", "design", "--out", filepath.Join(t.TempDir(), "d.join"), "--public-key", pubFile, "--json")
	if code != 2 || !strings.HasPrefix(jsonObject(t, so)["error"].(string), "not an endpoint address") {
		t.Fatalf("code %d %q %q", code, so, se)
	}
	if len(h.records()) != 0 {
		t.Error("a record was made")
	}
}

func TestEpWriteNew(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "a.jwt")
	// a stale temp file from a killed run does not matter
	if err := os.WriteFile(filepath.Join(dir, ".a.jwt.tmp-stale"), []byte("trunc"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := epWriteNew(p, []byte("one\n")); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(p); string(b) != "one\n" || fileMode(t, p) != 0o600 {
		t.Fatalf("content %q mode %v", b, fileMode(t, p))
	}
	// an existing file is kept and the error is ErrExist
	if err := epWriteNew(p, []byte("two\n")); !errors.Is(err, fs.ErrExist) {
		t.Fatalf("second write: %v", err)
	}
	if b, _ := os.ReadFile(p); string(b) != "one\n" {
		t.Errorf("file changed: %q", b)
	}
	// a failed write leaves no final file and no temp file of its own
	missing := filepath.Join(dir, "nodir", "b.jwt")
	if err := epWriteNew(missing, []byte("x")); err == nil {
		t.Fatal("write into a missing directory succeeded")
	}
	left, _ := filepath.Glob(filepath.Join(dir, ".*.tmp-*"))
	if len(left) != 1 || filepath.Base(left[0]) != ".a.jwt.tmp-stale" {
		t.Errorf("temp files left: %v", left)
	}
	ents, _ := os.ReadDir(dir)
	for _, e := range ents {
		if strings.HasPrefix(e.Name(), ".") && e.Name() != ".a.jwt.tmp-stale" {
			t.Errorf("unexpected file %s", e.Name())
		}
	}
	// a read-only directory: the final file is not made
	ro := filepath.Join(dir, "ro")
	if err := os.Mkdir(ro, 0o500); err != nil {
		t.Fatal(err)
	}
	if err := epWriteNew(filepath.Join(ro, "c.jwt"), []byte("x")); err == nil && os.Getuid() != 0 {
		t.Fatal("write into a read-only directory succeeded")
	}
	if os.Getuid() != 0 {
		if _, err := os.Lstat(filepath.Join(ro, "c.jwt")); err == nil {
			t.Error("final file exists after a failed write")
		}
	}
	os.Chmod(ro, 0o700)
}

func TestEpReplaceCredsLeavesNoTemp(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "x.creds")
	if err := os.WriteFile(p, []byte("old"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := epReplaceCreds(p, []byte("new")); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(p); string(b) != "new" || fileMode(t, p) != 0o600 {
		t.Errorf("content %q", b)
	}
	ents, _ := os.ReadDir(dir)
	if len(ents) != 1 {
		t.Errorf("files left: %v", ents)
	}
}

func TestEndpointCreateFlatJSON(t *testing.T) {
	h := newFleetEnv(t)
	fakeKeyRevoked(t, func(string) (bool, error) { return false, nil })
	out := t.TempDir()
	_, pubFile, _ := epKey(t, out, "k")
	a := ad("flat")
	so, se, code := h.run("endpoint", "create", a.String(), "--out", filepath.Join(out, "f.join"), "--public-key", pubFile, "--json")
	if code != 0 {
		t.Fatalf("%d %q %q", code, so, se)
	}
	m := jsonObject(t, so)
	if len(m) != 4 || m["address"] != a.String() || m["installed_here"] != false || m["creds"] != nil || m["join_file"] != filepath.Join(out, "f.join") {
		t.Errorf("out doc %v", m)
	}
	so, _, _ = h.run("endpoint", "create", ad("flat2").String(), "--json")
	m = jsonObject(t, so)
	if len(m) != 4 || m["address"] != ad("flat2").String() || m["installed_here"] != true || m["creds"] != h.creds(ad("flat2")) || m["join_file"] != nil {
		t.Errorf("installed doc %v", m)
	}
}

func TestEndpointShowDeleteFlatJSON(t *testing.T) {
	h := newFleetEnv(t)
	h.seed(ad("s"))
	so, _, _ := h.run("endpoint", "show", "o.g.s", "--json")
	m := jsonObject(t, so)
	if m["address"] != "o.g.s" || m["online"] != false || m["suspended"] != false || m["state"] != nil || m["public_key"] == "" || m["endpoint"] != nil || m["labels"] == nil {
		t.Errorf("show %v", m)
	}
	so, _, _ = h.run("endpoint", "delete", "o.g.s", "--yes", "--json")
	m = jsonObject(t, so)
	if m["address"] != "o.g.s" || m["deleted"] != true || m["messages_deleted"] != float64(0) || len(m) != 3 {
		t.Errorf("delete %v", m)
	}
}

// TestEndpointReadUsageErrorsInJSON: with --json the commands of the new form print one {"error","hint"} line on stdout
// and nothing on stderr.
func TestEndpointReadUsageErrorsInJSON(t *testing.T) {
	h := newFleetEnv(t)
	cases := []struct {
		args []string
		want string
	}{
		{[]string{"show", "o.g.x", "--bogus", "--json"}, `{"error":"unknown flag --bogus"}`},
		{[]string{"show", "--json"}, `{"error":"missing the endpoint address (owner.group.name)","hint":"mashctl endpoint show --help"}`},
		{[]string{"show", "Not A Valid!", "--json"}, `{"error":"not an endpoint address (the value is not shown): write owner.group.name, for example acme.demo.bob","hint":"mashctl endpoint show --help"}`},
		{[]string{"list", "--bogus", "--json"}, `{"error":"unknown flag --bogus"}`},
		{[]string{"list", "a.b.c", "--json"}, `{"error":"not an owner or owner.group prefix (the value is not shown): write acme or acme.demo","hint":"mashctl endpoint list --help"}`},
		{[]string{"list", "a", "b", "--json"}, `{"error":"too many arguments","hint":"mashctl endpoint list --help"}`},
		{[]string{"show", "o.g.none", "--json"}, `{"error":"no endpoint o.g.none","hint":"mashctl endpoint list"}`},
	}
	for _, c := range cases {
		so, se, code := h.run(append([]string{"endpoint"}, c.args...)...)
		wantCode := 2
		if strings.Contains(c.want, "no endpoint") {
			wantCode = 1
		}
		if code != wantCode || se != "" || strings.TrimSpace(so) != c.want {
			t.Errorf("%v: %d %q %q, want %d %q", c.args, code, so, se, wantCode, c.want)
		}
	}
}

// With --json a wrong command line of create or reissue prints one {"error","hint"} line on stdout and nothing on stderr.
func TestEndpointUsageErrorsInJSON(t *testing.T) {
	h := newFleetEnv(t)
	_, pubFile, _ := epKey(t, t.TempDir(), "k")
	cases := []struct {
		name string
		args []string
		want string
	}{
		{"create unknown flag", []string{"create", "o.g.x", "--bogus", "--json"}, `{"error":"unknown flag --bogus"}`},
		{"create unknown flag, json first", []string{"create", "--json", "o.g.x", "--bogus"}, `{"error":"unknown flag --bogus"}`},
		{"create removed option", []string{"create", "o.g.x", "--label", "k=v", "--json"}, `{"error":"unknown flag --label"}`},
		{"create no value", []string{"create", "o.g.x", "--json", "--out"}, `{"error":"--out needs a value","hint":"mashctl endpoint create --help"}`},
		{"create two addresses", []string{"create", "o.g.x", "o.g.y", "--json"}, `{"error":"too many arguments","hint":"mashctl endpoint create --help"}`},
		{"create public key without out", []string{"create", "o.g.x", "--public-key", pubFile, "--json"}, `{"error":"--public-key needs --out, the join file to write","hint":"mashctl endpoint create --help"}`},
		{"reissue unknown flag", []string{"reissue", "o.g.x", "--bogus", "--json"}, `{"error":"unknown flag --bogus"}`},
		{"reissue no address", []string{"reissue", "--json"}, `{"error":"missing the endpoint address (owner.group.name)","hint":"mashctl endpoint reissue --help"}`},
		{"reissue bad address", []string{"reissue", "Not A Valid!", "--json"}, `{"error":"not an endpoint address (the value is not shown): write owner.group.name, for example acme.demo.bob","hint":"mashctl endpoint reissue --help"}`},
		{"reissue public key without out", []string{"reissue", "o.g.x", "--public-key", pubFile, "--json"}, `{"error":"--public-key needs --out, the join file to write","hint":"mashctl endpoint reissue --help"}`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			so, se, code := h.run(append([]string{"endpoint"}, c.args...)...)
			if code != 2 || se != "" || strings.TrimSpace(so) != c.want {
				t.Errorf("%d %q %q, want 2 %q", code, so, se, c.want)
			}
		})
	}
	// a value that looks like --json is a value, not the flag
	if so, _, _ := h.run("endpoint", "create", "o.g.x", "--out", "--json", "--bogus"); so != "" {
		t.Errorf("--json taken as a value printed %q", so)
	}
}

func TestEndpointDryRunRefusals(t *testing.T) {
	h := newFleetEnv(t)
	fakeRevoke(t, nil)
	out := t.TempDir()
	_, pubA, _ := epKey(t, out, "a")
	_, pubB, _ := epKey(t, out, "b")
	if _, se, code := h.run("endpoint", "create", ad("a").String(), "--out", filepath.Join(out, "a.join"), "--public-key", pubA); code != 0 {
		t.Fatalf("create a: %q", se)
	}
	if _, se, code := h.run("endpoint", "create", ad("b").String(), "--out", filepath.Join(out, "b.join"), "--public-key", pubB); code != 0 {
		t.Fatalf("create b: %q", se)
	}
	refused := func(want string, args ...string) {
		t.Helper()
		so, se, code := h.run(append(args, "--dry-run")...)
		if code != 1 || so != "" || !strings.HasPrefix(se, "error: ") || !strings.Contains(se, want) {
			t.Errorf("%v: want an error holding %q, got %d %q %q", args, want, code, so, se)
		}
	}
	// the key of a is already issued at a
	refused("was already issued to another address", "endpoint", "create", ad("c").String(), "--out", filepath.Join(out, "c.join"), "--public-key", pubA)
	refused("was already issued to another address", "endpoint", "reissue", ad("b").String(), "--out", filepath.Join(out, "c.join"), "--public-key", pubA)
	refused("already exists", "endpoint", "create", ad("a").String(), "--out", filepath.Join(out, "c.join"), "--public-key", pubB)
	// the same key at its own address plans a rerun
	if so, se, code := h.run("endpoint", "create", ad("a").String(), "--out", filepath.Join(out, "c.join"), "--public-key", pubA, "--dry-run"); code != 0 {
		t.Errorf("own key: %d %q %q", code, so, se)
	}
	// a deleted endpoint's address is burned
	if _, se, code := h.run("endpoint", "delete", ad("b").String(), "--yes"); code != 0 {
		t.Fatalf("delete: %q", se)
	}
	refused("was deleted and its address cannot be used again", "endpoint", "create", ad("b").String(), "--out", filepath.Join(out, "c.join"), "--public-key", pubB)
	refused("was deleted and its address cannot be used again", "endpoint", "reissue", ad("b").String(), "--out", filepath.Join(out, "c.join"), "--public-key", pubB)
	// the revoked key of b is refused at a new address
	refused("revoked", "endpoint", "create", ad("d").String(), "--out", filepath.Join(out, "c.join"), "--public-key", pubB)
	if _, err := os.Lstat(filepath.Join(out, "c.join")); err == nil {
		t.Error("a dry run wrote a file")
	}
}

func TestEndpointReissueConcurrentSameHost(t *testing.T) {
	h := newFleetEnv(t)
	var mu sync.Mutex
	prev := revokeUsers
	revokeUsers = func(_ context.Context, _ config.Config, _ func(string) string, _ int, _ []string, _ io.Writer, _ func() time.Time) (int, error) {
		mu.Lock()
		defer mu.Unlock()
		time.Sleep(20 * time.Millisecond)
		return fakeAcked, nil
	}
	t.Cleanup(func() { revokeUsers = prev })
	a := ad("race")
	if _, se, code := h.run("endpoint", "create", a.String()); code != 0 {
		t.Fatalf("create: %q", se)
	}
	var wg sync.WaitGroup
	codes := make([]int, 3)
	errs := make([]string, 3)
	for i := range codes {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, errs[i], codes[i] = h.run("endpoint", "reissue", a.String())
		}()
	}
	wg.Wait()
	for i, c := range codes {
		if c != 0 {
			t.Errorf("run %d: exit %d: %s", i, c, errs[i])
		}
	}
	raw, err := os.ReadFile(h.creds(a))
	if err != nil {
		t.Fatal(err)
	}
	kp, err := jwt.ParseDecoratedUserNKey(raw)
	if err != nil {
		t.Fatal(err)
	}
	pub, _ := kp.PublicKey()
	tok, _ := jwt.ParseDecoratedJWT(raw)
	uc, err := jwt.DecodeUserClaims(tok)
	if err != nil || uc.Subject != pub {
		t.Fatalf("creds hold a seed and a JWT for different keys: %v", err)
	}
	if rec, ok := h.record(a); !ok || rec.User != pub {
		t.Errorf("directory key %q, creds file key %q", rec.User, pub)
	}
	if u, err := epLock(h.creds(a) + ".lock"); err != nil {
		t.Errorf("lock still held: %v", err)
	} else {
		u()
	}
	if left, _ := filepath.Glob(filepath.Join(h.dir, ".*.tmp-*")); len(left) != 0 {
		t.Errorf("temp files left: %v", left)
	}
}

func TestEndpointReissueBusy(t *testing.T) {
	h := newFleetEnv(t)
	fakeRevoke(t, nil)
	a := ad("busy")
	if _, se, code := h.run("endpoint", "create", a.String()); code != 0 {
		t.Fatalf("create: %q", se)
	}
	before, _ := os.ReadFile(h.creds(a))
	lock := h.creds(a) + ".lock"
	holdLock(t, lock)
	prev := epLockWait
	epLockWait = 150 * time.Millisecond
	t.Cleanup(func() { epLockWait = prev })
	so, se, code := h.run("endpoint", "reissue", a.String())
	if code != 1 || !strings.Contains(se, "another reissue of "+a.String()+" holds the lock") || !strings.Contains(se, fmt.Sprintf("pid %d", os.Getpid())) {
		t.Fatalf("%d %q %q", code, so, se)
	}
	if after, _ := os.ReadFile(h.creds(a)); string(after) != string(before) {
		t.Error("creds changed")
	}
	if _, err := os.Lstat(lock); err != nil {
		t.Error("a lock this run did not make was removed")
	}
}

func TestEndpointPublicKeyIssuedNamesRemedy(t *testing.T) {
	h := newFleetEnv(t)
	fakeKeyRevoked(t, func(string) (bool, error) { return false, nil })
	out := t.TempDir()
	_, pubA, _ := epKey(t, out, "a")
	if _, se, code := h.run("endpoint", "create", ad("a").String(), "--out", filepath.Join(out, "a.join"), "--public-key", pubA); code != 0 {
		t.Fatalf("create a: %q", se)
	}
	so, se, code := h.run("endpoint", "create", ad("c").String(), "--out", filepath.Join(out, "c.join"), "--public-key", pubA)
	if code != 1 || so != "" || !strings.HasSuffix(se, "\nhint: mash key create --out NAME.seed\n") || !strings.Contains(se, "already issued to another address") {
		t.Fatalf("code %d, stdout %q stderr %q", code, so, se)
	}
}

func TestEndpointReissueConcurrentPublicKey(t *testing.T) {
	h := newFleetEnv(t)
	fakeKeyRevoked(t, func(string) (bool, error) { return false, nil })
	var mu sync.Mutex
	rotations := 0
	prev := revokeUsers
	revokeUsers = func(_ context.Context, _ config.Config, _ func(string) string, _ int, _ []string, _ io.Writer, _ func() time.Time) (int, error) {
		mu.Lock()
		defer mu.Unlock()
		rotations++
		time.Sleep(20 * time.Millisecond)
		return 1, nil
	}
	t.Cleanup(func() { revokeUsers = prev })
	out := t.TempDir()
	a := ad("pk")
	_, pub0, _ := epKey(t, out, "k0")
	if _, se, code := h.run("endpoint", "create", a.String(), "--out", filepath.Join(out, "k0.join"), "--public-key", pub0); code != 0 {
		t.Fatalf("create: %q", se)
	}
	_, pub1, _ := epKey(t, out, "k1")
	_, pub2, _ := epKey(t, out, "k2")
	jwtOut := filepath.Join(out, "new.join")
	var wg sync.WaitGroup
	codes := make([]int, 2)
	errs := make([]string, 2)
	for i, pub := range []string{pub1, pub2} {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, errs[i], codes[i] = h.run("endpoint", "reissue", a.String(), "--out", jwtOut, "--public-key", pub)
		}()
	}
	wg.Wait()
	ok := 0
	for i, c := range codes {
		switch c {
		case 0:
			ok++
		case 1:
			if !strings.Contains(errs[i], "already exists") && !strings.Contains(errs[i], "was revoked") {
				t.Errorf("run %d: exit 1 without a refusal for the taken path: %s", i, errs[i])
			}
		default:
			t.Errorf("run %d: exit %d: %s", i, c, errs[i])
		}
	}
	if ok != 1 || rotations != 1 {
		t.Fatalf("%d runs succeeded, the server key rotated %d times; want 1 and 1 (%v)", ok, rotations, errs)
	}
	j, _ := readJoin(t, jwtOut)
	uc, err := jwt.DecodeUserClaims(j.UserJWT)
	if err != nil {
		t.Fatal(err)
	}
	if rec, found := h.record(a); !found || rec.User != uc.Subject {
		t.Errorf("directory key %q, output JWT key %q", rec.User, uc.Subject)
	}
}
