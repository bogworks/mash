package host

import (
	"encoding/json"
	"os"
	"slices"
	"strconv"
	"strings"

	"github.com/bogworks/mash/internal/endpoint"
	"github.com/bogworks/mash/internal/render"
	"github.com/bogworks/mash/internal/sap"
	"github.com/bogworks/mash/internal/spool"
)

// hookContextMax is how many characters of additionalContext one hook run emits: Claude Code's limit is 10,000.
const hookContextMax = 9500

// utf16Len is s's length in UTF-16 code units, which is how Claude Code counts its 10,000-character cap: a rune above
// U+FFFF (most emoji) is two units (codex H1 on #21, round 8).
func utf16Len(s string) int {
	n := 0
	for _, r := range s {
		n++
		if r > 0xFFFF {
			n++
		}
	}
	return n
}

// Claude is the adapter for Claude Code: PostToolUse and UserPromptSubmit hooks, additionalContext output.
type Claude struct{}

func (Claude) Name() string { return "claude" }

// Trigger reads Claude Code's hook JSON.
func (Claude) Trigger(stdin []byte, env func(string) string) (Trigger, bool) {
	var in struct {
		Event     string `json:"hook_event_name"`
		AgentID   string `json:"agent_id"`
		SessionID string `json:"session_id"`
	}
	// An input that does not name one of the two events is not ours to answer: nothing is touched or drained, the
	// mail stays spooled (codex H2 on #21, round 5).
	if json.Unmarshal(stdin, &in) != nil || (in.Event != "PostToolUse" && in.Event != "UserPromptSubmit") {
		return Trigger{}, false
	}
	// Claude Code also runs PostToolUse hooks for a subagent's tool calls, and their input carries an agent_id. A
	// subagent must not drain the main session's mail into its own context: the caller shows only that hooks run
	// (the spool's .alive) once the owner check passes, and reads and drains nothing, so the mail stays spooled for
	// the main session's next hook (codex H1 on #21, round 6).
	return Trigger{Event: in.Event, SessionID: in.SessionID, Subagent: in.AgentID != ""}, true
}

// ownerRecord is the owner serve writes into the spool's .owner (ADR 0040; .session keeps the legacy "pid:N" text,
// see spool.LegacyToken): the Claude process (serve's parent), the session id and the launch token serve was started
// with. Empty fields are omitted.
type ownerRecord struct {
	PID     int    `json:"pid,omitempty"`
	Session string `json:"session,omitempty"`
	Launch  string `json:"launch,omitempty"`
}

// OwnerToken is the JSON owner record of the serve that holds the lease: its parent pid, its CLAUDE_CODE_SESSION_ID
// and its MASH_LAUNCH_TOKEN.
func (Claude) OwnerToken(env func(string) string) string {
	r := ownerRecord{PID: os.Getppid()}
	if env != nil {
		r.Session, r.Launch = env("CLAUDE_CODE_SESSION_ID"), env("MASH_LAUNCH_TOKEN")
	}
	b, _ := json.Marshal(r)
	return string(b)
}

// Owns reports whether the hook may drain a spool recorded with owner; see Match.
func (c Claude) Owns(owner string, h HookIdentity) bool {
	ok, _ := c.Match(owner, h)
	return ok
}

// Match decides whether the hook belongs to the session whose serve holds the lease. Any one of three rules, cheapest
// first, is enough; a hook that matches none is refused, and no rule adopts an unknown session:
//  1. session: the hook's session id equals serve's (both non-empty), unless both launch tokens are non-empty and
//     differ (another launch of the same session, ADR 0043);
//  2. launch: both launch tokens are non-empty and equal (stable across /clear);
//  3. process: serve's parent pid is among the hook's ancestors (covers /clear on the plain CLI).
//
// Before them: a hook that descends from a serve recorded as waiting for the lease, and not from the owner, is refused
// as another launch (ADR 0043), tokens or not.
//
// A legacy owner ("pid:N" text) is checked by rule 3 alone; an empty or other older owner is accepted.
func (Claude) Match(owner string, h HookIdentity) (bool, string) {
	var r ownerRecord
	switch {
	case strings.HasPrefix(owner, "{"):
		if json.Unmarshal([]byte(owner), &r) != nil {
			return false, "owner record unreadable (is serve a different mash version than the hook?)"
		}
	case strings.HasPrefix(owner, "pid:"):
		n, err := strconv.Atoi(strings.TrimPrefix(owner, "pid:"))
		if err != nil {
			return false, "owner record unreadable"
		}
		r.PID = n
	default:
		return true, "no owner recorded"
	}
	// A hook that descends from a serve waiting for the lease, and not from the owner, is another launch of the
	// session, whatever the launch tokens say (ADR 0043).
	if WaiterAncestor(r.PID, h.Waiters) > 0 {
		return false, otherLaunchWhy + ", process " + strconv.Itoa(r.PID) + " is not an ancestor of the hook"
	}
	// Two launches of one session (a `claude --resume` of a running session) share the session id but not the launch
	// token: the session id then proves nothing (ADR 0043). Rule 3 still decides.
	otherLaunch := h.LaunchToken != "" && r.Launch != "" && h.LaunchToken != r.Launch
	sameSession := h.SessionID != "" && r.Session != "" && h.SessionID == r.Session
	if sameSession && !otherLaunch {
		return true, "session id"
	}
	if h.LaunchToken != "" && r.Launch != "" && h.LaunchToken == r.Launch {
		return true, "launch token"
	}
	if r.PID > 0 {
		for pid := range Ancestors { // lazy: stops at the owner
			if pid == r.PID {
				return true, "process ancestry"
			}
		}
	}
	if sameSession && otherLaunch {
		return false, otherLaunchWhy + ", process " + strconv.Itoa(r.PID) + " is not an ancestor of the hook"
	}
	return false, "session id " + differs(h.SessionID, r.Session) + ", launch token " + differs(h.LaunchToken, r.Launch) +
		", process " + strconv.Itoa(r.PID) + " is not an ancestor of the hook"
}

// WaiterAncestor is the first of waiters that is among the hook's ancestors, provided the owner (ownerPID) is not
// itself a waiter and not among the ancestors; 0 otherwise.
func WaiterAncestor(ownerPID int, waiters []int) int {
	if len(waiters) == 0 || ownerPID <= 0 || slices.Contains(waiters, ownerPID) {
		return 0
	}
	found := 0
	for pid := range Ancestors {
		if pid == ownerPID {
			return 0
		}
		if found == 0 && slices.Contains(waiters, pid) {
			found = pid
		}
	}
	return found
}

// otherLaunchWhy starts the refusal reason of a hook whose session id matches the owner's but whose launch token
// differs. No token is printed.
const otherLaunchWhy = "session id matches but launch token differs: another launch of this session (a --resume of a running session?)"

// IsOtherLaunch reports whether a Match refusal reason is the other-launch case.
func IsOtherLaunch(why string) bool { return strings.HasPrefix(why, otherLaunchWhy) }

// OwnerPID is the Claude process pid an owner record (JSON or legacy "pid:N") names, 0 if none.
func OwnerPID(owner string) int {
	var r ownerRecord
	if strings.HasPrefix(owner, "{") {
		_ = json.Unmarshal([]byte(owner), &r)
		return r.PID
	}
	n, _ := strconv.Atoi(strings.TrimPrefix(owner, "pid:"))
	return n
}

// Notice renders text as Claude Code hook additionalContext for event, in the shape Present uses.
func Notice(event, text string) []byte {
	out, _ := json.Marshal(map[string]any{"hookSpecificOutput": map[string]string{"hookEventName": event, "additionalContext": text}})
	return append(out, '\n')
}

// differs words why two ids did not match, without printing a launch token.
func differs(hook, owner string) string {
	switch {
	case owner == "":
		return "not recorded by serve"
	case hook == "":
		return "not seen by the hook"
	}
	return "differs"
}

// SpoolsFromStart: Claude Code takes channel events, so mail is the channel's until a hook first runs.
func (Claude) SpoolsFromStart() bool { return false }

// Waker: Claude Code takes the wake as the MCP channel notification itself.
func (Claude) Waker(notify func(text string, meta map[string]string) error) func(text string, meta map[string]string) error {
	return notify
}

// Present renders waiting as the hook's additionalContext.
func (Claude) Present(p Presentation, waiting []spool.Entry) ([]byte, []spool.Entry) {
	// One drain shows at most BatchMsgs messages, in full or, past BatchBytes, as a digest (the bodies stay readable
	// by #seq). The rest wait for the next run: never dropped.
	// BatchMsgs 0 is allowed and means "digest" to render, so it must not also empty the drain (codex M2 on #21).
	n := p.BatchMsgs
	if n < 1 {
		n = 10
	}
	// Urgent entries first, each group in seq order, so a drain never shows only normal mail while an urgent
	// message waits behind it, and fitting drops non-urgent entries first (codex H1 on #23).
	slices.SortStableFunc(waiting, func(a, b spool.Entry) int {
		switch {
		case a.Urgent == b.Urgent:
			return 0
		case a.Urgent:
			return -1
		}
		return 1
	})
	if len(waiting) > n {
		waiting = waiting[:n]
	}
	// Claude Code truncates hook additionalContext over 10,000 characters to a preview plus a file path, so what is
	// not shown in full must not be marked drained (serve would ack it unseen). Fit the text: full form, then a
	// digest, then fewer entries from the end, measured in UTF-16 units as Claude Code counts (codex H1 on #21,
	// round 8). Only the entries rendered are drained; the rest wait for the next run.
	build := func(w []spool.Entry, maxBytes int) string {
		ms := make([]render.Mail, len(w))
		ts := make([]sap.Trust, len(w))
		for i, e := range w {
			ms[i] = endpoint.MailOf(e)
			ts[i] = ms[i].Trust
		}
		return endpoint.HookFrame(p.Name, ts) + "\n" + render.Batch(p.Name, p.Owner, ms, p.BatchMsgs, maxBytes)
	}
	text := build(waiting, p.BatchBytes)
	if utf16Len(text) > hookContextMax {
		for {
			text = build(waiting, 0)
			if utf16Len(text) <= hookContextMax || len(waiting) == 1 {
				break
			}
			waiting = waiting[:len(waiting)-1]
		}
	}
	out, _ := json.Marshal(map[string]any{"hookSpecificOutput": map[string]string{"hookEventName": p.Event, "additionalContext": text}})
	return append(out, '\n'), waiting
}

// Utf16Len exposes utf16Len to tests of other packages that assert the Claude Code cap.
func Utf16Len(s string) int { return utf16Len(s) }

// MinClaudeCode is the oldest Claude Code a live receipt passed on; raise it with each receipt, never lower it.
// mash does not refuse older versions; `status` warns below it.
const MinClaudeCode = "2.1.312"
