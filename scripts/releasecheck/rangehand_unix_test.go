//go:build unix

package main

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

func alive(pid int) bool { return syscall.Kill(pid, 0) == nil }

// A timeout sends TERM to the shell only (which forwards it); a child that ignores TERM survives that, so the grace
// ends with the whole group killed and nothing left behind.
func TestRunRangehand_theGroupIsKilledAfterTheGrace(t *testing.T) {
	old := rangehandKillGrace
	rangehandKillGrace = 500 * time.Millisecond
	defer func() { rangehandKillGrace = old }()

	dir := t.TempDir()
	pidFile := filepath.Join(dir, "child.pid")
	termFile := filepath.Join(dir, "shell.term")
	// The shell records a TERM it receives, then exits; the child ignores TERM and outlives it.
	write(t, dir, rangehandScript, "trap 'echo got > "+termFile+"; exit 0' TERM\n"+
		"(trap '' TERM; echo $$ > "+pidFile+"; while :; do sleep 1; done) &\n"+
		"while :; do sleep 0.1; done\n")
	ctx, cancel := context.WithTimeout(context.Background(), 700*time.Millisecond)
	defer cancel()
	var out bytes.Buffer
	start := time.Now()
	code, err := runRangehand(ctx, dir, nil, []string{"PATH=" + os.Getenv("PATH")}, &out)
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
func TestRunRangehand_aRunnerThatStopsOnTermIsNotWaitedOn(t *testing.T) {
	old := rangehandKillGrace
	rangehandKillGrace = time.Minute
	defer func() { rangehandKillGrace = old }()
	dir := t.TempDir()
	write(t, dir, rangehandScript, "sleep 30 &\ntrap 'kill $!; exit 0' TERM\nwait\n")
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	start := time.Now()
	_, err := runRangehand(ctx, dir, nil, []string{"PATH=" + os.Getenv("PATH")}, &bytes.Buffer{})
	if err == nil || time.Since(start) > 10*time.Second {
		t.Fatalf("err %v after %v", err, time.Since(start))
	}
}
