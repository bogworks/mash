---
status: proposed
date: 2026-11-16
decision-makers: Randy Bias (mash maintainer)
---

# Codex is a supported host; the host is detected, never silently assumed, and an idle Codex is woken by a command

## Context and Problem Statement

[ADR 0137](0137-the-host-adapter-declares-its-session-id-sources-wake-and-words-claude-code-is-still-the-only-host.md)
put what differs between hosts behind `host.Adapter`, and [ADR 0140](0140-pi-is-a-supported-host-chosen-with-mash-host-and-served-by-an-extension.md)
added Pi, chosen only by `MASH_HOST`. The Codex CLI is the closest host to Claude Code: it runs hooks with Claude's
contract (the same stdin fields, the same `additionalContext` output), so `mash hook` delivers mail into a Codex session
unchanged. Three things are missing. Codex ignores the MCP channel notification, so an idle session is never woken.
It puts no session id in serve's environment (its MCP environment is filtered); the id is the hook's `session_id` and the
`_meta.threadId` on each `tools/call`. And with one shared Codex app-server, every thread's `mash serve` and every
hook descend from the app-server, so process ancestry ([ADR 0040](0040-hook-ownership-session-id-launch-token-then-process-chain.md)
rule 3) cannot tell two threads apart.

There is also a standing defect in how the host is chosen: `MASH_HOST` unset reads as Claude Code, silently. With three
hosts that is a wrong answer given with no sign of it.

## Decision Drivers

* Claude Code's behavior and every string it prints stay exactly as they are.
* One thread's hook must never drain another thread's mail.
* A wake must never turn a sender's text into a user turn.
* The host is never guessed without saying so.
* The hook and serve must agree on the host, though the hook cannot see the MCP client.

## Considered Options

* Name Codex only with `MASH_HOST=codex`, as for Pi.
* Detect the host, in a fixed order, and say so when nothing identifies it (chosen).
* Keep process ancestry for Codex and accept the shared app-server case.

## Decision Outcome

Chosen: a `Codex` adapter, a detection order with no silent default, a command wake, and no process-ancestry rule for
Codex.

1. **The adapter.** `Codex` reuses Claude's `Trigger`, `Present` and `Notice` (the contract is the same; a test pins
   them equal). Its session-id sources are no environment variable, the hook field `session_id`, and ancestry not
   unique. Its wake is `WakeCommand`, so mail is spooled from the first message. Its words are "Codex", "Codex process"
   and "Codex (mash): ", and it has none of Claude Code's settings checks.
2. **Ownership.** Claude's rules, without rule 3 when `SessionSources().AncestryUnique` is false: with a shared
   app-server, serve's parent being an ancestor of the hook proves nothing about the thread, so rule 3 could let another
   thread's hook drain this thread's mail. The session-id and launch-token rules, and the other-launch refusal, apply as
   before. serve learns the thread id from the first `tools/call` and rewrites the spool's owner record with it, so
   rule 1 can match; a launch token (`MASH_LAUNCH_TOKEN` passed through Codex's `env_vars`) identifies the session from
   the first prompt. Without one, a hook is not recognised until the first mash tool call; mail stays spooled.
3. **Detection.** The host is, in order: (a) `MASH_HOST`; (b) an environment marker, `PI_CODING_AGENT` for Pi, then
   `CLAUDE_CODE_SESSION_ID` for Claude Code (the one Claude variable mash already reads); (c) in `mash serve` only,
   the MCP `initialize` client name: `codex-mcp-client` is Codex, a name starting with `claude` is Claude Code, `pi`
   is Pi; (d) otherwise Claude Code, **flagged as assumed**. The assumption is never silent: serve writes one stderr
   line saying what was missing and to set `MASH_HOST`, and `mash status` shows it. An unknown `MASH_HOST` is still
   an error. `headless` is never detected.
4. **Status.** `mash status` gains a `host: <name> (<how chosen>)` line after the name line, for every host. Status
   is a report, and [ADR 0034](0034-text-output-and-plugin-commands-are-a-compatibility-contract.md) pins the render
   fixture, the plugin's commands and the default lines of `mash who`, not status; so the line is allowed, and no
   fixture changes. It is shown only where a host was detected (the `mash` binary's `status` and the MCP `status` tool).
5. **Late resolution in serve.** The client's name is known after `initialize`, so serve resolves a still-pending host
   when the channel-ready gate opens and uses the result for everything that depends on the host: whether mail is
   spooled from the start, the owner record, the wake, the service description and the words in notices and status.
   What serve does earlier is correct for either answer: the session-environment variable it reads
   (`CLAUDE_CODE_SESSION_ID`) is unset under Codex and Pi, so the lease's session id is empty both ways, and Claude's
   settings checks are held back until the host is settled, so they never warn about a host that is not Claude Code. A
   Claude Code session identified by its environment settles at once and prints what it always printed.
6. **The hook follows serve.** The hook cannot see the client's name. serve records the host in the owner record only
   for Codex (the field is omitted otherwise, so Claude's and Pi's records are byte for byte unchanged), and a hook that
   was not told the host by `MASH_HOST` reads it from there, so both apply the same rules and words. A hook matches a
   record only when the record's host is empty or its own adapter's name: a Claude Code hook is refused on a Codex record
   as on a headless one ([ADR 0139](0139-an-endpoint-can-run-with-no-agent-host-mash-host-headless.md)), and a Codex hook
   is refused on a headless one.
7. **The wake.** `codex queue --thread <thread id> --message <text>`, run as an argument list with no shell and a
   bounded timeout. The thread id is the last `_meta.threadId` the MCP server saw, checked against a conservative
   pattern before use; with none yet, nothing is run and the endpoint retries. The text is built by mash from the
   count of waiting messages and a fixed sentence, never from the endpoint's wake line, which names senders. The command
   needs Codex's home: Codex gives an MCP server only a short environment, so the configuration lists
   `env_vars = ["MASH_LAUNCH_TOKEN", "CODEX_HOME"]`, and when serve has no `CODEX_HOME` the hook, which runs in Codex's
   environment, records its value (an absolute existing directory, nothing else) in the spool for the wake to use. A
   failure (the command does not start, or exits non-zero) is written to stderr once per distinct failure, is shown
   by `mash status` as `delivery: wake FAILED at <time>: <reason>` until a wake succeeds, and is not fatal. `MASH_CODEX_BIN` and `MASH_CODEX_REMOTE` name the binary and the
   app-server address.
8. **The seam.** `WakerFor(adapter, WakeContext, notify)` gives an adapter that implements `ContextWaker` the thread
   id, the environment and stderr; every other adapter gets its `Waker(notify)` as before.

Not decided here, and later work:

* An installer for Codex's configuration and hook trust. Trust needs the user's own approval; mash will not bypass it.
* The plugin's MCP entry under Codex (`${CLAUDE_PLUGIN_ROOT}` is not expanded by Codex).
* An intake of Codex's activity into mash.
* Live prompts (`mash prompt`) to a Codex endpoint.
* Same-session takeover of the endpoint lease ([ADR 0092](0092-a-new-serve-of-the-same-session-takes-over-the-endpoint-lease.md))
  is off for Codex, as under headless: threads of one shared app-server share a parent process, so ancestry cannot tell
  one thread's serve from another's. A restarted serve of the same thread waits for the old lease to be released or to
  expire, until the thread id can identify the lease holder. A serve whose host is still only assumed opens its first
  lease without identity and gets it back when the client's name settles the host to one whose ancestry is unique.

This amends [ADR 0032](0032-host-adapter-seam.md) (a third host and an optional wake interface),
[ADR 0040](0040-hook-ownership-session-id-launch-token-then-process-chain.md) (rule 3 applies only where ancestry is
unique per session) and [ADR 0137](0137-the-host-adapter-declares-its-session-id-sources-wake-and-words-claude-code-is-still-the-only-host.md)
(the host is detected, and `Select` is only the by-name step). They are not edited.

### Consequences

* Good, because a Codex session receives labelled mail, an idle one is woken, and Claude Code is unchanged.
* Good, because a host that nothing identified is said to be assumed, in stderr and in status.
* Good, because one thread's hook cannot drain another's mail through a shared app-server.
* Bad, because without a launch token a fresh Codex session receives nothing until its first mash tool call.
* Bad, because a Claude Code session with no `CLAUDE_CODE_SESSION_ID` and an unrecognised client name now prints the
  assumed line.
* Bad, because a status line is added for every host, so a script that compares whole status output sees it.

### Confirmation

Every existing test passes; the one expected string that changed is the list of host names `Known` returns, which now
includes `codex`. New tests cover the adapter's declarations; `Trigger`, `Present` and `Notice` equal to Claude's;
rule 3 refused under Codex and accepted under Claude for the same input, with the session-id and launch-token rules
intact; the detection table (every order step, each client name, and none, which is assumed); the owner record's host
and the hook following it; the waker's argument list through a fake `codex` that records it (no mail text, no thread id
means no run, a bad thread id, failure logged once, timeout); the thread id kept by the MCP server and written to the
owner record; a serve run with a Codex client that wakes with the thread id and says nothing extra; the assumed line;
and Claude Code recognised by name or by environment with no extra line.

## Pros and Cons of the Options

### Name Codex only with `MASH_HOST`

* Good, because there is no detection to get wrong.
* Bad, because the silent Claude default stays, and a user who forgets the setting gets Claude's words and rules with no
  sign of it.

### Detect, and say when assumed (chosen)

* Good, because the common case needs no setting and the uncommon one is visible.
* Bad, because the client name is only known to serve, so the hook needs the owner record to agree with it.

### Keep process ancestry for Codex

* Good, because a single TUI would need no launch token.
* Bad, because under a shared app-server it accepts any thread's hook, which can drain another thread's mail.

## More Information

Amends [ADR 0032](0032-host-adapter-seam.md), [ADR 0040](0040-hook-ownership-session-id-launch-token-then-process-chain.md)
and [ADR 0137](0137-the-host-adapter-declares-its-session-id-sources-wake-and-words-claude-code-is-still-the-only-host.md).
Install page: [docs/hosts/codex.md](../hosts/codex.md).
