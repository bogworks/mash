package main

import (
	"errors"
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
	writeLockOwner(t, path, epOwner{os.Getpid(), hostName(t), time.Now().Unix()})
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
	old := now.Add(-2 * epLockStaleAfter).Unix()
	dead := deadPid(t)
	cases := []struct {
		name      string
		setup     func(t *testing.T, lock string)
		reclaimed bool
	}{
		{"dead pid on this host", func(t *testing.T, l string) { writeLockOwner(t, l, epOwner{dead, host, now.Unix()}) }, true},
		{"live pid on this host", func(t *testing.T, l string) { writeLockOwner(t, l, epOwner{os.Getpid(), host, now.Unix()}) }, false},
		{"other host, recent", func(t *testing.T, l string) { writeLockOwner(t, l, epOwner{dead, host + "-other", now.Unix()}) }, false},
		{"other host, older than the stale age", func(t *testing.T, l string) { writeLockOwner(t, l, epOwner{dead, host + "-other", old}) }, true},
		{"live pid on this host, older than the stale age", func(t *testing.T, l string) { writeLockOwner(t, l, epOwner{os.Getpid(), host, old}) }, true},
		{"no owner file, fresh", func(t *testing.T, l string) {
			if err := os.Mkdir(l, 0o700); err != nil {
				t.Fatal(err)
			}
		}, false},
		{"no owner file, older than 15 s", func(t *testing.T, l string) {
			if err := os.Mkdir(l, 0o700); err != nil {
				t.Fatal(err)
			}
			past := now.Add(-time.Minute)
			if err := os.Chtimes(l, past, past); err != nil {
				t.Fatal(err)
			}
		}, true},
		{"garbage owner file, fresh", func(t *testing.T, l string) {
			if err := os.Mkdir(l, 0o700); err != nil {
				t.Fatal(err)
			}
			os.WriteFile(filepath.Join(l, "owner"), []byte("not an owner\x00"), 0o600)
		}, false},
		{"garbage owner file, older than 15 s", func(t *testing.T, l string) {
			if err := os.Mkdir(l, 0o700); err != nil {
				t.Fatal(err)
			}
			os.WriteFile(filepath.Join(l, "owner"), []byte("not an owner\x00"), 0o600)
			past := now.Add(-time.Minute)
			if err := os.Chtimes(l, past, past); err != nil {
				t.Fatal(err)
			}
		}, true},
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
	writeLockOwner(t, lock, epOwner{deadPid(t), hostName(t), time.Now().Unix()})
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
