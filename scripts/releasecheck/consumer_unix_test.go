//go:build unix

package main

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

func alive(pid int) bool { return syscall.Kill(pid, 0) == nil }

// requireReaper skips a test that needs orphans to be reaped. runConsumer counts a process group as alive while
// kill(-pgid, 0) succeeds, and that succeeds for a zombie. Where PID 1 never reaps (a container run without --init),
// a child orphaned by the exiting script stays a zombie in the group and runConsumer waits out its whole grace.
func requireReaper(t *testing.T) {
	t.Helper()
	if runtime.GOOS != "linux" {
		return
	}
	out, err := exec.Command("sh", "-c", "sleep 0.2 & echo $!").Output()
	if err != nil {
		t.Fatalf("starting an orphan: %v", err)
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(out)))
	if err != nil {
		t.Fatalf("orphan pid %q: %v", out, err)
	}
	stat := "/proc/" + strconv.Itoa(pid) + "/stat"
	state := ""
	for deadline := time.Now().Add(3 * time.Second); time.Now().Before(deadline); time.Sleep(50 * time.Millisecond) {
		b, err := os.ReadFile(stat)
		if err != nil {
			return // gone: orphans are reaped
		}
		s := string(b)
		i := strings.LastIndex(s, ")")
		if i < 0 {
			return
		}
		if f := strings.Fields(s[i+1:]); len(f) > 0 {
			state = f[0]
		}
	}
	if state == "Z" {
		t.Skip("skipped: PID 1 here does not reap orphaned processes, so a finished child stays a zombie in its process group and runConsumer waits out its whole grace; run the container with --init (issue #1887)")
	}
	t.Skipf("skipped: an orphaned sleep 0.2 was still present in state %q after 3s, so orphan reaping cannot be checked here (issue #1887)", state)
}

// A timeout sends TERM to the shell only (which forwards it); a child that ignores TERM survives that, so the grace
// ends with the whole group killed and nothing left behind.
func TestRunConsumer_theGroupIsKilledAfterTheGrace(t *testing.T) {
	requireReaper(t)
	old := consumerKillGrace
	consumerKillGrace = 500 * time.Millisecond
	defer func() { consumerKillGrace = old }()

	dir := t.TempDir()
	pidFile := filepath.Join(dir, "child.pid")
	termFile := filepath.Join(dir, "shell.term")
	// The shell records a TERM it receives, then exits; the child ignores TERM and outlives it.
	write(t, dir, consumerScript, "trap 'echo got > "+termFile+"; exit 0' TERM\n"+
		"(trap '' TERM; echo $$ > "+pidFile+"; while :; do sleep 1; done) &\n"+
		"while :; do sleep 0.1; done\n")
	ctx, cancel := context.WithTimeout(context.Background(), 700*time.Millisecond)
	defer cancel()
	var out bytes.Buffer
	start := time.Now()
	code, err := runConsumer(ctx, dir, consumerScript, nil, []string{"PATH=" + os.Getenv("PATH")}, &out)
	if err == nil || !strings.Contains(err.Error(), "did not finish") {
		t.Fatalf("code %d err %v", code, err)
	}
	if d := time.Since(start); d < 1100*time.Millisecond || d > 5*time.Second {
		t.Errorf("returned after %v, want the timeout plus the grace", d)
	}
	b, rerr := os.ReadFile(pidFile)
	if rerr != nil {
		t.Fatalf("the child never started: %v", rerr)
	}
	pid, _ := strconv.Atoi(strings.TrimSpace(string(b)))
	for i := 0; i < 50 && alive(pid); i++ { // a killed child is reaped by init, not instantly
		time.Sleep(50 * time.Millisecond)
	}
	if alive(pid) {
		syscall.Kill(pid, syscall.SIGKILL)
		t.Fatalf("child %d survived the group kill", pid)
	}
	if _, err := os.Stat(termFile); err != nil {
		t.Errorf("the shell was not sent TERM: %v", err)
	}
}

// With a runner that exits on TERM promptly, nothing waits out the grace.
func TestRunConsumer_aRunnerThatStopsOnTermIsNotWaitedOn(t *testing.T) {
	requireReaper(t)
	old := consumerKillGrace
	consumerKillGrace = time.Minute
	defer func() { consumerKillGrace = old }()
	dir := t.TempDir()
	write(t, dir, consumerScript, "sleep 30 &\ntrap 'kill $!; exit 0' TERM\nwait\n")
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	start := time.Now()
	_, err := runConsumer(ctx, dir, consumerScript, nil, []string{"PATH=" + os.Getenv("PATH")}, &bytes.Buffer{})
	if err == nil || time.Since(start) > 10*time.Second {
		t.Fatalf("err %v after %v", err, time.Since(start))
	}
}
