package main

import (
	"bufio"
	"encoding/json"
	"io"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/bogworks/mash/internal/testenv"
)

// The standalone tests below go past the CLI: a real `mash serve` process (the MCP server, an endpoint with no agent
// host), a live prompt answered by it, and hook delivery from its spool. Each runs the built binary with the scrubbed
// environment of standalone_test.go (empty PATH and HOME, no RANGE_/HERDR_/CLAUDE variables) against the embedded
// test NATS, and nothing else.

// standaloneWorld provisions the named endpoints through the built binary and returns each one's scrubbed environment.
func standaloneWorld(t *testing.T, bin string, names ...string) map[string][]string {
	t.Helper()
	e := testenv.Start(t)
	adminEnv := scrubbedEnv(t, map[string]string{"MASH_URL": e.URL, "MASH_OWNER": e.Owner, "MASH_ADMIN_CREDS": e.AdminCreds})
	mustRun(t, bin, adminEnv, "admin", "init")
	envs := map[string][]string{}
	for _, n := range names {
		creds, user, err := e.MintEndpoint(n)
		if err != nil {
			t.Fatal(err)
		}
		mustRun(t, bin, adminEnv, "admin", "provision", n, "--user", user, "--account", e.Account)
		envs[n] = scrubbedEnv(t, map[string]string{"MASH_URL": e.URL, "MASH_OWNER": e.Owner, "MASH_NAME": n,
			"MASH_CREDS": creds, "MASH_SETTLE_MS": "20"})
		assertToolsAbsent(t, envs[n])
	}
	return envs
}

// procServe is `mash serve` as a subprocess, spoken to over MCP on stdio.
type procServe struct {
	cmd   *exec.Cmd
	in    io.WriteCloser
	lines chan map[string]any
	back  []map[string]any
}

func startProcServe(t *testing.T, bin string, env []string) *procServe {
	t.Helper()
	cmd := exec.Command(bin, "serve")
	cmd.Env = env
	in, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	out, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	p := &procServe{cmd: cmd, in: in, lines: make(chan map[string]any, 100)}
	go func() {
		sc := bufio.NewScanner(out)
		sc.Buffer(make([]byte, 1<<20), 1<<24)
		for sc.Scan() {
			var m map[string]any
			if json.Unmarshal(sc.Bytes(), &m) == nil {
				p.lines <- m
			}
		}
		close(p.lines)
	}()
	t.Cleanup(func() {
		in.Close()
		done := make(chan struct{})
		go func() { _ = cmd.Wait(); close(done) }()
		select {
		case <-done:
		case <-time.After(10 * time.Second):
			_ = cmd.Process.Kill()
			<-done
			t.Error("mash serve did not exit when stdin closed")
		}
	})
	return p
}

func (p *procServe) send(t *testing.T, msg string) {
	t.Helper()
	if _, err := io.WriteString(p.in, msg+"\n"); err != nil {
		t.Fatal(err)
	}
}

func (p *procServe) await(t *testing.T, pred func(map[string]any) bool) map[string]any {
	t.Helper()
	for i, m := range p.back {
		if pred(m) {
			p.back = append(p.back[:i], p.back[i+1:]...)
			return m
		}
	}
	deadline := time.After(15 * time.Second)
	for {
		select {
		case m, ok := <-p.lines:
			if !ok {
				t.Fatal("serve output closed")
			}
			if pred(m) {
				return m
			}
			p.back = append(p.back, m)
		case <-deadline:
			t.Fatal("timed out waiting for serve output")
		}
	}
}

// start performs the MCP handshake and lists the tools.
func (p *procServe) start(t *testing.T) (instructions string, tools map[string]bool) {
	t.Helper()
	p.send(t, `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18"}}`)
	r := p.await(t, idIs(1))["result"].(map[string]any)
	p.send(t, `{"jsonrpc":"2.0","method":"notifications/initialized"}`)
	p.send(t, `{"jsonrpc":"2.0","id":2,"method":"tools/list"}`)
	tools = map[string]bool{}
	for _, tl := range p.await(t, idIs(2))["result"].(map[string]any)["tools"].([]any) {
		tools[tl.(map[string]any)["name"].(string)] = true
	}
	ins, _ := r["instructions"].(string)
	return ins, tools
}

func (p *procServe) call(t *testing.T, id int, name, args string) string {
	t.Helper()
	p.send(t, `{"jsonrpc":"2.0","id":`+itoa(id)+`,"method":"tools/call","params":{"name":"`+name+`","arguments":`+args+`}}`)
	return resultText(p.await(t, idIs(float64(id)))["result"].(map[string]any))
}

func channelOf(m map[string]any) (content string, meta map[string]any) {
	pr, _ := m["params"].(map[string]any)
	content, _ = pr["content"].(string)
	meta, _ = pr["meta"].(map[string]any)
	return
}

// waitOnline polls `mash who` until name is online.
func waitOnline(t *testing.T, bin string, env []string, name string) {
	t.Helper()
	eventually(t, 15*time.Second, name+" online", func() bool {
		out, _ := mashRun(t, bin, env, "", "who")
		return strings.Contains(out, name+"\tonline")
	})
}

// (a) serve as an endpoint with no agent host: it starts, answers MCP, receives a verified mail as a channel event,
// and sends mail through its tool.
func TestStandaloneServeWithoutAHost(t *testing.T) {
	bin := mashBinary(t)
	envs := standaloneWorld(t, bin, "alpha", "beta")
	s := startProcServe(t, bin, envs["beta"])
	ins, tools := s.start(t)
	if !strings.Contains(ins, `"beta"`) {
		t.Fatalf("instructions do not name the endpoint: %q", ins)
	}
	for _, n := range []string{"send", "read", "inbox", "who", "reply"} {
		if !tools[n] {
			t.Fatalf("missing tool %s in %v", n, tools)
		}
	}
	waitOnline(t, bin, envs["alpha"], "beta")

	mustRun(t, bin, envs["alpha"], "send", "beta", "hello", "serving", "beta")
	content, _ := channelOf(s.await(t, isChannel))
	if !strings.Contains(content, "from alpha [verified]") || !strings.Contains(content, "hello serving beta") {
		t.Fatalf("channel event: %q", content)
	}
	if out := s.call(t, 3, "send", `{"to":"alpha","text":"hello back"}`); !strings.HasPrefix(out, "sent #") {
		t.Fatalf("send tool: %q", out)
	}
	if out := mustRun(t, bin, envs["alpha"], "read", "1"); !strings.Contains(out, "hello back") || !strings.Contains(out, "[verified]") {
		t.Fatalf("alpha read: %s", out)
	}
}

// (b) a live prompt from one endpoint, answered by a serving endpoint: the SAP prompt round trip.
func TestStandaloneLivePrompt(t *testing.T) {
	bin := mashBinary(t)
	envs := standaloneWorld(t, bin, "alpha", "beta")
	s := startProcServe(t, bin, envs["beta"])
	s.start(t)
	waitOnline(t, bin, envs["alpha"], "beta")

	type res struct {
		out  string
		code int
	}
	done := make(chan res, 1)
	go func() {
		o, c := mashRun(t, bin, envs["alpha"], "", "prompt", "beta", "what", "time?")
		done <- res{o, c}
	}()
	content, meta := channelOf(s.await(t, isChannel))
	id, _ := meta["request_id"].(string)
	if !strings.Contains(content, "what time?") || id == "" || meta["from"] != "alpha" {
		t.Fatalf("prompt event: %q %v", content, meta)
	}
	if out := s.call(t, 4, "reply", `{"request_id":"`+id+`","text":"noon"}`); strings.Contains(out, "no open prompt") {
		t.Fatalf("reply: %q", out)
	}
	select {
	case r := <-done:
		if r.code != 0 || strings.TrimSpace(r.out) != "noon" {
			t.Fatalf("prompt output: exit %d %q", r.code, r.out)
		}
	case <-time.After(15 * time.Second):
		t.Fatal("prompt never answered")
	}
}

// (c) hook delivery with no host: serve makes the spool, mail sent to it arrives in `mash hook`'s output as "mash mail"
// context (hook stdin is the JSON the plugin hooks feed it), and it is shown once.
func TestStandaloneHookDelivery(t *testing.T) {
	bin := mashBinary(t)
	envs := standaloneWorld(t, bin, "alpha", "beta")
	s := startProcServe(t, bin, envs["beta"])
	s.start(t)
	waitOnline(t, bin, envs["alpha"], "beta")

	hook := func() string {
		out, code := mashRun(t, bin, envs["beta"], `{"hook_event_name":"PostToolUse","session_id":"S1"}`, "hook")
		if code != 0 {
			t.Fatalf("hook exited %d: %s", code, out)
		}
		return out
	}
	// The first hook switches the session to the spool; there is no mail yet.
	if got := hook(); strings.TrimSpace(got) != "" {
		t.Fatalf("an empty spool printed %q", got)
	}
	mustRun(t, bin, envs["alpha"], "send", "beta", "--kind", "question", "hello", "through", "the", "hook")
	var got string
	eventually(t, 15*time.Second, "the hook shows the mail", func() bool {
		got = hook()
		return strings.TrimSpace(got) != ""
	})
	var o struct {
		Specific struct {
			Event   string `json:"hookEventName"`
			Context string `json:"additionalContext"`
		} `json:"hookSpecificOutput"`
	}
	if err := json.Unmarshal([]byte(got), &o); err != nil {
		t.Fatalf("hook output %q: %v", got, err)
	}
	for _, want := range []string{"mash mail for beta", "1 message from alpha", "[verified]", "> hello through the hook"} {
		if !strings.Contains(o.Specific.Context, want) {
			t.Fatalf("hook context lacks %q:\n%s", want, o.Specific.Context)
		}
	}
	// Serve acks what the hook showed, and nothing is shown twice.
	eventually(t, 10*time.Second, "shown once", func() bool { return strings.TrimSpace(hook()) == "" })
}
