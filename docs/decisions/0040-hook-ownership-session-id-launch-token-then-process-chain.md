---
status: accepted
date: 2026-11-01
decision-makers: Randy Bias (mash maintainer)
---

# Hook ownership: session id, launch token, then process chain

## Context and Problem Statement

A hook may drain an endpoint's spool only if it belongs to the Claude Code session whose `mash serve` holds the lease
(ADR-0025). Until now mash decided that by walking the hook's parent processes for serve's parent pid (`pid:N`, PR #49).
Where the process layout differs (serve started through a shell that does not `exec`, a PID namespace or sandbox around
hooks or MCP servers, an IDE or desktop host, a chain deeper than 10 levels), the hook returned silently. The mail stayed
spooled, serve kept waking the session with backoff up to 30 minutes, each wake cost a turn, and `status` said "hooks
have not run yet" although they ran on every tool call. Nothing was logged.

The session id alone is not enough: `/clear` gives hooks a new session id while serve keeps running.

## Decision Drivers

* Another session under the same name, or one whose serve lost its environment, must never drain this session's mail.
* The check runs on every tool call, so it must be cheap.
* A refusal must be visible, not silent.
* Claude Code sets `CLAUDE_CODE_SESSION_ID` in hook and stdio MCP server subprocesses, and mash already has a per-launch
  token, `MASH_LAUNCH_TOKEN`, that both serve and hooks inherit from the agent's environment.

## Considered Options

* Keep the process chain only, and log the refusal.
* Accept any hook when no rule can decide ("adopt" an unknown session).
* Record a JSON owner (pid, session id, launch token) and accept a hook that matches any one of them, refusing and
  recording the rest.

## Decision Outcome

Chosen option: the JSON owner with three rules, because each rule is an identity the other session cannot have by
accident, and no rule adopts an unknown session.

`serve` writes `.owner` as `{"pid":N,"session":"<CLAUDE_CODE_SESSION_ID>","launch":"<MASH_LAUNCH_TOKEN>"}`, empty
fields omitted, and `.session` as the legacy text `pid:N` (N is serve's parent pid), `.owner` first, both atomic under
the spool lock. A hook reads `.owner` when it exists and `.session` otherwise; an `.owner` that exists but is
unreadable or empty is refused, never read as "no owner". A hook owns the spool if any one of these holds, cheapest first:

1. **Session:** the hook's session id (`session_id` from its stdin, else `CLAUDE_CODE_SESSION_ID` from its environment)
   equals the recorded one. Both must be non-empty.
2. **Launch:** both launch tokens are non-empty and equal. It is stable across `/clear`. An empty token never matches.
3. **Process:** the recorded `pid` is among the hook's ancestors (the previous rule, unchanged).

Legacy owners keep their behaviour: `pid:N` text is checked by rule 3 alone, and an empty or other older owner is
accepted.

A hook that matches none is **refused, and the refusal is recorded**, under the spool lock: `<spool>/.refused` (JSON:
time, the reasons, the hook's session id, its first 10 ancestor pids) is written only if absent or older than 60
seconds, and one line goes to stderr (which the plugin appends to `hook.log`): `mash hook: not this session's spool
(<why>); mail left for its owner`. Nothing goes to stdout. `Own` clears `.refused`, so a new lease holder starts clean.
`status` reports `delivery: REFUSED` when a refusal is newer than the bind and no hook has run since it. `MASH_HOOK_TRACE=1`
makes every hook run log its pid chain, both session ids and the rule that matched.

This supersedes the ownership clause of ADR-0025 (ownership by process chain alone, `pid:N`). It also changes the
`Owns` signature recorded in ADR-0032: `host.Adapter` is now `OwnerToken(env)`, `Owns(owner, HookIdentity)` and
`Match(owner, HookIdentity) (ok, why)`, and `Trigger` also returns the stdin `session_id`.

Version skew is safe in both directions. A hook built before this change reads only `.session`, finds `pid:N` and
enforces ancestry by rule 3 alone: it neither refuses nor bypasses ownership, though it cannot use the session id or
launch token, so it may not recognise a session whose process chain is broken. (An earlier draft wrote the JSON into
`.session`; an older hook treated any owner not starting with `pid:` as "accept all", so mixed versions could drain
another session's mail.) A newer hook with an older serve finds only `.session` and applies the legacy rule. Both come
from the same `mash` on PATH in practice, and `mash version` shows both.

### Consequences

* Good, because a wrapper, sandbox or IDE host that keeps the session id (or a launcher-set `MASH_LAUNCH_TOKEN`) works.
* Good, because a refused hook is recorded and shown instead of leaving the session being woken for nothing.
* Bad, because `/clear` under an unrecognised process layout, without a launch token, is refused (visibly). The fix is
  a fresh `MASH_LAUNCH_TOKEN` per launch (for example `MASH_LAUNCH_TOKEN=$(uuidgen) claude ...`).
* Bad, because two live processes that share a session id (a second `claude --resume` of a running session, if Claude
  Code keeps the id) match rule 1; only one holds the lease, but the other's hooks would drain its mail. To be settled
  before release: drop rule 1 or document it.
* Bad, because a token reused across launches (a static shared `MASH_LAUNCH_TOKEN`) defeats rule 2: a waiting session
  with the same name and token would drain the holder's mail. Serve logs a warning when a session waiting for the lease
  sees its own token equal to the holder's.
* Bad, because rule 1 with two live processes on one session id (a second `claude --resume <id>` of a running session,
  inferred) lets both drain; low likelihood.
* Neutral, because the launch token is an identity hint, not a secret or credential: it sits in plaintext in the
  spool's `.owner` and, per ADR 0016, in the lease record in NATS KV. Only the same Unix user, the endpoint itself
  and the NATS server can read it.
* Neutral, because Claude Code could change `CLAUDE_CODE_SESSION_ID`; rules 2 and 3 remain.

### Confirmation

* `internal/host/claude_test.go`: `TestOwnsBySessionID`, `TestOwnsByLaunchToken`, `TestOwnsRefusesAnotherSession`,
  `TestOwnsEmptyTokensNeverMatch`, `TestOwnsLegacyPidOwner`, `TestOwnerTokenIsJSON`, `TestOwnsStopsAtTheOwner`.
* `cmd/mash/hook_chain_test.go` (real processes): `TestHookOwnershipThroughANonExecWrapper`,
  `TestHookUnderAnotherClaudeIsRefusedAndRecorded`, `TestHookAfterClearWithLaunchToken`,
  `TestHookOwnershipThroughARealProcessChain`.
* `cmd/mash/status_tool_test.go`: `TestStatusToolReportsRefusedHooks`.

## Pros and Cons of the Options

### Process chain only, log the refusal

* Good, because it is the smallest change.
* Bad, because every layout the walk cannot follow stays unrecognised.

### Adopt an unknown session

* Good, because mail would always flow.
* Bad, because a second session under the same name could take this session's mail; that is the harm ADR-0025's
  ownership rule exists to prevent.

### JSON owner, three rules, refuse and record the rest

* Good, because each rule identifies one session, and a refusal is visible.
* Bad, because an owner record in a new format needs serve and hook of one version.

## More Information

* Supersedes the ownership clause of ADR-0025; amends the `Owns` signature in ADR-0032. Related: ADR-0024 (hook delivery).
* Docs: `docs/operations.md` "Hook delivery", `docs/install.md` (`MASH_LAUNCH_TOKEN`). Code: `internal/host/claude.go`,
  `internal/spool/spool.go`, `cmd/mash/hook.go`, `cmd/mash/status.go`.
