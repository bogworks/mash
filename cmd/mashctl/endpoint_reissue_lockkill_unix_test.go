//go:build unix

package main

import (
	"os"
	"os/exec"
	"path/filepath"
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
	deadline := time.Now().Add(15 * time.Second)
	for {
		if o, _, ok := epReadOwner(lock); ok && o.pid == cmd.Process.Pid {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the child did not take the lock")
		}
		time.Sleep(20 * time.Millisecond)
	}
	if err := cmd.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	cmd.Wait()
	done = true
	if _, err := os.Lstat(filepath.Join(lock, "owner")); err != nil {
		t.Fatalf("the killed run's lock is gone: %v", err)
	}

	if _, se, code := h.run("endpoint", "reissue", a.String(), "--make-key"); code != 0 {
		t.Fatalf("reissue after a kill: %d %q", code, se)
	}
	if credsPub(t, h, a) == old {
		t.Error("the key did not change")
	}
	if _, err := os.Lstat(lock); err == nil {
		t.Error("lock left after the reissue")
	}
}
