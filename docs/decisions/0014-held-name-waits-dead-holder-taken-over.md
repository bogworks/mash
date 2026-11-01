---
status: accepted
date: 2026-10-23
decision-makers: Randy Bias (mash maintainer)
recorded: 2026-10-31 (backfilled)
---

# A session that finds its name held waits and retries; a dead holder on the same machine is taken over

## Context and Problem Statement

The lease (ADR-0013) first made a session that found its name held refuse to start, permanently. In practice the
holder was usually the same session's dead predecessor, whose lease had up to 90 s left. A live run on 2026-10-23
showed a resumed session that "stayed offline 3+ minutes past the 90 s lease because a startup lease conflict is
terminal" (`docs/plans/2026-10-23-lease-retry.md`). Even with retries, every restart then waited "about 40 s with no
mail" for the old lease to expire (pull request #44). A consumer needed restarts and upgrades without a gap in mail.

## Decision Drivers

* A restarted session should get its mail without an operator restarting it again.
* A live holder must never be displaced: mail for one name must not be split (ADR-0013).
* A transient network or server error at startup should not end the session.
* A process on another machine, or in another container, must never be judged dead from here.

## Considered Options

* Refuse permanently when the name is held (the original design)
* Wait and retry until the lease is free
* Also take over at once a lease whose holder process is gone, judging "same machine" by host name and process name
* Also take over at once, judging "same machine" by boot id and pid namespace, and the process by its start time

## Decision Outcome

Chosen option: wait and retry, and take over at once from a dead holder on the same machine, judged by boot id, pid
namespace and process start time. The steps:

* 2026-10-23 (commit `60c7879`): a session that finds its name held keeps its tools, says mail is off, and retries
  every 10 s. "The waiter never takes a name whose holder is continuously renewing." A lease lost after a session has
  started stays terminal.
* 2026-10-28 (pull requests #8 and #9): transient errors, while waiting or on the first attempt, are retried with
  back-off up to 2 minutes, within a 15-minute budget; a permanent error ends the wait.
* 2026-10-29 (pull request #44): the lease records a machine identity (on Linux, the boot id and pid namespace; on macOS, the boot's session id; none
  elsewhere) and
  the holder's process start time. A session takes over at once only when the lease's machine equals its own and the
  holder's pid no longer exists, or now belongs to a process with a different start time. The takeover is a
  compare-and-set, so it loses to any write the holder made since it was read. A lease with no machine identity is
  never judged and expires normally.

Review of pull request #44 rejected judging by host name and process name, because hosts and containers can share a
host name and a live remote holder's pid is absent locally. The values 10 s, 2 minutes and 15 minutes were chosen
without a recorded reason.

### Consequences

* Good, because a restart no longer needs a second restart, and a dead predecessor no longer costs the lease's TTL. A
  live run on 2026-10-31 measured 0.84-1.16 s from `kill -9` of a session to mail delivered by its replacement, against
  81.59 s before pull request #44 (`docs/evidence/2026-10-31-lease-rtt-live/`).
* Good, because a live holder, or any holder on another machine, keeps its name.
* Bad, because on platforms without a readable machine identity a dead holder is still waited out.
* Neutral, because the waiting session exposes its tools, which report that mail is off until the name is free.

### Confirmation

* `cmd/mash/retry_test.go`: `TestServeRetriesHeldLease`, `TestServeDoesNotDisplaceRenewingHolder`,
  `TestServeWaitSurvivesTransientErrors`, `TestServeTransientErrorsEndAfterTheBudget`,
  `TestServeHeldThenNonHeldErrorIsTerminal`, `TestServeFirstOpenTransientIsRetried`.
* `internal/endpoint/lease_handoff_test.go`: `TestLeaseTakenOverFromDeadHolderOnSameMachine`,
  `TestLeaseNotTakenFromLiveHolderOnSameHost`, `TestLeaseSameHostnameOtherMachineNotTakenOver`,
  `TestLeaseNoMachineNotTakenOver`, `TestLeaseReusedPIDTakenOver`, `TestLeaseTakeoverLosesToLiveWriter`.
* `cmd/mash/main_test.go` `TestServeLeaseConflict` and `TestServeExitsWhenLeaseLost`.

## Pros and Cons of the Options

### Refuse permanently

* Good, because it is simple and never displaces anyone.
* Bad, because a restarted session stays without mail until someone restarts it after the lease expires.

### Wait and retry

* Good, because the session recovers by itself once the lease expires.
* Bad, because every restart still waits out a dead predecessor's lease.

### Takeover judged by host name and process name

* Good, because it is portable.
* Bad, because two machines or containers can share a host name, so a live holder could be displaced.

### Takeover judged by boot id, pid namespace and start time

* Good, because a reused pid or another machine is never mistaken for the dead holder.
* Bad, because it needs operating-system support; elsewhere it falls back to waiting.

## More Information

* `docs/plans/2026-10-23-lease-retry.md`; `docs/evidence/2026-10-23-s5-lease-retry-live.md`;
  `docs/evidence/2026-10-31-lease-rtt-live/` (pull request #173).
* Pull requests #8, #9 and #44. Code: `cmd/mash/serve.go` (retry timing), `internal/endpoint/endpoint.go`
  (`takeLease`), `internal/endpoint/holder.go`, `internal/endpoint/machine_linux.go`.
* `docs/operations.md` (troubleshooting: the name is already in use).
* Supersedes the refusal in spec §7.1 step 4; the lease itself stands (ADR-0013).
* Related: ADR-0013 (lease), ADR-0015 (a deprovisioned name is fenced, never waited for).
* **Not yet true:** spec §1.1 goal 3 and §7.1 step 4 still describe refusal; `docs/install.md` says a session under a
  held name "starts without mail tools", while it keeps its tools and retries.
