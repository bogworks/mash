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
> docs/plans/2026-11-19-cli-targets-mash.md (DRAFT2: rules R9 and R10 apply)
> with the names swapped to alice and bob where the target used bob only.
> Where the draft departs from a target it says so: the group line counts
> two endpoints under acme (only alice and bob exist here, the target's cast
> has a third); at step 8 both are offline (no Claude Code session runs as
> either until step 9); the version is `<version>`.

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

One host (`myserver`), one group (`demo`), two endpoints (`alice` and `bob`).
Each has a mailbox on the server and can mail the other.

An endpoint's address is `acme.demo.alice`: owner, group and name. `acme` is
who owns it, `demo` is the group of endpoints that can see each other,
`alice` is this endpoint's name.

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
what to do next. Commands that mash tells you to run stand on a line of
their own, so you can copy one whole. Nothing is cleared: scroll back at
any time.

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

next: install mashctl, the server's tool (step 1-B; you, on myserver).

### Step 1-B: Install mashctl (command 2 of 2)

`mashctl` is what the person who runs the mash server uses to create
endpoints. Here that is you, on `myserver`. It is not among the release
assets, so you build it with Go.

```sh
go install github.com/bogworks/mash/cmd/mashctl@<version>
```

next: make Alice's key (step 2; you, on myserver, where Alice's agent will
run).

### Step 2: Make a key for Alice (command 1 of 3)

Alice needs a pair of keys to prove who she is on the mail system. The
private key never leaves Alice's machine; the public key is what you hand
to the server. Steps 2, 3 and 4 are the whole sign-up for one endpoint: make
a key, have the server create the endpoint, install the credentials.

```
$ mash key create --out alice.key
Created a key pair (alice.key, alice.pub).
  private key  alice.key  keep this on this machine
  public key   alice.pub  share this one

next: give alice.pub to whoever runs your mash server (you, if that is this
      machine). They run:
  mashctl endpoint create <owner>.<group>.alice --public-key alice.pub
```

You are the server's owner here, and the owner and group are `acme` and
`demo`: the next step runs that command with them filled in.

next: create Alice's endpoint on the server (step 3; you, on myserver).

### Step 3: Create Alice's endpoint on the server (command 2 of 3)

An endpoint is Alice's mailbox on the server, at the address
`acme.demo.alice`. The server signs a statement that `alice.pub` belongs to
it. That statement is a token: it holds no secret, so it is safe to carry
around.

```
$ mashctl endpoint create acme.demo.alice --public-key alice.pub
Created endpoint acme.demo.alice.
Wrote acme.demo.alice.token: your server's signed statement that alice.pub is
this endpoint. It holds no secret.

next: take acme.demo.alice.token to the agent's machine and run there:
  mash credential install --key alice.key --token acme.demo.alice.token
```

next: install Alice's credentials (step 4; you, on Alice's machine; here the
same machine).

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
Keep alice.key: it is the private half. mash signs in with the key and token.

  wrote   ~/.config/mash/acme.demo.alice.creds  (mode 600)
  wrote   ~/.config/mash/config.json            (server, owner, CA)
  copied  ca.pem to ~/.config/mash/ca.pem       (mode 644)

next: check that acme.demo.alice can sign in:
  MASH_GROUP=demo MASH_NAME=alice mash status
```

`config.json` stores the owner too, and mash read the group and name from
the token. The group and name of an agent that Claude Code starts come from
the environment that starts it, not from this file: step 9 does that, after
the plugin is installed. (If you set `MASH_CONFIG_DIR`, the files are in
that directory instead of `~/.config/mash`.)

next: make Bob's endpoint the short way (step 5; you, on myserver).

### Step 5: Make Bob in one command (command 1 of 1)

Steps 2, 3 and 4 were three commands because the key is made on the
endpoint's machine and only the public half goes to the server. When the
endpoint and `mashctl` are on the same machine, as here, one command does
all three. It is the wrong form when Bob's machine is another one: then use
the three steps above (docs/operations.md, "An endpoint on another machine").

```
$ mashctl endpoint create acme.demo.bob --make-key
Created a key pair (bob.key, bob.pub).
Created endpoint acme.demo.bob.
Installed credentials for acme.demo.bob.

  wrote   ~/.config/mash/bob.key              (mode 600)
  wrote   ~/.config/mash/bob.pub
  wrote   ~/.config/mash/acme.demo.bob.token
  wrote   ~/.config/mash/acme.demo.bob.creds  (mode 600)
  wrote   ~/.config/mash/config.json          (server, owner, CA)
  copied  ca.pem to ~/.config/mash/ca.pem     (mode 644)

next: check that acme.demo.bob can sign in:
  MASH_GROUP=demo MASH_NAME=bob mash status
```

No `--server` or `--ca` here: `mashctl` already knows the server and the CA
from the NATS setup, and writes them for `mash` too (the `config.json` and
`ca.pem` lines). Alice's step 4 did the same job with the two flags.

next: check that Alice can sign in (step 6; you, as alice).

### Step 6: Check that Alice can sign in (command 1 of 1)

`mash status` tells you whether mash is ready for this endpoint, and
whether the server accepts you. The owner (`acme`) and the server are
already in `config.json`; the command names the group and name for this one
run.

```
$ MASH_GROUP=demo MASH_NAME=alice mash status
acme.demo.alice is ready.

  server       myserver:4222, reachable, encrypted
  config       ok
  credentials  accepted by the server
  group        demo has 1 other endpoint; acme has 2 in all
```

next: send Bob a message (step 7; you, as alice).

### Step 7: Send mail from Alice to Bob and read it (command 1 of 4)

Alice sends two messages; the second one is marked urgent. Then you become
Bob and read them. Mail waits in Bob's mailbox on the server until he reads
it, whether or not he is running. The next line sets who you are for this
shell only (Claude Code gets its own, in step 9):

```sh
export MASH_GROUP=demo MASH_NAME=alice
```

```
$ mash send acme.demo.bob "lunch at noon?"
Sent your message to acme.demo.bob.
```

```
$ mash send acme.demo.bob --urgent --kind question "ready?"
Sent your urgent question to acme.demo.bob.
```

next: become Bob and list his inbox (you, as bob).

Command 3 of 4: now you are Bob, in this shell only.

```sh
export MASH_NAME=bob
```

```
$ mash inbox
2 messages for acme.demo.bob, 2 new, 1 urgent.

 #  STATE        FROM             TRUST     WHEN     KIND      MESSAGE
 1  new          acme.demo.alice  verified  12m ago  task      lunch at noon?
 2  new, urgent  acme.demo.alice  verified  12m ago  question  ready?
```

Command 4 of 4: read the urgent one. `verified` means the message was
signed by Alice's own key.

```
$ mash read 2
  message  #2
  from     acme.demo.alice
  when     12m ago
  trust    verified (signed by acme.demo.alice's key)
  kind     urgent question

> ready?

next: answer it, linked to #2:
  mash send acme.demo.alice --reply-to 2 "<your answer>"
```

next: see who else is here (step 8; you, as bob).

### Step 8: See who is here (command 1 of 1)

`mash who` lists the endpoints you can send to. Copy an address from it
into `mash send`. Both are offline: no Claude Code session runs as either of
them yet (step 9 starts them).

```
$ mash who
ENDPOINT         STATE    LAST SEEN
acme.demo.alice  offline  never
acme.demo.bob    offline  never      (you)
```

next: add mash to Claude Code (step 9; you, on myserver).

### Step 9: Use it from Claude Code

An agent in Claude Code reads and sends mail through the mash plugin; it
does not type these commands. First export the plugin and install it:

```
$ mash plugin export ./mash-plugin
Wrote the mash plugin for Claude Code to ./mash-plugin (5 files).

next: add it to Claude Code as a marketplace, then install it:
  claude plugin marketplace add ./mash-plugin
  claude plugin install mash@mash
```

Then start Claude Code as Alice. Claude Code hands its environment to the
mash plugin, so Alice's group and name are set in the terminal that starts
it:

```sh
export MASH_GROUP=demo MASH_NAME=alice
claude --dangerously-load-development-channels plugin:mash@mash
```

Do the same in a second terminal for Bob, with `MASH_NAME=bob` in the
`export` line. Claude Code will ask you to confirm the
`--dangerously-load-development-channels` flag at each launch; that is
expected: mash is not on Claude Code's channel allowlist, and the flag only
lets mash wake a session that is sitting idle (without it, mail still
arrives at the session's next prompt or tool call).

next: when you are done, clean up (step 10; you, on myserver).

### Step 10: Clean up

Remove what the steps made, and nothing else. Every path below is one a step
printed as `wrote` or `copied`, or a file you named in a command; the list is
exact, and there are no wildcards. If you set `MASH_CONFIG_DIR`, use that
directory in place of `~/.config/mash`. The keys are secrets, so remove them
too. In the directory where you ran steps 2 and 3:

```sh
rm alice.key alice.pub acme.demo.alice.token
```

The files steps 4 and 5 wrote in the config directory:

```sh
rm ~/.config/mash/acme.demo.alice.creds
rm ~/.config/mash/bob.key ~/.config/mash/bob.pub
rm ~/.config/mash/acme.demo.bob.token
rm ~/.config/mash/acme.demo.bob.creds
rm ~/.config/mash/config.json ~/.config/mash/ca.pem
```

(Remove `config.json` and `ca.pem` only if they did not exist before this
quick start; they now name this server.) The exported plugin, and its place
in Claude Code:

```sh
rm -r ./mash-plugin
claude plugin uninstall mash@mash
claude plugin marketplace remove mash
```

The `ca.pem` you gave to `--ca` is the NATS setup guide's file, not one of
these; leave it. There is no stop command for the server: `mashctl` has no
stop verb (checked in its source), so stop the server the way the NATS setup
guide ([docs/nats-setup.md](docs/nats-setup.md)) says.

---

## What is different from 0.5.5's quick start

- The server is set up first, by the NATS setup guide; `mashctl init
  --local --start` is not part of the quick start.
- Alice and Bob, host `myserver`, group `demo`, are named up front.
- Step 4 takes `--server` and `--ca` and writes `config.json` and the CA
  copy, and prints each file it wrote; the group and name are set per
  command (or in the terminal that starts Claude Code), and `MASH_OWNER` is
  no longer needed.
- The three-command endpoint flow is `mash key create --out alice.key`,
  `mashctl endpoint create ... --public-key alice.pub`, `mash credential
  install --key alice.key --token ...` (was `credential create`).
- Every step says why, shows the exact output, then what to do next; nothing
  clears. Commands to run stand on their own line.
- `mash status` opens with a verdict, shows the server, and checks the server
  and the credentials.
- Mail commands print words, a table and relative times, not tab-separated
  lines.
- Cleanup lists the exact files; it is not a guess.
