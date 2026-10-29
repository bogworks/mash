package host

import (
	"encoding/json"
	"os"
	"slices"
	"strconv"
	"strings"
	"testing"
)

// The real ancestry on this OS includes the parent process.
func TestAncestorsIncludesParent(t *testing.T) {
	var got []int
	for pid := range Ancestors {
		got = append(got, pid)
	}
	if len(got) == 0 || got[0] != os.Getppid() {
		t.Fatalf("Ancestors() = %v, parent %d", got, os.Getppid())
	}
}

func TestUTF16Len(t *testing.T) {
	for _, c := range []struct {
		s string
		n int
	}{{"", 0}, {"abc", 3}, {"é", 1}, {"😀", 2}, {"a😀é", 4}} {
		if got := utf16Len(c.s); got != c.n {
			t.Errorf("utf16Len(%q) = %d, want %d", c.s, got, c.n)
		}
	}
}

func TestSelect(t *testing.T) {
	for _, n := range []string{"", "claude"} {
		a, err := Select(n, "")
		if err != nil || a.Name() != "claude" {
			t.Fatalf("Select(%q) = %v, %v", n, a, err)
		}
	}
	if _, err := Select("nope", ""); err == nil || !strings.Contains(err.Error(), "nope") || !strings.Contains(err.Error(), "claude") {
		t.Fatalf("Select(nope) err = %v", err)
	}
}

func TestClaudeTrigger(t *testing.T) {
	c := Claude{}
	for in, want := range map[string]bool{
		`{"hook_event_name":"PostToolUse"}`:      true,
		`{"hook_event_name":"UserPromptSubmit"}`: true,
		`{"hook_event_name":"Stop"}`:             false,
		`not json`:                               false,
	} {
		if _, ok := c.Trigger([]byte(in), nil); ok != want {
			t.Errorf("Trigger(%s) ok = %v, want %v", in, ok, want)
		}
	}
}

// A subagent's hook is a trigger, reported as a subagent's so the caller never drains for it.
func TestClaudeTriggerReportsASubagent(t *testing.T) {
	tr, ok := Claude{}.Trigger([]byte(`{"hook_event_name":"PostToolUse","agent_id":"sub","session_id":"s1"}`), nil)
	if !ok || !tr.Subagent || tr.SessionID != "s1" {
		t.Fatalf("Trigger = %+v, %v", tr, ok)
	}
	tr, ok = Claude{}.Trigger([]byte(`{"hook_event_name":"PostToolUse","session_id":"s1"}`), nil)
	if !ok || tr.Subagent {
		t.Fatalf("a main session's Trigger = %+v, %v", tr, ok)
	}
}

// stubParents makes parentOf walk a fake chain (pid -> parent) and counts its calls.
func stubParents(t *testing.T, chain map[int]int) *int {
	t.Helper()
	old := parentOf
	calls := 0
	parentOf = func(pid int) (int, error) {
		calls++
		next, ok := chain[pid]
		if !ok {
			return 0, os.ErrNotExist
		}
		return next, nil
	}
	t.Cleanup(func() { parentOf = old })
	return &calls
}

// Owns walks up lazily and stops at the owner: the common case (the owner is the hook's parent or grandparent)
// costs at most one lookup, not ten (the review's item 9: on macOS each lookup was a forked ps).
func TestOwnsStopsAtTheOwner(t *testing.T) {
	pp := os.Getppid()
	chain := map[int]int{pp: 5001, 5001: 5002, 5002: 5003, 5003: 5004, 5004: 5005, 5005: 5006, 5006: 5007, 5007: 5008, 5008: 5009, 5009: 5010}
	for _, c := range []struct {
		owner     string
		want      bool
		maxCalls  int
		wantExact int // -1: use maxCalls
	}{
		{"pid:" + strconv.Itoa(pp), true, 0, 0},
		{"pid:5001", true, 1, 1},
		{"pid:5002", true, 2, 2},
		{"pid:4242", false, 10, -1},
		{"", true, 0, 0},
		{"older-session-id", true, 0, 0},
	} {
		calls := stubParents(t, chain)
		if got := (Claude{}).Owns(c.owner, HookIdentity{}); got != c.want {
			t.Errorf("Owns(%q) = %v, want %v", c.owner, got, c.want)
		}
		if *calls > c.maxCalls || (c.wantExact >= 0 && *calls != c.wantExact) {
			t.Errorf("Owns(%q) made %d parent lookups, want %d", c.owner, *calls, c.maxCalls)
		}
	}
}

// Ancestors is the same walk, run to the end, nearest first.
func TestAncestorsWalksTheChain(t *testing.T) {
	pp := os.Getppid()
	stubParents(t, map[int]int{pp: 7001, 7001: 7002, 7002: 1})
	var got []int
	for pid := range Ancestors {
		got = append(got, pid)
	}
	if want := []int{pp, 7001, 7002}; !slices.Equal(got, want) {
		t.Fatalf("Ancestors = %v, want %v", got, want)
	}
}

// Claude takes channel events, so its mail is the channel's until a hook first runs.
func TestClaudeSpoolsFromStart(t *testing.T) {
	if (Claude{}).SpoolsFromStart() {
		t.Fatal("Claude.SpoolsFromStart() = true, want false")
	}
}

func ownerJSON(pid int, session, launch string) string {
	b, _ := json.Marshal(ownerRecord{PID: pid, Session: session, Launch: launch})
	return string(b)
}

// unrelated stubs a chain that does not contain pid 4242 (the owner's process).
func unrelated(t *testing.T) {
	stubParents(t, map[int]int{os.Getppid(): 5001, 5001: 5002})
}

func TestOwnsBySessionID(t *testing.T) {
	unrelated(t)
	ok, why := (Claude{}).Match(ownerJSON(4242, "S1", ""), HookIdentity{SessionID: "S1"})
	if !ok || why != "session id" {
		t.Fatalf("Match = %v %q", ok, why)
	}
}

func TestOwnsByLaunchToken(t *testing.T) {
	unrelated(t)
	ok, why := (Claude{}).Match(ownerJSON(4242, "S1", "tok"), HookIdentity{SessionID: "S2", LaunchToken: "tok"})
	if !ok || why != "launch token" {
		t.Fatalf("Match = %v %q", ok, why)
	}
}

func TestOwnsRefusesAnotherSession(t *testing.T) {
	unrelated(t)
	ok, why := (Claude{}).Match(ownerJSON(4242, "S1", ""), HookIdentity{SessionID: "S2"})
	if ok {
		t.Fatal("another session was accepted")
	}
	for _, want := range []string{"session id", "launch token", "process 4242"} {
		if !strings.Contains(why, want) {
			t.Errorf("why %q does not name %q", why, want)
		}
	}
}

func TestOwnsEmptyTokensNeverMatch(t *testing.T) {
	unrelated(t)
	for _, h := range []HookIdentity{{}, {SessionID: "S2"}} {
		if (Claude{}).Owns(ownerJSON(4242, "S1", ""), h) {
			t.Errorf("empty launch tokens matched for %+v", h)
		}
	}
	// Empty session ids never match either, and a token on one side only is not a match.
	if (Claude{}).Owns(ownerJSON(4242, "", "tok"), HookIdentity{}) || (Claude{}).Owns(ownerJSON(4242, "", ""), HookIdentity{LaunchToken: "tok"}) {
		t.Error("an empty side matched")
	}
}

func TestOwnsLegacyPidOwner(t *testing.T) {
	unrelated(t)
	if !(Claude{}).Owns("pid:5002", HookIdentity{}) {
		t.Error("legacy pid owner in the chain refused")
	}
	if (Claude{}).Owns("pid:4242", HookIdentity{SessionID: "S1", LaunchToken: "t"}) {
		t.Error("legacy pid owner outside the chain accepted")
	}
	if !(Claude{}).Owns("", HookIdentity{}) {
		t.Error("empty owner refused")
	}
}

func TestOwnerTokenIsJSON(t *testing.T) {
	env := func(k string) string {
		return map[string]string{"CLAUDE_CODE_SESSION_ID": "S1", "MASH_LAUNCH_TOKEN": "tok"}[k]
	}
	var r ownerRecord
	if err := json.Unmarshal([]byte((Claude{}).OwnerToken(env)), &r); err != nil {
		t.Fatal(err)
	}
	if r.PID != os.Getppid() || r.Session != "S1" || r.Launch != "tok" {
		t.Fatalf("record %+v", r)
	}
	if got := (Claude{}).OwnerToken(func(string) string { return "" }); strings.Contains(got, "session") || strings.Contains(got, "launch") {
		t.Fatalf("empty fields not omitted: %s", got)
	}
}
