//go:build unix

package main

import (
	"bufio"
	"errors"
	"os"
	"os/exec"
	"testing"
	"time"
)

// TestHelperHoldLock is the child of TestEndpointReissueLockAfterRealKill: it takes the lock named by
// MASH_TEST_HOLD_LOCK and blocks on stdin until it is killed. In a normal run it does nothing.
func TestHelperHoldLock(t *testing.T) {
	path := os.Getenv("MASH_TEST_HOLD_LOCK")
	if path == "" {
		t.Skip("helper process only")
	}
	if _, err := epLock(path); err != nil {
		os.Exit(3)
	}
	os.Stdout.WriteString("locked\n")
	buf := make([]byte, 1)
	os.Stdin.Read(buf)
	os.Exit(0)
}

func TestEndpointReissueLockAfterRealKill(t *testing.T) {
	h := newFleetEnv(t)
	fakeRevoke(t, nil)
	a := ad("rk")
	if _, se, code := h.run("endpoint", "create", a.String(), "--make-key"); code != 0 {
		t.Fatalf("create %q", se)
	}
	old := credsPub(t, h, a)
	lock := h.creds(a) + ".lock"

	cmd := exec.Command(os.Args[0], "-test.run=^TestHelperHoldLock$")
	cmd.Env = append(os.Environ(), "MASH_TEST_HOLD_LOCK="+lock)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	defer stdin.Close()
	out, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	done := false
	t.Cleanup(func() {
		if !done {
			cmd.Process.Kill()
			cmd.Wait()
		}
	})
	line := make(chan string, 1)
	go func() {
		l, _ := bufio.NewReader(out).ReadString('\n')
		line <- l
	}()
	select {
	case l := <-line:
		if l != "locked\n" {
			t.Fatalf("the child did not take the lock: %q", l)
		}
	case <-time.After(15 * time.Second):
		t.Fatal("the child did not take the lock")
	}
	shortLockWait(t, 100*time.Millisecond)
	if _, err := epLock(lock); !errors.Is(err, errEpBusy) {
		t.Fatalf("lock while the child holds it: %v", err)
	}
	epLockWait = 10 * time.Second
	if err := cmd.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	cmd.Wait()
	done = true

	if _, se, code := h.run("endpoint", "reissue", a.String(), "--make-key"); code != 0 {
		t.Fatalf("reissue after a kill: %d %q", code, se)
	}
	if credsPub(t, h, a) == old {
		t.Error("the key did not change")
	}
	u, err := epLock(lock)
	if err != nil {
		t.Errorf("lock not free after the reissue: %v", err)
	} else {
		u()
	}
}
