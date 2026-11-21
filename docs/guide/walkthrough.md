# Walkthrough: two agents, one mail mesh

This page takes you from nothing to two Claude Code sessions mailing each
other. Each step is one heading, with "What this does", the command and its
output. Every output marked "built output" is pasted from a real run of
the built programs, never typed; that run is a container that follows this
page exactly.

## Quick start

Two agents mailing each other from a shell, on one machine, in a few minutes.
Every command below was run on the published v0.5.6 release. The steps after
this section explain each one and show its output.

Everything is installed as you, in your home folder. Nothing here needs
`sudo`, a system service or a package manager. You need Linux or macOS, Claude
Code, and `curl`, `tar` and `openssl`.

### The way to do it: let an agent set it up

Install the `mash-setup` plugin and ask your session to set mash up. It asks
before each step.

```sh
claude plugin marketplace add bogworks/mash
claude plugin install mash-setup@mash
```

Then tell the session: "set mash up on this machine"
([install.md](../install.md#2-the-plugin)).

### By hand

1. Install the programs. Work in an empty scratch folder (`cd "$(mktemp -d)"`):
   the downloads land in the folder you are in. First `nats-server` and the
   `nats` command, each from its GitHub release, checked against that
   release's checksums and put in `~/.local/bin` (`nats-server` 2.10.7 or
   newer; the `nats` zip is opened with `unzip`, or with `python3` if there is no `unzip`):

   ```sh
   install -d -m 755 ~/.local ~/.local/bin
   OS=$(uname -s | tr A-Z a-z); ARCH=$(uname -m); case $ARCH in x86_64) ARCH=amd64;; aarch64|arm64) ARCH=arm64;; esac
   V=v2.15.1; B=https://github.com/nats-io/nats-server/releases/download/$V
   curl -fsSLO $B/nats-server-$V-$OS-$ARCH.tar.gz -O $B/SHA256SUMS
   grep " nats-server-$V-$OS-$ARCH.tar.gz\$" SHA256SUMS | sha256sum -c -      # macOS: shasum -a 256 -c -
   tar -xzf nats-server-$V-$OS-$ARCH.tar.gz --strip-components=1 -C ~/.local/bin nats-server-$V-$OS-$ARCH/nats-server
   N=0.5.2; B=https://github.com/nats-io/natscli/releases/download/v$N
   curl -fsSLO $B/nats-$N-$OS-$ARCH.zip -O $B/SHA256SUMS
   grep " nats-$N-$OS-$ARCH.zip\$" SHA256SUMS | sha256sum -c -                # macOS: shasum -a 256 -c -
   if command -v unzip >/dev/null; then unzip -j -o nats-$N-$OS-$ARCH.zip '*/nats' -d ~/.local/bin
   else python3 -m zipfile -e nats-$N-$OS-$ARCH.zip natsx && install -m 755 natsx/*/nats ~/.local/bin/nats; fi
   ```

   (The second `SHA256SUMS` replaces the first, after the first has been
   used.) Then `mash` and `mashctl`: download the installer and its
   checksums, check, run:

   ```sh
   BASE=https://github.com/bogworks/mash/releases/latest/download
   curl -fsSLO $BASE/install.sh -O $BASE/SHA256SUMS
   grep ' install.sh$' SHA256SUMS | sha256sum -c -    # macOS: shasum -a 256 -c -
   bash install.sh
   ```

   It prints `Installed mash v0.5.6 (dd696e9) in ~/.local/bin`, and the same
   for `mashctl`. With a current `gh` installed it also checks the release's
   attestation first and installs nothing if that fails; it says nothing when
   the check passes ([how to check by hand](../install.md#verify-what-you-download)).
   If it ends with `~/.local/bin is not on your PATH`, add the folder and open a
   new terminal (on Debian a new login shell does it for you once the folder exists):

   ```sh
   echo 'export PATH="$HOME/.local/bin:$PATH"' >> ~/.profile   # zsh: ~/.zshrc
   ```

   The mash plugin also needs `mash` on the PATH that Claude Code starts with.

2. Make a NATS server that only this machine can reach (download, check, run):

   ```sh
   BASE=https://github.com/bogworks/mash/releases/latest/download
   curl -fsSLO $BASE/mash-local-nats-setup.sh -O $BASE/SHA256SUMS
   grep ' mash-local-nats-setup.sh$' SHA256SUMS | sha256sum -c -    # macOS: shasum -a 256 -c -
   bash mash-local-nats-setup.sh --owner alice
   mashctl init --config ~/mash-nats/mash-setup.yaml     # the last line the script printed
   ```

   `--owner alice` names the owner (the first part of every address). Without
   it the script asks on a terminal, and stops with `no terminal to ask for the
   owner name` when there is none. The server starts now; it does not start at
   boot, so after a reboot run `bash mash-local-nats-setup.sh --start`.

3. Make two agents. The group (`dev`) has no default: you always name it.

   ```sh
   mashctl endpoint create alice.dev.design
   mashctl endpoint create alice.dev.review
   ```

   With no option this makes each agent's key on this machine and installs it
   here, ready to use. To give an agent to another machine, write a join file
   and install it there ([step 10](#step-10-join-a-mesh-someone-else-runs)).

4. Send mail from a shell and read it:

   ```sh
   MASH_ENDPOINT=alice.dev.design mash send alice.dev.review "hello"     # Mail sent (number 1).
   MASH_ENDPOINT=alice.dev.review mash inbox
   MASH_ENDPOINT=alice.dev.review mash read 1
   MASH_ENDPOINT=alice.dev.review mash status                            # alice.dev.review: ready, offline
   ```

   The message still shows as `new` in the inbox after `mash read` while no
   session runs ([#866](https://github.com/bogworks/mash/issues/866)). With
   two endpoints installed on one machine, say which one you mean:
   `mash who --endpoint alice.dev.design`, `mash doctor --endpoint alice.dev.review`.
   `mash status` says `ready, online` while a session holds the endpoint.

5. Install the plugin and start two sessions:

   ```sh
   claude plugin marketplace add bogworks/mash
   claude plugin install mash@mash
   MASH_ENDPOINT=alice.dev.design claude   # terminal 1
   MASH_ENDPOINT=alice.dev.review claude   # terminal 2
   ```

   Mail arrives with the session's next prompt or tool call. An idle session
   is not woken; see "Optional: wake an idle session" below.

   Not yet run on 0.5.6: the two live sessions. [Step 7](#step-7-use-it-from-claude-code)
   describes them.

### Optional: wake an idle session

You do not need this. It is a choice. Without it, a session that is sitting
idle sees new mail the next time you type a prompt or it runs a tool. To wake
an idle session as soon as mail arrives, start it with Claude Code's own
research-preview flag, `--dangerously-load-development-channels`:

```sh
MASH_ENDPOINT=alice.dev.design claude --dangerously-load-development-channels plugin:mash@mash
```

The flag lets a development channel plugin push a message into a session
without being on Claude Code's approved-channels list. Claude Code asks you to
confirm it at each launch. It is Claude Code's flag, not mash's
([more in install.md](../install.md#3-optional-wake-an-idle-session)).

### Where things live and how to remove them

- `~/.local/bin`: `mash`, `mashctl`, `nats-server` and `nats`.
- `~/.config/mash`: each endpoint's key and config.
- `~/mash-nats`: the local NATS server, its data and every message.

[Uninstall](../install.md#uninstall) lists how to remove all of it.

If something goes wrong, see [troubleshooting](../troubleshooting.md).

## What you will build

By the end, two AI agents, Alice and Bob, send each other mail. Each agent
is a Claude Code session; each has an inbox on a message server that keeps
its mail until it is read, even while the agent is off.

Words used here, each explained again where it first matters (the full list is the [glossary](../reference/glossary.md)):

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
- A **mesh** is every endpoint on one server, and all their mail. In 0.5.6
  a mesh has one owner.

Alice and Bob are the two agents in every example. The picture of how the
parts fit is in [stack.md](stack.md); how mash differs from plain Synadia
Agent Protocol (SAP) is in [mash-vs-sap.md](mash-vs-sap.md).

## Before you start

What this does: lists what must be on the machine before step 1.

- A machine with Linux or macOS and Claude Code installed.
- `curl`, `tar`, `openssl`, `jq` (step 8 only; it reads JSON and
  picks out a field), `nats-server` 2.10.7 or newer and the `nats`
  command (the Quick start downloads both into `~/.local/bin`, checked; no
  `sudo` or package manager). On Debian or Ubuntu a minimal image also needs
  `iproute2` (the setup script uses `ss`).
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
stores messages on the server) and sign-in credentials. A server you run
yourself works too: [operations.md](../operations.md#setting-mash-up-with-mashctl-init---config)
shows how `mashctl init` takes it from a setup file.

## Step 2: install mash and mashctl

What this does: downloads the release, checks it against its checksums and
puts `mash` and `mashctl` in a folder; if it is not on your PATH, the
script says so.

What is created: two programs in `~/.local/bin`, the default;
`MASH_INSTALL_DIR` names another folder.

Command: `install.sh` from the release: download it, check it against
`SHA256SUMS`, run it. Nothing is piped into a shell.

```sh
BASE=https://github.com/bogworks/mash/releases/latest/download
curl -fsSLO $BASE/install.sh -O $BASE/SHA256SUMS
grep ' install.sh$' SHA256SUMS | sha256sum -c -    # macOS: shasum -a 256 -c -
bash install.sh
```

[install.md](../install.md#1-the-binary) explains it, and
[how to verify what you download](../install.md#verify-what-you-download)
shows how to read it first. With a current `gh` the installer also checks the
release's attestation; without one it says so and trusts only github.com's TLS.

<!-- output: install -->
Output:

```
Fetching mash and mashctl from the v0.5.6 release: https://github.com/bogworks/mash/releases/download/v0.5.6
Installed mash v0.5.6 (dd696e9) in ~/.local/bin
Installed mashctl v0.5.6 (dd696e9) in ~/.local/bin
```

Check: `mash version` and `mashctl version` print the same version.

## Step 3: make a NATS server on this machine

What this does: makes a local NATS server for mash: TLS, JetStream, sign-in
with credentials, listening on this machine only, run as you.

What is created: `~/mash-nats` (keys, certificates, the server's files) and
`mash-setup.yaml`. `~/mash-nats` is the default; `--dir` names another folder,
and the last line the script prints has the path it actually wrote.

Command: `bash mash-local-nats-setup.sh --owner acme`, downloaded from the same
release and checked against `SHA256SUMS` like `install.sh` (see
[install.md](../install.md#verify-what-you-download)). Then the line it
prints last, `mashctl init --config <path>/mash-setup.yaml`.

`mashctl init` reads the setup file and keeps its own copy of the keys in
`~/.config/mash`. That folder is where `mashctl` and `mash` look for their
configuration and credentials. It is the default: `MASH_CONFIG_DIR` (an
absolute path) names another folder, and so does an absolute
`XDG_CONFIG_HOME`, as [install.md](../install.md#4-configuration) describes.

With a server made by the setup script, that folder holds the operator's
identity key (the `nats` command signs accounts only with it), with which the
operator itself can be re-issued, and the account signing key. They are
protected only as your own files are. This is the accepted same-user custody
risk ([#869](https://github.com/bogworks/mash/issues/869)).

<!-- output: server -->
Output (the process id differs on your machine, and the path is where the script wrote its files):

```
mash-local-nats-setup.sh: OK
checked: nats-server 2.10.27, nats 0.5.2, openssl
port: 4222
made: CA, certificate, operator, accounts, users
started: nats-server pid 1523 on 127.0.0.1:4222
checked: sign-in and system ping ok
mashctl init --config /home/tester/mash-nats/mash-setup.yaml
```

The first line is the checksum check of the downloaded script. Run the last
line it printed:

```
mashctl init --config /home/tester/mash-nats/mash-setup.yaml
Initialized mash on tls://127.0.0.1:4222 for owner acme
```

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
only on the endpoint's host, at mode 0600.

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
MASH_ENDPOINT=acme.demo.bob claude
```

```
MASH_ENDPOINT=acme.demo.alice claude
```

Mail arrives with the session's next prompt or tool call, through `mash hook`.
An idle session is not woken. If you want that, see
[Optional: wake an idle session](#optional-wake-an-idle-session): it uses
Claude Code's own research-preview flag, and the choice is yours.

The session's own status. In either session, ask: "Run the mash status
tool." Claude calls it and reports what the tool says: whether mail works for
this endpoint and whether it is online. It is the same check as `mash
status` at a shell, but made from inside the session, so it is online:
`acme.demo.bob: ready, online`.

Now mail. In Bob's session: "Send acme.demo.alice a note: lunch at noon?".
A note is kind `task`, which wakes Alice's session if it is idle and was
started with the optional wake flag; ask
for kind `result` or `fyi` instead and it waits for her next turn. Add
"urgent" only for what cannot wait: it is delivered at once, into a running
turn where the agent host allows it. In Alice's session the mail arrives
through `mash hook`, and Claude shows it as mail from `acme.demo.bob`,
labelled `[verified]`.
Alice's session answers with `send`, and Bob's session sees the answer.
Mail you sent in step 6 is waiting too: it arrives the same way.

Output: not yet run on 0.5.6. The two Claude Code sessions need a live model and a login, which the test container does not have; this transcript will be added once they have been run on the published release.

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
{"to":"acme.demo.alice","number":3,"kind":"task","urgent":false,"duplicate":false,"retagged":false}
```

Each command prints its JSON on one line. Pipe it through `| jq` to
pretty-print it.

Alice reads that one message whole:

```
MASH_ENDPOINT=acme.demo.alice mash read 3 --json
```

<!-- output: json-read -->
```json
{"number":3,"from":"acme.demo.bob","sent":"2026-11-21T14:33:54Z","trust":"verified","kind":"task","urgent":false,"reply_to":null,"relay":null,"body":"ping"}
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
{"endpoint":"acme.demo.alice","total":3,"new":3,"urgent":1,"wake_count":15,"over_wake_count":false,"wake_age":"1h","oldest_waiting":"0s","over_wake_age":false,"messages":[{"number":1,"from":"acme.demo.bob","sent":"2026-11-21T14:33:54Z","state":"new","urgent":false,"kind":"task","trust":"verified","preview":"lunch at noon?"},{"number":2,"from":"acme.demo.bob","sent":"2026-11-21T14:33:54Z","state":"new","urgent":true,"kind":"question","trust":"verified","preview":"ready?"},{"number":3,"from":"acme.demo.bob","sent":"2026-11-21T14:33:54Z","state":"new","urgent":false,"kind":"task","trust":"verified","preview":"ping"}]}
```

The one-line retrieval, with `jq` (a command-line tool that picks a field
out of JSON), is the text of one message:

```
MASH_ENDPOINT=acme.demo.alice mash read 3 --json | jq -r .body
```

<!-- output: json-jq -->
Output: `ping`

## Step 9: take an endpoint away

What this does: close Bob's session first. It deletes Bob's endpoint, and
Bob's credentials stop working: on this machine `mash status` says the
endpoint was deleted and its address cannot be used again. On another machine
that still holds a copy, the server refuses the credentials.

Commands: `mashctl endpoint delete acme.demo.bob` (you type the address to
confirm), then `mash status` as Bob.

<!-- output: delete -->
Output:

```
This deletes acme.demo.bob and its empty inbox. It cannot be undone.
Type the address to confirm: acme.demo.bob
Deleted endpoint acme.demo.bob
```

Then Bob's status. The endpoint is gone, and the command exits with a
non-zero status:

<!-- output: refused -->
Output:

```
acme.demo.bob: not ready: this endpoint was deleted; its address cannot be used again
hint: mash doctor
```

## Step 10: join a mesh someone else runs

What this does: an endpoint joins a mesh from a join file, as in step 4. With
the server from step 3 that is its whole scope: a join file made against it
works only on this machine. Two machines need a server both can reach, with a
certificate that covers its address; in 0.5.6 that is a NATS server you run
yourself, which `mashctl init` takes from a setup file (see
[operations.md](../operations.md#setting-mash-up-with-mashctl-init---config)). A script option for a reachable address and
certificate is for 0.6.0.

<!-- not run in the built-output pass -->

What is created: on the joining side, the creds file and the server's address
in the configuration folder, as in step 4.

The person who runs the server makes the endpoint and its join file:

```
mashctl endpoint create acme.demo.carol --out acme.demo.carol.join
```

Hand `acme.demo.carol.join` over by a route you trust.
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

Commands, from the folder that holds the downloaded script:

```
bash mash-local-nats-setup.sh --stop
bash mash-local-nats-setup.sh --start
bash mash-local-nats-setup.sh --remove
```

`--start` starts an existing server again (after a reboot) and creates
nothing. `--remove` asks you to type the folder's path before it deletes
anything.

<!-- output: lifecycle -->
Output (the process ids differ on your machine):

```
stopped pid 2336
started: nats-server pid 2572 on 127.0.0.1:4222
type the folder to remove (/home/tester/mash-nats): /home/tester/mash-nats
stopped pid 2572
removed /home/tester/mash-nats
note: mashctl's folder /home/tester/.config/mash still points at this server.
To set up again, run mashctl init --config <file> --config-dir <new folder>
or move that folder aside.
```

## Not in this guide

The per-command reference, the glossary, the rules and "coming in 0.6.0" are
generated from the specification (`spec/*.json`), not written here. Start at
the [reference index](../reference/README.md); the words used in this guide
are in the [glossary](../reference/glossary.md), and what is planned for the
next release is in [coming in 0.6.0](../reference/coming-in-0-6-0.md). To
remove mash from a machine, see [Uninstall](../install.md#uninstall).
