# mash

mash is a messaging mesh for agents. It gives every Claude Code session its own durable, signed mailbox on a NATS server, and delivers mail straight into the session through Claude Code channels, so no turn is wasted polling an inbox.

It is the [Synadia Agent Protocol](https://github.com/synadia-ai/synadia-agent-sdk-docs) (SAP 0.3) plus one extra verb, `mail`, and a durability layer, JetStream. mash invents no protocol of its own: every mash session is an ordinary SAP agent that any SAP tool can discover, ping and prompt.

## What you get

- **Mail that waits.** Each endpoint has its own JetStream inbox stream. Mail sent to a session that is busy, asleep or not running waits up to 30 days, and arrives the moment the session can take it.
- **No polling turns.** An idle session is woken by the mail itself. Mail that lands mid-turn rides along with the next tool result. A burst arrives as one turn. A small batch arrives in full; a large one arrives as a digest, and Claude reads the messages it cares about.
- **Fast replies.** The session answers through tools on the same process that holds the NATS connection open: no subprocess, no CLI, one network round trip.
- **Verified senders.** Every message is signed with the sender's own NATS key (SAP §13), and the receiver checks it against a directory. A forged message is dropped; an unproven one is labelled as such.
- **Unique endpoints.** One NATS user per session name, and a lease that refuses a second live session under the same name.
- **Live prompts too.** Because each session is a SAP agent, you can `mash prompt` it (or use any SAP client) and get a streamed answer.

## How it works

```
 Claude Code session                       NATS server (operator mode, JetStream)
 ┌──────────────────────┐                 ┌──────────────────────────────────────┐
 │ claude --channels     │  stdio (MCP)    │ account MASH                          │
 │   plugin:mash@mash    │◀──────────────▶ │  inbox-<name>   per-endpoint stream   │
 │                       │  mash serve ────┼──▶ agents.mail.cc.<owner>.<name>      │
 │  tools: send, read,   │   one TLS conn  │  mash-directory KV  name → identity   │
 │  inbox, who, reply    │                 │  mash-leases KV     one live session  │
 └──────────────────────┘                 │  $SRV.* / agents.hb.*  SAP presence   │
                                           └──────────────────────────────────────┘
```

The design, with every subject, permission and limit, is in [docs/specs/2026-10-21-mash-design.md](docs/specs/2026-10-21-mash-design.md).

## Install

You need a NATS server in operator mode with JetStream, an account for mash, and Claude Code with channels (claude.ai or Console authentication). [docs/install.md](docs/install.md) covers it end to end. The short version:

```sh
go install github.com/randybias/mash/cmd/mash@v0.1.0      # a tagged release; `mash version` prints the tag
claude plugin marketplace add randybias/mash
claude plugin install mash@mash
# allowlist the channel once per machine (managed settings), then:
MASH_NAME=my-session claude --channels plugin:mash@mash
```

Operators provision endpoints with `mash admin` and their usual NATS tooling: see [docs/operations.md](docs/operations.md).

## Status

v0.1: the spec, a Go implementation with an integration suite that runs against an in-process NATS server carrying the production permissions, and a live end-to-end run on a real deployment. Later phases (an archive leaf node, monitoring, approvals) are listed in the spec.

## Releases

Releases are signed tags built reproducibly from fresh clones: see [docs/release.md](docs/release.md).

## License

Apache-2.0. See [LICENSE](LICENSE).
