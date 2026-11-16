package host

import (
	"encoding/json"
	"strings"

	"github.com/bogworks/mash/internal/spool"
)

// Pi's extension event names, as hosts/pi/mash.ts sends them on stdin: the extension runs `mash hook` after a tool
// result and before an agent start. They are Pi's own names for the two points, not Claude's.
const (
	piToolResult       = "tool_result"
	piBeforeAgentStart = "before_agent_start"
)

// Pi is the adapter for the Pi coding agent. Pi has no hooks and no channel: a small extension (hosts/pi/mash.ts)
// runs `mash hook` with {"event":"tool_result"|"before_agent_start","session":"<id>"} on stdin, takes plain text from
// stdout into the model's next request, and watches the spool to wake an idle session. Ownership is Claude's rules
// (launch token, process ancestry); Pi gives serve no session id, so the session-id rule never fires.
type Pi struct{ Claude }

func (Pi) Name() string { return "pi" }

// SessionSources: Pi sets no session id in serve's environment; the extension passes its own in the hook's input.
func (Pi) SessionSources() SessionSources {
	return SessionSources{Env: "", HookField: "session", AncestryUnique: true}
}

// Wake: nothing wakes Pi through mash; the extension watches the spool.
func (Pi) Wake() WakeKind { return WakeNone }

// Words names Pi in notices and status lines.
func (Pi) Words() Words {
	return Words{Product: "Pi", Process: "Pi process", ServicePrefix: "Pi (mash): "}
}

// ClaudeSettingsChecks: Claude Code's settings files say nothing about Pi.
func (Pi) ClaudeSettingsChecks() bool { return false }

// OwnerToken is Claude's record; with no session id source it carries the pid and the launch token only.
func (p Pi) OwnerToken(env func(string) string) string { return ownerToken(p, env) }

// Trigger reads the extension's input and maps its two events onto the ones the hook loop knows. Anything else is
// not ours.
func (Pi) Trigger(stdin []byte, _ func(string) string) (Trigger, bool) {
	var in struct {
		Event   string `json:"event"`
		Session string `json:"session"`
	}
	if json.Unmarshal(stdin, &in) != nil {
		return Trigger{}, false
	}
	switch in.Event {
	case piToolResult:
		return Trigger{Event: "PostToolUse", SessionID: in.Session}, true
	case piBeforeAgentStart:
		return Trigger{Event: "UserPromptSubmit", SessionID: in.Session}, true
	}
	return Trigger{}, false
}

// Notice is {"text": "..."} and a newline, the shape of a delivery with no ack: the extension reads one JSON document
// from every run of the hook, and a notice is not mail, so there is nothing to acknowledge.
func (Pi) Notice(_, text string) []byte {
	out, _ := json.Marshal(struct {
		Text string `json:"text"`
	}{text})
	return append(out, '\n')
}

// TwoPhase: Pi's hook offers a batch and does not drain it; the extension acks it once Pi has accepted the message
// (`mash hook --ack <token>`), so a session change between the two loses nothing.
func (Pi) TwoPhase() bool { return true }

// Offer wraps the text Present printed with the ack token as {"text": "...", "ack": "<token>"} and a newline.
func (Pi) Offer(out []byte, token string) []byte {
	b, _ := json.Marshal(struct {
		Text string `json:"text"`
		Ack  string `json:"ack"`
	}{strings.TrimSuffix(string(out), "\n"), token})
	return append(b, '\n')
}

// Present is the text Claude puts in additionalContext, with its trust-note frame and size cap, printed as it is.
func (Pi) Present(p Presentation, waiting []spool.Entry) ([]byte, []spool.Entry) {
	text, shown := presentText(p, waiting)
	return []byte(text + "\n"), shown
}

// SpoolsFromStart: Pi never takes the channel notification, so mail is spooled from the first message.
func (p Pi) SpoolsFromStart() bool { return p.Wake() != WakeChannel }

// Waker does nothing: there is nothing to wake through.
func (Pi) Waker(func(string, map[string]string) error) func(string, map[string]string) error {
	return func(string, map[string]string) error { return nil }
}
