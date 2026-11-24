package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bogworks/mash/internal/cliframe"
	"github.com/bogworks/mash/internal/natsauth"
)

const (
	skipVar     = "MASH_DANGEROUSLY_SKIP_NATS_ENCRYPTION"
	skipWarning = "warning: no encryption to the server (" + skipVar + ")"
)

func TestSkipEncryptionWarnsOnceOnStderrForEveryMashctlCommand(t *testing.T) {
	keepSkipWarning = true
	t.Cleanup(func() { keepSkipWarning = false })
	e := newApplyEnv(t).withDirectory() // a plain server; the helpers set the override
	e.env[skipVar] = "1"
	e.env["MASH_ADMIN_CREDS"] = e.pl.Creds
	for _, args := range [][]string{{"version"}, {"endpoint", "list"}, {"endpoint", "list", "--json"}, {"doctor"}} {
		out, errb, _ := e.run(args...)
		if n := strings.Count(errb, skipWarning); n != 1 {
			t.Errorf("%v: %d warnings on stderr, want 1: %q", args, n, errb)
		}
		if strings.Contains(out, "no encryption to the server") {
			t.Errorf("%v: the warning is on stdout: %q", args, out)
		}
	}
	e.env[skipVar] = ""
	for _, args := range [][]string{{"version"}} {
		out, errb, _ := e.run(args...)
		if strings.Contains(out+errb, "no encryption to the server") {
			t.Errorf("%v unset: warned: %q %q", args, out, errb)
		}
	}
}

func TestSkipEncryptionMashctlRefusesOldNamesAndBadValues(t *testing.T) {
	keepSkipWarning = true
	t.Cleanup(func() { keepSkipWarning = false })
	e := newApplyEnv(t).withDirectory()
	for _, tc := range []struct{ key, val, want string }{
		{"MASH_INSECURE_PLAINTEXT", "1", "MASH_INSECURE_PLAINTEXT is no longer read; set " + skipVar + "=1 instead"},
		{skipVar, "yes", skipVar + `="yes"`},
	} {
		e.env[skipVar] = ""
		e.env[tc.key] = tc.val
		for _, args := range [][]string{{"endpoint", "list"}, {"doctor"}, {"status"}} {
			out, errb, code := e.run(args...)
			if code != 2 || !strings.Contains(strings.ReplaceAll(out+errb, `\"`, `"`), tc.want) {
				t.Errorf("%s=%s %v: exit %d, want 2 with %q: %q %q", tc.key, tc.val, args, code, tc.want, out, errb)
			}
		}
		delete(e.env, tc.key)
	}
	// the removed config.json key
	e.env[skipVar] = "1"
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "config.json"), []byte(`{"insecure_plaintext":true}`), 0o600); err != nil {
		t.Fatal(err)
	}
	e.env["MASH_CONFIG_DIR"] = dir
	_, errb, code := e.run("endpoint", "list")
	if want := "error: insecure_plaintext in config.json is no longer read; set " + skipVar + "=1 instead"; code != 2 || !strings.Contains(errb, want) {
		t.Errorf("old config key: exit %d, want 2 with %q: %q", code, want, errb)
	}
}

func TestSkipEncryptionMashctlEntryPoint(t *testing.T) {
	keepSkipWarning = true
	t.Cleanup(func() { keepSkipWarning = false })
	e := newApplyEnv(t).withDirectory()
	e.env[skipVar] = "1"
	for _, args := range [][]string{{"--help"}, {"-h"}, {"help"}, {"version"}, {"version", "--json"}, {"endpoint", "--help"}, {"doctor", "--help"}} {
		out, errb, code := e.run(args...)
		if code != 0 || strings.Count(errb, skipWarning) != 1 || strings.Contains(out, "no encryption to the server") {
			t.Errorf("%v: exit %d, stderr %q: want 0 and one warning on stderr only", args, code, errb)
		}
	}
	// the warning is about the variable, not the URL: a tls:// server draws it too
	e.env["MASH_SERVER"] = "tls://127.0.0.1:1"
	if _, errb, _ := e.run("version"); strings.Count(errb, skipWarning) != 1 {
		t.Errorf("tls:// server: stderr %q, want the warning once", errb)
	}
	// the refusal of the removed names and a bad value reaches every command, help and version included
	for _, tc := range []struct{ key, val, want string }{
		{"MASH_INSECURE_PLAINTEXT", "1", "MASH_INSECURE_PLAINTEXT is no longer read; set " + skipVar + "=1 instead"},
		{skipVar, "yes", skipVar + `="yes"`},
	} {
		e.env[skipVar] = ""
		e.env[tc.key] = tc.val
		for _, args := range [][]string{{"version"}, {"version", "--json"}, {"help"}, {"--help"}, {"-h"}, {"limits", "--help"}} {
			out, errb, code := e.run(args...)
			if code != 2 || !strings.Contains(strings.ReplaceAll(out+errb, `\"`, `"`), tc.want) {
				t.Errorf("%s=%s %v: exit %d, want 2 with %q: %q %q", tc.key, tc.val, args, code, tc.want, out, errb)
			}
		}
		delete(e.env, tc.key)
	}
}

// The exit code 2 of a refused plain URL comes from the error (natsauth.PlainRefusalError, found with errors.As),
// never from output text: an ordinary failure whose message merely contains the refusal's words stays at 1.
func TestExitCodeIsNotDecidedByOutputText(t *testing.T) {
	const marker = "every mash connection requires TLS, to any host"
	_, _, refusal := natsauth.RequireTLS("nats://192.0.2.1:4222", false)
	if refusal == nil {
		t.Fatal("RequireTLS accepted a plain URL")
	}
	for _, tc := range []struct {
		name string
		err  error
		want int
	}{
		{"text only", errors.New("connect: " + marker), 1},
		{"text only, cliframe failure", cliframe.Failf("%s", marker), 1},
		{"text only, withPlainCode of another cause", withPlainCode(cliframe.Failf("%s", marker), errors.New(marker)), 1},
		{"the refusal, as a framed failure", withPlainCode(cliframe.Failf("%v", refusal), refusal), 2},
		{"the refusal, wrapped", fmt.Errorf("mashctl: %w", refusal), 2},
		{"the refusal, flattened to text and recoded", withPlainCode(fmt.Errorf("connect: %v", refusal), refusal), 2},
	} {
		var out, errb strings.Builder
		if code := cliframe.Report(&out, &errb, tc.err, false); code != tc.want {
			t.Errorf("%s: exit %d, want %d (%q)", tc.name, code, tc.want, errb.String())
		}
	}
}

// Every invocation warns or refuses, even one that fails before a command is chosen: bare `mashctl`, a removed
// command, a bad global flag.
func TestSkipEncryptionMashctlWarnsBeforeArgumentHandling(t *testing.T) {
	keepSkipWarning = true
	t.Cleanup(func() { keepSkipWarning = false })
	e := newApplyEnv(t).withDirectory()
	e.env[skipVar] = "1"
	var removed string
	for name := range removedCommands {
		removed = name
		break
	}
	for _, args := range [][]string{{}, {removed}, {"--no-such-flag"}, {"--endpoint"}} {
		_, errb, code := e.run(args...)
		if code == 0 || strings.Count(errb, skipWarning) != 1 {
			t.Errorf("%v: exit %d, stderr %q: want a failure with one warning", args, code, errb)
		}
	}
	for _, tc := range []struct{ key, val, hint string }{
		{"MASH_INSECURE_PLAINTEXT", "1", "hint: unset MASH_INSECURE_PLAINTEXT; use " + skipVar + "=1\n"},
		{skipVar, "yes", "hint: set " + skipVar + "=1 or unset it\n"},
	} {
		e.env[skipVar] = ""
		e.env[tc.key] = tc.val
		for _, args := range [][]string{{}, {removed}, {"--no-such-flag"}} {
			_, errb, code := e.run(args...)
			if code != 2 || !strings.HasSuffix(errb, tc.hint) || strings.Contains(errb, "mash doctor") {
				t.Errorf("%s=%s %v: exit %d, stderr %q, want 2 ending %q", tc.key, tc.val, args, code, errb, tc.hint)
			}
		}
		delete(e.env, tc.key)
	}
}

// Errors do not drop the warning: a usage error and a failing command each print it exactly once, first.
func TestSkipEncryptionMashctlWarnsOnceOnErroringCommands(t *testing.T) {
	keepSkipWarning = true
	t.Cleanup(func() { keepSkipWarning = false })
	e := newApplyEnv(t).withDirectory()
	e.env[skipVar] = "1"
	for _, args := range [][]string{{"nosuchcommand"}, {"endpoint", "nosuchverb"}, {"endpoint", "show"}} {
		out, errb, code := e.run(args...)
		if code == 0 {
			t.Errorf("%v: exit 0, want a failure", args)
		}
		if n := strings.Count(errb, skipWarning); n != 1 || !strings.HasPrefix(errb, skipWarning+"\n") {
			t.Errorf("%v: stderr %q, want the warning once, first", args, errb)
		}
		if strings.Contains(out, "no encryption to the server") {
			t.Errorf("%v: the warning is on stdout: %q", args, out)
		}
	}
}
