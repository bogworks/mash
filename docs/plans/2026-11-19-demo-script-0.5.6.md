# mash 0.5.6 demo: script

Status: draft for review. Text only; nothing is recorded yet.

The script answers Randy's feedback on the v0.5.5 walk-through (2026-11-19). It is written against the target
outputs that core and ctl are mocking in `docs/plans/2026-11-19-cli-targets-*.md`. Where those documents had not
landed when this draft was written, the output shown is this script's proposal, marked `TARGET?` beside the command.
The assumptions and proposals behind those outputs are listed at the end. When the target documents land, the outputs
are copied from them and the marks are removed.

Two reviewers read this draft cold before it was sent, as Randy asked for videos: a junior-developer reader (Fable)
and a CLI UX reviewer (Opus). Their fixes are in, and the section "Reviews of this draft" lists them.

## How the recording is laid out

- **No screen clears.** It runs like one terminal session that you can scroll back through.
- **80 columns.** Every line of command and output fits in 80 columns, and long output is wrapped by the tool, not
  by the terminal.
- **Every step is introduced before its command.** The title reads "Step 2-A: ... (command 1 of 3)", followed by what
  the step does and why in plain words. Then come the command and its output, a blank line, and a `NEXT:` line.
- **One command per step.**
- **`NEXT:` is in colour** (bold cyan) and is always true: it names exactly what the next step does.
- **The prompt names who runs the command.**
  - `admin@myserver$` is the person who runs mash for the team.
  - `bob@myserver$` and `alice@myserver$` are the two agents' accounts on the same machine.
- **Names:** owner `acme`, group `demo`, agents `bob` and `alice`, host `myserver`. No real person's name appears.

---

## Title screen

```
mash 0.5.6: two agents on one machine, step by step

mash gives each AI agent a mailbox that other agents can write to.
The mail is kept on a NATS server until it is read, and every
message is signed, so the reader knows who really sent it.

Two tools:
  mash      run by each agent (here: by the accounts bob and alice)
  mashctl   run by the admin, who creates and removes endpoints
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
 |   acme/demo/bob  <--->  tls://localhost:4222 <---> acme/demo/alice |
 |                                                                    |
 |   mashctl: the admin's tool; creates and removes endpoints         |
 |                                                                    |
 +--------------------------------------------------------------------+

   owner: acme        group: demo        agents: bob, alice
```

**Reading an endpoint path.** An endpoint path is `owner/group/agent`: `acme/demo/bob` is agent `bob`, in group
`demo`, owned by `acme`.

The script uses these words:
- **owner:** who the endpoints belong to: a team, a company or one developer. One owner's agents can mail only each
  other.
- **group:** a set of agents under one owner, such as a project. Naming a group in a path is enough; there is nothing
  to create.
- **endpoint:** an agent's address and mailbox on the server.
- **key:** a key pair. The private half is the **seed** (NATS's word for a private key), and it never leaves the
  account it was made in. The public half can be shared freely.
- **JWT** (JSON Web Token): a permission slip that the owner signs for one public key, saying "this key may act as
  `acme/demo/bob`".
- **credentials file:** the seed and the JWT together in one file. It is what mash logs in with.

---

## Part 1: the server

### Step 1-A: Start a NATS server for mash (command 1 of 2)

mash keeps mail on a NATS server. mash does not install or run NATS. Your platform team may already run one, or you
run your own.

For this demo we use the setup helper that ships with mash. It is a script you can read, not part of `mashctl`. It
writes a server configuration and a TLS certificate into the directory you choose, then starts the server on this
machine only. An agent can follow the same steps from the NATS setup guide.

```
admin@myserver$ ./scripts/nats-local.sh --dir ~/nats-demo  TARGET?
config      ~/nats-demo/server.conf
certificate ~/nats-demo/tls/ca.pem (clients trust this)
server      running, pid 4121, tls://localhost:4222
stop with   ./scripts/nats-local.sh --dir ~/nats-demo --stop
```

`NEXT:` We point mashctl at this server and make the owner acme.

### Step 1-B: Create the owner acme (command 2 of 2)

This step does three things:
- It records the server and the owner `acme` in the admin's mashctl configuration.
- It makes the owner's signing key, which mashctl uses to sign each endpoint's JWT. The key stays in the admin's
  account.
- It prepares the server to keep acme's mail.

Groups need no step of their own: `demo` exists as soon as an endpoint path names it.

```
admin@myserver$ mashctl init --server tls://localhost:4222 \  TARGET?
                  --ca ~/nats-demo/tls/ca.pem --owner acme
owner       acme
server      tls://localhost:4222 (connected)
signing key ~/.config/mashctl/acme.nk (private: stays here)
```

`NEXT:` We give agent bob an endpoint. Bob makes his own key, so his seed never leaves his account.

---

## Part 2: two endpoints, two ways

There are two ways to give an agent its credentials. Bob uses the first and Alice the second.

| | Who makes the key | When to use it |
|---|---|---|
| Path 1 (Bob) | the agent, in its own account | the agent and the admin are on different machines or accounts; the seed is never sent anywhere |
| Path 2 (Alice) | mashctl, for the agent | the admin can write into the agent's account, as on this one machine; one command instead of three. The admin's machine sees the seed. |

Both paths end with the same thing: a credentials file in the agent's mash directory, `~/.config/mash/`.

### Step 2-A: Bob makes a key (command 1 of 3)

Bob creates his key. The seed stays in Bob's account. The public half, `bob.pub`, is what he hands to the admin.

```
bob@myserver$ mash key create bob                             TARGET?
seed        bob.seed (private: never share this file)
public key  bob.pub
next: send bob.pub to your mash admin
```

`NEXT:` The admin creates Bob's endpoint from `bob.pub`. On one machine, the admin can read the file in Bob's home.
Across machines, you would send it.

### Step 2-B: The admin creates Bob's endpoint (command 2 of 3)

mashctl creates the endpoint `acme/demo/bob` and its mailbox on the server. It also signs a JWT that lets Bob's key,
and only that key, act as `acme/demo/bob`. The JWT is not secret: it is useless without the seed. The admin puts it
back in Bob's home.

```
admin@myserver$ mashctl endpoint create acme/demo/bob \
                  --public-key ~bob/bob.pub --jwt-out ~bob/bob.jwt
created     acme/demo/bob
jwt         ~bob/bob.jwt (give this to bob)
```

`NEXT:` Bob joins his seed and the JWT into one credentials file.

### Step 2-C: Bob makes his credentials file (command 3 of 3)

This joins the seed and the JWT into the one file mash logs in with. It checks that the JWT was issued for Bob's own
key. Bob's seed was never sent anywhere, and his endpoint now works.

```
bob@myserver$ mash credential create --seed bob.seed --jwt bob.jwt
credentials ~/.config/mash/acme/demo/bob.creds
endpoint    acme/demo/bob
next: bob.seed and bob.jwt are now in the credentials file;
      you can delete them
```

`NEXT:` We give agent alice an endpoint the short way: mashctl makes her key, in one command.

### Step 3-A: The admin creates Alice's endpoint and key (command 1 of 1)

On a machine the admin and the agent share, mashctl can make the key itself. This one command does what Steps 2-A to
2-C did: it makes Alice's key, creates `acme/demo/alice`, and writes her credentials file straight into her mash
directory.

No separate seed file appears, because the seed goes straight into the credentials file. These are Alice's
credentials, not the admin's. The admin's signing key never leaves mashctl.

```
admin@myserver$ mashctl endpoint create acme/demo/alice \  TARGET?
                  --make-key --creds-dir ~alice/.config/mash
created     acme/demo/alice
credentials ~alice/.config/mash/acme/demo/alice.creds
```

`NEXT:` We list acme's endpoints.

### Step 4-A: List the endpoints (command 1 of 2)

This lists every endpoint the owner has, one row each.

```
admin@myserver$ mashctl endpoint list                          TARGET?
OWNER   GROUP   AGENT   ENDPOINT PATH     CREATED
acme    demo    alice   acme/demo/alice   2026-11-19 10:02
acme    demo    bob     acme/demo/bob     2026-11-19 10:01
```

`mashctl endpoint list --wide` adds each endpoint's public key and mailbox, for when you need them.

`NEXT:` We look at Bob's endpoint in detail.

### Step 4-B: Show one endpoint (command 2 of 2)

This shows what the server knows about one endpoint: whose it is, and which key may act as it.

```
admin@myserver$ mashctl endpoint show acme/demo/bob             TARGET?
endpoint    acme/demo/bob
created     2026-11-19 10:01
key         UBX4...Q7KM
```

`NEXT:` Bob checks his own setup, with mash status.

---

## Part 3: the agents

In real use, Bob and Alice are Claude Code sessions. They don't type these commands: they use mash's tools (send,
inbox, who) from inside Claude Code. Here we run the same actions from the command line, so you can see each one.

Each account was given a short mash configuration off screen, naming the server and its own endpoint path. That is
how `mash` knows who Bob is, with nothing in front of each command.

### Step 5-A: Bob checks his setup (command 1 of 1)

`mash status` checks each part of Bob's setup and reports one line per check. If a check fails, the line says what is
wrong and what to do.

```
bob@myserver$ mash status                                       TARGET?
endpoint    acme/demo/bob
version     mash 0.5.6
server      tls://localhost:4222 (connected, encrypted)
config      ok
credentials ok
group       demo: 2 endpoints (alice, bob)
```

More detail, for debugging, comes from `mash status --detail`.

`NEXT:` Bob sends Alice a message.

### Step 6-A: Bob mails Alice (command 1 of 4)

The server keeps the message in Alice's mailbox until she reads it, even if she is offline. mash signs it with Bob's
key. Each mailbox numbers its own mail from #1.

```
bob@myserver$ mash send acme/demo/alice "hello from bob"
sent        #1 to acme/demo/alice
```

`NEXT:` Alice reads her mailbox.

### Step 6-B: Alice reads her mail (command 2 of 4)

`verified` means the message was signed by Bob's own key, so Alice knows it really came from Bob and not from
something pretending to be him.

```
alice@myserver$ mash inbox                                      TARGET?
#   FROM            SENT              TRUST
1   acme/demo/bob   2026-11-19 10:05  verified
    hello from bob
```

`NEXT:` Alice answers Bob.

### Step 6-C: Alice answers (command 3 of 4)

Alice sends Bob a message back, and says it answers his.

```
alice@myserver$ mash send acme/demo/bob "hi bob, got it" \     TARGET?
                  --answers 1
sent        #1 to acme/demo/bob, answering your mail #1
```

`NEXT:` Bob reads his mailbox.

### Step 6-D: Bob reads the answer (command 4 of 4)

```
bob@myserver$ mash inbox                                        TARGET?
#   FROM              SENT              TRUST
1   acme/demo/alice   2026-11-19 10:06  verified
    hi bob, got it  (answers: "hello from bob")
```

`NEXT:` The admin replaces Alice's key, as you would if it might have leaked.

---

## Part 4: changing and removing endpoints

### Step 7-A: Replace Alice's key (command 1 of 1)

This makes a new key for Alice and revokes the old one on the server, so the old key stops working at once. Her
endpoint path does not change, and neither does her mail. Her Claude Code session would restart to load the new key.

```
admin@myserver$ mashctl endpoint rotate-key acme/demo/alice \  TARGET?
                  --make-key --creds-dir ~alice/.config/mash
rotated     acme/demo/alice
old key     UDA7...3MZQ (revoked)
new key     UCK2...9PLD
credentials ~alice/.config/mash/acme/demo/alice.creds
next: restart alice's Claude Code session to load the new key
```

`NEXT:` The admin deletes Alice's endpoint.

### Step 8-A: Delete Alice's endpoint (command 1 of 2)

This removes the endpoint and its mailbox and revokes its key on the server. It does not touch Alice's account: her
credentials file is still there, but it no longer works.

```
admin@myserver$ mashctl endpoint delete acme/demo/alice         TARGET?
deleted     acme/demo/alice
key         UCK2...9PLD (revoked)
mailbox     removed
```

`NEXT:` Alice runs mash status and sees her key refused.

### Step 8-B: Alice's credentials are refused (command 2 of 2)

The server refuses a deleted endpoint's key, even though the credentials file is still on disk. `mash status` gives
the reason, on the same lines as before.

```
alice@myserver$ mash status                                     TARGET?
endpoint    acme/demo/alice
version     mash 0.5.6
server      tls://localhost:4222 (connected, encrypted)
config      ok
credentials refused: key revoked (endpoint deleted)
            ask your mash admin for a new endpoint
```

`NEXT:` Two mistakes mash refuses on purpose, and what it tells you to do.

---

## Part 5: what mash refuses

### Step 9-A: An endpoint path with a part missing (command 1 of 2)

Every endpoint path has all three parts. mash does not guess the missing ones. It shows the form to use instead.

```
admin@myserver$ mashctl endpoint create carol --make-key        TARGET?
error: "carol" is not an endpoint path (owner/group/agent)
  try: mashctl endpoint create acme/demo/carol --make-key
```

`NEXT:` mash refuses an unencrypted connection.

### Step 9-B: A server without TLS (command 2 of 2)

mash connects only with TLS, even on this one machine. A `nats://` address is refused before anything is sent.

```
bob@myserver$ mash send acme/demo/bob "note to self" \          TARGET?
                --server nats://localhost:4222
error: nats://localhost:4222 is not encrypted; mash needs tls://
  try: --server tls://localhost:4222
```

`NEXT:` That's the demo. A summary follows.

## Closing screen

```
What you saw
  1  a NATS server, started by a helper script, not by mash
  2  one owner (acme), one group (demo), two agents (bob, alice)
  3  two ways to get credentials: the agent makes its own key,
     or mashctl makes it when admin and agent share a machine
  4  signed mail both ways, marked verified
  5  a key replaced, an endpoint deleted, its key refused

Next: docs/quickstart.md sets this up on your own machine.
```

---

## Assumptions and proposals for the targets

Every `TARGET?` mark rests on one of these. Each is replaced by the target document's output when it lands. Where
this script proposes a CLI change, the target decides.

- **A1: NATS is set up outside mashctl.** `mashctl init --local` and its default under `/demo` go away (feedback 1).
  The helper's name (`scripts/nats-local.sh`), its flags and its output are a guess. A skill that an agent follows is
  the alternative Randy preferred.
- **A2: owner setup.** `mashctl init --server ... --ca ... --owner acme` records the server, makes the owner's signing
  key, and prepares the server's storage. The real command and output come from the ctl target.
- **A3: one address form on screen.** Every command, file name and output uses `acme/demo/bob`. The dotted and
  underscore forms never appear in normal output (feedback 13). That includes the credentials file:
  `acme/demo/bob.creds` under the mash directory, not `acme.demo.bob.creds`. Proposal: `mash send` takes the slash
  form.
- **A4: no preamble lines.** mashctl prints no "target server ... (from ...)" line (feedback 14). That kind of detail
  moves to `--verbose`.
- **A5: output shape.** Every command prints aligned `label value` rows, with a 12-column label in lower case. Lists
  have upper-case headers, fill every cell, and offer `--wide` (feedback 12 and 14). Timestamps are
  `YYYY-MM-DD HH:MM` everywhere.
- **A6: `mash status`.** One row per check, from Randy's own sketch: version, server, config, credentials. A group row
  is added if it is fast. `--detail` carries the rest (feedback 16 and 17). Proposal: status reports a revoked key
  (the v0.5.5 walk-through's finding was that it does not), exiting 1.
- **A7: `--creds-dir` on `--make-key`.** mashctl writes the credentials into the agent's mash directory. If the target
  keeps the admin's config directory instead, Step 3-A gains one step moving the file, and the narration says so.
- **A8: answering mail.** Proposal: `mash send ... --answers N`. The name `reply` is taken: the plugin's MCP `reply`
  tool answers a live prompt, so a CLI `reply` meaning something else would confuse. If the target has neither, Step
  6-C sends a plain message and the "answering" text is dropped.
- **A9: identity from the agent's own config.** Each agent's account has its own mash config, set up off screen, so
  no `MASH_*` variables appear on screen. Proposal: show it in one step if the target adds a command that writes it.
- **A10: `mash key create NAME`.** Writes `NAME.seed` and `NAME.pub`, labels the public key, and says what to do
  next. Today the command takes `--out FILE` and quietly writes the `.pub` beside it.
- **A11: `endpoint rotate-key`.** Proposed instead of `endpoint reissue`: "reissue" reads as re-signing the JWT, not
  as making a new key.
- **A12: delete leaves the agent's files alone.** v0.5.5 renamed the credentials file in the agent's directory.
  Proposal: mashctl does not touch another account's files on delete.
- **A13: errors say what to type.** Each error names what was wrong and gives a `try:` line that works as written.
- **A14: one flag name for the server.** It is `--server` in both tools, never `--url`.

## Reviews of this draft

- **Junior-developer reader (Fable).** It could not tell how an agent knows its server and identity. It was confused
  by files moving between accounts without a step, by a `NEXT:` in 8-A that was untrue, and by a step with two
  commands. Each "#1" read as the same message. One error guessed the owner and group right after the narration said
  mash doesn't guess.
  - Fixed: the note before Step 5-A, the `~bob/` paths in Step 2-B, the seed clean-up line, one command per step, the
    rewritten Steps 8-A and 8-B and Step 9-A, and the word list (owner, group, JWT).
- **CLI UX reviewer (Opus).** It found the dotted form back in file names, and the list repeating the address with
  blank cells. Status disagreed with its narration, and its error lines had a different shape from its success lines.
  It also flagged `reissue`, `--creds-out` taking a directory, `--url` against `--server`, and timestamps in two
  formats.
  - Fixed: A3, A5, A6 and A10 to A14. Step 9-B now sends to a live endpoint.
  - Kept against the reviewer: the list keeps Randy's OWNER, GROUP and AGENT columns beside the path, from his own
    sketch, but fills every cell.

## Feedback answered (Randy, 2026-11-19)

| # | Feedback | Where it is answered |
|---|---|---|
| 1 | mashctl must not install NATS; no `/demo` default | Step 1-A, A1 |
| 2 | Bob and Alice | throughout |
| 3 | seed or private key? | the word list: a seed is NATS's word for a private key |
| 4 | lines no longer than 80 characters | layout rules; every block checked |
| 6 | name the machine, set up first, diagram, explain the path in one line | the setup section |
| 7 | spacing and a coloured `NEXT:` | layout rules |
| 8 | explain each step; key compared with credential | Part 2 and its table; Steps 2-A to 2-C |
| 9 | no screen clears | layout rules |
| 10 | why there is no seed file for the second agent | Step 3-A |
| 11 | `NEXT:` must name the next screen | layout rules; each `NEXT:` checked |
| 12–14 | list output: aligned, `--wide`, one address form, no preamble lines | Step 4-A, A3 to A5 |
| 15 | no real names | owner acme |
| 16–17 | short show and status; detail behind a flag | Steps 4-B and 5-A, A6 |
| 18 | agents use MCP, not the CLI | the introduction to Part 3 |
