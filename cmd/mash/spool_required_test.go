package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/bogworks/mash/internal/spool"
)

// Through serve, MASH_SPOOL_REQUIRED=1 with a spool that cannot be made: mail is neither delivered by the channel nor
// acked, stderr says it is held and how to read it, and once the spool can be made the background
// retry binds it, the held mail is spooled, and a hook drains it and serve acks it.
func TestServeSpoolRequiredHoldsMailAndBindsWhenTheSpoolRecovers(t *testing.T) {
	w := newWorld(t, "a", "b")
	w.env["a"]["MASH_SPOOL_REQUIRED"] = "1"
	// The state directory is named explicitly (MASH_STATE_DIR), so the test never depends on HOME or XDG_STATE_HOME.
	state := filepath.Join(t.TempDir(), "state")
	w.env["a"]["MASH_STATE_DIR"] = state
	if err := os.WriteFile(state, nil, 0o600); err != nil { // a file where the state directory goes: no spool directory
		t.Fatal(err)
	}
	s := startServe(t, w.env["a"])
	s.initialize(t)
	s.tools(t)
	eventually(t, 5*time.Second, "a online", func() bool { out, _ := w.cli(t, "a", "who"); return whoShows(out, tTo("a"), "online") })
	if _, code := w.cli(t, "b", "send", tTo("a"), "must stay unacked"); code != 0 {
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
	// Fix the spool directory: the retry binds it.
	if err := os.Rename(state, state+".was"); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(state, 0o700); err != nil {
		t.Fatal(err)
	}
	sp := spool.Spool{Dir: spool.Dir(state, endpointPart(w.env["a"], 0), tAddr(endpointPart(w.env["a"], 2)).Flat())}
	eventually(t, 3*spoolRetryEvery+30*time.Second, "the held mail is spooled", func() bool { e, _ := sp.Waiting(); return len(e) == 1 })
	if n := unreadOf(t, w, "a"); n != 1 {
		t.Fatalf("unread %d, want 1: spooled, not yet drained", n)
	}
	got := runHook(t, w.env["a"], "UserPromptSubmit")
	if !strings.Contains(got, "> must stay unacked") {
		t.Fatalf("hook context lacks the body:\n%s", got)
	}
	eventually(t, 5*time.Second, "serve acks what the hook showed", func() bool { return unreadOf(t, w, "a") == 0 })
}
