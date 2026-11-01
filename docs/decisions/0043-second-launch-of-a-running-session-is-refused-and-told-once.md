---
status: accepted
date: 2026-11-01
decision-makers: Randy Bias (mash maintainer)
---

# A second launch of a running session is refused, and told once

## Context and Problem Statement

`claude --resume <id>` on a session that is still running starts a second Claude process with the same session id and
the same `MASH_NAME`, and with a launch token a new `MASH_LAUNCH_TOKEN`. The standard plugin launch sets none, so
tokens alone cannot tell the copies apart. Its `mash serve` waits for the lease. Its hooks, however, matched
rule 1 of ADR-0040 (session id equal) and drained the lease holder's spool, so mail was split between two processes
without a word. The refusal notice was also pull-only (`status`, `who`), so nobody in the second copy learned that it
gets no mail. ADR-0040 listed this as a known bad consequence "to be settled before release".

## Decision Drivers

* A copy that does not hold the lease must never drain the holder's spool.
* A refused copy has to be told, in its own session, without polling `status`.
* `/clear` keeps the launch token and must stay unaffected.

## Considered Options

* Drop rule 1 (session id) altogether.
* Narrow rule 1: it does not match when both launch tokens are non-empty and differ.
* Keep rule 1 and only document the hazard.
* Narrow rule 1 and also detect a copy through the waiting serve (a record in the spool).

## Decision Outcome

Chosen option: narrow rule 1 and add the waiter rule, because a `--resume` launched with tokens gets a new launch
token while `/clear` and the holder's own hooks keep theirs, and a launch with no tokens (the standard plugin launch)
is still caught by the serve that waits for the lease: its parent Claude process is an ancestor of that copy's hooks.

This amends ADR-0040.

* **Match.** Rule 1 does not match when the hook's and the owner's launch tokens are both non-empty and differ. Rule 2
  cannot match then, and rule 3 (process ancestry) is unchanged: a hook that really descends from the recorded pid is
  still accepted. The refusal reason starts `session id matches but launch token differs: another launch of this
  session (a --resume of a running session?)` and prints no token. `host.IsOtherLaunch(why)` recognises it.
* **Waiter.** A serve that waits for the lease writes `.waiting.<pid>` in the spool (JSON `{pid, session}`, pid its
  parent Claude process, under the spool lock) and removes it when it binds and on exit; `OwnRecord` also deletes every
  `.waiting.*` naming the new owner's pid. A hook whose ancestry contains a recorded live waiter (checked with signal
  0) and not the owner's pid, the owner not being a waiter itself, is refused with the same other-launch reason before
  rule 1 is tried, tokens or not. A hook that descends from the owner is accepted as before. Without a launch token
  the notice's once-key is session + NUL + the waiter's pid.
* **Notice.** A hook refused for that reason, besides the stderr line and `.refused` of ADR-0040, prints once per
  (session id, launch token) pair a hook `additionalContext`, in the shape and event name `Present` uses: "mash: this
  copy of the session is not receiving mail. Another Claude process of this same session (pid N) holds the endpoint
  NAME; mail goes to that process. End one of the two, or run this one under a different MASH_NAME." "Once" is the
  spool's `.told`, a 16-hex-digit sha256 of session + NUL + launch token, written under the spool lock the hook already
  holds. `Own` clears `.told` with `.refused`, so a new lease holder lets each copy be told again.
* A refusal for any other reason is unchanged: stderr only, nothing on stdout. A refused hook never drains, touches
  `.hooked` or marks anything else.

### Consequences

* Good, because two live processes on one session id no longer split the mail.
* Good, because the second copy learns it is mailless in its own context, once.
* Bad, because a legitimate relaunch whose serve has not bound yet (its hooks run with a new token before it owns the
  spool) is briefly refused and told. Its mail stays spooled for its own serve, which delivers it once it binds, and
  `Own` then clears `.told`.
* Neutral, because `/clear` keeps the launch token, so it is unaffected; `--resume` gets a new token.
* Neutral, because with no launch token and no recorded waiter the session-id rule works as in ADR-0040.
* Bad, because the waiter rule needs the copy's serve to be recorded and a visible process chain: a broken process
  layout (serve behind a wrapper, a PID namespace, a chain deeper than 10) falls back to the old behaviour.

### Confirmation

* `internal/host/claude_test.go`: `TestMatchOtherLaunchOfTheSameSession`, `TestMatchWaiterAncestorIsAnotherLaunch`; `internal/spool/spool_test.go`: `TestWaiters`.
* `cmd/mash/hook_chain_test.go` (real processes): `TestHookOtherLaunchOfTheSameSessionIsRefusedAndToldOnce`, `TestHookWaiterWithEmptyTokensIsRefusedAndToldOnce`; `cmd/mash/retry_test.go`: `TestServeRecordsItselfWhileWaiting`.
* Evidence: `docs/evidence/2026-11-01-dual-resume/RECEIPT.md`.

## Pros and Cons of the Options

### Drop rule 1 (session id) altogether

* Good, because a second launch can never pass on the session id.
* Bad, because a session whose process chain is broken (the case rule 1 was added for) would be refused again, silently.

### Narrow rule 1: it does not match when both launch tokens are non-empty and differ

* Good, because a resumed copy of a running session is refused while every case rule 1 covers (same launch, any process layout) still works.
* Good, because when either token is empty nothing changes.
* Bad, because a relaunch whose serve has not yet bound is briefly refused; its mail waits for its own serve.

### Keep rule 1 and only document the hazard

* Good, because nothing changes.
* Bad, because mail is split between the two processes with no signal, as the live run showed.

### Narrow rule 1 and also detect a copy through the waiting serve

* Good, because a plain `claude --resume` with no launch token is refused too.
* Good, because a layout where the waiter is not an ancestor changes nothing.
* Bad, because it adds spool state (`.waiting.*`) that must be cleaned on bind and exit.

## More Information

* Amends ADR-0040 (rule 1 and its known-bad consequence). Related: ADR-0013 (one live session per name).
* Code: `internal/host/claude.go`, `internal/spool/spool.go`, `cmd/mash/hook.go`.
