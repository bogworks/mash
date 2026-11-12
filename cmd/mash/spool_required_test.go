package main

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/bogworks/mash/internal/config"
	"github.com/bogworks/mash/internal/endpoint"
)

// The status tool's line for a failing spool (MASH_SPOOL_REQUIRED): present while the fault stands, plain, and gone
// once the spool works; with the setting off there is never one.
func TestStatusSpoolFaultLine(t *testing.T) {
	ep := &endpoint.Endpoint{}
	st := &atomic.Pointer[sessionState]{}
	st.Store(&sessionState{ep: ep})
	src := &statusSource{cfg: config.Config{Name: "a"}, state: st}
	if l := spoolFaultLine(src); l != "" {
		t.Fatalf("off: line %q, want none", l)
	}
	ep.ReportSpoolFaults(func(string) {})
	if l := spoolFaultLine(src); l != "" {
		t.Fatalf("healthy: line %q, want none", l)
	}
	ep.NoteSpoolFault(errors.New("disk full"))
	want := "spool: FAILING - mail is being held unacked in the inbox because the spool is failing: disk full; read it with the inbox and read tools; it is delivered once the spool recovers"
	if l := spoolFaultLine(src); l != want {
		t.Fatalf("line %q, want %q", l, want)
	}
	ep.ClearSpoolFault()
	if l := spoolFaultLine(src); l != "" {
		t.Fatalf("recovered: line %q, want none", l)
	}
	if l := spoolFaultLine(&statusSource{cfg: config.Config{Name: "a"}, state: st, cli: true}); l != "" {
		t.Fatalf("shell: line %q, want none", l)
	}
}

// Through serve, MASH_SPOOL_REQUIRED=1 with a spool that cannot be made: mail is neither delivered by the channel nor
// acked, stderr and the status tool say it is held and how to read it, and once the spool can be made the background
// retry binds it, the held mail is spooled, and a hook drains it and serve acks it.
func TestServeSpoolRequiredHoldsMailAndBindsWhenTheSpoolRecovers(t *testing.T) {
	w := newWorld(t, "a", "b")
	w.env["a"]["MASH_SPOOL_REQUIRED"] = "1"
	state := filepath.Join(w.env["a"]["HOME"], ".local", "state")
	if err := os.MkdirAll(filepath.Dir(state), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(state, nil, 0o600); err != nil { // a file where the state directory goes: no spool directory
		t.Fatal(err)
	}
	s := startServe(t, w.env["a"])
	s.initialize(t)
	s.tools(t)
	eventually(t, 5*time.Second, "a online", func() bool { out, _ := w.cli(t, "a", "who"); return strings.Contains(out, "a\tonline") })
	if _, code := w.cli(t, "b", "send", "a", "must stay unacked"); code != 0 {
		t.Fatal("send failed")
	}
	notice := content(s.await(t, 15*time.Second, isChannel))
	if strings.Contains(notice, "must stay unacked") || !strings.Contains(notice, "held unacked in the inbox because the spool is failing") || !strings.Contains(notice, "inbox and read tools") {
		t.Fatalf("channel event %q, want the notice and no body", notice)
	}
	time.Sleep(3 * time.Second)
	if n := unreadOf(t, w, "a"); n != 1 {
		t.Fatalf("unread %d, want 1: the mail stays unacked", n)
	}
	if e := s.stderr(); !strings.Contains(e, "mash: spool: mail is being held unacked in the inbox because the spool is failing: ") || !strings.Contains(e, "read it with the inbox and read tools") {
		t.Fatalf("stderr lacks the line:\n%s", e)
	}
	if txt := resultText(s.call(t, 30, "status", `{}`)); !strings.Contains(txt, "spool: FAILING - mail is being held unacked in the inbox because the spool is failing: ") ||
		!strings.Contains(txt, "read it with the inbox and read tools; it is delivered once the spool recovers") {
		t.Fatalf("status lacks the line:\n%s", txt)
	}
	// Fix the spool directory: the retry binds it.
	if err := os.Rename(state, state+".was"); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(state, 0o700); err != nil {
		t.Fatal(err)
	}
	sp := spoolOf(w.env["a"])
	eventually(t, 3*spoolRetryEvery+30*time.Second, "the held mail is spooled", func() bool { e, _ := sp.Waiting(); return len(e) == 1 })
	if n := unreadOf(t, w, "a"); n != 1 {
		t.Fatalf("unread %d, want 1: spooled, not yet drained", n)
	}
	if txt := resultText(s.call(t, 31, "status", `{}`)); strings.Contains(txt, "spool: FAILING") {
		t.Fatalf("status still has the line:\n%s", txt)
	}
	got := runHook(t, w.env["a"], "UserPromptSubmit")
	if !strings.Contains(got, "> must stay unacked") {
		t.Fatalf("hook context lacks the body:\n%s", got)
	}
	eventually(t, 5*time.Second, "serve acks what the hook showed", func() bool { return unreadOf(t, w, "a") == 0 })
}
