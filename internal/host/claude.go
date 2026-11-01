package host

import (
	"encoding/json"

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

// OwnerToken is the JSON owner record of the serve that holds the lease (see ownerToken).
func (Claude) OwnerToken(env func(string) string) string {
	return ownerToken(env, "CLAUDE_CODE_SESSION_ID")
}

// Owns reports whether the hook may drain a spool recorded with owner; see matchOwner.
func (c Claude) Owns(owner string, h HookIdentity) bool {
	ok, _ := c.Match(owner, h)
	return ok
}

// Match: see matchOwner.
func (Claude) Match(owner string, h HookIdentity) (bool, string) { return matchOwner(owner, h) }

// SpoolsFromStart: Claude Code takes channel events, so mail is the channel's until a hook first runs.
func (Claude) SpoolsFromStart() bool { return false }

// Waker: Claude Code takes the wake as the MCP channel notification itself.
func (Claude) Waker(notify func(text string, meta map[string]string) error) func(text string, meta map[string]string) error {
	return notify
}

// Present renders waiting as the hook's additionalContext.
func (Claude) Present(p Presentation, waiting []spool.Entry) ([]byte, []spool.Entry) {
	waiting = batchEntries(p, waiting)
	// Claude Code truncates hook additionalContext over 10,000 characters to a preview plus a file path, so what is
	// not shown in full must not be marked drained (serve would ack it unseen). Fit the text: full form, then a
	// digest, then fewer entries from the end, measured in UTF-16 units as Claude Code counts (codex H1 on #21,
	// round 8). Only the entries rendered are drained; the rest wait for the next run.
	build := func(w []spool.Entry, maxBytes int) string { return frameBatch(p, w, maxBytes) }
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
