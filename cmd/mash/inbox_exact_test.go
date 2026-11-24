package main

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/bogworks/mash/internal/endpoint"
)

const noServeWhy = "mash serve is not running, so mail it has not verified is still counted"

func exactOf(t *testing.T, s string) bool {
	t.Helper()
	var m struct {
		Exact *bool `json:"exact"`
	}
	if err := json.Unmarshal([]byte(s), &m); err != nil || m.Exact == nil {
		t.Fatalf("no exact in %q (%v)", s, err)
	}
	return *m.Exact
}

// #958, REQ-011, on a real endpoint. serve running (a live lease), count not capped: the count is exact and prints as it
// always did; "exact": true in --json and nothing under --detail.
func TestInboxCountIsExactWithServeRunning(t *testing.T) {
	w, _ := hooklessWorld(t, 150*time.Millisecond)
	sendAndWaitSpooled(t, w, "the body")
	out, code := w.cli(t, "a", "inbox")
	if code != 0 || !strings.HasPrefix(out, "1 message for o.dev.a, 1 new, 0 urgent.\n\nNUMBER") {
		t.Fatalf("inbox: %d %q", code, out)
	}
	if d, _ := w.cli(t, "a", "inbox", "--detail"); d != out {
		t.Errorf("--detail on an exact count: %q, want %q", d, out)
	}
	if j, _ := w.cli(t, "a", "inbox", "--json"); !exactOf(t, j) {
		t.Errorf("--json: %s", j)
	}
}

// serve not running, unverified mail waiting: "about", exact false, the reason under --detail, no extra line.
func TestInboxCountIsAboutWithoutServe(t *testing.T) {
	w := newWorld(t, "a", "b")
	if out, code := w.cli(t, "b", "send", tTo("a"), "hello"); code != 0 {
		t.Fatalf("send: %s", out)
	}
	out, code := w.cli(t, "a", "inbox")
	if code != 0 || !strings.HasPrefix(out, "About 1 message for o.dev.a, about 1 new, about 0 urgent.\n\nNUMBER") || strings.Contains(out, "approximate") {
		t.Fatalf("inbox: %d %q", code, out)
	}
	if d, _ := w.cli(t, "a", "inbox", "--detail"); !strings.HasSuffix(d, "\nexact: no ("+noServeWhy+")\n") {
		t.Errorf("--detail: %q", d)
	}
	if j, _ := w.cli(t, "a", "inbox", "--json"); exactOf(t, j) {
		t.Errorf("--json: %s", j)
	}
	// no mail, nothing to verify: exact
	if out, _ := w.cli(t, "b", "inbox"); out != "No messages for o.dev.b.\n" {
		t.Errorf("empty inbox: %q", out)
	}
}

// The count pass capped, serve running: "about", and the reason names the cap. Capped without serve: said once, the cap first.
func TestInboxCountIsAboutWhenCapped(t *testing.T) {
	w, _ := hooklessWorld(t, 150*time.Millisecond)
	for i := 0; i < 4; i++ {
		if out, code := w.cli(t, "b", "send", tTo("a"), "m"); code != 0 {
			t.Fatalf("send: %s", out)
		}
	}
	defer endpoint.SetCountCaps(2, 1<<30)()
	out, _ := w.cli(t, "a", "inbox", "--limit", "1")
	if !strings.HasPrefix(out, "About 4 messages for o.dev.a, about 4 new, about 0 urgent.\n") {
		t.Fatalf("capped: %q", out)
	}
	if d, _ := w.cli(t, "a", "inbox", "--limit", "1", "--detail"); !strings.HasSuffix(d, "\nexact: no (the count stopped at its limit of 2 messages)\n") {
		t.Errorf("--detail: %q", d)
	}
	if j, _ := w.cli(t, "a", "inbox", "--limit", "1", "--json"); exactOf(t, j) {
		t.Errorf("--json: %s", j)
	}
	defer endpoint.SetCountCaps(1, 1<<30)()
	nw := newWorld(t, "a", "b")
	for i := 0; i < 3; i++ {
		nw.cli(t, "b", "send", tTo("a"), "m")
	}
	d, _ := nw.cli(t, "a", "inbox", "--limit", "1", "--detail")
	if !strings.HasPrefix(d, "About 3 messages for o.dev.a, about 3 new") || strings.Count(d, "About") != 1 ||
		!strings.HasSuffix(d, "\nexact: no (the count stopped at its limit of 1 message; "+noServeWhy+")\n") {
		t.Errorf("both: %q", d)
	}
}
