package main

import (
	"unicode/utf8"

	"github.com/bogworks/mash/internal/activity"
)

// The wire words of program_status (ADR 0134). A state outside these never leaves this file.
const (
	programTitleMax = 120 // bytes
	programMsgMax   = 300 // bytes
)

// programStatusValue maps the activity tracker's answer to the program_status value, in SAP terms, or ok false for
// "no state is known" (the key is then absent: absent is never idle). It is the only place that maps activity to the
// wire.
//
// The mapping (planner ruling #1232, via mash-lead #599):
//
//   - hooks only: UserPromptSubmit, PreToolUse, PostToolUse, PreCompact give working; Stop gives idle;
//     a hooks-only done is idle whichever hook produced it, so a PostCompact reads as idle until the next turn event
//     (PreToolUse and the like) turns it back to working; a hooks-only blocked (PermissionRequest) gives
//     working, because the turn is still in progress and nothing fires when the dialog is answered.
//   - a runtime (herdr) report is kept as it is: idle, working, done, blocked with its kind where known, error.
//   - "both" (the runtime and the hooks agree on the state) is the runtime's state.
//   - absent gives no key.
func programStatusValue(a activity.Activity, present bool) (map[string]string, bool) {
	if !present {
		return nil, false
	}
	st, kind := a.State, a.Kind
	if a.Source == "hooks" {
		switch st {
		case activity.StateDone:
			st = activity.StateIdle
		case activity.StateBlocked:
			st, kind = activity.StateWorking, ""
		}
	}
	switch st {
	case activity.StateIdle, activity.StateWorking, activity.StateDone, activity.StateError:
		kind = ""
	case activity.StateBlocked:
		if kind != activity.KindPermission && kind != activity.KindQuestion && kind != activity.KindAuth {
			kind = ""
		}
	default:
		return nil, false
	}
	m := map[string]string{"state": string(st)}
	if kind != "" {
		m["kind"] = string(kind)
	}
	return m, true
}

// programStatus is the heartbeat-extras provider: one program_status key, or none.
func programStatus(cur func() (activity.Activity, bool)) func() map[string]any {
	return func() map[string]any {
		if cur == nil {
			return nil
		}
		a, ok := cur()
		v, ok := programStatusValue(a, ok)
		if !ok {
			return nil
		}
		return map[string]any{"program_status": v}
	}
}

// programMsg is the short state sentence of program_detail: fixed words chosen here, naming no tool and quoting no text.
func programMsg(m map[string]string) string {
	switch m["state"] {
	case "working":
		return "working"
	case "idle":
		return "idle"
	case "done":
		return "done"
	case "error":
		return "error"
	case "blocked":
		switch m["kind"] {
		case "permission":
			return "waiting for permission"
		case "question":
			return "waiting for an answer to a question"
		case "auth":
			return "waiting for authentication"
		}
		return "blocked"
	}
	return ""
}

// cutRunes cuts s to at most max bytes at a rune boundary.
func cutRunes(s string, max int) string {
	if len(s) <= max {
		return s
	}
	s = s[:max]
	for len(s) > 0 && !utf8.ValidString(s) {
		s = s[:len(s)-1]
	}
	return s
}

// programDetail is the status-extras provider: program_detail {title, msg}, reply only. The title is the endpoint's
// display name and " (Claude Code)"; the msg is programMsg of the current state. Both are generated here, never
// taken from the session or the model. Each member is omitted when empty and the key when both are.
func programDetail(display string, cur func() (activity.Activity, bool)) func() map[string]any {
	return func() map[string]any {
		d := map[string]string{}
		if display != "" {
			d["title"] = cutRunes(display+" (Claude Code)", programTitleMax)
		}
		if cur != nil {
			a, ok := cur()
			if v, ok := programStatusValue(a, ok); ok {
				if msg := programMsg(v); msg != "" {
					d["msg"] = cutRunes(msg, programMsgMax)
				}
			}
		}
		if len(d) == 0 {
			return nil
		}
		return map[string]any{"program_detail": d}
	}
}
