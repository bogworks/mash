package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/bogworks/mash/internal/endpoint"
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

// Two headless serves of one endpoint under one parent (the shell that started both) are never one session: the
// second waits, does not take the live lease, and gets it once the first has gone (codex H1, #705 round 2).
func TestHeadlessServesUnderOneParentDoNotTakeEachOthersLease(t *testing.T) {
	if os.Getppid() <= 1 {
		t.Skip("no usable parent pid")
	}
	setTiming(t, 200*time.Millisecond, 20*time.Second)
	endpoint.LeaseRefresh = 100 * time.Millisecond
	// The same parent and no session id, as two serves started from one shell would have.
	old := endpoint.OurSession
	endpoint.OurSession = func() endpoint.SessionIdent {
		return endpoint.SessionIdent{PPID: strconv.Itoa(os.Getppid()), PStart: "777"}
	}
	t.Cleanup(func() { endpoint.OurSession = old; endpoint.SetNoSessionIdentity(false) })
	w := newWorld(t, "a")
	w.env["a"]["MASH_HOST"] = "headless"
	kv := leaseKV(t, w)
	holder := func() map[string]any {
		e, err := kv.Get(context.Background(), "a")
		if err != nil {
			return nil
		}
		var m map[string]any
		_ = json.Unmarshal(e.Value(), &m)
		return m
	}
	start := func() (context.CancelFunc, *syncBuf, chan int) {
		ctx, cancel := context.WithCancel(context.Background())
		t.Cleanup(cancel)
		errb := &syncBuf{}
		done := make(chan int, 1)
		go func() {
			done <- runCtx(ctx, []string{"serve"}, envFrom(w.env["a"]), strings.NewReader(""), io.Discard, errb)
		}()
		return cancel, errb, done
	}
	cancel1, err1, done1 := start()
	eventually(t, 10*time.Second, "first is online", func() bool { return strings.Contains(err1.String(), "is online") })
	first := holder()
	if first == nil || first["ppid"] != nil && first["ppid"] != "" {
		t.Fatalf("a headless lease records a parent identity: %v", first)
	}
	cancel2, err2, done2 := start()
	time.Sleep(1500 * time.Millisecond) // several retries of the second
	if got := holder(); got == nil || got["instance_id"] != first["instance_id"] {
		t.Fatalf("the second headless serve took the live lease: %v, was %v", got, first)
	}
	if strings.Contains(err2.String(), "is online") {
		t.Fatalf("the second serve says it is online while the first holds the lease:\n%s", err2.String())
	}
	cancel1()
	if code := <-done1; code != 0 {
		t.Fatalf("first exit code %d", code)
	}
	defer func() {
		if t.Failed() {
			t.Logf("second stderr:\n%s\nholder %v", err2.String(), holder())
		}
	}()
	eventually(t, 10*time.Second, "second gets the lease after the first exits", func() bool {
		got := holder()
		return got != nil && got["instance_id"] != first["instance_id"] && strings.Contains(err2.String(), "is online")
	})
	cancel2()
	if code := <-done2; code != 0 {
		t.Fatalf("second exit code %d", code)
	}
}

// A headless serve whose SAP service cannot be registered says why on stderr, ends and exits 1; it does not go on as
// an endpoint that shows offline and answers nothing (codex H3, #705 round 2). A hosted serve still carries on.
func TestHeadlessServeExitsWhenServiceRegistrationFails(t *testing.T) {
	old := startService
	startService = func(*endpoint.Endpoint, context.Context, func(endpoint.PromptEvent)) error {
		return errors.New("mash: SAP registration: injected failure")
	}
	t.Cleanup(func() { startService = old })
	w := newWorld(t, "a")
	w.env["a"]["MASH_HOST"] = "headless"
	var errb syncBuf
	done := make(chan int, 1)
	go func() {
		done <- runCtx(context.Background(), []string{"serve"}, envFrom(w.env["a"]), strings.NewReader(""), io.Discard, &errb)
	}()
	select {
	case code := <-done:
		if code != 1 || !strings.Contains(errb.String(), "injected failure") || !strings.Contains(errb.String(), "cannot register its service") {
			t.Fatalf("code %d, stderr:\n%s", code, errb.String())
		}
	case <-time.After(10 * time.Second):
		t.Fatalf("headless serve did not exit on a registration failure; stderr:\n%s", errb.String())
	}
}
