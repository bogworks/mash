package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/bogworks/mash/internal/config"
	"github.com/bogworks/mash/internal/host"
	"github.com/bogworks/mash/internal/spool"
)

// initializeAs is initialize with the client's name, which is how serve recognises a host that sets no environment
// variable that reaches it.
func (s *session) initializeAs(t *testing.T, client string) {
	t.Helper()
	s.send(t, `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","clientInfo":{"name":"`+client+`","version":"0.166.0"}}}`)
	s.await(t, 5*time.Second, idIs(1))
	s.send(t, `{"jsonrpc":"2.0","method":"notifications/initialized"}`)
}

func statusLines(t *testing.T, env map[string]string) string {
	t.Helper()
	var out, errb bytes.Buffer
	if code := run([]string{"status"}, envFrom(env), strings.NewReader(""), &out, &errb); code != 0 {
		t.Fatalf("status exit %d: %s", code, errb.String())
	}
	return out.String()
}

func hostLineOf(out string) string {
	for _, l := range strings.Split(out, "\n") {
		if strings.HasPrefix(l, "host: ") {
			return l
		}
	}
	return ""
}

// `mash status` says which host it chose and how, and says so plainly when it only assumed one.
func TestStatusHostLine(t *testing.T) {
	for _, c := range []struct {
		name string
		env  map[string]string
		want string
	}{
		{"named", map[string]string{"MASH_HOST": "codex"}, "host: codex (MASH_HOST)"},
		{"pi marker", map[string]string{"PI_CODING_AGENT": "true"}, "host: pi (environment PI_CODING_AGENT)"},
		{"claude marker", map[string]string{"CLAUDE_CODE_SESSION_ID": "s"}, "host: claude (environment CLAUDE_CODE_SESSION_ID)"},
	} {
		t.Run(c.name, func(t *testing.T) {
			if got := hostLineOf(statusLines(t, hookEnv(t, c.env))); got != c.want {
				t.Fatalf("host line %q, want %q", got, c.want)
			}
		})
	}
	got := hostLineOf(statusLines(t, hookEnv(t, nil)))
	if !strings.HasPrefix(got, "host: claude (assumed: ") || !strings.Contains(got, "set MASH_HOST to one of claude, codex, headless, pi") {
		t.Fatalf("assumed host line %q", got)
	}
	// Codex does not get Claude Code's settings checks or words.
	out := statusLines(t, hookEnv(t, map[string]string{"MASH_HOST": "codex"}))
	if strings.Contains(out, "channel allowlist") || strings.Contains(out, "environment:") {
		t.Fatalf("Codex status carries Claude's checks:\n%s", out)
	}
}

// A hook that cannot see the MCP client follows the host the spool's owner record names, so it matches serve; with no
// host in the record it keeps the detected adapter, which is all Claude Code's hook ever does.
func TestAdapterFromOwner(t *testing.T) {
	env := hookEnv(t, nil)
	sp := spoolOf(env)
	if err := sp.Init(); err != nil {
		t.Fatal(err)
	}
	cfgOf := func() (a, b string) {
		det := mustDetect(t, "", env)
		got := adapterFromOwner(loadCfg(t, env), det.Adapter)
		return det.Adapter.Name(), got.Name()
	}
	for owner, want := range map[string]string{
		`{"pid":7,"host":"codex"}`:    "codex",
		`{"pid":7}`:                   "claude",
		`{"pid":7,"host":"nope"}`:     "claude",
		`{"pid":7,"host":"headless"}`: "claude",
		"pid:7":                       "claude",
	} {
		if err := os.WriteFile(filepath.Join(sp.Dir, ".owner"), []byte(owner), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, got := cfgOf(); got != want {
			t.Errorf("owner %s: adapter %s, want %s", owner, got, want)
		}
	}
	os.Remove(filepath.Join(sp.Dir, ".owner"))
	if _, got := cfgOf(); got != "claude" {
		t.Errorf("no owner: adapter %s", got)
	}
}

// Under Codex a hook of another thread on the shared app-server is refused by the session-id rule even though its
// ancestry matches the owner's pid, and the same input is delivered by the hook of the owner's own thread. The hook
// takes Codex's rules from the owner record, with no MASH_HOST in its environment.
func TestHookFollowsOwnerRecordHost(t *testing.T) {
	env := hookEnv(t, nil)
	sp := spoolOf(env)
	if err := sp.Init(); err != nil {
		t.Fatal(err)
	}
	// The owner is this test process's parent chain: every hook here descends from pid os.Getppid().
	owner := `{"pid":` + itoa(os.Getppid()) + `,"session":"thread-1","host":"codex"}`
	if err := os.WriteFile(filepath.Join(sp.Dir, ".owner"), []byte(owner), 0o600); err != nil {
		t.Fatal(err)
	}
	put := func(seq uint64) {
		if err := sp.Put(spool.Entry{Seq: seq, From: "b", Trust: "claimed", At: time.Now(), Body: []byte("m")}); err != nil {
			t.Fatal(err)
		}
	}
	runHook := func(session string) string {
		var out, errb bytes.Buffer
		run([]string{"hook"}, envFrom(env), strings.NewReader(`{"hook_event_name":"PostToolUse","session_id":"`+session+`"}`), &out, &errb)
		return out.String()
	}
	put(1)
	if got := runHook("thread-2"); got != "" {
		t.Fatalf("another thread's hook drained by ancestry:\n%s", got)
	}
	if w, _ := sp.Waiting(); len(w) != 1 {
		t.Fatalf("waiting %d", len(w))
	}
	if got := runHook("thread-1"); !strings.Contains(got, "additionalContext") || !strings.Contains(got, "1 message from b") {
		t.Fatalf("owner thread's hook got %q", got)
	}
	// The same record without a host is Claude's: ancestry accepts the other thread.
	if err := os.WriteFile(filepath.Join(sp.Dir, ".owner"), []byte(`{"pid":`+itoa(os.Getppid())+`,"session":"thread-1"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	put(2)
	if got := runHook("thread-2"); !strings.Contains(got, "additionalContext") {
		t.Fatalf("Claude's rules refused by ancestry: %q", got)
	}
}

// Serve settles an unnamed host from the client's name after initialize. With a Codex client it spools from the first
// message, records a Codex owner record, learns the thread id from the first tool call and writes it into the
// record, wakes with `codex queue --thread <id>` and a fixed message, and says nothing about an assumed host or about
// Claude Code's settings.
func TestServeSettlesCodexFromTheClient(t *testing.T) {
	w := newWorld(t, "a", "b")
	dir := t.TempDir()
	argv := filepath.Join(dir, "argv")
	bin := filepath.Join(dir, "codex")
	script := "#!/bin/sh\nfor a in \"$@\"; do printf '%s\\n' \"$a\"; done > '" + argv + ".tmp' && mv '" + argv + ".tmp' '" + argv + "'\n"
	if err := os.WriteFile(bin, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("MASH_CODEX_BIN", bin)
	t.Setenv("MASH_LAUNCH_TOKEN", "")
	s := startServe(t, w.env["a"])
	s.initializeAs(t, "codex-mcp-client")
	s.send(t, `{"jsonrpc":"2.0","id":2,"method":"tools/list"}`)
	s.await(t, 5*time.Second, idIs(2))
	sp := spoolOf(w.env["a"])
	var owner string
	eventually(t, 10*time.Second, "owner record", func() bool { owner = sp.Owner(); return strings.Contains(owner, `"host":"codex"`) })
	if strings.Contains(owner, `"session"`) {
		t.Fatalf("session recorded before any tool call: %s", owner)
	}
	// No thread id yet: mail is spooled and nothing is run.
	if out, code := w.cli(t, "b", "send", "a", "--urgent", "hello codex"); code != 0 {
		t.Fatalf("send: %s", out)
	}
	eventually(t, 10*time.Second, "mail spooled", func() bool { e, _ := sp.Waiting(); return len(e) == 1 })
	time.Sleep(300 * time.Millisecond)
	if _, err := os.Stat(argv); err == nil {
		t.Fatal("codex queue ran with no thread id")
	}
	// The first tool call carries the thread id (Codex's tools/call _meta): serve records it and wakes.
	s.send(t, `{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"version","arguments":{},"_meta":{"threadId":"019aaaaa-thread"}}}`)
	s.await(t, 5*time.Second, idIs(3))
	eventually(t, 10*time.Second, "thread id in the owner record", func() bool {
		return strings.Contains(sp.Owner(), `"session":"019aaaaa-thread"`) && strings.Contains(sp.Owner(), `"host":"codex"`)
	})
	if out, code := w.cli(t, "b", "send", "a", "--urgent", "second SECRET-BODY"); code != 0 {
		t.Fatalf("send: %s", out)
	}
	eventually(t, 15*time.Second, "codex queue run", func() bool { _, err := os.Stat(argv); return err == nil })
	b, _ := os.ReadFile(argv)
	lines := strings.Split(strings.TrimSpace(string(b)), "\n")
	if len(lines) != 5 || lines[0] != "queue" || lines[1] != "--thread" || lines[2] != "019aaaaa-thread" || lines[3] != "--message" {
		t.Fatalf("argv %q", lines)
	}
	if strings.Contains(string(b), "SECRET-BODY") || strings.Contains(string(b), "hello codex") || strings.Contains(string(b), "from ") {
		t.Fatalf("mail text reached the argument list: %s", b)
	}
	// The owner's own hook (same thread id, no launch token) is accepted; nothing but the settled host was said.
	errs := s.stderr()
	if strings.Contains(errs, "assumed") || strings.Contains(errs, "allowlist") || strings.Contains(errs, "environment:") {
		t.Fatalf("serve stderr:\n%s", errs)
	}
	var out, errb bytes.Buffer
	run([]string{"hook"}, envFrom(w.env["a"]), strings.NewReader(`{"hook_event_name":"UserPromptSubmit","session_id":"019aaaaa-thread"}`), &out, &errb)
	if !strings.Contains(out.String(), "hello codex") {
		t.Fatalf("the thread's hook got %q (stderr %s)", out.String(), errb.String())
	}
	// status inside the session reports the settled host and how.
	st := s.call(t, 4, "status", `{}`)
	text := st["content"].([]any)[0].(map[string]any)["text"].(string)
	if got := hostLineOf(text); got != "host: codex (MCP client codex-mcp-client)" {
		t.Fatalf("status host line %q in:\n%s", got, text)
	}
}

// A client mash does not recognise, with nothing else to go by, leaves Claude Code assumed, and serve says so once.
func TestServeSaysItAssumedTheHost(t *testing.T) {
	w := newWorld(t, "a")
	s := startServe(t, w.env["a"])
	s.initializeAs(t, "some-other-client")
	eventually(t, 10*time.Second, "the assumed line", func() bool { return strings.Contains(s.stderr(), "mash: host assumed to be claude") })
	if n := strings.Count(s.stderr(), "host assumed"); n != 1 || !strings.Contains(s.stderr(), "MASH_HOST") {
		t.Fatalf("stderr:\n%s", s.stderr())
	}
	st := s.call(t, 4, "status", `{}`)
	text := st["content"].([]any)[0].(map[string]any)["text"].(string)
	if got := hostLineOf(text); !strings.HasPrefix(got, "host: claude (assumed: ") || !strings.Contains(got, "some-other-client") {
		t.Fatalf("status host line %q", got)
	}
}

// Claude Code, recognised by its client name or by its environment, gets no extra stderr line.
func TestServeClaudeSaysNothingExtra(t *testing.T) {
	for name, c := range map[string]struct {
		client string
		env    map[string]string
	}{
		"client name": {client: "claude-code"},
		"environment": {client: "some-other-client", env: map[string]string{"CLAUDE_CODE_SESSION_ID": "s-1"}},
	} {
		t.Run(name, func(t *testing.T) {
			w := newWorld(t, "a")
			env := map[string]string{}
			for k, v := range w.env["a"] {
				env[k] = v
			}
			for k, v := range c.env {
				env[k] = v
			}
			s := startServe(t, env)
			s.initializeAs(t, c.client)
			s.send(t, `{"jsonrpc":"2.0","id":2,"method":"tools/list"}`)
			s.await(t, 5*time.Second, idIs(2))
			time.Sleep(time.Second)
			if strings.Contains(s.stderr(), "assumed") {
				t.Fatalf("stderr:\n%s", s.stderr())
			}
		})
	}
}

func loadCfg(t *testing.T, env map[string]string) config.Config {
	t.Helper()
	cfg, err := config.Load(config.LookupFromFunc(envFrom(env)), env["HOME"])
	if err != nil {
		t.Fatal(err)
	}
	return cfg
}

func mustDetect(t *testing.T, name string, env map[string]string) host.Detection {
	t.Helper()
	d, err := host.Detect(name, envFrom(env))
	if err != nil {
		t.Fatal(err)
	}
	return d
}

// Codex learns its thread id late. When the first spool bind fails under MASH_SPOOL_REQUIRED and the background retry
// binds it later, thread tracking starts then too: a thread id that arrived before the bind reaches the owner record.
func TestServeCodexRetryBindRecordsThreadID(t *testing.T) {
	old := spoolRetryEvery
	spoolRetryEvery = 100 * time.Millisecond
	t.Cleanup(func() { spoolRetryEvery = old }) // runs after startServe's cleanup has stopped serve
	w := newWorld(t, "a")
	w.env["a"]["MASH_SPOOL_REQUIRED"] = "1"
	state := filepath.Join(t.TempDir(), "state")
	w.env["a"]["MASH_STATE_DIR"] = state
	if err := os.WriteFile(state, nil, 0o600); err != nil { // no spool directory can be made
		t.Fatal(err)
	}
	t.Setenv("MASH_LAUNCH_TOKEN", "")
	s := startServe(t, w.env["a"])
	s.initializeAs(t, "codex-mcp-client")
	s.send(t, `{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"version","arguments":{},"_meta":{"threadId":"019bbbbb-thread"}}}`)
	s.await(t, 5*time.Second, idIs(3))
	// The first bind has failed before the spool directory is fixed, or this would test the initial bind.
	eventually(t, 10*time.Second, "the failed bind is reported", func() bool { return strings.Contains(s.stderr(), "the spool is failing") })
	if err := os.Rename(state, state+".was"); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(state, 0o700); err != nil {
		t.Fatal(err)
	}
	sp := spool.Spool{Dir: spool.Dir(state, w.env["a"]["MASH_OWNER"], w.env["a"]["MASH_NAME"])}
	eventually(t, 15*time.Second, "thread id in the owner record after the retry bind", func() bool {
		o := sp.Owner()
		return strings.Contains(o, `"session":"019bbbbb-thread"`) && strings.Contains(o, `"host":"codex"`)
	})
}
