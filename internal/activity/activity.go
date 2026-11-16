// Package activity tells `mash serve` what the harness it runs under (Claude Code) is doing, in OSC 7501's words:
// a state (idle, working, done, blocked, error), a kind when blocked (permission, question, auth), and since, when
// this process first saw that state. It reads two sources and combines them: a runtime source (anything that
// implements RuntimeSource and can look at the harness's terminal; the herdr adapter in internal/activity/herdr is
// one) and the harness's hooks (a tiny command writes the latest hook event to a file this package reads). This
// package knows no runtime's protocol: the caller injects the source, or none.
//
// Two rules hold everywhere:
//
//   - Never idle from hooks. Hooks see events, not quiet: no hook fires when a dialog is refused or a turn is
//     abandoned, so a hooks-only answer is working, done or blocked, never idle.
//   - Absent is not idle. When neither source can say, Current reports nothing, and a caller must not turn that into
//     "free". Only the runtime, which looks at the screen, may say idle.
//
// Nothing free-text leaves this package: Activity has four fields and the leakage guard test pins them.
package activity

import (
	"context"
	"time"
)

// State is what the harness is doing, in OSC 7501's words.
type State string

const (
	StateIdle    State = "idle"
	StateWorking State = "working"
	StateDone    State = "done"
	StateBlocked State = "blocked"
	StateError   State = "error"
)

// Kind says why the harness is blocked. It is empty unless the state is blocked, and may be empty then too.
type Kind string

const (
	KindPermission Kind = "permission"
	KindQuestion   Kind = "question"
	KindAuth       Kind = "auth"
)

// HooksStale is how old a hooks record may be before it counts as absent. A blocked record is exempt: the harness
// may sit at a real dialog for hours, and nothing fires when the dialog ends without a tool call.
const HooksStale = 30 * time.Minute

// Activity is the combined answer. Every field is a fixed word or a time; nothing a source said in free text is kept.
type Activity struct {
	State State
	Kind  Kind      // only with StateBlocked, may be empty
	Since time.Time // UTC, when this process first saw this (State, Kind) of the combined answer
	// Source is "runtime", "hooks" or "both". It is for the local user only and is never sent to a peer.
	Source string
}

// RuntimeReport is what a runtime source says about the harness's terminal.
type RuntimeReport struct {
	// Available is false when the source cannot say (not connected, an error); the other fields are then ignored.
	Available bool
	// State is empty when the source answered but the terminal is not an agent's.
	State State
	// Version is the source's own version text, for Describe; it may be empty.
	Version string
}

// RuntimeSource is a runtime the tracker may ask what the harness is doing. Implementations hold every detail of
// their protocol; the tracker sees only reports.
type RuntimeSource interface {
	// Run keeps the source going until ctx is done, calling report on every change (and with Available false at
	// once when it loses its source). Run is called from one goroutine, and report is called one call at a time.
	// Run must return only after every goroutine it started has stopped.
	Run(ctx context.Context, report func(RuntimeReport))
	// Name is the short word the source goes by in Describe, such as "herdr".
	Name() string
	// Describe is the text for a report the source made while available, such as "herdr 0.9.5 ok".
	Describe(r RuntimeReport) string
}

// runtimeView is what the runtime source last said.
type runtimeView struct {
	Available bool
	// State is empty when the source answered but the terminal is not an agent's.
	State State
	// Version is the last report's version text.
	Version string
	// BlockedSeen is the last time the runtime reported blocked; zero if never.
	BlockedSeen time.Time
}

// hooksView is what the hooks source says now, after the staleness rule.
type hooksView struct {
	Present bool
	State   State
	Kind    Kind
	At      time.Time
}

// combine applies the combination rules to the two views. The Since field is left zero: the tracker sets it.
func combine(rt runtimeView, hk hooksView) (Activity, bool) {
	if rt.Available && rt.State != "" {
		// The runtime says idle, the hooks say a dialog is open, and the runtime has not seen a dialog since the
		// record: the runtime's idle may be a prompt it does not recognise, and "free" on weak evidence is the error this
		// package exists to remove. If the runtime did see blocked after the record and is now idle, the person
		// refused or dismissed the dialog, no hook fires then, and the runtime is right.
		if rt.State == StateIdle && hk.Present && hk.State == StateBlocked && !rt.BlockedSeen.After(hk.At) {
			return Activity{State: StateBlocked, Kind: hk.Kind, Source: "hooks"}, true
		}
		if hk.Present && hk.State == rt.State {
			return Activity{State: rt.State, Kind: hk.Kind, Source: "both"}, true
		}
		return Activity{State: rt.State, Source: "runtime"}, true
	}
	if hk.Present {
		return Activity{State: hk.State, Kind: hk.Kind, Source: "hooks"}, true
	}
	return Activity{}, false
}
