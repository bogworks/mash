# Demo video script: two Claude Code sessions exchange mail

Status: draft for review. Text only; nothing is recorded. It is recorded later,
on the published 0.5.6 release, and the script and then the video are approved
before anything is published.

It follows [docs/guide/walkthrough.md](../guide/walkthrough.md), its Quick
start and steps 1 to 10, and uses its commands. Nothing on screen uses `sudo`,
a package manager, a system service or `curl | bash`: everything is installed
as the local user. Alice and Bob are the two endpoints. Total: about 5 minutes
25 seconds for the default flow, and 50 seconds more if the optional scene 9
is included; waits for the server or a session to answer are cut in the edit,
not shown.

How to read a scene:

- **Type** is the exact command, on its own line, copyable. Lines starting with
  `>` in a Claude Code session are what the person types to Claude.
- **Say** is the narration: short, plain, for someone new to mash.
- **Result** is marked `[recorded output]`. The recording supplies it; this
  script does not guess it.
- **Length** is approximate.

What mash is, for the narrator: a Claude Code plugin that gives a session an
inbox and an address. It does not run sessions; the person starts each one.
Mail reaches a session with its next prompt or tool call. An idle session is
not woken, unless the person has chosen the optional wake setting (scene 9).

---

## Scene 1: what you will see (about 0:15)

Type: nothing; a title card, then the terminal.

Say: "mash lets Claude Code sessions send each other mail. Each session has an
address and an inbox that keeps its mail until it is read, even while the
session is off. In the next few minutes: install it, the easy way and then by
hand, make two endpoints, Alice and Bob, and have two Claude Code sessions mail
each other. Nothing here needs sudo."

Result: `[recorded output]` (title card only).

## Scene 2: install, with an agent or by hand (about 1:15) (guide Quick start and steps 1 and 2)

Say: "We use the names acme, demo, alice and bob. Their addresses are written
owner, group, name: acme.demo.bob. First, the install. The easy way is to let
Claude Code do it. Everything is installed as you, in your home folder: no
sudo."

Type:

```
claude plugin marketplace add bogworks/mash
```

```
claude plugin install mash-setup@mash
```

Say: "Then ask a session to set mash up. It downloads the NATS programs and each mash script from the release, checks each against its checksums, shows you the script, and asks before it runs it, as you, never piped into a shell."

Type, in a Claude Code session:

```
> set mash up on this machine
```

Result: `[recorded output]` (the session shows each checked script and asks
before running it; cut the waits in the edit).

Say: "If you want to see each step by hand, here it is. We never pipe a
download into a shell. We download, check, then run. First the two NATS
programs, from their GitHub releases, each checked against its checksums and
put in your own bin folder. This block is pasted whole; it was checked before
the take."

Type (one pasted block):

```
cd "$(mktemp -d)"
install -d -m 755 ~/.local ~/.local/bin
OS=$(uname -s | tr A-Z a-z); ARCH=$(uname -m); case $ARCH in x86_64) ARCH=amd64;; aarch64|arm64) ARCH=arm64;; esac
V=v2.15.1; B=https://github.com/nats-io/nats-server/releases/download/$V
curl -fsSLO $B/nats-server-$V-$OS-$ARCH.tar.gz -O $B/SHA256SUMS
grep " nats-server-$V-$OS-$ARCH.tar.gz\$" SHA256SUMS | sha256sum -c -
tar -xzf nats-server-$V-$OS-$ARCH.tar.gz --strip-components=1 -C ~/.local/bin nats-server-$V-$OS-$ARCH/nats-server
N=0.5.2; B=https://github.com/nats-io/natscli/releases/download/v$N
curl -fsSLO $B/nats-$N-$OS-$ARCH.zip -O $B/SHA256SUMS
grep " nats-$N-$OS-$ARCH.zip\$" SHA256SUMS | sha256sum -c -
if command -v unzip >/dev/null; then unzip -j -o nats-$N-$OS-$ARCH.zip '*/nats' -d ~/.local/bin
else python3 -m zipfile -e nats-$N-$OS-$ARCH.zip natsx && install -m 755 natsx/*/nats ~/.local/bin/nats; fi
```

Say, over the block, no more than 10 seconds: "It makes your own bin folder,
downloads the NATS server and its command, checks each against the published
checksums, and unpacks them there."

Result: `[recorded output]` (both checksum lines report OK).

Say: "Now mash itself."

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
the session; you start it. The marketplace was added in scene 2. If you used
the setup route, this is already done; here it is by hand."

Type:

```
claude plugin install mash@mash
```

```
claude plugin list
```

Result: `[recorded output]` (mash shown as enabled, at the version `mash
version` printed).

## Scene 6: two sessions exchange mail (about 0:55) (guide step 7)

Say: "Two terminals. Each session starts as its own endpoint. Mail arrives
with the session's next prompt or tool call. An idle session is not woken; we
come back to that at the end."

Type, terminal 1 (Bob):

```
MASH_ENDPOINT=acme.demo.bob claude
```

Type, terminal 2 (Alice):

```
MASH_ENDPOINT=acme.demo.alice claude
```

Result: `[recorded output]` (both sessions open).

Say: "Ask a session for its status, from inside."

Type, in Bob's session:

```
> Run the mash status tool.
```

Result: `[recorded output]` (the guide shows `acme.demo.bob: ready, online`).

Say: "Now Bob sends Alice a note, in plain words. Alice's session shows it
with her next prompt, so she asks for her mail."

Type, in Bob's session:

```
> Send acme.demo.alice a note: lunch at noon?
```

Result: `[recorded output]` (Bob's session calls `send`).

Type, in Alice's session:

```
> Do I have any mail?
```

Result: `[recorded output]` (Alice's session shows mail from `acme.demo.bob`,
labelled `[verified]`: that label means Bob's own key signed it).

Say: "Alice's session shows it as mail from Bob, verified. Alice answers, and
Bob sees the answer with his next prompt."

Type, in Alice's session:

```
> Reply to acme.demo.bob: yes, noon works.
```

Result: `[recorded output]` (Alice's session calls `send`).

Type, in Bob's session:

```
> Do I have any mail?
```

Result: `[recorded output]` (Bob's session shows the answer from Alice).

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
MASH_ENDPOINT=acme.demo.alice claude
```

Type, in Alice's session:

```
> Do I have any mail?
```

Result: `[recorded output]` (Alice's session shows Bob's waiting note,
`[verified]`).

## Scene 8: who is online now (about 0:10) (guide steps 5 and 7)

Say: "With both sessions open, both endpoints are online."

Type, at a shell in a third terminal:

```
MASH_ENDPOINT=acme.demo.bob mash who
```

Result: `[recorded output]` (both addresses, state online).

## Scene 9: Optional: wake an idle session (about 0:50) (guide: Optional: wake an idle session)

Include this scene only if the owner decides to (see "Before recording").

Say: "This is optional, and it is your choice. Until now, an idle session
only saw mail when you typed a prompt. To wake an idle session the moment mail
arrives, start it with Claude Code's own research-preview flag,
dangerously-load-development-channels. It is Claude Code's flag, not mash's. It
lets a development channel plugin push a message into a session without being
on Claude Code's approved-channels list. Claude Code asks you to confirm it at
each launch. Whether to use it is up to you."

Type, in Alice's session, to close it:

```
> /exit
```

Type, terminal 2:

```
MASH_ENDPOINT=acme.demo.alice claude --dangerously-load-development-channels plugin:mash@mash
```

Result: `[recorded output]` (the session opens after the confirmation).

Say: "Alice's session is idle: nothing is being typed in it. Mail marked urgent
does not wait for her next prompt: it wakes the idle session. Use it only for
what cannot wait."

Type, in Bob's session:

```
> Send acme.demo.alice an urgent question: is the build green?
```

Result: `[recorded output]` (Alice's idle session starts a turn by itself, led
by a line that says the mail is urgent, and shows the message from Bob).

Say: "Alice's session was woken by the mail, and mash shows who sent it and
that it is verified."

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
here is written out, with each command, in the guide. Where everything lives:
mash and the NATS programs in ~/.local/bin, each endpoint's key in
~/.config/mash, the server and its mail in ~/mash-nats. To remove it all, see
docs/install.md, under Uninstall."

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
- Waking an idle session, unless the optional scene 9 is included.
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
- [ ] Scene 9 (wake an idle session) is optional. Including it is the
      owner's decision, because it shows a Claude Code research-preview flag.
      If it is included, rehearse its session start once, so the
      confirmation Claude Code asks for is not a surprise. If not, cut it and
      scene 10 follows scene 8 (renumber in the edit).
- [ ] `claude plugin marketplace add bogworks/mash` has not been run before the
      take, so scene 2 shows it. Nothing on screen uses sudo.
- [ ] The release is published (v0.5.6) before the take; do not record on a
      build from source.
- [ ] Recording tool: left to the person recording.
