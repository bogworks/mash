package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// nopickFixture is a world with one endpoint whose creds are installed in the config folder and no endpoint named.
func nopickFixture(t *testing.T) (env map[string]string, address string) {
	t.Helper()
	w := newWorld(t, "a")
	src := w.env["a"]
	address = src["MASH_ENDPOINT"]
	home := t.TempDir()
	dir := filepath.Join(home, ".config", "mash")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(src["MASH_CREDS"])
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, address+".creds"), b, 0o600); err != nil {
		t.Fatal(err)
	}
	return map[string]string{"HOME": home, "MASH_SERVER": src["MASH_SERVER"], "MASH_DANGEROUSLY_SKIP_NATS_ENCRYPTION": "1", "MASH_SETTLE_MS": "20"}, address
}

func TestServeNeverAutoPicksAnEndpoint(t *testing.T) {
	env, _ := nopickFixture(t)
	s := startServe(t, env)
	s.initialize(t)
	if names := toolNames(t, s); len(names) != 3 || !names["status"] || !names["doctor"] || !names["version"] {
		t.Fatalf("serve with no endpoint named exposes %v, want version, status and doctor", names)
	}
	const want = "No endpoint is chosen for this session.\nhint: start the session with MASH_ENDPOINT=<address>"
	for i, tool := range []string{"status", "doctor"} {
		w := want
		if tool == "doctor" { // no command of ours names a session's endpoint: doctor has no hint (#1099)
			w = "No endpoint is chosen for this session."
		}
		if got := reportText(t, s, 10+i, tool); got != w {
			t.Errorf("%s, no endpoint named: %q, want %q", tool, got, w)
		}
	}
}

func TestServeTakesTheNamedEndpoint(t *testing.T) {
	env, address := nopickFixture(t)
	env["MASH_ENDPOINT"] = address
	s := startServe(t, env)
	s.initialize(t)
	names := toolNames(t, s)
	for _, n := range []string{"send", "inbox", "read", "who", "status", "doctor", "version"} {
		if !names[n] {
			t.Fatalf("missing tool %s in %v", n, names)
		}
	}
}

func TestHookNeverAutoPicksAnEndpoint(t *testing.T) {
	env, _ := nopickFixture(t)
	var out, errb bytes.Buffer
	code := run([]string{"hook"}, envFrom(env), strings.NewReader(`{"hook_event_name":"UserPromptSubmit"}`), &out, &errb)
	if code != 0 || out.Len() != 0 {
		t.Fatalf("hook exited %d, stdout %q, stderr %q: want 0 and nothing printed", code, out.String(), errb.String())
	}
}

func TestOneShotCommandsStillAutoPick(t *testing.T) {
	env, address := nopickFixture(t)
	var out, errb bytes.Buffer
	if code := run([]string{"inbox"}, envFrom(env), strings.NewReader(""), &out, &errb); code != 0 {
		t.Fatalf("inbox exited %d: %s%s", code, out.String(), errb.String())
	}
	out.Reset()
	errb.Reset()
	run([]string{"status"}, envFrom(env), strings.NewReader(""), &out, &errb)
	if !strings.HasPrefix(out.String(), address+": ") {
		t.Fatalf("status does not show the auto-picked endpoint %s:\n%s%s", address, out.String(), errb.String())
	}
}
