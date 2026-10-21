package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/randybias/mash/internal/admin"
	"github.com/randybias/mash/internal/testenv"
)

func envFrom(m map[string]string) func(string) string { return func(k string) string { return m[k] } }

// session runs `mash serve` over pipes and speaks MCP to it.
type session struct {
	in      *io.PipeWriter
	lines   chan map[string]any
	done    chan int
	backlog []map[string]any // lines read while waiting for something else
}

func startServe(t *testing.T, env map[string]string) *session {
	t.Helper()
	inR, inW := io.Pipe()
	outR, outW := io.Pipe()
	s := &session{in: inW, lines: make(chan map[string]any, 100), done: make(chan int, 1)}
	go func() { s.done <- run([]string{"serve"}, envFrom(env), inR, outW, io.Discard); outW.Close() }()
	go func() {
		sc := bufio.NewScanner(outR)
		sc.Buffer(make([]byte, 1<<20), 1<<24)
		for sc.Scan() {
			var m map[string]any
			if json.Unmarshal(sc.Bytes(), &m) == nil {
				s.lines <- m
			}
		}
		close(s.lines)
	}()
	t.Cleanup(func() {
		inW.Close()
		select {
		case <-s.done:
		case <-time.After(5 * time.Second):
			t.Error("serve did not exit when stdin closed")
		}
	})
	return s
}

func (s *session) send(t *testing.T, msg string) {
	t.Helper()
	if _, err := io.WriteString(s.in, msg+"\n"); err != nil {
		t.Fatal(err)
	}
}

// await returns the first line matching pred within d.
// Lines that don't match are kept for later awaits, not discarded.
func (s *session) await(t *testing.T, d time.Duration, pred func(map[string]any) bool) map[string]any {
	t.Helper()
	for i, m := range s.backlog {
		if pred(m) {
			s.backlog = append(s.backlog[:i], s.backlog[i+1:]...)
			return m
		}
	}
	deadline := time.After(d)
	for {
		select {
		case m, ok := <-s.lines:
			if !ok {
				t.Fatal("serve output closed")
			}
			if pred(m) {
				return m
			}
			s.backlog = append(s.backlog, m)
		case <-deadline:
			t.Fatal("timed out waiting for output")
		}
	}
}

func idIs(n float64) func(map[string]any) bool {
	return func(m map[string]any) bool { v, _ := m["id"].(float64); return v == n }
}

func isChannel(m map[string]any) bool { return m["method"] == "notifications/claude/channel" }

func (s *session) initialize(t *testing.T) map[string]any {
	s.send(t, `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18"}}`)
	r := s.await(t, 5*time.Second, idIs(1))
	s.send(t, `{"jsonrpc":"2.0","method":"notifications/initialized"}`)
	return r
}

func (s *session) tools(t *testing.T) []any {
	s.send(t, `{"jsonrpc":"2.0","id":2,"method":"tools/list"}`)
	return s.await(t, 5*time.Second, idIs(2))["result"].(map[string]any)["tools"].([]any)
}

func TestServeInertWithoutName(t *testing.T) {
	s := startServe(t, map[string]string{"HOME": t.TempDir()})
	r := s.initialize(t)
	if instr := r["result"].(map[string]any)["instructions"].(string); !strings.Contains(instr, "MASH_NAME") {
		t.Fatalf("instructions %q", instr)
	}
	if n := len(s.tools(t)); n != 0 {
		t.Fatalf("inert serve exposes %d tools", n)
	}
}

type world struct {
	e   *testenv.Env
	env map[string]map[string]string
}

func newWorld(t *testing.T, names ...string) *world {
	t.Helper()
	e := testenv.Start(t)
	adm, err := admin.New(e.Conn(t, e.AdminCreds), e.Owner)
	if err != nil {
		t.Fatal(err)
	}
	if err := adm.Init(context.Background()); err != nil {
		t.Fatal(err)
	}
	w := &world{e: e, env: map[string]map[string]string{}}
	for _, n := range names {
		c, u := e.Mint(t, n)
		if _, err := adm.Provision(context.Background(), n, u, e.Account, ""); err != nil {
			t.Fatal(err)
		}
		w.env[n] = map[string]string{"HOME": t.TempDir(), "MASH_NAME": n, "MASH_URL": e.URL,
			"MASH_OWNER": e.Owner, "MASH_CREDS": c, "MASH_SETTLE_MS": "20"}
	}
	return w
}

func (w *world) cli(t *testing.T, as string, args ...string) (string, int) {
	t.Helper()
	var out, errb bytes.Buffer
	code := run(args, envFrom(w.env[as]), strings.NewReader(""), &out, &errb)
	return out.String() + errb.String(), code
}

func TestServeEndToEnd(t *testing.T) {
	w := newWorld(t, "a", "b")
	s := startServe(t, w.env["a"])
	s.initialize(t)
	names := map[string]bool{}
	for _, tl := range s.tools(t) {
		names[tl.(map[string]any)["name"].(string)] = true
	}
	for _, n := range []string{"send", "read", "inbox", "who", "reply"} {
		if !names[n] {
			t.Fatalf("missing tool %s in %v", n, names)
		}
	}
	out, code := w.cli(t, "b", "send", "a", "hi", "from", "the", "cli")
	if code != 0 || !strings.Contains(out, "sent #1 from b to a") {
		t.Fatalf("cli send: %d %s", code, out)
	}
	ev := s.await(t, 5*time.Second, isChannel)
	content := ev["params"].(map[string]any)["content"].(string)
	if !strings.Contains(content, "from b [verified]") || !strings.Contains(content, "hi from the cli") {
		t.Fatalf("channel event: %s", content)
	}
	s.send(t, `{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"send","arguments":{"to":"b","text":"thanks b"}}}`)
	r := s.await(t, 5*time.Second, idIs(3))["result"].(map[string]any)
	if txt := r["content"].([]any)[0].(map[string]any)["text"].(string); !strings.HasPrefix(txt, "sent #") || r["isError"] == true {
		t.Fatalf("tool send: %v", r)
	}
	out, code = w.cli(t, "b", "inbox")
	if code != 0 || !strings.Contains(out, "from a [verified]") || !strings.Contains(out, "thanks b") {
		t.Fatalf("cli inbox: %d %s", code, out)
	}
	out, code = w.cli(t, "b", "read", "1")
	if code != 0 || !strings.Contains(out, "thanks b") {
		t.Fatalf("cli read: %d %s", code, out)
	}
	out, code = w.cli(t, "b", "who")
	if code != 0 || !strings.Contains(out, "a\tonline") {
		t.Fatalf("cli who: %d %s", code, out)
	}
}

func TestServeLivePromptViaCLI(t *testing.T) {
	w := newWorld(t, "a", "b")
	s := startServe(t, w.env["a"])
	s.initialize(t)
	s.tools(t)
	type res struct {
		out  string
		code int
	}
	done := make(chan res, 1)
	go func() { o, c := w.cli(t, "b", "prompt", "a", "what", "time?"); done <- res{o, c} }()
	ev := s.await(t, 5*time.Second, isChannel)
	p := ev["params"].(map[string]any)
	meta := p["meta"].(map[string]any)
	if !strings.Contains(p["content"].(string), "what time?") || meta["request_id"] == "" || meta["from"] != "b" {
		t.Fatalf("prompt event %v", p)
	}
	s.send(t, `{"jsonrpc":"2.0","id":4,"method":"tools/call","params":{"name":"reply","arguments":{"request_id":"`+meta["request_id"].(string)+`","text":"noon"}}}`)
	s.await(t, 5*time.Second, idIs(4))
	select {
	case r := <-done:
		if r.code != 0 || strings.TrimSpace(r.out) != "noon" {
			t.Fatalf("prompt output %d %q", r.code, r.out)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("prompt never finished")
	}
}

func TestServeLeaseConflict(t *testing.T) {
	w := newWorld(t, "a")
	first := startServe(t, w.env["a"])
	first.initialize(t)
	first.tools(t)
	second := startServe(t, w.env["a"])
	r := second.initialize(t)
	if instr := r["result"].(map[string]any)["instructions"].(string); !strings.Contains(instr, "already in use") {
		t.Fatalf("instructions %q", instr)
	}
	if n := len(second.tools(t)); n != 0 {
		t.Fatalf("a refused session exposes %d tools", n)
	}
	ev := second.await(t, 5*time.Second, isChannel)
	if !strings.Contains(ev["params"].(map[string]any)["content"].(string), "already in use") {
		t.Fatalf("event %v", ev)
	}
}

func TestAdminCLI(t *testing.T) {
	e := testenv.Start(t)
	c, u := e.Mint(t, "z")
	env := map[string]string{"HOME": t.TempDir(), "MASH_URL": e.URL, "MASH_OWNER": e.Owner, "MASH_ADMIN_CREDS": e.AdminCreds}
	runAdmin := func(args ...string) (string, int) {
		var out bytes.Buffer
		code := run(append([]string{"admin"}, args...), envFrom(env), strings.NewReader(""), &out, &out)
		return out.String(), code
	}
	if out, code := runAdmin("permissions", "z", "--nsc"); code != 0 || !strings.Contains(out, "--allow-pub\nagents.mail.*.o.*\n") {
		t.Fatalf("permissions: %d %s", code, out)
	}
	if out, code := runAdmin("permissions", "z"); code != 0 || !strings.Contains(out, `"pub"`) {
		t.Fatalf("permissions json: %d %s", code, out)
	}
	if out, code := runAdmin("init"); code != 0 {
		t.Fatalf("init: %s", out)
	}
	if out, code := runAdmin("provision", "z", "--user", u, "--account", e.Account, "--note", "zed"); code != 0 || !strings.Contains(out, "agents.mail.cc.o.z") {
		t.Fatalf("provision: %d %s", code, out)
	}
	if out, code := runAdmin("list"); code != 0 || !strings.Contains(out, "z\t") || !strings.Contains(out, "zed") {
		t.Fatalf("list: %d %s", code, out)
	}
	_ = c
	if out, code := runAdmin("deprovision", "z"); code != 0 {
		t.Fatalf("deprovision: %s", out)
	}
	if out, code := runAdmin("bogus"); code == 0 {
		t.Fatalf("unknown admin command accepted: %s", out)
	}
}

func TestUsage(t *testing.T) {
	var out bytes.Buffer
	if code := run(nil, envFrom(nil), strings.NewReader(""), &out, &out); code != 2 || !strings.Contains(out.String(), "mash serve") {
		t.Fatalf("usage: %d %s", code, out.String())
	}
	out.Reset()
	if code := run([]string{"version"}, envFrom(nil), strings.NewReader(""), &out, &out); code != 0 || !strings.Contains(out.String(), "mash ") {
		t.Fatalf("version: %d %s", code, out.String())
	}
}
