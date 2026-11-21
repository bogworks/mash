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
	good := h.env["MASH_SERVER"]
	h.env["MASH_SERVER"] = "nats://127.0.0.1:1" // wrong: only the flag reaches the server
	for _, args := range [][]string{
		{"--detail", "--server", good, "limits", "apply"},
		{"limits", "apply", "--detail", "--server=" + good},
		{"--detail", "limits", "apply", "--no-color"},
	} {
		_, errb, code := h.run(args...)
		flagged := strings.Contains(strings.Join(args, " "), "--server")
		switch {
		case flagged && (code != 0 || strings.Contains(errb, "target server")):
			t.Errorf("%v: %d %q", args, code, errb)
		case !flagged && code == 0:
			t.Errorf("%v: reached the server without the flag", args)
		}
	}
}

func TestGlobalEndpointAndCredsFlagsSupplyOwnerAndAdminCreds(t *testing.T) {
	h := newFleetEnv(t)
	admin := h.env["MASH_ADMIN_CREDS"]
	delete(h.env, "MASH_ENDPOINT")
	delete(h.env, "MASH_ADMIN_CREDS")
	if _, errb, code := h.run("limits", "apply"); code == 0 || !strings.Contains(errb, "no owner") {
		t.Fatalf("no owner: %d %q", code, errb)
	}
	_, errb, code := h.run("--detail", "--endpoint", h.e.Owner+".dev.x", "--creds", admin, "limits", "apply")
	if code != 0 || errb != "" {
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
	if _, errb, code := h.run("limits", "apply"); code == 0 {
		t.Fatalf("limits worked with no server: %q", errb)
	}
	if _, errb, code := h.run("limits", "apply", "--config-dir", dir); code != 0 {
		t.Fatalf("--config-dir: %d %q", code, errb)
	}
}

func TestGlobalFlagErrorsAreUsageExit2(t *testing.T) {
	h := newFleetEnv(t)
	for _, args := range [][]string{{"limits", "apply", "--server"}, {"--endpoint", "--json", "limits", "apply"}} {
		_, errb, code := h.run(args...)
		if code != 2 || !strings.HasPrefix(errb, "mashctl: --") || !strings.Contains(errb, "needs a value") {
			t.Errorf("%v: %d %q", args, code, errb)
		}
	}
}

// mashctl's own --json (the verbs that parse it) are not taken as global options.
func TestGlobalFlagsLeaveTheVerbsOwnFlagsAlone(t *testing.T) {
	h := newFleetEnv(t)
	if so, _, code := h.run("endpoint", "show", "--bogus", "--json"); code != 2 || !strings.HasPrefix(so, `{"error":"unknown option --bogus"`) {
		t.Errorf("endpoint --json: %d %q", code, so)
	}
	if so, _, _ := h.run("endpoint", "create", "o.g.x", "--out", "--json", "--bogus"); so != "" {
		t.Errorf("--json taken as a value printed %q", so)
	}
}
