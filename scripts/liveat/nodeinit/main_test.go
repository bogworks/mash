package main

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

func look(t *testing.T, n string) string {
	t.Helper()
	p, err := exec.LookPath(n)
	if err != nil {
		t.Skip(n, "not found")
	}
	return p
}

func waitPid(t *testing.T, pf string) int {
	t.Helper()
	for i := 0; i < 300; i++ {
		if b, err := os.ReadFile(pf); err == nil {
			if n, err := strconv.Atoi(strings.TrimSpace(string(b))); err == nil {
				return n
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("no pidfile")
	return 0
}

func TestRunPassesStatusAndOutputAndRemovesPidfile(t *testing.T) {
	sh := look(t, "sh")
	pf := filepath.Join(t.TempDir(), "p.pid")
	var out bytes.Buffer
	code := superviseCmd(pf, []string{sh, "-c", "echo hi; exit 7"}, make(chan os.Signal, 1), strings.NewReader(""), &out, &out)
	if code != 7 || strings.TrimSpace(out.String()) != "hi" {
		t.Fatalf("code %d out %q", code, out.String())
	}
	if _, err := os.Stat(pf); !os.IsNotExist(err) {
		t.Fatal("pidfile left")
	}
}

func TestStopKillsOnlyTheRecordedChildAndRunForwardsSignals(t *testing.T) {
	sleep := look(t, "sleep")
	d := t.TempDir()
	pf := filepath.Join(d, "a.pid")
	other := exec.Command(sleep, "30")
	if err := other.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = other.Process.Kill(); _ = other.Wait() }()
	sigs := make(chan os.Signal, 1)
	res := make(chan int, 1)
	go func() { res <- superviseCmd(pf, []string{sleep, "30"}, sigs, nil, nil, nil) }()
	pid := waitPid(t, pf)
	if st, _ := os.Stat(pf); st.Mode().Perm() != 0o600 {
		t.Fatalf("mode %v", st.Mode())
	}
	if err := stopPidfile(pf, 5*time.Second); err != nil {
		t.Fatal(err)
	}
	select {
	case c := <-res:
		if c != 128+int(syscall.SIGTERM) {
			t.Fatalf("status %d", c)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("run did not end")
	}
	if syscall.Kill(pid, 0) == nil {
		t.Fatal("child still there")
	}
	if syscall.Kill(other.Process.Pid, 0) != nil {
		t.Fatal("stop killed another process")
	}
	go func() { res <- superviseCmd(pf, []string{sleep, "30"}, sigs, nil, nil, nil) }()
	waitPid(t, pf)
	sigs <- syscall.SIGTERM
	select {
	case c := <-res:
		if c != 128+int(syscall.SIGTERM) {
			t.Fatalf("status %d", c)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("signal not forwarded")
	}
}

func TestStopEscalatesToKillAndMissingPidfileIsStopped(t *testing.T) {
	sh := look(t, "sh")
	d := t.TempDir()
	if err := stopPidfile(filepath.Join(d, "none.pid"), time.Second); err != nil {
		t.Fatal(err)
	}
	pf := filepath.Join(d, "t.pid")
	res := make(chan int, 1)
	go func() {
		res <- superviseCmd(pf, []string{sh, "-c", "trap '' TERM; while :; do :; done"}, make(chan os.Signal, 1), nil, nil, nil)
	}()
	waitPid(t, pf)
	time.Sleep(200 * time.Millisecond)
	if err := stopPidfile(pf, 300*time.Millisecond); err != nil {
		t.Fatal(err)
	}
	select {
	case c := <-res:
		if c != 128+int(syscall.SIGKILL) {
			t.Fatalf("status %d", c)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("not killed")
	}
	if cmdStop([]string{"--pidfile", filepath.Join(d, "none.pid")}) != 0 || cmdStop([]string{"--bad"}) != 2 {
		t.Fatal("cmdStop")
	}
	if cmdRun([]string{"x"}) != 2 {
		t.Fatal("cmdRun usage")
	}
}
