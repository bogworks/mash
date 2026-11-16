package main

import (
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/bogworks/mash/internal/config"
	"github.com/bogworks/mash/internal/endpoint"
	"github.com/bogworks/mash/internal/host"
	"github.com/bogworks/mash/internal/spool"
)

// The strings that come from the host's Words are pinned byte for byte: moving them behind the adapter changed none.
func TestHostWordsGoldenStrings(t *testing.T) {
	w := host.Claude{}.Words()
	if got, want := otherLaunchNotice(w, 4242, "alpha"),
		"mash: this copy of the session is not receiving mail. Another Claude process of this same session (pid 4242) holds the endpoint alpha; mail goes to that process. End one of the two, or run this one under a different MASH_NAME."; got != want {
		t.Errorf("other-launch notice:\n got %q\nwant %q", got, want)
	}
	if got, want := nameLine(config.Config{}), "name: MISSING - not set. Next: start Claude Code with MASH_NAME=<endpoint>"; got != want {
		t.Errorf("name missing %q", got)
	}
	if got, want := nameLine(config.Config{Name: "Bad_Name"}),
		`name: FAILED - "Bad_Name" is not a valid endpoint name. Next: start Claude Code with MASH_NAME=<endpoint>, using 1-48 lower-case letters, digits and single hyphens, starting with a letter`; got != want {
		t.Errorf("name invalid %q", got)
	}
	st := &atomic.Pointer[sessionState]{}
	st.Store(&sessionState{failed: errors.New("boom")})
	if got, want := connectionLine(&statusSource{cfg: config.Config{Name: "a"}, state: st}),
		"connection: FAILED - boom. Next: fix the cause (see the creds and config lines) and restart Claude Code"; got != want {
		t.Errorf("connection failed %q", got)
	}
	if got, want := endpoint.InertInstructions(w.Product),
		"mash is installed but this session has no mash identity (MASH_NAME is not set), so mail is off. Start Claude Code with MASH_NAME=<endpoint> to give this session a mailbox. Call the status tool to see what is missing."; got != want {
		t.Errorf("inert instructions %q", got)
	}
	sp := spool.Spool{Dir: t.TempDir()}
	if err := sp.Init(); err != nil {
		t.Fatal(err)
	}
	if err := sp.NoteRefused(spool.Refusal{Time: time.Now().UTC(), Why: "x"}); err != nil {
		t.Fatal(err)
	}
	if got, want := hooklessFix(sp, time.Now().Add(-time.Hour), w), "start Claude Code with MASH_LAUNCH_TOKEN=<any unique value>, and report your setup (see docs/operations.md)"; got != want {
		t.Errorf("hookless fix %q", got)
	}
}
