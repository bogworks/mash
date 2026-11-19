# mash 0.5.6 demo: script

Status: draft for review. Text only; nothing is recorded yet.

This script answers Randy's feedback on the v0.5.5 walk-through (2026-11-19). Every `mash` output is copied from
core's targets, `docs/plans/2026-11-19-cli-targets-mash.md` (#820). Every `mashctl` output is marked `ctl` beside the
command. Two kinds are marked that way:
- outputs quoted from the CLI team's proposal, which #820 quotes;
- placeholders, until ctl's targets (`docs/plans/2026-11-19-cli-targets-mashctl.md`) land.

The section "Where this script differs from the targets" lists what is still open.

Two reviewers read the first draft cold, as Randy asked for videos: a junior-developer reader (Fable) and a CLI UX
reviewer (Opus). Their fixes are in.

## How the recording is laid out

- **No screen clears.** It runs like one terminal session that you can scroll back through.
- **80 columns.** Every line fits in 80 columns, and the tools wrap their own output.
- **Every step is introduced before its command.** The title reads "Step 3-A: ... (command 1 of 3)", followed by what
  the step does and why in plain words. Then come the command and its output, a blank line, and a `NEXT:` line.
- **`NEXT:` is in colour** (bold green) and is always true: the next step does what it says.
  - Where a command prints its own `NEXT:`, that line is the one shown.
  - Where a command has none, the guide adds one in the same colour, outside the command's output.
- **One command per step.** Switching agents with `export` is part of the step that needs it.
- **One user and one machine.** The demo is run by one user, `dev`, on `myserver`. That user runs mashctl and acts as
  each agent in turn.
- **Names:** owner `acme`, group `demo`, agents `bob` and `alice`, host `myserver`. No real person's name appears.

---

## Title screen

```
mash 0.5.6: two agents on one machine, step by step

mash gives each AI agent a mailbox that other agents can write to.
The mail is kept on a NATS server until it is read, and every
message is signed, so the reader knows who really sent it.

Two tools:
  mash      what each agent uses: send, inbox, read, status
  mashctl   what the person who runs the server uses: create
            and remove endpoints
```

## The setup we are building

We walk through setting up mash for two agents on a single machine.

```
                           host: myserver
 +--------------------------------------------------------------------+
 |                                                                    |
 |   agent bob             NATS server              agent alice       |
 |   (Claude Code)         (keeps the mail)         (Claude Code)     |
 |                                                                    |
 |   acme.demo.bob  <--->  tls://localhost:4222 <---> acme.demo.alice |
 |                                                                    |
 |   mashctl: creates and removes endpoints on the server             |
 |                                                                    |
 +--------------------------------------------------------------------+

   owner: acme        group: demo        agents: bob, alice
```

**Reading an address.** `acme.demo.bob`: acme is who owns it, demo is the group of agents that can see each other,
and bob is this agent. mash shows it as `acme.demo.bob`, and also accepts `acme/demo/bob` when you type it.

The script uses these words:
- **endpoint:** an agent's address and mailbox on the server.
- **private key:** a file only the agent's machine keeps (NATS calls it a seed).
- **public key:** the matching half, which is safe to share.
- **token:** what the server signs to say "this public key is endpoint `acme.demo.bob`". It holds no secret.
- **credentials:** the one file mash signs in with: the private key plus the token.

---

## Part 1: the server

### Step 1-A: Start a NATS server for mash (command 1 of 2)

mash keeps mail on a NATS server. mash does not install or run NATS. Your platform team may already run one, or you
run your own.

For this demo we use the setup helper that ships with mash. It is a script you can read, not part of `mashctl`. It
writes a server configuration and a TLS certificate into the directory you choose, then starts the server on this
machine only. An agent can follow the same steps from the NATS setup guide.

```
dev@myserver$ ./scripts/nats-local.sh --dir ~/nats-demo        ctl
Started a NATS server for mash at tls://localhost:4222.
  config       ~/nats-demo/server.conf
  certificate  ~/nats-demo/tls/ca.pem  (clients trust this)

NEXT: Set up your owner with: mashctl init --server tls://localhost:4222
--ca ~/nats-demo/tls/ca.pem --owner acme
```

### Step 1-B: Set up the owner acme (command 2 of 2)

An owner is who the endpoints belong to: a team, a company or one developer. One owner's agents can mail only each
other. This step points mashctl at the server, makes acme's signing key, and prepares the server to keep acme's mail.
Groups need no step of their own: `demo` exists as soon as an address names it.

```
dev@myserver$ mashctl init --server tls://localhost:4222 \      ctl
                --ca ~/nats-demo/tls/ca.pem --owner acme
Set up owner acme on tls://localhost:4222.

NEXT: Create an endpoint with: mashctl endpoint create acme/demo/bob
--public-key bob.pub (make the key first: mash key create --out bob.key)
```

`NEXT:` (guide) We give Bob an endpoint the long way first, as if Bob ran on another machine.

---

## Part 2: two endpoints, two ways

There are two ways to give an agent its credentials. Bob uses the first and Alice the second.

| | Who makes the private key | When to use it |
|---|---|---|
| Three commands (Bob) | the agent, on its own machine | the agent runs on a different machine from mashctl; the private key is never sent anywhere |
| One command (Alice) | mashctl | the agent runs on the same machine as mashctl, as here |

Both end with the same thing: Bob's or Alice's credentials, installed in `~/.config/mash`.

In this demo, every machine is `myserver`. In Steps 3-A and 3-C, picture Bob's machine. In Step 3-B, picture the
machine that runs mashctl.

### Step 3-A: Make a key for Bob (command 1 of 3)

Bob needs a pair of keys to prove who he is on the mail system. The private key never leaves Bob's machine. The
public key is what you hand to the server.

```
dev@myserver$ mash key create --out bob.key
Made a private key for bob (NATS calls it a seed).
  private key  bob.key  keep this on this machine
  public key   bob.pub  share this one

NEXT: Send bob.pub to whoever runs your mash server (you, if that is this
machine) and run: mashctl endpoint create acme/demo/bob --public-key bob.pub
```

### Step 3-B: Create Bob's endpoint on the server (command 2 of 3)

The server's owner creates the endpoint `acme.demo.bob` from Bob's public key. The server signs a token saying that
this public key, and only this one, is `acme.demo.bob`. The token holds no secret: without the private key it is
useless.

```
dev@myserver$ mashctl endpoint create acme/demo/bob \           ctl
                --public-key bob.pub
Created endpoint acme.demo.bob.
Wrote acme.demo.bob.token: your server's signed statement that bob.pub is
this endpoint. It holds no secret.

NEXT: Take acme.demo.bob.token to the agent's machine and run: mash
credential install --key bob.key --token acme.demo.bob.token
```

### Step 3-C: Install Bob's credentials (command 3 of 3)

Credentials are the one file mash signs in with: Bob's private key plus the server's token. This step joins the two,
and checks that the token was made for Bob's key.

```
dev@myserver$ mash credential install --key bob.key \
                --token acme.demo.bob.token
Installed credentials for acme.demo.bob in ~/.config/mash.
They combine bob.key and the token; mash uses them to sign in.

NEXT: Check that Bob can sign in, with: export MASH_OWNER=acme
MASH_GROUP=demo MASH_NAME=bob; mash status
```

### Step 3-D: Act as Bob and check his setup (command 1 of 1)

The three `MASH_` variables tell `mash` which endpoint you are acting as. `mash status` signs in to the server once,
and reports what a person needs to know. More detail comes from `mash status --detail`.

```
dev@myserver$ export MASH_OWNER=acme MASH_GROUP=demo MASH_NAME=bob
dev@myserver$ mash status
Checked mash for acme.demo.bob.

  version      mash 0.5.6
  server       reachable, encrypted
  config       valid
  credentials  valid
  group        no other endpoints in demo yet
```

(The `group` wording for zero endpoints is this script's; #820 shows only the case with some.)

`NEXT:` (guide) We give Alice an endpoint the short way: one mashctl command.

### Step 4-A: Create Alice's endpoint in one command (command 1 of 1)

When the agent runs on the machine that runs mashctl, one command does all of Steps 3-A to 3-C. It makes Alice's
private key, creates `acme.demo.alice`, and installs her credentials. No file goes anywhere else, so nothing has to be
handed over.

```
dev@myserver$ mashctl endpoint create acme/demo/alice --make-key  ctl
Made a private key for alice.      (alice.key, alice.pub in ~/.config/mash)
Created endpoint acme.demo.alice.  (token: acme.demo.alice.token)
Installed credentials for acme.demo.alice in ~/.config/mash.

NEXT: Check that Alice can sign in, with: MASH_OWNER=acme MASH_GROUP=demo
MASH_NAME=alice mash status
```

### Step 4-B: Check Alice's setup (command 1 of 1)

This is the command the last `NEXT:` printed. The variables in front of `mash` apply to this one command only, so the
shell still acts as Bob afterwards.

```
dev@myserver$ MASH_OWNER=acme MASH_GROUP=demo MASH_NAME=alice \
                mash status
Checked mash for acme.demo.alice.

  version      mash 0.5.6
  server       reachable, encrypted
  config       valid
  credentials  valid
  group        1 other endpoint in demo, 1 under acme
```

`NEXT:` (guide) We list the endpoints acme now has.

### Step 5-A: List the endpoints (command 1 of 1)

This lists every endpoint the owner has, one row each.

```
dev@myserver$ mashctl endpoint list                              ctl
ENDPOINT         CREATED
acme.demo.alice  2026-11-19 10:02
acme.demo.bob    2026-11-19 10:01
```

`NEXT:` (guide) Bob sends Alice a message.

---

## Part 3: the agents

In real use, Bob and Alice are Claude Code sessions. They don't type these commands: they use mash's tools (send,
inbox, read, who) from inside Claude Code. Here we run the same actions from the command line, so you can see each
one.

### Step 6-A: Bob mails Alice (command 1 of 5)

We are still acting as Bob. The server keeps the message in Alice's inbox until she reads it, even if she is offline.
mash signs it with Bob's private key.

```
dev@myserver$ mash send acme.demo.alice "lunch at noon?"
Sent your message to acme.demo.alice (message #1 in their inbox).
```

`NEXT:` (guide) We switch to Alice and look at her inbox.

### Step 6-B: Alice looks at her inbox (command 2 of 5)

Only the agent name changes, so one `export` switches to Alice. `verified` means the message was signed by Bob's own
key, so Alice knows it really came from Bob.

```
dev@myserver$ export MASH_NAME=alice
dev@myserver$ mash inbox
1 message in your inbox (acme.demo.alice).

 #  FROM            TRUST     WHEN       MESSAGE
 1  acme.demo.bob   verified  1 min ago  lunch at noon?

NEXT: Read one in full with: mash read 1
```

### Step 6-C: Alice reads the message (command 3 of 5)

```
dev@myserver$ mash read 1
Message #1 from acme.demo.bob, 1 min ago. Verified: signed by its key.

> lunch at noon?

NEXT: Answer it with: mash send acme.demo.bob "your answer"
```

### Step 6-D: Alice answers (command 4 of 5)

```
dev@myserver$ mash send acme.demo.bob "noon works"
Sent your message to acme.demo.bob (message #1 in their inbox).
```

Each inbox numbers its own mail from #1, so this is #1 in Bob's inbox.

`NEXT:` (guide) We switch back to Bob, who finds the answer.

### Step 6-E: Bob reads his inbox (command 5 of 5)

```
dev@myserver$ export MASH_NAME=bob
dev@myserver$ mash inbox
1 message in your inbox (acme.demo.bob).

 #  FROM              TRUST     WHEN       MESSAGE
 1  acme.demo.alice   verified  now        noon works

NEXT: Read one in full with: mash read 1
```

Bob could read it in full, as the `NEXT:` line says. Step 6-C showed what that looks like, so we move on.

`NEXT:` (guide) The server's owner deletes Alice's endpoint.

---

## Part 4: removing an endpoint

### Step 7-A: Delete Alice's endpoint (command 1 of 2)

This removes the endpoint and its inbox, and the server stops accepting Alice's key at once. Alice's credentials file
stays where it is, but it no longer works.

```
dev@myserver$ mashctl endpoint delete acme/demo/alice              ctl
Deleted endpoint acme.demo.alice. Its key no longer signs in.
```

`NEXT:` (guide) We act as Alice and check her setup.

### Step 7-B: Alice is refused (command 2 of 2)

The server refuses a deleted endpoint's key, even though the credentials file is still on disk. `mash status` gives
the reason and what to do.

```
dev@myserver$ export MASH_NAME=alice
dev@myserver$ mash status
Checked mash for acme.demo.alice.

  version      mash 0.5.6
  server       reachable, encrypted
  config       valid
  credentials  refused: this endpoint was deleted or its key reissued
  group        not known

NEXT: Ask whoever runs your mash server for a new token, then run mash
credential install again. They can check with: mashctl endpoint show
acme/demo/alice
```

`NEXT:` (guide) Two mistakes mash refuses on purpose, and what it tells you to do.

---

## Part 5: what mash refuses

### Step 8-A: A short address (command 1 of 2)

An address has all three parts. mash does not guess the missing ones.

```
dev@myserver$ export MASH_NAME=bob
dev@myserver$ mash send alice "hi"
mash: alice is not a full address.
fix: use owner.group.name, for example acme.demo.bob.
```

`NEXT:` (guide) mash refuses an unencrypted connection.

### Step 8-B: A server without TLS (command 2 of 2)

mash connects only with TLS, even on this one machine. A `nats://` address is refused before anything is sent.

```
dev@myserver$ MASH_URL=nats://localhost:4222 mash send \
                acme.demo.bob "note to self"
mash: refusing an unencrypted connection to localhost.
fix: use a tls:// address in MASH_URL.
```

`NEXT:` (guide) That's the demo. A summary follows.

## Closing screen

```
What you saw
  1  a NATS server, started by a helper script, not by mash
  2  one owner (acme), one group (demo), two agents (bob, alice)
  3  two ways to get credentials: the agent makes its own key
     (three commands), or mashctl makes it on its own machine
  4  signed mail both ways, marked verified
  5  an endpoint deleted, and its key refused

Next: docs/quickstart.md sets this up on your own machine.
```

---

## Where this script differs from the targets

These are for the lead to rule. Everything else follows #820 as written.

1. **One user, not three.** The first draft gave Bob, Alice and the admin separate login accounts. That needed
   mashctl to write into another user's home, which in turn needed a new flag, `--creds-dir`. It also drew two codex
   highs about home and key paths. Core's `--make-key` output installs into the running user's `~/.config/mash`. So
   the script now has one user act as both agents, switching with `export MASH_NAME`.
2. **The credentials file name.** I withdrew my proposal for `acme/demo/bob.creds` in nested directories. The script
   uses core's `acme.demo.bob.creds`, which fits R3 (one dotted form).
3. **`--answers N`.** I withdrew it as well. #820's `mash read` NEXT ("Answer it with: mash send ...") carries the
   reply with no new flag. The only loss is that Bob's inbox does not show which message Alice answered.
4. **`endpoint rotate-key` against `reissue`.** The key-rotation step is cut from this draft until ctl's targets
   land. #820's refusal text says "reissued", so if ctl keeps `reissue`, the step comes back under that name.
   - Open for a ruling: rename to `rotate-key`. "Reissue" reads as re-signing the token, not as making a new key.
5. **Guide `NEXT:` lines.** R4 has commands print `NEXT:` only when something follows. Some commands, such as `mash
   send` and `mashctl endpoint list`, print none. The guide adds its own there, in the same colour, and the narrator
   marks it as the guide's. The open question is whether those lines should look different from a command's own.
6. **mashctl outputs pending ctl.** The NATS helper (Step 1-A), `init` (1-B), `endpoint list` (5-A) and `endpoint
   delete` (7-A) are my placeholders, written in #820's style.
   - The `endpoint list` placeholder is a two-column table like `mash who`.
   - Randy's own grouped layout (owner, group and endpoint columns) is #820's `who --group` (its Q5). If ctl takes
     that layout for `endpoint list`, Step 5-A changes to match.
7. **Step 1-A.** mashctl no longer starts NATS (Randy's point 1). The helper script is a guess at what ctl will ship.
   Randy's preferred alternative is a skill an agent follows, which would make Step 1-A narration only.

## Feedback answered (Randy, 2026-11-19)

| # | Feedback | Where it is answered |
|---|---|---|
| 1 | mashctl must not install NATS; no `/demo` default | Step 1-A |
| 2 | Bob and Alice | throughout |
| 3 | seed or private key? | "private key (NATS calls it a seed)", said once (#820 R2) |
| 4 | lines no longer than 80 characters | layout rules; every block checked |
| 6 | name the machine, set up first, diagram, explain the path in one line | the setup section |
| 7 | spacing and a coloured `NEXT:` | layout rules |
| 8 | explain each step; key compared with credential | Part 2 and its table; Steps 3-A to 3-C |
| 9 | no screen clears | layout rules |
| 10 | why there is no key file to hand over for the second agent | Step 4-A |
| 11 | `NEXT:` must name the next screen | layout rules; each `NEXT:` checked against the step after it |
| 12–14 | list output: aligned, one address form, no preamble lines | Step 5-A, #820 R3 and R8 |
| 15 | no real names | owner acme |
| 16–17 | short status; detail behind a flag | Steps 3-D and 7-B, #820 R7 |
| 18 | agents use MCP, not the CLI | the introduction to Part 3 |
