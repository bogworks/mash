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

Status: v0.5.6, one host adapter (Claude Code). Known limits are listed in
[the comparison](docs/comparison.md#9-known-limits).

Supported platforms: Linux and macOS (amd64, arm64). Native Windows is not supported. WSL 2 with the Linux binary is expected to work but has not been tested.

## Install mash

One command installs `mash` and `mashctl` (the tool that sets up the server and makes endpoints) from the latest release into `~/.local/bin`, after checking each against the release's checksums (and the attestation too, when `gh`, the [GitHub CLI](https://cli.github.com/), is installed):

```sh
curl -fsSL https://github.com/bogworks/mash/releases/latest/download/install.sh | bash
```

Output:

```
[built output: to fill from the release build]
```

Check that both programs are there and are the same version:

```sh
mash version
mashctl version
```

[docs/install.md](docs/install.md#1-the-binary) says what the script does and how to read and check it before you run it. A release binary is the build that the project's release workflow made from a tag, and the release also carries the four `mash-<os>-<arch>` and four `mashctl-<os>-<arch>` files, `SHA256SUMS` and the attestation, if you would rather download and check them by hand ([how](docs/release.md)).

**`go install` (builds from source).** `go install github.com/bogworks/mash/cmd/mash@v0.5.6` and `go install github.com/bogworks/mash/cmd/mashctl@v0.5.6` need Go 1.27.4 or newer. They compile the source on your machine, so the result is not the attested build, and `mash version` prints `unknown` as the commit.

## Quick start: one machine, no account

This path needs no Synadia account and no server you run by hand. You build two agents, Alice and Bob, that send each other mail. An agent is a Claude Code session; its mailbox is an endpoint, with an address of three parts, `owner.group.name`. Here the owner is `acme`, the group is `demo` and the names are `alice` and `bob`, so the addresses are `acme.demo.alice` and `acme.demo.bob`. A group has no default: you always name it. The owner name goes into the server in step 1 and cannot change later without starting over.

You need:

- `mash` and `mashctl` installed as above.
- `nats-server`, 2.10.7 or newer with JetStream, and the `nats` command, on your `PATH`. Get them from the [nats-server releases](https://github.com/nats-io/nats-server/releases) and the [natscli releases](https://github.com/nats-io/natscli/releases). Also `openssl`, `curl`, and `ss` (Linux; on a minimal Debian or Ubuntu image it is in `iproute2`) or `lsof` (macOS).
- Claude Code with channels (a claude.ai or Console sign-in, not Bedrock, Agent Platform or Foundry). A channel is how a plugin pushes a message into a running session.

If `MASH_SERVER`, `MASH_ENDPOINT` or other `MASH_*` variables are set from another setup, clear them first (`unset $(env | sed -n 's/^\(MASH_[A-Z_]*\)=.*/\1/p')`). They override the config files, and the commands below would go to that other server.

**1. Make a NATS server on this machine.** `mash-local-nats-setup.sh` is a script of the release. It makes a local NATS server for mash (TLS, JetStream, sign-in with credentials, listening on this machine only, run as you) in a new folder, `~/mash-nats` unless you pass `--dir`. Download it and check it against `SHA256SUMS` before you run it:

```sh
BASE=https://github.com/bogworks/mash/releases/latest/download
curl -fsSLO $BASE/mash-local-nats-setup.sh -O $BASE/SHA256SUMS
grep ' mash-local-nats-setup.sh$' SHA256SUMS | sha256sum -c - &&  # macOS: shasum -a 256 -c -
bash mash-local-nats-setup.sh --owner acme
```

`--owner` names the owner; without it the script asks once on the terminal. Output:

```
[built output: to fill from the release build]
```

The last line the script prints is the command for the next step. It reads `mash-setup.yaml`, which the script wrote in the server folder.

Then have `mashctl` set mash up on that server from the setup file:

```sh
mashctl init --config ~/mash-nats/mash-setup.yaml
```

Output:

```
Initialized mash on tls://localhost:4222 for owner acme
```

`mashctl init` keeps its own copy of the keys in the config directory, `~/.config/mash` by default or `$MASH_CONFIG_DIR`. It is safe to run again: a second run says `Already initialized on tls://localhost:4222; nothing changed`.

**2. Make the two endpoints.** `mashctl` makes each endpoint, with its credentials, and writes a join file. The endpoint then joins with that file:

```sh
mashctl endpoint create acme.demo.bob --out acme.demo.bob.join
mash join acme.demo.bob.join

mashctl endpoint create acme.demo.alice --out acme.demo.alice.join
mash join acme.demo.alice.join
```

Output of the first `create`, then of the first `join`:

```
Created endpoint acme.demo.bob
Join file: acme.demo.bob.join
```

```
Joined as acme.demo.bob.
```

`mash join` installs `<address>.creds` and the server's address in the config directory. Whoever holds a creds file is that endpoint.

If the endpoint is on another machine and its private key must never leave it, the endpoint makes its own key and `mashctl` signs only the public half:

```sh
mash key create --out bob.seed                                                     # writes bob.seed and bob.seed.pub
mashctl endpoint create acme.demo.bob --out acme.demo.bob.join --public-key bob.seed.pub
mash join acme.demo.bob.join --key bob.seed
```

`mash key create` prints `Created bob.seed and bob.seed.pub.` See [an endpoint on another machine](docs/operations.md#an-endpoint-on-another-machine).

**3. Check them.** `MASH_ENDPOINT` names which endpoint `mash` acts as; it is how one machine plays two agents:

```sh
MASH_ENDPOINT=acme.demo.bob mash status
MASH_ENDPOINT=acme.demo.alice mash status
```

```
acme.demo.bob: ready, offline
```

Ready means the server is there and reachable and the credentials are valid. Offline is normal here: no session holds the endpoint open yet.

**4. Install the Claude Code plugins.** The mash repository is its own Claude Code marketplace. `mash` is the plugin that gives a session its inbox; `mash-setup` is two skills that set mash up and look after it (you can skip it):

```sh
claude plugin marketplace add bogworks/mash
claude plugin install mash@mash
claude plugin install mash-setup@mash
```

In Claude Code itself the same steps are `/plugin marketplace add bogworks/mash`, `/plugin install mash@mash` and `/plugin install mash-setup@mash`.

Start two sessions, each as its own endpoint:

```sh
MASH_ENDPOINT=acme.demo.bob claude --dangerously-load-development-channels plugin:mash@mash     # terminal 1
MASH_ENDPOINT=acme.demo.alice claude --dangerously-load-development-channels plugin:mash@mash   # terminal 2
```

**5. Send mail.** From a shell, Bob mails Alice, and Alice reads it:

```sh
MASH_ENDPOINT=acme.demo.bob mash send acme.demo.alice "lunch at noon?"
MASH_ENDPOINT=acme.demo.alice mash inbox
```

Output:

```
[built output: to fill from the release build]
```

Normally the agent does this through the plugin's tools, and you never type it. In Bob's session, ask: "Send acme.demo.alice a note: lunch at noon?". The mail reaches Alice's session with her next prompt, or at once if she was idle, labelled `[verified]`.

**6. Stop, start and remove the server.** The server is a program you run as yourself. After a reboot, start it again. Removing it deletes every mailbox and message on it.

```sh
bash mash-local-nats-setup.sh --stop
bash mash-local-nats-setup.sh --start
bash mash-local-nats-setup.sh --remove      # you type the folder's path to confirm
```

Pass the same `--dir` as in step 1 if you did not use the default.

`MASH_ENDPOINT` (or `endpoint` in `config.json`, or `--endpoint`), the full address `owner.group.name`, names the endpoint a session runs as. With none set, mash uses the one endpoint whose creds file sits directly in the config directory; if several are installed, as here, it stops and you name one. Each endpoint has a directory record and a credentials ("creds") file named `<owner>.<group>.<name>.creds` that proves who it is; `mashctl endpoint create` makes both. Recipients in `mash send` are written `owner.group.name`; a bare name is refused.

`--channels` and `--dangerously-load-development-channels` are Claude Code research-preview flags. They are not shown in `claude --help`, but they work. The development flag lets the plugin wake an idle session without the channel allowlist (a list of channel plugins that Claude Code lets run without asking), and Claude Code asks you to confirm it at each launch; that is expected. Without the flag, mail still arrives at a session's next prompt or tool call. The steps are explained in [docs/install.md](docs/install.md), and [docs/operations.md](docs/operations.md#setting-mash-up-with-mashctl-init---config) covers setting mash up on a server.

## Synadia Cloud

Synadia Cloud is not supported by mashctl in 0.5.6; see [#876](https://github.com/bogworks/mash/issues/876).

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

Requirements: Claude Code 2.1.312 or newer (the oldest version a live receipt passed on; mash does not refuse older ones).

You need a NATS server in operator mode with JetStream (TLS recommended: use a `tls://` url, and for a private CA or mutual TLS set `ca`, `tls_cert` and `tls_key`; see [docs/install.md](docs/install.md#4-configuration)), an account for mash, and Claude Code with channels (a claude.ai or Console sign-in, not Bedrock, Agent Platform or Foundry; organisations need an Owner to allow mash; the channel wakes an idle session, and hooks deliver the mail). [docs/install.md](docs/install.md) covers it end to end. The short version:

```sh
curl -fsSL https://github.com/bogworks/mash/releases/latest/download/install.sh | bash   # mash and mashctl; to build from source see above
claude plugin marketplace add bogworks/mash
claude plugin install mash@mash
# allowlist the channel once per machine (managed settings are the admin-controlled settings file of Claude Code): write /etc/claude-code/managed-settings.json (Linux)
# or /Library/Application Support/ClaudeCode/managed-settings.json (macOS), see docs/install.md section 3, then:
MASH_ENDPOINT=acme.demo.alice claude --channels plugin:mash@mash
```

Operators set up the server with `mashctl init --config` and make endpoints with `mashctl endpoint create`. See [docs/mashctl.md](docs/mashctl.md) and [docs/operations.md](docs/operations.md). For TLS, a cluster of NATS servers and a second `mashctl` machine, see [docs/tls-and-clusters.md](docs/tls-and-clusters.md).

## Status

mash is v0.5.6. Use the same version of `mash` and `mashctl` (the quick start needs `mash key create` and `mash join`, which 0.5.0 does not have); 0.5.0 (released 2026-11-16) stays the version for running deployments until they are moved (see the release notes). It supports Claude Code sessions as endpoints: durable, signed mail with trust labels, delivered by the plugin's hooks with a channel wake for idle sessions, on a NATS server you run (see [docs/install.md](docs/install.md)). The spec is in docs/specs, and an integration suite runs against an in-process NATS server carrying the production permissions. Mail survives process restarts. To survive a host crash on a self-hosted server set `sync_interval: always` ([docs/operations.md](docs/operations.md#durability-and-sync_interval)). "Durable" means stored on the NATS server until read, for up to 30 days and 16 MiB per inbox, on one replica. Past those limits the oldest messages go first, read or not, and an inbox on a clustered server is unavailable while its node is down; see [inbox limits](docs/operations.md#inbox-limits) and [durability](docs/operations.md#durability-and-sync_interval). Upgrading to 0.5.5 is a clean break from 0.5.0: make new endpoints with `mashctl endpoint create` and remove the old ones with `mashctl endpoint delete`. Mail unread in an old endpoint is not carried over. Interfaces may still change before the next major revision. Later phases (an archive leaf node, monitoring, approvals) are listed in the spec.

## Releases

Releases are signed tags built reproducibly from fresh clones: see [docs/release.md](docs/release.md).

## Changelog

What changed in each release: [CHANGELOG.md](CHANGELOG.md).

## Contributing

See [CONTRIBUTING.md](CONTRIBUTING.md). Everyone taking part follows the [Code of Conduct](CODE_OF_CONDUCT.md).

## License

Apache-2.0. See [LICENSE](LICENSE).
