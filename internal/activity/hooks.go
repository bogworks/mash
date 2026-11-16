package activity

import (
	"encoding/json"
	"os"
	"path/filepath"
	"time"
)

// RecordFile is the file in Options.Dir that holds the latest Record.
const RecordFile = "activity.json"

// Record is what the hook command writes: the latest main-session hook event.
type Record struct {
	Event   string    `json:"event"`          // UserPromptSubmit, PreToolUse, PostToolUse, PermissionRequest, Stop, PreCompact, PostCompact
	Tool    string    `json:"tool,omitempty"` // tool_name, for PermissionRequest
	At      time.Time `json:"at"`
	Session string    `json:"session,omitempty"` // the hook's session id
	Launch  string    `json:"launch,omitempty"`  // MASH_LAUNCH_TOKEN of the hook process
}

// trackedEvents are the hook events that say something about the main session's state.
var trackedEvents = map[string]bool{
	"UserPromptSubmit":  true,
	"PreToolUse":        true,
	"PostToolUse":       true,
	"PermissionRequest": true,
	"Stop":              true,
	"PreCompact":        true,
	"PostCompact":       true,
}

// WriteRecord writes r to dir/RecordFile atomically (a temp file in dir, then rename), mode 0600. It creates
// nothing else and not dir itself.
func WriteRecord(dir string, r Record) error {
	b, err := json.Marshal(r)
	if err != nil {
		return err
	}
	f, err := os.CreateTemp(dir, ".activity-*")
	if err != nil {
		return err
	}
	name := f.Name()
	fail := func(err error) error {
		f.Close()
		os.Remove(name)
		return err
	}
	if err := f.Chmod(0o600); err != nil {
		return fail(err)
	}
	if _, err := f.Write(b); err != nil {
		return fail(err)
	}
	if err := f.Close(); err != nil {
		os.Remove(name)
		return err
	}
	if err := os.Rename(name, filepath.Join(dir, RecordFile)); err != nil {
		os.Remove(name)
		return err
	}
	return nil
}

// RecordFromHook parses a Claude Code hook input (JSON on stdin). ok is false for an event that is not tracked, for
// malformed JSON, and when agent_id is non-empty: a subagent's event says nothing about the main session. The
// session is the input's session_id, else CLAUDE_CODE_SESSION_ID; the launch is MASH_LAUNCH_TOKEN.
func RecordFromHook(stdin []byte, env func(string) string, now time.Time) (Record, bool) {
	if env == nil {
		env = os.Getenv
	}
	var in struct {
		Event     string `json:"hook_event_name"`
		SessionID string `json:"session_id"`
		ToolName  string `json:"tool_name"`
		AgentID   string `json:"agent_id"`
	}
	if err := json.Unmarshal(stdin, &in); err != nil {
		return Record{}, false
	}
	if in.AgentID != "" || !trackedEvents[in.Event] {
		return Record{}, false
	}
	r := Record{Event: in.Event, At: now.UTC(), Session: in.SessionID, Launch: env("MASH_LAUNCH_TOKEN")}
	if r.Session == "" {
		r.Session = env("CLAUDE_CODE_SESSION_ID")
	}
	if in.Event == "PermissionRequest" {
		r.Tool = in.ToolName
	}
	return r, true
}

// stateOf maps a record to a state. A hooks state is never idle.
func (r Record) stateOf() (State, Kind, bool) {
	switch r.Event {
	case "UserPromptSubmit", "PreToolUse", "PostToolUse", "PreCompact":
		return StateWorking, "", true
	case "Stop", "PostCompact":
		return StateDone, "", true
	case "PermissionRequest":
		if r.Tool == "AskUserQuestion" {
			return StateBlocked, KindQuestion, true
		}
		return StateBlocked, KindPermission, true
	}
	return "", "", false
}

// matches reports whether the record came from this process's harness: the same launch token, or the same session id.
// Another session sharing the directory is ignored.
func (t *Tracker) matches(r Record) bool {
	if r.Launch != "" && r.Launch == t.env("MASH_LAUNCH_TOKEN") {
		return true
	}
	return r.Session != "" && r.Session == t.env("CLAUDE_CODE_SESSION_ID")
}

// hooksViewAt is the hooks view at time now. Caller holds t.mu.
func (t *Tracker) hooksViewAt(now time.Time) hooksView {
	if !t.hkOK {
		return hooksView{}
	}
	st, kind, ok := t.hkRec.stateOf()
	if !ok {
		return hooksView{}
	}
	if st != StateBlocked && now.Sub(t.hkRec.At) > HooksStale {
		return hooksView{}
	}
	return hooksView{Present: true, State: st, Kind: kind, At: t.hkRec.At}
}

// pollHooks re-reads the record file when its mod time or size changed. A missing file clears the record; a file
// that does not parse is ignored (it may be half written) and read again when it changes.
func (t *Tracker) pollHooks() {
	path := filepath.Join(t.o.Dir, RecordFile)
	fi, err := os.Stat(path)
	if err != nil {
		if os.IsNotExist(err) && t.hkSeen {
			t.hkSeen = false
			t.mu.Lock()
			t.hkOK = false
			t.evalLocked()
			t.mu.Unlock()
		} else {
			t.mu.Lock()
			t.evalLocked() // staleness moves with the clock
			t.mu.Unlock()
		}
		return
	}
	if t.hkSeen && fi.ModTime().Equal(t.hkInfo.ModTime()) && fi.Size() == t.hkInfo.Size() && os.SameFile(fi, t.hkInfo) {
		t.mu.Lock()
		t.evalLocked()
		t.mu.Unlock()
		return
	}
	t.hkSeen, t.hkInfo = true, fi
	b, err := os.ReadFile(path)
	var r Record
	if err != nil || json.Unmarshal(b, &r) != nil {
		return
	}
	t.mu.Lock()
	t.hkRec = r
	t.hkOK = t.matches(r)
	t.evalLocked()
	t.mu.Unlock()
}
