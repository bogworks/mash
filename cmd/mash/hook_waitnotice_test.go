package main

import (
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/bogworks/mash/internal/spool"
)

// Tests for the notice `mash hook` prints while its session's serve waits for the endpoint's lease (ADR 0090).

const waitNoticeText = "mash: this session does not hold endpoint a (held by pid 4242 on holderhost); mail is off until it recovers"

// waitHookEnv is an environment whose hook has a spool but no server: the hook makes no connection.
func waitHookEnv(t *testing.T) (map[string]string, spool.Spool) {
	t.Helper()
	creds := t.TempDir() + "/a.creds"
	if err := os.WriteFile(creds, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	env := map[string]string{"HOME": t.TempDir(), "MASH_NAME": "a", "MASH_URL": "nats://127.0.0.1:1",
		"MASH_OWNER": "o", "MASH_CREDS": creds}
	sp := spoolOf(env)
	if err := sp.Init(); err != nil {
		t.Fatal(err)
	}
	return env, sp
}

func noteWait(t *testing.T, sp spool.Spool, w spool.WaitState) {
	t.Helper()
	unlock, err := sp.Lock()
	if err != nil {
		t.Fatal(err)
	}
	defer unlock()
	if w.ServePID == 0 {
		w.ServePID = os.Getpid()
	}
	if w.HolderPID == "" {
		w.HolderPID, w.HolderHost = "4242", "holderhost"
	}
	if w.Session == "" && w.ParentPID == 0 {
		w.Session = "sess-1"
	}
	w.Updated = time.Now()
	if err := sp.NoteWaitState(w); err != nil {
		t.Fatal(err)
	}
}

func underLock(t *testing.T, sp spool.Spool, f func()) {
	t.Helper()
	unlock, err := sp.Lock()
	if err != nil {
		t.Fatal(err)
	}
	defer unlock()
	f()
}

func TestHookTellsWaitingSessionOnFirstPrompt(t *testing.T) {
	env, sp := waitHookEnv(t)
	if got := runHookAs(t, env, "UserPromptSubmit", "sess-1"); got != "" {
		t.Fatalf("a notice with no wait state: %q", got)
	}
	noteWait(t, sp, spool.WaitState{})
	if got := runHookAs(t, env, "UserPromptSubmit", "sess-1"); got != waitNoticeText {
		t.Fatalf("notice %q, want %q", got, waitNoticeText)
	}
	// Once, not on the next run.
	if got := runHookAs(t, env, "UserPromptSubmit", "sess-1"); got != "" {
		t.Fatalf("repeated at once: %q", got)
	}
	if got := runHookAs(t, env, "PostToolUse", "sess-1"); got != "" {
		t.Fatalf("repeated after a tool call: %q", got)
	}
}

func TestHookWaitNoticeRepeatsAfterTenMinutes(t *testing.T) {
	env, sp := waitHookEnv(t)
	noteWait(t, sp, spool.WaitState{})
	if got := runHookAs(t, env, "UserPromptSubmit", "sess-1"); got != waitNoticeText {
		t.Fatalf("first: %q", got)
	}
	underLock(t, sp, func() { _ = sp.NoteWaitNotice(time.Now().Add(-9 * time.Minute)) })
	if got := runHookAs(t, env, "UserPromptSubmit", "sess-1"); got != "" {
		t.Fatalf("after 9 minutes: %q", got)
	}
	underLock(t, sp, func() { _ = sp.NoteWaitNotice(time.Now().Add(-11 * time.Minute)) })
	if got := runHookAs(t, env, "UserPromptSubmit", "sess-1"); got != waitNoticeText {
		t.Fatalf("after 11 minutes: %q", got)
	}
	if got := runHookAs(t, env, "UserPromptSubmit", "sess-1"); got != "" {
		t.Fatalf("repeated right after the second: %q", got)
	}
}

func TestHookWaitNoticeShowsTheCurrentHolder(t *testing.T) {
	env, sp := waitHookEnv(t)
	noteWait(t, sp, spool.WaitState{HolderPID: "7", HolderHost: "other"})
	if got := runHookAs(t, env, "UserPromptSubmit", "sess-1"); !strings.Contains(got, "(held by pid 7 on other)") {
		t.Fatalf("notice %q", got)
	}
}

// A state whose serve has exited is stale: nothing is printed, and it is removed.
func TestHookWaitNoticeStaleStateIsSilent(t *testing.T) {
	env, sp := waitHookEnv(t)
	cmd := exec.Command("true")
	if err := cmd.Run(); err != nil {
		t.Skip(err)
	}
	noteWait(t, sp, spool.WaitState{ServePID: cmd.Process.Pid})
	if got := runHookAs(t, env, "UserPromptSubmit", "sess-1"); got != "" {
		t.Fatalf("a notice from a dead serve's state: %q", got)
	}
	if _, ok := sp.LiveWaitState(); ok {
		t.Fatal("stale state still live")
	}
}

// Once the state is cleared (the serve took the lease, or exited) nothing is printed, and a later wait is told again.
func TestHookWaitNoticeClearedIsSilentAndRearms(t *testing.T) {
	env, sp := waitHookEnv(t)
	noteWait(t, sp, spool.WaitState{})
	if got := runHookAs(t, env, "UserPromptSubmit", "sess-1"); got != waitNoticeText {
		t.Fatalf("first: %q", got)
	}
	underLock(t, sp, func() { _ = sp.ClearWaitState(os.Getpid(), "") })
	if got := runHookAs(t, env, "UserPromptSubmit", "sess-1"); got != "" {
		t.Fatalf("after the clear: %q", got)
	}
	noteWait(t, sp, spool.WaitState{})
	if got := runHookAs(t, env, "UserPromptSubmit", "sess-1"); got != waitNoticeText {
		t.Fatalf("a new wait must be told again: %q", got)
	}
}

// The session that holds the lease, or any other session, is not told: only the waiting serve's session is.
func TestHookWaitNoticeOnlyForTheWaitingSession(t *testing.T) {
	env, sp := waitHookEnv(t)
	noteWait(t, sp, spool.WaitState{Session: "sess-1", ParentPID: 999999})
	if got := runHookAs(t, env, "UserPromptSubmit", "sess-2"); got != "" {
		t.Fatalf("another session was told: %q", got)
	}
	if got := runHookAs(t, env, "UserPromptSubmit", "sess-1"); got != waitNoticeText {
		t.Fatalf("the waiting session: %q", got)
	}
}

// With no session id on either side, the serve's parent being an ancestor of the hook identifies the session.
func TestHookWaitNoticeByParentAncestry(t *testing.T) {
	env, sp := waitHookEnv(t)
	if os.Getppid() <= 1 {
		t.Skip("no parent")
	}
	noteWait(t, sp, spool.WaitState{ParentPID: os.Getppid()})
	if got := runHook(t, env, "UserPromptSubmit"); got != waitNoticeText {
		t.Fatalf("notice %q", got)
	}
}
