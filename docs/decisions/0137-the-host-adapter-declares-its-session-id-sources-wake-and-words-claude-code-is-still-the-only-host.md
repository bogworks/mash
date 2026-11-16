---
status: proposed
date: 2026-11-16
decision-makers: Randy Bias (mash maintainer)
---

# The host adapter declares its session-id sources, wake and words; Claude Code is still the only host

## Context and Problem Statement

[ADR 0032](0032-host-adapter-seam.md) put the agent host behind an `Adapter`: how a hook is read, who owns a spool, how
mail is presented and how serve is woken. [ADR 0040](0040-hook-ownership-session-id-launch-token-then-process-chain.md)
decided hook ownership by session id, launch token and process chain. Both were written with one host, and the code
still names Claude Code in places the adapter does not reach: the environment variable that holds the session id is
spelled out in the hook, in serve and in the endpoint's lease identity; the words "Claude Code" and "Claude process"
are written into the other-launch notice, the status lines, the inert-session instructions and the service
description; and the status checks that read Claude Code's settings run for any host. A second host would have to
find and change each of these.

## Decision Drivers

* A second host must be an adapter, not a search through the callers.
* Nothing a user or another endpoint can observe may change: every printed string, every stored record and every wire
  field stays byte for byte as it was.
* Only one host exists, so only the seam is made now.

## Considered Options

* Leave the host-specific names where they are, and move them when the second host arrives.
* Move them behind the adapter now, with tests that pin the strings (chosen).

## Decision Outcome

Chosen: move them behind the adapter now. Claude Code is still the only host, and `Select("")` still returns it.

What moved behind the adapter:

1. **Session-id sources.** `SessionSources()` says where the host keeps its session id: an environment variable, a key
   in the hook's input, and whether the process ancestry identifies the session. Claude Code's are
   `CLAUDE_CODE_SESSION_ID`, `session_id` and yes. The hook, serve's waiting record and the owner record read the id
   through `host.SessionID`. The endpoint cannot import the host package, so serve tells it the variable name once at
   start (`endpoint.SetSessionEnv`); unset, it reads the same variable as before. The ownership rules of ADR 0040 are
   unchanged; only where their inputs come from is.
2. **Wake.** `Wake()` names how an idle session is woken: by the MCP channel notification, by a command, or not at all.
   Claude Code's is the channel. `SpoolsFromStart` is true whenever the wake is not the channel.
3. **Words.** `Words()` gives the host's product name, the name of its process and the prefix of the service
   description. The other-launch notice, the `Next:` steps in `status`, the inert-session instructions and the service
   description use them; the endpoint takes the instruction text and description prefix as values set by serve, with
   today's text as the default.
4. **Per-host status checks.** The channel allowlist, managed-settings and MCP-environment lines and the client-version
   warning are Claude Code's, so `status` runs them only when the adapter says it has them
   (`ClaudeSettingsChecks`).

Not changed, and not part of this decision:

* Detection of the host by the MCP client's name, and the rule that an unrecognised host is never silently read as
  Claude Code, come with the second host, because only then does a default change behavior.
* The intake of activity from a host that has no hooks, and a per-host installer, are later work.
* No second adapter is added.

This amends ADR 0032 (the adapter's surface grows) and ADR 0040 (the session id's source is declared by the adapter).
Neither is edited.

### Consequences

* Good, because adding a host means writing an adapter against a surface that already carries what differs.
* Good, because golden tests pin the notice, the status lines, the instructions, the service description and the owner
  record, so any drift shows.
* Bad, because the endpoint holds two package-level settings that serve sets, until the endpoint is configured through
  its options.

### Confirmation

The existing tests pass unchanged. New tests pin the moved strings and the owner record, assert that Claude Code's
`Trigger` reads the declared hook field, and assert Claude Code's wake and `SpoolsFromStart`.

## Pros and Cons of the Options

### Leave the names where they are

* Good, because it changes nothing now.
* Bad, because the second host's change then touches the hook, serve, the endpoint and `status` at once, with nothing
  pinning what must stay the same.

### Move them behind the adapter now (chosen)

* Good, because the change is mechanical and every moved string is pinned by a test.
* Bad, because it adds adapter methods that only one host implements until the second arrives.

## More Information

Amends [ADR 0032](0032-host-adapter-seam.md) and
[ADR 0040](0040-hook-ownership-session-id-launch-token-then-process-chain.md).
