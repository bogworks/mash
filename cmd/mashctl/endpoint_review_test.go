package main

import (
	"context"
	"encoding/json"
	"errors"
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

func TestEndpointCreateIgnoresMashGroup(t *testing.T) {
	h := newFleetEnv(t)
	_, pubFile, _ := epKey(t, t.TempDir(), "k")
	h.env["MASH_GROUP"] = "dev"
	so, se, code := h.run("endpoint", "create", "design", "--public-key", pubFile, "--json")
	if code != 2 || jsonObject(t, so)["refusal"].(map[string]any)["code"] != "group_missing" {
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
	out := t.TempDir()
	_, pubFile, _ := epKey(t, out, "k")
	a := ad("flat")
	so, se, code := h.run("endpoint", "create", a.String(), "--public-key", pubFile, "--jwt-out", filepath.Join(out, "f.jwt"), "--json")
	if code != 0 {
		t.Fatalf("%d %q %q", code, so, se)
	}
	m := jsonObject(t, so)
	for k, want := range map[string]string{"schema": "mashctl-endpoint@1", "outcome": "created", "owner": a.Owner, "group": a.Group,
		"instance": a.Instance, "address": a.String(), "key": a.Flat(), "jwt_path": filepath.Join(out, "f.jwt")} {
		if m[k] != want {
			t.Errorf("%s = %v, want %s", k, m[k], want)
		}
	}
	for _, k := range []string{"endpoint", "jwt", "creds"} {
		if _, ok := m[k]; ok {
			t.Errorf("nested object %q", k)
		}
	}
	if _, ok := m["next_steps"]; !ok {
		t.Error("no next_steps")
	}
	if v, ok := m["refusal"]; !ok || v != nil {
		t.Errorf("refusal %v %v", v, ok)
	}
	so, _, _ = h.run("endpoint", "create", ad("flat2").String(), "--make-key", "--json")
	m = jsonObject(t, so)
	if m["creds_path"] != h.creds(ad("flat2")) || m["creds_written"] != true || m["key"] != ad("flat2").Flat() {
		t.Errorf("make-key doc %v", m)
	}
}

func TestEndpointShowDeleteFlatJSON(t *testing.T) {
	h := newFleetEnv(t)
	h.seed(ad("s"))
	so, _, _ := h.run("endpoint", "show", "o/g/s", "--json")
	m := jsonObject(t, so)
	if m["owner"] != "o" || m["address"] != "o.g.s" || m["key"] != "o_g_s" || m["outcome"] != "shown" || m["endpoint"] != nil {
		t.Errorf("show %v", m)
	}
	so, _, _ = h.run("endpoint", "delete", "o/g/s", "--json")
	m = jsonObject(t, so)
	if m["group"] != "g" || m["instance"] != "s" || m["key"] != "o_g_s" || m["outcome"] != "deleted" || m["endpoint"] != nil {
		t.Errorf("delete %v", m)
	}
}

// TestEndpointUsageErrorsInJSON: with --json a usage error also prints the refusal document on stdout.
func TestEndpointUsageErrorsInJSON(t *testing.T) {
	h := newFleetEnv(t)
	_, pubFile, _ := epKey(t, t.TempDir(), "k")
	cases := []struct {
		name string
		args []string
		code string
	}{
		{"create unknown flag", []string{"create", "o/g/x", "--public-key", pubFile, "--bogus", "--json"}, "unknown_flag"},
		{"create unknown flag, json first", []string{"create", "--json", "o/g/x", "--bogus"}, "unknown_flag"},
		{"create no value", []string{"create", "o/g/x", "--json", "--public-key"}, "usage"},
		{"create two addresses", []string{"create", "o/g/x", "o/g/y", "--json"}, "usage"},
		{"create bad label", []string{"create", "o/g/x", "--label", "nokv", "--json"}, "usage"},
		{"create key_missing", []string{"create", "o/g/x", "--json"}, "key_missing"},
		{"reissue unknown flag", []string{"reissue", "o/g/x", "--bogus", "--json"}, "unknown_flag"},
		{"reissue no address", []string{"reissue", "--public-key", pubFile, "--json"}, "address_missing"},
		{"reissue bad address", []string{"reissue", "Not A Valid!", "--public-key", pubFile, "--json"}, "address_invalid"},
		{"reissue no key", []string{"reissue", "o/g/x", "--json"}, "key_missing"},
		{"delete unknown flag", []string{"delete", "o/g/x", "--bogus", "--json"}, "unknown_flag"},
		{"delete no address", []string{"delete", "--json"}, "address_missing"},
		{"delete bad address", []string{"delete", "Not A Valid!", "--json"}, "address_invalid"},
		{"show unknown flag", []string{"show", "o/g/x", "--bogus", "--json"}, "unknown_flag"},
		{"show no address", []string{"show", "--json"}, "address_missing"},
		{"show bad address", []string{"show", "Not A Valid!", "--json"}, "address_invalid"},
		{"list unknown flag", []string{"list", "--bogus", "--json"}, "unknown_flag"},
		{"list argument", []string{"list", "o/g/x", "--json"}, "usage"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			so, se, code := h.run(append([]string{"endpoint"}, c.args...)...)
			if code != 2 {
				t.Fatalf("code %d, want 2; %q %q", code, so, se)
			}
			m := jsonObject(t, so)
			ref, _ := m["refusal"].(map[string]any)
			if ref == nil || ref["code"] != c.code || m["outcome"] != "error" || m["schema"] == nil {
				t.Errorf("document %v, want code %s", m, c.code)
			}
			if se == "" {
				t.Error("no sentence on stderr")
			}
		})
	}
	// without --json nothing goes to stdout
	if so, _, code := h.run("endpoint", "show", "--bogus"); code != 2 || so != "" {
		t.Errorf("plain usage error: %d %q", code, so)
	}
	// a value that looks like --json is a value, not the flag
	if so, _, _ := h.run("endpoint", "create", "o/g/x", "--note", "--json", "--bogus"); so != "" {
		t.Errorf("--json taken as a value printed %q", so)
	}
}

func TestEndpointDryRunRefusals(t *testing.T) {
	h := newFleetEnv(t)
	fakeRevoke(t, nil)
	out := t.TempDir()
	_, pubA, _ := epKey(t, out, "a")
	_, pubB, _ := epKey(t, out, "b")
	if _, se, code := h.run("endpoint", "create", ad("a").String(), "--public-key", pubA, "--jwt-out", filepath.Join(out, "a.jwt")); code != 0 {
		t.Fatalf("create a: %q", se)
	}
	if _, se, code := h.run("endpoint", "create", ad("b").String(), "--public-key", pubB, "--jwt-out", filepath.Join(out, "b.jwt")); code != 0 {
		t.Fatalf("create b: %q", se)
	}
	refused := func(want string, args ...string) {
		t.Helper()
		so, se, code := h.run(append(args, "--dry-run", "--json")...)
		m := jsonObject(t, so)
		ref, _ := m["refusal"].(map[string]any)
		if code != 1 || m["outcome"] != "refused" || ref == nil || ref["code"] != want {
			t.Errorf("%v: want refused %s, got %d %q %q", args, want, code, so, se)
		}
	}
	// the key of a is already issued at a
	refused("public_key_issued", "endpoint", "create", ad("c").String(), "--public-key", pubA, "--jwt-out", filepath.Join(out, "c.jwt"))
	refused("public_key_issued", "endpoint", "reissue", ad("b").String(), "--public-key", pubA, "--jwt-out", filepath.Join(out, "c.jwt"))
	refused("address_taken", "endpoint", "create", ad("a").String(), "--public-key", pubB, "--jwt-out", filepath.Join(out, "c.jwt"))
	// the same key at its own address plans a rerun
	if so, se, code := h.run("endpoint", "create", ad("a").String(), "--public-key", pubA, "--jwt-out", filepath.Join(out, "c.jwt"), "--dry-run"); code != 0 {
		t.Errorf("own key: %d %q %q", code, so, se)
	}
	// a deleted endpoint's address is burned
	if _, se, code := h.run("endpoint", "delete", ad("b").String()); code != 0 {
		t.Fatalf("delete: %q", se)
	}
	refused("key_retired", "endpoint", "create", ad("b").String(), "--public-key", pubB, "--jwt-out", filepath.Join(out, "c.jwt"))
	refused("key_retired", "endpoint", "reissue", ad("b").String(), "--public-key", pubB, "--jwt-out", filepath.Join(out, "c.jwt"))
	// the revoked key of b is refused at a new address
	refused("key_revoked", "endpoint", "create", ad("d").String(), "--public-key", pubB, "--jwt-out", filepath.Join(out, "c.jwt"))
	if _, err := os.Lstat(filepath.Join(out, "c.jwt")); err == nil {
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
	if _, se, code := h.run("endpoint", "create", a.String(), "--make-key"); code != 0 {
		t.Fatalf("create: %q", se)
	}
	var wg sync.WaitGroup
	codes := make([]int, 3)
	errs := make([]string, 3)
	for i := range codes {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, errs[i], codes[i] = h.run("endpoint", "reissue", a.String(), "--make-key")
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
	if _, err := os.Lstat(h.creds(a) + ".lock"); err == nil {
		t.Error("lock directory left behind")
	}
	if left, _ := filepath.Glob(filepath.Join(h.dir, ".*.tmp-*")); len(left) != 0 {
		t.Errorf("temp files left: %v", left)
	}
}

func TestEndpointReissueBusy(t *testing.T) {
	h := newFleetEnv(t)
	fakeRevoke(t, nil)
	a := ad("busy")
	if _, se, code := h.run("endpoint", "create", a.String(), "--make-key"); code != 0 {
		t.Fatalf("create: %q", se)
	}
	before, _ := os.ReadFile(h.creds(a))
	lock := h.creds(a) + ".lock"
	if err := os.Mkdir(lock, 0o700); err != nil {
		t.Fatal(err)
	}
	prev := epLockWait
	epLockWait = 150 * time.Millisecond
	t.Cleanup(func() { epLockWait = prev })
	so, se, code := h.run("endpoint", "reissue", a.String(), "--make-key", "--json")
	if code != 1 || !strings.Contains(se, "reissue_busy") || !strings.Contains(se, lock) {
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
	out := t.TempDir()
	_, pubA, _ := epKey(t, out, "a")
	if _, se, code := h.run("endpoint", "create", ad("a").String(), "--public-key", pubA, "--jwt-out", filepath.Join(out, "a.jwt")); code != 0 {
		t.Fatalf("create a: %q", se)
	}
	so, se, code := h.run("endpoint", "create", ad("c").String(), "--public-key", pubA, "--jwt-out", filepath.Join(out, "c.jwt"), "--json")
	if code != 1 || !strings.Contains(se, "mash key create") {
		t.Fatalf("code %d, stderr %q", code, se)
	}
	var doc struct {
		Refusal   struct{ Code, Detail string }
		NextSteps []struct{ Detail string } `json:"next_steps"`
	}
	if err := json.Unmarshal([]byte(so), &doc); err != nil {
		t.Fatal(err)
	}
	if doc.Refusal.Code != "public_key_issued" || !strings.Contains(doc.Refusal.Detail, "mash key create") ||
		len(doc.NextSteps) != 1 || !strings.Contains(doc.NextSteps[0].Detail, "mash key create") {
		t.Errorf("%+v", doc)
	}
}

func TestEndpointReissueConcurrentPublicKey(t *testing.T) {
	h := newFleetEnv(t)
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
	if _, se, code := h.run("endpoint", "create", a.String(), "--public-key", pub0, "--jwt-out", filepath.Join(out, "k0.jwt")); code != 0 {
		t.Fatalf("create: %q", se)
	}
	_, pub1, _ := epKey(t, out, "k1")
	_, pub2, _ := epKey(t, out, "k2")
	jwtOut := filepath.Join(out, "new.jwt")
	var wg sync.WaitGroup
	codes := make([]int, 2)
	errs := make([]string, 2)
	for i, pub := range []string{pub1, pub2} {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, errs[i], codes[i] = h.run("endpoint", "reissue", a.String(), "--public-key", pub, "--jwt-out", jwtOut)
		}()
	}
	wg.Wait()
	ok := 0
	for i, c := range codes {
		switch c {
		case 0:
			ok++
		case 1:
			if !strings.Contains(errs[i], "file_exists") && !strings.Contains(errs[i], "key_revoked") {
				t.Errorf("run %d: exit 1 without file_exists: %s", i, errs[i])
			}
		default:
			t.Errorf("run %d: exit %d: %s", i, c, errs[i])
		}
	}
	if ok != 1 || rotations != 1 {
		t.Fatalf("%d runs succeeded, the server key rotated %d times; want 1 and 1 (%v)", ok, rotations, errs)
	}
	tok, err := os.ReadFile(jwtOut)
	if err != nil {
		t.Fatal(err)
	}
	uc, err := jwt.DecodeUserClaims(strings.TrimSpace(string(tok)))
	if err != nil {
		t.Fatal(err)
	}
	if rec, found := h.record(a); !found || rec.User != uc.Subject {
		t.Errorf("directory key %q, output JWT key %q", rec.User, uc.Subject)
	}
}
