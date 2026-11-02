---
status: accepted
date: 2026-11-02
decision-makers: Randy Bias (mash maintainer)
---

# Relay by asking is traced, not blocked: content-free trace lines and counters

## Context and Problem Statement

A session that receives a request from one endpoint can pass it to another ("ask c what the build status is"), so that a
verified sender's request reaches a third endpoint through a session that was only asked to relay it. mash labels trust per
message and per signer ([ADR 0020](0020-verified-mail-is-a-delegated-request.md)); a relay is the relayer's own verified
message, and the origin is invisible to the third endpoint unless the relayer says so. [ADR 0063](0063-in-reply-to-is-signed-and-a-sender-can-declare-a-relay-with-a-signed-mash-via-both-are-shown-only-when-they-verify.md)
lets an honest relayer declare it with a signed `Mash-Via`. Nothing yet tells an operator how often relaying happens,
declared or not. mash does not judge payloads ([ADR 0021](0021-message-bodies-are-shown-quoted.md)), and the maintainer's
ruling is to look at logs and traces first (roadmap N14).

## Decision Drivers

* No message body is ever read or classified by mash.
* Nothing may be blocked or delayed while the false-positive rate is unknown: delegation is the point of mash.
* The record must come from what mash already verifies (signed sender, signed `Mash-Via`) and from time.
* A failure of the instrumentation must never reach delivery or a send.

## Considered Options

* Content-free trace lines and counters, from signed headers and timing (logs and traces).
* A seam in the send path with allow, flag and hold outcomes.
* A body-pattern detector for forwarded text.
* Blocking a send that follows another sender's mail.

## Decision Outcome

Chosen option: "content-free trace lines and counters", because it gives the operator the data to decide on a rule later
without mash judging content or risking a refused legitimate send.

* mash records, from signed headers and timing only, (1) when an inbound mail declares a relay and (2) when this endpoint
  sends to a third party soon after mail from someone else. It records nothing else, and never a body.
* Window: `relay_window` in `config.json` or `MASH_RELAY_WINDOW`, a duration from 1 s to 1 h, default 60 s.
* `mash serve` keeps the last 256 inbound mails (stream sequence, sender name, receipt time) of `verified` or `stale`
  senders, pruned by the window, and three counters per run: inbound mails whose `Mash-Via` verified, sends within the
  window of another sender's mail (relay candidates), and sends that declared a relay.
* One line each, to the endpoint's log, prefixed `trace relay:`: `mail #<seq> from <from> declares a relay of #<n> from
  <origin>`; `sent to <to> declaring a relay of #<via> from <origin>`; `sent to <to> <N> s after mail #<seq> from <from>
  (declared: yes|no)`. A candidate is a send, after a successful publish, to an endpoint other than the sender of a mail
  received within the window and other than this endpoint; a reply to that sender is not a candidate, a send to oneself is
  ignored. The line names the most recent such mail.
* `mash status` shows `relay trace: inbound with via N, sends within <window>s of another sender's mail N, declared relays
  N`. serve writes the counters, with its instance id and start time, to `relay-counters.json` (0600, atomic, at most once
  a second after a change, by a background worker, so a slow state directory never delays a receipt or a send; shutdown
  waits for the last write for 2 s at most) in the endpoint's state directory; the status command, another process, reads it and adds
  `(since <time>)`. A missing or unreadable file shows `relay trace: none recorded`. Counters are per serve run.
* No flag, no block, no delay, no pattern and no operator rule. A tracker failure is swallowed.

### Consequences

* Good, because the operator can see how often relaying happens, declared or not, without mash reading anything.
* Good, because nothing legitimate is refused and nothing waits.
* Bad, because time correlation proves adjacency, not causation: a busy endpoint is a candidate on most sends.
* Bad, because evasion is trivial: wait past the window, retype or paraphrase, send through another endpoint, or never declare.
  `Mash-Via` is an honesty mechanism, not a control.
* Bad, because the shell's `status` cannot tell whether the run that wrote the file is still running; it shows the run's
  start time instead of hiding a possibly stale file.
* Neutral, because a later rule, if the data justifies one, needs its own decision.

### Confirmation

* `internal/endpoint/relay_test.go`: `TestRelayTrackerRingIsBounded`, `TestRelayTrackerPrunesByWindow` (injectable clock),
  `TestRelayTrackerCountersAndExclusions` (self and reply exclusion), `TestRelayTrackerNilIsSafe`,
  `TestRelayTrackerConcurrent` (under -race), `TestRelayCountersFileIsWrittenOncePerSecond`; with a live endpoint:
  `TestRelayTraceAsksThenSendsToAThirdParty`, `TestRelayTraceNotAfterTheWindow`, `TestRelayTraceSendDeclaringAVia`,
  `TestRelayTraceInboundWithAVerifiedVia`, `TestRelayTraceIgnoresUnverifiedSenders`, `TestRelayTraceNeverReadsTheBody`,
  `TestRelayCountersFileFromRun`.
* `cmd/mash/relay_status_test.go`: `TestStatusRelayTraceLine` (in-process, the shell with the counters file, no file, an
  unreadable file).
* `internal/config/config_test.go`: `TestRelayWindowSettings` (default, valid, invalid, out of range, env over file).

## Pros and Cons of the Options

### Content-free trace lines and counters

* Good, because it reads nothing mash does not already verify, and cannot break a send.
* Bad, because it only observes.

### A seam with allow, flag and hold

* Good, because a later rule would have a place to plug in.
* Bad, because a hold delays delegation before anyone knows what to hold, and the seam is unused code until then.

### A body-pattern detector

* Good, because it could see a relay the sender does not declare.
* Bad, because mash does not read payloads: it would be a content judgement, easy to evade and to trip by accident.

### Blocking

* Good, because it would stop a relay outright.
* Bad, because the signal is timing, so false positives are the common case and would break ordinary delegation.

## More Information

* Spec `docs/specs/2026-10-21-mash-design.md` §7.2; `docs/operations.md`, "Relay tracing"; `docs/install.md` (`relay_window`).
* Code: `internal/endpoint/relay.go`, `internal/config/config.go` (`relayWindow`), `cmd/mash/status.go` (`relayLine`).
* Related: [ADR 0020](0020-verified-mail-is-a-delegated-request.md), [ADR 0021](0021-message-bodies-are-shown-quoted.md),
  [ADR 0063](0063-in-reply-to-is-signed-and-a-sender-can-declare-a-relay-with-a-signed-mash-via-both-are-shown-only-when-they-verify.md).
