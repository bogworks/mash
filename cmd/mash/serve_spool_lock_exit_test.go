package main

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
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

// holdSpoolLock takes the spool lock on its own file description, as another process's hook would, and returns the release.
func holdSpoolLock(t *testing.T, dir string) (release func()) {
	t.Helper()
	if err := (spool.Spool{Dir: dir}).Init(); err != nil {
		t.Fatal(err)
	}
	f, err := os.OpenFile(filepath.Join(dir, ".lock"), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX); err != nil {
		t.Fatal(err)
	}
	var once sync.Once
	rel := func() { once.Do(func() { f.Close() }) }
	t.Cleanup(rel)
	return rel
}

func waiterRecords(dir string) []string {
	des, _ := os.ReadDir(dir)
	var out []string
	for _, d := range des {
		if strings.HasPrefix(d.Name(), ".waiting") {
			out = append(out, d.Name())
		}
	}
	return out
}

// The lock held through serve's first attempt and its first lease-wait record leaves no waiter record for a lease retry
// away; serve keeps trying to write it, so a second launch's hook cannot match the waiting serve (#1651, ADR 0043).
func TestServeWaiterRecordIsRetriedUntilWritten(t *testing.T) {
	setTiming(t, time.Hour, 20*time.Second) // no lease retry writes the record
	oldStep := spoolLockStep
	spoolLockStep = 100 * time.Millisecond
	t.Cleanup(func() { spoolLockStep = oldStep })
	w := newWorld(t, "a")
	kv := leaseKV(t, w)
	holdLease(t, kv, tAddr("a").Flat())
	dir := spoolOf(w.env["a"]).Dir
	release := holdSpoolLock(t, dir)
	s := startServe(t, w.env["a"])
	time.Sleep(time.Second) // the first attempt and its noteHeld have both given up
	if n := waiterRecords(dir); len(n) != 0 {
		t.Fatalf("waiter record written while the lock was held: %v", n)
	}
	release()
	eventually(t, 5*time.Second, "the waiter record is written while serve still waits", func() bool {
		return len(waiterRecords(dir)) == 1
	})
	s.in.Close()
	s.exited(t, 5*time.Second)
	if n := waiterRecords(dir); len(n) != 0 {
		t.Fatalf("waiter record survived exit: %v", n)
	}
}

// A serve that waited and then takes the lease has no lease-wait state after it, even when its cleanup's lock wait gave
// up: the bind removes the state under the lock it takes (#1651, ADR 0092).
func TestServeAcquiringClearsTheWaitStateWhenTheCleanupTimedOut(t *testing.T) {
	setTiming(t, 50*time.Millisecond, 20*time.Second)
	oldStep := spoolLockStep
	spoolLockStep = 100 * time.Millisecond
	t.Cleanup(func() { spoolLockStep = oldStep })
	w := newWorld(t, "a")
	kv := leaseKV(t, w)
	holdLease(t, kv, tAddr("a").Flat())
	sp := spoolOf(w.env["a"])
	s := startServe(t, w.env["a"])
	eventually(t, 5*time.Second, "the wait state is recorded", func() bool { _, ok := sp.LiveWaitState(); return ok })
	release := holdSpoolLock(t, sp.Dir)
	freeLease(t, kv, tAddr("a").Flat())
	time.Sleep(time.Second) // serve takes the lease, and its cleanup gives up on the held lock
	s.initialize(t)
	s.tools(t)
	if _, ok := sp.LiveWaitState(); !ok {
		t.Fatal("the wait state was removed without the lock")
	}
	release()
	eventually(t, 5*time.Second, "the wait state is removed once serve binds the spool", func() bool {
		_, ok := sp.LiveWaitState()
		return !ok
	})
}
