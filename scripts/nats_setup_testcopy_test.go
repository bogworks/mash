package scripts

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// The shipped setup script reads `uname -s` and nothing else; the MLNT_UNAME hook exists only in the test copy (#1120).
func TestNatsSetupShippedScriptHasNoTestHook(t *testing.T) {
	raw, err := os.ReadFile("mash-local-nats-setup.sh")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "MLNT_") {
		t.Fatal("mash-local-nats-setup.sh names an MLNT_ test hook; the hook belongs in the test copy")
	}
}

func TestNatsSetupTestCopyAddsOnlyTheHook(t *testing.T) {
	bash, err := exec.LookPath("bash")
	if err != nil {
		t.Skip("no bash")
	}
	dst := filepath.Join(t.TempDir(), "setup.sh")
	if out, err := exec.Command(bash, "mash-local-nats-setup-testcopy.sh", "mash-local-nats-setup.sh", dst).CombinedOutput(); err != nil {
		t.Fatalf("testcopy: %v\n%s", err, out)
	}
	orig, _ := os.ReadFile("mash-local-nats-setup.sh")
	cp, _ := os.ReadFile(dst)
	const plain = `if [ "$(uname -s 2>/dev/null || true)" = Darwin ]; then`
	const hooked = `if [ "${MLNT_UNAME:-$(uname -s 2>/dev/null || true)}" = Darwin ]; then`
	if strings.Count(string(cp), "MLNT_UNAME") != 1 || string(cp) != strings.Replace(string(orig), plain, hooked, 1) {
		t.Fatal("the test copy differs from the script by more than the hook")
	}
}

// A script without the anchor is refused, so a change to it cannot turn the hook off unseen.
func TestNatsSetupTestCopyRefusesAScriptWithoutTheAnchor(t *testing.T) {
	bash, err := exec.LookPath("bash")
	if err != nil {
		t.Skip("no bash")
	}
	dir := t.TempDir()
	src := filepath.Join(dir, "src.sh")
	os.WriteFile(src, []byte("#!/bin/sh\necho hi\n"), 0o755)
	out, err := exec.Command(bash, "mash-local-nats-setup-testcopy.sh", src, filepath.Join(dir, "dst.sh")).CombinedOutput()
	if err == nil || !strings.Contains(string(out), "exactly once") {
		t.Fatalf("not refused: %v\n%s", err, out)
	}
}
