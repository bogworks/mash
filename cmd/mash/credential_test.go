package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// mashCred runs `mash credential ARGS` with an empty environment: no config, no server, no HOME.
func mashCred(args ...string) (rc int, stdout, stderr string) {
	var so, se bytes.Buffer
	rc = run(append([]string{"credential"}, args...), func(string) string { return "" }, strings.NewReader(""), &so, &se)
	return rc, so.String(), se.String()
}

func TestMashCredentialKeyAndInstallNeedNoEnvironment(t *testing.T) {
	dir := t.TempDir()
	seed := filepath.Join(dir, "ep.seed")
	rc, so, se := mashCred("key", "--out", seed)
	if rc != 0 || !strings.Contains(so, "public key: U") || se != "" {
		t.Fatalf("rc %d %q %q", rc, so, se)
	}
	seedText, _ := os.ReadFile(seed)
	if fi, _ := os.Stat(seed); fi.Mode().Perm() != 0o600 || strings.Contains(so, strings.TrimSpace(string(seedText))) {
		t.Fatalf("seed file mode %v, or the seed was printed", fi.Mode())
	}
	if rc, _, _ := mashCred("key", "--out", seed); rc == 0 {
		t.Fatal("key overwrote the seed")
	}
	if rc, _, _ := mashCred("install", "--seed", seed); rc != 2 {
		t.Fatalf("install without --jwt: rc %d", rc)
	}
}

func TestMashCredentialHelpRunsNothing(t *testing.T) {
	dir := t.TempDir()
	out := filepath.Join(dir, "x")
	for _, args := range [][]string{{"help"}, {"--help"}, {"key", "--help"}, {"key", "--out", out, "--help"}, {"install", "-h"}} {
		rc, so, se := mashCred(args...)
		if rc != 0 || !strings.Contains(so, "usage: mash credential") || se != "" {
			t.Fatalf("%v: rc %d %q %q", args, rc, so, se)
		}
	}
	if _, err := os.Stat(out); err == nil {
		t.Fatal("--help wrote a file")
	}
	if rc, _, _ := mashCred(); rc != 2 {
		t.Fatalf("no verb: rc %d", rc)
	}
	if rc, _, _ := mashCred("issue"); rc != 2 {
		t.Fatalf("issue is mashctl's: rc %d", rc)
	}
}
