---
status: accepted
date: 2026-10-21
decision-makers: Randy Bias (mash maintainer)
recorded: 2026-10-31 (backfilled)
---

# mash implements its own minimal MCP server, no SDK, and negotiates only revisions it supports

## Context and Problem Statement

`mash serve` talks to Claude Code over the Model Context Protocol (MCP), JSON-RPC messages over stdio by which an AI
host calls a tool server's tools. mash needs only a small part of it: `initialize` (declaring Claude Code's
experimental `claude/channel` capability, which lets the server push events into the session), tool listing and
calls, `ping`, and the `notifications/claude/channel` event (`internal/mcp/server.go`). mash had to choose between an
MCP library and its own code, and, later, which protocol revisions to accept.

## Decision Drivers

* Spec goal 6: "Minimal new code. One Go binary, plus conventions."
* The channel capability is Claude Code's own extension, outside the core protocol.
* A protocol revision that silently disables the channel must never be negotiated.

## Considered Options (reconstructed 2026-10-31; not recorded at the time)

* Its own minimal MCP server on the Go standard library.
* An MCP SDK for Go.
* For revisions: echo whatever revision the client asks for, or answer only from a fixed list mash supports.

## Decision Outcome

Chosen option: "its own minimal server, answering only revisions it supports". The v0.1 plan states "The standard
library for everything else; no MCP SDK" (`docs/plans/2026-10-22-mash-v0.1.md`), but no source records why an SDK
was rejected. Reconstructed: the server needs four things, and goal 6 asks for minimal code.

The revision pin was added on 2026-10-30 (PR #112) because Claude Code's documentation says "a channel server that
negotiates MCP protocol revision 2026-07-28 can't deliver channel messages, so Claude Code doesn't register it as a
channel". mash had echoed the client's requested revision, so a client asking for 2026-07-28 would have lost its
channel wake without any error.

As built (mash layer):

* `go.mod` has no MCP module.
* `initialize` answers with the client's revision only if it is 2024-11-05, 2025-03-26 or 2025-06-18; for anything
  else it answers 2025-06-18. `server/discover`, the newer handshake's first call, gets "method not found"
  (-32601), so Claude Code falls back to the older handshake.
* serve sends channel events only after Claude Code has registered the channel: after its first `tools/list` plus
  500 ms, or after 3 seconds without one, because an event sent earlier was lost (commit `3d27f58`, 2026-10-21). The
  values 500 ms and 3 seconds were chosen without a recorded reason.

### Consequences

* Good, because the server is small and has no third-party dependency to track.
* Good, because a client asking for a newer revision is answered with one mash supports, so mash's own answer never
  disables the channel.
* Bad, because mash stays on the older ("legacy") MCP protocol era and must follow Claude Code's changes by hand.
* Neutral, because on Claude Code 2.1.314 the revision bug could not be reached: the -32601 answer already kept mash
  on the older handshake (PR #112 live check).

### Confirmation

* `internal/mcp/server_test.go`: `TestInitializeNegotiatesSupportedVersion`, `TestServerDiscoverIsNotSupported`,
  `TestInitializeEchoesVersionAndChannelCapability`, `TestChannelReadyWaitsForToolsList`, `TestChannelReadyFallback`,
  `TestToolsListAndCall`, `TestPing`, `TestUnknownMethod`.
* No automated test checks that `go.mod` stays free of an MCP module.

## Pros and Cons of the Options

### Own minimal server

* Good, because it is one file of about 330 lines that mash controls completely.
* Bad, because every protocol change is mash's to notice and handle.

### An MCP SDK

* Good, because protocol updates would arrive with the library.
* Bad, because an SDK update could change the negotiated revision and drop the channel, the failure PR #112 guards.

### Echo the client's revision

* Good, because it is simplest.
* Bad, because a revision that disables channels would be accepted silently.

## More Information

* Spec `docs/specs/2026-10-21-mash-design.md` §1.1 (goal 6), §7; `docs/plans/2026-10-22-mash-v0.1.md` (tech stack).
* PR #112 (revision pin, 2026-10-30); commit `3d27f58` (wait for channel registration).
* Code: `internal/mcp/server.go` (`supportedVersions`, `negotiateVersion`, `ChannelReady`).
* Related: ADR-0022 (the endpoint manager), ADR-0024 (what the channel still carries).
