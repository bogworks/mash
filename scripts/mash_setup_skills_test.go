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
	for _, tool := range []string{"bash", "jq"} {
		if _, err := exec.LookPath(tool); err != nil {
			t.Skipf("%s not found", tool)
		}
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
