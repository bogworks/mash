package main

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/bogworks/mash/internal/sap"
)

func hostName(t *testing.T) string {
	t.Helper()
	h, err := os.Hostname()
	if err != nil {
		t.Fatal(err)
	}
	return h
}

// holdLock takes the lock at path with epLock, as a reissue does, and returns the function that releases it. The lock is
// also released when the test ends.
func holdLock(t *testing.T, path string) func() {
	t.Helper()
	unlock, err := epLock(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(unlock)
	return unlock
}

func shortLockWait(t *testing.T, d time.Duration) {
	t.Helper()
	prev := epLockWait
	epLockWait = d
	t.Cleanup(func() { epLockWait = prev })
}

func TestEpLockExcludesAndReleases(t *testing.T) {
	shortLockWait(t, 100*time.Millisecond)
	path := filepath.Join(t.TempDir(), "x.lock")
	unlock := holdLock(t, path)
	if _, err := epLock(path); !errors.Is(err, errEpBusy) {
		t.Fatalf("second lock: %v, want errEpBusy", err)
	}
	if b, err := os.ReadFile(path); err != nil || !strings.HasPrefix(string(b), "pid=") {
		t.Errorf("info line %q %v", b, err)
	}
	unlock()
	u2, err := epLock(path)
	if err != nil {
		t.Fatalf("after unlock: %v", err)
	}
	u2()
	if _, err := os.Lstat(path); err != nil {
		t.Errorf("the lock file was removed: %v", err)
	}
}

func TestEpLockOldDirectory(t *testing.T) {
	path := filepath.Join(t.TempDir(), "x.lock")
	if err := os.Mkdir(path, 0o700); err != nil {
		t.Fatal(err)
	}
	if _, err := epLock(path); !errors.Is(err, errEpOldLock) {
		t.Fatalf("got %v, want errEpOldLock", err)
	}
	d := epBusyDetail(sap.Address{}, path, true)
	if !strings.Contains(d, path) || !strings.Contains(d, "older mash") || !strings.Contains(d, "remove it once") {
		t.Errorf("detail %q", d)
	}
}

func TestEpLockMutualExclusion(t *testing.T) {
	path := filepath.Join(t.TempDir(), "x.lock")
	var wg sync.WaitGroup
	var mu sync.Mutex
	inside, max := 0, 0
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 5; j++ {
				unlock, err := epLock(path)
				if err != nil {
					t.Error(err)
					return
				}
				mu.Lock()
				inside++
				if inside > max {
					max = inside
				}
				mu.Unlock()
				time.Sleep(time.Millisecond)
				mu.Lock()
				inside--
				mu.Unlock()
				unlock()
			}
		}()
	}
	wg.Wait()
	if max != 1 {
		t.Errorf("up to %d holders at once", max)
	}
}
