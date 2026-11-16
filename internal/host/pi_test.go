package host

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/bogworks/mash/internal/spool"
)

func TestPiTrigger(t *testing.T) {
	p := Pi{}
	for _, c := range []struct {
		in    string
		event string
		ok    bool
	}{
		{`{"event":"tool_result","session":"s1"}`, "PostToolUse", true},
		{`{"event":"before_agent_start","session":"s1"}`, "UserPromptSubmit", true},
		{`{"event":"before_agent_start"}`, "UserPromptSubmit", true},
		{`{"hook_event_name":"PostToolUse","session_id":"s1"}`, "", false}, // Claude's input is not ours
		{`{"event":"agent_start","session":"s1"}`, "", false},
		{`{"event":"Stop"}`, "", false},
		{`not json`, "", false},
		{``, "", false},
	} {
		tr, ok := p.Trigger([]byte(c.in), nil)
		if ok != c.ok || tr.Event != c.event {
			t.Errorf("Trigger(%s) = %+v, %v; want %q, %v", c.in, tr, ok, c.event, c.ok)
		}
	}
	if tr, _ := p.Trigger([]byte(`{"event":"tool_result","session":"s1"}`), nil); tr.SessionID != "s1" || tr.Subagent {
		t.Errorf("trigger = %+v", tr)
	}
	// Claude does not read Pi's input.
	if _, ok := (Claude{}).Trigger([]byte(`{"event":"tool_result"}`), nil); ok {
		t.Error("Claude took Pi's input")
	}
}

func TestPiDeclarations(t *testing.T) {
	p := Pi{}
	if p.Name() != "pi" {
		t.Errorf("name %q", p.Name())
	}
	if got, want := p.SessionSources(), (SessionSources{Env: "", HookField: "session", AncestryUnique: true}); got != want {
		t.Errorf("sources %+v", got)
	}
	if p.Wake() != WakeNone || !p.SpoolsFromStart() || p.ClaudeSettingsChecks() {
		t.Errorf("wake %v spools %v checks %v", p.Wake(), p.SpoolsFromStart(), p.ClaudeSettingsChecks())
	}
	if got, want := p.Words(), (Words{Product: "Pi", Process: "Pi process", ServicePrefix: "Pi (mash): ", Launch: "pi"}); got != want {
		t.Errorf("words %+v", got)
	}
	called := false
	if err := p.Waker(func(string, map[string]string) error { called = true; return nil })("x", nil); err != nil || called {
		t.Errorf("waker err %v, called the channel %v", err, called)
	}
}

// With no environment variable for the session id, SessionID never reads the environment.
func TestSessionIDWithEmptyEnv(t *testing.T) {
	env := func(k string) string {
		if k == "" {
			t.Error("env called with an empty name")
		}
		return "leak"
	}
	if got := SessionID(Pi{}, env); got != "" {
		t.Errorf("SessionID(Pi) = %q", got)
	}
	if got := SessionID(Pi{}, nil); got != "" {
		t.Errorf("SessionID(Pi, nil) = %q", got)
	}
	if got := SessionID(Claude{}, func(k string) string { return k }); got != "CLAUDE_CODE_SESSION_ID" {
		t.Errorf("SessionID(Claude) = %q", got)
	}
	// Pi's owner record carries no session id, even when the variable Claude would read is set.
	owner := Pi{}.OwnerToken(func(k string) string {
		if k == "MASH_LAUNCH_TOKEN" {
			return "tok"
		}
		return "sid"
	})
	var r ownerRecord
	if err := json.Unmarshal([]byte(owner), &r); err != nil || r.Session != "" || r.Launch != "tok" || r.PID == 0 {
		t.Errorf("owner %s (%v)", owner, err)
	}
}

func TestSelectPi(t *testing.T) {
	a, err := Select("pi")
	if err != nil || a.Name() != "pi" {
		t.Fatalf("Select(pi) = %v, %v", a, err)
	}
	if a, err := Select(""); err != nil || a.Name() != "claude" {
		t.Fatalf("Select(\"\") = %v, %v: no detection, the default stays Claude", a, err)
	}
	if got := strings.Join(Known(), ","); got != "claude,headless,pi" {
		t.Errorf("Known = %s", got)
	}
}

func piEntries() []spool.Entry {
	return []spool.Entry{
		{Seq: 1, From: "b", Trust: "verified", At: time.Unix(1700000000, 0), Body: []byte("hello pi")},
		{Seq: 2, From: "c", Trust: "claimed", At: time.Unix(1700000001, 0), Body: []byte("second")},
	}
}

// Pi's output is the text Claude puts in additionalContext, plain, and drains the same entries.
func TestPiPresentIsClaudesAdditionalContextAsPlainText(t *testing.T) {
	pres := Presentation{Name: "a", Owner: "o", Event: "PostToolUse", BatchMsgs: 10, BatchBytes: 4096}
	cOut, cShown := Claude{}.Present(pres, piEntries())
	var o struct {
		H struct {
			Ctx string `json:"additionalContext"`
		} `json:"hookSpecificOutput"`
	}
	if err := json.Unmarshal(cOut, &o); err != nil || o.H.Ctx == "" {
		t.Fatalf("claude out %q: %v", cOut, err)
	}
	pOut, pShown := Pi{}.Present(pres, piEntries())
	if string(pOut) != o.H.Ctx+"\n" {
		t.Errorf("pi out %q\nclaude context %q", pOut, o.H.Ctx)
	}
	if strings.HasPrefix(string(pOut), "{") || !strings.Contains(string(pOut), "> hello pi") || !strings.Contains(string(pOut), "[verified]") {
		t.Errorf("pi out is not the plain text: %q", pOut)
	}
	if len(pShown) != len(cShown) {
		t.Errorf("shown %d vs %d", len(pShown), len(cShown))
	}
}

// The size cap Claude keeps applies to Pi's text too: what is not shown is not drained.
func TestPiPresentKeepsTheSizeCap(t *testing.T) {
	var es []spool.Entry
	for i := 1; i <= 5; i++ {
		es = append(es, spool.Entry{Seq: uint64(i), From: "b", Trust: "claimed", At: time.Now(), Body: []byte(strings.Repeat("x", 4000))})
	}
	out, shown := Pi{}.Present(Presentation{Name: "a", Owner: "o", Event: "PostToolUse", BatchMsgs: 10, BatchBytes: 1 << 20}, es)
	_, cShown := Claude{}.Present(Presentation{Name: "a", Owner: "o", Event: "PostToolUse", BatchMsgs: 10, BatchBytes: 1 << 20}, es)
	if n := utf16Len(string(out)); n > hookContextMax+1 || len(shown) == 0 || len(shown) != len(cShown) {
		t.Errorf("text %d units, shown %d of %d (Claude %d)", n, len(shown), len(es), len(cShown))
	}
}

func TestNotice(t *testing.T) {
	// Claude's is exactly what host.Notice returned before the method existed.
	want := `{"hookSpecificOutput":{"additionalContext":"mash: it \"quoted\" \u003c\u0026\u003e","hookEventName":"PostToolUse"}}` + "\n"
	if got := string(Claude{}.Notice("PostToolUse", `mash: it "quoted" <&>`)); got != want {
		t.Errorf("claude notice\n got %q\nwant %q", got, want)
	}
	if got, want := string(Claude{}.Notice("UserPromptSubmit", "x")), string(Notice("UserPromptSubmit", "x")); got != want {
		t.Errorf("claude notice %q differs from Notice %q", got, want)
	}
	if got := string(Pi{}.Notice("PostToolUse", "mash: hello")); got != `{"text":"mash: hello"}`+"\n" {
		t.Errorf("pi notice %q", got)
	}
	if got := string(Pi{}.Offer([]byte("a\nb\n"), "tok")); got != `{"text":"a\nb","ack":"tok"}`+"\n" {
		t.Errorf("pi offer %q", got)
	}
	if !IsTwoPhase(Pi{}) || IsTwoPhase(Claude{}) || IsTwoPhase(Headless{}) {
		t.Error("only Pi delivers in two phases")
	}
}
