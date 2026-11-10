package spool

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

func deadPID(t *testing.T) int {
	t.Helper()
	cmd := exec.Command("true")
	if err := cmd.Run(); err != nil {
		t.Skipf("cannot run true: %v", err)
	}
	return cmd.Process.Pid
}

func TestWaitStateRoundTrip(t *testing.T) {
	s := newSpool(t)
	if _, ok := s.LiveWaitState(); ok {
		t.Fatal("a state with none written")
	}
	w := WaitState{HolderPID: "4242", HolderHost: "h", HolderSince: "x", ServePID: os.Getpid(), ParentPID: 7, Session: "S", Updated: time.Now()}
	if err := s.NoteWaitState(w); err != nil {
		t.Fatal(err)
	}
	got, ok := s.LiveWaitState()
	if !ok || got.HolderPID != "4242" || got.HolderHost != "h" || got.ParentPID != 7 || got.Session != "S" {
		t.Fatalf("got %+v %v", got, ok)
	}
	if mode(t, filepath.Join(s.Dir, waitStateFile)) != 0o600 {
		t.Fatalf("mode %v", mode(t, filepath.Join(s.Dir, waitStateFile)))
	}
	// Rewritten on a retry: the new holder replaces the old.
	w.HolderPID = "5555"
	if err := s.NoteWaitState(w); err != nil {
		t.Fatal(err)
	}
	if got, _ := s.LiveWaitState(); got.HolderPID != "5555" {
		t.Fatalf("not rewritten: %+v", got)
	}
}

func TestWaitStateStaleWhenServeGone(t *testing.T) {
	s := newSpool(t)
	if err := s.NoteWaitState(WaitState{HolderPID: "1", ServePID: deadPID(t)}); err != nil {
		t.Fatal(err)
	}
	if _, ok := s.LiveWaitState(); ok {
		t.Fatal("a state whose serve is gone must read as absent")
	}
}

func TestWaitStateClearedWithItsRateLimit(t *testing.T) {
	s := newSpool(t)
	now := time.Now()
	_ = s.NoteWaitState(WaitState{HolderPID: "1", ServePID: os.Getpid()})
	_ = s.NoteWaitNotice(now)
	if s.WaitNoticeDue(now.Add(time.Minute), 10*time.Minute) {
		t.Fatal("due a minute after a notice")
	}
	if err := s.ClearWaitState(os.Getpid(), ""); err != nil {
		t.Fatal(err)
	}
	if _, ok := s.LiveWaitState(); ok {
		t.Fatal("state survived the clear")
	}
	if !s.WaitNoticeDue(now.Add(time.Minute), 10*time.Minute) {
		t.Fatal("the rate limit survived the clear: a later wait would be told nothing")
	}
	if err := s.ClearWaitState(os.Getpid(), ""); err != nil {
		t.Fatalf("clearing nothing: %v", err)
	}
}

func TestWaitNoticeRateLimit(t *testing.T) {
	s := newSpool(t)
	now := time.Now()
	if !s.WaitNoticeDue(now, 10*time.Minute) {
		t.Fatal("not due with no notice yet")
	}
	_ = s.NoteWaitNotice(now)
	for _, c := range []struct {
		after time.Duration
		due   bool
	}{{0, false}, {9 * time.Minute, false}, {10 * time.Minute, true}, {time.Hour, true}} {
		if got := s.WaitNoticeDue(now.Add(c.after), 10*time.Minute); got != c.due {
			t.Fatalf("after %v: due %v, want %v", c.after, got, c.due)
		}
	}
}

// Two waiting serves of one endpoint share the files: the later writer's state stands, and a serve's removal clears only
// a state that still names it (C3).
func TestWaitStateRemovalIsOwnershipChecked(t *testing.T) {
	s := newSpool(t)
	me := os.Getpid()
	other := os.Getppid() // a live process that is not this serve
	a := WaitState{HolderPID: "1", ServePID: me, Instance: "A"}
	b := WaitState{HolderPID: "2", ServePID: other, Instance: "B"}
	if err := s.NoteWaitState(a); err != nil {
		t.Fatal(err)
	}
	if err := s.NoteWaitState(b); err != nil { // last writer wins
		t.Fatal(err)
	}
	if got, ok := s.LiveWaitState(); !ok || got.Instance != "B" {
		t.Fatalf("later writer must win: %+v %v", got, ok)
	}
	now := time.Now()
	_ = s.NoteWaitNotice(now)
	if err := s.ClearWaitState(me, "A"); err != nil { // the loser removes: not its state
		t.Fatal(err)
	}
	if got, ok := s.LiveWaitState(); !ok || got.Instance != "B" {
		t.Fatalf("the loser's removal cleared the other serve's state: %+v %v", got, ok)
	}
	if s.WaitNoticeDue(now.Add(time.Minute), 10*time.Minute) {
		t.Fatal("the loser's removal cleared the other serve's rate limit")
	}
	// The same pid with another instance is not the owner either.
	if err := s.ClearWaitState(other, "A"); err != nil {
		t.Fatal(err)
	}
	if _, ok := s.LiveWaitState(); !ok {
		t.Fatal("a removal by another instance cleared the state")
	}
	if err := s.ClearWaitState(other, "B"); err != nil {
		t.Fatal(err)
	}
	if _, ok := s.LiveWaitState(); ok {
		t.Fatal("the owner's removal did not clear its state")
	}
}

// The hook removes only a state whose serve is gone.
func TestClearStaleWaitState(t *testing.T) {
	s := newSpool(t)
	_ = s.NoteWaitState(WaitState{ServePID: os.Getpid(), Instance: "live"})
	if err := s.ClearStaleWaitState(); err != nil {
		t.Fatal(err)
	}
	if _, ok := s.LiveWaitState(); !ok {
		t.Fatal("a live serve's state was cleared as stale")
	}
	_ = s.NoteWaitState(WaitState{ServePID: deadPID(t), Instance: "dead"})
	if err := s.ClearStaleWaitState(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(s.Dir, waitStateFile)); !os.IsNotExist(err) {
		t.Fatalf("stale state kept: %v", err)
	}
}
