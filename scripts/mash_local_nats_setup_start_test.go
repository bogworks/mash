package scripts

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Issue #1094: `mash-local-nats-setup.sh --start` must check that a live recorded server answers before it says
// "already running". These tests use stubs for nats and ss on PATH (no real server, no network, no containers) and a
// stand-in process, a copy of this test binary named nats-server, whose arguments name the folder's server.conf, so
// the script's own "is this our nats-server" check passes.

const fakeServerEnv = "MLNT_TEST_FAKE_SERVER"

func TestMain(m *testing.M) {
	if os.Getenv(fakeServerEnv) == "1" {
		time.Sleep(10 * time.Minute) // killed by the test, by its recorded pid
		os.Exit(0)
	}
	os.Exit(m.Run())
}

func runStartAgainstLiveServer(t *testing.T, listens, answers bool) (rc int, stdout, stderr string, pid int, dir string) {
	t.Helper()
	if _, err := exec.LookPath("bash"); err != nil {
		t.Skip("bash not found")
	}
	if _, err := os.Stat("/proc/self/exe"); err != nil {
		t.Skip("needs /proc (Linux)")
	}
	root := t.TempDir()
	bin := filepath.Join(root, "bin")
	home := filepath.Join(root, "home")
	dir = filepath.Join(home, "mash-nats")
	for _, d := range []string{bin, home, dir} {
		if err := os.MkdirAll(d, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	const port = 4290
	write := func(p, s string, mode os.FileMode) {
		if err := os.WriteFile(p, []byte(s), mode); err != nil {
			t.Fatal(err)
		}
	}
	write(filepath.Join(dir, "mash-setup.json"), fmt.Sprintf(
		"{\n  \"schema\": 1,\n  \"owner\": \"acme\",\n  \"port\": %d,\n  \"dir\": %q\n}\n", port, dir), 0o600)
	write(filepath.Join(dir, "server.conf"), "listen: 127.0.0.1:4290\n", 0o600)
	// stub nats: succeeds when answers, else fails every call; stub ss: shows the port listening only when listens
	natsRC, ssLine := 0, fmt.Sprintf("LISTEN 0 4096 127.0.0.1:%d 0.0.0.0:*", port)
	if !answers {
		natsRC = 1
	}
	if !listens {
		ssLine = ""
	}
	write(filepath.Join(bin, "nats"), fmt.Sprintf("#!/bin/sh\nexit %d\n", natsRC), 0o755)
	write(filepath.Join(bin, "ss"), "#!/bin/sh\necho 'State Recv-Q Send-Q Local-Address:Port Peer-Address:Port'\n"+
		"[ -z '"+ssLine+"' ] || echo '"+ssLine+"'\n", 0o755)
	// the stand-in server: this test binary, named nats-server, with -c DIR/server.conf
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(exe)
	if err != nil {
		t.Fatal(err)
	}
	fake := filepath.Join(bin, "nats-server")
	write(fake, string(data), 0o755)
	cmd := exec.Command(fake, "-c", filepath.Join(dir, "server.conf"))
	cmd.Env = append(os.Environ(), fakeServerEnv+"=1")
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	pid = cmd.Process.Pid
	t.Cleanup(func() { // only the process this test started, by its recorded pid
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
	})
	write(filepath.Join(dir, "server.pid"), fmt.Sprintf("%d\n", pid), 0o600)

	script, err := filepath.Abs("mash-local-nats-setup.sh")
	if err != nil {
		t.Fatal(err)
	}
	var so, se bytes.Buffer
	c := exec.Command("bash", script, "--start", "--dir", dir)
	c.Env = []string{"PATH=" + bin + ":" + os.Getenv("PATH"), "HOME=" + home}
	c.Stdout, c.Stderr = &so, &se
	err = c.Run()
	if ee, ok := err.(*exec.ExitError); ok {
		rc = ee.ExitCode()
	} else if err != nil {
		t.Fatal(err)
	}
	return rc, so.String(), se.String(), pid, dir
}

func TestStartLiveServerThatAnswersIsAlreadyRunning(t *testing.T) {
	rc, out, errOut, pid, dir := runStartAgainstLiveServer(t, true, true)
	want := fmt.Sprintf("already running: pid %d on 127.0.0.1:4290\n", pid)
	if rc != 0 || out != want || errOut != "" {
		t.Fatalf("rc=%d stdout=%q stderr=%q; want rc 0, stdout %q, no stderr", rc, out, errOut, want)
	}
	if b, _ := os.ReadFile(filepath.Join(dir, "server.pid")); strings.TrimSpace(string(b)) != fmt.Sprint(pid) {
		t.Fatalf("server.pid changed: %q", b)
	}
}

func TestStartLiveServerThatDoesNotAnswerFails(t *testing.T) {
	// not listening at all, and listening but failing the sign-in and ping (verify retries for about 6 s)
	for _, c := range []struct {
		name    string
		listens bool
	}{{"not-listening", false}, {"listening-no-signin", true}} {
		t.Run(c.name, func(t *testing.T) {
			rc, out, errOut, pid, dir := runStartAgainstLiveServer(t, c.listens, false)
			want := fmt.Sprintf("error: pid %d is running but the server does not answer on 127.0.0.1:4290\n"+
				"hint: run mash-local-nats-setup.sh --stop, then mash-local-nats-setup.sh --start\n", pid)
			if rc != 1 || errOut != want || strings.Contains(out, "already running") {
				t.Fatalf("rc=%d stdout=%q stderr=%q; want rc 1, stderr %q, no \"already running\"", rc, out, errOut, want)
			}
			// nothing killed or restarted: the process is still alive, and the pid file is unchanged
			if _, err := os.Stat(fmt.Sprintf("/proc/%d", pid)); err != nil {
				t.Fatalf("the server process was killed: %v", err)
			}
			if b, _ := os.ReadFile(filepath.Join(dir, "server.pid")); strings.TrimSpace(string(b)) != fmt.Sprint(pid) {
				t.Fatalf("server.pid changed: %q", b)
			}
		})
	}
}
