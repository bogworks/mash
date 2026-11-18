package main

import (
	"bytes"
	"net"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/bogworks/mash/internal/testenv"
)

// connectCounter is a loopback listener (port 0) that records every connect and drops it: a refusal must make none.
type connectCounter struct {
	addr string
	n    atomic.Int64
}

func newConnectCounter(t *testing.T) *connectCounter {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	c := &connectCounter{addr: ln.Addr().String()}
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			c.n.Add(1)
			conn.Close()
		}
	}()
	t.Cleanup(func() { ln.Close() })
	return c
}

// plainEnv is an endpoint configuration whose server URL is url, with no override (REQ-014): the tests of the TLS
// requirement name exactly the variables they mean, and use envStrict.
func plainEnv(t *testing.T, url string) map[string]string {
	t.Helper()
	return map[string]string{"HOME": t.TempDir(), "MASH_NAME": "a", "MASH_GROUP": testGroup, "MASH_OWNER": "o", "MASH_CREDS": "/none",
		"MASH_URL": url}
}

// withRealCreds gives env a credentials file the identity checks accept, so that a connection attempt is reached.
// The URL stays the one under test: it points at a listener, not at the server that signed the credentials.
func withRealCreds(t *testing.T, env map[string]string) map[string]string {
	t.Helper()
	e := testenv.Start(t)
	creds, _ := e.Mint(t, tAddr("a"))
	env["MASH_CREDS"] = creds
	return env
}

// refusalWant are the parts every refusal of a plain URL has: the scheme, the override named both ways, and the fix.
func refusalWant(host string) []string {
	return []string{`refusing a plain nats:// connection to "` + host + `"`, "insecure_plaintext", "MASH_INSECURE_PLAINTEXT=1", "tls://", "tls_ca"}
}

func wantAllIn(t *testing.T, what, got string, wants []string) {
	t.Helper()
	for _, w := range wants {
		if !strings.Contains(got, w) {
			t.Errorf("%s %q lacks %q", what, got, w)
		}
	}
}

// Every plain URL is refused, loopback or remote, with a scheme or without, and the refusal does not leak userinfo.
func plainURLs(c *connectCounter) map[string]string { // url -> host named in the refusal
	host, port, _ := net.SplitHostPort(c.addr)
	return map[string]string{
		"nats://" + c.addr:                 host, // loopback, with a scheme
		c.addr:                             host, // loopback, no scheme
		"nats://u:s3cret@" + c.addr:        host,
		"nats://localhost:" + port:         "localhost",
		"nats://10.255.255.1:4222":         "10.255.255.1", // remote
		"nats.example.com:4222":            "nats.example.com",
		"nats://u:s3cret@10.255.255.1:422": "10.255.255.1",
	}
}

// serve stops at session start, exit 2, with the refusal on stderr and nothing on stdout (the MCP stream); it
// connects to nothing.
func TestServeRefusesPlainURLAndExitsTwo(t *testing.T) {
	c := newConnectCounter(t)
	for url, host := range plainURLs(c) {
		var out, errb bytes.Buffer
		code := run([]string{"serve"}, envStrict(plainEnv(t, url)), strings.NewReader(""), &out, &errb)
		if code != 2 {
			t.Errorf("%s: serve exited %d, want 2 (stderr %q)", url, code, errb.String())
		}
		if out.Len() != 0 {
			t.Errorf("%s: stdout %q", url, out.String())
		}
		wantAllIn(t, url+": stderr", errb.String(), refusalWant(host))
		if strings.Contains(errb.String(), "s3cret") {
			t.Errorf("%s: stderr leaks the password: %q", url, errb.String())
		}
	}
	if n := c.n.Load(); n != 0 {
		t.Fatalf("%d connections reached the server, want none", n)
	}
}

// The hook connects to nothing under a refused configuration: it says why on stderr (the hook log) and exits 0.
func TestHookRefusesPlainURLSaysSoAndExitsZero(t *testing.T) {
	c := newConnectCounter(t)
	for url, host := range plainURLs(c) {
		var out, errb bytes.Buffer
		code := run([]string{"hook"}, envStrict(plainEnv(t, url)), strings.NewReader(`{"hook_event_name":"UserPromptSubmit"}`), &out, &errb)
		if code != 0 {
			t.Errorf("%s: hook exited %d", url, code)
		}
		if out.Len() != 0 {
			t.Errorf("%s: stdout %q", url, out.String())
		}
		wantAllIn(t, url+": stderr", errb.String(), append(refusalWant(host), "mash hook:"))
		if strings.Contains(errb.String(), "s3cret") {
			t.Errorf("%s: stderr leaks the password: %q", url, errb.String())
		}
	}
	if n := c.n.Load(); n != 0 {
		t.Fatalf("%d connections reached the server, want none", n)
	}
}

// The CLI commands refuse before connecting, exit 2, and print the refusal.
func TestCLIRefusesPlainURLExitTwo(t *testing.T) {
	c := newConnectCounter(t)
	for url, host := range plainURLs(c) {
		for _, cmd := range [][]string{{"inbox"}, {"who"}, {"read", "1"}, {"send", tTo("b"), "hi"}} {
			var out, errb bytes.Buffer
			code := run(cmd, envStrict(plainEnv(t, url)), strings.NewReader(""), &out, &errb)
			got := out.String() + errb.String()
			if code != 2 {
				t.Errorf("%s %v: exit %d, want 2: %q", url, cmd, code, got)
			}
			wantAllIn(t, url+" "+strings.Join(cmd, " ")+": output", got, refusalWant(host))
			if strings.Contains(got, "s3cret") {
				t.Errorf("%s %v: leaks the password: %q", url, cmd, got)
			}
		}
	}
	if n := c.n.Load(); n != 0 {
		t.Fatalf("%d connections reached the server, want none", n)
	}
}

// foo:// is refused as unsupported, with and without the override, and nothing connects.
func TestUnsupportedSchemeRefusedEvenWithTheOverride(t *testing.T) {
	c := newConnectCounter(t)
	for _, override := range []string{"", "1"} {
		env := plainEnv(t, "foo://"+c.addr)
		env["MASH_INSECURE_PLAINTEXT"] = override
		for _, cmd := range [][]string{{"serve"}, {"inbox"}} {
			var out, errb bytes.Buffer
			code := run(cmd, envStrict(env), strings.NewReader(""), &out, &errb)
			if code != 2 || !strings.Contains(errb.String(), `unsupported scheme "foo"`) {
				t.Errorf("override %q %v: exit %d, stderr %q", override, cmd, code, errb.String())
			}
		}
	}
	if n := c.n.Load(); n != 0 {
		t.Fatalf("%d connections reached the server, want none", n)
	}
}

// With the override a plain connection is made: the CLI command connects (the listener records it) and prints the
// warning on stderr, once, and nothing of it goes to stdout.
func TestCLIWithTheOverrideConnectsAndWarnsOnStderrOnce(t *testing.T) {
	c := newConnectCounter(t)
	env := withRealCreds(t, plainEnv(t, "nats://"+c.addr))
	env["MASH_INSECURE_PLAINTEXT"] = "1"
	env["MASH_OPEN_TIMEOUT"] = "300ms"
	var out, errb bytes.Buffer
	run([]string{"inbox"}, envStrict(env), strings.NewReader(""), &out, &errb) // the listener is not a server: it fails
	if c.n.Load() == 0 {
		t.Fatal("no connection was made with the override set")
	}
	if n := strings.Count(errb.String(), "WARNING: plain-text connection to 127.0.0.1"); n != 1 {
		t.Errorf("%d warnings on stderr, want 1: %q", n, errb.String())
	}
	if strings.Contains(out.String(), "WARNING") {
		t.Errorf("the warning is on stdout: %q", out.String())
	}
}

// serve with the override prints the warning on stderr once (at start, not at each retry) and never on stdout, which is
// the MCP stream; the connection is made.
func TestServeWithTheOverrideWarnsOnStderrNotStdout(t *testing.T) {
	c := newConnectCounter(t)
	env := withRealCreds(t, plainEnv(t, "nats://"+c.addr))
	env["MASH_INSECURE_PLAINTEXT"] = "1"
	s := startServe(t, env)
	s.initialize(t)
	status := resultText(s.call(t, 7, "status", `{}`))
	if l := statusLine(t, status, "tls: "); !strings.HasPrefix(l, "tls: WARN - ") || !strings.Contains(l, "insecure_plaintext") {
		t.Errorf("status tool tls line %q", l)
	}
	if c.n.Load() == 0 {
		t.Error("no connection was made with the override set")
	}
	if n := strings.Count(s.stderr(), "WARNING: plain-text connection"); n != 1 {
		t.Errorf("%d warnings on stderr, want 1: %q", n, s.stderr())
	}
}
