package scripts

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// These tests write a stub nats-server into the real /usr/sbin, so they run only as root inside a throwaway
// container with MASH_TEST_SBIN=1 (see the run command in the pull request). Anywhere else they skip.
func sbinSetup(t *testing.T, mode os.FileMode, uid int) (out string, argvLog string) {
	t.Helper()
	if os.Geteuid() != 0 || os.Getenv("MASH_TEST_SBIN") != "1" {
		t.Skip("needs root and MASH_TEST_SBIN=1, in a throwaway container")
	}
	stub := "/usr/sbin/nats-server"
	if _, err := os.Lstat(stub); err == nil {
		t.Fatal(stub + " already exists: run in a clean container")
	}
	// not t.TempDir: its parent is 0700, and the script runs as uid 1000
	tmp, err := os.MkdirTemp("", "sbin")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(tmp) })
	argvLog = filepath.Join(tmp, "argv.log")
	body := "#!/bin/sh\necho \"$0 $*\" >> " + argvLog + "\necho 'nats-server: v2.10.9'\n"
	if err := os.WriteFile(stub, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Remove(stub) })
	if err := os.Chown(stub, uid, uid); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(stub, mode); err != nil {
		t.Fatal(err)
	}
	// a `nats` that fails, so create mode stops right after the version check
	bin := filepath.Join(tmp, "bin")
	if err := os.Mkdir(bin, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(bin, "nats"), []byte("#!/bin/sh\nexit 1\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	// the image has no ss: one that lists nothing, so no port is taken
	if err := os.WriteFile(filepath.Join(bin, "ss"), []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	root, err := filepath.Abs("..")
	if err != nil {
		t.Fatal(err)
	}
	// the script refuses to run as root, so run it as uid 1000; the stub in /usr/sbin keeps its owner and mode
	if err := os.Chown(tmp, 1000, 1000); err != nil {
		t.Fatal(err)
	}
	if err := os.Chown(bin, 1000, 1000); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("setpriv", "--reuid=1000", "--regid=1000", "--clear-groups", "bash", filepath.Join(root, "scripts/mash-local-nats-setup.sh"), "--owner", "alice", "--dir", filepath.Join(tmp, "n"))
	cmd.Env = []string{"PATH=" + bin + ":/usr/bin:/bin", "HOME=" + tmp}
	b, _ := cmd.CombinedOutput()
	return string(b), argvLog
}

const sbinLine = "nats-server: using /usr/sbin/nats-server (that folder is not on your PATH)"

func TestSbinNatsServerUsed(t *testing.T) {
	out, argv := sbinSetup(t, 0o755, 0)
	if strings.Count(out, sbinLine) != 1 {
		t.Fatalf("want the line once, got:\n%s", out)
	}
	if strings.Contains(out, "missing") {
		t.Fatalf("nats-server reported missing:\n%s", out)
	}
	b, err := os.ReadFile(argv)
	if err != nil || !strings.Contains(string(b), "/usr/sbin/nats-server --version") {
		t.Fatalf("stub not called by full path with --version: %q %v\n%s", b, err, out)
	}
}

func TestSbinNatsServerGroupWritable(t *testing.T) {
	out, _ := sbinSetup(t, 0o775, 0)
	if !strings.Contains(out, "missing: nats-server") || strings.Contains(out, "using /usr/sbin") {
		t.Fatalf("want missing: nats-server, got:\n%s", out)
	}
}

func TestSbinNatsServerNotRootOwned(t *testing.T) {
	out, _ := sbinSetup(t, 0o755, 1000)
	if !strings.Contains(out, "missing: nats-server") || strings.Contains(out, "using /usr/sbin") {
		t.Fatalf("want missing: nats-server, got:\n%s", out)
	}
}
