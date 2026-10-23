# Lease retry on startup (S5) Implementation Plan, revision 2

> **For agentic workers:** REQUIRED SUB-SKILL: superpowers:executing-plans. Steps use checkbox syntax.

**Goal:** a `mash serve` that starts while its endpoint's lease is held keeps retrying the take until it gets
it, then comes online with no restart of Claude.

**Revision 2** answers Codex's reject (10 findings, `.codex-review-lease-retry.md`, untracked): one MCP server
and one joined worker; tools registered once; serialized, readiness-ordered notices; cancellation is never
"held"; a defined terminal state; a narrowed guarantee; honest scope on MCP I/O leaks; real tests.

**Architecture:** `serve` makes one MCP server and one `Run`. One worker goroutine owns the whole endpoint
lifecycle (acquire, run, close) and is joined before `serve` returns. The five tools are registered once; each
handler loads an atomic state and uses the endpoint it finds.

**Tech stack:** Go, the existing `internal/testenv` in-process operator-mode NATS.

**Evidence:** rangehand `docs/spikes/s5-s6.md` (variant B: claude and `mash serve` killed together; the resumed
session stayed offline 3+ minutes past the 90 s lease because a startup lease conflict is terminal,
`cmd/mash/serve.go:53`); rangehand spec §15 (mash fix) and §18 M4. Scope approved by the operator: this fix.

## Global Constraints

- **Files allowed:** `cmd/mash/serve.go`, `cmd/mash/main_test.go`, `internal/testenv/testenv.go` (a connection
  count accessor), `docs/operations.md` (one troubleshooting line), and **one narrow change in
  `internal/endpoint/endpoint.go` `takeLease`** (finding 4): after `Create`/`Get`, if `ctx.Err() != nil`
  return it instead of a `LeaseHeldError`. No change to `store.LeaseTTL` (90 s), the protocol, `internal/mcp`,
  or lease refresh/loss behaviour.
- **Which errors retry:** only `*endpoint.LeaseHeldError` from the **first** `Open`. Any other first-`Open`
  error is handled exactly as today (tool-less error server, terminal), unchanged.
- **Retry rule:** `var leaseRetry = 10 * time.Second`; each attempt gets a fresh `Open` context bounded at 20 s
  (nats dial adds its own default 2 s timeout; `nats.Connect` takes no context, so shutdown during a dial can
  take up to that 2 s: accepted and tested with a 5 s allowance). Never gives up; ends on stdin close or
  `ctx` cancel.
- **Guarantee, narrowed (finding 6):** the waiter never takes a name whose holder is *continuously renewing*.
  If a holder loses its NATS connection and its lease expires, a waiter may take the name before the old holder
  notices; the old holder then stops, as it does today. Lease loss after a session has started stays terminal:
  it is never fed back into acquisition.
- **Out of scope (finding 7), not promised:** the existing `internal/mcp` behaviours: the readiness goroutine
  of `mcp.New` when stdin closes before `initialize`, a scanner blocked on stdin, and `Notify` blocking on a
  stuck stdout. This plan promises only: `serve` returns promptly on stdin close or cancel during the wait,
  the worker is joined, and the endpoint's NATS connection and lease refresher are closed. Tests assert those
  (connection count), not goroutine zero.
- **Terminal state after a retry hits a non-held error (finding 8):** stop retrying; store the error in the
  state; send one terminal notice (after readiness, through the same owner); every tool call returns that error
  ("mash endpoint <name> could not start: …"). The server keeps running so Claude Code sees a normal session.

## Design

State (an `atomic.Pointer[sessionState]`, replaced whole, never mutated):
`{ep *endpoint.Endpoint; waiting *endpoint.LeaseHeldError; failed error}`. The tools are built once from
immutable definitions; each handler does `st := state.Load()` once and: `st.ep != nil` → run the operation on
that endpoint; `st.waiting != nil` → return "mash is waiting for endpoint <name> (held by <host> pid <pid>
since <t>); mail is off"; `st.failed != nil` → that error.

Worker (one goroutine, joined): `acquire` loop → `Store(ep)` → wait `ChannelReady` → notices → existing
`StartService`/`Run` body → `ep.Close()` in a `defer`, unconditionally, **including when cancellation wins the
race with a successful `Open`** (finding 5: the worker, not the caller, owns every successful result).

Notices are sent only by the worker, in order, and only after `ChannelReady` (finding 3): if the lease was
still held when readiness arrives → one "held, retrying" notice; when acquired after having waited → one
"online, mail is on" notice (if acquisition happened before readiness, only the online notice is sent, after
readiness). A session that never waited sends no notice (ordinary startup is unchanged, finding 9).

First `Open` succeeds → state is pre-stored `ep` and the worker skips the loop: the same code path, no notices.

## Review Focus (each has a test)

- Acquisition completing **before** `notifications/initialized`: no early channel event; after initialize,
  notices arrive in order and queued mail is delivered.
- Cancellation (stdin close) during acquisition, during a dial, and immediately after a successful `Open`:
  `serve` returns, the worker is joined, no endpoint connection or lease is left.
- A live holder that keeps renewing across several refreshes is never displaced.
- Held, then a non-held error on retry, goes terminal as defined; tools return the error.
- A tool call during the wait returns "waiting", never panics; after acquisition, the same registered tool
  works on the new endpoint.

## Task 1: restructure `serve` and add the retry

**Files:** modify `cmd/mash/serve.go`, `internal/endpoint/endpoint.go` (the ctx check only),
`internal/testenv/testenv.go` (`func (e *Env) Connections() int`, from the server's client count); tests in
`cmd/mash/main_test.go`; one line in `docs/operations.md`.

**Interfaces:** consumes `endpoint.Open`, `*endpoint.LeaseHeldError`; produces `var leaseRetry`,
`var acquireAttempts atomic.Int64` (test-visible attempt counter), `type sessionState`.

- [ ] **Step 1: tests first.** Timing globals (`leaseRetry`, `endpoint.LeaseRefresh`) are restored only after
  the worker has exited (cleanup registered after the serve-join).
  - `TestServeRetriesHeldLease`: a stand-in holder key for `alpha`, created directly in the lease bucket;
    `leaseRetry=50ms`; `serve` over pipes; `initialize`; assert tools list has five tools (not zero), one
    "held, retrying" channel event naming holder host/pid/since and containing "already in use"; delete the key
    (what the TTL does); assert exactly one "online" event, then mail to `alpha` arrives and `send` works.
  - `TestServeAcquireBeforeInitialized`: lease freed **before** `notifications/initialized` is sent; assert
    no channel event before initialize; after it, one "online" event only, then queued mail delivered.
  - `TestServeDoesNotDisplaceRenewingHolder`: a real `endpoint.Open` holder with `LeaseRefresh=100ms`; the
    waiter makes at least 10 attempts (`acquireAttempts`) while the holder refreshes at least 5 times; the
    waiter never comes online; holder still serves mail. Then the holder closes and the waiter acquires.
  - `TestServeWaitingToolsAreSafe`: every tool returns the "waiting" result, no panic.
  - `TestServeCancelDuringWait`, `TestServeCancelDuringDial`, `TestServeCancelRightAfterOpen`: stdin closed at
    each point, with barriers (a hook between `Open` returning and publication); `serve` returns within 5 s,
    `Env.Connections()` returns to its baseline, and no lease key remains held by the instance.
  - `TestServeHeldThenNonHeldErrorIsTerminal`: revoke or unprovision `alpha` while it waits; assert retries
    stop (attempt count stops growing), one terminal notice, tools return the error.
  - `TestServeLeaseConflict` (existing) is **updated**: second session now sees the guarded tools, the waiting
    instructions/notice still contain "already in use", the first session keeps serving.
  - `TestServeOrdinaryStartupHasNoNotice`: a free name sends no channel event before mail.
  - Existing suite expectations for non-held startup errors unchanged.
- [ ] **Step 2: run, confirm FAIL.** `go test ./cmd/mash -run 'TestServe' -count=1`
- [ ] **Step 3: implement** per Design; the endpoint change is the `ctx.Err()` check in `takeLease` (and a unit
  test in `internal/endpoint` that a cancelled context yields the context error, not `LeaseHeldError`).
- [ ] **Step 4: whole suite.** `CGO_ENABLED=1 go test -race ./... -count=1`, then again with `-count=5` on
  `./cmd/mash` for flakes. Expected: PASS.
- [ ] **Step 5: Codex adversarial review** of the diff, re-running its tests; fix or answer every finding.
- [ ] **Step 6: commit.** `fix(serve): retry a held lease at startup until it is taken; no restart needed`

## Task 2: live check on the scratch NATS

- [ ] Rerun S5 variant B from rangehand `docs/spikes/s5-s6.md` (scratch operator-mode `nats-server`, two real
  claude sessions): kill claude and `mash serve` together, resume at once; expect `mash who` to show the lane
  online within about 100 s of the kill with no restart, and mail answered. Also run it with the holder still
  alive (kill nothing) and confirm the waiter stays out. Write the evidence to `docs/evidence/` and commit;
  report the SHA to the lead for `compat.json` (spec §15).
