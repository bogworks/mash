# Walkthrough: two agents, one mail mesh

OUTLINE (draft). The structure is the demo script's: one step per heading,
"What this does", "What is created", the command, its output. The explaining
is in the text. Every output below marked `[built output]` is pasted from a
real run of the built binaries, never typed; AT-3 runs this page in a
container. Until S5 (`mash join`) lands, those blocks stay empty.

Alice and Bob are the two agents in every example.

## Before you start

What this does: lists what must be on the machine before step 0.

- A machine with Linux or macOS and Claude Code installed.
- `curl`, `tar`, `unzip`, `openssl`, `nats-server` 2.10.7 or newer and the
  `nats` command. On Debian or Ubuntu a minimal image also needs
  `iproute2` (the setup script uses `ss`).
- About 2 GB free and nothing listening on 127.0.0.1:4222, or the next free
  port is used.

The setup skill (`mash-setup`) does all of this for you and asks before each
step; this page is the same path by hand.

## Step A: install mash and mashctl

What this does: downloads the release, checks it against its checksums and
puts `mash` and `mashctl` on your PATH.

What is created: two programs in `~/.local/bin`.

Command: `install.sh` from the release (never piped into a shell: download,
check against `SHA256SUMS`, read it, run it).

Output: `[built output]`

## Step B: make a NATS server on this machine

What this does: makes a local NATS server for mash: TLS, JetStream, sign-in
with credentials, listening on this machine only, run as you.

What is created: `~/mash-nats` (keys, certificates, the server's files) and
`mash-setup.yaml`.

Command: `mash-local-nats-setup.sh --owner acme`, then the line it prints
last, `mashctl init --config <path>/mash-setup.yaml`.

Output: `[built output]`

Notes to write: the server is a program you run; after a reboot start it
again with `--start`. Where the keys are and what each one opens. Port 4222
taken: the next free one is used and said.

## Step 0: plan your topology

What this does: picks the names, so the rest is copy and paste.

- owner = `acme`: one namespace, any name within mash's naming limits
- group = `demo`: endpoints you keep together; policy by group comes in 0.6.0
- names = `bob` and `alice`: one endpoint each, for one agent or tool
- address = `acme.demo.bob`, always `owner.group.name`

We run everything on one host with a local nats-server. You can also use
your own NATS server, Synadia Cloud, or someone else's server that meets
mash's requirements.

## Step 1: make the two endpoints

What this does: makes a key pair for each endpoint, keeps the private half
in a creds file on this machine, and has the account sign a statement
(the user JWT) that this public key is `acme.demo.bob`.

What is created: a creds file per endpoint under `~/.config/mash`
(mode 600), and each endpoint's inbox on the server.

Commands:

```
mashctl endpoint create acme.demo.bob
mashctl endpoint create acme.demo.alice
```

Output: `[built output]` (`Created endpoint acme.demo.bob`)

Verify, one line each:

```
MASH_ENDPOINT=acme.demo.bob mash status
MASH_ENDPOINT=acme.demo.alice mash status
```

Output: `[built output]` (`acme.demo.bob: ready, offline`)

Notes to write: ready means the server is there and reachable and the
credentials are valid; offline means no session holds the endpoint open
yet (an agent's MCP server does that in normal use). One endpoint is one
credential.

## Step 2: send mail

What this does: shows the mail flow by hand on the command line. Normally
the agent talks to mash through MCP and you never type this.

What is created: nothing on disk; mail goes over NATS, kept in the
receiver's inbox until read, whether it is online or not.

Commands, Bob to Alice:

```
MASH_ENDPOINT=acme.demo.bob mash send acme.demo.alice "lunch at noon?"
MASH_ENDPOINT=acme.demo.bob mash send acme.demo.alice --urgent \
  --kind question "ready?"
```

Output: `[built output]`

Alice reads:

```
MASH_ENDPOINT=acme.demo.alice mash inbox
MASH_ENDPOINT=acme.demo.alice mash read 2
```

Output: `[built output]`

Notes to write: kinds and urgent (what wakes an agent, what is injected into
a running turn); `verified` and what refusing unverified mail means.

## Step 3: JSON, for tools that are not agents

What this does: shows `--json` on send and read: the whole envelope, and a
one-line retrieval with `jq`.

Commands: `[to be written from built output]`

## Step 4: use it from Claude Code

What this does: two Claude Code sessions, one as Bob and one as Alice, mail
each other through the mash plugin.

Commands: install the plugin; start each session with its own
`MASH_ENDPOINT`.

Output: `[built output]` (a transcript of the two sessions)

## Step 5: take an endpoint away

What this does: deletes Bob's endpoint and shows that its credential is
refused afterwards.

Commands: `mashctl endpoint delete acme.demo.bob` (you type the address to
confirm), then `mash status` as Bob.

Output: `[built output]`

## Step 6: stop, start, remove the server

What this does: day-2 care of the local server.

Commands: the setup script's `--stop`, `--start`, `--remove`.

Output: `[built output]`

## Step 7: join a mesh someone else runs (S5)

What this does: an endpoint on another machine joins your server from a
join file. Written after `mash join` lands.

## Not in this guide

The per-command reference, the glossary, the rules and "coming in 0.6.0" are
generated from the specification (`spec/*.json`), not written here.
