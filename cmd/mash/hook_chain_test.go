package main

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/nats-io/nats.go/jetstream"

	"github.com/bogworks/mash/internal/sap"
	"github.com/bogworks/mash/internal/spool"
	"github.com/bogworks/mash/internal/store"
)

// ownedByPID reports whether the spool's owner record names pid as serve's parent.
func ownedByPID(sp spool.Spool, pid int) bool {
	var r struct{ PID int }
	if json.Unmarshal([]byte(sp.Owner()), &r) != nil || r.PID != pid {
		return false
	}
	b, err := os.ReadFile(filepath.Join(sp.Dir, ".session")) // an older hook reads only this
	return err == nil && strings.TrimSpace(string(b)) == "pid:"+strconv.Itoa(pid)
}

func skipOnWindows(t *testing.T) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("hook delivery needs flock and a process tree")
	}
}

// Ownership through a real process chain, nothing stubbed: a fake claude is the parent of a real `mash serve` (its
// owner record names <claude>) and of `sh -c 'mash hook'`. A hook under that claude shows and drains the mail,
// also after /clear (a new session_id, the same serve); a hook under another claude process shows nothing and does
// not mark the spool hooked.
func TestHookOwnershipThroughARealProcessChain(t *testing.T) {
	skipOnWindows(t)
	w := newWorld(t, "a", "b")
	sp := spoolOf(w.env["a"])
	owner := startFakeClaude(t, w.env["a"], true)
	eventually(t, 20*time.Second, "serve owns the spool as its claude", func() bool { return ownedByPID(sp, owner.pid) })
	other := startFakeClaude(t, w.env["a"], false) // same endpoint, another claude process, no serve

	hooked := filepath.Join(sp.Dir, ".hooked")
	if got := other.hook(t, "S1"); got != "" {
		t.Fatalf("a hook under another claude printed %q", got)
	}
	if _, err := os.Stat(hooked); err == nil {
		t.Fatal("a hook under another claude marked the spool hooked")
	}
	if got := owner.hook(t, "S1"); got != "" {
		t.Fatalf("an empty spool printed %q", got)
	}
	if _, err := os.Stat(hooked); err != nil {
		t.Fatalf("a hook under the owning claude did not mark the spool hooked: %v", err)
	}

	send := func(body string) {
		t.Helper()
		if out, code := w.cli(t, "b", "send", "a", body); code != 0 {
			t.Fatalf("send: %s", out)
		}
		eventually(t, 10*time.Second, "serve spooled "+body, func() bool { e, _ := sp.Waiting(); return len(e) == 1 })
	}
	send("hello through the chain")
	if got := other.hook(t, "S1"); got != "" {
		t.Fatalf("a hook under another claude showed %q", got)
	}
	if e, _ := sp.Waiting(); len(e) != 1 {
		t.Fatalf("a hook under another claude drained: %+v", e)
	}
	if got := owner.hook(t, "S1"); !strings.Contains(got, "> hello through the chain") {
		t.Fatalf("the owning claude's hook showed %q", got)
	}
	if e, _ := sp.Waiting(); len(e) != 0 {
		t.Fatalf("left waiting: %+v", e)
	}
	eventually(t, 5*time.Second, "serve acks what was shown", func() bool { return unreadOf(t, w, "a") == 0 })

	// /clear: the hook has a new session_id, the claude process and its serve are the same.
	send("after the clear")
	if got := owner.hook(t, "S2-after-clear"); !strings.Contains(got, "> after the clear") {
		t.Fatalf("a hook after /clear showed %q", got)
	}
	if e, _ := sp.Waiting(); len(e) != 0 {
		t.Fatalf("left waiting after /clear: %+v", e)
	}
	eventually(t, 5*time.Second, "serve acks what was shown", func() bool { return unreadOf(t, w, "a") == 0 })
}

// A claude restart: the old claude and its serve die with mail spooled and unshown; the new claude's serve takes the
// spool over (owner pid:<new>) and NATS redelivers the mail, which the new claude's hook shows once. Nothing is lost.
func TestHookMailSurvivesAClaudeRestart(t *testing.T) {
	skipOnWindows(t)
	w := newWorld(t, "a", "b")
	// A short AckWait, so the dead serve's unacked mail comes back in seconds, not half a minute.
	js, err := jetstream.New(w.e.Conn(t, w.e.AdminCreds))
	if err != nil {
		t.Fatal(err)
	}
	cc := store.ConsumerConfig()
	cc.AckWait = 8 * time.Second
	if _, err := js.UpdateConsumer(context.Background(), sap.InboxStream("a"), cc); err != nil {
		t.Fatal(err)
	}
	sp := spoolOf(w.env["a"])
	old := startFakeClaude(t, w.env["a"], true)
	eventually(t, 20*time.Second, "serve owns the spool", func() bool { return ownedByPID(sp, old.pid) })
	old.hook(t, "S1") // the session is on the spool from now on
	if out, code := w.cli(t, "b", "send", "a", "survives the restart"); code != 0 {
		t.Fatalf("send: %s", out)
	}
	eventually(t, 10*time.Second, "serve spooled it", func() bool { e, _ := sp.Waiting(); return len(e) == 1 })
	old.kill() // before any hook showed it

	fresh := startFakeClaude(t, w.env["a"], true)
	eventually(t, 40*time.Second, "the new serve owns the spool", func() bool { return ownedByPID(sp, fresh.pid) })
	// The hook of the new claude runs from the start (so mail redelivered to the new serve is spooled, not sent
	// down a channel), and shows the old mail when NATS redelivers it.
	shown := 0
	deadline := time.Now().Add(40 * time.Second)
	for time.Now().Before(deadline) && shown == 0 {
		shown += strings.Count(fresh.hook(t, "S-new"), "> survives the restart")
		time.Sleep(300 * time.Millisecond)
	}
	if shown != 1 {
		t.Fatalf("the old mail was shown %d times by the new claude", shown)
	}
	eventually(t, 5*time.Second, "serve acks it", func() bool { return unreadOf(t, w, "a") == 0 })
	if got := fresh.hook(t, "S-new"); got != "" {
		t.Fatalf("shown again: %q", got)
	}
}

// MASH_SERVE_FAILPOINT=before-ack, through real processes: a serve killed after a hook showed and drained a message but
// before serve acked it loses nothing and shows nothing twice. The new serve (no failpoint) takes the lease the dead
// one left behind, finds the redelivered message's tombstone and acks it instead of spooling it again.
func TestHookShownOnceAcrossAServeKilledBeforeAck(t *testing.T) {
	skipOnWindows(t)
	w := newWorld(t, "a", "b")
	js, err := jetstream.New(w.e.Conn(t, w.e.AdminCreds))
	if err != nil {
		t.Fatal(err)
	}
	cc := store.ConsumerConfig()
	cc.AckWait = 8 * time.Second // the dead serve's unacked mail comes back in seconds
	if _, err := js.UpdateConsumer(context.Background(), sap.InboxStream("a"), cc); err != nil {
		t.Fatal(err)
	}
	sp := spoolOf(w.env["a"])
	serveLog := filepath.Join(t.TempDir(), "serve.log")
	env := map[string]string{"MASH_SERVE_FAILPOINT": "before-ack", "MASH_TEST_SERVE_LOG": serveLog}
	for k, v := range w.env["a"] {
		env[k] = v
	}
	old := startFakeClaude(t, env, true)
	eventually(t, 20*time.Second, "serve owns the spool", func() bool { return ownedByPID(sp, old.pid) })
	old.hook(t, "S1") // the session is on the spool from now on
	if out, code := w.cli(t, "b", "send", "a", "shown once"); code != 0 {
		t.Fatalf("send: %s", out)
	}
	eventually(t, 10*time.Second, "serve spooled it", func() bool { e, _ := sp.Waiting(); return len(e) == 1 })
	if got := old.hook(t, "S1"); !strings.Contains(got, "> shown once") {
		t.Fatalf("the hook showed %q", got)
	}
	eventually(t, 10*time.Second, "serve reached the failpoint", func() bool {
		b, _ := os.ReadFile(serveLog)
		return strings.Count(string(b), "mash serve: failpoint before-ack #") == 1
	})
	time.Sleep(time.Second)
	if n := unreadOf(t, w, "a"); n != 1 {
		t.Fatalf("unread %d at the failpoint, want 1 (shown but not acked)", n)
	}
	if b, _ := os.ReadFile(serveLog); strings.Count(string(b), "failpoint") != 1 {
		t.Fatalf("serve stderr: %q", b)
	}
	pid, err := os.ReadFile(serveLog + ".pid")
	if err != nil {
		t.Fatal(err)
	}
	n, _ := strconv.Atoi(string(pid))
	if p, err := os.FindProcess(n); err == nil {
		p.Kill() // serve first, so it cannot release its lease; then its fake claude
		p.Wait()
	}
	old.kill()

	// The dead serve's lease would run out on its own after store.LeaseTTL (90 s, NATS's bucket TTL, nothing of
	// mash's): drop it now, as that expiry would, rather than wait.
	lease, err := js.KeyValue(context.Background(), store.LeaseBucket)
	if err != nil {
		t.Fatal(err)
	}
	if err := lease.Delete(context.Background(), "a"); err != nil {
		t.Fatal(err)
	}
	fresh := startFakeClaude(t, w.env["a"], true)
	eventually(t, 40*time.Second, "the new serve owns the spool", func() bool { return ownedByPID(sp, fresh.pid) })
	fresh.hook(t, "S-new")
	eventually(t, 40*time.Second, "the new serve acks the shown message", func() bool { return unreadOf(t, w, "a") == 0 })
	for i := 0; i < 3; i++ {
		if got := fresh.hook(t, "S-new"); got != "" {
			t.Fatalf("shown again: %q", got)
		}
		time.Sleep(300 * time.Millisecond)
	}
	if e, _ := sp.Waiting(); len(e) != 0 {
		t.Fatalf("re-spooled: %+v", e)
	}
}

// withEnv is env plus extra variables.
func withEnv(env map[string]string, extra map[string]string) map[string]string {
	out := map[string]string{}
	for k, v := range env {
		out[k] = v
	}
	for k, v := range extra {
		out[k] = v
	}
	return out
}

// sendAndSpool sends body from b to a and waits until serve has spooled it.
func sendAndSpool(t *testing.T, w *world, sp spool.Spool, body string) {
	t.Helper()
	if out, code := w.cli(t, "b", "send", "a", body); code != 0 {
		t.Fatalf("send: %s", out)
	}
	eventually(t, 10*time.Second, "serve spooled "+body, func() bool { e, _ := sp.Waiting(); return len(e) == 1 })
}

// Serve started through a shell that does not exec it: serve's parent is the shell, not claude, so the process rule
// cannot match; the hook (same CLAUDE_CODE_SESSION_ID, as Claude Code gives its hooks and MCP servers) is recognised
// by its session id.
func TestHookOwnershipThroughANonExecWrapper(t *testing.T) {
	skipOnWindows(t)
	w := newWorld(t, "a", "b")
	sp := spoolOf(w.env["a"])
	c := startFakeClaudeMode(t, withEnv(w.env["a"], map[string]string{"CLAUDE_CODE_SESSION_ID": "S-wrap"}), "wrapper")
	eventually(t, 20*time.Second, "serve owns the spool", func() bool { return sp.Owner() != "" })
	if ownedByPID(sp, c.pid) {
		t.Fatalf("serve's parent is claude %d: the wrapper did not interpose (owner %s)", c.pid, sp.Owner())
	}
	c.hook(t, "S-wrap") // the session is on the spool from now on
	sendAndSpool(t, w, sp, "through the wrapper")
	if got := c.hook(t, "S-wrap"); !strings.Contains(got, "> through the wrapper") {
		t.Fatalf("the hook showed %q", got)
	}
	if e, _ := sp.Waiting(); len(e) != 0 {
		t.Fatalf("left waiting: %+v", e)
	}
	eventually(t, 5*time.Second, "serve acks what was shown", func() bool { return unreadOf(t, w, "a") == 0 })
}

// A second claude with the same endpoint name, another session id and no launch token matches no rule: it prints
// nothing, leaves .hooked and the mail alone, records .refused and says so on stderr (which lands in hook.log).
func TestHookUnderAnotherClaudeIsRefusedAndRecorded(t *testing.T) {
	skipOnWindows(t)
	w := newWorld(t, "a", "b")
	sp := spoolOf(w.env["a"])
	owner := startFakeClaude(t, withEnv(w.env["a"], map[string]string{"CLAUDE_CODE_SESSION_ID": "S-owner", "MASH_SPOOL_FROM_START": "1"}), true)
	eventually(t, 20*time.Second, "serve owns the spool", func() bool { return ownedByPID(sp, owner.pid) })
	other := startFakeClaude(t, withEnv(w.env["a"], map[string]string{"CLAUDE_CODE_SESSION_ID": "S-other"}), false)
	sendAndSpool(t, w, sp, "not for the other claude")

	stdout, stderr := other.hookRaw(t, "S-other")
	if stdout != "" {
		t.Fatalf("a refused hook printed %q", stdout)
	}
	if !strings.Contains(stderr, "mash hook: not this session's spool (") || !strings.Contains(stderr, "mail left for its owner") {
		t.Fatalf("stderr %q", stderr)
	}
	if _, err := os.Stat(filepath.Join(sp.Dir, ".hooked")); err == nil {
		t.Fatal("a refused hook marked the spool hooked")
	}
	if _, why, ok := sp.Refused(); !ok || !strings.Contains(why, "session id") {
		t.Fatalf("refusal not recorded: %v %q", ok, why)
	}
	if e, _ := sp.Waiting(); len(e) != 1 {
		t.Fatalf("a refused hook drained: %+v", e)
	}
	if got := owner.hook(t, "S-owner"); !strings.Contains(got, "> not for the other claude") {
		t.Fatalf("the owner's hook showed %q", got)
	}
}

// After /clear on a layout the process rule cannot follow: the hook has a new session id and is under an unrelated
// process, but inherits the launch token serve was started with.
func TestHookAfterClearWithLaunchToken(t *testing.T) {
	skipOnWindows(t)
	w := newWorld(t, "a", "b")
	sp := spoolOf(w.env["a"])
	startFakeClaude(t, withEnv(w.env["a"], map[string]string{"CLAUDE_CODE_SESSION_ID": "S-old", "MASH_LAUNCH_TOKEN": "launch-1"}), true)
	eventually(t, 20*time.Second, "serve owns the spool", func() bool { return sp.Owner() != "" })
	cleared := startFakeClaude(t, withEnv(w.env["a"], map[string]string{"CLAUDE_CODE_SESSION_ID": "S-new", "MASH_LAUNCH_TOKEN": "launch-1"}), false)
	cleared.hook(t, "S-new")
	sendAndSpool(t, w, sp, "after the clear, by token")
	if got := cleared.hook(t, "S-new"); !strings.Contains(got, "> after the clear, by token") {
		t.Fatalf("the hook showed %q", got)
	}
	if e, _ := sp.Waiting(); len(e) != 0 {
		t.Fatalf("left waiting: %+v", e)
	}
	eventually(t, 5*time.Second, "serve acks what was shown", func() bool { return unreadOf(t, w, "a") == 0 })
}

// The hook's session id is the one on its stdin; CLAUDE_CODE_SESSION_ID only fills in when stdin has none. With both set
// and different, the stdin one decides.
func TestHookStdinSessionIDBeatsTheEnvironment(t *testing.T) {
	skipOnWindows(t)
	w := newWorld(t, "a", "b")
	sp := spoolOf(w.env["a"])
	owner := startFakeClaude(t, withEnv(w.env["a"], map[string]string{"CLAUDE_CODE_SESSION_ID": "S-owner", "MASH_SPOOL_FROM_START": "1"}), true)
	eventually(t, 20*time.Second, "serve owns the spool", func() bool { return ownedByPID(sp, owner.pid) })
	// Another claude whose environment claims the owner's session id, while its stdin says otherwise: refused.
	liar := startFakeClaude(t, withEnv(w.env["a"], map[string]string{"CLAUDE_CODE_SESSION_ID": "S-owner"}), false)
	sendAndSpool(t, w, sp, "stdin decides")
	if stdout, _ := liar.hookRaw(t, "S-else"); stdout != "" {
		t.Fatalf("env session id beat stdin: %q", stdout)
	}
	if e, _ := sp.Waiting(); len(e) != 1 {
		t.Fatalf("a refused hook drained: %+v", e)
	}
	// An environment that names another session, with the owner's id on stdin: owned.
	other := startFakeClaude(t, withEnv(w.env["a"], map[string]string{"CLAUDE_CODE_SESSION_ID": "S-other"}), false)
	if got := other.hook(t, "S-owner"); !strings.Contains(got, "> stdin decides") {
		t.Fatalf("stdin session id not honoured: %q", got)
	}
}
