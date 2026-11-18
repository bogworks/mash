# mash

**mash ("mail mesh") gives each of your AI agent sessions an inbox that keeps its mail.** The inbox lives on a NATS server (NATS is an open-source message server). Mail waits (in a single-replica inbox; see [durability](docs/operations.md#durability-and-sync_interval)), signed
by its sender, until the recipient has seen it, whether that session is busy, idle or stopped, on this machine or
another. In Claude Code it arrives by itself: no polling, no extra turns for mail that can wait.

mash is deliberately small: one Go binary per agent (an "agent" here is one running Claude Code session, named by you; the docs also call it an endpoint), one outbound connection to one NATS server, no open ports and no
per-agent server. It layers an opinionated approach to agent communication on top of the Synadia Agent Protocol (SAP)
and NATS.

**Use mash when** your agents are sessions you run, in one NATS account, working on related problems (a dev swarm),
and messages must wait for a busy or stopped recipient. **Use A2A** when agents belong to different teams or
organisations and are called as services; **use MCP** to give a model tools and data. The two can sit side by side:
A2A at the edge, mash inside. See [how mash compares](docs/comparison.md), including "why not A2A?".

**Why not Claude Code's built-in [cross-session messaging](https://code.claude.com/docs/en/cross-session-messaging)?** It is tied to one agent host and one user, needs a claude.ai sign-in to reach another machine, holds mail for an offline session only until it reconnects, and describes no sender proof. Use mash when mail must outlive the recipient's session, when you want signed sender proof with trust labels, when you run your own NATS server, or when more than one user or machine is involved. The comparison table is in [architecture.md](docs/architecture.md#14-alternatives-and-why-they-do-not-fit), and [comparison.md](docs/comparison.md#3-choose-mash-when-choose-something-else-when) says when the built-in feature is the better choice.

![How mash connects sessions](docs/images/001-topology.svg)

Two paths, one bus: live traffic (presence, status, live prompts) rides plain NATS and is not stored; mail rides
JetStream (the NATS feature that stores messages on the server) and is kept up to 30 days. ([details](docs/comparison.md#4-two-paths-live-and-durable))

Status: v0.5.5, one host adapter (Claude Code). Known limits are listed in
[the comparison](docs/comparison.md#9-known-limits).

Supported platforms: Linux and macOS (amd64, arm64). Native Windows is not supported. WSL 2 with the Linux binary is expected to work but has not been tested.

## Install mash

**Download a release (recommended).** A release binary is the build that the project's release workflow made from a tag, and you can check that. Download the binary for your platform and `SHA256SUMS` from the [v0.5.5 release](https://github.com/bogworks/mash/releases/tag/v0.5.5). The four binaries are:

- `mash-linux-amd64`
- `mash-linux-arm64`
- `mash-darwin-amd64`
- `mash-darwin-arm64`

```sh
BASE=https://github.com/bogworks/mash/releases/download/v0.5.5
curl -fsSLO $BASE/mash-linux-amd64
curl -fsSLO $BASE/SHA256SUMS
grep ' mash-linux-amd64$' SHA256SUMS | sha256sum -c - &&   # macOS: shasum -a 256 -c -
gh attestation verify mash-linux-amd64 -R bogworks/mash \
  --signer-workflow bogworks/mash/.github/workflows/release.yml \
  --source-ref refs/tags/v0.5.5 &&
chmod +x mash-linux-amd64 &&
BIN=~/.local/bin &&                                         # any directory; it must be on your PATH
mkdir -p "$BIN" && mv mash-linux-amd64 "$BIN/mash"
case ":$PATH:" in *":$BIN:"*) ;; *) echo "add $BIN to your PATH" ;; esac
mash version
```

The checksum shows the file arrived whole. The attestation, a record signed by GitHub, shows that this binary was built from the `v0.5.5` tag by the release workflow ([more](docs/release.md)). `gh` is the [GitHub CLI](https://cli.github.com/).

**`go install` (builds from source).** `go install github.com/bogworks/mash/cmd/mash@v0.5.5` needs Go 1.27.4 or newer. It compiles the source on your machine, so the result is not the attested build, and `mash version` prints `commit unknown`.

**`mashctl`** (the tool that sets up servers and administers endpoints) is not among the release assets: v0.5.5 carries only the four `mash-<os>-<arch>` binaries. For now install it with `go install github.com/bogworks/mash/cmd/mashctl@v0.5.5`; `mashctl version` prints `0.0.0-dev` from a go install.

## Quick start: one machine, no account

Step-by-step guides: your own NATS server on one machine, [docs/guides/nats-local.md](docs/guides/nats-local.md); Synadia Cloud (an account above the free tier), [docs/guides/nats-synadia.md](docs/guides/nats-synadia.md).

This path needs no Synadia account and no server you run by hand. You need:

- `mash` installed as above, and a Go 1.27.4 or newer toolchain for `mashctl`, with the directory `go install` writes to on your `PATH` (`go env GOBIN`, or `$(go env GOPATH)/bin` when `GOBIN` is empty; `~/go/bin` by default).
- `nats-server`, a current 2.x release with JetStream, on your `PATH`. Get it from the [nats-server releases](https://github.com/nats-io/nats-server/releases) (this repository builds and tests against 2.15).
- Claude Code with channels (a claude.ai or Console sign-in, not Bedrock, Agent Platform or Foundry). A channel is how a plugin pushes a message into a running session.

Start the server. `mashctl` is the tool that runs the server and makes endpoints; `mash` is what each session runs.

If `MASH_URL`, `MASH_OWNER` or other `MASH_*` variables are set from another setup, clear them first (`unset $(env | sed -n 's/^\(MASH_[A-Z_]*\)=.*/\1/p')`). They override the config files, and the commands below would go to that other server.

```sh
go install github.com/bogworks/mash/cmd/mashctl@v0.5.5   # mashctl is not in the release assets
mashctl init --local --owner alice --start                # a TLS-only NATS server that only this machine can reach, plus a local CA, keys and credentials
```

An agent is an endpoint with an address of three tokens, `owner.group.instance`. Here the owner is `alice`, the group is `dev` and the instances are `design` and `review`, so the addresses are `alice.dev.design` and `alice.dev.review`. A group has no default: you always name it.

Make each endpoint in three commands. The endpoint makes its own key, `mashctl` signs the public half, and the endpoint joins the signed JWT to its seed. The seed never leaves the endpoint's machine.

```sh
mash key create --out design.seed                                        # writes design.seed and design.pub
mashctl endpoint create alice/dev/design --public-key design.pub         # writes alice.dev.design.jwt
mash credential create --seed design.seed --jwt alice.dev.design.jwt     # writes alice.dev.design.creds in the config directory

mash key create --out review.seed
mashctl endpoint create alice/dev/review --public-key review.pub
mash credential create --seed review.seed --jwt alice.dev.review.jwt
```

When the endpoint and `mashctl` are on the same machine, one command does all three:

```sh
mashctl endpoint create alice/dev/design --make-key
mashctl endpoint create alice/dev/review --make-key
```

`--make-key` has one limit: the host that runs `mashctl` makes the key and holds it while the command runs, so it is the wrong form when the endpoint is on another machine. Use the three-command form there ([an endpoint on another machine](docs/operations.md#an-endpoint-on-another-machine)).

Then install the plugin and start two sessions:

```sh
mash plugin export ~/.mash-plugin
claude plugin marketplace add ~/.mash-plugin
claude plugin install mash@mash
MASH_OWNER=alice MASH_GROUP=dev MASH_NAME=design claude --dangerously-load-development-channels plugin:mash@mash   # terminal 1
MASH_OWNER=alice MASH_GROUP=dev MASH_NAME=review claude --dangerously-load-development-channels plugin:mash@mash   # terminal 2
```

Send mail from a shell and read it:

```sh
MASH_OWNER=alice MASH_GROUP=dev MASH_NAME=design mash send alice.dev.review "hello"
MASH_OWNER=alice MASH_GROUP=dev MASH_NAME=review mash inbox
```

When you are done, stop the local server: `D=<the server directory that mashctl init --local printed>; kill "$(cat "$D/server.pid")"`. The directory is `server` inside the config directory, which depends on `MASH_CONFIG_DIR` and `XDG_CONFIG_HOME`, so use the one `init --local` printed.

`MASH_OWNER` (or `owner` in `config.json`), `MASH_GROUP` (no default and no config key; a session without it stops with `group_missing`, exit 2) and `MASH_NAME`, the instance, name the endpoint a session runs as. Each endpoint has a directory record and a credentials ("creds") file named `<owner>.<group>.<instance>.creds` that proves who it is; `mashctl endpoint create` makes both. Recipients in `mash send` are written `owner.group.instance` or the flat key `owner_group_instance`; a bare name is refused.

`mashctl init --local` makes a server on port 4222 by default; if something else uses 4222, pass `--port N` to pick another. The server uses TLS with a certificate from a local certificate authority that `init --local` makes (`ca.pem` in the server directory); the `url` in `config.json` is `tls://127.0.0.1:4222` and `tls_ca` names `ca.pem`, so `mashctl` and endpoints on this host need no extra setting.

`--channels` and `--dangerously-load-development-channels` are Claude Code research-preview flags. They are not shown in `claude --help`, but they work.

The config and the credentials land in the config directory, `~/.config/mash` by default or `$MASH_CONFIG_DIR`; `init --local` prints it. The development flag lets the plugin wake an idle session without the channel allowlist (a list of channel plugins that Claude Code lets run without asking), and Claude Code asks you to confirm it at each launch; that is expected. Without the flag, mail still arrives at a session's next prompt or tool call. The steps are explained in [docs/install.md](docs/install.md), and [docs/operations.md](docs/operations.md#local-setup-with-mashctl-init---local) covers the local server, including how to stop it and start it again.

## Quickstart on Synadia Cloud (free tier)

This is the second path, for a server you do not run yourself. The step-by-step guide is [docs/guides/nats-synadia.md](docs/guides/nats-synadia.md). The free tier of [Synadia Cloud](https://cloud.synadia.com) cannot run mash (see the limits below), so use an account with a higher stream limit: [`scripts/quickstart-synadia.sh`](scripts/quickstart-synadia.sh) sets up the server side from one API token (it needs curl and jq; it is a script, not part of the mash binary). You need Go 1.27.4 or newer (or the release binary above) and Claude Code with channels: a claude.ai or Console sign-in, not Bedrock, Agent Platform or Foundry; organisations need an Owner to allow mash ([docs/install.md](docs/install.md)).

See it working first: [two Claude Code sessions exchanging mail, one woken by it](docs/demo/) (a recording to play with `asciinema play`).

1. Sign up for Synadia Cloud (an account that allows at least 5 streams plus 1 per endpoint; the free tier's 5 do not) and create an API token for your team in the Synadia console.
2. Install mash, the plugin, and the channel allowlist (once per machine; the allowlist step is a managed settings file, shown in [docs/install.md](docs/install.md#managed-settings-teams-and-managed-machines)), as in [docs/install.md](docs/install.md) sections 1 to 3:

   ```sh
   go install github.com/bogworks/mash/cmd/mash@v0.5.5   # or the release binary above
   claude plugin marketplace add bogworks/mash
   claude plugin install mash@mash
   ```

3. Give the token to the script through the environment (or `--token-stdin`, or the prompt in a terminal; it is never taken from the command line and never stored), look at the plan, then run it:

   ```sh
   export SYNADIA_API_TOKEN=...
   curl -fsSLO https://github.com/bogworks/mash/releases/download/v0.5.5/quickstart-synadia.sh
   curl -fsSLO https://github.com/bogworks/mash/releases/download/v0.5.5/SHA256SUMS
   test "$(grep -c ' quickstart-synadia.sh$' SHA256SUMS)" = 1 &&
     grep ' quickstart-synadia.sh$' SHA256SUMS | sha256sum -c - &&   # macOS: shasum -a 256 -c -
     bash quickstart-synadia.sh --dry-run --endpoint alice --endpoint bob &&
     bash quickstart-synadia.sh --endpoint alice --endpoint bob
   ```

   The Synadia script is part of the v0.5.5 release, and it was not part of 0.5.5's acceptance tests.

   The script comes from the release, not from `main`, and nothing runs unless its checksum matches (the `&&` chain stops at the first failure). Contributors can run the script from their checkout instead.

   `--endpoint` repeats; with none, the script makes one named `<user>-<host>`. It checks the account's stream limit first and, if the plan does not fit, creates nothing and says so. It uses a temporary admin user, which it deletes at the end, and writes `~/.config/mash/config.json` and one `~/.config/mash/<owner>.<group>.<instance>.creds` per endpoint (mode 0600). With two or more endpoints it sends a test mail from the first to the second (`--no-test` skips it).
4. Start a session as an endpoint, and mail the other one:

   ```sh
   MASH_GROUP=<group> MASH_NAME=alice claude --channels plugin:mash@mash
   ```

   Ask Claude to send "hello" to `<owner>.<group>.bob`. In a second terminal, `MASH_GROUP=<group> MASH_NAME=bob claude --channels plugin:mash@mash` receives it; Claude can answer with the `reply` or `send` tool, and `MASH_GROUP=<group> MASH_NAME=alice mash inbox` shows the mailbox from a shell.

The free tier allows 5 streams, but mash uses 5 shared streams plus 1 per endpoint, so the free tier cannot hold even one endpoint and the script refuses it before it creates anything; an account with a limit of 5 + N holds N endpoints. The free tier also allows 5 connections (one per live session), a 512 KiB message payload (mash caps sends to the server's `max_payload`), and R1 only (no replication, so no HA). Limits, and what to do when the script fails, are in [docs/synadia.md](docs/synadia.md).

## Addresses and the Synadia Agent Protocol

mash is a SAP agent per session, with one departure. An endpoint is addressed `owner.group.instance`, and its subjects are `agents.<verb>.<owner>.<group>.<instance>` (verbs `mail`, `prompt`, `status`, `hb`). SAP says the first subject slot SHOULD carry the harness; mash puts the owner there, because that is what lets a credential be fenced to its owner. So finding agents by a wildcard on the harness slot does not work with mash; discovery by the service name does. The metadata still says `agent: claude-code` and adds `owner`, `group` and `instance`. The three slots:

- **owner**: whose fleet this is; the permission fence. A credential publishes `agents.{mail,prompt,status}.<owner>.*.*`, so it is open to the whole owner by default, and it reaches no other owner.
- **group**: a set of endpoints of one owner working together (a project or a swarm). It is part of the address, not a permission boundary.
- **instance**: the session's own name.

## What you get

- **Mail that waits.** Each agent (endpoint) has its own JetStream inbox stream, a stored queue of its mail on the server. Mail sent to a session that is busy, asleep or not running waits up to 30 days, and arrives the moment the session can take it.
- **Mail kinds cut turn cost.** `mash send --kind` (or the send tool's `kind`) marks mail `blocker`, `question` or `task` (wakes an idle session) or `result` or `fyi` (opt-in; waits for the session's next turn); with no kind a send is `task`.
- **No polling turns.** Mail that lands mid-turn rides along with the next tool result (a hook). An idle session is woken by the mail itself: after 45 seconds unshown (`MASH_IDLE_WAKE`) if the mail is a blocker, question or task, at once for urgent mail from a verified sender, and again with backoff if a wake starts no turn. A burst arrives as one turn. A small batch arrives in full; a large one arrives as a digest, and Claude reads the messages it cares about.
- **Fast replies.** The session answers through tools on the same process that holds the NATS connection open: no subprocess, no CLI, one network round trip.
- **Verified senders.** Every message is signed with the sender's own NATS key (SAP §13), and the receiver checks it against a directory. A forged message is dropped; an unproven one is labelled as such.
- **Unique endpoints.** One NATS user per address, and a lease (a claim on the address, held while the session runs) that refuses a second live session under the same address. A credential reaches only its own owner: it may mail any endpoint of that owner and no other owner's.
- **Live prompts too.** Because each session is a SAP agent, you can `mash prompt` it (or use any SAP client) and get a streamed answer.

Policy commands (`mashctl policy`, allow lists) are not in this release; the engine arrives in 0.5.5.

## How it works

![A Claude Code session talks stdio (MCP) to mash serve, which holds one TLS connection to the NATS server (account MASH: inbox stream, mail subject, directory and lease KVs, SAP presence).](docs/images/007-session-and-server.svg)

The design, with every subject, permission and limit, is in [docs/specs/2026-10-21-mash-design.md](docs/specs/2026-10-21-mash-design.md).

## Install

mash needs a NATS server in operator mode (the NATS setup where an operator key signs accounts and accounts sign users) with JWT user credentials (a signed file that names one user and what it may do). Token, user/password and plain nkey authentication are not supported, because an endpoint's SAP signing identity is its NATS user key (the key in its creds file's user JWT; see the [design](docs/specs/2026-10-21-mash-design.md)).

Requirements: Claude Code 2.1.312 or newer (the oldest version a live receipt passed on; mash does not refuse older ones, `status` warns).

You need a NATS server in operator mode with JetStream (TLS recommended: use a `tls://` url, and for a private CA or mutual TLS set `tls_ca`, `tls_cert` and `tls_key`; see [docs/install.md](docs/install.md#4-configuration)), an account for mash, and Claude Code with channels (a claude.ai or Console sign-in, not Bedrock, Agent Platform or Foundry; organisations need an Owner to allow mash; the channel wakes an idle session, and hooks deliver the mail). [docs/install.md](docs/install.md) covers it end to end. The short version:

```sh
go install github.com/bogworks/mash/cmd/mash@v0.5.5      # Go 1.27.4 or newer; or use the release binary above. `mash version` prints the tag and `commit unknown`
claude plugin marketplace add bogworks/mash
claude plugin install mash@mash
# allowlist the channel once per machine (managed settings are the admin-controlled settings file of Claude Code): write /etc/claude-code/managed-settings.json (Linux)
# or /Library/Application Support/ClaudeCode/managed-settings.json (macOS), see docs/install.md section 3, then:
MASH_GROUP=dev MASH_NAME=my-session claude --channels plugin:mash@mash
```

To install the plugin of exactly the mash you have (a pinned release, no GitHub fetch), export it and add the directory as the marketplace:

```sh
mash plugin export ~/.mash-plugin        # writes marketplace + plugin; needs no config or NATS
claude plugin marketplace add ~/.mash-plugin
claude plugin install mash@mash
```

Operators set up the server with `mashctl init` (or `mashctl adopt` for a server you already run) and make endpoints with `mashctl endpoint create`; `mash admin` is deprecated. See [docs/mashctl.md](docs/mashctl.md) and [docs/operations.md](docs/operations.md). For TLS, a cluster of NATS servers and a second `mashctl` machine, see [docs/tls-and-clusters.md](docs/tls-and-clusters.md).

## Status

mash is v0.5.5. Use the same version of `mash` and `mashctl` (the quick start needs 0.5.5's `mash key create` and `mash credential create`, which 0.5.0 does not have); 0.5.0 (released 2026-11-16) stays the version for running deployments until they are moved (see the release notes). It supports Claude Code sessions as endpoints: durable, signed mail with trust labels, delivered by the plugin's hooks with a channel wake for idle sessions, on a NATS server you run or on Synadia Cloud's free tier ([`scripts/quickstart-synadia.sh`](scripts/quickstart-synadia.sh); see [docs/install.md](docs/install.md)). The spec is in docs/specs, and an integration suite runs against an in-process NATS server carrying the production permissions. Mail survives process restarts. To survive a host crash on a self-hosted server set `sync_interval: always` ([docs/operations.md](docs/operations.md#durability-and-sync_interval)). "Durable" means stored on the NATS server until read, for up to 30 days and 16 MiB per inbox, on one replica. Past those limits the oldest messages go first, read or not, and an inbox on a clustered server is unavailable while its node is down; see [inbox limits](docs/operations.md#inbox-limits) and [durability](docs/operations.md#durability-and-sync_interval). Upgrading to 0.5.5 is a clean break from 0.5.0: make new endpoints with `mashctl endpoint create` and remove the old ones with `mashctl endpoint delete`. Mail unread in an old endpoint is not carried over. Interfaces may still change before the next major revision. Later phases (an archive leaf node, monitoring, approvals) are listed in the spec.

## Releases

Releases are signed tags built reproducibly from fresh clones: see [docs/release.md](docs/release.md).

## Changelog

What changed in each release: [CHANGELOG.md](CHANGELOG.md).

## Contributing

See [CONTRIBUTING.md](CONTRIBUTING.md). Everyone taking part follows the [Code of Conduct](CODE_OF_CONDUCT.md).

## License

Apache-2.0. See [LICENSE](LICENSE).
