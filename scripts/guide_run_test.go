package scripts

import (
	"os/exec"
	"path/filepath"
	"testing"
)

// The guide-run harness (scripts/guide-run/guide-run.sh) is checked here without podman: bash -n, shellcheck when it
// is installed, and the harness's own selftest, which compares the guide's block ids with the step ids and runs fill
// on a fixture run folder (no line over 80 columns, a second fill changes nothing, a secret-shaped output stops it).

func guideRunScript(t *testing.T) string {
	t.Helper()
	p, err := filepath.Abs(filepath.Join("guide-run", "guide-run.sh"))
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func TestGuideRunSyntax(t *testing.T) {
	if out, err := exec.Command("bash", "-n", guideRunScript(t)).CombinedOutput(); err != nil {
		t.Fatalf("bash -n: %v\n%s", err, out)
	}
}

func TestGuideRunShellcheck(t *testing.T) {
	sc, err := exec.LookPath("shellcheck")
	if err != nil {
		t.Skip("shellcheck not installed")
	}
	if out, err := exec.Command(sc, "-x", guideRunScript(t)).CombinedOutput(); err != nil {
		t.Fatalf("shellcheck: %v\n%s", err, out)
	}
}

func TestGuideRunSelftest(t *testing.T) {
	for _, tool := range []string{"bash", "python3", "grep", "awk", "sort"} {
		if _, err := exec.LookPath(tool); err != nil {
			t.Skipf("%s not found", tool)
		}
	}
	cmd := exec.Command("bash", guideRunScript(t), "selftest")
	cmd.Env = append(cmd.Environ(), "TMPDIR="+t.TempDir())
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("selftest: %v\n%s", err, out)
	}
}
