package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The global flags are taken out of the command line wherever they stand, and a flag shadows its environment variable.

func TestGlobalFlagsShadowEnvironmentAndNameTheFlagAsSource(t *testing.T) {
	h := newFleetEnv(t)
	showTarget(t)
	good := h.env["MASH_SERVER"]
	h.env["MASH_SERVER"] = "nats://127.0.0.1:1" // wrong: only the flag reaches the server
	for _, args := range [][]string{
		{"--server", good, "list"},
		{"list", "--server=" + good},
		{"--verbose", "list", "--no-color"},
	} {
		_, errb, code := h.run(args...)
		flagged := strings.Contains(strings.Join(args, " "), "--server")
		switch {
		case flagged && (code != 0 || !strings.Contains(errb, "target server "+good+" (from flag:--server)")):
			t.Errorf("%v: %d %q", args, code, errb)
		case !flagged && code == 0:
			t.Errorf("%v: reached the server without the flag", args)
		}
	}
}

func TestGlobalEndpointAndCredsFlagsSupplyOwnerAndAdminCreds(t *testing.T) {
	h := newFleetEnv(t)
	showTarget(t)
	admin := h.env["MASH_ADMIN_CREDS"]
	delete(h.env, "MASH_ENDPOINT")
	delete(h.env, "MASH_ADMIN_CREDS")
	if _, errb, code := h.run("list"); code == 0 || !strings.Contains(errb, "no owner") {
		t.Fatalf("no owner: %d %q", code, errb)
	}
	_, errb, code := h.run("--endpoint", h.e.Owner+".dev.x", "--creds", admin, "list")
	if code != 0 || !strings.Contains(errb, "owner "+h.e.Owner+" (from flag:--endpoint)") || !strings.Contains(errb, "admin creds "+admin+" (from flag:--creds)") {
		t.Fatalf("%d %q", code, errb)
	}
}

func TestGlobalConfigDirFlag(t *testing.T) {
	h := newFleetEnv(t)
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "config.json"), []byte(`{"servers":["`+h.e.URL+`"]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	delete(h.env, "MASH_SERVER")
	if _, errb, code := h.run("list"); code == 0 {
		t.Fatalf("list worked with no server: %q", errb)
	}
	if _, errb, code := h.run("list", "--config-dir", dir); code != 0 {
		t.Fatalf("--config-dir: %d %q", code, errb)
	}
}

func TestGlobalFlagErrorsAreUsageExit2(t *testing.T) {
	h := newFleetEnv(t)
	for _, args := range [][]string{{"list", "--server"}, {"--endpoint", "--json", "list"}} {
		_, errb, code := h.run(args...)
		if code != 2 || !strings.HasPrefix(errb, "mashctl: --") || !strings.Contains(errb, "needs a value") {
			t.Errorf("%v: %d %q", args, code, errb)
		}
	}
}

// mashctl's own --server (adopt's target) and --json (the verbs that parse it) are not taken as global options.
func TestGlobalFlagsLeaveTheVerbsOwnFlagsAlone(t *testing.T) {
	h := newFleetEnv(t)
	if so, _, code := h.run("endpoint", "show", "--bogus", "--json"); code != 2 || !strings.Contains(so, `"outcome": "error"`) {
		t.Errorf("endpoint --json: %d %q", code, so)
	}
	if so, _, _ := h.run("endpoint", "create", "o/g/x", "--note", "--json", "--bogus"); so != "" {
		t.Errorf("--json taken as a value printed %q", so)
	}
}
