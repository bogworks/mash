# Lease retry on startup (S5) Implementation Plan, revision 3

> **For agentic workers:** REQUIRED SUB-SKILL: superpowers:executing-plans. Steps use checkbox syntax.

**Goal:** a `mash serve` that starts while its endpoint's lease is held keeps retrying the take until it gets
it, then comes online with no restart of Claude.

**Revision 3** answers Codex's second review (4 new findings, 4 partly-closed) and the lead's rulings; approved on
commit, no further review round. **Revision 2** answered Codex's reject (10 findings, `.codex-review-lease-retry.md`, untracked): one MCP server
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
  count accessor), `cmd/mash/main.go` (`mash version` prints the commit), `docs/operations.md` (one troubleshooting line), and **one narrow change in
  `internal/endpoint/endpoint.go` `takeLease`** (approved by the lead, confined to the held-error branch):
  immediately before `return held`, `if ctx.Err() != nil { return ctx.Err() }`. **No** unconditional
  post-success cancellation return (`Open` closes the connection only on failure, so that could strand a
  created lease). Its test goes in `internal/endpoint/endpoint_test.go` (in scope). No change to `store.LeaseTTL` (90 s), the protocol, `internal/mcp`,
  or lease refresh/loss behaviour.
- **Which errors retry:** only `*endpoint.LeaseHeldError` from the **first** `Open`. Any other first-`Open`
  error is handled exactly as today (tool-less error server, terminal), unchanged.
- **Retry rule:** `var leaseRetry = 10 * time.Second`; each attempt gets a fresh `Open` context bounded by
  `var openTimeout = 20 * time.Second`. `nats.Connect` takes no context and its DNS lookup, connection init
  and server-pool attempts are not bounded by it, so **no connect-time bound is claimed**: the worker waits for
  an in-flight attempt to finish and closes its result. Never gives up; ends on stdin close or `ctx` cancel.
- **First `Open` (finding A):** it stays before the MCP server exists, exactly as today, so it is bounded only
  by `openTimeout` and stdin EOF during it is noticed after it returns (existing behaviour, unchanged). A test
  forces an unresponsive first `Open` with a short `openTimeout` and asserts `serve` then exits on EOF.
  Everything after the first `Open` (retries) is cancellable by stdin EOF and parent cancellation.
- **Guarantee, narrowed (finding 6):** the waiter never takes a name whose holder is *continuously renewing*.
  If a holder loses its NATS connection and its lease expires, a waiter may take the name before the old holder
  notices; the old holder then stops, as it does today. Lease loss after a session has started stays terminal:
  it is never fed back into acquisition.
- **Out of scope (finding 7), not promised:** the existing `internal/mcp` behaviours: the readiness goroutine
  of `mcp.New` when stdin closes before `initialize`, a scanner blocked on stdin, and `Notify` blocking on a
  stuck stdout. This plan promises only, **when stdout is writable**: `serve` returns promptly on stdin close or cancel
  during the wait (a worker blocked in `Notify` on a stuck stdout cannot reach its cleanup; `internal/mcp` has
  no cancellation path and is not changed), the worker is joined, and the endpoint's NATS connection and
  lease refresher are closed. Tests assert those
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

Worker (one goroutine, joined): a `select` loop over {`ChannelReady` once, the retry timer, the result of an
in-flight attempt (each attempt runs in a sub-goroutine the worker always receives from and, on success,
owns), `ctx.Done`}, so readiness is observed *while* acquisition continues and the held notice can be sent
while waiting; on acquisition → `Store(ep)` → (if not yet ready, wait `ChannelReady`) → online notice → existing
`StartService`/`Run` body → `ep.Close()` in a `defer`, unconditionally, **including when cancellation wins the
race with a successful `Open`** (finding 5: the worker, not the caller, owns every successful result).

Notices are sent only by the worker, in order, and only after `ChannelReady` (finding 3): if the lease was
still held when readiness arrives → one "held, retrying" notice; when acquired after having waited → one
"online, mail is on" notice (if acquisition happened before readiness, only the online notice is sent, after
readiness). A session that never waited sends no notice (ordinary startup is unchanged, finding 9).

**Waiting instructions (finding C):** `endpoint.Instructions(name, owner, policy)` (the normal text, including
`policy.md`) plus one temporary line "mail is off until the name is free; mash is retrying", so a recovered
session has the full operating guidance. Tested with a policy sentinel in `policy.md`.

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

- [ ] **Step 1: tests first.** Timing globals (`leaseRetry`, `openTimeout`, `endpoint.LeaseRefresh`) are
  saved and their restoration is **registered before any session starts**, so (cleanups run in reverse
  registration order) it runs after every serve/holder/waiter has been cancelled and joined (finding B).
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
  - `TestServeCancelDuringWait`, `TestServeCancelDuringAttempt`, `TestServeCancelRightAfterOpen`: stdin closed,
    and separately the parent context cancelled, at each point, with barriers (a hook between `Open` returning and publication); `serve` returns within 5 s,
    `Env.Connections()` returns to its baseline, and no lease key remains held by the instance.
  - `TestServeWaitingInstructionsKeepPolicy`: the initialize instructions contain the `policy.md` sentinel and
    the waiting line.
  - `TestServeFirstOpenUnresponsiveThenEOF` (finding A).
  - `TestServeHeldThenNonHeldErrorIsTerminal`: revoke or unprovision `alpha` while it waits; assert retries
    stop (attempt count stops growing), one terminal notice, tools return the error.
  - `TestServeLeaseConflict` (existing) is **updated**: second session now sees the guarded tools, the waiting
    instructions/notice still contain "already in use", the first session keeps serving.
  - `TestServeOrdinaryStartupHasNoNotice`: a free name sends no channel event before mail.
  - Existing suite expectations for non-held startup errors unchanged.
- [ ] **Step 2: run, confirm FAIL.** `go test ./cmd/mash -run 'TestServe' -count=1`
- [ ] **Step 3: implement** per Design; the endpoint change is the `ctx.Err()` check in `takeLease` (and the endpoint
  regression test, in package `endpoint`: wrap the JetStream/KV so `Create` returns `ErrKeyExists`, the wrapper's
  `Get` cancels the context and fails; assert `takeLease` returns the context error and **not** a
  `LeaseHeldError`. It fails on the current code at exactly that branch).
- [ ] **Step 4: whole suite.** `CGO_ENABLED=1 go test -race ./... -count=1`, then again with `-count=5` on
  `./cmd/mash` for flakes. Expected: PASS.
- [ ] **Step 5: Codex adversarial review** of the diff, re-running its tests; fix or answer every finding.
- [ ] **Step 6: commit.** `fix(serve): retry a held lease at startup until it is taken; no restart needed`

## Task 1b: `mash version` prints the commit

**Files:** `cmd/mash/main.go`, `main_test.go`. `mash version` prints `mash 0.1.0 (commit <rev>)`, where `<rev>`
is `main.commit` from `-ldflags "-X main.commit=…"` when set, else `debug.ReadBuildInfo` `vcs.revision`
(`+dirty` if `vcs.modified`), else `unknown`; so rangehand's `compat.json` can pin and check the S5-fixed commit.
- [ ] Test (function takes the build info as an argument): ldflags wins; vcs.revision used; modified marks
  `+dirty`; none → `unknown`; the existing `version` assertions are updated. Commit separately.

## Task 2: live check on the scratch NATS

- [ ] Rerun S5 variant B from rangehand `docs/spikes/s5-s6.md` (scratch operator-mode `nats-server`, two real
  claude sessions): kill claude and `mash serve` together, resume at once; expect `mash who` to show the lane
  online within about 100 s of the kill with no restart, and mail answered. Also run it with the holder still
  alive (kill nothing) and confirm the waiter stays out. Write the evidence to `docs/evidence/` and commit;
  report the SHA to the lead for `compat.json` (spec §15).
