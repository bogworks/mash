package main

import (
	"bytes"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// The one way to allow a plain-text connection is MASH_DANGEROUSLY_SKIP_NATS_ENCRYPTION=1, and every command says so
// on standard error while it is set.
const (
	skipVar     = "MASH_DANGEROUSLY_SKIP_NATS_ENCRYPTION"
	skipWarning = "warning: encryption to the server is OFF (" + skipVar + " is set)"
)

func runCapture(env map[string]string, args ...string) (string, string, int) {
	var out, errb bytes.Buffer
	code := run(args, envStrict(env), strings.NewReader(""), &out, &errb)
	return out.String(), errb.String(), code
}

func TestSkipEncryptionWarnsOnceOnStderrForEveryCommand(t *testing.T) {
	c := newConnectCounter(t)
	env := withRealCreds(t, plainEnv(t, "nats://"+c.addr))
	env["MASH_OPEN_TIMEOUT"] = "300ms"
	env[skipVar] = "1"
	for _, args := range [][]string{{"version"}, {"status"}, {"doctor"}, {"inbox"}, {"version", "--json"}} {
		out, errb, _ := runCapture(env, args...)
		if n := strings.Count(errb, skipWarning); n != 1 {
			t.Errorf("%v: %d warnings on stderr, want 1: %q", args, n, errb)
		}
		if strings.Contains(out, "encryption to the server is OFF") {
			t.Errorf("%v: the warning is on stdout: %q", args, out)
		}
		if strings.Contains(errb, "plain-text connection") {
			t.Errorf("%v: the old per-connection warning is still printed: %q", args, errb)
		}
	}
	if c.n.Load() == 0 {
		t.Error("no connection was made with the override set")
	}
}

func TestSkipEncryptionUnsetOrZeroPrintsNothing(t *testing.T) {
	for _, v := range []string{"", "0"} {
		env := plainEnv(t, "tls://127.0.0.1:1")
		env[skipVar] = v
		for _, args := range [][]string{{"version"}, {"doctor"}} {
			out, errb, _ := runCapture(env, args...)
			if strings.Contains(out+errb, "encryption to the server is OFF") {
				t.Errorf("value %q %v: warned: %q %q", v, args, out, errb)
			}
		}
	}
}

func TestSkipEncryptionJSONKeepsStdoutClean(t *testing.T) {
	env := plainEnv(t, "nats://127.0.0.1:1")
	env[skipVar] = "1"
	env["MASH_OPEN_TIMEOUT"] = "300ms"
	out, errb, _ := runCapture(env, "version", "--json")
	var v map[string]any
	if err := json.Unmarshal([]byte(out), &v); err != nil {
		t.Fatalf("stdout is not one JSON document: %q (%v)", out, err)
	}
	if errb != skipWarning+"\n" {
		t.Errorf("stderr %q, want only the warning", errb)
	}
}

func TestSkipEncryptionRefusesTheOldVariableAndBadValues(t *testing.T) {
	for _, tc := range []struct {
		name, key, val, want string
	}{
		{"old variable", "MASH_INSECURE_PLAINTEXT", "1", "MASH_INSECURE_PLAINTEXT is no longer read; set " + skipVar + "=1 instead"},
		{"old variable 0", "MASH_INSECURE_PLAINTEXT", "0", "MASH_INSECURE_PLAINTEXT is no longer read; set " + skipVar + "=1 instead"},
		{"yes", skipVar, "yes", skipVar + `="yes"`},
		{"true", skipVar, "true", skipVar + `="true"`},
	} {
		env := plainEnv(t, "nats://127.0.0.1:1")
		env[tc.key] = tc.val
		for _, args := range [][]string{{"who"}, {"who", "--json"}} {
			out, errb, code := runCapture(env, args...)
			if code != 2 || !strings.Contains(strings.ReplaceAll(out+errb, `\"`, `"`), tc.want) {
				t.Errorf("%s %v: exit %d, want 2 with %q: %q %q", tc.name, args, code, tc.want, out, errb)
			}
		}
		if _, errb, _ := runCapture(env, "inbox"); !strings.HasPrefix(errb, "error: ") {
			t.Errorf("%s: stderr %q does not start with error:", tc.name, errb)
		}
	}
}

func TestSkipEncryptionRefusesTheOldConfigKey(t *testing.T) {
	for _, v := range []string{"true", "false"} {
		env := plainEnv(t, "nats://127.0.0.1:1")
		dir := t.TempDir()
		if err := os.WriteFile(filepath.Join(dir, "config.json"), []byte(`{"insecure_plaintext":`+v+`}`), 0o600); err != nil {
			t.Fatal(err)
		}
		env["MASH_CONFIG_DIR"] = dir
		_, errb, code := runCapture(env, "inbox")
		want := "error: insecure_plaintext in config.json is no longer read; set " + skipVar + "=1 instead"
		if code != 2 || !strings.Contains(errb, want) {
			t.Errorf("%s: exit %d, want 2 with %q: %q", v, code, want, errb)
		}
	}
}

// Both tools check the override once, at the entry point, before any command dispatches: help and version print the
// warning too, and the removed names and a bad value are refused for every command.
func TestSkipEncryptionHelpAndVersionWarn(t *testing.T) {
	env := plainEnv(t, "tls://127.0.0.1:1")
	env[skipVar] = "1"
	for _, args := range [][]string{{"--help"}, {"-h"}, {"help"}, {"help", "send"}, {"send", "--help"}, {"version"}, {"version", "--json"}} {
		out, errb, code := runCapture(env, args...)
		if code != 0 || strings.Count(errb, skipWarning) != 1 || strings.Contains(out, "encryption to the server is OFF") {
			t.Errorf("%v: exit %d, stderr %q: want 0 and one warning on stderr only", args, code, errb)
		}
	}
}

func TestSkipEncryptionRefusalReachesEveryCommand(t *testing.T) {
	for _, tc := range []struct{ key, val, want string }{
		{"MASH_INSECURE_PLAINTEXT", "1", "MASH_INSECURE_PLAINTEXT is no longer read; set " + skipVar + "=1 instead"},
		{skipVar, "yes", skipVar + `="yes"`},
	} {
		env := plainEnv(t, "tls://127.0.0.1:1")
		env[tc.key] = tc.val
		for _, args := range [][]string{{"version"}, {"--help"}, {"help"}, {"plugin"}, {"key"}, {"credential"}, {"status"}, {"doctor"}, {"send", "--help"}} {
			out, errb, code := runCapture(env, args...)
			if code != 2 || !strings.Contains(out+errb, tc.want) {
				t.Errorf("%s=%s %v: exit %d, want 2 with %q: %q %q", tc.key, tc.val, args, code, tc.want, out, errb)
			}
		}
	}
	// the removed config.json key, on a command that reads no config
	env := plainEnv(t, "tls://127.0.0.1:1")
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "config.json"), []byte(`{"insecure_plaintext":true}`), 0o600); err != nil {
		t.Fatal(err)
	}
	env["MASH_CONFIG_DIR"] = dir
	for _, args := range [][]string{{"version"}, {"--help"}, {"plugin"}} {
		if _, errb, code := runCapture(env, args...); code != 2 || !strings.Contains(errb, "insecure_plaintext in config.json is no longer read") {
			t.Errorf("old config key %v: exit %d: %q", args, code, errb)
		}
	}
}

// A hook never blocks the session: the refusal is said on stderr, the exit is 0, nothing is read and nothing printed.
func TestSkipEncryptionHookRefusalExitsZeroAndReadsNothing(t *testing.T) {
	for _, tc := range []struct{ key, val, want string }{
		{"MASH_INSECURE_PLAINTEXT", "1", "mash hook: MASH_INSECURE_PLAINTEXT is no longer read; set " + skipVar + "=1 instead"},
		{skipVar, "yes", "mash hook: " + skipVar + `="yes"`},
	} {
		env := plainEnv(t, "tls://127.0.0.1:1")
		env[tc.key] = tc.val
		in := &readCounter{}
		var out, errb bytes.Buffer
		code := run([]string{"hook"}, envStrict(env), in, &out, &errb)
		if code != 0 || out.Len() != 0 || !strings.Contains(errb.String(), tc.want) || in.n != 0 {
			t.Errorf("%s: exit %d, stdout %q, stderr %q, stdin reads %d", tc.key, code, out.String(), errb.String(), in.n)
		}
	}
}

type readCounter struct{ n int }

func (r *readCounter) Read([]byte) (int, error) { r.n++; return 0, io.EOF }

// The hook prints the warning on stderr and leaves its stdout as it was; serve's stdout stays JSON-RPC lines only. The
// warning shows with a tls:// server too: it is about the variable, not the URL.
func TestSkipEncryptionHookAndServeKeepStdoutClean(t *testing.T) {
	for _, url := range []string{"tls://127.0.0.1:1", "nats://127.0.0.1:1"} {
		env := withRealCreds(t, plainEnv(t, url))
		env[skipVar] = "1"
		env["MASH_OPEN_TIMEOUT"] = "300ms"
		var out, errb bytes.Buffer
		if code := run([]string{"hook"}, envStrict(env), strings.NewReader(`{"hook_event_name":"UserPromptSubmit"}`), &out, &errb); code != 0 || out.Len() != 0 || strings.Count(errb.String(), skipWarning) != 1 {
			t.Errorf("hook %s: exit %d, stdout %q, stderr %q", url, code, out.String(), errb.String())
		}
		inR, inW := io.Pipe()
		var sout, serr lockedBuf
		done := make(chan int, 1)
		go func() { done <- run([]string{"serve"}, envStrict(env), inR, &sout, &serr) }()
		io.WriteString(inW, `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18"}}`+"\n")
		time.Sleep(500 * time.Millisecond)
		inW.Close()
		select {
		case <-done:
		case <-time.After(10 * time.Second):
			t.Fatalf("serve %s did not exit when stdin closed", url)
		}
		if n := strings.Count(serr.String(), skipWarning); n != 1 {
			t.Errorf("serve %s: %d warnings on stderr, want 1: %q", url, n, serr.String())
		}
		lines := strings.Split(strings.TrimSpace(sout.String()), "\n")
		for _, l := range lines {
			var m map[string]any
			if json.Unmarshal([]byte(l), &m) != nil || m["jsonrpc"] != "2.0" {
				t.Errorf("serve %s: stdout line is not JSON-RPC: %q", url, l)
			}
		}
		if len(lines) == 0 || lines[0] == "" {
			t.Errorf("serve %s: no JSON-RPC output", url)
		}
	}
}

func (l *lockedBuf) String() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.String()
}
