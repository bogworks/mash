# Demo video script: the quick start, filmed

Status: draft for review. Text only; nothing is recorded. It is recorded later,
on the published release, and the script and then the video are approved before
anything is published.

It films the [quick start](../guide/quickstart.md), step for step, in about two
minutes. Everything is installed as the local user: nothing on screen uses
`sudo`, a package manager, a system service or a pipe into a shell. Waits for
Claude Code or the setup to answer are cut in the edit, not shown.

How to read a scene:

- **Type** is the exact text, on its own line, copyable. A line starting with
  `>` is what the person types to Claude Code.
- **Say** is the narration: short, plain, for someone new to mash.
- **Result** is marked `[recorded output]`. The recording supplies it; this
  script does not guess it.
- **Length** is approximate.

---

## Scene 1: what you will see (about 0:10)

Type: nothing; a title card, then the terminal.

Say: "mash gives each Claude Code session an inbox. Two sessions, one machine,
and mail that waits."

Result: `[recorded output]` (title card only).

## Scene 2: two lines to install (about 0:15)

Type:

```
claude plugin marketplace add bogworks/mash
```

```
claude plugin install mash-setup@mash
```

Say: "Two lines add the setup skill. Everything installs in your home folder,
with no sudo."

Result: `[recorded output]`.

## Scene 3: one sentence to the agent (about 0:35)

Type, in a Claude Code session:

```
> Set mash up on this machine, with two agents: acme.demo.alice and acme.demo.bob.
```

Say: "One sentence. The setup skill asks before each step: the programs it
needs, a local NATS server, the mash plugin, and the two agents. I say yes."

Result: `[recorded output]` (the questions and the answers; the downloads are
cut in the edit).

## Scene 4: two sessions (about 0:15)

Type, in two terminals:

```
MASH_ENDPOINT=acme.demo.bob claude
```

```
MASH_ENDPOINT=acme.demo.alice claude
```

Say: "One session as Bob, one as Alice. Claude Code asks whether to trust the
folder; yes."

Result: `[recorded output]` (two terminals side by side).

## Scene 5: Bob's note, Alice's answer (about 0:25)

Type, in Bob's session:

```
> Send alice a note: lunch at noon?
```

Type, in Alice's session:

```
> Do I have mail?
```

Say: "Bob sends a note. Alice asks for her mail, and there it is, marked
verified: signed by Bob."

Result: `[recorded output]` (Bob's send; Alice's answer with the note, marked
verified, after the setup's own test note).

## Scene 6: mail waits (about 0:20)

Type: close Alice's session. In Bob's session:

```
> Send alice another note: bring the notes
```

Then start Alice's session again:

```
MASH_ENDPOINT=acme.demo.alice claude
```

```
> Do I have mail?
```

Say: "Alice's session is closed. Bob sends again. The mail waits. When Alice's
session opens, it is there."

Result: `[recorded output]` (the second note in Alice's inbox).

## Closing card (about 0:05)

Type: nothing; a card with the address of the quick start, and "By hand, the
same thing typed yourself: docs/guide/by-hand.md".

---

## Not shown

Typing the commands by hand, `mash status`, the endpoint directory, taking an
endpoint away, waking an idle session, and a second machine. They are in the
[by-hand guide](../guide/by-hand.md), [join](../guide/join.md) and the
[install guide](../install.md).

## Before recording

- A clean machine or container, made for the recording and destroyed after.
- The published release, the setup skill from the marketplace, and Claude Code
  signed in.
- Names acme, demo, alice and bob, as in the quick start.
- Check the script against the quick start the day of the recording; the
  quick start wins.
