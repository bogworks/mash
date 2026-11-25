package main

import (
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"github.com/bogworks/mash/internal/endpoint"
	"github.com/bogworks/mash/internal/spool"
)

// spoolLockExitBound is how long a running serve may take to exit after stdin closes while a stalled hook holds the
// spool's lock: the cleanup waits are bounded (spoolLockStep, 1 s each), so it is well under this.
const spoolLockExitBound = 10 * time.Second

// A hook stalled while holding the spool lock cannot keep a running serve from exiting (#1651): serve's own lock waits
// (the hook-state record and its clearing, the waiter cleanup, the held-mail loop) give up with its context or a short bound.
func TestServeExitsWhileTheSpoolLockIsHeld(t *testing.T) {
	setTiming(t, time.Hour, 20*time.Second)
	w := newWorld(t, "a")
	s := startServe(t, w.env["a"])
	s.initialize(t)
	s.tools(t)
	dir, err := endpoint.SpoolDir(w.cfgOf(t, "a"))
	if err != nil {
		t.Fatal(err)
	}
	lockPath := filepath.Join(dir, ".lock")
	eventually(t, 5*time.Second, "serve has bound its spool", func() bool {
		_, err := os.Stat(lockPath)
		return err == nil
	})
	// A second file description, as another process's hook would hold it.
	f, err := os.OpenFile(lockPath, os.O_RDWR, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX); err != nil {
		t.Fatal(err)
	}
	// Let serve's recorder tick at least once against the held lock.
	time.Sleep(2500 * time.Millisecond)
	start := time.Now()
	s.in.Close()
	s.exited(t, spoolLockExitBound)
	t.Logf("serve exited %s after stdin closed", time.Since(start))
}

// serve's startup waits for the spool lock (the waiter record, the lease-wait state) run before the MCP server reads
// stdin, so they are bounded by themselves: a hook holding the lock does not hang a new serve whose stdin closes (#1651).
func TestServeStartupExitsWhileTheSpoolLockIsHeld(t *testing.T) {
	setTiming(t, time.Hour, 20*time.Second)
	w := newWorld(t, "a")
	dir, err := endpoint.SpoolDir(w.cfgOf(t, "a"))
	if err != nil {
		t.Fatal(err)
	}
	if err := (spool.Spool{Dir: dir}).Init(); err != nil {
		t.Fatal(err)
	}
	f, err := os.OpenFile(filepath.Join(dir, ".lock"), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX); err != nil {
		t.Fatal(err)
	}
	s := startServe(t, w.env["a"])
	start := time.Now()
	s.in.Close()
	s.exited(t, spoolLockExitBound)
	t.Logf("serve exited %s after stdin closed", time.Since(start))
}
