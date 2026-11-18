package main

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func hostName(t *testing.T) string {
	t.Helper()
	h, err := os.Hostname()
	if err != nil {
		t.Fatal(err)
	}
	return h
}

// writeLockOwner makes the lock directory at path with an owner file holding o.
func writeLockOwner(t *testing.T, path string, o epOwner) {
	t.Helper()
	if err := os.MkdirAll(path, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(path, "owner"), []byte(o.String()), 0o600); err != nil {
		t.Fatal(err)
	}
}

// holdLock makes a lock at path held by this process, as epLock would.
func holdLock(t *testing.T, path string) {
	t.Helper()
	writeLockOwner(t, path, epOwner{os.Getpid(), hostName(t), epHostID(), time.Now().Unix()})
}

// deadPid is the pid of a process that has run and been waited for.
func deadPid(t *testing.T) int {
	t.Helper()
	cmd := exec.Command("true")
	if err := cmd.Run(); err != nil {
		t.Fatal(err)
	}
	return cmd.Process.Pid
}

func TestEpLockReclaimRules(t *testing.T) {
	if epPidState(os.Getpid()) < 0 {
		t.Skip("no process check on this platform")
	}
	prev := epLockWait
	epLockWait = 200 * time.Millisecond
	t.Cleanup(func() { epLockWait = prev })
	host := hostName(t)
	now := time.Now()
	old := now.Add(-time.Hour).Unix()
	dead := deadPid(t)
	id := epHostID()
	if id == "" {
		t.Skip("no machine and namespace id on this platform")
	}
	cases := []struct {
		name      string
		setup     func(t *testing.T, lock string)
		reclaimed bool
	}{
		{"dead pid on this host", func(t *testing.T, l string) { writeLockOwner(t, l, epOwner{dead, host, id, now.Unix()}) }, true},
		{"live pid on this host", func(t *testing.T, l string) { writeLockOwner(t, l, epOwner{os.Getpid(), host, id, now.Unix()}) }, false},
		{"other host, recent", func(t *testing.T, l string) { writeLockOwner(t, l, epOwner{dead, host + "-other", id, now.Unix()}) }, false},
		{"other host, old start", func(t *testing.T, l string) { writeLockOwner(t, l, epOwner{dead, host + "-other", id, old}) }, false},
		{"live pid with an old start time on this host", func(t *testing.T, l string) { writeLockOwner(t, l, epOwner{os.Getpid(), host, id, old}) }, false},
		{"dead pid with an old start time on this host", func(t *testing.T, l string) { writeLockOwner(t, l, epOwner{dead, host, id, old}) }, true},
		{"same host, dead pid, same id", func(t *testing.T, l string) { writeLockOwner(t, l, epOwner{dead, host, id, now.Unix()}) }, true},
		{"same host, dead pid, different id", func(t *testing.T, l string) { writeLockOwner(t, l, epOwner{dead, host, id + "x", now.Unix()}) }, false},
		{"same host, dead pid, empty id", func(t *testing.T, l string) { writeLockOwner(t, l, epOwner{dead, host, "", now.Unix()}) }, false},
		{"same host, dead pid, no id field", func(t *testing.T, l string) {
			if err := os.Mkdir(l, 0o700); err != nil {
				t.Fatal(err)
			}
			txt := fmt.Sprintf("pid=%d host=%s start=%d\n", dead, host, now.Unix())
			os.WriteFile(filepath.Join(l, "owner"), []byte(txt), 0o600)
		}, false},
		{"existing empty directory (older mash lock), fresh", func(t *testing.T, l string) {
			if err := os.Mkdir(l, 0o700); err != nil {
				t.Fatal(err)
			}
		}, false},
		{"no owner file, 1 hour old", func(t *testing.T, l string) {
			if err := os.Mkdir(l, 0o700); err != nil {
				t.Fatal(err)
			}
			past := now.Add(-time.Hour)
			if err := os.Chtimes(l, past, past); err != nil {
				t.Fatal(err)
			}
		}, false},
		{"garbage owner file, 1 hour old", func(t *testing.T, l string) {
			if err := os.Mkdir(l, 0o700); err != nil {
				t.Fatal(err)
			}
			os.WriteFile(filepath.Join(l, "owner"), []byte("not an owner\x00"), 0o600)
			past := now.Add(-time.Hour)
			if err := os.Chtimes(l, past, past); err != nil {
				t.Fatal(err)
			}
		}, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			lock := filepath.Join(t.TempDir(), "x.creds.lock")
			c.setup(t, lock)
			unlock, err := epLock(lock)
			if c.reclaimed {
				if err != nil {
					t.Fatalf("not reclaimed: %v", err)
				}
				if o, _, ok := epReadOwner(lock); !ok || o.pid != os.Getpid() {
					t.Errorf("lock not taken by this run: %+v %v", o, ok)
				}
				unlock()
				if _, err := os.Lstat(lock); err == nil {
					t.Error("lock left after unlock")
				}
			} else if !errors.Is(err, errEpBusy) {
				t.Fatalf("want busy, got %v", err)
			}
			if left, _ := filepath.Glob(lock + ".stale.*"); len(left) != 0 {
				t.Errorf("stale copies left: %v", left)
			}
		})
	}
}

func TestEpLockOwnerFile(t *testing.T) {
	lock := filepath.Join(t.TempDir(), "x.lock")
	unlock, err := epLock(lock)
	if err != nil {
		t.Fatal(err)
	}
	fi, err := os.Stat(filepath.Join(lock, "owner"))
	if err != nil || fi.Mode().Perm() != 0o600 {
		t.Fatalf("owner file: %v %v", fi, err)
	}
	o, _, ok := epReadOwner(lock)
	if !ok || o.pid != os.Getpid() || o.host != hostName(t) || time.Since(time.Unix(o.start, 0)) > time.Minute {
		t.Errorf("owner %+v %v", o, ok)
	}
	if ents, _ := os.ReadDir(lock); len(ents) != 1 {
		t.Errorf("lock directory holds %d entries, want 1", len(ents))
	}
	unlock()
	if _, err := os.Lstat(lock); err == nil {
		t.Error("lock left after unlock")
	}
}

func TestEpLockReclaimConcurrent(t *testing.T) {
	if epPidState(os.Getpid()) < 0 {
		t.Skip("no process check on this platform")
	}
	prev := epLockWait
	epLockWait = 20 * time.Second
	t.Cleanup(func() { epLockWait = prev })
	lock := filepath.Join(t.TempDir(), "x.creds.lock")
	writeLockOwner(t, lock, epOwner{deadPid(t), hostName(t), epHostID(), time.Now().Unix()})
	var inside, maxInside, entered atomic.Int32
	var wg sync.WaitGroup
	errs := make(chan error, 8)
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			unlock, err := epLock(lock)
			if err != nil {
				errs <- err
				return
			}
			n := inside.Add(1)
			for {
				m := maxInside.Load()
				if n <= m || maxInside.CompareAndSwap(m, n) {
					break
				}
			}
			entered.Add(1)
			time.Sleep(10 * time.Millisecond)
			inside.Add(-1)
			unlock()
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Errorf("epLock: %v", err)
	}
	if maxInside.Load() > 1 || entered.Load() != 8 {
		t.Errorf("max holders %d, entered %d of 8", maxInside.Load(), entered.Load())
	}
}

func TestEpUnlockLeavesReplacedLock(t *testing.T) {
	lock := filepath.Join(t.TempDir(), "x.creds.lock")
	unlock, err := epLock(lock)
	if err != nil {
		t.Fatal(err)
	}
	other := epOwner{os.Getpid() + 1, hostName(t), epHostID(), time.Now().Unix() + 7}
	if err := os.WriteFile(filepath.Join(lock, "owner"), []byte(other.String()), 0o600); err != nil {
		t.Fatal(err)
	}
	unlock()
	if _, raw, _ := epReadOwner(lock); raw != other.String() {
		t.Errorf("lock of another run was removed or changed: %q", raw)
	}
	if left, _ := filepath.Glob(lock + ".stale.*"); len(left) != 0 {
		t.Errorf("stale copies left: %v", left)
	}
	// An owner file that is missing is left alone too.
	os.Remove(filepath.Join(lock, "owner"))
	unlock()
	if _, err := os.Lstat(lock); err != nil {
		t.Errorf("directory without an owner file was removed: %v", err)
	}
}

func TestEpReclaimLeavesReplacedLockAside(t *testing.T) {
	if epPidState(os.Getpid()) < 0 || epHostID() == "" {
		t.Skip("no process check or host id on this platform")
	}
	lock := filepath.Join(t.TempDir(), "x.creds.lock")
	writeLockOwner(t, lock, epOwner{deadPid(t), hostName(t), epHostID(), time.Now().Unix()})
	var fresh epOwner
	epReclaimAfterJudge = func() {
		// The holder finished and a new run took the path between the judgement and the rename.
		epReclaimAfterJudge = nil
		os.RemoveAll(lock)
		fresh = epOwner{os.Getpid(), hostName(t), epHostID(), time.Now().Unix()}
		writeLockOwner(t, lock, fresh)
	}
	t.Cleanup(func() { epReclaimAfterJudge = nil })
	epReclaim(lock)
	// The path is not touched after the rename: the moved directory is not put back and not removed.
	if _, err := os.Lstat(lock); err == nil {
		t.Fatal("reclaim put something at the lock path")
	}
	left, _ := filepath.Glob(lock + ".stale.*")
	if len(left) != 1 {
		t.Fatalf("want the moved-aside copy left, got %v", left)
	}
	if _, raw, _ := epReadOwner(left[0]); raw != fresh.String() {
		t.Errorf("moved-aside copy holds %q", raw)
	}
}

func TestEpReclaimKeepsThirdHolderDirectory(t *testing.T) {
	if epPidState(os.Getpid()) < 0 || epHostID() == "" {
		t.Skip("no process check or host id on this platform")
	}
	lock := filepath.Join(t.TempDir(), "x.creds.lock")
	writeLockOwner(t, lock, epOwner{deadPid(t), hostName(t), epHostID(), time.Now().Unix()})
	epReclaimAfterJudge = func() {
		// A new run replaced the judged lock.
		epReclaimAfterJudge = nil
		os.RemoveAll(lock)
		writeLockOwner(t, lock, epOwner{os.Getpid(), hostName(t), epHostID(), time.Now().Unix()})
	}
	epReclaimAfterMove = func() {
		// A third run makes its lock directory at the freed path before the owner text is read again.
		epReclaimAfterMove = nil
		if err := os.Mkdir(lock, 0o700); err != nil {
			t.Error(err)
		}
	}
	t.Cleanup(func() { epReclaimAfterJudge, epReclaimAfterMove = nil, nil })
	epReclaim(lock)
	ents, err := os.ReadDir(lock)
	if err != nil || len(ents) != 0 {
		t.Errorf("third holder's directory was replaced or changed: %v %v", ents, err)
	}
	if left, _ := filepath.Glob(lock + ".stale.*"); len(left) != 1 {
		t.Errorf("want the moved-aside copy left, got %v", left)
	}
}
