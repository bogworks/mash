# Walkthrough: two agents, one mail mesh

OUTLINE (draft). One step per heading, with "What this does", "What is
created", the command and its output. Every output marked `[built output]` is
pasted from a real run of the built programs, never typed; that run is a
container that follows this page exactly. Until `mash join` lands, the join
step stays short.

## What you will build

By the end, two AI agents, Alice and Bob, send each other mail. Each agent
is a Claude Code session; each has an inbox on a message server that keeps
its mail until it is read, even while the agent is off.

Words used here, each explained again where it first matters:

- **NATS** is an open-source message server. mash keeps the mail on one.
- **mash** is the program one agent uses to send and read mail. **mashctl**
  is the administrator's tool: it makes and removes the agents' mailboxes.
- An **endpoint** is one agent's mailbox, with its own address and key.
- An **address** is `owner.group.name`, for example `acme.demo.bob`.
- A **credential** (a creds file) is an endpoint's key. Whoever holds it is
  that endpoint.
- A **mesh** is the set of endpoints on one server.

Alice and Bob are the two agents in every example. The picture of how the
parts fit is in [stack.md](stack.md); how mash differs from plain Synadia
Agent Protocol (SAP) is in [mash-vs-sap.md](mash-vs-sap.md).

## Before you start

What this does: lists what must be on the machine before step 1.

- A machine with Linux or macOS and Claude Code installed.
- `curl`, `tar`, `unzip`, `openssl`, `jq` (step 7 only), `nats-server`
  2.10.7 or newer and the `nats` command. On Debian or Ubuntu a minimal image
  also needs `iproute2` (the setup script uses `ss`).
- About 2 GB free and nothing listening on 127.0.0.1:4222, or the next free
  port is used.

The `mash-setup` plugin has a skill that does all of this for you and asks
before each step; this page is the same path by hand.

## Step 1: plan the names

What this does: nothing on the machine. It picks the names, so the rest is
copy and paste. The owner name goes into the server in step 3 and cannot
change later without starting over.

- owner = `acme`: one namespace, any name within mash's naming limits
- group = `demo`: endpoints you keep together
- names = `bob` and `alice`: one endpoint each, for one agent or tool
- address = `acme.demo.bob`, always `owner.group.name`

We run everything on one host with a local nats-server, which step 3 makes
for you. mash needs a NATS server with TLS, JetStream and sign-in
credentials; yours may do instead (see the NATS notes in `docs/`).

## Step 2: install mash and mashctl

What this does: downloads the release, checks it against its checksums and
puts `mash` and `mashctl` in a folder; if it is not on your PATH, the
script says so.

What is created: two programs in `~/.local/bin`.

Command: `install.sh` from the release (never piped into a shell: download,
check against `SHA256SUMS`, read it, run it).

Output: `[built output]`

Check: `mash version` and `mashctl version` print the same version.

## Step 3: make a NATS server on this machine

What this does: makes a local NATS server for mash: TLS, JetStream, sign-in
with credentials, listening on this machine only, run as you.

What is created: `~/mash-nats` (keys, certificates, the server's files) and
`mash-setup.yaml`.

Command: `mash-local-nats-setup.sh --owner acme`, downloaded from the same
release and checked against `SHA256SUMS` like `install.sh`. Then the line it
prints last, `mashctl init --config <path>/mash-setup.yaml`.

`mashctl init` reads the setup file and keeps its own copy of the keys in
`~/.config/mash`. That folder is where `mashctl` and `mash` look for their
configuration and credentials.

Output: `[built output]`

Notes to write: the server is a program you run; after a reboot start it
again with `--start`. Where the keys are and what each one opens. Port 4222
taken: the next free one is used and said.

## Step 4: make the two endpoints

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

Verify, one line each. `MASH_ENDPOINT` names which endpoint `mash` acts as;
it is how one machine plays two agents:

```
MASH_ENDPOINT=acme.demo.bob mash status
MASH_ENDPOINT=acme.demo.alice mash status
```

Ready means the server is there and reachable and the credentials are valid.
Offline is normal here: no session holds the endpoint open yet.

Output: `[built output]` (`acme.demo.bob: ready, offline`)

Notes to write: ready means the server is there and reachable and the
credentials are valid; offline means no session holds the endpoint open
yet (an agent's MCP server does that in normal use). One endpoint is one
credential.

## Step 5: send mail

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
means Bob's own key did. By default mash refuses mail it cannot verify, on
receipt.

`kind` says what the mail is: `task`, `question`, `result`, `fyi`. Mail
waits in the inbox. A running agent is shown it at its next prompt or tool
call; `--urgent` is what asks an idle agent to wake (step 6).

## Step 6: use it from Claude Code

What this does: two Claude Code sessions, one as Bob and one as Alice, mail
each other through the mash plugin. The plugin gives each session the mash
tools (`send`, `inbox`, `read`, `who`, `status`, `version`) and
three hooks that deliver mail: after a tool call, with your next prompt, and
at session start. An idle session is woken by one line.

What is created: the plugins' files in Claude Code's own plugin folder. No
mash endpoint or server is changed.

Install, once. Both plugins come from the `bogworks/mash` marketplace. `mash`
(plugin `mash`, marketplace `mash`) is the one that gives a session its inbox;
`mash-setup` is two skills that set mash up and look after it (a person can
skip it):

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
expected: the flag is what lets mash wake an idle session at all. Without
it, mail still arrives at the session's next prompt or tool call.

The session's own status. In either session, ask: "Run the mash status
tool." Claude calls it and reports what the tool says: the endpoint's name
(`acme.demo.bob`), that the configuration and credentials are in place, that
the connection to the server works, and that mail delivery is on. It is the
same check as `mash status` at a shell, but made from inside the session, so
it is online: `acme.demo.bob: ready, online`.

Now mail. In Bob's session: "Send acme.demo.alice a note: lunch at noon?".
Add "urgent" to wake Alice's session if it is idle. In Alice's
session the mail arrives with her next prompt, or at once if she was idle,
and Claude shows it as mail from `acme.demo.bob`, labelled `[verified]`.
Alice's session answers with `send`, and Bob's session sees the answer.
Mail you sent in step 5 is waiting too: it arrives the same way.

Output: `[built output]` (a transcript of the two sessions)

## Step 7: JSON, for scripts (optional)

What this does: for a script or tool that is not an agent. Every command takes
`--json` and prints the same object the matching MCP tool returns, so a script
gets the whole message with its fields. Nothing else changes: the same mail,
the same inbox.

What is created: nothing. Reading a message at a shell shows it; it does not
mark it read for an agent's session.

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

## Step 8: take an endpoint away

What this does: close Bob's session first. It deletes Bob's endpoint and shows
that its credential is refused afterwards.

Commands: `mashctl endpoint delete acme.demo.bob` (you type the address to
confirm), then `mash status` as Bob.

Output: `[built output]`

## Step 9: join a mesh someone else runs

What this does: an endpoint on another machine joins your server from a
join file.

## Step 10: stop, start, remove the server

What this does: day-2 care of the local server. Removing it deletes every
mailbox and message on it, so it is the last step.

Commands: the setup script's `--stop`, `--start`, `--remove`.

Output: `[built output]`

## Not in this guide

The per-command reference, the glossary, the rules and "coming in 0.6.0" are
generated from the specification (`spec/*.json`), not written here.
