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

// The egress proxy of published mode (scripts/guide-run/egressproxy) has its own unit tests: the address vetting
// (loopback, private, link-local, the tailnet's CGNAT range, ULA, multicast, rebinding answers) and the allowlist,
// with no network. They run here too, so one `go test ./scripts/` covers the harness.
func TestGuideRunEgressProxy(t *testing.T) {
	goBin, err := exec.LookPath("go")
	if err != nil {
		t.Skip("go not found")
	}
	if out, err := exec.Command(goBin, "vet", "./guide-run/egressproxy").CombinedOutput(); err != nil {
		t.Fatalf("go vet: %v\n%s", err, out)
	}
	if out, err := exec.Command(goBin, "test", "-count=1", "./guide-run/egressproxy").CombinedOutput(); err != nil {
		t.Fatalf("egressproxy tests: %v\n%s", err, out)
	}
	if gofmt, err := exec.LookPath("gofmt"); err == nil {
		if out, err := exec.Command(gofmt, "-l", "guide-run/egressproxy").CombinedOutput(); err != nil || len(out) > 0 {
			t.Fatalf("gofmt -l: %v\n%s", err, out)
		}
	}
}
