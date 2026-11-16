package host

import (
	"slices"
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
	_ = h.OwnerToken(func(string) string { return "" })
	_, _ = h.Match("", HookIdentity{})
	_ = h.Owns("{bad", HookIdentity{})
	if out, shown := h.Present(Presentation{Name: "a"}, nil); out == nil && shown != nil {
		t.Error("Present(nil) misbehaved")
	}
}
