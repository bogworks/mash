# Demo video script: two Claude Code sessions exchange mail

Status: draft for review. Text only; nothing is recorded. It is recorded later,
on the published 0.5.6 release, and the script and then the video are approved
before anything is published.

It follows [docs/guide/walkthrough.md](../guide/walkthrough.md), steps 1 to 10,
and uses its commands. Alice and Bob are the two endpoints. Total: about 4
minutes 40 seconds, under 5 minutes; waits for the server or a session to
answer are cut in the edit, not shown.

How to read a scene:

- **Type** is the exact command, on its own line, copyable. Lines starting with
  `>` in a Claude Code session are what the person types to Claude.
- **Say** is the narration: short, plain, for someone new to mash.
- **Result** is marked `[recorded output]`. The recording supplies it; this
  script does not guess it.
- **Length** is approximate.

What mash is, for the narrator: a Claude Code plugin that gives a session an
inbox and an address. It does not run sessions; the person starts each one.

---

## Scene 1: what you will see (about 0:15)

Type: nothing; a title card, then the terminal.

Say: "mash lets Claude Code sessions send each other mail. Each session has an
address and an inbox that keeps its mail until it is read, even while the
session is off. In the next few minutes: install it, make two endpoints, Alice
and Bob, and have two Claude Code sessions mail each other."

Result: `[recorded output]` (title card only).

## Scene 2: check the download, then install (about 0:40) (guide steps 1 and 2)

Say: "We use the names acme, demo, alice and bob. Their addresses are written
owner, group, name: acme.demo.bob. First, the install. We do not pipe a
download into a shell. We download the script, check it, then run it."

Type:

```
BASE=https://github.com/bogworks/mash/releases/download/v0.5.6
```

```
curl -fsSLO $BASE/install.sh -O $BASE/mash-local-nats-setup.sh -O $BASE/SHA256SUMS -O $BASE/mash.intoto.jsonl
```

Say: "The release's checksum file is vouched for by a signed attestation from
GitHub. This checks it, using the GitHub command line, gh."

Type:

```
gh attestation verify SHA256SUMS -R bogworks/mash --bundle mash.intoto.jsonl --signer-workflow bogworks/mash/.github/workflows/release.yml --source-ref refs/tags/v0.5.6
```

Result: `[recorded output]` (the verification succeeds).

Say: "Then the scripts against the checksums."

Type:

```
grep ' install.sh$' SHA256SUMS | sha256sum -c -
```

```
grep ' mash-local-nats-setup.sh$' SHA256SUMS | sha256sum -c -
```

Result: `[recorded output]` (both report OK).

Say: "Now the install script runs. It checks both programs against the same
checksums before it installs anything. If you have no gh, or an old one, the
script does not stop: it says the checksums come from the same download, so it
trusts github.com's secure connection, and carries on. Install gh and run the
check above if you want the stronger proof."

Type:

```
bash install.sh
```

Result: `[recorded output]` (one install line for each of mash and mashctl).

Type:

```
mash version
```

```
mashctl version
```

Say: "Both print the same version."

Result: `[recorded output]` (the same version twice).

## Scene 3: a server and two endpoints (about 0:50) (guide steps 3 and 4)

Say: "mash keeps mail on a NATS server, an open-source message server. This
script makes a small one on this machine only, run as you. Then mashctl, the
administrator's tool, reads the setup it wrote."

Type:

```
bash mash-local-nats-setup.sh --owner acme
```

Result: `[recorded output]` (the script's last line names the setup file).

Type (use the path the script printed last; the default is shown):

```
mashctl init --config ~/mash-nats/mash-setup.yaml
```

Result: `[recorded output]`

Say: "An endpoint is one agent's mailbox, with its own address and its own key.
One command makes it, one installs it. Whoever holds the key is that endpoint,
so the join file is kept private."

Type:

```
mashctl endpoint create acme.demo.bob --out acme.demo.bob.join
```

```
mash join acme.demo.bob.join
```

```
mashctl endpoint create acme.demo.alice --out acme.demo.alice.join
```

```
mash join acme.demo.alice.join
```

Result: `[recorded output]` (created and joined, for each; the guide shows
`Created endpoint ...`, `Join file: ...`, `Joined as ...`).

## Scene 4: status and who (about 0:25) (guide steps 4 and 5)

Say: "MASH_ENDPOINT says which endpoint mash acts as. Status says whether mail
works for it. Offline is normal here: no session is holding the endpoint open
yet."

Type:

```
MASH_ENDPOINT=acme.demo.bob mash status
```

```
MASH_ENDPOINT=acme.demo.alice mash status
```

Result: `[recorded output]` (the guide shows `ready, offline` for each).

Say: "Who lists the endpoints you can see, including offline ones, because
their mail waits for them."

Type:

```
MASH_ENDPOINT=acme.demo.bob mash who
```

Result: `[recorded output]` (both addresses, state offline).

## Scene 5: add the plugin to Claude Code (about 0:15) (guide step 7)

Say: "The mash plugin gives a Claude Code session its inbox. It does not run
the session; you start it."

Type:

```
claude plugin marketplace add bogworks/mash
```

```
claude plugin install mash@mash
```

```
claude plugin list
```

Result: `[recorded output]` (mash shown as enabled, at the version `mash
version` printed).

## Scene 6: two sessions exchange mail (about 0:55) (guide step 7)

Say: "Two terminals. Each session starts as its own endpoint. The flag is
Claude Code's own: it is how a plugin's wake-up channel is switched on, and
Claude Code asks you to confirm it. Without it mail still arrives, with the
next prompt."

Type, terminal 1 (Bob):

```
MASH_ENDPOINT=acme.demo.bob claude --dangerously-load-development-channels plugin:mash@mash
```

Type, terminal 2 (Alice):

```
MASH_ENDPOINT=acme.demo.alice claude --dangerously-load-development-channels plugin:mash@mash
```

Result: `[recorded output]` (both sessions open after the confirmation).

Say: "Ask a session for its status, from inside."

Type, in Bob's session:

```
> Run the mash status tool.
```

Result: `[recorded output]` (the guide shows `acme.demo.bob: ready, online`).

Say: "Now Bob sends Alice a note, in plain words."

Type, in Bob's session:

```
> Send acme.demo.alice a note: lunch at noon?
```

Result: `[recorded output]` (Bob's session calls `send`; Alice's session shows
mail from `acme.demo.bob`, labelled `[verified]`: that label means Bob's own
key signed it).

Say: "Alice's session shows it as mail from Bob, verified. Alice answers, and
Bob sees the answer."

Type, in Alice's session:

```
> Reply to acme.demo.bob: yes, noon works.
```

Result: `[recorded output]` (Alice's session calls `send`; Bob's session shows
the answer).

## Scene 7: mail waits for a session that is stopped (about 0:35) (guide steps 6 and 7)

Say: "Alice's session is closed now. Her endpoint still exists, so her mail
waits for her. Bob sends one more note."

Type, in Alice's session, to close it:

```
> /exit
```

Type, in Bob's session:

```
> Send acme.demo.alice a note: bring the notes from Monday.
```

Result: `[recorded output]` (Bob's session reports it sent).

Say: "Nothing is lost. From the command line: Alice's endpoint is ready, but
offline, and the note is waiting."

Type:

```
MASH_ENDPOINT=acme.demo.alice mash status
```

```
MASH_ENDPOINT=acme.demo.alice mash inbox
```

Result: `[recorded output]` (status `ready, offline`; the inbox lists the
waiting note as `new`).

Say: "Now Alice's session starts again, and the note is there."

Type, terminal 2:

```
MASH_ENDPOINT=acme.demo.alice claude --dangerously-load-development-channels plugin:mash@mash
```

Type, in Alice's session:

```
> Do I have any mail?
```

Result: `[recorded output]` (Alice's session shows Bob's waiting note,
`[verified]`).

## Scene 8: an urgent message wakes an idle session (about 0:35) (guide steps 6 and 7)

Say: "Alice's session is idle: nothing is being typed in it. An ordinary note
waits for her next turn or the wake-up. Mail marked urgent does not wait: it
wakes the idle session at once. Use it only for what cannot wait."

Type, in Bob's session:

```
> Send acme.demo.alice an urgent question: is the build green?
```

Result: `[recorded output]` (Alice's idle session starts a turn by itself, led
by a line that says the mail is urgent, and shows the message from Bob).

Say: "Alice's session was woken by the mail. The mail arrives through the
plugin's hook, and mash shows who sent it and that it is verified."

## Scene 9: who is online now (about 0:10) (guide steps 5 and 7)

Say: "With both sessions open, both endpoints are online."

Type, at a shell in a third terminal:

```
MASH_ENDPOINT=acme.demo.bob mash who
```

Result: `[recorded output]` (both addresses, state online).

## Scene 10: take an endpoint away (about 0:25) (guide step 9)

Say: "To remove an agent, close its session first. Then the administrator
deletes its endpoint; you type the address to confirm. Its key stops working,
and its address cannot be used again."

Type, in Bob's session, to close it:

```
> /exit
```

Type:

```
mashctl endpoint delete acme.demo.bob
```

Result: `[recorded output]` (asks for the address; the person types
`acme.demo.bob`).

Type:

```
MASH_ENDPOINT=acme.demo.bob mash status
```

Result: `[recorded output]` (mash says the endpoint was deleted and the command
exits with a non-zero status; the guide does not give the exact wording).

Say: "Bob is refused. Alice and her mail are untouched."

## Scene 11: what this video does not show, and where to go (about 0:20)

Say: "Plainly, what you did not see. Synadia Cloud: mash 0.5.6 does not
support it. Interoperating with other agents that use the Synadia Agent
Protocol, beyond the address form: not shown. And nothing from the 0.6.0 list.
We also ran on one machine. Two machines need a NATS server both can reach,
which you run yourself in 0.5.6; step 10 of the guide explains it. Everything
here is written out, with each command, in the guide."

Type: nothing; the closing card.

Closing card (on screen, a few seconds):

```
mash 0.5.6
README:        https://github.com/bogworks/mash#readme
Walkthrough:   docs/guide/walkthrough.md
               https://github.com/bogworks/mash/blob/main/docs/guide/walkthrough.md
```

Result: `[recorded output]` (card only).

---

## Not shown

- Synadia Cloud (not supported in 0.5.6; see [docs/synadia.md](../synadia.md)).
- Interoperating with Synadia Agent Protocol (SAP) agents beyond the address form.
- Anything on the 0.6.0 list, including a script option for two machines.
- Reading mail as JSON for scripts (guide step 8), and the day-2 server
  commands (guide step 11).

## Before recording

- [ ] A clean container or VM, made for this take and destroyed after it. No
      recording on a working host, and no test accounts on one.
- [ ] The prerequisites from the guide's "Before you start" are in the image,
      Claude Code is signed in and `gh` is current (for the attestation
      check). Nothing is listening on 127.0.0.1:4222.
- [ ] No `MASH_*` variables set except the ones this script types.
- [ ] Terminal font large enough to read at 720p; wide enough that no command
      wraps; the same size for all three terminals.
- [ ] No secrets on screen: no tokens, no environment dump, no creds or
      join file contents. Do not `cat` a join file.
- [ ] No personal paths or names: a throwaway user and host name, Alice and
      Bob only.
- [ ] Rehearse the session start once, so the confirmation for the channel
      flag is not a surprise.
- [ ] The release is published (v0.5.6) before the take; do not record on a
      build from source.
- [ ] Recording tool: left to the person recording.
