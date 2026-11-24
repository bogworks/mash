package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime/debug"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/nats-io/nats.go/jetstream"

	"github.com/bogworks/mash/internal/admin"
	"github.com/bogworks/mash/internal/config"
	"github.com/bogworks/mash/internal/endpoint"
	"github.com/bogworks/mash/internal/render"
	"github.com/bogworks/mash/internal/sap"
	"github.com/bogworks/mash/internal/store"
	"github.com/bogworks/mash/internal/testenv"
)

// envFrom is the environment of a test whose subject is not TLS. REQ-014: these tests are not about TLS; the override
// is explicit. MASH_DANGEROUSLY_SKIP_NATS_ENCRYPTION is 1 unless the test names it, and a test about TLS names it ("" or "0" for
// none), or uses envStrict.
func envFrom(m map[string]string) func(string) string {
	return func(k string) string {
		if v, ok := m[k]; ok {
			return v
		}
		if k == "MASH_DANGEROUSLY_SKIP_NATS_ENCRYPTION" {
			return "1"
		}
		return ""
	}
}

// envStrict is envFrom with no default: a test about the TLS requirement sees exactly the variables it names.
func envStrict(m map[string]string) func(string) string { return func(k string) string { return m[k] } }

// session runs `mash serve` over pipes and speaks MCP to it.
type session struct {
	in      *io.PipeWriter
	lines   chan map[string]any
	done    chan int
	backlog []map[string]any // lines read while waiting for something else
	errs    lockedBuf        // serve's stderr
}

// lockedBuf is a bytes.Buffer serve can write while a test reads.
type lockedBuf struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (l *lockedBuf) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.Write(p)
}

func (s *session) stderr() string {
	s.errs.mu.Lock()
	defer s.errs.mu.Unlock()
	return s.errs.b.String()
}

func startServe(t *testing.T, env map[string]string) *session {
	t.Helper()
	return startServeArgs(t, env, "serve")
}

func startServeArgs(t *testing.T, env map[string]string, args ...string) *session {
	t.Helper()
	inR, inW := io.Pipe()
	outR, outW := io.Pipe()
	s := &session{in: inW, lines: make(chan map[string]any, 100), done: make(chan int, 1)}
	go func() { s.done <- run(args, envFrom(env), inR, outW, &s.errs); outW.Close() }()
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
	if instr := r["result"].(map[string]any)["instructions"].(string); !strings.Contains(instr, "MASH_ENDPOINT") {
		t.Fatalf("instructions %q", instr)
	}
	if names := toolNames(t, s); len(names) != 3 || !names["status"] || !names["doctor"] || !names["version"] {
		t.Fatalf("inert serve exposes %v, want version, status and doctor", names)
	}
}

// testGroup is the group of every endpoint the world makes; the owner is the test environment's. tAddr is the address
// of the instance name, and tTo the form a recipient is typed in.
const testGroup = "dev"

func tAddr(name string) sap.Address {
	a, err := sap.NewAddress(testenv.Owner, testGroup, name)
	if err != nil {
		panic(err)
	}
	return a
}

func tTo(name string) string { return tAddr(name).String() }

type world struct {
	e   *testenv.Env
	env map[string]map[string]string
}

func newWorld(t *testing.T, names ...string) *world {
	t.Helper()
	return newWorldWith(t, testenv.Options{}, names...)
}

func newWorldWith(t *testing.T, o testenv.Options, names ...string) *world {
	t.Helper()
	e := testenv.StartWith(t, o)
	adm, err := admin.New(e.Conn(t, e.AdminCreds), e.Owner)
	if err != nil {
		t.Fatal(err)
	}
	if err := adm.Init(context.Background()); err != nil {
		t.Fatal(err)
	}
	w := &world{e: e, env: map[string]map[string]string{}}
	for _, n := range names {
		c, u := e.Mint(t, tAddr(n))
		if _, err := adm.Provision(context.Background(), tAddr(n), u, e.Account, "", admin.Meta{}); err != nil {
			t.Fatal(err)
		}
		w.env[n] = map[string]string{"HOME": t.TempDir(), "MASH_ENDPOINT": e.Owner + "." + testGroup + "." + n, "MASH_SERVER": e.URL,
			"MASH_CREDS": c, "MASH_SETTLE_MS": "20"}
		if !o.TLS {
			// REQ-014: these tests are not about TLS; the override is explicit.
			w.env[n]["MASH_DANGEROUSLY_SKIP_NATS_ENCRYPTION"] = "1"
		}
	}
	return w
}

func (w *world) cli(t *testing.T, as string, args ...string) (string, int) {
	t.Helper()
	var out, errb bytes.Buffer
	code := run(args, envFrom(w.env[as]), strings.NewReader(""), &out, &errb)
	return out.String() + withoutPlainWarning(errb.String()), code
}

// held reads message n of endpoint as's inbox as the endpoint package returns it (render.Mail), for what the commands
// do not show: the sender's instance and the stored body.
func (w *world) held(t *testing.T, as string, n uint64) render.Mail {
	t.Helper()
	env := envFrom(w.env[as])
	cfg, err := config.Load(config.LookupFromFunc(env), env("HOME"))
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	ep, err := endpoint.OpenClientWith(ctx, cfg, endpoint.ClientOptions{Inbox: true})
	if err != nil {
		t.Fatal(err)
	}
	defer ep.Close()
	m, err := ep.Read(ctx, n)
	if err != nil {
		t.Fatal(err)
	}
	return m
}

// plainWarningLine is the line a command prints on standard error while MASH_DANGEROUSLY_SKIP_NATS_ENCRYPTION is set.
// REQ-014: these tests are not about TLS; the override is explicit, and the helpers that merge the two streams leave
// that one line out. The line itself is asserted in plaintext_refusal_test.go.
var plainWarningLine = regexp.MustCompile(`(?m)^warning: no encryption to the server \(MASH_DANGEROUSLY_SKIP_NATS_ENCRYPTION\)\n`)

// quiet wraps w so that the encryption-off line is dropped: the default test environment sets the override, so
// every command prints the line, and the tests that compare a whole stream are not about it.
func quiet(w io.Writer) io.Writer { return quietWriter{w} }

type quietWriter struct{ w io.Writer }

func (q quietWriter) Write(b []byte) (int, error) {
	if plainWarningLine.Match(b) && len(plainWarningLine.ReplaceAll(b, nil)) == 0 {
		return len(b), nil
	}
	return q.w.Write(b)
}

func withoutPlainWarning(s string) string { return plainWarningLine.ReplaceAllString(s, "") }

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
	out, code := w.cli(t, "b", "send", tTo("a"), "hi from the cli")
	if code != 0 || !strings.Contains(out, "Mail sent (number ") {
		t.Fatalf("cli send: %d %s", code, out)
	}
	ev := s.await(t, 5*time.Second, isChannel)
	content := ev["params"].(map[string]any)["content"].(string)
	if !strings.Contains(content, "from o.dev.b [verified]") || !strings.Contains(content, "hi from the cli") {
		t.Fatalf("channel event: %s", content)
	}
	s.send(t, `{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"send","arguments":{"address":"o.dev.b","message":"thanks b"}}}`)
	r := s.await(t, 5*time.Second, idIs(3))["result"].(map[string]any)
	if txt := r["content"].([]any)[0].(map[string]any)["text"].(string); !isReceipt(txt, "Mail sent") || r["isError"] == true {
		t.Fatalf("tool send: %v", r)
	}
	out, code = w.cli(t, "b", "inbox", "--wide")
	if code != 0 || !strings.Contains(out, "o.dev.a") || !strings.Contains(out, "verified") || !strings.Contains(out, "thanks b") {
		t.Fatalf("cli inbox: %d %s", code, out)
	}
	out, code = w.cli(t, "b", "read", "1")
	if code != 0 || !strings.Contains(out, "thanks b") {
		t.Fatalf("cli read: %d %s", code, out)
	}
	out, code = w.cli(t, "b", "who")
	if code != 0 || !whoShows(out, tTo("a"), "online") {
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
	// Prompt once a is registered (its SAP service starts when the channel is ready).
	deadline := time.Now().Add(10 * time.Second)
	for {
		if out, _ := w.cli(t, "b", "who"); whoShows(out, tTo("a"), "online") {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("a never came online")
		}
		time.Sleep(100 * time.Millisecond)
	}
	done := make(chan res, 1)
	go func() { o, c := w.cli(t, "b", "prompt", tTo("a"), "what", "time?"); done <- res{o, c} }()
	ev := s.await(t, 5*time.Second, isChannel)
	p := ev["params"].(map[string]any)
	meta := p["meta"].(map[string]any)
	if !strings.Contains(p["content"].(string), "what time?") || meta["request_id"] == "" || meta["from"] != "o_dev_b" {
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
	setTiming(t, time.Hour, 20*time.Second)
	w := newWorld(t, "a", "b")
	first := startServe(t, w.env["a"])
	first.initialize(t)
	first.tools(t)
	second := startServe(t, w.env["a"])
	r := second.initialize(t)
	// The refused session now waits for the name: normal instructions plus a waiting line, and the tools
	// (which report that mail is off until the name is free).
	if instr := r["result"].(map[string]any)["instructions"].(string); !strings.Contains(instr, "waiting") {
		t.Fatalf("instructions %q", instr)
	}
	if n := len(second.tools(t)); n != 8 {
		t.Fatalf("a waiting session exposes %d tools, want 8", n)
	}
	ev := second.await(t, 5*time.Second, isChannel)
	if !strings.Contains(ev["params"].(map[string]any)["content"].(string), "already in use") {
		t.Fatalf("event %v", ev)
	}
	// The holder is undisturbed and still gets its mail.
	if out, code := w.cli(t, "b", "send", tTo("a"), "still yours"); code != 0 {
		t.Fatalf("send: %d %s", code, out)
	}
	if got := content(first.await(t, 5*time.Second, isChannel)); !strings.Contains(got, "still yours") {
		t.Fatalf("holder event %s", got)
	}
}

// The admin command was removed: it answers as any unknown command does.
func TestAdminIsAnUnknownCommand(t *testing.T) {
	for _, args := range [][]string{{"admin"}, {"admin", "list"}, {"admin", "provision", "o.dev.z"}, {"admin", "-h"}} {
		var out, errb bytes.Buffer
		code := run(args, envFrom(nil), strings.NewReader(""), &out, &errb)
		if code != 2 || !strings.Contains(errb.String(), `mash has no command admin`) || out.Len() != 0 {
			t.Fatalf("%v: %d out=%q err=%q", args, code, out.String(), errb.String())
		}
	}
}

func TestUsage(t *testing.T) {
	var out bytes.Buffer
	if code := run(nil, envFrom(nil), strings.NewReader(""), quiet(&out), quiet(&out)); code != 2 || out.String() != "error: mash needs a command\nhint: mash --help\n" {
		t.Fatalf("usage: %d %s", code, out.String())
	}
	out.Reset()
	if code := run([]string{"version"}, envFrom(nil), strings.NewReader(""), quiet(&out), quiet(&out)); code != 0 || !strings.HasPrefix(out.String(), "mash v") {
		t.Fatalf("version: %d %s", code, out.String())
	}
}

func TestServeExitsWhenLeaseLost(t *testing.T) {
	old := endpoint.LeaseRefresh
	endpoint.LeaseRefresh = 100 * time.Millisecond
	t.Cleanup(func() { endpoint.LeaseRefresh = old })
	w := newWorld(t, "a")
	s := startServe(t, w.env["a"])
	s.initialize(t)
	s.tools(t)
	deadline := time.Now().Add(10 * time.Second)
	for {
		if out, _ := w.cli(t, "a", "who"); whoShows(out, tTo("a"), "online") {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("a never came online")
		}
		time.Sleep(100 * time.Millisecond)
	}
	// Another session takes the name: the lease is purged and re-created by it.
	js, _ := jetstream.New(w.e.Conn(t, w.e.AdminCreds))
	kv, err := js.KeyValue(context.Background(), store.LeaseBucket)
	if err != nil {
		t.Fatal(err)
	}
	// A Put overwrites whatever is there; Purge then Create raced with serve's refresh re-creating the key between
	// the two calls ("wrong last sequence ... key exists").
	if _, err := kv.Put(context.Background(), tAddr("a").Flat(), []byte(`{"instance_id":"someone-else","host":"h","pid":"1"}`)); err != nil {
		t.Fatal(err)
	}
	select {
	case code := <-s.done:
		s.done <- code
		if code == 0 {
			t.Fatal("serve exited 0 after losing its lease")
		}
	case <-time.After(10 * time.Second):
		t.Fatal("serve kept running after losing its lease (it would split prompts with the new holder)")
	}
	// Its SAP registration is gone with it.
	nc := w.e.Conn(t, w.e.AdminCreds)
	if m, err := nc.Request(tAddr("a").Subjects().Status, nil, time.Second); err == nil {
		t.Fatalf("the old session still answers status: %s", m.Data)
	}
}

// Losing the lease must shut down even when Claude has stopped reading stdout:
// the notice to Claude may block, the shutdown may not.
func TestServeLeaseLossDoesNotHangOnStdout(t *testing.T) {
	old := endpoint.LeaseRefresh
	endpoint.LeaseRefresh = 100 * time.Millisecond
	t.Cleanup(func() { endpoint.LeaseRefresh = old })
	w := newWorld(t, "a")
	inR, inW := io.Pipe()
	outR, outW := io.Pipe()
	done := make(chan int, 1)
	go func() { done <- run([]string{"serve"}, envFrom(w.env["a"]), inR, outW, io.Discard) }()
	t.Cleanup(func() { inW.Close(); outR.Close() })
	sc := bufio.NewScanner(outR)
	io.WriteString(inW, `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18"}}`+"\n")
	sc.Scan()
	io.WriteString(inW, `{"jsonrpc":"2.0","method":"notifications/initialized"}`+"\n")
	io.WriteString(inW, `{"jsonrpc":"2.0","id":2,"method":"tools/list"}`+"\n")
	sc.Scan()
	// From here on nobody reads stdout.
	deadline := time.Now().Add(10 * time.Second)
	for {
		if out, _ := w.cli(t, "a", "who"); whoShows(out, tTo("a"), "online") {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("a never came online")
		}
		time.Sleep(100 * time.Millisecond)
	}
	js, _ := jetstream.New(w.e.Conn(t, w.e.AdminCreds))
	kv, _ := js.KeyValue(context.Background(), store.LeaseBucket)
	// Another session takes the name. A Put overwrites whatever is there; a Purge then Create raced with serve's own
	// refresh re-creating the key between the two calls ("wrong last sequence ... key exists").
	if _, err := kv.Put(context.Background(), tAddr("a").Flat(), []byte(`{"instance_id":"someone-else"}`)); err != nil {
		t.Fatal(err)
	}
	select {
	case code := <-done:
		if code == 0 {
			t.Fatal("exit 0 after losing the lease")
		}
	case <-time.After(10 * time.Second):
		t.Fatal("serve hung on a blocked stdout after losing its lease")
	}
	if _, err := w.e.Conn(t, w.e.AdminCreds).Request(tAddr("a").Subjects().Status, nil, time.Second); err == nil {
		t.Fatal("the old session still answers status")
	}
}

func TestVersionLine(t *testing.T) {
	bi := func(rev, modified string) *debug.BuildInfo {
		s := []debug.BuildSetting{{Key: "vcs.revision", Value: rev}}
		if modified != "" {
			s = append(s, debug.BuildSetting{Key: "vcs.modified", Value: modified})
		}
		return &debug.BuildInfo{Settings: s}
	}
	cases := []struct {
		name, ldflags string
		info          *debug.BuildInfo
		want          string
	}{
		{"ldflags wins", "abc1234", bi("ffff", ""), "mash v0.1.0 (abc1234)"},
		{"vcs revision", "", bi("0123456789abcdef", "false"), "mash v0.1.0 (0123456)"},
		{"modified tree", "", bi("0123456789abcdef", "true"), "mash v0.1.0 (0123456+dirty)"},
		{"no build info", "", nil, "mash v0.1.0 (unknown)"},
		{"no vcs settings", "", &debug.BuildInfo{}, "mash v0.1.0 (unknown)"},
	}
	for _, c := range cases {
		if got := versionLine("0.1.0", c.ldflags, c.info); got != c.want {
			t.Errorf("%s: got %q want %q", c.name, got, c.want)
		}
	}
}

func TestResolveVersion(t *testing.T) {
	mod := func(v string) *debug.BuildInfo { return &debug.BuildInfo{Main: debug.Module{Version: v}} }
	cases := []struct {
		name, configured string
		bi               *debug.BuildInfo
		want             string
	}{
		{"the release ldflag wins over the module", "1.2.3", mod("v9.9.9"), "1.2.3"},
		{"a go install @tag build uses the module version, minus the v", endpoint.DevVersion, mod("v0.1.0"), "0.1.0"},
		{"a pseudo-version is a development build", endpoint.DevVersion, mod("v0.1.1-0.20261024000000-abcdef123456"), endpoint.DevVersion},
		{"a dirty pseudo-version is a development build", endpoint.DevVersion, mod("v0.0.0-20261024004654-55a16af46855+dirty"), endpoint.DevVersion},
		{"a hyphenated prerelease is a version", endpoint.DevVersion, mod("v1.2.3-rc-1"), "1.2.3-rc-1"},
		{"a prerelease tag is a version", endpoint.DevVersion, mod("v0.2.0-rc.1"), "0.2.0-rc.1"},
		{"an incompatible suffix is not a clean tag", endpoint.DevVersion, mod("v2.0.0+incompatible"), endpoint.DevVersion},
		{"(devel) is not a version", endpoint.DevVersion, mod("(devel)"), endpoint.DevVersion},
		{"empty module version", endpoint.DevVersion, mod(""), endpoint.DevVersion},
		{"no build info", endpoint.DevVersion, nil, endpoint.DevVersion},
		{"an empty configured version is dev", "", nil, endpoint.DevVersion},
	}
	for _, c := range cases {
		if got := resolveVersion(c.configured, c.bi); got != c.want {
			t.Errorf("%s: got %q want %q", c.name, got, c.want)
		}
	}
}

// A release build stamps the version by ldflag: the binary prints it, and the SAP service version (endpoint.Version)
// is the same string, resolved before any command runs.
func TestBuiltBinaryPrintsTheReleaseVersion(t *testing.T) {
	bin := filepath.Join(t.TempDir(), "mash")
	build := exec.Command("go", "build", "-o", bin,
		"-ldflags", "-X github.com/bogworks/mash/internal/endpoint.Version=7.8.9 -X main.commit=abc1234", ".")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	out, err := exec.Command(bin, "version").Output()
	if err != nil || strings.TrimSpace(string(out)) != "mash v7.8.9 (abc1234)" {
		t.Fatalf("%q %v", out, err)
	}
	// An unstamped build says dev, never a number that is not the source's.
	plain := filepath.Join(t.TempDir(), "mash")
	if out, err := exec.Command("go", "build", "-buildvcs=false", "-o", plain, ".").CombinedOutput(); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if out, _ := exec.Command(plain, "version").Output(); !strings.HasPrefix(string(out), "mash v0.0.0-dev (") {
		t.Fatalf("%q", out)
	}
}

func TestVersionCommandSetsTheServiceVersion(t *testing.T) {
	old := endpoint.CurrentVersion()
	defer endpoint.SetVersion(old)
	endpoint.SetVersion(endpoint.DevVersion)
	var out bytes.Buffer
	if code := run([]string{"version"}, envFrom(nil), strings.NewReader(""), quiet(&out), quiet(&out)); code != 0 {
		t.Fatal(out.String())
	}
	line := out.String()
	if !strings.HasPrefix(line, "mash v"+endpoint.CurrentVersion()+" (") {
		t.Fatalf("printed %q but the service version is %q", line, endpoint.CurrentVersion())
	}
}

// The version is resolved before dispatch: with tagged build info injected, `version` prints it and the service
// version (what SAP INFO and the MCP initialize report) is the same literal.
func TestVersionIsResolvedBeforeDispatch(t *testing.T) {
	oldV, oldR := endpoint.CurrentVersion(), readBuildInfo
	defer func() { endpoint.SetVersion(oldV); readBuildInfo = oldR }()
	endpoint.SetVersion(endpoint.DevVersion)
	readBuildInfo = func() (*debug.BuildInfo, bool) {
		return &debug.BuildInfo{Main: debug.Module{Version: "v1.2.3"}}, true
	}
	var out bytes.Buffer
	if code := run([]string{"version"}, envFrom(nil), strings.NewReader(""), quiet(&out), quiet(&out)); code != 0 {
		t.Fatal(out.String())
	}
	if !strings.HasPrefix(out.String(), "mash v1.2.3 (") || endpoint.CurrentVersion() != "1.2.3" {
		t.Fatalf("printed %q, service version %q", out.String(), endpoint.CurrentVersion())
	}
	// And the MCP server (an inert session answers initialize) announces the same version.
	s := startServe(t, map[string]string{"HOME": t.TempDir()})
	r := s.initialize(t)
	if v := r["result"].(map[string]any)["serverInfo"].(map[string]any)["version"]; v != "1.2.3" {
		t.Fatalf("MCP serverInfo version %v", v)
	}
}

// A `mash send` from the shell of a launched session (same MASH_LAUNCH_TOKEN as the session's serve) names the
// serve's lease instance, and the receiver shows it as the verified sender instance.
func TestCLISendAdoptsTheLaunchInstance(t *testing.T) {
	w := newWorld(t, "a", "b")
	w.env["a"]["MASH_LAUNCH_TOKEN"] = "tok-1"
	s := startServe(t, w.env["a"])
	s.initialize(t)
	s.tools(t)
	js, _ := jetstream.New(w.e.Conn(t, w.e.AdminCreds))
	kv, _ := js.KeyValue(context.Background(), store.LeaseBucket)
	var lease string
	deadline := time.Now().Add(10 * time.Second)
	for lease == "" {
		if en, err := kv.Get(context.Background(), tAddr("a").Flat()); err == nil {
			var lv struct {
				Instance string `json:"instance_id"`
			}
			_ = json.Unmarshal(en.Value(), &lv)
			lease = lv.Instance
		}
		if lease == "" && time.Now().After(deadline) {
			t.Fatal("a never took its lease")
		}
		time.Sleep(50 * time.Millisecond)
	}
	if out, code := w.cli(t, "a", "send", tTo("b"), "from the shell"); code != 0 {
		t.Fatalf("cli send: %d %s", code, out)
	}
	out, code := w.cli(t, "b", "read", "1")
	if m := w.held(t, "b", 1); code != 0 || !strings.Contains(out, "from:     o.dev.a\n") || !strings.Contains(out, "trust:    verified\n") || m.Instance != lease {
		t.Fatalf("want a verified mail naming the lease instance %q: %d %s (instance %q)", lease, code, out, m.Instance)
	}
}

// waitLeaseInstance waits for a's lease and returns its instance id.
func waitLeaseInstance(t *testing.T, w *world) string {
	t.Helper()
	js, _ := jetstream.New(w.e.Conn(t, w.e.AdminCreds))
	kv, _ := js.KeyValue(context.Background(), store.LeaseBucket)
	deadline := time.Now().Add(10 * time.Second)
	for {
		if en, err := kv.Get(context.Background(), tAddr("a").Flat()); err == nil {
			var lv struct {
				Instance string `json:"instance_id"`
			}
			_ = json.Unmarshal(en.Value(), &lv)
			if lv.Instance != "" {
				return lv.Instance
			}
		}
		if time.Now().After(deadline) {
			t.Fatal("a never took its lease")
		}
		time.Sleep(50 * time.Millisecond)
	}
}

// otherLaunchShell adds a copy of a's env under the name "a-other" with another launch's token (a copy, so a running
// serve's env is untouched).
func otherLaunchShell(w *world, token string) {
	w.env["a-other"] = map[string]string{}
	for k, v := range w.env["a"] {
		w.env["a-other"][k] = v
	}
	w.env["a-other"]["MASH_LAUNCH_TOKEN"] = token
}

// requireSentUnderOwnInstance asserts a send that could not adopt the lease still delivered: exit 0, the warning
// (naming the reason and the fallback) on stderr, and b's copy naming an instance that is not notLease.
func requireSentUnderOwnInstance(t *testing.T, w *world, out string, code int, reason, notLease string) {
	t.Helper()
	if code != 0 {
		t.Fatalf("want exit 0 (the send must not be refused): %d %s", code, out)
	}
	if !strings.Contains(out, reason) || !strings.Contains(out, "sending under this command's own instance") {
		t.Fatalf("want a warning naming %q and the fallback: %s", reason, out)
	}
	if !strings.Contains(out, "Mail sent (number ") {
		t.Fatalf("want the send reported: %s", out)
	}
	rd, rcode := w.cli(t, "b", "read", "1")
	inst := w.held(t, "b", 1).Instance
	if rcode != 0 || !strings.Contains(rd, "from:     o.dev.a\n") || inst == "" {
		t.Fatalf("b must hold the mail from a: %d %s (instance %q)", rcode, rd, inst)
	}
	if notLease != "" && inst == notLease {
		t.Fatalf("the mail must not name the lease's instance %q: %s", notLease, rd)
	}
}

// A token that names another launch than the serve's cannot adopt the lease; the send is still delivered, under the
// client's own instance, with a warning (a session relaunched after a crash holds a new token while the dead serve's
// lease lingers).
func TestCLISendWithAnotherLaunchTokenFallsBackToItsOwnInstance(t *testing.T) {
	w := newWorld(t, "a", "b")
	w.env["a"]["MASH_LAUNCH_TOKEN"] = "tok-1"
	s := startServe(t, w.env["a"])
	s.initialize(t)
	s.tools(t)
	lease := waitLeaseInstance(t, w)
	otherLaunchShell(w, "tok-2")
	out, code := w.cli(t, "a-other", "send", tTo("b"), "mismatch body")
	requireSentUnderOwnInstance(t, w, out, code, "does not match the launch holding", lease)
}

// A token with no serve running has no lease to adopt; the send is still delivered under the client's own instance.
func TestCLISendWithATokenAndNoServeFallsBackToItsOwnInstance(t *testing.T) {
	w := newWorld(t, "a", "b")
	w.env["a"]["MASH_LAUNCH_TOKEN"] = "tok-1"
	out, code := w.cli(t, "a", "send", tTo("b"), "no serve")
	requireSentUnderOwnInstance(t, w, out, code, "lease can't be read", "")
}

// Restart window (a): a relaunched session has a new token, and the new serve has not taken the lease yet.
func TestCLISendWithANewTokenBeforeTheNewServeTakesTheLease(t *testing.T) {
	w := newWorld(t, "a", "b")
	w.env["a"]["MASH_LAUNCH_TOKEN"] = "tok-2"
	out, code := w.cli(t, "a", "send", tTo("b"), "before the lease")
	requireSentUnderOwnInstance(t, w, out, code, "lease can't be read", "")
}

// Restart window (b): the old serve's lease (tok-1) is still held (a running serve stands in for a lingering one),
// and the relaunched session's shell has tok-2. The mail goes out under the shell's own instance, not the lease's.
func TestCLISendWithANewTokenAgainstTheOldLease(t *testing.T) {
	w := newWorld(t, "a", "b")
	w.env["a"]["MASH_LAUNCH_TOKEN"] = "tok-1"
	s := startServe(t, w.env["a"])
	s.initialize(t)
	s.tools(t)
	lease := waitLeaseInstance(t, w)
	otherLaunchShell(w, "tok-2")
	out, code := w.cli(t, "a-other", "send", tTo("b"), "after the kill")
	requireSentUnderOwnInstance(t, w, out, code, "does not match the launch holding", lease)
}

// Without a token and without a serve a send works as it always did.
func TestCLISendWithoutATokenOrServeStillWorks(t *testing.T) {
	w := newWorld(t, "a", "b")
	if out, code := w.cli(t, "a", "send", tTo("b"), "plain"); code != 0 || !strings.Contains(out, "Mail sent (number ") {
		t.Fatalf("plain send: %d %s", code, out)
	}
	if out, code := w.cli(t, "b", "read", "1"); code != 0 || !strings.Contains(out, "plain") {
		t.Fatalf("b read: %d %s", code, out)
	}
}

// mash inbox --after NUMBER pages forward past the newest-N window; a malformed inbox argument is a usage error, not a
// silent default.
func TestCLIInboxAfter(t *testing.T) {
	w := newWorld(t, "a", "b")
	for i := 1; i <= 4; i++ {
		if out, code := w.cli(t, "b", "send", tTo("a"), fmt.Sprintf("m%d", i)); code != 0 {
			t.Fatalf("send: %d %s", code, out)
		}
	}
	nums := func(out string) []string {
		var ns []string
		for i, l := range strings.Split(strings.TrimRight(out, "\n"), "\n") {
			if f := strings.Fields(l); i > 1 && len(f) > 0 { // the summary line, a blank line, then the header
				ns = append(ns, f[0])
			}
		}
		return ns
	}
	out, code := w.cli(t, "a", "inbox", "--after", "1", "--limit", "2")
	if got := strings.Join(nums(out), ","); code != 0 || got != "NUMBER,2,3" {
		t.Fatalf("inbox --after 1 --limit 2: %d %q", code, out)
	}
	out, code = w.cli(t, "a", "inbox", "--limit", "2", "--after", "3")
	if got := strings.Join(nums(out), ","); code != 0 || got != "NUMBER,4" {
		t.Fatalf("inbox --limit 2 --after 3: %d %q", code, out)
	}
	out, code = w.cli(t, "a", "inbox", "--after", "4")
	if !strings.HasPrefix(out, "About 4 messages for ") || strings.Contains(out, "NUMBER") || code != 0 {
		t.Fatalf("inbox --after 4: %d %q", code, out)
	}
	for _, bad := range [][]string{{"inbox", "--after", "x"}, {"inbox", "--limit"}, {"inbox", "--bogus", "1"}, {"inbox", "--limit", "two"}} {
		if out, code := w.cli(t, "a", bad...); code != 2 || !strings.HasPrefix(out, "error: ") || !strings.Contains(out, "hint: mash inbox --help") {
			t.Fatalf("%v: want a usage error, got %d %q", bad, code, out)
		}
	}
}

// --limit is 1 to 100 in both doors (the command line allowed 1000 before 0.5.6); more is a usage error, not a silent
// cut.
func TestCLIInboxLimitBounds(t *testing.T) {
	w := newWorld(t, "a", "b")
	for i := 1; i <= 120; i++ {
		if out, code := w.cli(t, "b", "send", tTo("a"), fmt.Sprintf("m%d", i)); code != 0 {
			t.Fatalf("send: %d %s", code, out)
		}
	}
	out, code := w.cli(t, "a", "inbox", "--limit", "100")
	if rows := strings.Split(strings.TrimRight(out, "\n"), "\n"); code != 0 || len(rows) != 100+4 {
		t.Fatalf("inbox --limit 100: %d, %d lines", code, len(rows))
	}
	for _, bad := range []string{"101", "500", "0", "-1"} {
		out, code := w.cli(t, "a", "inbox", "--limit", bad)
		if code != 2 || !strings.HasPrefix(out, "error: --limit must be a whole number from 1 to 100\nhint: mash inbox --help") {
			t.Fatalf("--limit %s: %d %q", bad, code, out)
		}
	}
	out, code = w.cli(t, "a", "inbox")
	if rows := strings.Split(strings.TrimRight(out, "\n"), "\n"); code != 0 || len(rows) != 20+4 {
		t.Fatalf("inbox default: %d, %d lines", code, len(rows))
	}
}
