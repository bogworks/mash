# Lease retry on startup (S5) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: superpowers:executing-plans. Steps use checkbox syntax.

**Goal:** a `mash serve` that starts while its endpoint's lease is held keeps retrying the take until it gets
it, then comes online with no restart of Claude.

**Architecture:** `serve` opens the MCP server at once; the endpoint is opened in a retry loop in the
background and published through an atomic pointer; the five tools guard on that pointer.

**Tech stack:** Go, the existing `internal/testenv` in-process operator-mode NATS.

**Spec / evidence:** rangehand `docs/spikes/s5-s6.md` (variant B: claude and `mash serve` killed together;
the resumed session stayed offline 3+ minutes past the 90 s lease because `cmd/mash/serve.go:53` is
terminal); rangehand spec §15 ("mash fix") and §18 M4. Operator-approved scope: this fix only.

## Global Constraints

- No change to `internal/endpoint`, `store.LeaseTTL` (90 s), the protocol, or any other behavior.
- Only `*endpoint.LeaseHeldError` retries. Any other `Open` error (NATS down, creds, unprovisioned) stays
  terminal, exactly as today.
- Retry interval 10 s (`var leaseRetry` in `cmd/mash`, overridable in tests); never gives up; it ends when
  stdin closes or the context is cancelled.

## Review Focus

- Stdin closes while waiting: exit 0, no goroutine or NATS connection left.
- The lease is taken on a retry while Claude has not yet signalled `ChannelReady`: the online notice must wait
  for it, and mail must still be delivered afterwards.
- A tool call during the wait returns a clear "waiting" error, not a nil-pointer panic.
- A live second session that never ends: the waiter never takes the name and never disturbs the holder.
- Cancellation mid-`Open` (20 s open timeout) is not treated as "held".

## Task 1: retry loop in `serve`

**Files:** modify `cmd/mash/serve.go`; test `cmd/mash/main_test.go`; one line in `docs/operations.md`
troubleshooting row "name already in use".

**Interfaces:** consumes `endpoint.Open(ctx, cfg) (*Endpoint, error)` and `*endpoint.LeaseHeldError`;
produces `var leaseRetry = 10 * time.Second` and an unexported `epRef atomic.Pointer[endpoint.Endpoint]`.

- [ ] **Step 1: tests (fail first).**
  - `TestServeRetriesHeldLease`: create the `alpha` lease key directly in the lease bucket (another instance,
    never refreshed); `leaseRetry = 50ms`; run `serve` over pipes. Assert: `initialize` is answered; exactly one
    channel notice names the holder (host, pid, since) and says it is retrying; then delete the key (what the
    TTL does); assert one "online" notice, then that mail sent to `alpha` arrives as a channel event and the
    `send` tool succeeds. Expected before the change: FAIL (terminal "could not start", no retry).
  - `TestServeWaitingToolsAreSafe`: while held, `tools/call` for each of the five tools returns an error result
    containing "waiting for endpoint alpha", no panic.
  - `TestServeStdinCloseWhileWaiting`: close stdin during the wait; `serve` returns 0 promptly; no goroutine
    leak (`runtime.NumGoroutine` settles) and the NATS server shows no lingering connection.
  - `TestServeOtherOpenErrorsStayTerminal`: unprovisioned name still yields the terminal notice, no retry.
- [ ] **Step 2: run, confirm FAIL.** `go test ./cmd/mash -run 'TestServe(Retries|Waiting|StdinClose|Other)' -count=1`
- [ ] **Step 3: implement.** In `serve`: on `Open` error, if `errors.As(err, &held)` start the MCP server with the
  normal `Instructions` plus one "waiting for the name" line and the five tools wrapped by the pointer guard,
  send the one-time held notice (after `ChannelReady`), loop `Open` every `leaseRetry` (a fresh 20 s open
  context each try) until it succeeds or `ctx` ends, then store the endpoint pointer, send the online notice,
  and fall through to the existing `StartService`/`Run` path. Otherwise unchanged.
- [ ] **Step 4: run the whole suite.** `go test -race ./... -count=1` (needs `CGO_ENABLED=1`). Expected: PASS.
- [ ] **Step 5: commit.** `fix(serve): retry a held lease at startup until it is taken; no restart needed`

## Task 2: live check on the scratch NATS

- [ ] Rerun S5 variant B from `docs/spikes/s5-s6.md` (scratch operator-mode `nats-server`, two claude
  sessions): kill claude and `mash serve` together, resume at once; expect `mash who` to show the lane online
  within about 100 s of the kill with no restart, and mail to be answered. Append the result and timings to
  `docs/evidence/` (the mash repo's convention) and commit. Report the commit SHA to the lead for pinning in
  rangehand's `compat.json` (spec §15).

## Review

Codex adversarial review of the diff before the lead is told (rangehand brief, "How you work" 4).
