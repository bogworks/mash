package main

import (
	"bytes"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bogworks/mash/internal/testenv"
)

func TestRun_requiresOutVersionAndCommit(t *testing.T) {
	for _, args := range [][]string{{}, {"-out", "x"}, {"-out", "x", "-version", "1"}, {"-version", "1", "-commit", "c"}} {
		if err := run(args, &bytes.Buffer{}); err == nil || !strings.Contains(err.Error(), "required") {
			t.Errorf("%v: err = %v", args, err)
		}
	}
}

// The commit stamped into the binary must be the commit built: a different (or abbreviated) commit argument for the
// same checkout is refused, never built.
func TestRun_refusesACommitThatIsNotTheCheckout(t *testing.T) {
	root, err := exec.Command("git", "rev-parse", "--show-toplevel").Output()
	if err != nil {
		t.Fatal(err)
	}
	src := filepath.Join(t.TempDir(), "src")
	if out, err := testenv.RunGit("clone", "-q", strings.TrimSpace(string(root)), src); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	head, _ := exec.Command("git", "-C", src, "rev-parse", "HEAD").Output()
	x := strings.TrimSpace(string(head))
	out := filepath.Join(t.TempDir(), "mash")
	for name, commit := range map[string]string{"another commit": strings.Repeat("a", 40), "abbreviated": x[:12], "uppercase": strings.ToUpper(x)} {
		var buf bytes.Buffer
		if err := run([]string{"-src", src, "-out", out, "-version", "0.1.0", "-commit", commit}, &buf); err == nil {
			t.Errorf("%s: built a binary stamped %s from a checkout at %s", name, commit, x)
		}
	}
	var buf bytes.Buffer
	if err := run([]string{"-src", src, "-out", out, "-version", "0.1.0", "-commit", x}, &buf); err != nil || !strings.Contains(buf.String(), filepath.Base(out)) {
		t.Fatalf("%v %q", err, buf.String())
	}
}
