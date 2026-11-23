package scripts

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

// Issue #1093: a stale DIR/.lock is taken over under a second lock, DIR/.lock.recover, so two runs that read the same
// stale pid cannot both win. No server runs here: --stop on a made folder with no server.pid just takes the lock.

func lockTestDir(t *testing.T) (script, dir string) {
	t.Helper()
	if _, err := exec.LookPath("bash"); err != nil {
		t.Skip("bash not found")
	}
	root := t.TempDir()
	// under umask 002 TempDir is 0775, and the script refuses a --dir under a group-writable folder
	if err := os.Chmod(root, 0o700); err != nil {
		t.Fatal(err)
	}
	dir = filepath.Join(root, "home", "mash-nats")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	marker := fmt.Sprintf("{\n  \"schema\": 1,\n  \"owner\": \"acme\",\n  \"port\": 4290,\n  \"dir\": %q\n}\n", dir)
	if err := os.WriteFile(filepath.Join(dir, "mash-setup.json"), []byte(marker), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "server.conf"), []byte("listen: 127.0.0.1:4290\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	script, err := filepath.Abs("mash-local-nats-setup.sh")
	if err != nil {
		t.Fatal(err)
	}
	return script, dir
}

func runStop(script, dir string) (rc int, stdout, stderr string, err error) {
	var so, se bytes.Buffer
	c := exec.Command("bash", script, "--stop", "--dir", dir)
	c.Env = []string{"PATH=" + os.Getenv("PATH"), "HOME=" + filepath.Dir(dir)}
	c.Stdout, c.Stderr = &so, &se
	err = c.Run()
	if ee, ok := err.(*exec.ExitError); ok {
		rc, err = ee.ExitCode(), nil
	}
	return rc, so.String(), se.String(), err
}

func deadPid(t *testing.T) int {
	t.Helper()
	c := exec.Command("true")
	if err := c.Start(); err != nil {
		t.Fatal(err)
	}
	pid := c.Process.Pid
	_ = c.Wait()
	return pid
}

func writeLockFile(t *testing.T, p string, pid int) {
	t.Helper()
	if err := os.WriteFile(p, []byte(fmt.Sprintf("%d\n", pid)), 0o600); err != nil {
		t.Fatal(err)
	}
}

func noLockFiles(t *testing.T, dir string) {
	t.Helper()
	for _, n := range []string{".lock", ".lock.recover"} {
		if _, err := os.Lstat(filepath.Join(dir, n)); err == nil {
			t.Errorf("%s left behind", n)
		}
	}
}

func TestStaleLockIsTakenOver(t *testing.T) {
	script, dir := lockTestDir(t)
	writeLockFile(t, filepath.Join(dir, ".lock"), deadPid(t))
	rc, out, errOut, err := runStop(script, dir)
	if err != nil {
		t.Fatal(err)
	}
	if rc != 0 {
		t.Fatalf("rc=%d stdout=%q stderr=%q; want 0", rc, out, errOut)
	}
	noLockFiles(t, dir)
}

func TestLiveRecoverHolderIsBusy(t *testing.T) {
	script, dir := lockTestDir(t)
	live := exec.Command("sleep", "60")
	if err := live.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = live.Process.Kill(); _ = live.Wait() }) // only the process this test started
	writeLockFile(t, filepath.Join(dir, ".lock"), deadPid(t))
	writeLockFile(t, filepath.Join(dir, ".lock.recover"), live.Process.Pid)
	rc, _, errOut, err := runStop(script, dir)
	if err != nil {
		t.Fatal(err)
	}
	if rc != 1 || !strings.Contains(errOut, "another run is using") ||
		!strings.Contains(errOut, fmt.Sprintf("pid %d", live.Process.Pid)) {
		t.Fatalf("rc=%d stderr=%q; want rc 1 and \"another run is using\" with the holder's pid", rc, errOut)
	}
	if b, _ := os.ReadFile(filepath.Join(dir, ".lock.recover")); strings.TrimSpace(string(b)) != fmt.Sprint(live.Process.Pid) {
		t.Fatalf(".lock.recover changed: %q", b)
	}
}

func TestDeadRecoverHolderStopsWithHint(t *testing.T) {
	script, dir := lockTestDir(t)
	writeLockFile(t, filepath.Join(dir, ".lock"), deadPid(t))
	writeLockFile(t, filepath.Join(dir, ".lock.recover"), deadPid(t))
	rc, _, errOut, err := runStop(script, dir)
	if err != nil {
		t.Fatal(err)
	}
	rec := filepath.Join(dir, ".lock.recover")
	if rc != 1 || !strings.Contains(errOut, "error: "+rec+" is left over") ||
		!strings.Contains(errOut, "hint: remove that one file ("+rec+")") {
		t.Fatalf("rc=%d stderr=%q; want rc 1 naming %s with a hint", rc, errOut, rec)
	}
	if _, err := os.Stat(rec); err != nil {
		t.Fatalf("a run that did not hold .lock.recover removed it: %v", err)
	}
}

// Best effort: the script has no test hooks, so the overlap cannot be forced. Eight runs start at once against one
// stale lock; each must exit 0 or say busy, at least one must succeed, and none may leave a lock file behind.
func TestConcurrentTakeoverOfStaleLock(t *testing.T) {
	script, dir := lockTestDir(t)
	for round := 0; round < 3; round++ {
		writeLockFile(t, filepath.Join(dir, ".lock"), deadPid(t))
		const n = 8
		var wg sync.WaitGroup
		type res struct {
			rc  int
			err string
			e   error
		}
		results := make([]res, n)
		start := make(chan struct{})
		for i := 0; i < n; i++ {
			wg.Add(1)
			go func(i int) {
				defer wg.Done()
				<-start
				rc, _, se, err := runStop(script, dir)
				results[i] = res{rc, se, err}
			}(i)
		}
		close(start)
		wg.Wait()
		ok := 0
		for i, r := range results {
			if r.e != nil {
				t.Fatal(r.e)
			}
			if r.rc == 0 {
				ok++
			} else if r.rc != 1 || !strings.Contains(r.err, "another run is using") {
				t.Errorf("round %d run %d: rc=%d stderr=%q; want 0 or busy", round, i, r.rc, r.err)
			}
		}
		if ok == 0 {
			t.Errorf("round %d: no run took the lock", round)
		}
		noLockFiles(t, dir)
	}
}
