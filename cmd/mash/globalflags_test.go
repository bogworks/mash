package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The global flags are taken out of the command line before any verb runs, and a flag shadows its environment variable.

func statusOut(t *testing.T, env map[string]string, args ...string) (string, string, int) {
	t.Helper()
	var out, errb strings.Builder
	code := run(args, envStrict(env), strings.NewReader(""), &out, &errb)
	return out.String(), errb.String(), code
}

func TestGlobalEndpointFlagShadowsEnvironment(t *testing.T) {
	env := map[string]string{"HOME": t.TempDir(), "MASH_ENDPOINT": "env.dev.fromenv", "MASH_SERVER": "tls://x:4222"}
	out, _, code := statusOut(t, env, "status") // no creds: not ready, but it names the endpoint it chose
	if code != 1 || !strings.HasPrefix(out, "env.dev.fromenv: not ready: ") {
		t.Fatalf("env: %d %s", code, out)
	}
	for _, args := range [][]string{
		{"--endpoint", "flag.dev.fromflag", "status"},
		{"status", "--endpoint", "flag.dev.fromflag"},
		{"status", "--endpoint=flag.dev.fromflag"},
	} {
		out, errb, code := statusOut(t, env, args...)
		if code != 1 || !strings.HasPrefix(out, "flag.dev.fromflag: not ready: ") || strings.Contains(out, "fromenv") {
			t.Errorf("%v: %d %s %s", args, code, out, errb)
		}
	}
}

func TestGlobalServerAndConfigDirFlags(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "config.json"), []byte(`{"servers":"tls://file:4222","endpoint":"file.dev.fromfile"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	env := map[string]string{"HOME": t.TempDir(), "MASH_SERVER": "tls://env:4222", "MASH_OPEN_TIMEOUT": "2s"}
	out, _, code := statusOut(t, env, "--config-dir", dir, "--server", "tls://flag1:4222", "--server=tls://flag2:4222", "doctor")
	if code != 1 || !strings.Contains(out, "tls://flag1:4222,tls://flag2:4222") {
		t.Fatalf("%d %s", code, out)
	}
	if st, _, code := statusOut(t, env, "--config-dir", dir, "status"); code != 1 || !strings.HasPrefix(st, "file.dev.fromfile: not ready: ") {
		t.Fatalf("the endpoint of config.json: %d %s", code, st)
	}
	if strings.Contains(out, "tls://env:4222") || strings.Contains(out, "tls://file:4222") {
		t.Errorf("a flag did not shadow:\n%s", out)
	}
}

func TestGlobalFlagErrorIsUsageExit2(t *testing.T) {
	env := map[string]string{"HOME": t.TempDir()}
	for _, args := range [][]string{{"status", "--endpoint"}, {"--server"}, {"who", "--ca"}} {
		out, errb, code := statusOut(t, env, args...)
		if code != 2 || out != "" || !strings.HasPrefix(errb, "error: --") || !strings.Contains(errb, "needs a value") || !strings.HasSuffix(errb, "hint: mash --help\n") {
			t.Errorf("%v: %d %q %q", args, code, out, errb)
		}
	}
	if out, errb, code := statusOut(t, env, "--json"); code != 2 || errb != "" || out != `{"error":"mash needs a command","hint":"mash --help"}`+"\n" {
		t.Errorf("only a global flag: %d %q %q", code, out, errb)
	}
}

// --verbose, --json and --no-color are accepted anywhere and stripped, so a verb that takes no arguments is not refused.
func TestGlobalVerboseAndNoColorAreStripped(t *testing.T) {
	env := map[string]string{"HOME": t.TempDir()}
	for _, args := range [][]string{{"--verbose", "status"}, {"status", "-v", "--no-color"}} {
		if out, errb, code := statusOut(t, env, args...); code != 2 || out != "" || errb != "error: no endpoint is installed here\nhint: mash join <file>\n" {
			t.Errorf("%v: %d %s %s", args, code, out, errb)
		}
	}
}

// A verb that parses --json itself still sees it, before or after the verb.
func TestGlobalJSONStillReachesTheVerbsThatParseIt(t *testing.T) {
	for _, pos := range []string{"before", "after"} {
		seed := filepath.Join(t.TempDir(), "k.seed")
		args := []string{"key", "create", "--out", seed}
		if pos == "before" {
			args = append([]string{"--json"}, args...)
		} else {
			args = append(args, "--json")
		}
		out, errb, code := statusOut(t, map[string]string{"HOME": t.TempDir()}, args...)
		var m map[string]any
		if code != 0 || json.Unmarshal([]byte(out), &m) != nil {
			t.Errorf("--json %s the verb: %d stdout %q stderr %q", pos, code, out, errb)
		}
	}
}

// The old names are not read.
func TestOldEnvironmentNamesAreNotRead(t *testing.T) {
	env := map[string]string{"HOME": t.TempDir(), "MASH_OWNER": "alice", "MASH_GROUP": "dev", "MASH_NAME": "design", "MASH_URL": "tls://old:4222", "MASH_TLS_CA": "/old.pem"}
	out, errb, code := statusOut(t, env, "status")
	if code != 2 || out != "" || errb != "error: no endpoint is installed here\nhint: mash join <file>\n" {
		t.Fatalf("%d %s %s", code, out, errb)
	}
}
