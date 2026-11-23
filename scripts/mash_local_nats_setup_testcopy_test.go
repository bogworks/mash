package scripts

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func mlntTestcopy(t *testing.T, src, dst string) ([]byte, error) {
	t.Helper()
	bash, err := exec.LookPath("bash")
	if err != nil {
		t.Skip("no bash")
	}
	return exec.Command(bash, "mash-local-nats-setup-testcopy.sh", src, dst).CombinedOutput()
}

// The shipped script reads only `uname -s`; the MLNT_UNAME hook exists in the
// tests-only copy.
func TestMashLocalNatsSetup_shippedHasNoUnameHook(t *testing.T) {
	b, err := os.ReadFile("mash-local-nats-setup.sh")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(b), "MLNT_UNAME") {
		t.Error("the shipped mash-local-nats-setup.sh mentions MLNT_UNAME; the hook belongs in the test copy only")
	}
}

func TestMashLocalNatsSetupTestcopy_addsTheHookOnly(t *testing.T) {
	dst := filepath.Join(t.TempDir(), "copy.sh")
	if out, err := mlntTestcopy(t, "mash-local-nats-setup.sh", dst); err != nil {
		t.Fatalf("testcopy: %v\n%s", err, out)
	}
	orig, _ := os.ReadFile("mash-local-nats-setup.sh")
	cp, err := os.ReadFile(dst)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(cp), "MLNT_UNAME") {
		t.Error("the test copy does not honour MLNT_UNAME")
	}
	ol, cl := strings.Split(string(orig), "\n"), strings.Split(string(cp), "\n")
	if len(ol) != len(cl) {
		t.Fatalf("line count differs: %d vs %d", len(ol), len(cl))
	}
	diff := 0
	for i := range ol {
		if ol[i] != cl[i] {
			diff++
		}
	}
	if diff != 1 {
		t.Errorf("want exactly one changed line, got %d", diff)
	}
	if fi, err := os.Stat(dst); err != nil || fi.Mode().Perm()&0o111 == 0 {
		t.Errorf("the copy is not executable: %v", err)
	}
}

func TestMashLocalNatsSetupTestcopy_failsWithoutTheAnchor(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "src.sh")
	dst := filepath.Join(dir, "dst.sh")
	if err := os.WriteFile(src, []byte("#!/usr/bin/env bash\necho hi\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	out, err := mlntTestcopy(t, src, dst)
	if err == nil || !strings.Contains(string(out), "exactly once") {
		t.Errorf("want a loud failure without the anchor, got err=%v\n%s", err, out)
	}
	if _, err := os.Stat(dst); err == nil {
		t.Error("a copy was written despite the missing anchor")
	}
	line := "    if [ \"$(uname -s 2>/dev/null || true)\" = Darwin ]; then\n"
	if err := os.WriteFile(src, []byte(line+line), 0o755); err != nil {
		t.Fatal(err)
	}
	if out, err := mlntTestcopy(t, src, dst); err == nil {
		t.Errorf("want a failure with the anchor twice, got success\n%s", out)
	}
}
