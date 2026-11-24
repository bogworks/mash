package release

import (
	"os/exec"
	"strings"
	"testing"
)

func TestRepo_noCommittedBinariesOrLargeFiles(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	bad, err := CheckTrackedFiles("../..")
	if err != nil {
		t.Skipf("not a git checkout: %v", err)
	}
	if len(bad) > 0 {
		t.Errorf("committed binaries or large files (build outputs belong in .gitignore):\n  %s", strings.Join(bad, "\n  "))
	}
}
