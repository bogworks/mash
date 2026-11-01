package main

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/bogworks/mash/internal/spool"
)

// D1 of the live tier (opt-in: MASH_LIVE_CLAUDE=1): what happens when two live claude processes run on the SAME
// session id, with the same MASH_NAME, credentials and XDG_STATE_HOME, and the plugin installed. Process A starts a
// fresh session; process B is started with `--resume <A's session id>` (no --fork-session). The experiment observes and
// RECORDS (ls.info) how the second process's MCP server connects, which process receives mail, how the session
// transcript looks after both wrote to it, and what happens to B when the lease holder A is killed. Only harness
// sanity is asserted (A produced a session id). Nothing from the streams or the transcript is written anywhere except
// scrubbed ls rows.
//
// Run: MASH_LIVE_CLAUDE=1 MASH_LIVE_CLAUDE_CONFIG_DIR=<seat> go test ./cmd/mash -run TestLiveDualResumeD1 -v -timeout 20m
//
// One overall context of lsDualTO bounds the whole run (the 5 minute lsScenarioTO is too short for this flow); every
// wait has its own timeout.

const lsDualTO = 15 * time.Minute

// lsLockedBuf is an io.Writer safe for a child's stderr that is read while the child runs.
type lsLockedBuf struct {
	mu sync.Mutex
	b  strings.Builder
}

func (l *lsLockedBuf) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.Write(p)
}

func (l *lsLockedBuf) String() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.String()
}

// lsLive is one long-lived `claude -p --input-format stream-json` process fed turn by turn.
type lsLive struct {
	name   string
	cmd    interface{ Wait() error }
	pid    int
	stdin  io.WriteCloser
	stderr *lsLockedBuf
	done   chan struct{}

	mu       sync.Mutex
	st       lsStream
	sid      string
	tools    map[string]string // tool_use id -> name, for the who result
	whoText  string
	exitErr  error
	exitCode int
}

func (sc *lsScenario) startLive(ctx context.Context, name string, env map[string]string, extra ...string) *lsLive {
	t := sc.t
	t.Helper()
	args := append([]string{"-p", "--model", "haiku", "--input-format", "stream-json", "--output-format", "stream-json",
		"--verbose", "--include-hook-events", "--allowedTools", "Bash(sleep:*)," + lsStatusTool + "," + lsWhoTool}, extra...)
	cmd := sc.command(ctx, env, args...)
	in, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	out, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	p := &lsLive{name: name, cmd: cmd, stdin: in, stderr: &lsLockedBuf{}, done: make(chan struct{}), tools: map[string]string{}}
	cmd.Stderr = p.stderr
	if err := cmd.Start(); err != nil {
		t.Fatalf("start claude %s: %v", name, err)
	}
	p.pid = cmd.Process.Pid
	t.Cleanup(p.kill)
	go func() {
		s := bufio.NewScanner(out)
		s.Buffer(make([]byte, 1<<20), 64<<20)
		for s.Scan() {
			p.feedLine(s.Text())
		}
		err := cmd.Wait()
		p.mu.Lock()
		p.exitErr = err
		p.exitCode = cmd.ProcessState.ExitCode()
		p.mu.Unlock()
		close(p.done)
	}()
	return p
}

func (p *lsLive) feedLine(line string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.st.feed(line)
	var m map[string]any
	if json.Unmarshal([]byte(line), &m) != nil {
		return
	}
	if sid, _ := m["session_id"].(string); sid != "" && p.sid == "" {
		p.sid = sid
	}
	switch m["type"] {
	case "assistant":
		msg, _ := m["message"].(map[string]any)
		content, _ := msg["content"].([]any)
		for _, c := range content {
			if cm, ok := c.(map[string]any); ok && cm["type"] == "tool_use" {
				id, _ := cm["id"].(string)
				name, _ := cm["name"].(string)
				p.tools[id] = name
			}
		}
	case "user":
		msg, _ := m["message"].(map[string]any)
		content, _ := msg["content"].([]any)
		for _, c := range content {
			cm, ok := c.(map[string]any)
			if !ok || cm["type"] != "tool_result" {
				continue
			}
			id, _ := cm["tool_use_id"].(string)
			if strings.HasSuffix(p.tools[id], "__who") {
				p.whoText = lsText(cm["content"])
			}
		}
	}
}

// send writes one user turn.
func (p *lsLive) send(text string) error {
	b, err := json.Marshal(map[string]any{"type": "user", "message": map[string]any{"role": "user", "content": text}})
	if err != nil {
		return err
	}
	_, err = p.stdin.Write(append(b, '\n'))
	return err
}

// waitResults waits until n results arrived, the process exited or the timeout passed; it reports whether n arrived.
func (p *lsLive) waitResults(n int, timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if p.snapshot().results >= n {
			return true
		}
		select {
		case <-p.done:
			return p.snapshot().results >= n
		case <-time.After(250 * time.Millisecond):
		}
	}
	return p.snapshot().results >= n
}

// snapshot is a copy of the stream state (the slices are shared but only ever appended to).
func (p *lsLive) snapshot() lsStream {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.st
}

func (p *lsLive) sessionID() string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.sid
}

func (p *lsLive) who() string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.whoText
}

func (p *lsLive) closeStdin() { p.stdin.Close() }

// kill SIGKILLs the process group (serve and hooks go too).
func (p *lsLive) kill() { _ = syscall.Kill(-p.pid, syscall.SIGKILL) }

func (p *lsLive) exited() <-chan struct{} { return p.done }

func (p *lsLive) isExited() bool {
	select {
	case <-p.done:
		return true
	default:
		return false
	}
}

// stderrMash is the stderr lines that mention mash, lease or bind.
func (p *lsLive) stderrMash() string {
	var out []string
	for _, l := range strings.Split(p.stderr.String(), "\n") {
		ll := strings.ToLower(l)
		if strings.Contains(ll, "mash") || strings.Contains(ll, "lease") || strings.Contains(ll, "bind") {
			out = append(out, l)
		}
	}
	return strings.Join(out, " / ")
}

func (p *lsLive) stderrTail(n int) string {
	l := strings.Split(strings.TrimSpace(p.stderr.String()), "\n")
	if len(l) > n {
		l = l[len(l)-n:]
	}
	return strings.Join(l, " / ")
}

// ---- transcript ----

// lsTranscript summarises the session's JSONL transcript: structure only, never content.
func lsTranscript(cfg, sid string) (summary, others string) {
	files, _ := filepath.Glob(filepath.Join(cfg, "projects", "*", sid+".jsonl"))
	if len(files) == 0 {
		return "no transcript file found for the session id", ""
	}
	b, err := os.ReadFile(files[0])
	if err != nil {
		return fmt.Sprintf("read: %v", err), ""
	}
	var lines, bad, roots int
	uuids := map[string]bool{}
	parentRefs := map[string]int{}
	perSession := map[string]int{}
	for _, l := range strings.Split(string(b), "\n") {
		if strings.TrimSpace(l) == "" {
			continue
		}
		lines++
		var m map[string]any
		if json.Unmarshal([]byte(l), &m) != nil {
			bad++
			continue
		}
		if u, _ := m["uuid"].(string); u != "" {
			uuids[u] = true
		}
		if v, has := m["parentUuid"]; has {
			if v == nil {
				roots++
			} else if pu, _ := v.(string); pu != "" {
				parentRefs[pu]++
			}
		}
		if s, _ := m["sessionId"].(string); s != "" {
			perSession[s]++
		}
	}
	forks := 0
	for _, n := range parentRefs {
		if n >= 2 {
			forks++
		}
	}
	var per []string
	for s, n := range perSession {
		per = append(per, fmt.Sprintf("%.8s=%d", s, n))
	}
	sort.Strings(per)
	summary = fmt.Sprintf("lines %d, unparsable %d, distinct uuid %d, null-parent roots %d, uuids referenced as parent by 2+ lines %d, lines per sessionId: %s",
		lines, bad, len(uuids), roots, forks, strings.Join(per, " "))
	dir := filepath.Dir(files[0])
	all, _ := filepath.Glob(filepath.Join(dir, "*.jsonl"))
	var names []string
	for _, f := range all {
		names = append(names, filepath.Base(f))
	}
	sort.Strings(names)
	return summary, strings.Join(names, ", ")
}

// ---- D1 ----

func TestLiveDualResumeD1(t *testing.T) { liveDualResume(t, "D1", false) }

// D2: as D1, but the two processes carry distinct MASH_LAUNCH_TOKENs (ADR 0043): the second launch's hooks are
// refused, mail stays with the lease holder, and the refused hook tells the session once that this copy is not
// receiving mail. The mail-routing and notice observations are asserted; the rest is recorded as in D1.
func TestLiveDualResumeD2(t *testing.T) { liveDualResume(t, "D2", true) }

func liveDualResume(t *testing.T, id string, tokens bool) {
	sc := lsStart(t, id, true)
	w := newWorld(t, "a", "b")
	env := sc.env(sc.mashPath(), false, sc.identity(w, "a", map[string]string{"MASH_HOOK_TRACE": "1"}))
	sc.installPlugin(env)
	envA, envB := env, env
	if tokens {
		envA, envB = map[string]string{}, map[string]string{}
		for k, v := range env {
			envA[k], envB[k] = v, v
		}
		envA["MASH_LAUNCH_TOKEN"] = "live-token-a"
		envB["MASH_LAUNCH_TOKEN"] = "live-token-b"
	}
	ctx, cancel := context.WithTimeout(context.Background(), lsDualTO)
	t.Cleanup(cancel)
	sp := spool.Spool{Dir: spool.Dir(sc.state, w.env["a"]["MASH_OWNER"], "a")}
	spoolState := func() string {
		var names []string
		ents, _ := os.ReadDir(sp.Dir)
		for _, e := range ents {
			names = append(names, e.Name())
		}
		wait, err := sp.Waiting()
		return fmt.Sprintf("owner record: %q; files: %s; waiting entries %d (err %v)", sp.Owner(), strings.Join(names, ","), len(wait), err)
	}
	facts := func(p *lsLive) string {
		st := p.snapshot()
		return lsFacts(st.statusText, "delivery:", "channel allowlist:", "lease", "endpoint", "name")
	}
	ask := func(p *lsLive, prompt string, n int, timeout time.Duration) bool {
		if err := p.send(prompt); err != nil {
			ls.info(t, id, p.name+": send failed", "%v", err)
			return false
		}
		return p.waitResults(n, timeout)
	}

	// a. A: a fresh session, with mail waiting from before anything started (does it drain?).
	m0 := lsMarker(id + "a")
	lsSend(t, w, id, m0)
	ls.info(t, id, "spool before any process started", "%s", spoolState())
	a := sc.startLive(ctx, "A", envA)
	okA := ask(a, "Run the shell command `sleep 1`, then call the mash status tool, then reply with the single word ready.", 1, 3*time.Minute)
	ls.check(t, id, "process A produced a session id and a result", okA && a.sessionID() != "", "result %v, session id %q, stderr tail: %s", okA, a.sessionID(), a.stderrTail(3))
	if a.sessionID() == "" {
		return
	}
	stA := a.snapshot()
	ls.info(t, id, "A: MCP state (init)", "%s", strings.Join(stA.mcp, ", "))
	ls.info(t, id, "A: status facts", "%s", facts(a))
	ls.info(t, id, "A: status text, first 15 lines", "%s", firstLines(stA.statusText, 15))
	ls.info(t, id, "A: spool after turn 1 (is M0 drained?)", "%s", spoolState())

	// b. B resumes A's session id, same env, no --fork-session.
	b := sc.startLive(ctx, "B", envB, "--resume", a.sessionID())
	bPrompt := "Run the shell command `sleep 1`, then call the mash status tool, then call the mash who tool, then reply with the single word ready."
	if tokens {
		bPrompt = "Run the shell command `sleep 1`, then call the mash status tool, then call the mash who tool, then reply with the single word ready, and also tell me verbatim any line from mash that says this copy of the session is not receiving mail, or say none."
	}
	okB := ask(b, bPrompt, 1, 3*time.Minute)
	ls.info(t, id, "B: first turn finished", "%v; exited %v", okB, b.isExited())
	if b.isExited() {
		b.mu.Lock()
		ls.info(t, id, "B: early exit", "err %v code %d; stderr tail: %s", b.exitErr, b.exitCode, b.stderrTail(8))
		b.mu.Unlock()
	}
	stB := b.snapshot()
	ls.info(t, id, "B: session id same as A's", "%v (B has %q)", b.sessionID() == a.sessionID(), b.sessionID())
	ls.info(t, id, "B: MCP state (init)", "%s", strings.Join(stB.mcp, ", "))
	ls.info(t, id, "B: status facts", "%s", facts(b))
	ls.info(t, id, "B: status text, first 15 lines", "%s", firstLines(stB.statusText, 15))
	ls.info(t, id, "B: stderr lines about mash, lease or bind", "%s", b.stderrMash())
	ls.info(t, id, "B: who output", "%s", firstLines(b.who(), 10))
	ls.info(t, id, "spool after B started", "%s", spoolState())
	if tokens {
		ls.info(t, id, "B: turn-1 result, first line", "%s", firstLines(stB.resultText, 1))
		ls.check(t, id, "B's answer carries the not-receiving-mail notice", strings.Contains(strings.ToLower(stB.resultText), "not receiving mail"), "answer: %s", firstLines(stB.resultText, 2))
	}

	// c. Mail: who sees what.
	listPrompt := "Run the shell command `sleep 3`, then reply with one line listing every token starting with MARK-" + id + " that you saw in any mash mail context in this session, and nothing else (write none if there are none)."
	has := func(p *lsLive, m string) bool { return strings.Contains(p.snapshot().resultText, m) }
	named := func(p *lsLive, ms map[string]string) string {
		text := p.snapshot().resultText
		var got []string
		for k, m := range ms {
			if strings.Contains(text, m) {
				got = append(got, k)
			}
		}
		sort.Strings(got)
		return fmt.Sprintf("names [%s]; answer: %s", strings.Join(got, " "), firstLines(text, 2))
	}
	m1 := lsMarker(id + "b")
	lsSend(t, w, id, m1)
	ra, rb := a.snapshot().results, b.snapshot().results
	var wg sync.WaitGroup
	var doneA, doneB bool
	for _, x := range []struct {
		p    *lsLive
		base int
		ok   *bool
	}{{a, ra, &doneA}, {b, rb, &doneB}} {
		wg.Add(1)
		go func() {
			defer wg.Done()
			*x.ok = ask(x.p, listPrompt, x.base+1, 3*time.Minute)
		}()
	}
	wg.Wait()
	marks := map[string]string{"M0": m0, "M1": m1}
	ls.info(t, id, "M1 round, A", "finished %v; %s", doneA, named(a, marks))
	ls.info(t, id, "M1 round, B", "finished %v; %s", doneB, named(b, marks))
	ls.info(t, id, "spool after M1 round", "%s", spoolState())
	if tokens {
		ls.check(t, id, "M1 round: A names M1", has(a, m1), "%s", named(a, marks))
		ls.check(t, id, "M1 round: B does not name M1 (mail stays with A)", !has(b, m1), "%s", named(b, marks))
	}

	m2 := lsMarker(id + "c")
	lsSend(t, w, id, m2)
	marks["M2"] = m2
	ok := ask(b, listPrompt, b.snapshot().results+1, 3*time.Minute)
	ls.info(t, id, "M2 round, only B asked", "finished %v; %s", ok, named(b, marks))
	if tokens {
		ls.check(t, id, "M2 round: B does not name M2 (mail stays with A)", !has(b, m2), "%s", named(b, marks))
	}

	m3 := lsMarker(id + "d")
	lsSend(t, w, id, m3)
	marks["M3"] = m3
	ok = ask(a, listPrompt, a.snapshot().results+1, 3*time.Minute)
	ls.info(t, id, "M3 round, only A asked", "finished %v; %s", ok, named(a, marks))
	if tokens {
		ls.check(t, id, "M3 round: A names M2 (left for A) and M3", has(a, m2) && has(a, m3), "%s", named(a, marks))
	}

	// d. Transcript shape.
	sum, others := lsTranscript(sc.cfg, a.sessionID())
	ls.info(t, id, "transcript of the session id", "%s", sum)
	ls.info(t, id, "jsonl files in the same project dir", "%s", others)

	// e. Lease handoff: kill the holder.
	a.kill()
	select {
	case <-a.exited():
	case <-time.After(20 * time.Second):
	}
	time.Sleep(5 * time.Second)
	ls.info(t, id, "after killing A: B alive", "%v; spool %s", !b.isExited(), spoolState())
	m4 := lsMarker(id + "e")
	lsSend(t, w, id, m4)
	marks["M4"] = m4
	ok = ask(b, listPrompt, b.snapshot().results+1, 3*time.Minute)
	ls.info(t, id, "M4 round, only B asked (A dead)", "finished %v; exited %v; %s", ok, b.isExited(), named(b, marks))
	ls.info(t, id, "B: status facts after A died", "%s", facts(b))
	out, code := w.cli(t, "b", "who")
	ls.info(t, id, "who from endpoint b after A died", "code %d: %s", code, firstLines(out, 10))
	online := false
	for deadline := time.Now().Add(90 * time.Second); time.Now().Before(deadline); time.Sleep(5 * time.Second) {
		out, _ = w.cli(t, "b", "who")
		for _, l := range strings.Split(out, "\n") {
			f := strings.Fields(l)
			if len(f) > 0 && f[0] == "a" && strings.Contains(strings.ToLower(l), "online") && !strings.Contains(strings.ToLower(l), "offline") {
				online = true
			}
		}
		if online {
			break
		}
	}
	ls.info(t, id, "endpoint a online within 90s of A's death (B serving)", "%v; last who: %s", online, firstLines(out, 10))
	ls.info(t, id, "spool at the end", "%s", spoolState())

	// f. Clean end for B.
	b.closeStdin()
	select {
	case <-b.exited():
	case <-time.After(20 * time.Second):
		b.kill()
		<-b.exited()
	}
	b.mu.Lock()
	ls.info(t, id, "B: exit", "code %d err %v", b.exitCode, b.exitErr)
	b.mu.Unlock()
}
