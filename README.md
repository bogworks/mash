# mash

mash is a messaging mesh for agents. It gives every Claude Code session its own durable, signed mailbox on a NATS server, and delivers mail into the session through Claude Code hooks (a Claude Code channel wakes an idle session), so no turn is wasted polling an inbox.

It is the [Synadia Agent Protocol](https://github.com/synadia-ai/synadia-agent-sdk-docs) (SAP 0.3) plus one extra verb, `mail`, and a durability layer, JetStream. mash invents no protocol of its own: every mash session is an ordinary SAP agent that any SAP tool can discover, ping and prompt.

## Quickstart on Synadia Cloud (free tier)

The fastest way to try mash is a free [Synadia Cloud](https://cloud.synadia.com) account: [`scripts/quickstart-synadia.sh`](scripts/quickstart-synadia.sh) sets up the server side from one API token (it needs curl and jq; it is a script, not part of the mash binary). You need Go and Claude Code with channels: a claude.ai or Console sign-in, not Bedrock, Agent Platform or Foundry; organisations need an Owner to allow mash ([docs/install.md](docs/install.md)).

1. Sign up for Synadia Cloud (the free tier is enough) and create an API token for your team in the Synadia console.
2. Install mash, the plugin, and the channel allowlist (once per machine), as in [docs/install.md](docs/install.md) sections 1 to 3:

   ```sh
   go install github.com/bogworks/mash/cmd/mash@latest
   claude plugin marketplace add bogworks/mash
   claude plugin install mash@mash
   ```

3. Give the token to the script through the environment (or `--token-stdin`, or the prompt in a terminal; it is never taken from the command line and never stored), look at the plan, then run it:

   ```sh
   export SYNADIA_API_TOKEN=...
   curl -fsSLO https://github.com/bogworks/mash/releases/download/v0.3.0/quickstart-synadia.sh
   curl -fsSLO https://github.com/bogworks/mash/releases/download/v0.3.0/SHA256SUMS
   test "$(grep -c ' quickstart-synadia.sh$' SHA256SUMS)" = 1 &&
     grep ' quickstart-synadia.sh$' SHA256SUMS | sha256sum -c - &&   # macOS: shasum -a 256 -c -
     bash quickstart-synadia.sh --dry-run --endpoint alice --endpoint bob &&
     bash quickstart-synadia.sh --endpoint alice --endpoint bob
   ```

   The script comes from the release, not from `main`, and nothing runs unless its checksum matches (the `&&` chain stops at the first failure). Contributors can run the script from their checkout instead.

   `--endpoint` repeats; with none, the script makes one named `<user>-<host>`. It checks the account's stream limit first and, if the plan does not fit, creates nothing and says so. It uses a temporary admin user, which it deletes at the end, and writes `~/.config/mash/config.json` and one `~/.config/mash/<name>.creds` per endpoint (mode 0600). With two or more endpoints it sends a test mail from the first to the second (`--no-test` skips it).
4. Start a session as an endpoint, and mail the other one:

   ```sh
   MASH_NAME=alice claude --channels plugin:mash@mash
   ```

   Ask Claude to send "hello" to `bob`. In a second terminal, `MASH_NAME=bob claude --channels plugin:mash@mash` receives it; Claude can answer with the `reply` or `send` tool, and `MASH_NAME=alice mash inbox` shows the mailbox from a shell.

The free tier allows 5 streams (mash uses 2 shared, plus 1 per endpoint, so 3 endpoints), 5 connections (one per live session), a 512 KiB message payload (mash caps sends to the server's `max_payload`), and R1 only (no replication, so no HA). Limits, and what to do when the script fails, are in [docs/synadia.md](docs/synadia.md).

## What you get

- **Mail that waits.** Each endpoint has its own JetStream inbox stream. Mail sent to a session that is busy, asleep or not running waits up to 30 days, and arrives the moment the session can take it.
- **Mail kinds cut turn cost.** `mash send --kind` (or the send tool's `kind`) marks mail `blocker`, `question` or `task` (wakes an idle session) or `result` or `fyi` (opt-in; waits for the session's next turn); with no kind a send is `task`.
- **No polling turns.** Mail that lands mid-turn rides along with the next tool result (a hook). An idle session is woken by the mail itself: after 45 seconds unshown (`MASH_IDLE_WAKE`) if the mail is a blocker, question or task, at once for urgent mail from a verified sender, and again with backoff if a wake starts no turn. A burst arrives as one turn. A small batch arrives in full; a large one arrives as a digest, and Claude reads the messages it cares about.
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

You need a NATS server in operator mode with JetStream, an account for mash, and Claude Code with channels (a claude.ai or Console sign-in, not Bedrock, Agent Platform or Foundry; organisations need an Owner to allow mash; the channel wakes an idle session, and hooks deliver the mail). [docs/install.md](docs/install.md) covers it end to end. The short version:

```sh
go install github.com/bogworks/mash/cmd/mash@v0.3.0      # a tagged release (use @latest for the newest); `mash version` prints the tag
claude plugin marketplace add bogworks/mash
claude plugin install mash@mash
# allowlist the channel once per machine (managed settings), then:
MASH_NAME=my-session claude --channels plugin:mash@mash
```

To install the plugin of exactly the mash you have (a pinned release, no GitHub fetch), export it and add the directory as the marketplace:

```sh
mash plugin export ~/.mash-plugin        # writes marketplace + plugin; needs no config or NATS
claude plugin marketplace add ~/.mash-plugin
claude plugin install mash@mash
```

Operators provision endpoints with `mash admin` and their usual NATS tooling: see [docs/operations.md](docs/operations.md).

## Status

mash is pre-1.0 (0.3.0). It supports Claude Code sessions as endpoints: durable, signed mail with trust labels, delivered by the plugin's hooks with a channel wake for idle sessions, on a NATS server you run or on Synadia Cloud's free tier ([`scripts/quickstart-synadia.sh`](scripts/quickstart-synadia.sh); see [docs/install.md](docs/install.md)). The spec is in docs/specs, and an integration suite runs against an in-process NATS server carrying the production permissions. Mail survives process restarts. To survive a host crash on a self-hosted server set `sync_interval: always` ([docs/operations.md](docs/operations.md#durability-and-sync_interval)). Interfaces may still change before 1.0. Later phases (an archive leaf node, monitoring, approvals) are listed in the spec.

## Releases

Releases are signed tags built reproducibly from fresh clones: see [docs/release.md](docs/release.md).

## Changelog

What changed in each release: [CHANGELOG.md](CHANGELOG.md).

## Contributing

See [CONTRIBUTING.md](CONTRIBUTING.md). Everyone taking part follows the [Code of Conduct](CODE_OF_CONDUCT.md).

## License

Apache-2.0. See [LICENSE](LICENSE).
