// Package host isolates what is specific to the agent host a mash session runs in (Claude Code, Pi), so other
// hosts (Hermes) can plug in. An Adapter says how a host's hook invocation is read, who owns a session's
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
	// SpoolsFromStart is true when the host takes no channel delivery (Pi never sees the channel notification): serve
	// then spools mail from its first message and acks only what a hook drained, never on a channel write.
	SpoolsFromStart() bool
	// Waker returns the delivery serve uses to wake the session, given the MCP channel notification.
	Waker(notify func(text string, meta map[string]string) error) func(text string, meta map[string]string) error
}

// Trigger is a recognised host hook invocation. Subagent is set when it ran for a subagent's tool call, not the main
// session's: it must never drain, only show that hooks run.
type Trigger struct {
	Event, SessionID string
	Subagent         bool
}

// HookIdentity is what a hook process knows about the session it runs in: its session id (from its environment, else
// the hook's stdin) and the launch token (MASH_LAUNCH_TOKEN) it inherited.
type HookIdentity struct{ SessionID, LaunchToken string }

// Presentation is what Present needs to render a batch.
type Presentation struct {
	Name, Owner           string
	Event                 string // the Trigger's event, for hosts that echo it
	BatchMsgs, BatchBytes int
}

// Initer is an optional Adapter capability: a hook event that only answers the host (no drain, no lock) and the
// bytes to print. handled=false means the trigger is an ordinary delivery point.
type Initer interface {
	Init(t Trigger) (out []byte, handled bool)
}

// Select returns the adapter for a host name: "" or "claude" is Claude Code. spoolDir is the endpoint's spool
// directory, which hosts that wake through it (Pi) need.
func Select(name, spoolDir string) (Adapter, error) {
	switch name {
	case "", "claude":
		return Claude{}, nil
	case "pi":
		return Pi{SpoolDir: spoolDir}, nil
	}
	return nil, fmt.Errorf("mash: unknown host %q (MASH_HOST); known hosts: %s", name, strings.Join(Known(), ", "))
}

// Known lists the host names Select accepts.
func Known() []string { return []string{"claude", "pi"} }
