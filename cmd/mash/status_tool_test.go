package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/nats-io/nats.go"

	"github.com/bogworks/mash/internal/config"
	"github.com/bogworks/mash/internal/endpoint"
	"github.com/bogworks/mash/internal/mcp"
	"github.com/bogworks/mash/internal/spool"
)

const credsSecret = "SECRET-CREDS-SENTINEL-do-not-print"

func toolNames(t *testing.T, s *session) map[string]bool {
	t.Helper()
	names := map[string]bool{}
	for _, tl := range s.tools(t) {
		names[tl.(map[string]any)["name"].(string)] = true
	}
	return names
}

// statusOf calls the status tool and returns its text; it fails if the tool is missing or errors.
func statusOf(t *testing.T, s *session, id int) string {
	t.Helper()
	r := s.call(t, id, "status", `{}`)
	if r["isError"] == true {
		t.Fatalf("status errored: %v", r)
	}
	return resultText(r)
}

// statusLine is the first line of out that starts with prefix.
func statusLine(t *testing.T, out, prefix string) string {
	t.Helper()
	for _, l := range strings.Split(out, "\n") {
		if strings.HasPrefix(l, prefix) {
			return l
		}
	}
	t.Fatalf("no %q line in:\n%s", prefix, out)
	return ""
}

func wantAll(t *testing.T, what, out string, subs ...string) {
	t.Helper()
	for _, sub := range subs {
		if !strings.Contains(out, sub) {
			t.Errorf("%s: want %q in:\n%s", what, sub, out)
		}
	}
}

func wantInfoTools(t *testing.T, s *session) {
	t.Helper()
	names := toolNames(t, s)
	if !names["status"] || !names["version"] {
		t.Fatalf("want status and version in %v", names)
	}
}

func TestStatusToolInert(t *testing.T) {
	s := startServe(t, map[string]string{"HOME": t.TempDir()})
	s.initialize(t)
	wantInfoTools(t, s)
	out := statusOf(t, s, 10)
	wantAll(t, "inert", out,
		"name: MISSING",
		"not set",
		"start Claude Code with MASH_NAME=<endpoint>",
		"config: MISSING",
		"url",
		"MASH_URL",
		"config.json",
		"connection:",
		"channel notifications sent: 0 (Claude Code delivers them only if this plugin is on its channel allowlist and the session was started with --channels; the allowlist line is mash's reading of the settings files, /status in Claude Code is the authority)",
		"channel allowlist: ",
		"version: "+versionLine(endpoint.CurrentVersion(), endpoint.CurrentCommit(), nil))
	if v := s.call(t, 11, "version", `{}`); v["isError"] == true || !strings.Contains(resultText(v), "mash ") {
		t.Fatalf("version in an inert serve: %v", v)
	}
}

func TestStatusToolInstructionsMentionIt(t *testing.T) {
	s := startServe(t, map[string]string{"HOME": t.TempDir()})
	if instr := s.initialize(t)["result"].(map[string]any)["instructions"].(string); !strings.Contains(instr, "status tool") {
		t.Fatalf("inert instructions do not point at the status tool: %q", instr)
	}
	if !strings.Contains(endpoint.Instructions("a", "o", ""), "status") {
		t.Fatal("running instructions do not mention status")
	}
	if !strings.Contains(endpoint.ErrorInstructions("a", fmt.Errorf("boom")), "status tool") {
		t.Fatal("error instructions do not mention the status tool")
	}
}

// A config error (a name but no url) still answers MCP, and now with status and version.
func TestStatusToolConfigError(t *testing.T) {
	home := t.TempDir()
	creds := filepath.Join(t.TempDir(), "a.creds")
	if err := os.WriteFile(creds, []byte("-----BEGIN NATS USER JWT-----\n"+credsSecret+"\n------END NATS USER JWT------\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	s := startServe(t, map[string]string{"HOME": home, "MASH_NAME": "a", "MASH_OWNER": "o", "MASH_CREDS": creds})
	s.initialize(t)
	wantInfoTools(t, s)
	out := statusOf(t, s, 10)
	wantAll(t, "config error", out,
		"name: OK",
		"config: MISSING",
		"url",
		"~/.config/mash/config.json",
		"MASH_URL",
		"creds: OK",
		creds,
		"connection: FAILED",
		"not started")
	if strings.Contains(out, credsSecret) || strings.Contains(out, "BEGIN NATS") {
		t.Fatalf("status printed creds content:\n%s", out)
	}
	if l := statusLine(t, out, "config:"); strings.Contains(l, "owner") && !strings.Contains(l, "MISSING") {
		t.Fatalf("owner is set: %s", l)
	}
}

func TestStatusToolMissingOwner(t *testing.T) {
	s := startServe(t, map[string]string{"HOME": t.TempDir(), "MASH_NAME": "a", "MASH_URL": "nats://u:pw@127.0.0.1:1"})
	s.initialize(t)
	out := statusOf(t, s, 10)
	wantAll(t, "missing owner", out, "config: MISSING", "owner", "MASH_OWNER")
	if strings.Contains(out, "pw@") {
		t.Fatalf("status printed a url password:\n%s", out)
	}
}

func TestStatusToolCredsMissingThenPresent(t *testing.T) {
	home := t.TempDir()
	env := map[string]string{"HOME": home, "MASH_NAME": "a", "MASH_OWNER": "o"} // no url: a cheap config-error serve
	def := filepath.Join(home, ".config", "mash", "a.creds")

	s := startServe(t, env)
	s.initialize(t)
	out := statusOf(t, s, 10)
	wantAll(t, "creds missing", out, "creds: MISSING", def, "does not exist", "MASH_CREDS")

	if err := os.MkdirAll(filepath.Dir(def), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(def, []byte(credsSecret), 0o600); err != nil {
		t.Fatal(err)
	}
	s2 := startServe(t, env)
	s2.initialize(t)
	out = statusOf(t, s2, 10)
	wantAll(t, "creds present", out, "creds: OK", def)
	if strings.Contains(out, credsSecret) {
		t.Fatalf("status printed creds content:\n%s", out)
	}

	if err := os.Chmod(def, 0); err != nil {
		t.Fatal(err)
	}
	s3 := startServe(t, env)
	s3.initialize(t)
	if os.Geteuid() != 0 {
		out = statusOf(t, s3, 10)
		wantAll(t, "creds unreadable", out, "creds: FAILED", def, "not readable")
		if strings.Contains(out, credsSecret) {
			t.Fatalf("status printed creds content:\n%s", out)
		}
	}
}

// The first Open fails with a transient error: the session retries, and status says so with the error text.
func TestStatusToolRetrying(t *testing.T) {
	setTiming(t, time.Hour, 20*time.Second)
	w := newWorld(t, "a")
	openEndpoint = func(context.Context, config.Config) (*endpoint.Endpoint, error) {
		return nil, fmt.Errorf("mash: connect nats://x: %w", nats.ErrNoServers)
	}
	s := startServe(t, w.env["a"])
	s.initialize(t)
	wantInfoTools(t, s)
	out := statusOf(t, s, 10)
	wantAll(t, "retrying", out, "name: OK", "connection: RETRYING", "no servers", "creds: OK")
}

func TestStatusToolWaitingForLease(t *testing.T) {
	setTiming(t, time.Hour, 20*time.Second)
	w := newWorld(t, "a")
	holdLease(t, leaseKV(t, w), "a")
	s := startServe(t, w.env["a"])
	s.initialize(t)
	wantInfoTools(t, s)
	out := statusOf(t, s, 10)
	l := statusLine(t, out, "connection:")
	wantAll(t, "waiting", l, "WAITING", "lease", "another session", "host", "pid")
}

func TestStatusToolFailed(t *testing.T) {
	setTiming(t, time.Hour, 20*time.Second)
	w := newWorld(t, "a")
	openEndpoint = func(context.Context, config.Config) (*endpoint.Endpoint, error) {
		return nil, fmt.Errorf("mash: connect nats://x: %w", nats.ErrAuthorization)
	}
	s := startServe(t, w.env["a"])
	s.initialize(t)
	wantInfoTools(t, s) // a failed start exposed no tools before
	out := statusOf(t, s, 10)
	wantAll(t, "failed", out, "connection: FAILED", "authorization violation")
}

func TestStatusToolRunning(t *testing.T) {
	w := newWorld(t, "a", "b")
	s := startServe(t, w.env["a"])
	s.initialize(t)
	names := toolNames(t, s)
	for _, n := range []string{"send", "read", "inbox", "who", "reply", "version", "status"} {
		if !names[n] {
			t.Fatalf("missing tool %s in %v", n, names)
		}
	}
	if len(names) != 7 {
		t.Fatalf("running serve exposes %d tools, want 7: %v", len(names), names)
	}
	var out string
	id := 10
	eventually(t, 5*time.Second, "the endpoint is running", func() bool {
		id++
		out = statusOf(t, s, id)
		return strings.Contains(out, "connection: OK") && strings.Contains(out, "hooks have not run")
	})
	wantAll(t, "running", out, "name: OK", "config: OK", "creds: OK", w.env["a"]["MASH_CREDS"],
		"hooks have not run", "channel notifications sent: ")
	// The real creds file's content never appears.
	raw, err := os.ReadFile(w.env["a"]["MASH_CREDS"])
	if err != nil {
		t.Fatal(err)
	}
	for _, l := range strings.Split(string(raw), "\n") {
		if len(strings.TrimSpace(l)) > 16 && strings.Contains(out, strings.TrimSpace(l)) {
			t.Fatalf("status printed a line of the creds file:\n%s", out)
		}
	}

	// A notification sent to the session is counted.
	if o, code := w.cli(t, "b", "send", "a", "count me"); code != 0 {
		t.Fatalf("send: %d %s", code, o)
	}
	s.await(t, 5*time.Second, isChannel)
	counted := regexp.MustCompile(`channel notifications sent: [1-9]`)
	eventually(t, 5*time.Second, "the notification is counted", func() bool {
		id++
		return counted.MatchString(statusOf(t, s, id))
	})

	// A hook run once this session owns the spool shows.
	eventually(t, 5*time.Second, "the session owns the spool", func() bool {
		id++
		return strings.Contains(statusOf(t, s, id), "hooks have not run")
	})
	sp := spool.Spool{Dir: spool.Dir(filepath.Join(w.env["a"]["HOME"], ".local", "state"), w.e.Owner, "a")}
	if err := sp.Init(); err != nil {
		t.Fatal(err)
	}
	if err := sp.Touch(); err != nil {
		t.Fatal(err)
	}
	eventually(t, 5*time.Second, "the hook run shows", func() bool {
		id++
		o := statusOf(t, s, id)
		return strings.Contains(o, "hooks have run in this session") && strings.Contains(o, "mail also arrives with tool results")
	})
}

const pwSecret = "s3cretpw"

func TestRedactText(t *testing.T) {
	for _, in := range []string{
		"nats://alice:s3cretpw@127.0.0.1:1",
		"nats://s3cretpw@127.0.0.1:1",
		"nats://a:s3cretpw@h1:4222,nats://b:s3cretpw@h2:4222",
		"alice:s3cretpw@h1:4222,h2:4222",
		"nats://alice:s3cretpw@127.0.0.1:1/%zz",
		"nats://alice:s3c@retpw@127.0.0.1:1",
		"mash: connect nats://alice:s3cretpw@127.0.0.1:1: no servers available",
		"nats://alice:s3cretpw@[::1",
	} {
		out := redactText(in)
		if strings.Contains(out, "retpw") || strings.Contains(out, "alice:") || !strings.Contains(out, "REDACTED@") {
			t.Errorf("redactText(%q) = %q", in, out)
		}
	}
	if got := redactText("nats://127.0.0.1:4222"); got != "nats://127.0.0.1:4222" {
		t.Errorf("no userinfo changed: %q", got)
	}
}

func TestStatusToolRedactsErrors(t *testing.T) {
	setTiming(t, time.Hour, 20*time.Second)
	const u = "nats://alice:" + pwSecret + "@127.0.0.1:1"
	for _, tc := range []struct{ name, want string }{{"retrying", "RETRYING"}, {"failed", "FAILED"}} {
		t.Run(tc.name, func(t *testing.T) {
			w := newWorld(t, "a")
			openEndpoint = func(context.Context, config.Config) (*endpoint.Endpoint, error) {
				err := error(nats.ErrNoServers)
				if tc.name == "failed" {
					err = nats.ErrAuthorization
				}
				return nil, fmt.Errorf("mash: connect %s: %w", u, err)
			}
			s := startServe(t, w.env["a"])
			s.initialize(t)
			out := statusOf(t, s, 10)
			wantAll(t, tc.name, out, "connection: "+tc.want, "REDACTED@")
			if strings.Contains(out, pwSecret) {
				t.Fatalf("status printed a url password:\n%s", out)
			}
		})
	}
}

// The real open of an unreachable url that carries a password.
func TestStatusToolRedactsRealConnectError(t *testing.T) {
	setTiming(t, time.Hour, 20*time.Second)
	w := newWorld(t, "a")
	env := map[string]string{}
	for k, v := range w.env["a"] {
		env[k] = v
	}
	env["MASH_URL"] = "nats://alice:" + pwSecret + "@127.0.0.1:1"
	s := startServe(t, env)
	s.initialize(t)
	var out string
	id := 10
	eventually(t, 10*time.Second, "the connection leaves STARTING", func() bool {
		id++
		out = statusOf(t, s, id)
		return !strings.Contains(statusLine(t, out, "connection:"), "STARTING")
	})
	if strings.Contains(out, pwSecret) {
		t.Fatalf("status printed a url password:\n%s", out)
	}
}

func TestStatusToolMalformedURLRedacted(t *testing.T) {
	const bad = "nats://alice:" + pwSecret + "@127.0.0.1:1/%zz"
	s := startServe(t, map[string]string{"HOME": t.TempDir(), "MASH_NAME": "a", "MASH_OWNER": "o", "MASH_URL": bad})
	s.initialize(t)
	out := statusOf(t, s, 10)
	if strings.Contains(out, pwSecret) {
		t.Fatalf("status printed a malformed url's password:\n%s", out)
	}
	if got := redactURL(bad); strings.Contains(got, pwSecret) {
		t.Fatalf("redactURL kept the password: %q", got)
	}
}

// A malformed config.json: the parse error comes first, and unresolved fields are not called missing.
func TestStatusToolConfigParseErrorFirst(t *testing.T) {
	home := t.TempDir()
	dir := filepath.Join(home, ".config", "mash")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "config.json"), []byte("{ not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	s := startServe(t, map[string]string{"HOME": home, "MASH_NAME": "a", "MASH_URL": "nats://alice:" + pwSecret + "@127.0.0.1:1"})
	s.initialize(t)
	out := statusOf(t, s, 10)
	l := statusLine(t, out, "config:")
	wantAll(t, "config line", l, "config: FAILED", "config.json", "could not be resolved because of this error")
	if strings.Contains(l, "MISSING") || strings.Contains(l, "is not set") {
		t.Fatalf("an unresolved field was reported missing: %s", l)
	}
	if strings.Contains(out, pwSecret) {
		t.Fatalf("status printed a url password:\n%s", out)
	}
}

func spoolFor(w *world, name string) spool.Spool {
	return spool.Spool{Dir: spool.Dir(filepath.Join(w.env[name]["HOME"], ".local", "state"), w.e.Owner, name)}
}

// While the lease is held by another session, that holder's hooks are not this session's.
func TestStatusToolHooksNaWhileWaiting(t *testing.T) {
	setTiming(t, time.Hour, 20*time.Second)
	w := newWorld(t, "a")
	holdLease(t, leaseKV(t, w), "a")
	s := startServe(t, w.env["a"])
	s.initialize(t)
	sp := spoolFor(w, "a")
	if err := sp.Init(); err != nil {
		t.Fatal(err)
	}
	if err := sp.Touch(); err != nil {
		t.Fatal(err)
	}
	out := statusOf(t, s, 10)
	wantAll(t, "waiting", out, "hooks n/a - this session does not hold the lease yet")
	if strings.Contains(out, "hooks have run") {
		t.Fatalf("a held lease's hook counted as this session's:\n%s", out)
	}
}

// A hook run before this session took the lease belongs to the previous holder.
func TestStatusToolStaleHookAfterAcquisition(t *testing.T) {
	w := newWorld(t, "a")
	sp := spoolFor(w, "a")
	if err := sp.Init(); err != nil {
		t.Fatal(err)
	}
	if err := sp.Touch(); err != nil {
		t.Fatal(err)
	}
	time.Sleep(20 * time.Millisecond)
	s := startServe(t, w.env["a"])
	s.initialize(t)
	var out string
	id := 10
	eventually(t, 5*time.Second, "the session owns the spool", func() bool {
		id++
		out = statusOf(t, s, id)
		return strings.Contains(out, "hooks have not run")
	})
	if strings.Contains(out, "hooks have run") {
		t.Fatalf("a previous holder's hook counted:\n%s", out)
	}
}

// settingsSource is a statusSource whose Claude Code settings come from a temp HOME and a temp managed dir.
func settingsSource(t *testing.T, name string, files map[string]string) *statusSource {
	t.Helper()
	root := t.TempDir()
	for rel, body := range files {
		p := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return &statusSource{cfg: config.Config{Name: name}, srv: &atomic.Pointer[mcp.Server]{},
		home: filepath.Join(root, "home"), managedDir: filepath.Join(root, "managed"),
		getenv: func(string) string { return "" }}
}

const mashAllowed = `{"channelsEnabled": true, "allowedChannelPlugins": [{"marketplace":"mash","plugin":"mash"}]}`

func TestStatusAllowlistShadowed(t *testing.T) {
	src := settingsSource(t, "a", map[string]string{
		"home/.claude/remote-settings.json": `{"permissions": {"deny": []}}`,
		"managed/managed-settings.json":     mashAllowed,
	})
	l := statusLine(t, statusText(src), "channel allowlist: ")
	wantAll(t, "shadowed", l, "channel allowlist: SHADOWED - local managed settings file", "lists mash, but server-managed settings",
		"remote-settings.json", "Next: ask an organisation Owner", "confirm with /status")
}

func TestStatusAllowlistOK(t *testing.T) {
	src := settingsSource(t, "a", map[string]string{"managed/managed-settings.json": mashAllowed})
	l := statusLine(t, statusText(src), "channel allowlist: ")
	if l != "channel allowlist: OK - local managed settings file enables channels and lists mash@mash" {
		t.Fatalf("got %q", l)
	}
}

func TestStatusAllowlistMissing(t *testing.T) {
	src := settingsSource(t, "a", nil)
	l := statusLine(t, statusText(src), "channel allowlist: ")
	if l != "channel allowlist: MISSING - no managed source lists mash@mash. Next: see docs/install.md section 3" {
		t.Fatalf("got %q", l)
	}
}

func TestStatusAllowlistOffAndUnknown(t *testing.T) {
	off := settingsSource(t, "a", map[string]string{"managed/managed-settings.json": `{"channelsEnabled": false, "allowedChannelPlugins": [{"marketplace":"mash","plugin":"mash"}]}`})
	wantAll(t, "off", statusLine(t, statusText(off), "channel allowlist: "), "channel allowlist: OFF - local managed settings file sets channelsEnabled false")
	bad := settingsSource(t, "a", map[string]string{"managed/managed-settings.json": `{`})
	wantAll(t, "unknown", statusLine(t, statusText(bad), "channel allowlist: "), "channel allowlist: UNKNOWN - ")
}

func TestStatusEnvironmentStripped(t *testing.T) {
	for _, name := range []string{"a", ""} {
		src := settingsSource(t, name, map[string]string{"managed/managed-settings.json": `{"env": {"CLAUDE_CODE_MCP_ALLOWLIST_ENV": "1"}}`})
		l := statusLine(t, statusText(src), "environment: ")
		wantAll(t, "stripped", l, "environment: STRIPPED - Claude Code passes MCP servers only a baseline environment (CLAUDE_CODE_MCP_ALLOWLIST_ENV)",
			"~/.config/mash/config.json", "unsupported here")
	}
	clean := settingsSource(t, "a", nil)
	if strings.Contains(statusText(clean), "environment: ") {
		t.Fatal("no environment line expected when nothing strips the environment")
	}
}

// A hook that runs in this session but is refused as not its own is reported, not "have not run yet".
func TestStatusToolReportsRefusedHooks(t *testing.T) {
	w := newWorld(t, "a")
	s := startServe(t, w.env["a"])
	s.initialize(t)
	sp := spoolFor(w, "a")
	var out string
	id := 10
	eventually(t, 5*time.Second, "the session owns the spool", func() bool {
		id++
		return strings.Contains(statusOf(t, s, id), "hooks have not run")
	})
	time.Sleep(20 * time.Millisecond)
	if err := sp.NoteRefused(spool.Refusal{Time: time.Now(), Why: "session id differs"}); err != nil {
		t.Fatal(err)
	}
	eventually(t, 5*time.Second, "REFUSED shows", func() bool {
		id++
		out = statusOf(t, s, id)
		return strings.Contains(out, "delivery: REFUSED - mash's hook runs in this session but is not recognised as this session's (session id differs, last at ")
	})
	wantAll(t, "refused", out, "Next: start Claude Code with MASH_LAUNCH_TOKEN=$(uuidgen) claude ... (a fresh value per launch; one reused across launches defeats the check)", "docs/operations.md")
	if strings.Contains(out, "hooks have not run") {
		t.Fatalf("both lines shown:\n%s", out)
	}
}

func runStatusCLI(t *testing.T, env map[string]string) (string, int) {
	t.Helper()
	var out, errb strings.Builder
	code := run([]string{"status"}, envFrom(env), strings.NewReader(""), &out, &errb)
	if errb.Len() > 0 {
		t.Logf("stderr: %s", errb.String())
	}
	return out.String(), code
}

func TestStatusCLIInert(t *testing.T) {
	out, code := runStatusCLI(t, map[string]string{"HOME": t.TempDir()})
	if code != 0 {
		t.Fatalf("exit %d, want 0:\n%s", code, out)
	}
	wantAll(t, "cli inert", out, "name: MISSING - not set. Next: start Claude Code with MASH_NAME=<endpoint>",
		"config: MISSING", "connection: not checked from the CLI", "delivery: not checked from the CLI", "channel allowlist: ")
}

func TestStatusCLINoSecrets(t *testing.T) {
	home := t.TempDir()
	creds := filepath.Join(t.TempDir(), "a.creds")
	if err := os.WriteFile(creds, []byte("-----BEGIN NATS USER JWT-----\n"+credsSecret+"\n------END NATS USER JWT------\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	out, code := runStatusCLI(t, map[string]string{"HOME": home, "MASH_NAME": "a", "MASH_OWNER": "o", "MASH_CREDS": creds,
		"MASH_URL": "nats://user:hunter2pw@127.0.0.1:1"})
	if code != 0 {
		t.Fatalf("exit %d:\n%s", code, out)
	}
	wantAll(t, "cli valid", out, "name: OK - a", "config: OK", "REDACTED@", "creds: OK")
	for _, bad := range []string{credsSecret, "BEGIN NATS", "hunter2pw"} {
		if strings.Contains(out, bad) {
			t.Fatalf("status printed %q:\n%s", bad, out)
		}
	}
}

// The CLI and the tool share one builder: apart from the live-only lines, they print the same text.
func TestStatusCLIMatchesTool(t *testing.T) {
	home := t.TempDir()
	creds := filepath.Join(t.TempDir(), "a.creds")
	if err := os.WriteFile(creds, []byte("x\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	env := map[string]string{"HOME": home, "MASH_NAME": "a", "MASH_OWNER": "o", "MASH_CREDS": creds, "MASH_URL": "nats://127.0.0.1:1"}
	cliOut, _ := runStatusCLI(t, env)
	cfg, err := config.Load(envFrom(env), home)
	if err != nil {
		t.Fatal(err)
	}
	tool := statusText(&statusSource{cfg: cfg, srv: &atomic.Pointer[mcp.Server]{}, home: home, getenv: envFrom(env)})
	shared := func(s string) []string {
		var out []string
		for _, l := range strings.Split(s, "\n") {
			if !strings.HasPrefix(l, "connection:") && !strings.HasPrefix(l, "delivery:") {
				out = append(out, l)
			}
		}
		return out
	}
	if a, b := strings.Join(shared(strings.TrimRight(cliOut, "\n")), "\n"), strings.Join(shared(tool), "\n"); a != b {
		t.Fatalf("CLI and tool differ:\ncli:\n%s\ntool:\n%s", a, b)
	}
}
