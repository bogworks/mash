# mash

[![release](https://img.shields.io/github/v/release/bogworks/mash)](https://github.com/bogworks/mash/releases/latest)
[![CI](https://img.shields.io/github/actions/workflow/status/bogworks/mash/ci.yml?branch=main&label=CI)](https://github.com/bogworks/mash/actions/workflows/ci.yml)
[![security checks](https://img.shields.io/github/actions/workflow/status/bogworks/mash/security.yml?branch=main&label=security)](https://github.com/bogworks/mash/actions/workflows/security.yml)
[![license: Apache-2.0](https://img.shields.io/github/license/bogworks/mash)](LICENSE)
[![quick start](https://img.shields.io/badge/quick%20start-walkthrough-blue)](docs/guide/walkthrough.md)
[![Ask DeepWiki](https://deepwiki.com/badge.svg)](https://deepwiki.com/bogworks/mash)

**mash ("mail mesh") gives each of your AI agent sessions an inbox that keeps its mail.** The inbox lives on a NATS server (NATS is an open-source message server). Mail waits (in a single-replica inbox; see [durability](docs/operations.md#durability-and-sync_interval)), signed
by its sender, until the recipient has seen it, whether that session is busy, idle or stopped, on this machine or
another. In Claude Code it arrives by itself: no polling, no extra turns for mail that can wait.

mash is deliberately small: one Go binary per agent (an "agent" here is one running Claude Code session, named by you; the docs also call it an endpoint), one outbound connection to one NATS server, no open ports and no
per-agent server. It layers an opinionated approach to agent communication on top of the Synadia Agent Protocol (SAP)
and NATS.

**The key values that mash provides:**

1. Fast, durable, secure mail between agents.
2. No polling, no blocking, no extra turns.
3. Location-agnostic.
4. Agent discovery and presence.
5. Agent wake on urgent message delivery.
6. Agent-agnostic (ROADMAP).
7. Messaging policy (ROADMAP).

**Use mash when** your agents are sessions you run, in one NATS account, working on related problems,
and messages must wait for a busy or stopped recipient. **Use A2A** when agents belong to different teams or
organisations and are called as services; **use MCP** to give a model tools and data. The two can sit side by side:
A2A at the edge, mash inside. See [how mash compares](docs/comparison.md), including "why not A2A?".

**Why not Claude Code's built-in [cross-session messaging](https://code.claude.com/docs/en/cross-session-messaging)?** It is tied to one agent host and one user, needs a claude.ai sign-in to reach another machine, holds mail for an offline session only until it reconnects, and describes no sender proof. Use mash when mail must outlive the recipient's session, when you want signed sender proof with trust labels, when you run your own NATS server, or when more than one user or machine is involved. The comparison table is in [architecture.md](docs/architecture.md#14-alternatives-and-why-they-do-not-fit), and [comparison.md](docs/comparison.md#3-choose-mash-when-choose-something-else-when) says when the built-in feature is the better choice.

![How mash connects sessions](docs/images/001-topology.svg)

Two paths, one bus: live traffic (presence, status, live prompts) rides plain NATS and is not stored; mail rides
JetStream (the NATS feature that stores messages on the server) and is kept up to 30 days. ([details](docs/comparison.md#4-two-paths-live-and-durable))

**Status: v0.5.6, pre-1.0.** One host adapter (Claude Code). Linux and macOS, amd64 and arm64; Windows is not supported (WSL 2 with the Linux binary is expected to work but has not been tested). Interfaces may still change before 1.0. Known limits are listed in [the comparison](docs/comparison.md#9-known-limits).

## Install

```sh
curl -fsSL https://github.com/bogworks/mash/releases/latest/download/install.sh | bash
```

This installs `mash` and `mashctl` from the latest release into `~/.local/bin`, after checking each against the release's `SHA256SUMS`. It asks nothing and needs no `sudo`. [docs/install.md](docs/install.md) says what it does, how to pin a release, and how to check the script before you run it. To install the Claude Code plugin, see [docs/install.md](docs/install.md#2-the-plugin).

## Check the download

`install.sh` checks each binary against the release's `SHA256SUMS`. When a current GitHub CLI (`gh`) is installed it also verifies `SHA256SUMS` against the release's build attestation (a record signed by GitHub that the release workflow built these files from the release tag), and installs nothing if that fails. Without `gh` it says so and trusts the TLS connection to github.com. To check by hand before you run anything, download the files and verify the attestation:

```sh
BASE=https://github.com/bogworks/mash/releases/download/v0.5.6
curl -fsSLO $BASE/install.sh -O $BASE/SHA256SUMS -O $BASE/mash.intoto.jsonl
gh attestation verify SHA256SUMS -R bogworks/mash --bundle mash.intoto.jsonl \
  --signer-workflow bogworks/mash/.github/workflows/release.yml --source-ref refs/tags/v0.5.6
```


The rest (checking `install.sh` itself against `SHA256SUMS`, and building from source) is in [docs/install.md](docs/install.md#verify-what-you-download).

## Quick start

The quick start is one guided walkthrough that takes you from nothing to two Claude Code sessions mailing each other, with the output of every command. Start at the [walkthrough](docs/guide/walkthrough.md).

## Documentation

- [docs/README.md](docs/README.md): the index of the documentation.
- [docs/reference/README.md](docs/reference/README.md): the command reference.
- [docs/mashctl.md](docs/mashctl.md) and [docs/operations.md](docs/operations.md): setting up and running the server side. For TLS, a cluster of NATS servers and a second `mashctl` machine, see [docs/tls-and-clusters.md](docs/tls-and-clusters.md).

## Synadia Cloud

Synadia Cloud is not supported by mashctl in 0.5.6: see [docs/synadia.md](docs/synadia.md).


## Addresses and the Synadia Agent Protocol

mash is a SAP agent per session, with one departure: an endpoint is addressed `owner.group.instance`, and the owner (not the harness) takes the first subject slot, so a credential can be fenced to its owner. Details, and what that means for discovery, are in [docs/sap.md](docs/sap.md).

## What you get

- **Mail that waits.** Each agent (endpoint) has its own JetStream inbox stream, a stored queue of its mail on the server. Mail sent to a session that is busy, asleep or not running waits up to 30 days, and arrives the moment the session can take it.
- **Mail kinds cut turn cost.** `mash send --kind` (or the send tool's `kind`) marks mail `blocker`, `question` or `task` (wakes an idle session) or `result` or `fyi` (opt-in; waits for the session's next turn); with no kind a send is `task`.
- **No polling turns.** Mail that lands mid-turn rides along with the next tool result (a hook). An idle session is woken by the mail itself: after 45 seconds unshown (`MASH_IDLE_WAKE`) if the mail is a blocker, question or task, at once for urgent mail from a verified sender, and again with backoff if a wake starts no turn. A burst arrives as one turn. A small batch arrives in full; a large one arrives as a digest, and Claude reads the messages it cares about.
- **Fast replies.** The session answers through tools on the same process that holds the NATS connection open: no subprocess, no CLI, one network round trip.
- **Verified senders.** Every message is signed with the sender's own NATS key (SAP §13), and the receiver checks it against a directory. A forged message is dropped; an unproven one is labelled as such.
- **Unique endpoints.** One NATS user per address, and a lease (a claim on the address, held while the session runs) that refuses a second live session under the same address. A credential reaches only its own owner: it may mail any endpoint of that owner and no other owner's.
- **Live prompts too.** Because each session is a SAP agent, you can `mash prompt` it (or use any SAP client) and get a streamed answer.

Messaging policy is not in this release; see [what is coming in 0.6.0](docs/reference/coming-in-0-6-0.md).

## How it works

![A Claude Code session talks stdio (MCP) to mash serve, which holds one TLS connection to the NATS server (the mash account: inbox streams, mail subjects, directory and lease buckets, SAP presence).](docs/images/007-session-and-server.svg)

The design, with every subject, permission and limit, is in [docs/specs/2026-10-21-mash-design.md](docs/specs/2026-10-21-mash-design.md).

## Releases

How releases are built and checked: [docs/release.md](docs/release.md).

## Changelog

What changed in each release: [CHANGELOG.md](CHANGELOG.md).

## Contributing

See [CONTRIBUTING.md](CONTRIBUTING.md). Everyone taking part follows the [Code of Conduct](CODE_OF_CONDUCT.md).

## License

Apache-2.0. See [LICENSE](LICENSE).
