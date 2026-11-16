package main

import (
	"bytes"
	"context"
	"io"
	"strings"
	"sync"
	"testing"
	"time"
)

// syncBuf is a stderr that serve writes to and the test reads.
type syncBuf struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *syncBuf) Write(p []byte) (int, error) { s.mu.Lock(); defer s.mu.Unlock(); return s.b.Write(p) }
func (s *syncBuf) String() string              { s.mu.Lock(); defer s.mu.Unlock(); return s.b.String() }

// MASH_HOST=headless (ADR 0139): serve keeps the endpoint online with no MCP client. An empty or closed stdin does not
// end it, the endpoint shows online, mail sent to it is read with the CLI, and cancelling the context ends it cleanly
// and releases the lease.
func TestHeadlessServeStaysUpWithNoStdin(t *testing.T) {
	for name, stdin := range map[string]func() io.Reader{
		"empty":  func() io.Reader { return strings.NewReader("") },
		"closed": func() io.Reader { r, w := io.Pipe(); w.Close(); return r },
	} {
		t.Run(name, func(t *testing.T) {
			w := newWorld(t, "a", "b")
			w.env["a"]["MASH_HOST"] = "headless"
			kv := leaseKV(t, w)
			base := w.e.Connections()
			ctx, cancel := context.WithCancel(context.Background())
			t.Cleanup(cancel)
			var errb syncBuf
			done := make(chan int, 1)
			go func() { done <- runCtx(ctx, []string{"serve"}, envFrom(w.env["a"]), stdin(), io.Discard, &errb) }()

			eventually(t, 10*time.Second, "who shows a online", func() bool {
				out, _ := w.cli(t, "b", "who", "--names", "--online")
				return strings.Contains(out, "a")
			})
			// Serve does not end on an empty stdin: nothing reads it.
			select {
			case code := <-done:
				t.Fatalf("headless serve exited with %d on its own; stderr:\n%s", code, errb.String())
			case <-time.After(1500 * time.Millisecond):
			}
			if out, code := w.cli(t, "b", "send", "a", "hello headless"); code != 0 {
				t.Fatalf("send: %s", out)
			}
			eventually(t, 10*time.Second, "inbox shows the mail", func() bool {
				out, code := w.cli(t, "a", "inbox")
				return code == 0 && strings.Contains(out, "hello headless")
			})
			if out, code := w.cli(t, "a", "read", "1"); code != 0 || !strings.Contains(out, "> hello headless") {
				t.Fatalf("read: %d %s", code, out)
			}
			// No wake or hook accounting runs: nothing is written to stderr beyond the two lines serve says.
			if got := errb.String(); strings.Count(got, "\n") != 2 || !strings.Contains(got, "runs headless") || !strings.Contains(got, "is online") {
				t.Fatalf("serve stderr:\n%s", got)
			}

			cancel()
			select {
			case code := <-done:
				if code != 0 {
					t.Fatalf("exit code %d on cancel; stderr:\n%s", code, errb.String())
				}
			case <-time.After(5 * time.Second):
				t.Fatal("headless serve did not exit on cancel")
			}
			waitGone(t, w, base, "a", kv, true)
		})
	}
}

// A headless serve that cannot run (here: no endpoint name) says why on stderr and exits 1, instead of idling with no
// client to tell.
func TestHeadlessServeWithoutIdentityExits(t *testing.T) {
	w := newWorld(t, "a")
	env := map[string]string{"HOME": t.TempDir(), "MASH_HOST": "headless", "MASH_URL": w.env["a"]["MASH_URL"], "MASH_OWNER": w.env["a"]["MASH_OWNER"]}
	var errb syncBuf
	done := make(chan int, 1)
	go func() {
		done <- runCtx(context.Background(), []string{"serve"}, envFrom(env), strings.NewReader(""), io.Discard, &errb)
	}()
	select {
	case code := <-done:
		if code != 1 || !strings.Contains(errb.String(), "MASH_NAME") {
			t.Fatalf("code %d, stderr:\n%s", code, errb.String())
		}
	case <-time.After(10 * time.Second):
		t.Fatal("headless serve with no identity did not exit")
	}
}

// `mash status` with MASH_HOST=headless runs none of Claude Code's settings checks and does not fail.
func TestHeadlessStatusSkipsClaudeChecks(t *testing.T) {
	w := newWorld(t, "a")
	w.env["a"]["MASH_HOST"] = "headless"
	out, code := w.cli(t, "a", "status")
	if code != 0 || strings.Contains(out, "channel allowlist") || strings.Contains(out, "environment:") {
		t.Fatalf("status (%d):\n%s", code, out)
	}
	delete(w.env["a"], "MASH_HOST")
	if out, _ := w.cli(t, "a", "status"); !strings.Contains(out, "channel allowlist") {
		t.Fatalf("default status lost the allowlist line:\n%s", out)
	}
}
