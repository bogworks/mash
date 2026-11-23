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
	return runStopEnv(script, dir)
}

func runStopEnv(script, dir string, extra ...string) (rc int, stdout, stderr string, err error) {
	var so, se bytes.Buffer
	c := exec.Command("bash", script, "--stop", "--dir", dir)
	c.Env = append([]string{"PATH=" + os.Getenv("PATH"), "HOME=" + filepath.Dir(dir)}, extra...)
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

// An empty .lock.recover is left by a run that died between creating it and writing its pid: same error and hint as a
// dead pid, not "another run is using ... (pid ...)" with no way out.
func TestEmptyLockRecoverFileIsLeftOver(t *testing.T) {
	script, dir := lockTestDir(t)
	writeLockFile(t, filepath.Join(dir, ".lock"), deadPid(t))
	rec := filepath.Join(dir, ".lock.recover")
	if err := os.WriteFile(rec, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	rc, _, errOut, err := runStop(script, dir)
	if err != nil {
		t.Fatal(err)
	}
	if rc != 1 || !strings.Contains(errOut, "error: "+rec+" is left over") ||
		!strings.Contains(errOut, "hint: remove that one file ("+rec+")") ||
		strings.Contains(errOut, "another run is using") {
		t.Fatalf("rc=%d stderr=%q; want rc 1, the left-over error and hint", rc, errOut)
	}
	if _, err := os.Stat(rec); err != nil {
		t.Fatalf("a run that did not hold .lock.recover removed it: %v", err)
	}
}

// The takeover re-reads .lock under .lock.recover. If that read gives nothing (missing or empty) a live run C may have
// taken .lock in between through the fast path, so the takeover must not remove it. The script has no test hook; a
// wrapper cat on PATH stages the timing: the first read of .lock is real (the stale pid), on the second (the re-read)
// it shows an empty or missing .lock and then C's live pid appears in .lock.
func TestTakeoverDoesNotRemoveLiveLockAtReread(t *testing.T) {
	for _, mode := range []string{"empty", "missing"} {
		t.Run(mode, func(t *testing.T) {
			realCat, err := exec.LookPath("cat")
			if err != nil {
				t.Skip("cat not found")
			}
			script, dir := lockTestDir(t)
			live := exec.Command("sleep", "60")
			if err := live.Start(); err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = live.Process.Kill(); _ = live.Wait() }) // only the process this test started
			lock := filepath.Join(dir, ".lock")
			writeLockFile(t, lock, deadPid(t))
			wrap := t.TempDir()
			state := filepath.Join(wrap, "n")
			sh := `#!/bin/sh
for a; do case $a in */.lock) f=$a ;; esac; done
if [ -n "${f:-}" ]; then
  c=0; [ ! -f "$STATE" ] || c=$(` + realCat + ` "$STATE"); c=$((c + 1)); echo "$c" >"$STATE"
  if [ "$c" = 2 ]; then
    if [ "$MODE" = missing ]; then rm -f "$f"; else : >"$f"; fi
    printf '%s\n' "$LIVE" >"$f.c"; mv -f "$f.c" "$f"
    [ "$MODE" = missing ] && exit 1
    exit 0
  fi
fi
exec ` + realCat + ` "$@"
`
			if err := os.WriteFile(filepath.Join(wrap, "cat"), []byte(sh), 0o755); err != nil {
				t.Fatal(err)
			}
			rc, _, errOut, err := runStopEnv(script, dir,
				"PATH="+wrap+":"+os.Getenv("PATH"), "STATE="+state, "MODE="+mode, fmt.Sprintf("LIVE=%d", live.Process.Pid))
			if err != nil {
				t.Fatal(err)
			}
			if rc != 1 || !strings.Contains(errOut, "another run is using") ||
				!strings.Contains(errOut, fmt.Sprintf("pid %d", live.Process.Pid)) {
				t.Fatalf("rc=%d stderr=%q; want rc 1 and busy with the live run's pid", rc, errOut)
			}
			if b, _ := os.ReadFile(lock); strings.TrimSpace(string(b)) != fmt.Sprint(live.Process.Pid) {
				t.Fatalf("the live run's .lock was removed or replaced: %q", b)
			}
			if _, err := os.Lstat(filepath.Join(dir, ".lock.recover")); err == nil {
				t.Error(".lock.recover left behind")
			}
		})
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

// An old-format lock is a folder; it is stale (nothing holds it) and is taken over.
func TestStaleLockFolderIsTakenOver(t *testing.T) {
	script, dir := lockTestDir(t)
	if err := os.Mkdir(filepath.Join(dir, ".lock"), 0o700); err != nil {
		t.Fatal(err)
	}
	rc, out, errOut, err := runStop(script, dir)
	if err != nil {
		t.Fatal(err)
	}
	if rc != 0 {
		t.Fatalf("rc=%d stdout=%q stderr=%q; want 0", rc, out, errOut)
	}
	noLockFiles(t, dir)
}

// A .lock that stays empty (or holds no pid) after the 1 s wait was left by a run stopped between creating it and
// writing its pid. It is reported with the file to remove and is never removed by the script.
func TestEmptyLockIsLeftOver(t *testing.T) {
	for name, content := range map[string]string{"empty": "", "non-numeric": "abc\n"} {
		t.Run(name, func(t *testing.T) {
			script, dir := lockTestDir(t)
			lock := filepath.Join(dir, ".lock")
			if err := os.WriteFile(lock, []byte(content), 0o600); err != nil {
				t.Fatal(err)
			}
			rc, _, errOut, err := runStop(script, dir)
			if err != nil {
				t.Fatal(err)
			}
			if rc != 1 || !strings.Contains(errOut, "error: "+lock+" is left over from a run that was stopped while it took the lock") ||
				!strings.Contains(errOut, "hint: remove that one file ("+lock+"), then run mash-local-nats-setup.sh again") ||
				strings.Contains(errOut, "another run is using") {
				t.Fatalf("rc=%d stderr=%q; want rc 1, the left-over error and hint", rc, errOut)
			}
			if b, err := os.ReadFile(lock); err != nil || string(b) != content {
				t.Fatalf(".lock was removed or changed: %q, %v", b, err)
			}
			if _, err := os.Lstat(filepath.Join(dir, ".lock.recover")); err == nil {
				t.Error(".lock.recover left behind")
			}
		})
	}
}

// catStage runs --stop with a cat wrapper on PATH. The wrapper counts the script's reads of a path ending in /.lock;
// body is shell run on the read numbered $c (1 = first). If body does not exit, the real cat then runs.
func catStage(t *testing.T, script, dir, body string, extra ...string) (rc int, errOut string) {
	t.Helper()
	realCat, err := exec.LookPath("cat")
	if err != nil {
		t.Skip("cat not found")
	}
	wrap := t.TempDir()
	sh := `#!/bin/sh
for a; do case $a in */.lock) f=$a ;; esac; done
if [ -n "${f:-}" ]; then
  c=0; [ ! -f "$STATE" ] || c=$(` + realCat + ` "$STATE"); c=$((c + 1)); echo "$c" >"$STATE"
` + body + `
fi
exec ` + realCat + ` "$@"
`
	if err := os.WriteFile(filepath.Join(wrap, "cat"), []byte(sh), 0o755); err != nil {
		t.Fatal(err)
	}
	env := append([]string{"PATH=" + wrap + ":" + os.Getenv("PATH"), "STATE=" + filepath.Join(wrap, "n")}, extra...)
	rc, _, errOut, err = runStopEnv(script, dir, env...)
	if err != nil {
		t.Fatal(err)
	}
	return rc, errOut
}

func startLive(t *testing.T) int {
	t.Helper()
	live := exec.Command("sleep", "60")
	if err := live.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = live.Process.Kill(); _ = live.Wait() }) // only the process this test started
	return live.Process.Pid
}

// The #1093 interleaving: runs A and B both read the same stale pid. A takes over and starts; B then reads .lock again
// under .lock.recover and must see A's new pid, end busy with it, and leave A's .lock alone.
func TestSecondRunSeesFirstRunsNewLockAtReread(t *testing.T) {
	script, dir := lockTestDir(t)
	live := startLive(t)
	lock := filepath.Join(dir, ".lock")
	stale := deadPid(t)
	writeLockFile(t, lock, stale)
	// read 1 is B's outside read (the stale pid); before read 2 (under .lock.recover) A replaces .lock with its pid
	rc, errOut := catStage(t, script, dir, `  if [ "$c" = 2 ]; then printf '%s\n' "$LIVE" >"$f.c"; mv -f "$f.c" "$f"; fi`,
		fmt.Sprintf("LIVE=%d", live))
	if rc != 1 || !strings.Contains(errOut, fmt.Sprintf("another run is using %s (pid %d)", dir, live)) ||
		strings.Contains(errOut, fmt.Sprint(stale)) {
		t.Fatalf("rc=%d stderr=%q; want rc 1 and busy with A's pid %d", rc, errOut, live)
	}
	if b, _ := os.ReadFile(lock); strings.TrimSpace(string(b)) != fmt.Sprint(live) {
		t.Fatalf("A's .lock was removed or replaced: %q", b)
	}
	if _, err := os.Lstat(filepath.Join(dir, ".lock.recover")); err == nil {
		t.Error(".lock.recover left behind")
	}
}

// .lock is empty on the first outside read and missing on the second: nothing is left over, the lock is just taken.
func TestEmptyThenMissingLockIsNotLeftOver(t *testing.T) {
	script, dir := lockTestDir(t)
	lock := filepath.Join(dir, ".lock")
	if err := os.WriteFile(lock, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	rc, errOut := catStage(t, script, dir, `  if [ "$c" = 2 ]; then rm -f "$f"; exit 1; fi`)
	if rc != 0 || strings.Contains(errOut, "left over") {
		t.Fatalf("rc=%d stderr=%q; want rc 0 and no left-over error", rc, errOut)
	}
	noLockFiles(t, dir)
}

// .lock is empty on both outside reads but gets a live pid during the wait under .lock.recover: not left over, busy.
func TestEmptyLockGetsPidDuringRecoverWait(t *testing.T) {
	script, dir := lockTestDir(t)
	live := startLive(t)
	lock := filepath.Join(dir, ".lock")
	if err := os.WriteFile(lock, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	// after read 2 returns, the live run writes its pid 0.4 s later, inside the 1 s wait under .lock.recover
	rc, errOut := catStage(t, script, dir, `  if [ "$c" = 2 ]; then
    ( sleep 0.4; printf '%s\n' "$LIVE" >"$f.c"; mv -f "$f.c" "$f" ) </dev/null >/dev/null 2>&1 &
  fi`, fmt.Sprintf("LIVE=%d", live))
	if rc != 1 || strings.Contains(errOut, "left over") ||
		!strings.Contains(errOut, fmt.Sprintf("another run is using %s (pid %d)", dir, live)) {
		t.Fatalf("rc=%d stderr=%q; want rc 1 and busy with pid %d, no left-over error", rc, errOut, live)
	}
	if b, _ := os.ReadFile(lock); strings.TrimSpace(string(b)) != fmt.Sprint(live) {
		t.Fatalf("the live run's .lock was removed or replaced: %q", b)
	}
	if _, err := os.Lstat(filepath.Join(dir, ".lock.recover")); err == nil {
		t.Error(".lock.recover left behind")
	}
}
