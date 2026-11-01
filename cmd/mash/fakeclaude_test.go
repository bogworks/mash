package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

// A fake "claude" for the process-chain tests: this test binary, re-run with MASH_TEST_FAKE_CLAUDE=<mash binary>. Like
// Claude Code it is the parent of a real `mash serve` (when MASH_TEST_FAKE_SERVE=1) and of the hooks it runs, each
// as `sh -c 'mash hook'`, so the chain a hook walks is the real one (hook -> sh -> claude) with nothing stubbed. It is
// driven over its stdin and stdout: the line "hook <session_id>" runs one hook and answers one JSON line
// {"stdout","stderr","code"}; "call <tool> <json args>" runs a tools/call on serve and answers {"result": <text>,
// "error": <bool>}; "notes" answers {"notes": [<text of every channel notification serve sent so far>]}. Closing its
// stdin ends it, serve first.
func fakeClaudeMain() {
	bin := os.Getenv("MASH_TEST_FAKE_CLAUDE")
	var serveIn io.WriteCloser
	var serve *exec.Cmd
	var notesMu sync.Mutex
	var notes []string
	responses := make(chan map[string]any, 64)
	if m := os.Getenv("MASH_TEST_FAKE_SERVE"); m == "1" || m == "wrapper" {
		serve = exec.Command(bin, "serve")
		if m == "wrapper" {
			// `; :` keeps sh from exec'ing serve, so serve's parent is a shell, not claude.
			serve = exec.Command("sh", "-c", `"$MASH_TEST_FAKE_CLAUDE" serve; :`)
		}
		serveIn, _ = serve.StdinPipe()
		out, _ := serve.StdoutPipe()
		go func() { // serve's stdout: channel notifications are kept, answers go to whoever called
			sc := bufio.NewScanner(out)
			sc.Buffer(make([]byte, 1<<20), 1<<24)
			for sc.Scan() {
				var m map[string]any
				if json.Unmarshal(sc.Bytes(), &m) != nil {
					continue
				}
				if m["method"] == "notifications/claude/channel" {
					p, _ := m["params"].(map[string]any)
					txt, _ := p["content"].(string)
					notesMu.Lock()
					notes = append(notes, txt)
					notesMu.Unlock()
					continue
				}
				if _, ok := m["id"]; ok {
					select {
					case responses <- m:
					default:
					}
				}
			}
		}()
		// MASH_TEST_SERVE_LOG: where serve's stderr goes (appended), with its pid in <log>.pid, so a test can wait on
		// a line of it and SIGKILL it.
		logPath := os.Getenv("MASH_TEST_SERVE_LOG")
		if logPath != "" {
			lf, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
			if err != nil {
				os.Exit(2)
			}
			defer lf.Close()
			serve.Stderr = lf
		}
		if err := serve.Start(); err != nil {
			os.Exit(2)
		}
		if logPath != "" {
			_ = os.WriteFile(logPath+".pid", []byte(strconv.Itoa(serve.Process.Pid)), 0o600)
		}
		for _, m := range []string{
			`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18"}}`,
			`{"jsonrpc":"2.0","method":"notifications/initialized"}`,
			`{"jsonrpc":"2.0","id":2,"method":"tools/list"}`,
		} {
			io.WriteString(serveIn, m+"\n")
		}
	}
	reply := func(v map[string]any) {
		b, _ := json.Marshal(v)
		os.Stdout.Write(append(b, '\n'))
	}
	callID := 100
	sc := bufio.NewScanner(os.Stdin)
	sc.Buffer(make([]byte, 1<<20), 1<<24)
	for sc.Scan() {
		if sc.Text() == "notes" {
			notesMu.Lock()
			reply(map[string]any{"notes": append([]string{}, notes...)})
			notesMu.Unlock()
			continue
		}
		if rest, ok := strings.CutPrefix(sc.Text(), "call "); ok {
			tool, args, _ := strings.Cut(rest, " ")
			callID++
			if serve == nil {
				reply(map[string]any{"result": "no serve", "error": true})
				continue
			}
			msg, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": callID, "method": "tools/call",
				"params": map[string]any{"name": tool, "arguments": json.RawMessage(args)}})
			io.WriteString(serveIn, string(msg)+"\n")
			timeout := time.After(15 * time.Second)
		wait:
			for {
				select {
				case m := <-responses:
					if id, _ := m["id"].(float64); int(id) != callID {
						continue
					}
					r, _ := m["result"].(map[string]any)
					txt := ""
					if c, _ := r["content"].([]any); len(c) > 0 {
						txt, _ = c[0].(map[string]any)["text"].(string)
					}
					isErr, _ := r["isError"].(bool)
					reply(map[string]any{"result": txt, "error": isErr || r == nil})
					break wait
				case <-timeout:
					reply(map[string]any{"result": "timeout", "error": true})
					break wait
				}
			}
			continue
		}
		session, ok := strings.CutPrefix(sc.Text(), "hook ")
		if !ok {
			continue
		}
		// `; exit` keeps sh from exec'ing the hook, so there is a real shell between claude and mash.
		cmd := exec.Command("sh", "-c", `"$MASH_TEST_FAKE_CLAUDE" hook; rc=$?; exit $rc`)
		cmd.Stdin = strings.NewReader(`{"hook_event_name":"PostToolUse","session_id":"` + session + `"}`)
		var out, errb bytes.Buffer
		cmd.Stdout, cmd.Stderr = &out, &errb
		code := 0
		if err := cmd.Run(); err != nil {
			code = 1
			if ee, ok := err.(*exec.ExitError); ok {
				code = ee.ExitCode()
			}
		}
		b, _ := json.Marshal(map[string]any{"stdout": out.String(), "stderr": errb.String(), "code": code})
		os.Stdout.Write(append(b, '\n'))
	}
	if serve != nil {
		serveIn.Close()
		serve.Wait()
	}
}

// fakeClaude is a running fake claude process, a child of the test.
type fakeClaude struct {
	pid   int
	cmd   *exec.Cmd
	in    io.WriteCloser
	lines chan string
}

// startFakeClaude runs a fake claude with env (the mash environment of the endpoint), and a real serve under it if
// withServe.
func startFakeClaude(t *testing.T, env map[string]string, withServe bool) *fakeClaude {
	t.Helper()
	return startFakeClaudeMode(t, env, map[bool]string{true: "1"}[withServe])
}

// startFakeClaudeMode is startFakeClaude with serve mode "" (none), "1" (serve is claude's child) or "wrapper" (serve
// is the child of a shell that does not exec it).
func startFakeClaudeMode(t *testing.T, env map[string]string, serveMode string) *fakeClaude {
	t.Helper()
	cmd := exec.Command(os.Args[0])
	cmd.Env = []string{"PATH=" + os.Getenv("PATH"), "MASH_TEST_FAKE_CLAUDE=" + mashBinary(t)}
	if serveMode != "" {
		cmd.Env = append(cmd.Env, "MASH_TEST_FAKE_SERVE="+serveMode)
	}
	for k, v := range env {
		cmd.Env = append(cmd.Env, k+"="+v)
	}
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
	f := &fakeClaude{pid: cmd.Process.Pid, cmd: cmd, in: in, lines: make(chan string, 16)}
	go func() {
		sc := bufio.NewScanner(out)
		sc.Buffer(make([]byte, 1<<20), 1<<24)
		for sc.Scan() {
			f.lines <- sc.Text()
		}
		close(f.lines)
	}()
	t.Cleanup(f.stop)
	return f
}

// hook runs `sh -c 'mash hook'` under the fake claude as a session_id and returns the additionalContext it printed.
func (f *fakeClaude) hook(t *testing.T, session string) string {
	t.Helper()
	stdout, _ := f.hookRaw(t, session)
	return hookContext(t, []byte(stdout))
}

// hookRaw is hook, returning the hook's raw stdout and stderr.
func (f *fakeClaude) hookRaw(t *testing.T, session string) (stdout, stderr string) {
	t.Helper()
	if _, err := io.WriteString(f.in, "hook "+session+"\n"); err != nil {
		t.Fatal(err)
	}
	select {
	case l, ok := <-f.lines:
		if !ok {
			t.Fatal("the fake claude exited")
		}
		var r struct {
			Stdout, Stderr string
			Code           int
		}
		if err := json.Unmarshal([]byte(l), &r); err != nil || r.Code != 0 {
			t.Fatalf("hook reply %q (%v)", l, err)
		}
		return r.Stdout, r.Stderr
	case <-time.After(20 * time.Second):
		t.Fatal("no hook reply")
	}
	return "", ""
}

// kill ends the fake claude with SIGKILL: its serve sees stdin close and ends too.
func (f *fakeClaude) kill() {
	f.cmd.Process.Kill()
	f.cmd.Wait()
}

// stop ends the fake claude gracefully (its stdin closes, serve first); a no-op once killed.
func (f *fakeClaude) stop() {
	f.in.Close()
	done := make(chan struct{})
	go func() { f.cmd.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		f.cmd.Process.Kill()
		<-done
	}
}

// call runs the tools/call tool with the JSON args on the fake claude's serve and returns the text of the result.
func (f *fakeClaude) call(t *testing.T, tool, args string) string {
	t.Helper()
	if _, err := io.WriteString(f.in, "call "+tool+" "+args+"\n"); err != nil {
		t.Fatal(err)
	}
	l := f.reply(t)
	var r struct {
		Result string
		Error  bool
	}
	if err := json.Unmarshal([]byte(l), &r); err != nil || r.Error {
		t.Fatalf("call %s: %q (%v)", tool, l, err)
	}
	return r.Result
}

// notifications is the text of every channel notification the fake claude's serve has sent so far.
func (f *fakeClaude) notifications(t *testing.T) []string {
	t.Helper()
	if _, err := io.WriteString(f.in, "notes\n"); err != nil {
		t.Fatal(err)
	}
	var r struct{ Notes []string }
	if err := json.Unmarshal([]byte(f.reply(t)), &r); err != nil {
		t.Fatal(err)
	}
	return r.Notes
}

func (f *fakeClaude) reply(t *testing.T) string {
	t.Helper()
	select {
	case l, ok := <-f.lines:
		if !ok {
			t.Fatal("the fake claude exited")
		}
		return l
	case <-time.After(30 * time.Second):
		t.Fatal("no reply from the fake claude")
	}
	return ""
}
