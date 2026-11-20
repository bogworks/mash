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

// (a) init writes the owner, and mashctl then works from config.json alone (no MASH_ENDPOINT).
func TestInitWritesTheOwnerAndMashctlUsesIt(t *testing.T) {
	a := newIniter(t, newApplyEnv(t))
	if out, errb, code := a.init(); code != 0 {
		t.Fatalf("init: %d\n%s\n%s", code, out, errb)
	}
	raw, err := os.ReadFile(filepath.Join(a.env["MASH_CONFIG_DIR"], "config.json"))
	if err != nil || !strings.Contains(string(raw), `"owner": "alice"`) {
		t.Fatalf("config.json: %s %v", raw, err)
	}
	env := func(k string) string { return a.env[k] }
	cfg, err := loadConfig(env, lk(env))
	if err != nil || cfg.Owner != "alice" || cfg.SysCreds != filepath.Join(a.env["MASH_CONFIG_DIR"], "server", "sys.creds") {
		t.Fatalf("mashctl owner and system creds after init: %q %q %v", cfg.Owner, cfg.SysCreds, err)
	}
}

// (b) two installed endpoint creds files do not stop an owner-scoped command.
func TestSeveralInstalledEndpointsDoNotStopMashctl(t *testing.T) {
	h := newFleetEnv(t)
	delete(h.env, "MASH_ENDPOINT")
	if err := os.WriteFile(filepath.Join(h.dir, "config.json"), []byte(`{"owner":"`+h.e.Owner+`"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	installEndpointCreds(t, h.dir, h.e.Owner+".dev.a.creds")
	installEndpointCreds(t, h.dir, h.e.Owner+".dev.b.creds")
	if _, errb, code := h.run("limits", "apply"); code != 0 || strings.Contains(errb, "more than one") {
		t.Fatalf("limits with two installed endpoints: %d %q", code, errb)
	}
}

// (c) a single installed endpoint of another owner does not change who mashctl acts as.
func TestForeignInstalledEndpointDoesNotChangeMashctlsOwner(t *testing.T) {
	h := newFleetEnv(t)
	delete(h.env, "MASH_ENDPOINT")
	installEndpointCreds(t, h.dir, "zed.dev.x.creds")
	// no owner configured: mashctl says so, it does not become zed
	if _, errb, code := h.run("limits", "apply"); code != 1 || !strings.Contains(errb, "no owner") {
		t.Fatalf("no owner configured: %d %q", code, errb)
	}
	// owner configured: it acts as that owner
	if err := os.WriteFile(filepath.Join(h.dir, "config.json"), []byte(`{"owner":"`+h.e.Owner+`"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	out, errb, code := h.run("limits", "apply")
	if code != 0 {
		t.Fatalf("limits: %d %q", code, errb)
	}
	if strings.Contains(out+errb, "zed") {
		t.Errorf("acted as the installed endpoint's owner:\n%s\n%s", out, errb)
	}
	if _, errb, _ = h.run("limits", "apply", "--verbose"); !strings.Contains(errb, "owner "+h.e.Owner+" (from config.json \"owner\")") {
		t.Errorf("target line: %q", errb)
	}
}
