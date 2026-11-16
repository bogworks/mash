// Package host isolates what is specific to the agent host a mash session runs in (Claude Code today), so other
// hosts (Hermes, Pi) can plug in later. An Adapter says how a host's hook invocation is read, who owns a session's
// spool, how spooled mail is presented, and how serve's wake is delivered.
//
// The wake is today the MCP channel notification serve already passes to the endpoint as its Deliverer; Waker is the
// seam through which an adapter shapes it. A future durable adapter (issue #43: explicit receive/ack MCP tools, a
// startup-declared durable mode) will extend this interface; it is deliberately not built here.
package host

import (
	"fmt"
	"strings"

	"github.com/bogworks/mash/internal/spool"
)

// Adapter is how mash talks to the agent host a session runs in (Claude Code today).
type Adapter interface {
	Name() string
	// SessionSources says where this host puts its session id: serve and the hook read it from here, never from a
	// host-specific name spelled out in the callers.
	SessionSources() SessionSources
	// Wake says how serve wakes an idle session of this host.
	Wake() WakeKind
	// Words is the host's name for itself and its processes, as the notices and status lines say it.
	Words() Words
	// ClaudeSettingsChecks is true when `mash status` should run the checks that read Claude Code's settings files
	// (channel allowlist, managed settings, MCP environment) and warn on the client version. Only Claude has them.
	ClaudeSettingsChecks() bool
	// Trigger reads a host hook invocation; ok=false means it is not a delivery point for us (do nothing).
	Trigger(stdin []byte, env func(string) string) (t Trigger, ok bool)
	// OwnerToken is what the lease-holding serve records as the spool's owner. env reads serve's environment.
	OwnerToken(env func(string) string) string
	// Owns reports whether this hook process may drain a spool recorded with owner.
	Owns(owner string, hook HookIdentity) bool
	// Match is Owns with the reason: which rule matched, or why every rule failed.
	Match(owner string, hook HookIdentity) (ok bool, why string)
	// Present renders entries for the host; shown are the entries actually rendered (only they are drained).
	Present(p Presentation, entries []spool.Entry) (out []byte, shown []spool.Entry)
	// Notice renders a one-off message of mash's own (the other-launch and lease-wait notices) the way the host takes
	// hook output, for the hook event it ran for.
	Notice(event, text string) []byte
	// SpoolsFromStart is true whenever Wake is not WakeChannel: the host takes no channel delivery (Pi never sees the
	// channel notification), so serve then spools mail from its first message and acks only what a hook drained, never on a channel write.
	SpoolsFromStart() bool
	// Waker returns the delivery serve uses to wake the session, given the MCP channel notification.
	Waker(notify func(text string, meta map[string]string) error) func(text string, meta map[string]string) error
}

// TwoPhaser is implemented by an adapter whose hook offers a batch without draining it (Pi, ADR 0140): the host
// acknowledges what it accepted with a second invocation, `mash hook --ack <token>`. An adapter without it (Claude
// Code, headless) drains in the one step, as it always has.
type TwoPhaser interface {
	TwoPhase() bool
	// Offer renders out, what Present printed, together with the ack token naming the entries it shows.
	Offer(out []byte, token string) []byte
}

// IsTwoPhase reports whether a delivers in two phases.
func IsTwoPhase(a Adapter) bool {
	t, ok := a.(TwoPhaser)
	return ok && t.TwoPhase()
}

// SessionSources is where a host carries its session id.
type SessionSources struct {
	// Env is the environment variable that holds the session id, in serve's and the hook's environment.
	Env string
	// HookField is the key of the session id in the hook's stdin JSON.
	HookField string
	// AncestryUnique is true when the host's process ancestry identifies the session (serve's parent pid is the
	// host process, ADR 0040 rule 3).
	AncestryUnique bool
}

// SessionID is the session id a's host sets in the environment env reads; "" when none, or env is nil.
func SessionID(a Adapter, env func(string) string) string {
	if env == nil || a.SessionSources().Env == "" { // a host with no session variable has no session id
		return ""
	}
	name := a.SessionSources().Env
	if name == "" { // a host with no session id in the environment
		return ""
	}
	return env(name)
}

// WakeKind is how a host is woken when mail arrives for an idle session.
type WakeKind int

const (
	// WakeChannel is the MCP channel notification serve already sends (Claude Code).
	WakeChannel WakeKind = iota
	// WakeCommand is a command the host runs to be woken.
	WakeCommand
	// WakeNone is a host with no wake: mail waits for a hook or a poll.
	WakeNone
)

// Words is how notices and status lines name the host.
type Words struct {
	// Product is the host product ("Claude Code").
	Product string
	// Process is what its process is called in a notice ("Claude process").
	Process string
	// ServicePrefix starts the micro service description; the endpoint name follows it ("Claude Code (mash): ").
	ServicePrefix string
}

// Trigger is a recognised host hook invocation. Subagent is set when it ran for a subagent's tool call, not the main
// session's: it must never drain, only show that hooks run.
type Trigger struct {
	Event, SessionID string
	Subagent         bool
}

// HookIdentity is what a hook process knows about the session it runs in: its session id (from its environment, else
// the hook's stdin), the launch token (MASH_LAUNCH_TOKEN) it inherited, and the pids of the serves recorded as waiting
// for the lease (spool.Waiters, ADR 0043).
type HookIdentity struct {
	SessionID, LaunchToken string
	Waiters                []int
}

// Presentation is what Present needs to render a batch.
type Presentation struct {
	Name, Owner           string
	Event                 string // the Trigger's event, for hosts that echo it
	BatchMsgs, BatchBytes int
}

// Select returns the adapter for a host name: "" or "claude" is Claude Code, "pi" is Pi. The host is chosen by name
// only (MASH_HOST); nothing is detected from the environment.
func Select(name string) (Adapter, error) {
	switch name {
	case "", "claude":
		return Claude{}, nil
	case "headless": // no host at all (ADR 0139); never detected, only named
		return Headless{}, nil
	case "pi":
		return Pi{}, nil
	}
	return nil, fmt.Errorf("mash: unknown host %q (MASH_HOST); known hosts: %s", name, strings.Join(Known(), ", "))
}

// Known lists the host names Select accepts.
func Known() []string { return []string{"claude", "headless", "pi"} }
