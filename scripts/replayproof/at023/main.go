// Command at023 is the restart-replay proof of AT-023 (REQ-022). Run it by scripts/replayproof/at023.sh, inside a throwaway
// container, or directly in a throwaway VM guest (it starts a NATS server and mash processes, so never on a working host).
//
// For the last 0.5.x release (the oracle, a published binary checked against that release's SHA256SUMS) and then for the
// candidate commit (built by scripts/buildrelease), each in its own fresh server and state: Bob sends Alice a signed live
// prompt with the build's own `mash prompt`, a capture identity records the exact message off Alice's prompt subject,
// Alice's endpoint is stopped and started again inside the 30-second live window, and the captured message is replayed on
// its subject. The runner records what happened and asserts today's behaviour: the replay is accepted, on both builds, with
// equal results. It does not assert that the window is closed.
//
//	at023 -src <clean clone at the commit> -commit <40 hex> -version 0.6.0 -oracle-version 0.5.10 -out <dir>
package main

import (
	"bufio"
	"bytes"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/nats-io/jwt/v2"
	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/micro"

	"github.com/bogworks/mash/internal/sap"
	"github.com/bogworks/mash/scripts/storedmail/kit"
)

const (
	owner       = "acme"
	releaseBase = "https://github.com/bogworks/mash/releases/download/"
	// replyWait is how long a replay waits for the first chunk before the result is "no reply".
	replyWait = 5 * time.Second
)

var commitRE = regexp.MustCompile(`^[0-9a-f]{40}$`)
var versionRE = regexp.MustCompile(`^[0-9]+\.[0-9]+\.[0-9]+$`)

// logger writes the human log to stderr and keeps it for run.log.
type logger struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (l *logger) f(format string, a ...any) {
	l.mu.Lock()
	defer l.mu.Unlock()
	line := time.Now().UTC().Format("15:04:05.000") + " " + fmt.Sprintf(format, a...) + "\n"
	l.buf.WriteString(line)
	fmt.Fprint(os.Stderr, line)
}

var lg = &logger{}

// Assertion is one check of the run.
type Assertion struct {
	Name   string `json:"name"`
	Pass   bool   `json:"pass"`
	Detail string `json:"detail,omitempty"`
}

// Capture is the exact message taken off Alice's prompt subject.
type Capture struct {
	Subject    string              `json:"subject"`
	Headers    map[string][]string `json:"headers"`
	Body       string              `json:"body"`
	TS         time.Time           `json:"header_ts"` // the signing time in the Agent-Sender header
	ReceivedAt time.Time           `json:"received_at"`
}

// Timings are the moments that show the replay was inside the window.
type Timings struct {
	CaptureTS      time.Time `json:"capture_ts"`
	ControlSentAt  time.Time `json:"control_sent_at"`
	RestartBegan   time.Time `json:"restart_began"`
	OldProcessGone time.Time `json:"old_process_gone"`
	RestartDone    time.Time `json:"restart_done"`
	ReplaySentAt   time.Time `json:"replay_sent_at"`
	ReplayWindow   Window    `json:"replay_window"`
	OldPID, NewPID int       `json:"-"`
}

// Build is the record of one build's run.
type Build struct {
	Role        string   `json:"role"` // oracle or candidate
	Version     string   `json:"version"`
	Source      string   `json:"source"`
	SHA256      string   `json:"binary_sha256"`
	VersionText string   `json:"mash_version_output,omitempty"`
	Endpoints   []string `json:"endpoints"`
	PIDs        string   `json:"alice_pids"`
	Capture     Capture  `json:"capture"`
	Timings     Timings  `json:"timings"`
	Baseline    Replay   `json:"baseline_prompt"`
	Control     Replay   `json:"control_replay_no_restart"`
	Replay      Replay   `json:"replay_after_restart"`
	Result      Result   `json:"result"`
	Notes       []string `json:"notes,omitempty"`
}

// Record is the evidence file.
type Record struct {
	AT           string      `json:"at"`
	Req          string      `json:"req"`
	Venue        string      `json:"venue"`
	StartedAt    time.Time   `json:"started_at"`
	FinishedAt   time.Time   `json:"finished_at"`
	Environment  string      `json:"environment_lifecycle"`
	Commit       string      `json:"candidate_commit"`
	Arch         string      `json:"arch"`
	KnownIssue   KnownIssue  `json:"known_issue"`
	Builds       []Build     `json:"builds"`
	Equal        bool        `json:"results_equal"`
	Differences  []string    `json:"differences,omitempty"`
	Assertions   []Assertion `json:"assertions"`
	Pass         bool        `json:"pass"`
	Pointer      string      `json:"note"`
	OracleSource string      `json:"oracle_source"`
}

func main() { os.Exit(run()) }

func run() int {
	src := flag.String("src", "", "a clean git clone checked out at -commit (the candidate source)")
	commit := flag.String("commit", "", "the candidate commit, 40 hex digits")
	version := flag.String("version", "0.6.0", "the candidate's version")
	oracleVer := flag.String("oracle-version", "0.5.10", "the oracle release, the last 0.5.x")
	outDir := flag.String("out", "", "directory for record.json, run.log and the captured messages")
	work := flag.String("work", "", "scratch directory for servers, state and binaries (default: a new temp directory)")
	known := flag.String("known-issue", "", "URL of the known-0.6.0 entry (REQ-022); empty is recorded as empty")
	venue := flag.String("venue", "", "a line describing where this runs (container image, VM), for the record")
	flag.Parse()
	if *src == "" || *outDir == "" || !commitRE.MatchString(*commit) || !versionRE.MatchString(*version) || !versionRE.MatchString(*oracleVer) {
		fmt.Fprintln(os.Stderr, "usage: at023 -src <clone> -commit <40 hex> -version X.Y.Z -oracle-version X.Y.Z -out <dir> [-work dir] [-known-issue URL] [-venue text]")
		return 2
	}
	if err := os.MkdirAll(*outDir, 0o755); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 2
	}
	wd := *work
	if wd == "" {
		d, err := os.MkdirTemp("", "at023-")
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			return 2
		}
		wd = d
	} else if err := os.MkdirAll(wd, 0o700); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 2
	}
	rec := &Record{AT: "AT-023", Req: "REQ-022", Venue: *venue, StartedAt: time.Now().UTC(), Commit: *commit, Arch: runtime.GOARCH,
		KnownIssue: NewKnownIssue(*known), OracleSource: releaseBase + "v" + *oracleVer + "/",
		Environment: "each build gets its own NATS server (operator mode, generated keys) and state directories under " + wd +
			", created at the start of the build's run and stopped at its end; the launcher creates and destroys the container",
		Pointer: "the replay is asserted accepted because that is today's behaviour; the test does not assert that the window is closed"}
	lg.f("AT-023 restart-replay proof: candidate %s version %s, oracle v%s, %s/%s", *commit, *version, *oracleVer, runtime.GOOS, runtime.GOARCH)
	lg.f("venue: %s", *venue)
	lg.f("known-issue entry: %s", rec.KnownIssue.Status+" "+rec.KnownIssue.URL)

	ok := execute(rec, *src, *commit, *version, *oracleVer, wd)
	rec.FinishedAt = time.Now().UTC()
	rec.Pass = ok
	if len(rec.Assertions) == 0 {
		rec.Pass = false
	}
	for _, a := range rec.Assertions {
		lg.f("%s %s %s", map[bool]string{true: "PASS", false: "FAIL"}[a.Pass], a.Name, a.Detail)
	}
	if rec.KnownIssue.URL == "" {
		lg.f("NOTE: the known-issues link is empty in this record")
	}
	lg.f("RESULT: %s", map[bool]string{true: "PASS", false: "FAIL"}[rec.Pass])
	b, _ := json.MarshalIndent(rec, "", "  ")
	_ = os.WriteFile(filepath.Join(*outDir, "record.json"), append(b, '\n'), 0o644)
	_ = os.WriteFile(filepath.Join(*outDir, "run.log"), lg.buf.Bytes(), 0o644)
	if rec.Pass {
		return 0
	}
	return 1
}

func (r *Record) check(name string, pass bool, detail string) bool {
	r.Assertions = append(r.Assertions, Assertion{name, pass, detail})
	return pass
}

// execute fetches both binaries and runs both builds. It returns whether every assertion held.
func execute(rec *Record, src, commit, version, oracleVer, wd string) bool {
	arch := runtime.GOARCH
	oracleBin, oracleSum, err := fetchOracle(oracleVer, arch, filepath.Join(wd, "oracle"))
	if !rec.check("oracle v"+oracleVer+" binary downloaded and matches the release's SHA256SUMS", err == nil, errText(err)) {
		return false
	}
	lg.f("oracle mash-linux-%s sha256 %s (matches SHA256SUMS)", arch, oracleSum)
	candBin := filepath.Join(wd, "candidate", "mash")
	candSum, err := buildCandidate(src, commit, version, arch, candBin)
	if !rec.check("candidate "+version+" built by scripts/buildrelease at "+commit[:12], err == nil, errText(err)) {
		return false
	}
	lg.f("candidate mash sha256 %s", candSum)

	ok := true
	var builds [2]*Build
	for i, bc := range []struct{ role, ver, bin, sum, source string }{
		{"oracle", oracleVer, oracleBin, oracleSum, releaseBase + "v" + oracleVer + "/mash-linux-" + arch},
		{"candidate", version, candBin, candSum, "scripts/buildrelease at " + commit},
	} {
		lg.f("=== build %s %s ===", bc.role, bc.ver)
		b := &Build{Role: bc.role, Version: bc.ver, Source: bc.source, SHA256: bc.sum}
		builds[i] = b
		berr := runBuild(rec, b, bc.bin, filepath.Join(wd, bc.role))
		if berr != nil {
			lg.f("build %s stopped: %v", bc.role, berr)
			rec.check(bc.role+": the run completed", false, berr.Error())
			ok = false
		} else {
			rec.check(bc.role+": the run completed", true, "")
		}
		rec.Builds = append(rec.Builds, *b)
		if berr != nil {
			return false
		}
		ok = assertBuild(rec, b) && ok
	}
	d := Diff(builds[0].Result, builds[1].Result)
	rec.Equal = len(d) == 0
	rec.Differences = d
	if !rec.check("the recorded results are equal on the oracle and the candidate", rec.Equal, strings.Join(d, "; ")) {
		ok = false
	}
	return ok
}

func assertBuild(rec *Record, b *Build) bool {
	ok := true
	p := b.Role + ": "
	ok = rec.check(p+"the original prompt was accepted and answered", b.Baseline.Outcome == "accepted" && b.Baseline.SessionSaw && b.Baseline.ReplyText != "", b.Baseline.Summary()) && ok
	ok = rec.check(p+"control: the same bytes replayed to the running endpoint are refused (its nonce memory is intact)", strings.HasPrefix(b.Control.Outcome, "refused") && !b.Control.SessionSaw, b.Control.Summary()) && ok
	ok = rec.check(p+"the endpoint was restarted and the replay was sent inside the 30 s window", b.Timings.ReplayWindow.Inside,
		fmt.Sprintf("replay sent %.2f s after the captured ts, %.2f s left", b.Timings.ReplayWindow.Elapsed.Seconds(), b.Timings.ReplayWindow.Remaining.Seconds())) && ok
	ok = rec.check(p+"the restarted endpoint is a new process", b.Timings.OldPID != 0 && b.Timings.NewPID != 0 && b.Timings.OldPID != b.Timings.NewPID, b.PIDs) && ok
	ok = rec.check(p+"the replay is accepted (today's behaviour)", b.Replay.Outcome == "accepted" && b.Replay.SessionSaw, b.Replay.Summary()) && ok
	return ok
}

func errText(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

// --- binaries -----------------------------------------------------------------------------------------------------

func httpGet(url string) ([]byte, error) {
	c := &http.Client{Timeout: 3 * time.Minute}
	resp, err := c.Get(url)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("GET %s: %s", url, resp.Status)
	}
	return io.ReadAll(io.LimitReader(resp.Body, 200<<20))
}

// fetchOracle downloads the release's SHA256SUMS and mash-linux-<arch>, and checks the binary against the sums.
func fetchOracle(ver, arch, dir string) (path, sum string, err error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", "", err
	}
	base := releaseBase + "v" + ver + "/"
	sums, err := httpGet(base + "SHA256SUMS")
	if err != nil {
		return "", "", err
	}
	m, err := ParseSums(string(sums))
	if err != nil {
		return "", "", err
	}
	name := "mash-linux-" + arch
	want := m[name]
	if want == "" {
		return "", "", fmt.Errorf("SHA256SUMS of v%s has no %s", ver, name)
	}
	bin, err := httpGet(base + name)
	if err != nil {
		return "", "", err
	}
	h := sha256.Sum256(bin)
	got := hex.EncodeToString(h[:])
	if got != want {
		return "", "", fmt.Errorf("%s digest %s does not match SHA256SUMS %s", name, got, want)
	}
	path = filepath.Join(dir, "mash")
	if err := os.WriteFile(path, bin, 0o755); err != nil {
		return "", "", err
	}
	return path, got, nil
}

// buildCandidate runs scripts/buildrelease in src (a clean clone at commit) and checks the digest it printed.
func buildCandidate(src, commit, version, arch, out string) (string, error) {
	if err := os.MkdirAll(filepath.Dir(out), 0o700); err != nil {
		return "", err
	}
	cmd := exec.Command("go", "run", "./scripts/buildrelease", "-src", ".", "-out", out, "-goos", "linux", "-goarch", arch,
		"-version", version, "-commit", commit)
	cmd.Dir = src
	var o, e bytes.Buffer
	cmd.Stdout, cmd.Stderr = &o, &e
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("buildrelease: %v: %s", err, strings.TrimSpace(e.String()))
	}
	f := strings.Fields(o.String())
	if len(f) < 1 {
		return "", errors.New("buildrelease printed no digest")
	}
	b, err := os.ReadFile(out)
	if err != nil {
		return "", err
	}
	h := sha256.Sum256(b)
	if got := hex.EncodeToString(h[:]); got != f[0] {
		return "", fmt.Errorf("buildrelease printed %s, the file is %s", f[0], got)
	}
	return f[0], nil
}

// --- one build ----------------------------------------------------------------------------------------------------

// session is one `mash serve` process, driven over MCP on its stdin and stdout.
type session struct {
	cmd    *exec.Cmd
	stdin  io.WriteCloser
	lines  chan string
	pid    int
	done   chan struct{}
	errf   *os.File
	nextID int
}

func startSession(k *kit.Kit, label, bin, dir string, n int) (*session, error) {
	state := filepath.Join(k.Home(label), "state")
	if err := os.MkdirAll(k.Home(label), 0o700); err != nil {
		return nil, err
	}
	cmd := exec.Command(bin, "serve")
	cmd.Env = k.ProcEnv(label, state)
	in, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	out, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	ef, err := os.Create(filepath.Join(dir, fmt.Sprintf("serve-%s-%d.stderr", label, n)))
	if err != nil {
		return nil, err
	}
	cmd.Stderr = ef
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	s := &session{cmd: cmd, stdin: in, lines: make(chan string, 256), pid: cmd.Process.Pid, done: make(chan struct{}), errf: ef, nextID: 10}
	go func() {
		sc := bufio.NewScanner(out)
		sc.Buffer(make([]byte, 1<<20), 1<<20)
		for sc.Scan() {
			s.lines <- sc.Text()
		}
		close(s.lines)
	}()
	go func() { _ = cmd.Wait(); close(s.done) }()
	return s, nil
}

func (s *session) send(line string) error { _, err := io.WriteString(s.stdin, line+"\n"); return err }

// await reads stdout lines until pred accepts one.
func (s *session) await(d time.Duration, pred func(map[string]any) bool) (map[string]any, bool) {
	t := time.After(d)
	for {
		select {
		case l, ok := <-s.lines:
			if !ok {
				return nil, false
			}
			var m map[string]any
			if json.Unmarshal([]byte(l), &m) == nil && pred(m) {
				return m, true
			}
		case <-t:
			return nil, false
		}
	}
}

func idIs(id int) func(map[string]any) bool {
	return func(m map[string]any) bool { v, ok := m["id"].(float64); return ok && int(v) == id }
}

func isChannel(m map[string]any) bool { return m["method"] == "notifications/claude/channel" }

func (s *session) handshake() error {
	if err := s.send(`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{"experimental":{"claude/channel":{}}},"clientInfo":{"name":"at023","version":"1"}}}`); err != nil {
		return err
	}
	if _, ok := s.await(20*time.Second, idIs(1)); !ok {
		return errors.New("no initialize reply from mash serve")
	}
	if err := s.send(`{"jsonrpc":"2.0","method":"notifications/initialized"}`); err != nil {
		return err
	}
	if err := s.send(`{"jsonrpc":"2.0","id":2,"method":"tools/list"}`); err != nil {
		return err
	}
	if _, ok := s.await(20*time.Second, idIs(2)); !ok {
		return errors.New("no tools/list reply from mash serve")
	}
	return nil
}

// prompt waits for a prompt event and returns its request id.
func (s *session) prompt(d time.Duration) (string, bool) {
	m, ok := s.await(d, isChannel)
	if !ok {
		return "", false
	}
	p, _ := m["params"].(map[string]any)
	meta, _ := p["meta"].(map[string]any)
	id, _ := meta["request_id"].(string)
	return id, id != ""
}

// reply answers a prompt through the session's reply tool.
func (s *session) reply(reqID, text string) error {
	s.nextID++
	id := s.nextID
	b, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": id, "method": "tools/call",
		"params": map[string]any{"name": "reply", "arguments": map[string]string{"request_id": reqID, "text": text}}})
	if err := s.send(string(b)); err != nil {
		return err
	}
	if _, ok := s.await(10*time.Second, idIs(id)); !ok {
		return errors.New("no answer to the reply tool call")
	}
	return nil
}

// stop ends the process by its recorded PID: stdin closed first, then SIGTERM, then SIGKILL, each to that PID only.
func (s *session) stop() {
	_ = s.stdin.Close()
	for _, sig := range []syscall.Signal{0, syscall.SIGTERM, syscall.SIGKILL} {
		if sig != 0 {
			_ = syscall.Kill(s.pid, sig)
		}
		select {
		case <-s.done:
			s.errf.Close()
			return
		case <-time.After(5 * time.Second):
		}
	}
	s.errf.Close()
}

// registered reports whether an endpoint answers $SRV.INFO with the prompt subject.
func registered(nc *nats.Conn, subject string) bool {
	// An endpoint may publish only to the inboxes of its own owner, so the answers are asked for there.
	inbox := "_INBOX." + owner + ".probe." + randHex()
	sub, err := nc.SubscribeSync(inbox)
	if err != nil {
		return false
	}
	defer sub.Unsubscribe()
	if nc.PublishRequest("$SRV.INFO."+sap.ServiceName, inbox, nil) != nil {
		return false
	}
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		m, err := sub.NextMsg(time.Until(deadline))
		if err != nil {
			return false
		}
		var info micro.Info
		if json.Unmarshal(m.Data, &info) != nil {
			continue
		}
		for _, ep := range info.Endpoints {
			if ep.Name == "prompt" && ep.Subject == subject {
				return true
			}
		}
	}
	return false
}

func waitRegistered(nc *nats.Conn, subject string, want bool, d time.Duration) bool {
	end := time.Now().Add(d)
	for time.Now().Before(end) {
		if registered(nc, subject) == want {
			return true
		}
		time.Sleep(100 * time.Millisecond)
	}
	return false
}

func randHex() string {
	var b [16]byte
	_, _ = rand.Read(b[:])
	return hex.EncodeToString(b[:])
}

func copyHeader(h nats.Header) map[string][]string {
	m := map[string][]string{}
	for k, v := range h {
		m[k] = append([]string(nil), v...)
	}
	return m
}

// replayOnce publishes a copy of the captured message with a fresh reply inbox and records what comes back. When the
// endpoint accepts it, the session answers it so the stream ends.
func replayOnce(nc *nats.Conn, c Capture, s *session, label string) (Replay, error) {
	inbox := "_INBOX." + owner + ".capture." + randHex()
	sub, err := nc.SubscribeSync(inbox)
	if err != nil {
		return Replay{}, err
	}
	defer sub.Unsubscribe()
	if err := nc.Flush(); err != nil {
		return Replay{}, err
	}
	m := nats.NewMsg(c.Subject)
	m.Data = []byte(c.Body)
	m.Reply = inbox
	for k, v := range c.Headers {
		m.Header[k] = append([]string(nil), v...)
	}
	r := Replay{SentAt: time.Now().UTC()}
	if err := nc.PublishMsg(m); err != nil {
		return r, err
	}
	next := func(d time.Duration) (Chunk, bool) {
		x, err := sub.NextMsg(d)
		if err != nil {
			return Chunk{}, false
		}
		return ParseChunk(x.Data, x.Header.Get("Nats-Service-Error-Code"), x.Header.Get("Nats-Service-Error"), len(x.Header) > 0), true
	}
	first, got := next(replyWait)
	if got {
		r.Chunks = append(r.Chunks, first)
	}
	r.Outcome = Outcome(r.Chunks)
	if r.Outcome == "accepted" {
		if id, ok := s.prompt(5 * time.Second); ok {
			r.SessionSaw = true
			if err := s.reply(id, "answer to the replayed prompt"); err != nil {
				return r, err
			}
		}
	}
	for got && r.Chunks[len(r.Chunks)-1].Kind != "end" && r.Chunks[len(r.Chunks)-1].Kind != "error" {
		var c Chunk
		if c, got = next(replyWait); got {
			r.Chunks = append(r.Chunks, c)
			if c.Kind == "response" {
				r.ReplyText += c.Text
			}
		}
	}
	if r.Chunks == nil {
		r.Chunks = []Chunk{}
	}
	lg.f("%s replay: %s", label, r.Summary())
	return r, nil
}

// runBuild is one build's whole run: server, endpoints, original prompt, capture, control replay, restart, replay.
func runBuild(rec *Record, b *Build, bin, dir string) error {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	names := []kit.Name{{Label: "alice", Group: "dev", Instance: "alice"}, {Label: "bob", Group: "dev", Instance: "bob"}}
	k, err := kit.NewOwner(dir, bin, b.Version, owner, names)
	if err != nil {
		return fmt.Errorf("server and endpoints: %w", err)
	}
	defer k.Close()
	b.Endpoints = []string{k.To("alice"), k.To("bob")}
	lg.f("server %s, endpoints %v", k.E.URL, b.Endpoints)
	if out, e := k.Mash("bob", "", "version"); e == nil {
		b.VersionText = strings.TrimSpace(out)
		lg.f("mash version: %s", b.VersionText)
	}
	sj, err := k.Addrs["alice"].Subjects()
	if err != nil {
		return err
	}
	subj := sj.Prompt

	// The capture identity: test-only. It may read Alice's prompt subject and publish to it, and read its own inboxes.
	var perms jwt.Permissions
	perms.Pub.Allow.Add(subj)
	perms.Sub.Allow.Add(subj, "_INBOX."+owner+".capture.>")
	creds, _, err := k.E.MintUserWith(perms, nil)
	if err != nil {
		return fmt.Errorf("capture identity: %w", err)
	}
	cnc, err := nats.Connect(k.E.URL, nats.UserCredentialBytes(creds), nats.Timeout(15*time.Second))
	if err != nil {
		return fmt.Errorf("capture connection: %w", err)
	}
	defer cnc.Close()
	capSub, err := cnc.SubscribeSync(subj)
	if err != nil {
		return err
	}
	if err := cnc.Flush(); err != nil {
		return err
	}

	// Alice's session.
	alice, err := startSession(k, "alice", bin, dir, 1)
	if err != nil {
		return err
	}
	live := alice
	defer func() { live.stop() }()
	if err := alice.handshake(); err != nil {
		return fmt.Errorf("alice: %w", err)
	}
	if !waitRegistered(k.Conn, subj, true, 20*time.Second) {
		return errors.New("alice's prompt endpoint never registered")
	}
	lg.f("alice's session is up (pid %d), prompt subject %s", alice.pid, subj)

	// Bob prompts Alice with the build's own `mash prompt`; Alice answers.
	type pout struct {
		out string
		err error
	}
	pc := make(chan pout, 1)
	go func() {
		o, e := k.Mash("bob", "", "prompt", k.To("alice"), "what is the date?")
		pc <- pout{o, e}
	}()
	msg, err := capSub.NextMsg(20 * time.Second)
	if err != nil {
		return fmt.Errorf("the capture saw no prompt on %s: %w", subj, err)
	}
	b.Capture = Capture{Subject: msg.Subject, Headers: copyHeader(msg.Header), Body: string(msg.Data), ReceivedAt: time.Now().UTC()}
	var hdr struct {
		TS string `json:"ts"`
	}
	if v := msg.Header.Get(sap.HeaderName); json.Unmarshal([]byte(v), &hdr) != nil || hdr.TS == "" {
		return errors.New("the captured prompt has no readable Agent-Sender header with a ts")
	}
	if b.Capture.TS, err = time.Parse(time.RFC3339Nano, hdr.TS); err != nil {
		return fmt.Errorf("the captured ts: %w", err)
	}
	b.Timings.CaptureTS = b.Capture.TS
	lg.f("captured a prompt: subject %s, headers %d, body %d bytes, ts %s", msg.Subject, len(msg.Header), len(msg.Data), hdr.TS)
	capJSON, _ := json.MarshalIndent(b.Capture, "", "  ")
	_ = os.WriteFile(filepath.Join(dir, "capture.json"), capJSON, 0o600)

	id, ok := alice.prompt(10 * time.Second)
	b.Baseline = Replay{SentAt: b.Capture.ReceivedAt, SessionSaw: ok, Outcome: "no reply", Chunks: []Chunk{}}
	if !ok {
		return errors.New("alice's session never saw the original prompt")
	}
	if err := alice.reply(id, "the date is today"); err != nil {
		return err
	}
	select {
	case p := <-pc:
		if p.err != nil {
			b.Baseline.Outcome = "refused " + p.err.Error()
		} else {
			b.Baseline.Outcome = "accepted"
			b.Baseline.ReplyText = strings.TrimSpace(p.out)
		}
	case <-time.After(20 * time.Second):
		return errors.New("`mash prompt` did not return after Alice answered")
	}
	lg.f("original prompt: %s", b.Baseline.Summary())

	// Control: the same bytes to the running endpoint, whose nonce memory is intact.
	b.Timings.ControlSentAt = time.Now().UTC()
	if b.Control, err = replayOnce(cnc, b.Capture, alice, "control (no restart)"); err != nil {
		return err
	}

	// Restart Alice's endpoint, then replay inside the window.
	b.Timings.RestartBegan = time.Now().UTC()
	b.Timings.OldPID = alice.pid
	alice.stop()
	b.Timings.OldProcessGone = time.Now().UTC()
	if !waitRegistered(k.Conn, subj, false, 15*time.Second) {
		return errors.New("alice's prompt endpoint stayed registered after her session stopped")
	}
	alice2, err := startSession(k, "alice", bin, dir, 2)
	if err != nil {
		return err
	}
	live = alice2
	if err := alice2.handshake(); err != nil {
		return fmt.Errorf("alice (restarted): %w", err)
	}
	if !waitRegistered(k.Conn, subj, true, 20*time.Second) {
		return errors.New("alice's prompt endpoint never registered after the restart")
	}
	b.Timings.NewPID = alice2.pid
	b.Timings.RestartDone = time.Now().UTC()
	b.PIDs = fmt.Sprintf("before the restart %d, after %d", b.Timings.OldPID, b.Timings.NewPID)
	lg.f("alice restarted: pid %d -> %d, registered again %.2f s after the stop began", b.Timings.OldPID, b.Timings.NewPID, b.Timings.RestartDone.Sub(b.Timings.RestartBegan).Seconds())
	if b.Replay, err = replayOnce(cnc, b.Capture, alice2, "after restart"); err != nil {
		return err
	}
	b.Timings.ReplaySentAt = b.Replay.SentAt
	b.Timings.ReplayWindow = InWindow(b.Capture.TS, b.Replay.SentAt)
	lg.f("replay sent %.2f s after the captured ts (inside the window: %v)", b.Timings.ReplayWindow.Elapsed.Seconds(), b.Timings.ReplayWindow.Inside)
	b.Result = Result{Baseline: b.Baseline.Summary(), Control: b.Control.Summary(), Replay: b.Replay.Summary()}
	return nil
}
