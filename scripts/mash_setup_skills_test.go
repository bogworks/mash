package scripts

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// The static checker for the mash-setup plugin and its skills runs here, so CI's `go test ./...` runs it: it reads files
// only (no network, no containers) and needs bash and jq.
func TestMashSetupSkillsStatic(t *testing.T) {
	if _, err := exec.LookPath("bash"); err != nil {
		t.Skip("bash not found")
	}
	if _, err := exec.LookPath("jq"); err != nil {
		if os.Getenv("CI_ALLOW_NO_JQ") != "1" {
			t.Fatal("jq not found: install it, or set CI_ALLOW_NO_JQ=1 to skip this check")
		}
		t.Skip("jq not found (CI_ALLOW_NO_JQ=1)")
	}
	root, err := filepath.Abs("..")
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("bash", "scripts/mash-setup-skills_test.sh")
	cmd.Dir = root
	cmd.Env = append(os.Environ(), "MASH_NATS_SETUP=scripts/mash-local-nats-setup.sh")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("mash-setup-skills_test.sh failed: %v\n%s", err, out)
	}
}
