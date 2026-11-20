package main

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/bogworks/mash/internal/spool"
)

// "Setups unlike ours": mash's own logic under the settings and launch layouts a personal-account, hand-written
// managed file, mash-on-PATH run never meets. A fake claude stands in for the host, so these tests prove what mash
// does with such a layout, not what Claude Code does with it; the live receipts cover Claude Code itself.
//
// MASH_SPOOL_FROM_START=1 is set where mail is sent before the first hook: without it, mail sent before a session's
// first hook goes down the channel (which Claude Code drops before the first prompt).

// strangerEnv is the endpoint a's environment for a stranger test: its own HOME and XDG_STATE_HOME, the config
// minimum idle wake, and extra.
func strangerEnv(w *world, extra map[string]string) map[string]string {
	home := w.env["a"]["HOME"]
	return withEnv(w.env["a"], withEnv(map[string]string{
		"XDG_STATE_HOME": filepath.Join(home, ".local", "state"),
		"MASH_IDLE_WAKE": "5s",
	}, extra))
}

// strangerDelivery sends two task messages from o.dev.b to a before any hook, and checks the first hook shows both, once,
// and that serve acks them (unread 0).
func strangerDelivery(t *testing.T, w *world, c *fakeClaude, sp spool.Spool, session string) {
	t.Helper()
	eventually(t, 20*time.Second, "serve owns the spool", func() bool { return sp.Owner() != "" })
	for _, body := range []string{"first task", "second task"} {
		if out, code := w.cli(t, "b", "send", tTo("a"), "--kind", "task", body); code != 0 {
			t.Fatalf("send: %s", out)
		}
	}
	eventually(t, 10*time.Second, "serve spooled both", func() bool { e, _ := sp.Waiting(); return len(e) == 2 })
	got := c.hook(t, session)
	for _, want := range []string{"2 messages from o.dev.b", "> first task", "> second task"} {
		if !strings.Contains(got, want) {
			t.Fatalf("the first hook lacks %q:\n%s", want, got)
		}
	}
	eventually(t, 5*time.Second, "serve acks what the hook showed", func() bool { return unreadOf(t, w, "a") == 0 })
	if again := c.hook(t, session); again != "" {
		t.Fatalf("shown twice: %q", again)
	}
}

// S1: plain claude, no managed settings and so no channel allowlist. Hooks still deliver and nothing is lost.
func TestStrangerPlainNoAllowlist(t *testing.T) {
	skipOnWindows(t)
	w := newWorld(t, "a", "b")
	env := strangerEnv(w, map[string]string{"MASH_SPOOL_FROM_START": "1"})
	c := startFakeClaude(t, env, true)
	strangerDelivery(t, w, c, spoolOf(env), "S1")

}

// S3a: an IDE-like launch with a minimal environment and PATH=/usr/bin:/bin, and no mash anywhere on it. The plugin's
// own hook commands (parsed from hooks.json, run through the launcher) exit 0, print nothing and leave a line in
// hook.log saying no binary was found: the failure is visible, not silent.
func TestStrangerMinimalPathHookIsVisible(t *testing.T) {
	skipOnWindows(t)
	envBin, err := exec.LookPath("env")
	if err != nil {
		t.Skip("no env(1)")
	}
	for _, d := range []string{"/usr/bin", "/bin"} {
		if _, err := os.Stat(filepath.Join(d, "mash")); err == nil {
			t.Skipf("a mash is installed in %s", d)
		}
	}
	root := pluginRootUnpinned(t)
	ran := 0
	for _, h := range pluginHooks(t) {
		if h.Event != "PostToolUse" && h.Event != "UserPromptSubmit" {
			continue
		}
		ran++
		home, state := t.TempDir(), t.TempDir()
		cmd := exec.Command(envBin, "-i", "HOME="+home, "XDG_STATE_HOME="+state, "PATH=/usr/bin:/bin",
			"CLAUDE_PLUGIN_ROOT="+root, "sh", "-c", h.Command)
		cmd.Stdin = strings.NewReader(`{"hook_event_name":"` + h.Event + `"}`)
		var out, errb bytes.Buffer
		cmd.Stdout, cmd.Stderr = &out, &errb
		if err := cmd.Run(); err != nil {
			t.Fatalf("%s: the hook did not exit 0: %v (stderr %q)", h.Event, err, errb.String())
		}
		if out.Len() != 0 || errb.Len() != 0 {
			t.Fatalf("%s: stdout %q stderr %q, want nothing (the hook's stderr goes to hook.log)", h.Event, out.String(), errb.String())
		}
		log, err := os.ReadFile(filepath.Join(state, "mash", "hook.log"))
		if err != nil || !strings.Contains(string(log), "no binary found") {
			t.Fatalf("%s: hook.log %q (%v), want a \"no binary found\" line", h.Event, log, err)
		}
	}
	if ran != 2 {
		t.Fatalf("ran %d hook commands, want PostToolUse and UserPromptSubmit", ran)
	}
}

// S3b: serve started through a shell that does not exec it, so its parent is not claude. With the same
// CLAUDE_CODE_SESSION_ID for serve and hooks the hook drains; a hook with another session id and no launch token is
// refused and leaves the mail.
func TestStrangerWrappedServeOwnsItsSpool(t *testing.T) {
	skipOnWindows(t)
	w := newWorld(t, "a", "b")
	env := strangerEnv(w, map[string]string{"CLAUDE_CODE_SESSION_ID": "s1", "MASH_SPOOL_FROM_START": "1"})
	sp := spoolOf(env)
	c := startFakeClaudeMode(t, env, "wrapper")
	eventually(t, 20*time.Second, "serve owns the spool", func() bool { return sp.Owner() != "" })
	if ownedByPID(sp, c.pid) {
		t.Fatalf("serve's parent is claude %d: the wrapper did not interpose (owner %s)", c.pid, sp.Owner())
	}
	sendAndSpool(t, w, sp, "through the wrapper")

	stdout, stderr := c.hookRaw(t, "s2")
	if stdout != "" || !strings.Contains(stderr, "mash hook: not this session's spool (") {
		t.Fatalf("another session's hook: stdout %q stderr %q", stdout, stderr)
	}
	if e, _ := sp.Waiting(); len(e) != 1 {
		t.Fatalf("a refused hook drained: %+v", e)
	}

	if got := c.hook(t, "s1"); !strings.Contains(got, "> through the wrapper") {
		t.Fatalf("the owner's hook showed %q", got)
	}
	eventually(t, 5*time.Second, "serve acks what was shown", func() bool { return unreadOf(t, w, "a") == 0 })
}

// S4: hooks disabled; the fake claude never runs one. Needs gate 3 (#200), the degradation to "hooks do not run".
func TestStrangerHooksDisabled(t *testing.T) {
	skipOnWindows(t)
	w := newWorld(t, "a", "b")
	env := strangerEnv(w, map[string]string{"MASH_SPOOL_FROM_START": "1"}) // the detector only runs on a session spooled from the start
	idle := 5 * time.Second
	c := startFakeClaude(t, env, true)
	sp := spoolOf(env)
	eventually(t, 20*time.Second, "serve owns the spool", func() bool { return sp.Owner() != "" })
	if out, code := w.cli(t, "b", "send", tTo("a"), "--kind", "task", "hooks are off"); code != 0 {
		t.Fatalf("send: %s", out)
	}
	// Four idle wakes' worth of time: the wakes back off and stop, they do not loop.
	time.Sleep(4 * idle)
	if n := len(c.notifications(t)); n > 3 {
		t.Fatalf("%d notifications reached the host, want at most 3 wakes", n)
	}
	// Two tool calls, 12 s apart (or gate 3's grace through its test-only knob, once it exists): no hook has run,
	// so delivery degrades.
	c.call(t, "who", `{}`)
	time.Sleep(12 * time.Second)
	c.call(t, "who", `{}`)
	// read delivers the message and acks it. The seq is the first message's.
	if got := c.call(t, "read", `{"seq":1}`); !strings.Contains(got, "> hooks are off") {
		t.Fatalf("read:\n%s", got)
	}
	eventually(t, 5*time.Second, "read acked the message", func() bool { return unreadOf(t, w, "a") == 0 })
}

// The who tool inside serve asks who is online when called (ADR 0066): the serving endpoint answers, the one that is
// not running does not, and nothing listens to heartbeats for it.
func TestServeWhoToolAsksNow(t *testing.T) {
	skipOnWindows(t)
	w := newWorld(t, "a", "b")
	c := startFakeClaude(t, strangerEnv(w, nil), true)
	eventually(t, 20*time.Second, "who shows a online", func() bool {
		out := c.call(t, "who", `{}`)
		return whoShows(out, tTo("a"), "online") && whoShows(out, tTo("b"), "offline")
	})
	if out := c.call(t, "who", `{"online":true}`); !strings.Contains(out, tTo("a")) || strings.Contains(out, tTo("b")) {
		t.Fatalf("who online:\n%s", out)
	}
}
