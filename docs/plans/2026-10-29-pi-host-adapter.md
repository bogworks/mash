# Pi host adapter (design note)

Goal: a session driven by Pi (`@earendil-works/pi-coding-agent`, checked at 1.4.0) can be a mash endpoint with the
same delivery Claude Code gets from hooks: mail shown during a busy run without a turn of its own, an idle session
woken for waking mail, the same kinds, urgency, batching, at-least-once marking and ownership. This builds on the host
adapter interface from v0.2.1 (`internal/host`) and is the Pi half of issue #43's fast-follow. It is not #43's durable
receipt mode (peek plus explicit ack across restarts of the host); that stays a separate item.

## What Pi gives us

- **MCP tools work today.** Pi's built-in MCP extension spawns stdio servers as direct children
  (`extensions/mcp/runtime.js`, `pi-mcp` `StdioTransport`), so `mash serve` runs under Pi unchanged, and its parent is
  the Pi process, as it is the Claude process under Claude Code.
- **Pi drops mash's wake.** Its MCP client handles only `notifications/message` and `tools/list_changed`; the
  `notifications/claude/channel` that wakes Claude is never seen. A Pi session therefore must never get mail by
  channel, and serve must not ack mail on a channel write.
- **An extension gets every hook point mash needs** (`dist/core/extensions/types.d.ts`):

  | Claude Code | Pi extension equivalent |
  |---|---|
  | `UserPromptSubmit` hook, `additionalContext` | `before_agent_start` handler returning `{ message }` (a custom message added to that run) |
  | `PostToolUse` hook, `additionalContext` | `tool_result` handler returning extra `content` appended to the tool's result |
  | channel event wakes an idle session | `pi.sendMessage({customType, content, details}, { triggerTurn: true })` while `ctx.isIdle()` |
  | session lifecycle | `session_start` (startup, reload, new, resume, fork) and `session_shutdown` |
  | `.mcp.json` from the plugin | `pi.registerMcpServer("mash", { command: "mash", args: ["serve"], env })` |

  The extension, like a Claude hook, runs `mash hook` as a child process of the Pi process, so the ancestry ownership
  check Claude uses applies as is.

## Design

One extension file plus one Go adapter. Everything that decides what is delivered stays in mash's Go code; the
extension only decides when to ask and where Pi puts the answer.

### Go: `host.Pi` and one new adapter property

1. **`SpoolsFromStart() bool`** on `host.Adapter`. Claude returns false: delivery is the channel's until the first hook
   runs, as now. Pi returns true: serve spools from its first message and treats the session as hooked from start, so
   nothing is acked before a hook shows it. This is the only behavioural change to shared code, and it is off for
   Claude.
2. **Trigger** reads `{"event": "prompt" | "tool" | "wake"}` on stdin. Anything else is not ours (`ok=false`).
3. **OwnerToken / Owns**: the same `pid:<parent>` token and ancestry walk as Claude (shared, not copied).
4. **Present** writes one JSON object: `{"text": <the same framed batch Claude gets>, "seqs": [...], "urgent": bool}`.
   It uses the same batch rules (urgent first, `BatchMsgs`, `BatchBytes` digest) without Claude's 10,000-character
   cap; Pi has none, and the byte budget still bounds the text.
5. **Waker** cannot use the MCP notification. When serve wakes, it writes the summary line atomically to
   `<spool dir>/wake` (temp file and rename). The extension watches that file. Re-wake backoff, kinds and backlog
   wakes are serve's, unchanged.
6. `host.Select("pi")`; `MASH_HOST=pi` / `"host": "pi"`.

`mash hook` itself is unchanged: lock, owner check, touch, waiting, mark shown, print, mark drained. A Pi run that is
killed between print and drained shows the batch again, flagged as re-delivered, exactly as under Claude.

### Pi extension (`plugin/pi/mash.ts`)

- On load: `registerMcpServer("mash", { command: "mash", args: ["serve"], env: { MASH_HOST: "pi" } })`, unless the user
  already configures a `mash` server in `mcp.json` (Pi then gives theirs precedence; the extension warns if it lacks
  `MASH_HOST=pi`).
- `before_agent_start` with `messageType: "user"`: run `mash hook` with `{"event":"prompt"}`; if it printed a batch,
  return it as `{ message: { customType: "mash", content: text, display: true, details: { seqs, urgent } } }`.
- `tool_result`: run `mash hook` with `{"event":"tool"}`; if it printed a batch, return the result's content with the
  batch appended as one more text block.
- Wake: `fs.watch` on the spool directory for `wake`. On a change, if `ctx.isIdle()`, run `mash hook` with
  `{"event":"wake"}` and `sendMessage(..., { triggerTurn: true })`. If busy, do nothing: the next `tool_result` drains,
  and serve repeats the wake with backoff while waking mail is held.
- `session_start` (any reason) re-binds the watcher and context; `session_shutdown` closes the watcher. A new, resumed
  or forked session in the same Pi process keeps the same serve and spool owner; the mail goes to whichever session is
  current.
- The spool path comes from mash, not the extension: `mash hook` with `{"event":"init"}` prints
  `{"wake": "<path>"}` and drains nothing.
- Every `mash hook` call has a timeout (5 s) and its failures are swallowed with a one-line `ctx.ui.notify`; a broken
  mash never blocks a Pi run. Mail it did not show stays spooled.
- Shipped embedded and written out by `mash plugin export --host pi <dir>`; installed by adding that path to Pi's
  `extensions` setting (docs/install.md).

## Invariants (tested before the adapter code is merged)

1. With `MASH_HOST` unset or `claude`, behaviour and plugin output are byte-for-byte what v0.2.5 produces (existing
   tests, plus a test that Claude's `SpoolsFromStart` is false).
2. Under Pi, serve never acks a message a hook has not marked drained: none before the first hook, none on a wake, none
   on a channel write. A test sends mail to a Pi-mode serve with no hook ever run and asserts it is still unacked and
   spooled after the idle wake.
3. A wake is a hint, never a receipt: losing it, or the extension being busy, leaves the mail spooled and serve
   re-wakes with backoff.
4. Only the owning Pi process drains (ancestry, as Claude); a second Pi with the same `MASH_NAME` drains nothing.
5. Kinds, urgency, backlog wakes, batching and the re-delivered flag are the shared endpoint code: the Pi tests run the
   existing kind and crash matrices with the Pi adapter.
6. The extension never fails a Pi run: hook errors, timeouts and a missing binary are swallowed.

## Pull requests

1. This note.
2. `SpoolsFromStart` on the interface, serve spooling from start when set, invariant tests 1 to 3 (a test-only
   adapter until 3 lands).
3. `host.Pi` (Trigger, shared ownership, JSON Present, wake-file Waker, Select) and invariant tests 4 and 5.
4. The extension, `mash plugin export --host pi`, and its tests (Node's test runner against a fake `pi` API and a
   stub `mash`), plus release-check coverage that the exported file matches the embedded one.
5. Live proof with a real Pi session against a lab model: idle wake, busy-run drain on `tool_result`, prompt drain,
   urgent, fyi held, `/new` and resume keeping delivery, Pi restart replaying unshown mail. Evidence committed under
   `docs/evidence/`. Then install docs.

## Out of scope

- #43's durable receipt mode (non-destructive receive, explicit ack bound to a consumer generation): this design keeps
  mash's hook semantics, at-least-once into the model's context, the same guarantee Claude has.
- Hermes.
- Pi RPC mode and SDK embeddings: they load the same extension, but only the interactive CLI is proved here.
- Live prompts (SAP `reply`) work through the MCP tool as today; nothing new.

## Open questions

- Does Pi keep an idle stdio MCP server connected, or close and respawn it? A respawn would move the lease to a new
  serve (the G270 fast takeover covers the handover); the live proof checks this.
- `tool_result` content append versus a `steer` custom message: append is chosen because it is in context
  deterministically in the same run, as `PostToolUse` is; a steer queued at the run's end may wait for the next turn.
