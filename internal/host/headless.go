package host

import (
	"github.com/bogworks/mash/internal/spool"
)

// IsHeadless reports whether a is the headless adapter: serve then has no MCP client to talk to.
func IsHeadless(a Adapter) bool { _, ok := a.(Headless); return ok }

// Headless is the adapter for an endpoint that runs with no agent host at all (MASH_HOST=headless, ADR 0139): serve
// keeps the endpoint online and its mail is read with the CLI. There is no session, no hook and no wake, so every
// method that serves a hook or a wake is inert and safe to call.
//
// It is never detected: it is chosen only by name.
type Headless struct{}

func (Headless) Name() string { return "headless" }

// SessionSources: no host, so no session id anywhere. The endpoint is not one session of anything.
func (Headless) SessionSources() SessionSources {
	return SessionSources{Env: "", HookField: "", AncestryUnique: true}
}

// Wake: none. Mail waits in the inbox and the spool until it is read.
func (Headless) Wake() WakeKind { return WakeNone }

// Words names the serve process, as there is no host product.
func (Headless) Words() Words {
	return Words{Product: "mash serve (headless)", Process: "serve process", ServicePrefix: "mash (headless): "}
}

// ClaudeSettingsChecks: none; Claude Code's settings are not this endpoint's concern.
func (Headless) ClaudeSettingsChecks() bool { return false }

// Trigger: no host runs a hook for this adapter, so nothing is ever a delivery point.
func (Headless) Trigger([]byte, func(string) string) (Trigger, bool) { return Trigger{}, false }

// OwnerToken is the owner record Claude writes (serve's parent pid; no session, since none is declared), so a spool
// is readable by anything that expects one.
func (Headless) OwnerToken(env func(string) string) string { return Claude{}.OwnerToken(env) }

// Owns and Match follow the Claude rules. They are unused, since no hook exists to ask.
func (Headless) Owns(owner string, h HookIdentity) bool { return Claude{}.Owns(owner, h) }

func (Headless) Match(owner string, h HookIdentity) (bool, string) { return Claude{}.Match(owner, h) }

// Present renders what Claude renders. It is unused, as nothing drains the spool.
func (Headless) Present(p Presentation, entries []spool.Entry) ([]byte, []spool.Entry) {
	return Claude{}.Present(p, entries)
}

// SpoolsFromStart is true because the wake is not the channel.
func (h Headless) SpoolsFromStart() bool { return h.Wake() != WakeChannel }

// Waker is nil: there is nothing to wake, and the endpoint sends no wake and counts none (so no hookless verdict).
func (Headless) Waker(func(string, map[string]string) error) func(string, map[string]string) error {
	return nil
}
