package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/bogworks/mash/internal/endpoint"
)

// The relay-trace line: "none recorded" before anything, the live counters inside serve, and the counters file the
// serve of the endpoint wrote for the status command run from a shell (ADR 0064).
func TestStatusRelayTraceLine(t *testing.T) {
	w := newWorld(t, "a", "b", "c")
	if out, code := w.cli(t, "b", "status"); code != 0 || !strings.Contains(out, "\nrelay trace: none recorded\n") {
		t.Fatalf("no file: %d\n%s", code, out)
	}
	s := startServe(t, w.env["b"])
	s.initialize(t)
	if out := statusOf(t, s, 10); !strings.Contains(out, "relay trace: inbound with via 0, sends within 60s of another sender's mail 0, declared relays 0") {
		t.Fatalf("zero counters:\n%s", out)
	}
	if out, code := w.cli(t, "c", "send", "b", "the original"); code != 0 {
		t.Fatalf("send: %d %s", code, out)
	}
	id := 20
	eventually(t, 10*time.Second, "serve received c's mail", func() bool {
		id++
		return strings.Contains(statusOf(t, s, id), "channel notifications sent: 1")
	})
	if r := s.call(t, 30, "send", `{"to":"a","text":"fwd","via":1}`); r["isError"] == true {
		t.Fatalf("send: %v", resultText(r))
	}
	want := "relay trace: inbound with via 0, sends within 60s of another sender's mail 1, declared relays 1"
	if l := statusLine(t, statusOf(t, s, 31), "relay trace: "); l != want {
		t.Fatalf("in-process %q", l)
	}
	// From a shell: the file, with the start of the run it came from. It is written within a second of the change.
	var l string
	eventually(t, 10*time.Second, "counters file", func() bool {
		out, _ := w.cli(t, "b", "status")
		l = statusLine(t, out, "relay trace: ")
		return strings.HasPrefix(l, want)
	})
	if !strings.Contains(l, " (since 20") {
		t.Fatalf("cli line %q", l)
	}
	p := filepath.Join(spoolFor(w, "b").Dir, endpoint.RelayCountersFile)
	if fi, err := os.Stat(p); err != nil || fi.Mode().Perm() != 0o600 {
		t.Fatalf("counters file: %v %v", fi, err)
	}
	// An unreadable file is the same as none.
	if err := os.WriteFile(p, []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	if out, _ := w.cli(t, "b", "status"); !strings.Contains(out, "\nrelay trace: none recorded\n") {
		t.Fatalf("garbled file:\n%s", out)
	}
}
