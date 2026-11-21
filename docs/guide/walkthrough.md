# Walkthrough: two agents, one mail mesh

This page takes you from nothing to two Claude Code sessions mailing each
other. Each step is one heading, with "What this does", the command and its
output. Every output marked "built output" is pasted from a real run of
the built programs, never typed; that run is a container that follows this
page exactly.

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
  that endpoint. A creds file is one text file holding a user JWT and its
  seed.
- A **join file** is one text file that lets a machine use an endpoint: the
  server address, the certificate authority and the creds.
- **MCP** (the Model Context Protocol) is how an agent host such as Claude
  Code talks to a tool server. mash offers its commands to a session as MCP
  tools.
- A **mesh** is the set of endpoints on one server.

Alice and Bob are the two agents in every example. The picture of how the
parts fit is in [stack.md](stack.md); how mash differs from plain Synadia
Agent Protocol (SAP) is in [mash-vs-sap.md](mash-vs-sap.md).

## Before you start

What this does: lists what must be on the machine before step 1.

- A machine with Linux or macOS and Claude Code installed.
- `curl`, `tar`, `unzip`, `openssl`, `jq` (step 8 only; it reads JSON and
  picks out a field), `nats-server` 2.10.7 or newer and the `nats`
  command. On Debian or Ubuntu a minimal image also needs `iproute2` (the
  setup script uses `ss`).
- About 2 GB free and nothing listening on 127.0.0.1:4222, or the next free
  port is used.

`mash-setup` is a Claude Code plugin of two skills that set mash up and look
after it; the setup skill does all of this for you and asks before each
step. This page is the same path by hand.

## Step 1: plan the names

What this does: nothing on the machine. It picks the names, so the rest is
copy and paste. The owner name goes into the server in step 3 and cannot
change later without starting over.

- owner = `acme`: one namespace, any name within mash's naming limits
- group = `demo`: endpoints you keep together
- names = `bob` and `alice`: one endpoint each, for one agent or tool
- address = `acme.demo.bob`, always `owner.group.name`

We run everything on one host with a local nats-server, which step 3 makes
for you. mash needs a NATS server with TLS, JetStream (the NATS feature that
stores messages on the server) and sign-in credentials; yours may do
instead (see the NATS notes in `docs/`).

## Step 2: install mash and mashctl

What this does: downloads the release, checks it against its checksums and
puts `mash` and `mashctl` in a folder; if it is not on your PATH, the
script says so.

What is created: two programs in `~/.local/bin`, the default;
`MASH_INSTALL_DIR` names another folder.

Command: `install.sh` from the release (never piped into a shell: download,
check against `SHA256SUMS`, read it, run it).

<!-- output: install -->
Output: `[built output: install]`

Check: `mash version` and `mashctl version` print the same version.

## Step 3: make a NATS server on this machine

What this does: makes a local NATS server for mash: TLS, JetStream, sign-in
with credentials, listening on this machine only, run as you.

What is created: `~/mash-nats` (keys, certificates, the server's files) and
`mash-setup.yaml`. `~/mash-nats` is the default; `--dir` names another folder,
and the last line the script prints has the path it actually wrote.

Command: `mash-local-nats-setup.sh --owner acme`, downloaded from the same
release and checked against `SHA256SUMS` like `install.sh`. Then the line it
prints last, `mashctl init --config <path>/mash-setup.yaml`.

`mashctl init` reads the setup file and keeps its own copy of the keys in
`~/.config/mash`. That folder is where `mashctl` and `mash` look for their
configuration and credentials. It is the default: `MASH_CONFIG_DIR` (an
absolute path) names another folder, and so does an absolute
`XDG_CONFIG_HOME`, as `docs/install.md` describes.

<!-- output: server -->
Output: `[built output: server]`

The server is a program you run as yourself; step 11 shows `--start` for
after a reboot. If port 4222 is taken, the next free one is used.

## Step 4: make the two endpoints

What this does: `mashctl endpoint create` makes a key pair for each
endpoint, has the account sign a statement (the user JWT) that this public
key is `acme.demo.bob`, and writes a join file that holds the endpoint's
credentials. `mash join` then installs that file: it puts the creds file
and the server's address in `~/.config/mash`. Whoever holds a creds file is
that endpoint, so keep the join file private; you can delete it once the
endpoint has joined.

What is created: a join file per endpoint (written with mode 600) in the
current folder, then a creds file per endpoint under `~/.config/mash`, and
each endpoint's inbox on the server. An endpoint's credentials file lives
only on the endpoint's host, at mode 0600 (design, section 4).

Commands:

```
mashctl endpoint create acme.demo.bob --out acme.demo.bob.join
mash join acme.demo.bob.join

mashctl endpoint create acme.demo.alice --out acme.demo.alice.join
mash join acme.demo.alice.join
```

<!-- output: endpoints -->
```
Created endpoint acme.demo.bob
Join file: acme.demo.bob.join
Joined as acme.demo.bob.
Created endpoint acme.demo.alice
Join file: acme.demo.alice.join
Joined as acme.demo.alice.
```

Verify, one line each. `MASH_ENDPOINT` names which endpoint `mash` acts as;
it is how one machine plays two agents:

```
MASH_ENDPOINT=acme.demo.bob mash status
MASH_ENDPOINT=acme.demo.alice mash status
```

Ready means the configuration is readable, the server answers, and it
accepts this endpoint's credentials. Offline is normal here: no agent
session is holding the endpoint open yet.

<!-- output: status -->
```
acme.demo.bob: ready, offline
acme.demo.alice: ready, offline
```

## Step 5: see who is there

What this does: lists the endpoints you can see and which are online. It is
the directory: unlike a list of live agents, it includes endpoints that are
offline, because their mail waits for them.

What is created: nothing.

```
MASH_ENDPOINT=acme.demo.bob mash who
```

<!-- output: who -->
```
ADDRESS          STATE
acme.demo.alice  offline
acme.demo.bob    offline
```

## Step 6: send mail

What this does: shows the mail flow by hand on the command line. Normally
the agent talks to mash through MCP (see the words at the top) and you
never type this.

What is created: nothing on disk; mail goes over NATS, kept in the
receiver's inbox until read, whether it is online or not.

Commands, Bob to Alice:

```
MASH_ENDPOINT=acme.demo.bob mash send acme.demo.alice "lunch at noon?"
MASH_ENDPOINT=acme.demo.bob mash send acme.demo.alice --urgent \
  --kind question "ready?"
```

<!-- output: send -->
```
Mail sent (number 1).
Urgent question mail sent (number 2).
```

Alice reads:

```
MASH_ENDPOINT=acme.demo.alice mash inbox
MASH_ENDPOINT=acme.demo.alice mash read 2
```

<!-- output: inbox -->
```
2 messages for acme.demo.alice, 2 new, 1 urgent.

NUMBER  FROM  WHEN     STATE  URGENT  KIND      MESSAGE
1       bob   10s ago  new    no      task      lunch at noon?
2       bob   8s ago   new    yes     question  ready?
```

<!-- output: read -->
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
means Bob's own key did. By default only mail whose trust is `verified`
is delivered. Mail that is unsigned, signed by an unknown key, claimed but
unproven, or stale is dropped and counted; `mash doctor` shows the count.
The sender is not told. `--accept-unverified`, an option of `mash serve`,
delivers it instead, labelled.

`--kind` says what the mail is: `blocker` (you cannot continue without the
recipient), `question` (you need an answer), `task` (you are asking them to
do something; the default), `result` (the outcome of something they asked
for) and `fyi` (information, no answer needed). `blocker`, `question` and
`task` wake an idle recipient; `result` and `fyi` wait for its next turn.
`--urgent` delivers at once, into a running turn where the agent host
allows it (step 7).

## Step 7: use it from Claude Code

What this does: two Claude Code sessions, one as Bob and one as Alice, mail
each other through the mash plugin. The plugin gives each session these
MCP tools, one for each everyday mash command:

- `send`: send mail to another endpoint by its address, owner.group.name.
- `inbox`: list the messages this endpoint has received.
- `read`: show one message in full.
- `who`: list the endpoints you can see, and which are online.
- `status`: say in one line whether mail works for this endpoint, and
  whether it is online.
- `doctor`: check everything mail depends on for this endpoint, and say how
  to fix what is wrong. Changes nothing.
- `version`: print the version.
- `reply`: answer a live prompt.

The plugin also runs `mash hook`, which prints the mail waiting for the
session. Claude Code runs it after each tool call and with each prompt. An
idle session is woken by one line saying mail is waiting; the mail itself
arrives through `mash hook`. The plugin's launcher, at session start, says
something only when the mash program is not installed.

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

Claude Code registers a plugin's channel only when it is launched with its
own flag, `--dangerously-load-development-channels plugin:mash@mash`, and it
asks the person to confirm at each launch. Without the flag nothing is lost
but the wake: mail still arrives with the next prompt or tool call through
`mash hook`. The flag is Claude Code's, not mash's.

The session's own status. In either session, ask: "Run the mash status
tool." Claude calls it and reports what the tool says: whether mail works for
this endpoint and whether it is online. It is the same check as `mash
status` at a shell, but made from inside the session, so it is online:
`acme.demo.bob: ready, online`.

Now mail. In Bob's session: "Send acme.demo.alice a note: lunch at noon?".
A note is kind `task`, which wakes Alice's session if it is idle; ask
for kind `result` or `fyi` instead and it waits for her next turn. Add
"urgent" only for what cannot wait: it is delivered at once, into a running
turn where the agent host allows it. In Alice's session the mail arrives
through `mash hook`, and Claude shows it as mail from `acme.demo.bob`,
labelled `[verified]`.
Alice's session answers with `send`, and Bob's session sees the answer.
Mail you sent in step 6 is waiting too: it arrives the same way.

Output: `[built output: claude-code]` (a transcript of the two sessions)

## Step 8: JSON, for scripts (optional)

What this does: for a script or tool that is not an agent. `--json` is an
option of every `mash` command; it prints the same object the matching MCP
tool returns, so a script gets the whole message with its fields. `mashctl`
commands take `--json` too and print their result as JSON. Nothing else
changes: the same mail, the same inbox.

What is created: nothing. Reading a message at a shell shows it; it does not
mark it read for an agent's session.

Send, and keep the number:

```
MASH_ENDPOINT=acme.demo.bob mash send acme.demo.alice --json "ping"
```

<!-- output: json-send -->
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

<!-- output: json-read -->
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

<!-- output: json-inbox -->
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

The one-line retrieval, with `jq` (a command-line tool that picks a field
out of JSON), is the text of one message:

```
MASH_ENDPOINT=acme.demo.alice mash read 3 --json | jq -r .body
```

<!-- output: json-jq -->
Output: `ping`

(The JSON above is the spec's example, shown with Alice and Bob. The
built-output pass replaces it with the real run's.)

## Step 9: take an endpoint away

What this does: close Bob's session first. It deletes Bob's endpoint, and
Bob's credentials stop working: on this machine the creds file is removed too,
so `mash status` says it is missing. On another machine that still holds a
copy, the server refuses the credentials.

Commands: `mashctl endpoint delete acme.demo.bob` (you type the address to
confirm), then `mash status` as Bob.

<!-- output: delete -->
Output: `[built output: delete]`

Then Bob's status. The endpoint is gone, and the command exits with a
non-zero status:

<!-- output: refused -->
Output: `[built output: refused]`

## Step 10: join a mesh someone else runs

What this does: an endpoint on another machine joins a mesh that someone
else runs, from a join file. Nothing here needs the server's own files.

Read this first: the server from step 3 can be reached only from the machine
it runs on (the setup script "makes a server that only this machine can
reach, with encryption on"), and the join file carries that server's
address. So a join file from the step 3 server works only on that machine.
Joining from another machine needs a server that machine can reach, with a
certificate that covers the address it uses. The specification does not
describe how to change the step 3 server to do that. [to confirm with the
planner]

<!-- not run by the AT-3 harness -->

What is created: on the joining machine, the creds file and the server's
address in the configuration folder, as in step 4.

On the machine of the person who runs the server, make the endpoint and its
join file:

```
mashctl endpoint create acme.demo.carol --out acme.demo.carol.join
```

Send `acme.demo.carol.join` to the joining machine by a route you trust.
It holds the endpoint's credentials, so treat it like a password.

If the private key must never leave the joining machine, make the key
there and give only the public half to whoever runs the server:

```
mash key create --out carol.seed
```

That writes `carol.seed` and `carol.seed.pub`. The server's owner runs:

```
mashctl endpoint create acme.demo.carol --out acme.demo.carol.join \
  --public-key carol.seed.pub
```

On the joining machine, install the join file (with `--key carol.seed` if
you made your own key):

```
mash join acme.demo.carol.join
```

Then `MASH_ENDPOINT=acme.demo.carol mash status` shows whether it works,
as in step 4.

## Step 11: stop, start, remove the server

What this does: day-2 care of the local server. Removing it deletes every
mailbox and message on it, so it is the last step.

Commands: the setup script's `--stop`, `--start`, `--remove`.

<!-- output: lifecycle -->
Output: `[built output: lifecycle]`

## Not in this guide

The per-command reference, the glossary, the rules and "coming in 0.6.0" are
generated from the specification (`spec/*.json`), not written here.
