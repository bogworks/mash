package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// #16 through serve: with MASH_SPOOL_FROM_START=1 on the claude host, mail sent before any hook has run is spooled and
// stays unacked (Claude Code drops channel events that arrive before its first prompt), the idle wake is the summary
// line alone, and the first hook shows the body, after which serve acks it. Without the variable the same start is the
// channel's: TestHookDeliveryEndToEnd and the endpoint package's TestHookSwitchOverShowsEachMessageOnce.
func TestServeSpoolFromStartHoldsMailBeforeAnyHook(t *testing.T) {
	w := newWorld(t, "a", "b")
	w.env["a"]["MASH_IDLE_WAKE"] = "5s" // the config minimum
	w.env["a"]["MASH_SPOOL_FROM_START"] = "1"
	s := startServe(t, w.env["a"])
	s.initialize(t)
	s.tools(t)
	eventually(t, 5*time.Second, "a online", func() bool { out, _ := w.cli(t, "a", "who"); return strings.Contains(out, "a\tonline") })
	sp := spoolOf(w.env["a"])
	eventually(t, 5*time.Second, "serve made the spool", func() bool { _, err := os.Stat(sp.Dir); return err == nil })
	// No hook has run.
	if _, code := w.cli(t, "b", "send", "a", "--kind", "question", "sent during startup"); code != 0 {
		t.Fatal("send failed")
	}
	eventually(t, 5*time.Second, "serve spooled it", func() bool { e, _ := sp.Waiting(); return len(e) == 1 })
	if n := unreadOf(t, w, "a"); n != 1 {
		t.Fatalf("unread %d, want 1: nothing is acked before a hook shows it", n)
	}
	if ev := content(s.await(t, 15*time.Second, isChannel)); ev != "1 message from b (question)" {
		t.Fatalf("the only channel event is the wake, the summary line alone; got %q", ev)
	}
	if n := unreadOf(t, w, "a"); n != 1 {
		t.Fatalf("unread %d after the wake, want 1 (a wake is not a receipt)", n)
	}
	got := runHook(t, w.env["a"], "UserPromptSubmit")
	if !strings.Contains(got, "> sent during startup") {
		t.Fatalf("hook context lacks the body:\n%s", got)
	}
	eventually(t, 5*time.Second, "serve acks what the hook showed", func() bool { return unreadOf(t, w, "a") == 0 })
}

// v0.2.7 through serve: a fresh session that has run no hook gets its first mail with no kind (a task) as a wake at once,
// not after the idle wake (the 45 s default here), and the mail stays spooled and unacked.
func TestServeSpoolFromStartWakesAtOnceBeforeAnyHook(t *testing.T) {
	w := newWorld(t, "a", "b")
	w.env["a"]["MASH_SPOOL_FROM_START"] = "1" // MASH_IDLE_WAKE left at the 45 s default
	s := startServe(t, w.env["a"])
	s.initialize(t)
	s.tools(t)
	eventually(t, 5*time.Second, "a online", func() bool { out, _ := w.cli(t, "a", "who"); return strings.Contains(out, "a\tonline") })
	sp := spoolOf(w.env["a"])
	eventually(t, 5*time.Second, "serve made the spool", func() bool { _, err := os.Stat(sp.Dir); return err == nil })
	if _, code := w.cli(t, "b", "send", "a", "first mail"); code != 0 {
		t.Fatal("send failed")
	}
	if ev := content(s.await(t, 2*time.Second, isChannel)); ev != "1 message from b (task)" {
		t.Fatalf("the wake %q, want the summary line alone", ev)
	}
	if n := unreadOf(t, w, "a"); n != 1 {
		t.Fatalf("unread %d, want 1 (a wake is not a receipt)", n)
	}
}

// If serve cannot make the spool, MASH_SPOOL_FROM_START holds the mail: Claude drops early channel events but the
// channel acks, so the mail stays unacked in the inbox, and the model is told to use the inbox and read tools.
func TestServeSpoolFromStartNeedsASpool(t *testing.T) {
	w := newWorld(t, "a", "b")
	w.env["a"]["MASH_SPOOL_FROM_START"] = "1"
	// $HOME/.local/state is a file, so the spool directory cannot be made.
	if err := os.MkdirAll(filepath.Join(w.env["a"]["HOME"], ".local"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(w.env["a"]["HOME"], ".local", "state"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	s := startServe(t, w.env["a"])
	s.initialize(t)
	s.tools(t)
	eventually(t, 5*time.Second, "a online", func() bool { out, _ := w.cli(t, "a", "who"); return strings.Contains(out, "a\tonline") })
	if _, code := w.cli(t, "b", "send", "a", "must stay unacked"); code != 0 {
		t.Fatal("send failed")
	}
	notice := content(s.await(t, 15*time.Second, isChannel))
	if strings.Contains(notice, "must stay unacked") || !strings.Contains(notice, "use the inbox and read tools") {
		t.Fatalf("channel event %q, want the notice and no body", notice)
	}
	time.Sleep(3 * time.Second)
	if n := unreadOf(t, w, "a"); n != 1 {
		t.Fatalf("unread %d, want 1: the mail stays unacked", n)
	}
	if !strings.Contains(s.stderr(), "mash: spool: ") || !strings.Contains(s.stderr(), "mail is held in the inbox, unacked (MASH_SPOOL_FROM_START): use the inbox and read tools") {
		t.Fatalf("stderr lacks the line:\n%s", s.stderr())
	}
}
