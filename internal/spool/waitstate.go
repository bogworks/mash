package spool

import (
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

// The lease-wait state (ADR 0090): while a serve waits for its endpoint's lease it records who holds it, in
// .lease-wait next to the waiter records, so `mash hook` can tell the session that its mail is off. The hook keeps its
// rate limit for that notice in .lease-wait-told. serve removes both when it takes the lease or exits, but only a state that still names it (several serves of an
// endpoint can wait at once and the later writer's state stands); a state whose
// serve process is gone is stale and is read as absent.
const (
	waitStateFile = ".lease-wait"
	waitToldFile  = ".lease-wait-told"
)

// WaitState is a waiting serve's record of the lease it waits for.
type WaitState struct {
	HolderPID   string    `json:"holder_pid"`
	HolderHost  string    `json:"holder_host"`
	HolderSince string    `json:"holder_since,omitempty"`
	ServePID    int       `json:"serve_pid"`
	Instance    string    `json:"instance,omitempty"`   // the serve's own token: told from another serve whose pid is reused
	ParentPID   int       `json:"parent_pid,omitempty"` // the serve's parent: its hooks descend from it
	Session     string    `json:"session,omitempty"`    // the serve's CLAUDE_CODE_SESSION_ID
	Updated     time.Time `json:"updated"`              // rewritten on every retry
}

// NoteWaitState records the state, atomically. Call it under Lock.
func (s Spool) NoteWaitState(w WaitState) error {
	b, err := json.Marshal(w)
	if err != nil {
		return err
	}
	return s.writeOrRemove(waitStateFile, string(b))
}

// readWaitState is the recorded state, whatever its serve's condition; ok is false for none or an unreadable one.
func (s Spool) readWaitState() (WaitState, bool) {
	b, err := os.ReadFile(filepath.Join(s.Dir, waitStateFile))
	var w WaitState
	if err != nil || json.Unmarshal(b, &w) != nil {
		return WaitState{}, false
	}
	return w, true
}

func (s Spool) removeWaitFiles() error {
	for _, n := range []string{waitStateFile, waitToldFile} {
		if err := os.Remove(filepath.Join(s.Dir, n)); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return err
		}
	}
	return nil
}

// ClearWaitState removes the state and the notice's rate-limit record, but only the state that still names this serve
// (its pid and instance): the files are shared by every serve of the endpoint that waits, so a serve whose state a later
// one overwrote must not clear the later one's. With no state there is none to protect and the rate-limit record goes.
// Call it under Lock.
func (s Spool) ClearWaitState(servePID int, instance string) error {
	if w, ok := s.readWaitState(); ok && (w.ServePID != servePID || w.Instance != instance) {
		return nil
	}
	return s.removeWaitFiles()
}

// ClearStaleWaitState removes the state when its serve process is gone (or it is unreadable) and the rate-limit record
// with it; a state of a live serve is left alone. Call it under Lock.
func (s Spool) ClearStaleWaitState() error {
	if _, ok := s.LiveWaitState(); ok {
		return nil
	}
	return s.removeWaitFiles()
}

// LiveWaitState is the recorded state when there is one and its serve process is still alive; a missing, unreadable or
// stale state (the serve is gone) is not one. A stale state's files are left for the next ClearWaitState.
func (s Spool) LiveWaitState() (WaitState, bool) {
	b, err := os.ReadFile(filepath.Join(s.Dir, waitStateFile))
	var w WaitState
	if err != nil || json.Unmarshal(b, &w) != nil || w.ServePID <= 0 {
		return WaitState{}, false
	}
	if err := syscall.Kill(w.ServePID, 0); err != nil && !errors.Is(err, syscall.EPERM) {
		return WaitState{}, false
	}
	return w, true
}

// WaitNoticeDue reports whether the hook's notice is due: none was recorded since the state appeared, or the last was at
// least every ago. Call it under Lock.
func (s Spool) WaitNoticeDue(now time.Time, every time.Duration) bool {
	b, err := os.ReadFile(filepath.Join(s.Dir, waitToldFile))
	if err != nil {
		return true
	}
	at, err := time.Parse(time.RFC3339Nano, strings.TrimSpace(string(b)))
	return err != nil || now.Sub(at) >= every || at.After(now.Add(time.Minute))
}

// NoteWaitNotice records that the notice was printed at now. Call it under Lock.
func (s Spool) NoteWaitNotice(now time.Time) error {
	return s.writeOrRemove(waitToldFile, now.UTC().Format(time.RFC3339Nano))
}
