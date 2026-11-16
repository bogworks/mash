package host

import (
	"os"
	"slices"
	"strconv"
	"strings"
	"testing"
)

func TestSelectHeadless(t *testing.T) {
	a, err := Select("headless")
	if err != nil || a.Name() != "headless" || !IsHeadless(a) {
		t.Fatalf("Select(headless) = %v, %v", a, err)
	}
	if !slices.Contains(Known(), "headless") || !slices.Contains(Known(), "claude") {
		t.Fatalf("Known() = %v", Known())
	}
	// No detection: the empty name is still Claude Code.
	d, err := Select("")
	if err != nil || d.Name() != "claude" || IsHeadless(d) {
		t.Fatalf("Select(\"\") = %v, %v", d, err)
	}
	if _, err := Select("nope"); err == nil || !strings.Contains(err.Error(), "headless") {
		t.Fatalf("an unknown host lists the known ones: %v", err)
	}
}

func TestHeadlessAdapter(t *testing.T) {
	h := Headless{}
	if got := h.SessionSources(); got != (SessionSources{Env: "", HookField: "", AncestryUnique: true}) {
		t.Errorf("SessionSources = %+v", got)
	}
	if h.Wake() != WakeNone || !h.SpoolsFromStart() {
		t.Errorf("wake %v, spools from start %v", h.Wake(), h.SpoolsFromStart())
	}
	if got, want := h.Words(), (Words{Product: "mash serve (headless)", Process: "serve process", ServicePrefix: "mash (headless): "}); got != want {
		t.Errorf("Words = %+v", got)
	}
	if h.ClaudeSettingsChecks() {
		t.Error("headless has no Claude settings checks")
	}
	if h.Waker(func(string, map[string]string) error { return nil }) != nil {
		t.Error("headless has no wake: its Waker is nil")
	}
	// No hook is ever a delivery point, whatever the input.
	for _, in := range []string{``, `{}`, `{"hook_event_name":"PostToolUse","session_id":"s"}`, `{"hook_event_name":"UserPromptSubmit"}`} {
		if _, ok := h.Trigger([]byte(in), func(string) string { return "x" }); ok {
			t.Errorf("Trigger(%q) is a delivery point", in)
		}
	}
	// The unused methods are safe on nil and empty input.
	if SessionID(h, func(string) string { return "leaks" }) != "" {
		t.Error("headless reads no session id, even from an environment that has one")
	}
	_ = h.OwnerToken(nil)
	_, _ = h.Match("", HookIdentity{})
	_ = h.Owns("{bad", HookIdentity{})
	if out, shown := h.Present(Presentation{Name: "a"}, nil); out == nil && shown != nil {
		t.Error("Present(nil) misbehaved")
	}
}

// The headless owner record names the host and carries neither a session id nor a launch token, whatever the
// environment holds; a Claude Code hook with a matching session, launch token and ancestry is refused on it (H2).
func TestHeadlessOwnerRecordIsNotClaimable(t *testing.T) {
	env := func(k string) string {
		return map[string]string{"CLAUDE_CODE_SESSION_ID": "S1", "MASH_LAUNCH_TOKEN": "tok"}[k]
	}
	got := Headless{}.OwnerToken(env)
	if want := `{"pid":` + strconv.Itoa(os.Getpid()) + `,"host":"headless"}`; got != want {
		t.Fatalf("owner record %s, want %s", got, want)
	}
	for _, id := range []HookIdentity{
		{SessionID: "S1", LaunchToken: "tok"},
		{SessionID: "S1"},
		{LaunchToken: "tok"},
	} {
		if ok, why := (Claude{}).Match(got, id); ok || !strings.Contains(why, "headless") {
			t.Errorf("Claude matched a headless owner for %+v: %v %q", id, ok, why)
		}
	}
	// Ancestry too: a hook whose ancestors include serve's pid and serve's parent.
	if ok, _ := (Claude{}).Match(got, HookIdentity{SessionID: "S1", LaunchToken: "tok", Waiters: []int{os.Getppid()}}); ok {
		t.Error("Claude matched a headless owner")
	}
	if OwnerPID(got) != os.Getpid() {
		t.Errorf("OwnerPID = %d", OwnerPID(got))
	}
}
