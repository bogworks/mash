---
status: proposed
date: 2026-11-16
decision-makers: Randy Bias (mash maintainer)
---

# An endpoint can run with no agent host (MASH_HOST=headless)

## Context and Problem Statement

[ADR 0032](0032-host-adapter-seam.md) put the agent host behind an `Adapter`, and
[ADR 0137](0137-the-host-adapter-declares-its-session-id-sources-wake-and-words-claude-code-is-still-the-only-host.md) moved
the host's names behind it. `mash serve` is still an MCP server for an agent: it answers `initialize`, it waits for the
client's first `tools/list` before it sends anything, it pings the client, and it ends when the client closes its input.
An endpoint therefore cannot be online unless an agent host is running it.

That is more than mash needs. mash works standalone: a mailbox on NATS that is read with `mash inbox` and `mash read`
and written with `mash send`. A test, a script or a service that only needs to receive and keep mail must today start a
host it does not use, or fake the MCP client.

## Decision Drivers

* mash must work with no agent host at all; the host is an option, not a requirement.
* A test or a script must be able to keep an endpoint online.
* Claude Code's behavior, every printed string and every stored record must not change.
* The smallest change to serve that cannot leave something waiting for a client that never comes.

## Considered Options

* A separate command that keeps an endpoint online.
* A host named `headless`, with serve run unchanged except that it has no MCP client (chosen).
* Leave it: a script supplies a pipe that never closes and never speaks.

## Decision Outcome

Chosen: a host named `headless`, chosen by `MASH_HOST=headless` and never detected. `Select("")` is still Claude Code.

What it does:

1. **Keeps the endpoint online.** `MASH_HOST=headless mash serve` takes the endpoint's lease, registers the endpoint so
   `mash who` shows it online, receives its mail and refreshes the lease, until it gets SIGINT, SIGTERM or SIGHUP. It
   then stops, releases the lease and exits 0. It writes one line to stderr when it starts and one when the endpoint is
   online.
2. **Has no MCP client.** serve gives its MCP server an input that ends only when serve is stopped and an output that is
   discarded. Standard input and output are not read or written for MCP, so a closed or empty stdin does not end it. The
   one thing in serve that waits on the client, the gate that holds notices until the client's `tools/list`, is opened
   at once, and the client ping is not run. A lost lease, or a failure that would otherwise leave an inactive server
   running, ends serve with exit code 1 and its reason on stderr, because there is no client to be told. That includes a failure to register the endpoint's service.
3. **Spools and waits.** Its wake is none, so `SpoolsFromStart` is true: mail is spooled from the first message and held
   unacked in the inbox, and nothing drains it. There is no wake to send, so none is counted and the session is never
   judged to have no hooks. `mash inbox` and `mash read` show the mail; they never take the lease, so they run alongside
   serve.
4. **Names itself.** Its words are `mash serve (headless)`, `serve process` and the service prefix `mash (headless): `.
   It declares no session id source, so a session variable in the environment it was started from is not read as its
   session. Its lease records no session identity (no parent pid either), so a headless serve is never the same session
   as another serve and never takes a live lease; only a dead or expired holder is taken over. Its spool owner record is
   `{"host":"headless","pid":N}` (serve's own pid, no session, no launch token), and Claude Code's hook refuses any
   owner record with a host field, so no hook drains it. `mash status` run with `MASH_HOST=headless` does not run Claude Code's settings checks, and neither does
   serve's start-up report.

What it does not do:

* It cannot answer a live prompt. `mash prompt` to it is answered at once with a line that says so, rather than left to
  time out.
* It never wakes anything and runs no hook. Nothing is shown to an agent.
* It does not detect that it should be used; an agent host's own start keeps doing what it did.
* It adds no installer, no process supervisor and no new command.

This amends ADR 0032 (a second adapter, which has no hook and no wake). It does not edit it.

### Consequences

* Good, because mash is usable, testable and scriptable with no agent host.
* Good, because serve keeps one code path, so what the headless endpoint does on the wire is what any endpoint does.
* Good, because the end-to-end documentation can be run by anyone with `nats-server` on their path.
* Bad, because serve now knows one host by type (`host.IsHeadless`), not only through the adapter, until the MCP stream
  is an option of the endpoint instead of a property of serve.
* Bad, because mail that nothing reads waits in the inbox until the stream's limits drop it (30 days or 16 MiB); a
  headless endpoint with no reader loses its oldest mail like any other.

### Confirmation

Unit tests pin the adapter's declarations and `Select`. An in-process serve test, against the embedded NATS, shows that
headless serve stays up with an empty and with a closed stdin, shows online, holds mail that `inbox` and `read` return,
exits 0 on cancel and releases the lease, and that serve with no identity exits 1. A status test shows no Claude Code
settings lines. The existing tests pass with no expected string edited.

## Pros and Cons of the Options

### A separate command

* Good, because serve is untouched.
* Bad, because it repeats the lease, spool and service steps, and the two drift.

### A host named `headless` (chosen)

* Good, because it is the seam ADR 0032 made, and it changes nothing for an existing host.
* Bad, because serve has a few places that say "no client".

### A pipe that never closes

* Good, because it needs no change.
* Bad, because every caller invents it, the gate that waits for `tools/list` is never opened, and the endpoint never
  receives mail by the route a host would.

## More Information

Amends [ADR 0032](0032-host-adapter-seam.md). Builds on
[ADR 0137](0137-the-host-adapter-declares-its-session-id-sources-wake-and-words-claude-code-is-still-the-only-host.md).
