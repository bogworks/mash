package main

// The delivery-latency gate. v0.2.6 held mail to a fresh session for ~45 s (the idle wake) where v0.2.5 delivered it in
// ~0.1 s, and nothing before the tag noticed. This drives `mash serve` directly as the MCP client (no Claude, no
// a launcher) against an embedded NATS server and times the first channel notification a fresh session gets for mail.

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/bogworks/mash/internal/natsauth"
	"github.com/nats-io/nats.go"

	"github.com/bogworks/mash/internal/admin"
	"github.com/bogworks/mash/internal/release"
	"github.com/bogworks/mash/internal/testenv"
)

// The bounds of the scenarios. freshTimeout is generous because a v0.2.6 binary takes ~45 s and must still be measured.
const (
	freshTimeout  = 3 * time.Minute
	urgentLimit   = 10 * time.Second
	fyiQuiet      = 5 * time.Second // how long a hooked session must stay silent after fyi mail
	onlineTimeout = 30 * time.Second
	channelMethod = "notifications/claude/channel"
)

// serveSpec is how the plugin launches serve: the arguments and env block of its .mcp.json.
type serveSpec struct {
	Args []string
	Env  map[string]string
	// Root is the materialized plugin directory when the plugin's command is the launcher script (${CLAUDE_PLUGIN_ROOT}/
	// scripts/mash); empty when it is bare mash.
	Root string
}

func (s *serveSpec) rootOrEmpty() string {
	if s == nil {
		return ""
	}
	return s.Root
}

// launch is the program that runs serve for the binary under test, and the env it adds: bin itself for bare mash, the
// plugin's launcher pointed at bin (MASH_BIN) the way Claude Code runs it (CLAUDE_PLUGIN_ROOT) otherwise.
func (s serveSpec) launch(bin string) (name string, env []string) {
	if s.Root == "" {
		return bin, nil
	}
	return filepath.Join(s.Root, "scripts", "mash"), []string{"CLAUDE_PLUGIN_ROOT=" + s.Root, "MASH_BIN=" + bin}
}

// envPairs is the .mcp.json env block as KEY=VALUE, sorted, without PATH and HOME (the isolation); a launcher plugin's
// ${CLAUDE_PLUGIN_ROOT} is expanded, as Claude Code does.
func (s serveSpec) envPairs() []string {
	keys := make([]string, 0, len(s.Env))
	for k := range s.Env {
		if k != "PATH" && k != "HOME" {
			keys = append(keys, k)
		}
	}
	sort.Strings(keys)
	out := make([]string, 0, len(keys))
	for _, k := range keys {
		v := s.Env[k]
		if s.Root != "" {
			v = strings.ReplaceAll(v, "${CLAUDE_PLUGIN_ROOT}", s.Root)
		}
		out = append(out, k+"="+v)
	}
	return out
}

// latencies is what one binary did. Fresh is the send-to-first-notification time of unkinded mail to a fresh session
// (freshTimeout when none came). Findings are the violations of the other rules, and of Fresh when nothing arrived.
type latencies struct {
	Fresh    time.Duration
	FreshOK  bool
	Findings []string
	Notes    []string
}

// measureFunc measures bin; an error is a failure to run the measurement, not a finding about the binary.
// hasHook is whether bin has `mash hook`; candidate is whether bin is the candidate (and not the previous release), which
// must pass every scenario where the previous release may merely lack a feature.
type measureFunc func(ctx context.Context, bin string, spec serveSpec, hasHook, candidate bool) (latencies, error)

// freshAllowed is the slowest first-mail time a candidate may have, given the previous release's: twice as slow, but never
// less than a second slower, so 0.1 s against 0.3 s is noise and not a regression.
func freshAllowed(prev time.Duration) time.Duration { return max(2*prev, prev+time.Second) }

func secs(d time.Duration) string { return fmt.Sprintf("%.2fs", d.Seconds()) }

// judgeLatency applies the rule. prev is nil when there is no previous release to compare with. The candidate's own
// findings and a slow first mail are problems; the previous release's findings are only notes.
func judgeLatency(cand latencies, prev *latencies, prevTag string) (problems []error, notes []string) {
	for _, n := range cand.Notes {
		notes = append(notes, strings.Replace(n, "note  ", "note  candidate: ", 1))
	}
	for _, f := range cand.Findings {
		problems = append(problems, errors.New("the candidate: "+f))
	}
	if prev == nil {
		notes = append(notes, fmt.Sprintf("note  fresh-session first mail: candidate %s (no previous release to compare with)", secs(cand.Fresh)))
		return problems, notes
	}
	for _, n := range prev.Notes {
		notes = append(notes, strings.Replace(n, "note  ", "note  "+prevTag+": ", 1))
	}
	for _, f := range prev.Findings {
		notes = append(notes, fmt.Sprintf("note  the previous release %s: %s", prevTag, f))
	}
	notes = append(notes, fmt.Sprintf("note  fresh-session first mail: candidate %s, %s %s", secs(cand.Fresh), prevTag, secs(prev.Fresh)))
	if cand.FreshOK && cand.Fresh > freshAllowed(prev.Fresh) {
		problems = append(problems, fmt.Errorf("mail to a fresh session took the candidate %s, the previous release %s took %s: at most %s is allowed (twice as slow, and never under a second slower)",
			secs(cand.Fresh), prevTag, secs(prev.Fresh), secs(freshAllowed(prev.Fresh))))
	}
	return problems, notes
}

// judgeUnderPrevPlugin applies the rule to the candidate's binary launched the way the previous release's plugin launches
// serve (its args and env), which is how users who have not updated their plugin run it: as strictly as the candidate
// under its own plugin. Its findings and a first mail slower than freshAllowed(prev.Fresh) are problems.
func judgeUnderPrevPlugin(cand, prev latencies, prevTag string) (problems []error, notes []string) {
	who := fmt.Sprintf("the candidate under the previous release's plugin (%s)", prevTag)
	for _, n := range cand.Notes {
		notes = append(notes, strings.Replace(n, "note  ", "note  "+who+": ", 1))
	}
	for _, f := range cand.Findings {
		problems = append(problems, errors.New(who+": "+f))
	}
	notes = append(notes, fmt.Sprintf("note  fresh-session first mail: %s %s, %s %s", who, secs(cand.Fresh), prevTag, secs(prev.Fresh)))
	if cand.FreshOK && cand.Fresh > freshAllowed(prev.Fresh) {
		problems = append(problems, fmt.Errorf("mail to a fresh session took %s %s, the previous release %s took %s: at most %s is allowed (twice as slow, and never under a second slower)",
			who, secs(cand.Fresh), prevTag, secs(prev.Fresh), secs(freshAllowed(prev.Fresh))))
	}
	return problems, notes
}

// serveProc is a running `mash serve` driven as an MCP client.
type serveProc struct {
	cancel  context.CancelFunc
	cmd     *exec.Cmd
	in      io.WriteCloser
	events  chan time.Time // arrival of each channel notification that carries mail for the endpoint
	anyEv   chan chanEvent // arrival of every channel notification: any of them wakes Claude, mail or not
	replies chan string    // ids of answered requests
	stderr  *bytes.Buffer
	done    chan struct{} // closed when stdout ended
}

// chanEvent is one channel notification: when it arrived and its meta (for a finding that names what woke the session).
type chanEvent struct {
	At   time.Time
	Meta string
}

// startServe runs serve for the endpoint name; events holds only the channel notifications that carry mail for name,
// anyEv every channel notification.
func startServe(ctx context.Context, bin, name string, args, env []string) (*serveProc, error) {
	cctx, cancel := context.WithCancel(ctx)
	c := exec.CommandContext(cctx, bin, args...)
	c.Env = env
	c.WaitDelay = 2 * time.Second
	p := &serveProc{cancel: cancel, cmd: c, events: make(chan time.Time, 256), anyEv: make(chan chanEvent, 256), replies: make(chan string, 16), stderr: &bytes.Buffer{}, done: make(chan struct{})}
	c.Stderr = p.stderr
	in, err := c.StdinPipe()
	if err != nil {
		cancel()
		return nil, err
	}
	out, err := c.StdoutPipe()
	if err != nil {
		cancel()
		return nil, err
	}
	if err := c.Start(); err != nil {
		cancel()
		return nil, fmt.Errorf("starting %s: %w", bin, err)
	}
	p.in = in
	go func() {
		defer close(p.done)
		sc := bufio.NewScanner(out)
		sc.Buffer(make([]byte, 1<<20), 1<<24)
		for sc.Scan() {
			var m struct {
				Method string          `json:"method"`
				ID     json.RawMessage `json:"id"`
				Params json.RawMessage `json:"params"`
			}
			if json.Unmarshal(sc.Bytes(), &m) != nil {
				continue
			}
			switch {
			case m.Method == channelMethod:
				now := time.Now()
				select {
				case p.anyEv <- chanEvent{now, metaOf(m.Params)}:
				default:
				}
				if mailFor(m.Params, name) {
					select {
					case p.events <- now:
					default:
					}
				}
			case m.Method == "" && len(m.ID) != 0:
				select {
				case p.replies <- string(m.ID):
				default:
				}
			}
		}
	}()
	return p, nil
}

// mailFor is whether a channel notification's params announce mail (a delivery or a wake) for endpoint to, and not one
// of serve's own status or error notices: mail carries meta "to" (the receiver) and "seqs" (the messages), notices
// carry neither. Each probe receiver is sent exactly one message, so a non-empty seqs is the probe and no seq is matched.
func mailFor(params json.RawMessage, to string) bool {
	var p struct {
		Meta map[string]any `json:"meta"`
	}
	if json.Unmarshal(params, &p) != nil {
		return false
	}
	seqs, _ := p.Meta["seqs"].(string)
	got, _ := p.Meta["to"].(string)
	return got == to && seqs != ""
}

// metaOf is a channel notification's meta as compact JSON ("" when it has none), for naming what woke a session.
func metaOf(params json.RawMessage) string {
	var p struct {
		Meta json.RawMessage `json:"meta"`
	}
	if json.Unmarshal(params, &p) != nil {
		return ""
	}
	return string(p.Meta)
}

// stop kills serve and waits for it; its stderr is safe to read afterwards. Wait comes first: WaitDelay bounds it and
// closes the pipes, so a child that kept stdout open cannot hold the gate; the reader is then waited for, bounded too.
func (p *serveProc) stop() {
	p.in.Close()
	p.cancel()
	p.cmd.Wait()
	select {
	case <-p.done:
	case <-time.After(5 * time.Second):
	}
}

// request sends one JSON-RPC request and waits for its answer.
func (p *serveProc) request(ctx context.Context, id int, line string) error {
	if _, err := io.WriteString(p.in, line+"\n"); err != nil {
		return err
	}
	timer := time.NewTimer(onlineTimeout)
	defer timer.Stop()
	for {
		select {
		case got := <-p.replies:
			if got == fmt.Sprint(id) {
				return nil
			}
		case <-timer.C:
			return fmt.Errorf("no answer to request %d within %v", id, onlineTimeout)
		case <-p.done:
			return errors.New("serve exited")
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}

// handshake does what Claude Code does before it takes channel events: initialize, initialized, tools/list.
func (p *serveProc) handshake(ctx context.Context) error {
	if err := p.request(ctx, 1, `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{"experimental":{"claude/channel":{}}},"clientInfo":{"name":"release-check","version":"1"}}}`); err != nil {
		return err
	}
	if _, err := io.WriteString(p.in, `{"jsonrpc":"2.0","method":"notifications/initialized"}`+"\n"); err != nil {
		return err
	}
	return p.request(ctx, 2, `{"jsonrpc":"2.0","id":2,"method":"tools/list"}`)
}

// waitResult is how a wait for a notification ended: one came, the time ran out quietly, or serve died first.
type waitResult int

const (
	gotEvent waitResult = iota
	quiet
	exited
)

// next waits up to d for a mail notification that arrived at or after since. A serve that exits is not a quiet
// session: a notification that was already queued when it exited still counts, and an exit is reported as one.
func (p *serveProc) next(ctx context.Context, since time.Time, d time.Duration) (time.Time, waitResult) {
	return waitChan(ctx, p, p.events, func(t time.Time) time.Time { return t }, since, d)
}

// nextAny is next for every channel notification, mail or not, and returns the notification: a hooked session that gets
// any of them is woken, so the fyi silence window counts them all.
func (p *serveProc) nextAny(ctx context.Context, since time.Time, d time.Duration) (chanEvent, waitResult) {
	return waitChan(ctx, p, p.anyEv, func(e chanEvent) time.Time { return e.At }, since, d)
}

// waitChan is the wait both next and nextAny make: the first value on ch stamped at or after since, skipping older ones.
func waitChan[T any](ctx context.Context, p *serveProc, ch chan T, stamp func(T) time.Time, since time.Time, d time.Duration) (T, waitResult) {
	var zero T
	timer := time.NewTimer(d)
	defer timer.Stop()
	for {
		select {
		case v := <-ch:
			if !stamp(v).Before(since) {
				return v, gotEvent
			}
		case <-timer.C:
			return zero, quiet
		case <-p.done:
			for { // the reader is finished, so what it queued is all there will be
				select {
				case v := <-ch:
					if !stamp(v).Before(since) {
						return v, gotEvent
					}
				default:
					return zero, exited
				}
			}
		case <-ctx.Done():
			return zero, quiet
		}
	}
}

// lab is one embedded NATS server in mash's account setup and the sender endpoint on it.
type lab struct {
	bin     string
	spec    serveSpec
	ne      *testenv.Env
	adm     *admin.Admin
	mu      sync.Mutex // minting and provisioning one at a time
	dir     string
	senderE []string
}

// endpointEnv provisions endpoint name and returns the environment its mash commands run with: a fresh HOME and state
// directory, the plugin's env block (for serve), and the connection settings.
func (l *lab) endpointEnv(ctx context.Context, name string, plugin bool) ([]string, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	creds, user, err := l.ne.MintEndpoint(name)
	if err != nil {
		return nil, err
	}
	if _, err := l.adm.Provision(ctx, name, user, l.ne.Account, "", admin.Meta{}); err != nil {
		return nil, fmt.Errorf("provisioning %s: %w", name, err)
	}
	home := filepath.Join(l.dir, "home-"+name)
	if err := os.MkdirAll(home, 0o755); err != nil {
		return nil, err
	}
	env := []string{"PATH=/usr/bin:/bin", "HOME=" + home, "XDG_STATE_HOME=" + filepath.Join(home, "state")}
	if plugin {
		env = append(env, l.spec.envPairs()...)
	}
	return append(env, "MASH_NAME="+name, "MASH_URL="+l.ne.URL, "MASH_OWNER="+l.ne.Owner, "MASH_CREDS="+creds), nil
}

// sendMail sends the standard probe text from the sender to to, with the binary under test.
func (l *lab) sendMail(ctx context.Context, to string, flags ...string) (out string, code int, err error) {
	return l.sendText(ctx, to, "release-check latency probe", flags...)
}

// sendText sends text from the sender to to, with the binary under test.
func (l *lab) sendText(ctx context.Context, to, text string, flags ...string) (out string, code int, err error) {
	rctx, cancel := context.WithTimeout(ctx, versionTimeout)
	defer cancel()
	c := exec.CommandContext(rctx, l.bin, append(append([]string{"send", to}, flags...), text)...)
	c.Env = l.senderE
	c.WaitDelay = 2 * time.Second
	b, err := c.CombinedOutput()
	var ee *exec.ExitError
	switch {
	case err == nil:
		return string(b), 0, nil
	case errors.As(err, &ee):
		return string(b), ee.ExitCode(), nil
	}
	return "", 0, fmt.Errorf("starting %s send: %w", l.bin, err)
}

// session starts serve as a fresh receiver and takes it through the handshake. A serve that does not come up is a
// finding (the string), a failure to start things an error.
func (l *lab) session(ctx context.Context, name string) (p *serveProc, env []string, finding string, err error) {
	env, err = l.endpointEnv(ctx, name, true)
	if err != nil {
		return nil, nil, "", err
	}
	serveBin, extra := l.spec.launch(l.bin)
	p, err = startServe(ctx, serveBin, name, l.spec.Args, append(append([]string(nil), env...), extra...))
	if err != nil {
		return nil, nil, "", err
	}
	if herr := p.handshake(ctx); herr != nil {
		p.stop()
		return nil, nil, fmt.Sprintf("serve did not complete the MCP handshake: %v (stderr %q)", herr, p.stderr.String()), nil
	}
	// Mail sent before serve holds its lease is still delivered, but the wait would count in the timing: wait until it is online.
	deadline := time.Now().Add(onlineTimeout)
	for {
		out, code, err := l.runPlain(ctx, "who")
		if err != nil {
			p.stop()
			return nil, nil, "", err
		}
		if code == 0 && strings.Contains(out, name+"\tonline") {
			return p, env, "", nil
		}
		if time.Now().After(deadline) || ctx.Err() != nil {
			p.stop()
			return nil, nil, fmt.Sprintf("serve did not come online within %v (who: %q, stderr %q)", onlineTimeout, strings.TrimSpace(out), p.stderr.String()), nil
		}
		time.Sleep(100 * time.Millisecond)
	}
}

func (l *lab) runPlain(ctx context.Context, args ...string) (string, int, error) {
	stdout, stderr, code, err := runBounded(ctx, l.bin, args, l.senderE, "")
	return stdout + stderr, code, err
}

// judgeFyiSend reads the result of sending fyi mail: a finding or a note when the send failed, both empty when it went.
// Only the previous release may fail for not knowing `--kind`, and then it is a note; the candidate's failure is a finding.
func judgeFyiSend(out string, code int, candidate bool) (finding, note string) {
	switch {
	case code == 0:
		return "", ""
	case !candidate && strings.Contains(out, "kind"):
		return "", "note  fyi-after-hook skipped: this release does not know `--kind fyi`"
	}
	return fmt.Sprintf("the fyi mail was not sent: exit %d, %q", code, strings.TrimSpace(out)), ""
}

// hookShowed is whether the hook's stdout carries the probe mail: a hook exits 0 as well when it does nothing, so the
// exit code proves nothing and the marker's presence in what it printed is the proof that it showed the spooled mail.
func hookShowed(stdout, marker string) bool { return strings.Contains(stdout, marker) }

// judgeHookShowed reads whether `mash hook` showed the pre-hook probe mail: a finding for the candidate when it did not,
// a note (and the fyi check skipped) for the previous release, both empty when it did.
func judgeHookShowed(stdout, marker string, candidate bool) (finding, note string) {
	switch {
	case hookShowed(stdout, marker):
		return "", ""
	case candidate:
		return fmt.Sprintf("`mash hook` did not show spooled mail (stdout %q)", stdout), ""
	}
	return "", "note  fyi-after-hook skipped: this release's `mash hook` did not show spooled mail"
}

// judgeFyiWindow reads how the silence after fyi mail ended: only quiet passes. A woken session is a finding (woke is the
// notification's meta, whatever its kind), and so is a serve that died (a dead receiver is not a silent one); for the
// previous release, a dead serve is a note.
func judgeFyiWindow(res waitResult, candidate bool, woke string) (finding, note string) {
	switch res {
	case gotEvent:
		return fmt.Sprintf("fyi mail to a hooked session woke it with a channel notification within %v (meta %s)", fyiQuiet, woke), ""
	case exited:
		if !candidate {
			return "", "note  fyi-after-hook not judged: serve exited during the silence window"
		}
		return "serve exited while fyi mail to a hooked session was being waited out", ""
	}
	return "", ""
}

// shownKind is the kind the hook showed for the message carrying marker: it decodes the hook's JSON, takes
// hookSpecificOutput.additionalContext (the raw stdout when it is not JSON), splits that into the per-message blocks
// render emits (a full message opens with a "--- #N from" line and declares "kind: X"; a digest is one "#N\tfrom ..."
// line per message ending "\tkind X"; bodies are quoted "> " so they cannot forge either), and reads the kind of the
// first block that contains the marker. found is false when no block carries the marker, kind "" when that block
// declares none.
func shownKind(stdout, marker string) (kind string, found bool) {
	text := stdout
	var out struct {
		H struct {
			Ctx string `json:"additionalContext"`
		} `json:"hookSpecificOutput"`
	}
	if json.Unmarshal([]byte(stdout), &out) == nil {
		text = out.H.Ctx
	}
	var blocks [][]string
	for _, l := range strings.Split(text, "\n") {
		if strings.HasPrefix(l, "--- #") || (strings.HasPrefix(l, "#") && strings.Contains(l, "\tfrom ")) || len(blocks) == 0 {
			blocks = append(blocks, nil)
		}
		blocks[len(blocks)-1] = append(blocks[len(blocks)-1], l)
	}
	for _, b := range blocks {
		if !strings.Contains(strings.Join(b, "\n"), marker) {
			continue
		}
		for _, l := range b {
			if k, ok := strings.CutPrefix(l, "kind: "); ok {
				return strings.TrimSpace(k), true
			}
		}
		if i := strings.LastIndex(b[0], "\tkind "); i >= 0 && strings.HasPrefix(b[0], "#") {
			if f := strings.Fields(b[0][i+len("\tkind "):]); len(f) > 0 {
				return f[0], true
			}
		}
		return "", true
	}
	return "", false
}

// judgeFyiDelivered reads whether the next `mash hook` showed the fyi mail as fyi: silence is only right if the mail was
// kept for the hook and not discarded, and the message block holding the marker must declare kind fyi (a candidate that
// ignores `--kind fyi` stores the probe as task, whose idle wake is beyond the silence window, so the marker alone would
// pass it). A finding for the candidate when the marker is missing or its block has another kind, a note for the
// previous release.
func judgeFyiDelivered(stdout, marker string, candidate bool) (finding, note string) {
	k, found := shownKind(stdout, marker)
	switch {
	case found && k == "fyi":
		return "", ""
	case found && candidate:
		if k == "" {
			return fmt.Sprintf("the fyi probe was shown at the next hook without a kind (stdout %q)", stdout), ""
		}
		return fmt.Sprintf("the fyi probe was shown at the next hook as kind %s, not fyi (stdout %q)", k, stdout), ""
	case found:
		return "", fmt.Sprintf("note  the fyi probe was shown at the next hook as kind %q, not fyi, by this release", k)
	case candidate:
		return fmt.Sprintf("fyi mail was not delivered at the next hook (stdout %q)", stdout), ""
	}
	return "", "note  fyi mail was not delivered at the next hook of this release"
}

// measure runs the scenarios, each against its own fresh receiver, all at once: they share nothing.
func (l *lab) measure(ctx context.Context, hasHook, candidate bool) (latencies, error) {
	var (
		res  latencies
		mu   sync.Mutex
		errs []error
		wg   sync.WaitGroup
	)
	add := func(finding string, note string, err error) {
		mu.Lock()
		defer mu.Unlock()
		if finding != "" {
			res.Findings = append(res.Findings, finding)
		}
		if note != "" {
			res.Notes = append(res.Notes, note)
		}
		if err != nil {
			errs = append(errs, err)
		}
	}
	run := func(f func()) { wg.Add(1); go func() { defer wg.Done(); f() }() }

	run(func() { // fresh: unkinded mail to a fresh session
		p, _, finding, err := l.session(ctx, "fresh")
		if p == nil {
			add(finding, "", err)
			return
		}
		defer p.stop()
		start := time.Now()
		if out, code, err := l.sendMail(ctx, "fresh"); err != nil || code != 0 {
			add(fmt.Sprintf("the fresh-session mail was not sent: exit %d, %q", code, strings.TrimSpace(out)), "", err)
			return
		}
		at, got := p.next(ctx, start, freshTimeout)
		ok := got == gotEvent
		mu.Lock()
		res.Fresh, res.FreshOK = max(at.Sub(start), 0), ok
		if !ok {
			res.Fresh = freshTimeout
		}
		mu.Unlock()
		switch got {
		case exited:
			add("serve exited before it announced the mail to a fresh session", "", nil)
		case quiet:
			add(fmt.Sprintf("mail to a fresh session produced no channel notification within %v", freshTimeout), "", nil)
		}
	})
	run(func() { // urgent mail must wake at once
		p, _, finding, err := l.session(ctx, "urgent")
		if p == nil {
			add(finding, "", err)
			return
		}
		defer p.stop()
		start := time.Now()
		if out, code, err := l.sendMail(ctx, "urgent", "--urgent"); err != nil || code != 0 {
			add(fmt.Sprintf("the urgent mail was not sent: exit %d, %q", code, strings.TrimSpace(out)), "", err)
			return
		}
		switch at, got := p.next(ctx, start, urgentLimit); got {
		case exited:
			add("serve exited before it woke for urgent mail to a fresh session", "", nil)
		case quiet:
			add(fmt.Sprintf("urgent mail to a fresh session produced no channel notification within %v", urgentLimit), "", nil)
		default:
			add("", fmt.Sprintf("note  urgent mail: %s", secs(at.Sub(start))), nil)
		}
	})
	if !hasHook {
		add("", "note  fyi-after-hook skipped: this release has no `mash hook`", nil)
	} else {
		run(func() { // fyi mail to a hooked session must not wake it
			p, env, finding, err := l.session(ctx, "fyi")
			if p == nil {
				add(finding, "", err)
				return
			}
			defer p.stop()
			// A zero exit proves nothing (the hook exits 0 when it does nothing), so first strand a task mail in the
			// spool: it wakes the session at once before the hook (v0.2.7+; an older serve may take its idle wake), and
			// the hook must then print it. Waiting for that notification also keeps it out of the silence window below.
			marker := fmt.Sprintf("release-check-hook-probe-%d", time.Now().UnixNano())
			probeAt := time.Now()
			if out, code, err := l.sendText(ctx, "fyi", marker); err != nil || code != 0 {
				add(fmt.Sprintf("the hook-probe mail was not sent: exit %d, %q", code, strings.TrimSpace(out)), "", err)
				return
			}
			switch _, got := p.next(ctx, probeAt, freshTimeout); {
			case got == exited:
				add("serve exited before it announced the hook-probe mail", "", nil)
				return
			case got == quiet && ctx.Err() == nil:
				if candidate {
					add(fmt.Sprintf("the hook-probe mail to a session that is not hooked yet produced no channel notification within %v", freshTimeout), "", nil)
				} else {
					add("", "note  fyi-after-hook skipped: the hook-probe mail produced no channel notification", nil)
				}
				return
			}
			stdout, stderr, code, err := runBounded(ctx, l.bin, []string{"hook"}, env, hookInputs["UserPromptSubmit"])
			if err != nil {
				add("", "", err)
				return
			}
			if code != 0 {
				add(fmt.Sprintf("`mash hook` exited %d (stdout %q, stderr %q)", code, stdout, stderr), "", nil)
				return
			}
			if f, n := judgeHookShowed(stdout, marker, candidate); f != "" || n != "" {
				add(f, n, nil)
				return
			}
			start := time.Now()
			fyiMarker := fmt.Sprintf("release-check-fyi-probe-%d", time.Now().UnixNano())
			out, code, err := l.sendText(ctx, "fyi", fyiMarker, "--kind", "fyi")
			if f, n := judgeFyiSend(out, code, candidate); err != nil {
				add("", "", err)
			} else if f != "" || n != "" {
				add(f, n, nil)
			} else {
				// Any channel notification wakes Claude, so the window counts them all, not only mail matches.
				ev, res := p.nextAny(ctx, start, fyiQuiet)
				if f, n := judgeFyiWindow(res, candidate, ev.Meta); f != "" || n != "" {
					add(f, n, nil)
					return
				}
				// Silence is only right if the fyi was kept for the next hook, not discarded.
				stdout, stderr, code, err := runBounded(ctx, l.bin, []string{"hook"}, env, hookInputs["UserPromptSubmit"])
				if err != nil {
					add("", "", err)
					return
				}
				if code != 0 {
					add(fmt.Sprintf("`mash hook` exited %d after fyi mail (stdout %q, stderr %q)", code, stdout, stderr), "", nil)
					return
				}
				f, n := judgeFyiDelivered(stdout, fyiMarker, candidate)
				add(f, n, nil)
			}
		})
	}
	wg.Wait()
	if ctx.Err() != nil {
		return latencies{}, op(ctx.Err())
	}
	if len(errs) != 0 {
		return latencies{}, op(errors.Join(errs...))
	}
	return res, nil
}

// measureLatencies is the real measure: a throwaway NATS server in mash's account setup, a sender endpoint, and the
// scenarios against bin. A failure to set any of it up is an op error.
func measureLatencies(ctx context.Context, bin string, spec serveSpec, hasHook, candidate bool) (latencies, error) {
	dir, err := os.MkdirTemp("", "release-check-latency-")
	if err != nil {
		return latencies{}, op(err)
	}
	defer os.RemoveAll(dir)
	ne, stop, err := testenv.Launch(filepath.Join(dir, "nats"))
	if err != nil {
		return latencies{}, op(fmt.Errorf("starting the NATS server: %w", err))
	}
	defer stop()
	nc, err := nats.Connect(ne.URL, natsauth.UserCredentials(ne.AdminCreds))
	if err != nil {
		return latencies{}, op(err)
	}
	defer nc.Close()
	adm, err := admin.New(nc, ne.Owner)
	if err != nil {
		return latencies{}, op(err)
	}
	if err := adm.Init(ctx); err != nil {
		return latencies{}, op(fmt.Errorf("initialising the directory: %w", err))
	}
	l := &lab{bin: bin, spec: spec, ne: ne, adm: adm, dir: dir}
	if l.senderE, err = l.endpointEnv(ctx, "sender", false); err != nil {
		return latencies{}, op(err)
	}
	return l.measure(ctx, hasHook, candidate)
}

// latencyStep is the release-check step: the candidate's first-mail latency against the previous release's, which is
// measured as shipped (its binary under its own plugin's spec) and is the one baseline for both judgements.
func latencyStep(ctx context.Context, o Options, b *binaries) error {
	if b.cand == "" {
		return op(errors.New("not run: the plugin step did not get a candidate binary built"))
	}
	measure := o.measure
	if measure == nil {
		measure = measureLatencies
	}
	ctx, cancel := context.WithTimeout(ctx, freshTimeout+2*time.Minute)
	defer cancel()
	spec := serveSpec{Args: b.mcpArgs, Env: b.mcpEnv, Root: b.mcpRoot}
	// The baseline is the previous release as shipped: its binary under its own plugin's serve args and env. Without
	// that plugin (no or unreadable .mcp.json) it falls back to the candidate's spec, and says so below.
	prevBaseSpec := spec
	if b.prevSpec != nil {
		prevBaseSpec = *b.prevSpec
	}
	var (
		cand, prev, candOld latencies
		cerr, perr, oerr    error
		wg                  sync.WaitGroup
		comparePrev         = b.prev != ""
		underPrevPlugin     = comparePrev && b.prevSpec != nil
	)
	wg.Add(1)
	go func() { defer wg.Done(); cand, cerr = measure(ctx, b.cand, spec, true, true) }()
	if comparePrev {
		wg.Add(1)
		go func() {
			defer wg.Done()
			prev, perr = measure(ctx, b.prev, prevBaseSpec, !release.TagBelow(b.prevTag, release.HookSince), false)
		}()
	}
	if underPrevPlugin { // the candidate launched as the previous release's plugin launches serve
		wg.Add(1)
		go func() { defer wg.Done(); candOld, oerr = measure(ctx, b.cand, *b.prevSpec, true, true) }()
	}
	wg.Wait()
	if cerr != nil {
		return cerr
	}
	if perr != nil {
		return perr // the baseline could not be taken: the gate cannot decide, and must not quietly judge alone
	}
	if oerr != nil {
		return oerr
	}
	var pp *latencies
	switch {
	case comparePrev:
		pp = &prev
	case b.hasPrev: // the plugin step failed before it had the previous release's binary, and said so
		fmt.Fprintf(o.Out, "note  the previous release %s has no binary here: the candidate is judged alone\n", b.prevTag)
	}
	problems, notes := judgeLatency(cand, pp, b.prevTag)
	switch {
	case underPrevPlugin:
		p2, n2 := judgeUnderPrevPlugin(candOld, prev, b.prevTag)
		problems, notes = append(problems, p2...), append(notes, n2...)
	case comparePrev:
		notes = append(notes, fmt.Sprintf("note  the previous release's plugin (%s) could not be read: the candidate is not measured under its serve args and env, and the baseline uses the candidate's plugin", b.prevTag))
	}
	for _, n := range notes {
		fmt.Fprintln(o.Out, n)
	}
	return errors.Join(problems...)
}
