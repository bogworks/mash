package main

import (
	"bytes"
	"io"
	"net"
	"strings"
	"testing"
	"time"
)

// The open and call timeouts are the configured ones: over a link whose round trip (40 ms) is longer than the
// timeout, a command or a tool call fails with the deadline, and with the defaults it does not.
func TestConfiguredTimeoutsApply(t *testing.T) {
	r := newRTTWorld(t, 6)
	envFor := func(as string, extra map[string]string) map[string]string {
		env := map[string]string{}
		for k, v := range r.env[as] {
			env[k] = v
		}
		env["MASH_URL"] = r.px.URL()
		for k, v := range extra {
			env[k] = v
		}
		return env
	}
	cli := func(env map[string]string, args ...string) (string, int) {
		var out, errb bytes.Buffer
		code := run(args, envFrom(env), strings.NewReader(""), &out, &errb)
		return out.String() + errb.String(), code
	}

	if out, code := cli(envFor("me", nil), "inbox"); code != 0 || !strings.Contains(out, "message") { // 25 messages wait
		t.Fatalf("defaults: exit %d: %s", code, out)
	}
	t.Run("cli open", func(t *testing.T) {
		out, code := cli(envFor("me", map[string]string{"MASH_OPEN_TIMEOUT": "10ms"}), "inbox")
		if code == 0 || !openTimedOut(out) {
			t.Fatalf("exit %d: %s", code, out)
		}
	})
	t.Run("cli call", func(t *testing.T) {
		out, code := cli(envFor("me", map[string]string{"MASH_CALL_TIMEOUT": "10ms"}), "inbox")
		if code == 0 || !strings.Contains(out, "deadline exceeded") {
			t.Fatalf("exit %d: %s", code, out)
		}
	})
	t.Run("serve call", func(t *testing.T) {
		s := startServe(t, envFor("peer1", map[string]string{"MASH_CALL_TIMEOUT": "10ms"}))
		s.initialize(t)
		eventually(t, 10*time.Second, "serve holding the endpoint", func() bool {
			_, ok := r.px.WavesThrough(consumerPull)
			return ok
		})
		res := s.call(t, 3, "inbox", `{}`)
		if res["isError"] != true || !strings.Contains(resultText(res), "deadline exceeded") {
			t.Fatalf("inbox with a 10 ms call timeout: %v", res)
		}
	})
	t.Run("serve open", func(t *testing.T) {
		s := startServe(t, envFor("peer1", map[string]string{"MASH_OPEN_TIMEOUT": "10ms"}))
		s.initialize(t)
		res := s.call(t, 3, "inbox", `{}`)
		if res["isError"] != true || !strings.Contains(resultText(res), "retrying") {
			t.Fatalf("a serve whose open timed out: %v", res)
		}
		if !openTimedOut(s.stderr()) {
			t.Fatalf("serve stderr: %q", s.stderr())
		}
	})
}

// codex M1 on #121: open_timeout bounds the NATS connect too. A server that accepts the TCP connection and never
// speaks NATS must fail the command within about the timeout, not after nats' own handshake wait.
func TestOpenTimeoutBoundsTheConnect(t *testing.T) {
	w := newWorld(t, "a")
	ln, err := net.Listen("tcp", "127.0.0.1:0") // accepts, never speaks NATS
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			t.Cleanup(func() { c.Close() })
		}
	}()
	env := map[string]string{}
	for k, v := range w.env["a"] {
		env[k] = v
	}
	env["MASH_URL"] = "nats://" + ln.Addr().String()
	env["MASH_OPEN_TIMEOUT"] = "300ms"
	var out, errb bytes.Buffer
	start := time.Now()
	code := run([]string{"inbox"}, envFrom(env), strings.NewReader(""), &out, &errb)
	if took := time.Since(start); code == 0 || took > 1500*time.Millisecond {
		t.Fatalf("exit %d after %v (open_timeout 300ms): %s%s", code, took, out.String(), errb.String())
	}
}

// slowReader yields its body only after a delay, like a pipe whose writer is slow.
type slowReader struct {
	delay time.Duration
	body  string
	done  bool
}

func (s *slowReader) Read(p []byte) (int, error) {
	if s.done {
		return 0, io.EOF
	}
	time.Sleep(s.delay)
	s.done = true
	return copy(p, s.body), nil
}

// codex on #121: the lease adoption in `mash send` is bounded by the configured call timeout, not a fixed five
// seconds. When stdin outlasts the prefetched lease's freshness the adoption makes a fresh lease request over the
// link; with a call timeout shorter than the link's round trip that request must fail with the deadline (a fixed
// 5 s would have let it succeed), and with the default it must adopt the lease's instance.
func TestSendAdoptionUsesTheCallTimeout(t *testing.T) {
	r := newRTTWorld(t, 6)
	send := func(callTimeout string) (string, int) {
		env := map[string]string{}
		for k, v := range r.env["me"] {
			env[k] = v
		}
		env["MASH_URL"] = r.px.URL()
		env["MASH_LAUNCH_TOKEN"] = rttToken
		if callTimeout != "" {
			env["MASH_CALL_TIMEOUT"] = callTimeout
		}
		var out, errb bytes.Buffer
		in := &slowReader{delay: 2500 * time.Millisecond, body: "late body"} // past the 2 s prefetch freshness
		code := run([]string{"send", "peer1", "-"}, envFrom(env), in, &out, &errb)
		return out.String() + errb.String(), code
	}
	const warn = "sending under this command's own instance"
	out, code := send("")
	if code != 0 || strings.Contains(out, warn) {
		t.Fatalf("default call timeout: exit %d, the stale prefetch must be refreshed and adopted: %s", code, out)
	}
	out, _ = send("10ms")
	if !strings.Contains(out, "deadline exceeded") || !strings.Contains(out, warn) {
		t.Fatalf("a 10 ms call timeout must bound the adoption's fresh lease request: %s", out)
	}
}

// openTimedOut reports whether text shows the open timeout firing: the deadline can end the NATS connect itself, which
// reports an i/o timeout, or a step after it, which reports the context's deadline.
func openTimedOut(text string) bool {
	return strings.Contains(text, "deadline exceeded") || strings.Contains(text, "i/o timeout")
}
