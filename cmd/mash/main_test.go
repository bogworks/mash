package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"os/exec"
	"path/filepath"
	"runtime/debug"
	"strings"
	"testing"
	"time"

	"github.com/nats-io/nats.go/jetstream"

	"github.com/randybias/mash/internal/admin"
	"github.com/randybias/mash/internal/endpoint"
	"github.com/randybias/mash/internal/store"
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
	// Prompt once a is registered (its SAP service starts when the channel is ready).
	deadline := time.Now().Add(10 * time.Second)
	for {
		if out, _ := w.cli(t, "b", "who"); strings.Contains(out, "a\tonline") {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("a never came online")
		}
		time.Sleep(100 * time.Millisecond)
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
	if n := len(second.tools(t)); n != 5 {
		t.Fatalf("a waiting session exposes %d tools, want 5", n)
	}
	ev := second.await(t, 5*time.Second, isChannel)
	if !strings.Contains(ev["params"].(map[string]any)["content"].(string), "already in use") {
		t.Fatalf("event %v", ev)
	}
	// The holder is undisturbed and still gets its mail.
	if out, code := w.cli(t, "b", "send", "a", "still yours"); code != 0 {
		t.Fatalf("send: %d %s", code, out)
	}
	if got := content(first.await(t, 5*time.Second, isChannel)); !strings.Contains(got, "still yours") {
		t.Fatalf("holder event %s", got)
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
		if out, _ := w.cli(t, "a", "who"); strings.Contains(out, "a\tonline") {
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
	if err := kv.Purge(context.Background(), "a"); err != nil {
		t.Fatal(err)
	}
	if _, err := kv.Create(context.Background(), "a", []byte(`{"instance_id":"someone-else","host":"h","pid":"1"}`)); err != nil {
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
	if m, err := nc.Request("agents.status.cc.o.a", nil, time.Second); err == nil {
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
		if out, _ := w.cli(t, "a", "who"); strings.Contains(out, "a\tonline") {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("a never came online")
		}
		time.Sleep(100 * time.Millisecond)
	}
	js, _ := jetstream.New(w.e.Conn(t, w.e.AdminCreds))
	kv, _ := js.KeyValue(context.Background(), store.LeaseBucket)
	_ = kv.Purge(context.Background(), "a")
	if _, err := kv.Create(context.Background(), "a", []byte(`{"instance_id":"someone-else"}`)); err != nil {
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
	if _, err := w.e.Conn(t, w.e.AdminCreds).Request("agents.status.cc.o.a", nil, time.Second); err == nil {
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
		{"ldflags wins", "abc1234", bi("ffff", ""), "mash 0.1.0 (commit abc1234)"},
		{"vcs revision", "", bi("0123456789abcdef", "false"), "mash 0.1.0 (commit 0123456789abcdef)"},
		{"modified tree", "", bi("0123456789abcdef", "true"), "mash 0.1.0 (commit 0123456789abcdef+dirty)"},
		{"no build info", "", nil, "mash 0.1.0 (commit unknown)"},
		{"no vcs settings", "", &debug.BuildInfo{}, "mash 0.1.0 (commit unknown)"},
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
		"-ldflags", "-X github.com/randybias/mash/internal/endpoint.Version=7.8.9 -X main.commit=abc1234", ".")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	out, err := exec.Command(bin, "version").Output()
	if err != nil || strings.TrimSpace(string(out)) != "mash 7.8.9 (commit abc1234)" {
		t.Fatalf("%q %v", out, err)
	}
	// An unstamped build says dev, never a number that is not the source's.
	plain := filepath.Join(t.TempDir(), "mash")
	if out, err := exec.Command("go", "build", "-o", plain, ".").CombinedOutput(); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if out, _ := exec.Command(plain, "version").Output(); !strings.HasPrefix(string(out), "mash 0.0.0-dev (commit ") {
		t.Fatalf("%q", out)
	}
}

func TestVersionCommandSetsTheServiceVersion(t *testing.T) {
	old := endpoint.CurrentVersion()
	defer endpoint.SetVersion(old)
	endpoint.SetVersion(endpoint.DevVersion)
	var out bytes.Buffer
	if code := run([]string{"version"}, envFrom(nil), strings.NewReader(""), &out, &out); code != 0 {
		t.Fatal(out.String())
	}
	line := out.String()
	if !strings.HasPrefix(line, "mash "+endpoint.CurrentVersion()+" (commit ") {
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
	if code := run([]string{"version"}, envFrom(nil), strings.NewReader(""), &out, &out); code != 0 {
		t.Fatal(out.String())
	}
	if !strings.HasPrefix(out.String(), "mash 1.2.3 (commit ") || endpoint.CurrentVersion() != "1.2.3" {
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
		if en, err := kv.Get(context.Background(), "a"); err == nil {
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
	if out, code := w.cli(t, "a", "send", "b", "from", "the", "shell"); code != 0 {
		t.Fatalf("cli send: %d %s", code, out)
	}
	out, code := w.cli(t, "b", "read", "1")
	if code != 0 || !strings.Contains(out, "from a [verified]") || !strings.Contains(out, "instance: "+lease+"\n") {
		t.Fatalf("want a verified mail naming the lease instance %q: %d %s", lease, code, out)
	}
}

// bInboxEmpty fails the test when b holds any mail from a.
func bInboxEmpty(t *testing.T, w *world) {
	t.Helper()
	if out, code := w.cli(t, "b", "read", "1"); code == 0 {
		t.Fatalf("b has a message #1 that must not exist: %s", out)
	}
	if out, code := w.cli(t, "b", "inbox"); code != 0 || strings.Contains(out, "from a") {
		t.Fatalf("b's inbox holds mail from a: %d %s", code, out)
	}
}

// A token that names another launch than the serve's must stop the send: nothing is delivered under a fresh
// instance a reader would reject.
func TestCLISendWithAnotherLaunchTokenFails(t *testing.T) {
	w := newWorld(t, "a", "b")
	w.env["a"]["MASH_LAUNCH_TOKEN"] = "tok-1"
	s := startServe(t, w.env["a"])
	s.initialize(t)
	s.tools(t)
	// wait for the serve's lease
	js, _ := jetstream.New(w.e.Conn(t, w.e.AdminCreds))
	kv, _ := js.KeyValue(context.Background(), store.LeaseBucket)
	deadline := time.Now().Add(10 * time.Second)
	for {
		if _, err := kv.Get(context.Background(), "a"); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("a never took its lease")
		}
		time.Sleep(50 * time.Millisecond)
	}
	// the same endpoint, another launch's shell (a copy, so the running serve's env is untouched)
	w.env["a-other"] = map[string]string{}
	for k, v := range w.env["a"] {
		w.env["a-other"][k] = v
	}
	w.env["a-other"]["MASH_LAUNCH_TOKEN"] = "tok-2"
	out, code := w.cli(t, "a-other", "send", "b", "mismatch", "body")
	if code == 0 || !strings.Contains(out, "does not match") {
		t.Fatalf("want a non-zero exit naming the mismatch: %d %s", code, out)
	}
	bInboxEmpty(t, w)
}

// A token with no serve running has no lease to adopt: the send fails and delivers nothing.
func TestCLISendWithATokenAndNoServeFails(t *testing.T) {
	w := newWorld(t, "a", "b")
	w.env["a"]["MASH_LAUNCH_TOKEN"] = "tok-1"
	out, code := w.cli(t, "a", "send", "b", "no", "serve")
	if code == 0 || !strings.Contains(out, "MASH_LAUNCH_TOKEN") {
		t.Fatalf("want a non-zero exit naming MASH_LAUNCH_TOKEN: %d %s", code, out)
	}
	bInboxEmpty(t, w)
}

// Without a token and without a serve a send works as it always did.
func TestCLISendWithoutATokenOrServeStillWorks(t *testing.T) {
	w := newWorld(t, "a", "b")
	if out, code := w.cli(t, "a", "send", "b", "plain"); code != 0 || !strings.Contains(out, "sent #1 from a to b") {
		t.Fatalf("plain send: %d %s", code, out)
	}
	if out, code := w.cli(t, "b", "read", "1"); code != 0 || !strings.Contains(out, "plain") {
		t.Fatalf("b read: %d %s", code, out)
	}
}
