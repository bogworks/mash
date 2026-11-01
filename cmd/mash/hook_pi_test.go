package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/bogworks/mash/internal/spool"
)

// piEnv is a hook-only environment for the Pi adapter (no server is reached).
func piEnv(t *testing.T) map[string]string {
	t.Helper()
	return map[string]string{"HOME": t.TempDir(), "MASH_NAME": "a", "MASH_OWNER": "o", "MASH_URL": "nats://127.0.0.1:1", "MASH_CREDS": "/none", "MASH_HOST": "pi"}
}

// piOut is what `mash hook` printed under Pi.
type piOut struct {
	Text   string   `json:"text"`
	Seqs   []uint64 `json:"seqs"`
	Urgent bool     `json:"urgent"`
}

// runPiHook runs `mash hook` with the Pi adapter for event; a zero piOut means it printed nothing.
func runPiHook(t *testing.T, env map[string]string, event string) piOut {
	t.Helper()
	var out, errb bytes.Buffer
	if code := run([]string{"hook"}, envFrom(env), strings.NewReader(`{"event":"`+event+`"}`), &out, &errb); code != 0 {
		t.Fatalf("hook exited %d: %s", code, errb.String())
	}
	var o piOut
	if out.Len() == 0 {
		return o
	}
	if err := json.Unmarshal(out.Bytes(), &o); err != nil {
		t.Fatalf("hook output %q: %v", out.String(), err)
	}
	return o
}

// Pi end to end: serve with the Pi adapter spools from its first message and wakes through a wake file, never the
// channel; the hook shows the mail as JSON, after which serve acks it.
func TestPiHookDeliveryEndToEnd(t *testing.T) {
	w := newWorld(t, "a", "b")
	w.env["a"]["MASH_HOST"] = "pi"
	w.env["a"]["MASH_IDLE_WAKE"] = "5s"
	s := startServe(t, w.env["a"])
	s.initialize(t)
	s.tools(t)
	eventually(t, 5*time.Second, "a online", func() bool { out, _ := w.cli(t, "a", "who"); return strings.Contains(out, "a\tonline") })
	sp := spoolOf(w.env["a"])
	wakes := filepath.Join(sp.Dir, "wakes")
	if _, code := w.cli(t, "b", "send", "a", "--kind", "question", "hello through pi"); code != 0 {
		t.Fatal("send failed")
	}
	var wakeFile string
	eventually(t, 15*time.Second, "a wake file", func() bool {
		m, _ := filepath.Glob(filepath.Join(wakes, "*.json"))
		if len(m) > 0 {
			wakeFile = m[0]
		}
		return len(m) > 0
	})
	raw, err := os.ReadFile(wakeFile)
	if err != nil {
		t.Fatal(err)
	}
	var wake struct {
		At   string `json:"at"`
		Text string `json:"text"`
	}
	if err := json.Unmarshal(raw, &wake); err != nil || wake.At == "" || !strings.Contains(wake.Text, "1 message from b") {
		t.Fatalf("wake %s: %v", raw, err)
	}
	if n := unreadOf(t, w, "a"); n != 1 {
		t.Fatalf("serve acked mail no hook drained: %d unread", n)
	}
	got := runPiHook(t, w.env["a"], "wake")
	if len(got.Seqs) != 1 || got.Urgent || !strings.Contains(got.Text, "> hello through pi") || !strings.Contains(got.Text, "[verified]") {
		t.Fatalf("hook output %+v", got)
	}
	eventually(t, 5*time.Second, "serve acks what the hook showed", func() bool { return unreadOf(t, w, "a") == 0 })
	if again := runPiHook(t, w.env["a"], "tool"); len(again.Seqs) != 0 {
		t.Fatalf("shown twice: %+v", again)
	}
}

// The init event prints the wake directory and touches nothing: the mail stays waiting and the session is not marked
// hooked.
func TestPiInitDrainsNothing(t *testing.T) {
	stubAncestors(t, 111)
	env := piEnv(t)
	sp := spoolOf(env)
	if err := sp.Init(); err != nil {
		t.Fatal(err)
	}
	if err := sp.Own("pid:111"); err != nil {
		t.Fatal(err)
	}
	if err := sp.Put(spool.Entry{Seq: 1, From: "b", Trust: "claimed", At: time.Now(), Body: []byte("held")}); err != nil {
		t.Fatal(err)
	}
	list := func() []string {
		var names []string
		_ = filepath.WalkDir(sp.Dir, func(p string, d os.DirEntry, err error) error { names = append(names, p); return nil })
		return names
	}
	before := list()
	var out, errb bytes.Buffer
	if code := run([]string{"hook"}, envFrom(env), strings.NewReader(`{"event":"init"}`), &out, &errb); code != 0 {
		t.Fatalf("exit %d: %s", code, errb.String())
	}
	var o map[string]string
	if err := json.Unmarshal(out.Bytes(), &o); err != nil || len(o) != 1 || o["wake_dir"] != filepath.Join(sp.Dir, "wakes") || !filepath.IsAbs(o["wake_dir"]) {
		t.Fatalf("init printed %q (want wake_dir %s)", out.String(), filepath.Join(sp.Dir, "wakes"))
	}
	if w, _ := sp.Waiting(); len(w) != 1 || strings.Join(list(), "\n") != strings.Join(before, "\n") {
		t.Fatalf("init touched the spool: waiting %+v, before %v after %v", w, before, list())
	}
	if _, err := os.Stat(filepath.Join(sp.Dir, ".hooked")); err == nil {
		t.Fatal("init marked the session hooked")
	}
}

// Init needs no spool at all: it answers before the lock and creates nothing.
func TestPiInitWithoutASpool(t *testing.T) {
	env := piEnv(t)
	var out bytes.Buffer
	if code := run([]string{"hook"}, envFrom(env), strings.NewReader(`{"event":"init"}`), &out, &bytes.Buffer{}); code != 0 || !strings.Contains(out.String(), `"wake_dir"`) {
		t.Fatalf("exit %d out %q", code, out.String())
	}
	if _, err := os.Stat(spoolOf(env).Dir); err == nil {
		t.Fatal("init created the spool")
	}
}

// Only the owning Pi process drains (invariant 4).
func TestPiHookIgnoresAnotherProcessesSpool(t *testing.T) {
	stubAncestors(t, 111, 222)
	env := piEnv(t)
	sp := spoolOf(env)
	if err := sp.Init(); err != nil {
		t.Fatal(err)
	}
	if err := sp.Own("pid:999"); err != nil {
		t.Fatal(err)
	}
	if err := sp.Put(spool.Entry{Seq: 1, From: "b", Trust: "claimed", At: time.Now(), Body: []byte("secret")}); err != nil {
		t.Fatal(err)
	}
	if got := runPiHook(t, env, "tool"); got.Text != "" {
		t.Fatalf("shown: %+v", got)
	}
	if w, _ := sp.Waiting(); len(w) != 1 {
		t.Fatalf("drained: %+v", w)
	}
	if _, err := os.Stat(filepath.Join(sp.Dir, ".hooked")); err == nil {
		t.Fatal("touched .hooked")
	}
	// And the owner's descendant drains it.
	stubAncestors(t, 111, 999)
	if got := runPiHook(t, env, "tool"); !strings.Contains(got.Text, "> secret") {
		t.Fatalf("owner's hook: %+v", got)
	}
}
