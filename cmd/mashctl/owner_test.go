package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func installEndpointCreds(t *testing.T, dir, name string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte("creds"), 0o600); err != nil {
		t.Fatal(err)
	}
}

// (a) init --local writes the owner, and mashctl then works from config.json alone (no MASH_ENDPOINT).
func TestInitLocalWritesTheOwnerAndMashctlUsesIt(t *testing.T) {
	root := t.TempDir()
	env := map[string]string{"HOME": root, "MASH_CONFIG_DIR": filepath.Join(root, "cfg"), "MASH_KEYS_DIR": filepath.Join(root, "keys")}
	var out, errb strings.Builder
	code := runEnv([]string{"init", "--local", "--owner", "acme", "--dir", filepath.Join(root, "srv"), "--port", "4399"}, func(k string) string { return env[k] }, &out, &errb)
	if code != 0 {
		t.Fatalf("init --local: %d\n%s\n%s", code, out.String(), errb.String())
	}
	raw, err := os.ReadFile(filepath.Join(env["MASH_CONFIG_DIR"], "config.json"))
	if err != nil || !strings.Contains(string(raw), `"owner": "acme"`) {
		t.Fatalf("config.json: %s %v", raw, err)
	}
	cfg, err := loadConfig(func(k string) string { return env[k] }, lk(func(k string) string { return env[k] }))
	if err != nil || cfg.Owner != "acme" {
		t.Fatalf("mashctl owner after init --local: %q %v", cfg.Owner, err)
	}
}

// (b) two installed endpoint creds files do not stop an owner-scoped command.
func TestSeveralInstalledEndpointsDoNotStopMashctl(t *testing.T) {
	h := newFleetEnv(t)
	delete(h.env, "MASH_ENDPOINT")
	if err := os.WriteFile(filepath.Join(h.dir, "config.json"), []byte(`{"owner":"`+h.e.Owner+`"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, errb, code := h.run("init"); code != 0 {
		t.Fatalf("init from config.json owner: %d %q", code, errb)
	}
	installEndpointCreds(t, h.dir, h.e.Owner+".dev.a.creds")
	installEndpointCreds(t, h.dir, h.e.Owner+".dev.b.creds")
	if _, errb, code := h.run("list"); code != 0 || strings.Contains(errb, "more than one") {
		t.Fatalf("list with two installed endpoints: %d %q", code, errb)
	}
}

// (c) a single installed endpoint of another owner does not change who mashctl acts as.
func TestForeignInstalledEndpointDoesNotChangeMashctlsOwner(t *testing.T) {
	h := newFleetEnv(t)
	delete(h.env, "MASH_ENDPOINT")
	installEndpointCreds(t, h.dir, "zed.dev.x.creds")
	// no owner configured: mashctl says so, it does not become zed
	if _, errb, code := h.run("list"); code != 1 || !strings.Contains(errb, "no owner") {
		t.Fatalf("no owner configured: %d %q", code, errb)
	}
	// owner configured: it acts as that owner
	if err := os.WriteFile(filepath.Join(h.dir, "config.json"), []byte(`{"owner":"`+h.e.Owner+`"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	out, errb, code := h.run("list")
	if code != 0 {
		t.Fatalf("list: %d %q", code, errb)
	}
	if strings.Contains(out+errb, "zed") {
		t.Errorf("acted as the installed endpoint's owner:\n%s\n%s", out, errb)
	}
	showTarget(t)
	if _, errb, _ = h.run("list"); !strings.Contains(errb, "owner "+h.e.Owner+" (from config.json \"owner\")") {
		t.Errorf("target line: %q", errb)
	}
}
