# Reviewer notes: the README quick start and Day 2 (v3)

Not README text. These notes go with
`docs/plans/2026-11-19-readme-quickstart-plain-mash.md` (the quick start) and
`docs/plans/2026-11-19-readme-day2-plain-mash.md` (Day 2). The two pages carry no
notes section and no internal marks: everything a reviewer needs is here.

Built on the targets DRAFT12d (`docs/plans/2026-11-19-cli-targets-mash.md`, contract
revision 26) for the ruled scope (Randy's decision 264). Regenerated after DRAFT12 (contract revision 26, still final; the lead's rulings of 11:17Z: the quick start is the Debian or Ubuntu path the helper supports, with no other platform described; one wording for a server that refuses the key and token, which this page does not print; the page says why the pasted install refuses a folder that others can write to or a link). After DRAFT11 (contract revision 26 final; the lead's rulings of 11:08Z: the pasted install refuses a destination folder, or a folder above it, that is a link or that other users can write, and prints the refusal; `mash endpoint status` has no mailbox column; the answer `no` keeps the `error:` label; the refusals for a server that refuses and for one that does not answer are written). Earlier, after DRAFT10 (contract revisions
18 to 26: revisions 22 to 26 appeared while the regeneration ran and were applied): every install takes `--account <key>` (the operator's account key, copied whole) and the first install confirms three values before it writes anything; the
fingerprint is in groups of eight separated by spaces; the count of the mailbox row is unacknowledged mail and
reading acknowledges. DRAFT12d is a conformance pass: every `mash status` screen on the two pages
now has six rows (a `session` row is added), a server row that says only whether
the server is reachable and no address (`mash server status` shows it), and the
mailbox-over screen names the time mash woke the session. No steps or commands
changed. The lead's count of what is
counted is `readme-step-count.md` (scratchpad), regenerated with the pages by
`p2/plain-gen.py`, `p2/count-report.py` and this file's `p2/notes-gen.py`.

## Real size

| | lines | prose and headings | inside fenced blocks and markers | widest printed block | widest typed block |
|---|---|---|---|---|---|
| quick start | 631 | 333 | 298 | 87 columns | 88 columns |
| Day 2 | 401 | 199 | 202 | 87 columns | 76 columns |

The quick start is 26 pastes, 77 lines and 10 other actions for the whole
page; the first mail is sent after 11 pastes (52 lines) with the
prerequisites and 4 without (6 lines); Alice reads it after
13 pastes (6 without). Without the prerequisites the whole page is
19 pastes and 31 lines. Lines are now physical lines of a paste (v2 counted a
wrapped `&&` chain as one; the junior reader saw 11 lines in the download block and
read "18 lines" as smaller than what was pasted).

## Outputs and the lint

Every printed block is copied by `p2/plain-gen.py` from the targets by its
`<!-- packet: key -->` marker and checked byte for byte by `p2/lint-plain.py` (outside
the repository). The lint also rejects: PENDING, UNVERIFIED, lane, external,
placeholder, team names, "Notes for reviewers", "the server signed" and "signed by
the server", `-v2`, a marker comment that is not `from`, `action` or `alt`, a
fingerprint that is not groups of 8 lower-case hex digits separated by single spaces, four to a line (eight groups on one line, the old `-` joined form, a `:` form and upper case are refused), an account shown shortened or not 56 characters, a printed
block wider than 80 columns (a command on its own line is exempt, as in the targets' rule R9: the hint commands with `--account` are 87 columns, so the lint's check now skips a command-position line), the cut scope (trust record, generations, plugin
install, install script, `MASH_INSECURE_PLAINTEXT`, the plain-text override before
the failing-status section of Day 2), and it requires the lead's verbatim sentences
(the token sentence on the quick start; the two "never travels" lines, the
requirements-page URL, the deliberate-refusal heading and the lead's question
`Do these three match what the operator sent you? (yes/no)` on Day 2). Negative tests:
`p2/neg3.py`, 37 cases (six new for the fingerprint and account forms and the question), all rejected, both pages pass as controls.

Not printed on purpose: the targets' `install-check.2` (the `gh` report; its widest
line is 138 columns, over the 80 limit, and the junior found it frightening; the page
describes it in one sentence), `status-noconfig` (its fix line was 121 columns in the earlier draft, measured; the block now matches the status-views page and is wrapped, widest line 76, so width is no longer the reason; the pages do not reach that state), and
the
`key`, `server`, `install-confirm`, `install-main` and the other install blocks of the other-machine flow (they name
`bob`, which the quick start has retired; the lead wants `carol` and no invented
output, so Day 2 gives those commands, and what the confirmation shows, in prose).

## What is not built, and where it sits

All of it is under one heading on each page, "Not available until the 0.5.6 release",
in plain sentences, or is off the page.

- `mashctl` as a release asset. The targets' `install-check` blocks (1, 4, 5) already
  show `mashctl-linux-amd64` in the one paste, and the guide's part 4 downloads it; the
  release does not carry it yet. The page installs `mash` by the checked paste and
  `mashctl` by `go install` (so Go 1.27.4 stays a prerequisite). When the asset exists:
  the paste fetches both, `install-check.4` and `.5` become printable, and Prerequisite
  2 command 2 and the Go requirement go.
- `mashctl adopt --from <dir>`: the targets' `adopt-from` block is a placeholder (ctl's
  text is not in the targets). The page shows the command and no output. The page cannot
  be followed to the end until it exists (it also copies the CA, which 0.5.5's adopt
  does not).
- The helper's closing summary ("What you hold") is not printed: the guide revision 7's
  box is the real output of its test but its CA fingerprint is `sha256:72ce00ec 3b2b4a33
  ...` (a prefix and five groups), not the one form the lead fixed (groups of eight
  separated by spaces, eight groups, two lines of four), and it shows no account; it ends "what a
  person on ANOTHER machine types to trust this server", which the contract now
  contradicts (the person confirms three values, a yes or a no, and types nothing else). Both need a change in the guide
  and the helper before the box can be printed. The page lists the three things the
  summary names (address, CA file, folder), which are the guide's.
- Reading acknowledges (contract rev 21, 26). The sentences "a message stops waiting when the
  session reads it" (Step 3) and "a message stops waiting when the session is shown it in full or
  reads it" (Day 2) are true only after three changes that are not built: the `read` tool
  draining any waiting message, `mash read` writing the drained tombstone (so with no session
  running the count drops when the next session starts; no direct acknowledgement in 0.5.6), and
  `mash serve` writing the count into the lease (targets section 5b). Today `mash read` never
  acknowledges and the `read` tool only some messages.
- The `--account` flag and the first install's confirmation (yes or no, in a terminal) are not built; Day 2 describes it
  in prose and does not print it. An agent or script that runs the install is refused (exit 4):
  the page says so in one clause.
- Claude Code's own output and questions (`claude plugin marketplace add`, `claude plugin
  install`, the flag confirmation, a folder question, a permission question before the
  send tool) are not shown and are not counted. Not verified.
- `mashctl endpoint reissue` (ctl's B1 text, revision 19) is off Day 2 except one
  sentence under the heading. The v2 section also said "make a new key here", which for
  an endpoint on another machine sent the private key across, against the rule that it
  never travels; a `reissue --public-key` form is not in the targets.
- The v0.5.6 release files (`mash-linux-amd64`, `mash.intoto.jsonl`, a `SHA256SUMS`
  that is a subject of the bundle) were not run: only v0.5.5 was. The paste and the
  Day 2 failing screen (`install-check.3`, `gh`'s own text from a real run on the
  v0.5.5 bundle) depend on them.
- The helper's `--stop`, `--start` and `--remove` print nothing on the pages (not in
  the targets), and `--remove` needs a terminal and a typed directory name (guide, part 6).

## Differences between the lead's text and the targets (kept the targets)

- The make-key output now agrees: the targets (DRAFT9) carry ctl's first line (`Made a key
  pair for acme.demo.bob: ...`) and the `server` block says that mashctl's signing key signed the
  token; the generated blocks changed and nothing on a page was edited by hand. (Earlier notes listed
  both as differences; they are closed.)
- Placeholder values in the targets' blocks. The fingerprint (`3f9a41c2 7b0d9e55 ...`) is
  ctl's sample and the account (`ADQ6KRG4JFLSMDNOLVYUFUXM3JOVHOS5OJZPTIFCCRPM3DTQKTE4VLXF`) is
  a made-up 56-character key that starts with `A`; neither is derived from any real CA or
  token. Neither is on a page (Day 2 describes the three values in prose); a reviewer who
  reads the targets must not look for them in a real run.
- The three values' form. The page text and the targets agree: `server address`, `CA fingerprint`
  (two lines of four groups of eight) and `account` (the whole key, 73 columns with its label),
  as ctl prints them for the operator, and the question is the lead's verbatim. `--make-key`
  shows no confirmation (the same machine and user); the contract asks its output to say in one
  line why no fingerprint is shown, and that line is ctl's text, not in the targets' block.
- The first install asks for a person at a terminal, on Day 2 only; the quick start never runs it
  (same machine: `--make-key`). The contract says an agent never runs it, and the page gives the
  command to the person.
- Contract rev 24 to 26, REQ-011 (i), now CONFORMED (DRAFT11): the pasted install (Prerequisite 2) refuses a
  destination folder, or any folder above it, that is a symbolic link or that another user can write, before it
  copies, and prints the two refusals. The paste grew from 14 lines to 31 (the check is 16); it uses `[ -L ]` and
  `find -maxdepth 0 -perm`, and the page is Linux only. DRAFT12 adds one sentence, where the refusal is shown, on why
  it refuses (a folder others can write to, or a link, lets someone swap mash for their own program after the
  check). A folder made
  `0775` (common on Linux accounts) is refused, and the fix is a `chmod go-w` on that folder. UNVERIFIED: that
  the real 0.5.6 release and a real run behave as the mockup; nothing was run against a release.
- The status-views page (DRAFT4) and the targets now agree: `mash endpoint status` has no mailbox column (contract rev 26).
- The targets' `status-deleted` shows `DELETED`; the server must say so for status to
  (targets N11). Until then the screen on the page may read REFUSED. Both pages show the
  targets' DELETED.
- `ctl-delete`: ctl's revision 19 adds the line "Its credentials file here was left as it
  was..." that the targets do not have. v2 said so in prose as if the screen printed it
  (junior finding); v3 says only what the screen shows.

## Contradictions and choices

- The guide makes its own `acme.demo.alice` and `acme.demo.bob`, the quick start's
  Step 1 makes them too, and a deleted address cannot be used again. The page says to
  use the guide or the page, not both. The guide's own "What next" sends the reader to the
  repository README's "Quick start: one machine, no account" ("launch a session" step),
  not to this page: the README and these pages must be reconciled when the pages replace it.
- Wake text. "Past 15" (REQ-015 says when the backlog passes the limit; the brief said
  "at 15"). Step 3 now explains `mash serve` and the kinds of wake in one paragraph:
  the hook delivers waiting mail when the session next acts, a channel wake pushes a
  message to an idle session, and past 15 messages `mash serve` wakes the session at most
  every 10 minutes (targets `serve-backlog`, `status-mailbox-over`).
- REQ-011 (iv): the quick start has no deliberate refusal; `status-choose` moved to Day 2
  under "When two endpoints are installed and none is chosen" (the lead's #6). Step 4 is
  now two commands.
- `mash key create` is introduced in one sentence before its first use, which is the fix
  line of `status-deleted` in Step 5.
- The targets and the helper both use `tls://localhost:4222` on one machine now
  (DRAFT8): regenerated, the "screens show myserver" sentence is gone.
- Sized choices: the page is the Debian or Ubuntu amd64 path (the helper runs only there); the download paste works in `~/mash-setup/dl` and ends in `$HOME` so Day 2
  can remove the downloads and the plugin folder by a known path; the helper's one long
  line is wrapped with variables; Step 5 removes the three dead credentials files and
  ends with the `status-none` screen (a failing screen by design, said so on the page).
- Day 2's "another machine" uses `carol` (the quick start retires bob, alice and you),
  only in prose and commands. Its delete section is the same command as Step 5, for
  `carol`, with the proof described.

## If the scope changes

- A ruling that brings back the trust record changes `install-main` and `status-noconfig`
  first (neither is printed now); Day 2's other-machine step 5 would then say what it asks.
- `mashctl` as a release asset: see above (shorter Prerequisite 2, no Go).
- `reissue` built: the Day 2 heading section becomes a section again, with the targets'
  block by key.
- If the guide stops making alice and bob (or makes other names), the "not both" sentence
  goes.
- A fix to the targets' make-key text changes only the generated blocks.

## The junior's walk of v2: every finding and what was done

FIXED means changed on the page; CHANGED means handled in another way than the finding
asked; NOT TAKEN has the reason.

Opening and diagram
1. "NATS server" never explained: CHANGED. The diagram says "where the mail waits" and
   Prerequisite 3 says the mail waits on a NATS server.
2. "from a fresh machine" against the assumptions; "Nothing else is needed": FIXED. The
   opening states the real prerequisites (Debian or Ubuntu amd64, Claude Code with
   channels, `gh`, Go, a `sudo` for a missing tool); both phrases are gone.
3. The bold count paragraph is for auditors: CHANGED. Kept near the top (the lead wants
   it), not bold, in plain sentences, recomputed.
4. `owner.group.name`, group, why `acme`: FIXED. One sentence under the diagram.

Prerequisite 1
5. No fix for an old Go: FIXED. "install one from go.dev/dl".
6. "with channels (a claude.ai or Console sign-in)" unclear: FIXED. Reworded; a channel
   is "the way mash wakes an idle session".
7. `gh --version` does not show `gh attestation`: CHANGED. The failing paste says what
   a missing `gh attestation` looks like and installs nothing.

Prerequisite 2
8. A second platform's block first on a Linux page: FIXED (DRAFT12 ruling: no second platform anywhere).
9. A wall of `gh` text: CHANGED. The report is not printed (also 138 columns wide); the
   page says it ends with "Verification succeeded!" and that `sha256sum` prints `OK`.
10. Where the files land: FIXED (`~/mash-setup/dl`, back to `$HOME`).
11. Leftover `SHA256SUMS` and `mash.intoto.jsonl`: FIXED (inside `~/mash-setup`, which
    Day 2 removes; the old `rm SHA256SUMS ...` line is gone).
12. "UNVERIFIED: only v0.5.5 was run": FIXED on the page; recorded above.
13. `gh` without a login: nothing to do.
14. `go install` shows no output and is unchecked: CHANGED. One plain sentence says it is
    built on your machine and not checked against the release.
15. Is the repository public: NOT TAKEN. Not known from the targets or the guide.
16. `mashctl` never checked: NOT TAKEN. No `mashctl version` output is in the targets
    (the guide's "one line" is not); Prerequisite 4 is its first run.
17. The PENDING release-lane paragraph: FIXED. Plain sentence plus the heading section.

Prerequisite 3
18. "CA" never spelled out: FIXED ("the authority that signed the server's certificate").
19. The guide paragraph arrived after Prerequisite 2: FIXED. It moved to the top of the
    prerequisites, names the three questions, and says what a reader holds (it is a
    separate route; use it or the page).
20. "adopts the server" before adopt is introduced: FIXED (reordered).
21. "seven steps" and the box with `/home/alice`: FIXED. Not printed (see above).
22. "Owner acme", CA fingerprint dropped silently, "no step here moves them": FIXED. The
    box is gone; the owner is explained; one sentence says the server folder holds the
    CA's private key.
23. `myserver` against `localhost`: FIXED in the targets DRAFT8; regenerated.
24. The six tools and `sudo`: FIXED (named in Prerequisite 3 and the opening). A reboot
    is not warned: FIXED. The warning is bold in Prerequisite 3 with the start command,
    and again on Day 2.

Prerequisite 4
25. "(PENDING the mashctl lane: ...)": FIXED. Off the body, in the heading section.
26. "No fingerprint asked": CHANGED. Replaced by "it asks for nothing, because the folder
    is yours".
27. Two kinds of keys: FIXED. "mashctl's signing key: the key that signs every
    endpoint's token"; each endpoint has its own key (Step 1).
28. "Copied 5 files": FIXED (output not shown).
29. "no longer needed to make mailboxes" invites deleting the folder: FIXED ("keep it: the
    server runs from it").

Step 1
30. What a token is and who signs: FIXED. The lead's verbatim sentence; "the server signed"
    is gone everywhere (lint).
31. Five nouns for two things: FIXED by that sentence, and by "a key pair (a private half
    ... and a public half)".
32. Where the public half goes: FIXED (the token says "this public key is this address").
33. The `mailbox` row's "wake" too early: FIXED (it moved to Step 3).
34. The hint names Alice, Step 2 uses Bob: FIXED (a sentence says so).
35. The make-key output does not say what it made: FIXED. The targets carry ctl's first line (DRAFT9) and the page prints the block.

Step 2
36. `--urgent --kind` unexplained: FIXED. 37. Message 1 stays `new`: FIXED (a sentence).
38. "Why a mail tool says treat as information": NOT TAKEN; Step 3 shows the reader.

Step 3
39. "write it out": FIXED ("export the plugin to a folder"). 40. UNVERIFIED claude lines:
    CHANGED to the heading section.
41. "marketplace", "mash@mash": FIXED (one sentence).
42. What to confirm and how: CHANGED. The page says it is Claude Code's own and to answer as it
    asks.
43. A block shown that the reader will not see: FIXED ("what you see is her agent acting on it,
    not this text") and a proof is given: `mash inbox` as Alice (Command 5), no invented screen.
44. Permission and folder prompts: CHANGED to the heading section.
45. Stuck if nothing happens: FIXED (`mash status` and the Day 2 screen).
46. Where the plugin folder is written: FIXED (`cd "$HOME"` in the first paste).

Step 4
47. The refusal: CHANGED. It moved to Day 2 (the lead's #6).
48. The name `you`: FIXED (a sentence).

Step 5
49. Three dead credentials files, `MASH_ENDPOINT` left on a deleted endpoint: FIXED
    (Command 5 removes the files and unsets it; Command 6 shows `status-none`).
50. "its inbox, with 1 message": where from: FIXED (the count is the endpoint's own).
51. How to close a session: FIXED ("type `/exit`").
52. "Nothing follows" then "the next command": FIXED (wording).
53. The page said the screen shows a "left as it was" line that it does not: FIXED.
54. The fix names `mash key create`: FIXED (one sentence before the command).
55. REFUSED against DELETED: recorded above.
56. The link to Day 2: FIXED (the final file name; `-v2` rejected by the lint).

The old "Notes for reviewers" section: FIXED. It is this file.

Part 2 (how an agent gets its address): FIXED by the token sentence, the two Day 2 lines
and "mashctl's signing key". The word "seed" appears on neither page.

Part 3 (obstacles and tasks)
57. Obstacle 1 (PENDING in the body): FIXED. 58. Obstacle 2 (Day 2 on another machine
    cannot work after the quick start): FIXED. One sentence on reach, the requirements
    page by full URL, `carol` instead of a retired name. 59. Obstacle 3 (nouns, signer):
    FIXED. 60. Obstacle 4 (Step 3): CHANGED as above. 61. Obstacle 5 (fresh machine,
    prerequisites): FIXED.
62. Task (c), the "server did not answer" fix does not give the helper's `--start` or
    mention reboots: CHANGED. The fix is the targets' text; Day 2 says to start the helper
    again after a reboot, next to the screens.
63. Task (d), where the token is written, which `ca.pem`, the PATH lines calling `go`, `gh`
    needed there: FIXED (the folder you ran it from; `~/.config/mash/ca.pem`; only
    `~/.local/bin` on the PATH; `gh` named).
64. Task (e), a session you cannot close, the dead file: FIXED in part (Day 2 says to close
    it first, if one is running, and to `rm` the file); a session on another machine that
    cannot be closed is not covered.
65. The count (11 and 13 pastes matched; 38 lines read small): CHANGED. Lines are physical
    now. Not counted, and said so in the opening: installing Claude Code, `gh` and Go, and
    Claude Code's own questions.

Part 3 (5), between the pages
66. Links to `-v2` names: FIXED. 67. Retired addresses reused: FIXED (`carol`). 68. A local
    server against a second machine: FIXED. 69. "a deletion with its proof" announced as new:
    CHANGED (Day 2's is for the other machine). 70. Two fingerprint formats: FIXED on the
    pages (one format, now groups of eight separated by spaces on two lines); the guide's box must change. 71. PATH lines calling `go` on the second
    machine: FIXED. 72. `mash key create` first appears in a fix: FIXED. 73. `mash serve` never
    introduced and two wakes not told apart: FIXED (Step 3).

Within the quick start (not listed above)
74. "Only `mash version` is checked": NOT TAKEN (see 16). 75. Guide alternative after
    Prerequisite 2: FIXED (see 19).

Within Day 2
76. Reissue with `--make-key` against "never travels": FIXED (reissue is off the page).
77. "types back the directory name": FIXED (wording). 78. `rm SHA256SUMS ...` from an unnamed
    folder: FIXED. 79. Exit codes read oddly: FIXED (wording). 80. "delete it with `rm`" in
    the quick start too: FIXED (Step 5). 81. `[unknown-signer]` appeared only in the reissue
    screen: FIXED (gone with it). 82. "mail she sent in her inbox": FIXED (gone with it).

## Decisions beyond the brief

- No `gh` report is printed (80-column limit) and no `status-noconfig` block (no step on the pages reaches that state).
- The helper's long line is wrapped; the download paste changes directory and returns.
- Step 5 gains two commands (remove the dead credentials files, show `status-none`);
  Step 3 gains one (`mash inbox` as the proof).
- The quick start has one platform, Linux amd64.
- Day 2's delete section describes the proof instead of printing bob's screens.
- Lines are counted physically.
