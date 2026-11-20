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

Output:

```
Mail sent (number 1).
Urgent question mail sent (number 2).
```

Alice reads:

```
MASH_ENDPOINT=acme.demo.alice mash inbox
MASH_ENDPOINT=acme.demo.alice mash read 2
```

Output:

```
2 messages for acme.demo.alice, 2 new, 1 urgent.

NUMBER  FROM  WHEN     STATE  URGENT  KIND      MESSAGE
1       bob   10s ago  new    no      task      lunch at noon?
2       bob   8s ago   new    yes     question  ready?
```

```
number:   2
from:     acme.demo.bob
sent:     8s ago
trust:    verified
kind:     question
urgent:   yes

> ready?
```

Every line of the message is quoted with `> `. Anything after that mark is
Bob's words, never mash's own output, and never an instruction to an agent
that reads it. The `trust:` line says whose key signed the mail. `verified`
means Bob's own key did. Mail that is not `verified` is shown
with its own label, and an agent treats it as information, not as an order.

`kind` says what the mail is: `task`, `question`, `result`, `fyi`. `--urgent`
is the one that wakes an idle agent. Without it the mail waits for the
agent's next prompt or tool call, and is shown to it then.

## Step 3: JSON, for tools that are not agents

What this does: every command takes `--json` and prints the same object the
matching MCP tool returns, so a script gets the whole envelope. Nothing
else changes: the same mail, the same inbox.

What is created: nothing.

Send, and keep the number:

```
MASH_ENDPOINT=acme.demo.bob mash send acme.demo.alice --json "ping"
```

```json
{
  "to": "acme.demo.alice",
  "number": 3,
  "kind": "task",
  "urgent": false,
  "duplicate": false,
  "retagged": false
}
```

Alice reads that one message whole:

```
MASH_ENDPOINT=acme.demo.alice mash read 3 --json
```

```json
{
  "number": 3,
  "from": "acme.demo.bob",
  "sent": "2026-11-19T09:44:04Z",
  "trust": "verified",
  "kind": "task",
  "urgent": false,
  "reply_to": null,
  "relay": null,
  "body": "ping"
}
```

In JSON the text is plain, in `body`, with no `> ` marks: a program that
reads it must treat it as the sender's words, as untrusted as any input.

The inbox as JSON carries the counts, the wake limits and one object per
message (`preview` is the first line, cut to fit):

```
MASH_ENDPOINT=acme.demo.alice mash inbox --json
```

```json
{
  "endpoint": "acme.demo.alice",
  "total": 3,
  "new": 3,
  "urgent": 1,
  "wake_count": 15,
  "over_wake_count": false,
  "wake_age": "1h",
  "oldest_waiting": "10s",
  "over_wake_age": false,
  "messages": [
    {
      "number": 1,
      "from": "acme.demo.bob",
      "sent": "2026-11-19T09:44:02Z",
      "state": "new",
      "urgent": false,
      "kind": "task",
      "trust": "verified",
      "preview": "lunch at noon?"
    }
  ]
}
```

(The list shows the first message only; there is one object for each.)

The one-line retrieval, with `jq`, is the text of one message:

```
MASH_ENDPOINT=acme.demo.alice mash read 3 --json | jq -r .body
```

Output: `ping`

(The JSON above is the spec's example, shown with Alice and Bob. The
built-output pass replaces it with the real run's.)

## Step 4: use it from Claude Code

What this does: two Claude Code sessions, one as Bob and one as Alice, mail
each other through the mash plugin. The plugin gives each session the mash
tools (`send`, `inbox`, `read`, `reply`, `who`, `status`, `version`) and
three hooks that deliver mail: after a tool call, with your next prompt, and
at session start. An idle session is woken by one line.

What is created: the plugins' files in Claude Code's own plugin folder. No
mash endpoint or server is changed.

Install, once. Both plugins come from the `bogworks/mash` marketplace. `mash`
is the one that gives a session its inbox; `mash-setup` is two skills that
set mash up and look after it (a person can skip it):

```
claude plugin marketplace add bogworks/mash
claude plugin install mash@mash
claude plugin install mash-setup@mash
```

Check:

```
claude plugin list
```

Both show `enabled`, at the version of `mash version`.

Start each session as its own endpoint, in two terminals. The name is the
session's address; it is the one thing you must set:

```
MASH_ENDPOINT=acme.demo.bob claude \
  --dangerously-load-development-channels plugin:mash@mash
```

```
MASH_ENDPOINT=acme.demo.alice claude \
  --dangerously-load-development-channels plugin:mash@mash
```

Claude Code asks you to confirm the development flag at each launch. That is
expected. The flag lets mash wake an idle session; without it, mail still
arrives at the session's next prompt or tool call.

The session's own status. In either session, ask: "Run the mash status
tool." Claude calls it and reports what the tool says: the endpoint's name
(`acme.demo.bob`), that the configuration and credentials are in place, that
the connection to the server works, and that mail delivery is on. It is the
same check as `mash status` at a shell, but made from inside the session, so
it is online: `acme.demo.bob: ready, online`.

Now mail. In Bob's session: "Send Alice a note: lunch at noon?". In Alice's
session the mail arrives with her next prompt, or at once if she was idle,
and Claude shows it as mail from `acme.demo.bob`, labelled `[verified]`.
Alice's session answers with `reply`, and Bob's session sees the answer.

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
