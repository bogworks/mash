# README quick start, draft for 0.5.6 (written to the target outputs)

> NOTE TO THE READER OF THIS DRAFT (not README text): everything between the
> two horizontal rules below is the proposed README quick start. The live
> README describes the frozen v0.5.5 and is not edited.
>
> DEPENDENCY: the NATS setup skill/script from the policy work,
> docs/plans/2026-11-19-nats-skill.md. The quick start sends the reader to
> docs/nats-setup.md, which that work writes.
>
> Output blocks are the After outputs of
> docs/plans/2026-11-19-cli-targets-mash.md with the names swapped to alice
> and bob where the target used bob only. Where the draft departs from a
> target (group count, who rows, version) the report that came with this
> draft says so.

---

## Quick start: two agents on one machine

We will set up mash between two agents on one machine. Here is what you
will have at the end:

```
  host: myserver
 +-----------------------------------------------------------+
 |                                                           |
 |   mash server (NATS)                                      |
 |   +---------------------------------------------------+   |
 |   |  group: demo                                      |   |
 |   |                                                   |   |
 |   |   +-------------+   mail   +-------------+        |   |
 |   |   |    alice    | <------> |     bob     |        |   |
 |   |   +-------------+          +-------------+        |   |
 |   |   acme.demo.alice          acme.demo.bob          |   |
 |   +---------------------------------------------------+   |
 |                                                           |
 +-----------------------------------------------------------+
```

One host (`myserver`), one group (`demo`), two agents (`alice` and `bob`).
Each agent has a mailbox on the server and can mail the other.

An agent's path is `acme/demo/alice`: owner / group / agent, which mash
shows with dots, `acme.demo.alice`. `acme` is who owns it, `demo` is the
group of agents that can see each other, `alice` is this agent.

### Before you start

You need:

- A Linux or macOS machine; the examples call it `myserver`.
- A Go 1.27.4 or newer toolchain, for `mashctl`. `go install` writes to
  `go env GOBIN`, or `$(go env GOPATH)/bin` when that is empty (`~/go/bin`
  by default); that directory must be on your `PATH`.
- Claude Code with channels (a claude.ai or Console sign-in; not Bedrock,
  Agent Platform or Foundry), for step 9 only.
- A mash server. `mashctl` does not install, configure or start the NATS
  server. Set up a mash server first: follow the NATS setup guide
  ([docs/nats-setup.md](docs/nats-setup.md)). It ends with a server running
  and `mashctl` able to reach it.

If `MASH_URL`, `MASH_OWNER` or other `MASH_*` variables are set from
another setup, clear them first:

```sh
unset $(env | sed -n 's/^\(MASH_[A-Z_]*\)=.*/\1/p')
```

They override the config files, and the commands below would go to that
other server.

Every command below is shown as you type it, then what it prints, then
what to run next. Nothing is cleared: scroll back at any time.

### Step 1-A: Install mash (command 1 of 2)

`mash` is what each agent runs to send and read mail. The release binary
is the build the project's release workflow made from a tag, and the
commands check that. The 0.5.6 release is not published yet; until it is,
`<version>` below has no download behind it.

```sh
BASE=https://github.com/bogworks/mash/releases/download/<version>
curl -fsSLO $BASE/mash-linux-amd64
curl -fsSLO $BASE/SHA256SUMS
grep ' mash-linux-amd64$' SHA256SUMS | sha256sum -c - &&
# macOS: use  shasum -a 256 -c -  instead of  sha256sum -c -
gh attestation verify mash-linux-amd64 -R bogworks/mash \
  --signer-workflow bogworks/mash/.github/workflows/release.yml \
  --source-ref refs/tags/<version> &&
chmod +x mash-linux-amd64 &&
BIN=~/.local/bin &&   # any directory on your PATH
mkdir -p "$BIN" && mv mash-linux-amd64 "$BIN/mash"
case ":$PATH:" in *":$BIN:"*) ;; *) echo "add $BIN to your PATH" ;; esac
```

Other platforms: `mash-linux-arm64`, `mash-darwin-amd64`,
`mash-darwin-arm64`. The checksum shows the file arrived whole; the
attestation, a record signed by GitHub, shows it was built from the
`<version>` tag by the release workflow (more: docs/release.md). `gh` is
the [GitHub CLI](https://cli.github.com/).

Check it:

```
$ mash version
mash <version>
```

NEXT: Install `mashctl`, the server's tool, with `go install` (you, on
myserver).

### Step 1-B: Install mashctl (command 2 of 2)

`mashctl` is what the person who runs the mash server uses to create
endpoints. Here that is you, on `myserver`. It is not among the release
assets, so you build it with Go.

```sh
go install github.com/bogworks/mash/cmd/mashctl@<version>
```

NEXT: Make Alice's key with `mash key create` (you, on myserver, where
Alice's agent will run).

### Step 2: Make a key for Alice (command 1 of 3)

Alice needs a pair of keys to prove who she is on the mail system. The
private key never leaves Alice's machine; the public key is what you hand
to the server. Steps 2, 3 and 4 are the whole sign-up for one agent: make
a key, have the server create the endpoint, install the credentials.

```
$ mash key create --out alice.key
Made a private key for alice (NATS calls it a seed).
  private key  alice.key  keep this on this machine
  public key   alice.pub  share this one

NEXT: Send alice.pub to whoever runs your mash server (you, if that is this
machine) and run: mashctl endpoint create acme/demo/alice --public-key alice.pub
```

NEXT: Create Alice's endpoint on the server with `mashctl endpoint create`
(whoever runs the mash server: you, on myserver).

### Step 3: Create Alice's endpoint on the server (command 2 of 3)

An endpoint is Alice's mailbox on the server, at the path `acme/demo/alice`.
The server signs a statement that `alice.pub` belongs to it. That statement
is a token: it holds no secret, so it is safe to carry around.

```
$ mashctl endpoint create acme/demo/alice --public-key alice.pub
Created endpoint acme.demo.alice.
Wrote acme.demo.alice.token: your server's signed statement that alice.pub is
this endpoint. It holds no secret.

NEXT: Take acme.demo.alice.token to the agent's machine and run: mash
credential install --key alice.key --token acme.demo.alice.token
```

NEXT: Install Alice's credentials with `mash credential install` (you, on
Alice's machine; here the same machine).

### Step 4: Install Alice's credentials (command 3 of 3)

Credentials are the one file mash signs in with: Alice's private key plus
the server's token. This step joins the two and puts the result in mash's
config directory. It also writes down where the server is and which CA
(certificate authority) mash trusts, so you can see all of it: `--server`
is the server's address, and `--ca` is the `ca.pem` file from the NATS setup
guide (the server proves itself with a certificate; mash will only talk to a
server that this CA signed; the file holds no secret).

```
$ mash credential install --key alice.key --token acme.demo.alice.token \
    --server tls://myserver:4222 --ca ca.pem
Installed credentials for acme.demo.alice.
They combine alice.key and the token; mash uses them to sign in.

  wrote   ~/.config/mash/acme.demo.alice.creds  (mode 600)
  wrote   ~/.config/mash/config.json            (server, owner, CA)
  copied  ca.pem to ~/.config/mash/ca.pem       (mode 644)

NEXT: Start Claude Code as this agent:
      MASH_GROUP=demo MASH_NAME=alice claude
      (or check first: MASH_GROUP=demo MASH_NAME=alice mash status)
```

`config.json` stores the owner too (it comes from the token), so only
`MASH_GROUP` and `MASH_NAME` are needed from here on. This guide checks
first (step 6) and starts Claude Code in step 9, after the plugin is
installed.

NEXT: Make Bob's endpoint the short way, with
`mashctl endpoint create acme/demo/bob --make-key` (you, on myserver).

### Step 5: Make Bob in one command (command 1 of 1)

Steps 2, 3 and 4 were three commands because the key is made on the
agent's machine and only the public half goes to the server. When the agent
and `mashctl` are on the same machine, as here, one command does all three.
It is the wrong form when Bob's machine is another one: then use the three
steps above (docs/operations.md, "An endpoint on another machine").

```
$ mashctl endpoint create acme/demo/bob --make-key
Made a private key for bob.
  wrote  ~/.config/mash/bob.key, bob.pub
Created endpoint acme.demo.bob.
  wrote  ~/.config/mash/acme.demo.bob.token
Installed credentials for acme.demo.bob.
  wrote   ~/.config/mash/acme.demo.bob.creds  (mode 600)
  wrote   ~/.config/mash/config.json          (server, owner, CA)
  copied  ca.pem to ~/.config/mash/ca.pem     (mode 644)

NEXT: Start Claude Code as this agent:
      MASH_GROUP=demo MASH_NAME=bob claude
      (or check first: MASH_GROUP=demo MASH_NAME=bob mash status)
```

No `--server` or `--ca` here: `mashctl` already knows the server and the CA
from the NATS setup, and writes them for `mash` too (the `config.json` and
`ca.pem` lines). Alice's step 4 did the same job with the two flags.

NEXT: Check that Alice can sign in with `mash status` (you, as alice).

### Step 6: Check that Alice can sign in (command 1 of 1)

`mash status` tells you who mash thinks you are, and whether the server
accepts you. The owner (`acme`) and the server are already in
`config.json`; the first command says which group and agent you are acting
as, Alice; it stays in
effect for this shell.

```sh
export MASH_GROUP=demo MASH_NAME=alice
```

```
$ mash status
Checked mash for acme.demo.alice.

  version      mash <version>
  server       reachable, encrypted
  config       valid
  credentials  valid
  group        1 other endpoint in demo, 3 under acme
```

NEXT: Send Bob a message with `mash send` (you, as alice).

### Step 7: Send mail from Alice to Bob and read it (command 1 of 4)

Alice sends two messages; the second one is marked urgent. Then you become
Bob and read them. Mail waits in Bob's mailbox on the server until he reads
it, whether or not he is running.

```
$ mash send acme.demo.bob "lunch at noon?"
Sent your message to acme.demo.bob (message #1 in their inbox).
```

```
$ mash send acme.demo.bob --urgent --kind question "ready?"
Sent your urgent question to acme.demo.bob (message #2 in their inbox).
```

NEXT: Become Bob and list his inbox with `mash inbox` (you, as bob).

Command 3 of 4: now you are Bob.

```sh
export MASH_NAME=bob
```

```
$ mash inbox
2 messages in your inbox (acme.demo.bob).

 #  FROM             TRUST     WHEN        MESSAGE
 1  acme.demo.alice  verified  12 min ago  lunch at noon?
 2  acme.demo.alice  verified  12 min ago  ready? (urgent question)

NEXT: Read one in full with: mash read 2
```

Command 4 of 4: read the urgent one. `verified` means the message was
signed by Alice's own key.

```
$ mash read 2
Message #2 from acme.demo.alice, 12 min ago. Verified: signed by its key.
Urgent question.

> ready?
> (the sender's text is shown quoted, as sent)

NEXT: Answer it with: mash send acme.demo.alice "your answer"
```

NEXT: See who else is here with `mash who` (you, as bob).

### Step 8: See who is here (command 1 of 1)

`mash who` lists the endpoints you can send to. Copy an address from it
into `mash send`.

```
$ mash who
ENDPOINT         STATE    LAST SEEN
acme.demo.alice  online   now
acme.demo.bob    offline  not seen yet
```

NEXT: Add mash to Claude Code with `mash plugin export` (you, on myserver).

### Step 9: Use it from Claude Code

An agent in Claude Code reads and sends mail through the mash plugin; it
does not type these commands. Four short lines:

```sh
mash plugin export ./mash-plugin
claude plugin marketplace add ./mash-plugin
claude plugin install mash@mash
MASH_GROUP=demo MASH_NAME=alice \
  claude --dangerously-load-development-channels plugin:mash@mash
```

Run the last line in one terminal for Alice, and again with
`MASH_NAME=bob` in a second terminal for Bob. Claude Code will ask you to
confirm the `--dangerously-load-development-channels` flag at each launch;
that is expected: mash is not on Claude Code's channel allowlist, and the
flag only lets mash wake a session that is sitting idle (without it, mail
still arrives at the session's next prompt or tool call).

NEXT: When you are done, clean up (you, on myserver).

### Step 10: Clean up

Delete what the steps made. The keys are secrets, so delete them too.

```sh
rm alice.key alice.pub acme.demo.alice.token
rm ~/.config/mash/acme.demo.alice.creds ~/.config/mash/acme.demo.bob.creds
rm ~/.config/mash/bob.key ~/.config/mash/bob.pub
rm ~/.config/mash/acme.demo.bob.token
rm -r ./mash-plugin
```

Stop the server the way the NATS setup guide says; `mashctl` has no stop
command.

---

## What is different from 0.5.5's quick start

- The server is set up first, by the NATS setup guide; `mashctl init
  --local --start` is not part of the quick start.
- Alice and Bob, host `myserver`, group `demo`, are named up front.
- Step 4 takes `--server` and `--ca` and writes `config.json` and the CA
  copy, and prints each file it wrote; only `MASH_GROUP` and `MASH_NAME`
  are set by hand afterwards (`MASH_OWNER` was needed before).
- The three-command endpoint flow is `mash key create --out alice.key`,
  `mashctl endpoint create ... --public-key alice.pub`, `mash credential
  install --key alice.key --token ...` (was `credential create`).
- Every step says why, shows the exact output, then NEXT; nothing clears.
- `mash status` checks the server and the credentials, and prints five
  facts.
- Mail commands print words, a table and relative times, not tab-separated
  lines.
