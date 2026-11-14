//go:build unix

package ledger

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func seqsOf(t *testing.T, p, gen string) Ranges {
	t.Helper()
	st, err := Read(p)
	if err != nil {
		t.Fatal(err)
	}
	g := st.Gens[gen]
	if g == nil {
		return nil
	}
	return g.P
}

// The lost-append race: a second process appends while the first is between its read and its rename. The append waits
// for the compaction and is then part of the fold, and the snapshot's content survives too.
func TestAppendDuringCompactionWaitsAndSurvives(t *testing.T) {
	old := compactAt
	compactAt = 1500
	t.Cleanup(func() { compactAt = old; afterCompactRead = nil })
	p := filepath.Join(t.TempDir(), File)
	a, b := New(p), New(p)

	started := make(chan struct{})
	release := make(chan struct{})
	var once sync.Once
	afterCompactRead = func() {
		once.Do(func() {
			close(started)
			<-release
		})
	}
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := uint64(1); i < 400; i += 2 {
			if err := a.Note("g", Presented, i); err != nil {
				t.Error(err)
				return
			}
		}
	}()
	<-started // a is inside compaction, holding the lock, after its read

	done := make(chan error, 1)
	go func() { done <- b.Note("late", Presented, 7777) }()
	select {
	case err := <-done:
		t.Fatalf("the second instance appended during compaction (err %v)", err)
	case <-time.After(150 * time.Millisecond):
	}
	close(release)
	wg.Wait()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	st, _ := Read(p)
	if !st.Gens["late"].P.Has(7777) {
		t.Fatal("the late append was lost to the compaction")
	}
	if n := st.Gens["g"].P.Count(); n == 0 {
		t.Fatal("the snapshot content is gone")
	}
	b2, _ := os.ReadFile(p)
	if strings.Count(string(b2), "snapshot") == 0 {
		t.Fatal("the file was never compacted")
	}
}

// Two instances append concurrently while a tiny threshold forces compaction: nothing is lost.
func TestConcurrentAppendersWithCompactionLoseNothing(t *testing.T) {
	old := compactAt
	compactAt = 800
	t.Cleanup(func() { compactAt = old })
	p := filepath.Join(t.TempDir(), File)
	var wg sync.WaitGroup
	for w, base := range []uint64{0, 100000} {
		wg.Add(1)
		go func() {
			defer wg.Done()
			l := New(p)
			for i := range 50 {
				if err := l.Note("g", Presented, base+uint64(i)*2+1); err != nil {
					t.Errorf("writer %d round %d: %v", w, i, err)
					return
				}
			}
		}()
	}
	wg.Wait()
	got := seqsOf(t, p, "g")
	var want Ranges
	for _, base := range []uint64{0, 100000} {
		for i := range 50 {
			want.Add(base+uint64(i)*2+1, base+uint64(i)*2+1)
		}
	}
	if !equal(got, want) {
		t.Fatalf("fold %v, want %v", got, want)
	}
}

// An appender that held the old file across a compaction opens the new one: its record lands in the file now at path.
func TestAppenderAfterCompactionWritesToTheNewFile(t *testing.T) {
	old := compactAt
	compactAt = 1 << 30
	t.Cleanup(func() { compactAt = old })
	p := filepath.Join(t.TempDir(), File)
	a, b := New(p), New(p)
	for i := uint64(1); i < 40; i += 2 {
		if err := a.Note("g", Presented, i); err != nil {
			t.Fatal(err)
		}
	}
	oldInode, _ := os.Stat(p)
	// b has written before (its torn-tail state is known); a compaction by another process then replaces the file.
	if err := b.Note("g", Presented, 101); err != nil {
		t.Fatal(err)
	}
	unlock, err := lockFile(p+LockSuffix, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if err := compact(p); err != nil {
		t.Fatal(err)
	}
	unlock()
	if newInode, _ := os.Stat(p); os.SameFile(oldInode, newInode) {
		t.Fatal("the compaction did not replace the file")
	}
	if err := b.Note("g", Presented, 103); err != nil {
		t.Fatal(err)
	}
	got := seqsOf(t, p, "g")
	if !got.Has(103) || !got.Has(101) || !got.Has(39) {
		t.Fatalf("fold %v lost a record across the compaction", got)
	}
}

// A lock held elsewhere makes a write fail within the bound, as any write failure; once released, writes work again.
func TestLockTimeoutFailsTheWriteWithinTheBound(t *testing.T) {
	oldWait := lockWait
	lockWait = 200 * time.Millisecond
	t.Cleanup(func() { lockWait = oldWait })
	p := filepath.Join(t.TempDir(), File)
	l := New(p)
	unlock, err := lockFile(p+LockSuffix, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	err = l.Note("g", Presented, 1)
	took := time.Since(start)
	if !errors.Is(err, errLockTimeout) {
		t.Fatalf("err %v, want the lock timeout", err)
	}
	if took < 150*time.Millisecond || took > 2*time.Second {
		t.Fatalf("the failure took %v, want about the bound", took)
	}
	if _, serr := os.Stat(p); serr == nil {
		t.Fatal("the log was written without the lock")
	}
	unlock()
	if err := l.Note("g", Presented, 1); err != nil {
		t.Fatal(err)
	}
	if !seqsOf(t, p, "g").Has(1) {
		t.Fatal("the write after release is missing")
	}
}

func TestLockFileIs0600(t *testing.T) {
	p := filepath.Join(t.TempDir(), File)
	if err := New(p).Note("g", Presented, 1); err != nil {
		t.Fatal(err)
	}
	fi, err := os.Stat(p + LockSuffix)
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode().Perm() != 0o600 {
		t.Fatalf("lock file mode %v, want 0600", fi.Mode().Perm())
	}
}

func TestLockFileSymlinkIsRefused(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, File)
	target := filepath.Join(dir, "elsewhere")
	if err := os.WriteFile(target, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, p+LockSuffix); err != nil {
		t.Fatal(err)
	}
	if err := New(p).Note("g", Presented, 1); err == nil {
		t.Fatal("a symlinked lock file was followed")
	}
}
