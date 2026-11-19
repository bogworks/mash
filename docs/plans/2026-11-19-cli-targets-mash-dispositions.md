# Dispositions: the review of DRAFT1 and what DRAFT2 did with each finding

Date: 2026-11-19. Source: the independent CLI-UX review of DRAFT1 (the
five serious problems, every command-by-command note, the colour notes and the
answers to the 13 open questions). Target document: `docs/plans/2026-11-19-cli-targets-mash.md`
(DRAFT2). Line numbers are lines of that file. Every finding has one row.
Dispositions: FIXED (done as the review asked, or as the planner's rules R9 and
R10 say), CHANGED OTHERWISE (the finding is met by a different change, stated),
NOT TAKEN (with the reason).

Rules in DRAFT2: R1 (line 30), R2 (line 33), R3 (line 42), R4 (line 56), R5 (line 64), R6 (line 67), R7 (line 73), R8 (line 78), R9 (line 80), R10 (line 90), R11 (line 101), R12 (line 110). N1 to N7 and Q13 to Q17 are in section 5 (line 1857).

| # | finding (review section) | disposition | where in DRAFT2, or why not |
|---|---|---|---|
| 1 | Problem 1, pasteable NEXT/fix: NEXT glued to the end of a sentence after "run:"/"with:", then hard-wrapped mid-command; step 3-C `export ... ; mash status` splits into two broken pastes | FIXED | R9 (rules); every next: is a colon sentence plus a command line of its own: step 3-C (line 254) |
| 2 | Problem 1, pasteable NEXT/fix: Same wrap breakage in step 3-A, 3-B, the `--make-key` shortcut and the status screens (no configuration, no endpoint, group missing, credentials missing, credentials refused) | FIXED | 3-A (line 173), 3-B (line 238), make-key (line 491): one command, and each status screen under mash status (line 550) |
| 3 | Problem 1, pasteable NEXT/fix: fix lines end a command with a full stop or comma (`run mash help status.`, `export MASH_GROUP=demo, then restart ...`) | FIXED | R9, R6: the command is on its own line with nothing after it; Unknown flag (line 847) |
| 4 | Problem 1, pasteable NEXT/fix: Many "pasteable" commands contain values mash cannot know (myserver, acme, the key location); a newcomer pastes MASH_URL=tls://myserver:4222 and gets a DNS error | FIXED | R9: placeholders <owner>.<group>, <key file>, <token file>, <url>; only values from the token, the file name or config are filled in. No configuration at all (line 671) |
| 5 | Problem 1, pasteable NEXT/fix: What to ship: sentence on one line ending in a colon, command on its own line indented two spaces, never wrapped, no trailing punctuation, placeholders in angle brackets; shorter form if over 78 columns | FIXED | R9 states it word for word; the widest printed command is 78 columns (section 7) |
| 6 | Problem 1, pasteable NEXT/fix: Rewrite of 3-C: "Next, check that acme.demo.bob can sign in:" then `MASH_OWNER=acme MASH_GROUP=demo MASH_NAME=bob mash status` | CHANGED OTHERWISE | Same shape, label `next:` (R4), and `MASH_GROUP=demo MASH_NAME=bob mash status` without MASH_OWNER, because config.json now holds the owner (Step 3-C (line 254)) |
| 7 | Problem 1, pasteable NEXT/fix: Rewrite of 3-A: "Created a key pair for bob" with the two-row table, `Next, give bob.pub to whoever runs your mash server...` and the mashctl command | CHANGED OTHERWISE | Wording "Created a key pair (bob.key, bob.pub)." (see row for the 3-A note); mashctl command uses the dotted address with <owner>.<group> placeholders because mash knows only bob. Step 3-A (line 173) |
| 8 | Problem 1, pasteable NEXT/fix: Rewrite of the no-configuration status: `export MASH_URL=tls://<server>:4222 MASH_OWNER=<owner>` in the profile or ~/.config/mash/config.json | CHANGED OTHERWISE | The fix names config.json first (R10) and the pasteable command is `mash credential install ... --server <url>`, which writes server and owner into that file; an export of MASH_URL is not offered. No configuration at all (line 671) |
| 9 | Problem 2, status verdict: `mash status` opens with "Checked mash for acme.demo.bob.", an action not a result; the verdict is one word in one row | FIXED | R11; every status screen opens with the one-line verdict, e.g. Healthy (line 601) |
| 10 | Problem 2, status verdict: Opening line has three shapes ("Checked mash for", "mash is not set up", "mash is configured, but ...") | FIXED | R11: `<who> is ready.` / `<who> is ready (1 warning).` / `<who> is NOT ready: <why>.`; all status screens |
| 11 | Problem 2, status verdict: Rows carry facts not about their label: `config group missing`, `credentials no endpoint chosen` | FIXED | Group missing (line 718); Configuration but no endpoint chosen (line 696): new `endpoint` row carries the identity facts; `config` says ok; `credentials` says not checked |
| 12 | Problem 2, status verdict: `credentials valid, not tested` is a contradiction | FIXED | Server not reachable (line 761): "file found, not tested (server not reached)" |
| 13 | Problem 2, status verdict: Version row is first row of a health check but cannot fail; belongs in --detail | FIXED | Version row removed from the default screen; in Healthy, --detail (line 616) |
| 14 | Problem 2, status verdict: Group line `2 other endpoints in demo, 5 under acme` is ambiguous (does 5 include demo, me?) | FIXED | "demo has 1 other endpoint; acme has 3 in all", and the text says the second number counts every endpoint of the owner. Healthy (line 601) |
| 15 | Problem 2, status verdict: Server address hidden behind --detail while the NEXT says run --detail to see it | FIXED | `server myserver:4222, ...` row on the default screen; the NEXT no longer sends the person to --detail. Server not reachable (line 761) |
| 16 | Problem 2, status verdict: What to ship: line 1 verdict, same shape; problem values in CAPITALS (red/yellow as an addition); show the server host; drop the version row | FIXED | R11 (CAPITALS: MISSING, REFUSED, NOT REACHABLE, NOT ENCRYPTED, NOT SET, WARNING; colour red/yellow, addition only) |
| 17 | Problem 2, status verdict: Example rewrites: ready, credentials missing, server not reachable, no endpoint (rewrite 4 screens) | CHANGED OTHERWISE | All four adopted with these differences: "acme.demo.bob is ready (1 warning)" form added; unreachable credentials row reads "file found, not tested (server not reached)" (the review said "installed, not tested"); no-endpoint screen puts group and name in an `endpoint` row and the next uses MASH_GROUP/MASH_NAME in front of `claude` (R10); credentials missing keeps <key file> <token file> placeholders. Screens, first one (line 601) |
| 18 | Problem 3, env-var fixes: Environment-variable fixes ("export MASH_GROUP=demo, then restart Claude Code") are false: the export lives in one shell, Claude Code is started from another | FIXED | R10; serve group-missing fix and send not-set-up fix now start Claude Code with the variables in front (mash serve (line 1339) , mash send (line 866)) |
| 19 | Problem 3, env-var fixes: `mash status` passes in the exporting shell while the agent session stays unconfigured; the status NEXTs say export | FIXED | No status `next:` uses `export`; the one-command form `MASH_GROUP=<group> MASH_NAME=<name> mash status` is offered as the one-shell test only. Configuration but no endpoint (line 696) |
| 20 | Problem 3, env-var fixes: The draft notes config.json does the same as the variables, then picks the variables because they are pasteable | FIXED | R10 and the status screens: the file is named first where the setting lives in config.json (server address, CA, owner), and the command that writes it is the pasteable part |
| 21 | Problem 3, env-var fixes: `mash: mash does not know ...` stutters (serve, send) | FIXED | R6: prefix never doubled; serve and send texts rewritten; plain warning is `warning:` alone |
| 22 | Problem 3, env-var fixes: What to ship: name the persistent place first with the resolved path (honouring MASH_CONFIG_DIR); the variable only as the one-shell test; a future `mash config set` would be the pasteable fix | FIXED | R10 (resolved path honouring MASH_CONFIG_DIR); N7 and Q15 record that `mash config` could come later |
| 23 | Problem 3, env-var fixes: Rewrite: `fix: add "group": "<group>" to ~/.config/mash/config.json, or set MASH_GROUP ...` | CHANGED OTHERWISE | config.json has no group key (checked in internal/config) and two endpoints on one machine share one config.json, so a file value would be wrong for one of them. The fix is the environment of the command that starts Claude Code. N7; R10; mash serve (line 1339)  |
| 24 | Problem 3, env-var fixes: Rewrite of the send not-set-up refusal: set owner, group and name in config.json or export for this shell only | CHANGED OTHERWISE | Owner is a config.json fact, group and name are not: send not-set-up block (mash send (line 866)) names the environment for Claude Code and the one-shell form in front of `mash send`; the owner-missing variant names config.json ("owner") first |
| 25 | Problem 4, inbox: Inbox urgency is appended after the preview and truncation deletes it | FIXED | STATE column first, MESSAGE last; mash inbox (line 1011). The state words are `new, urgent` / `new` / `read`: the review gave `new, urgent`, the planner said urgency FIRST, so the column is first and the cell lists new before urgent as in the header |
| 26 | Problem 4, inbox: No read/unread marker; an inbox that cannot show what is new is a log | FIXED | STATE column (`new` / `read`), counts in the header; Layout rules in mash inbox (line 1011) |
| 27 | Problem 4, inbox: "2 messages in your inbox" with a default limit of 20: must say "showing 20 of 57" and how to get the rest | FIXED | Header "57 messages for X, 3 new, 1 urgent. Showing the latest 2." and an `older ones:` line with `mash inbox --before 56` on its own line (mash inbox (line 1011)). `--before` does not exist in the source (only --limit and --after, inboxArgs in cmd/mash/cli.go): marked NEW, N1 |
| 28 | Problem 4, inbox: FROM column padded wider than its widest cell | FIXED | Columns padded to the widest cell, header included (R12); the inbox blocks are padded exactly |
| 29 | Problem 4, inbox: NEXT on a listing contradicts the rule for who; drop it | FIXED | No next: under inbox or who (R4) |
| 30 | Problem 4, inbox: Order: oldest first; say so in the help | FIXED | mash inbox (line 1011) states "oldest first, newest last"; `mash help inbox` says so (the help text for inbox is not drawn here) |
| 31 | Problem 4, inbox: --detail drops the MESSAGE column and the header, so it is not "the same plus more" | FIXED | --detail keeps the header and the MESSAGE column and adds SIZE (R7). The exact receive time moved to `mash read --detail`; the table cannot hold it in 80 columns |
| 32 | Problem 4, inbox: What to ship: header "2 messages for X, 1 new, 1 urgent.", columns # STATE FROM TRUST WHEN KIND MESSAGE with `12m ago` | FIXED | Identical columns and header; mash inbox (line 1011) |
| 33 | Problem 4, inbox: Paging flag: "use whatever the paging flag really is; the draft only shows --after" | CHANGED OTHERWISE | Verified in cmd/mash/cli.go: there is no older-messages flag; `--before SEQ` is proposed as new (N1), and MCP inbox gains `before` (section 4) |
| 34 | Problem 5, names: mash prints the slash form (`mashctl endpoint create acme/demo/bob`) next to the dotted form | FIXED | R3: verified mashctl endpoint create/show already take the dotted form (parseEndpointRef, endpoint_create.go), so every mashctl command mash prints is dotted; the slash form is only accepted as input by mash send. No open question needed for mashctl |
| 35 | Problem 5, names: `ENDPOINT` means the full address in who and only the last part in `who --group` | FIXED | R3: ENDPOINT always the full address; the grouped view (`--by-group`) keeps the full address on every row (mash who (line 1171)) |
| 36 | Problem 5, names: `--group` does not take a group and looks like a filter, like `--owner`; rename | FIXED | Renamed `--by-group` (the planner's name); mash who (line 1171), N3 |
| 37 | Problem 5, names: Vocabulary: address is owner.group.name in help, owner.group.instance in the draft and JSON, MASH_NAME, "agent", "bob"; settle on endpoint + name | FIXED | R3; no "instance" or "agent" for the thing on screens or help. MCP JSON keeps the field `instance`; the mapping is listed in section 4 and flagged as a decision, N4 |
| 38 | Problem 5, names: "flat key" in --detail beside private/public key; read --detail calls it "Sent as" | FIXED | Called `storage name`, shown only in --detail, never beside the keys (R3; key create --detail, send --detail, read --detail, who --detail, status --detail, credential install --detail); never "flat key" on a default screen |
| 39 | Problem 5, names: `send` reports "message #1 in their inbox": collides with the sender's own #1 | FIXED | Default output drops it; --detail shows "their number #1 (in acme.demo.alice's inbox, not yours)" (mash send (line 866)) |
| 40 | Problem 5, names: Units: `12 min ago` / `3 h ago` / `2 seconds` / `2 s`; `14 bytes` vs `14 B` | FIXED | R12: `12m ago`, `3h ago`, `2 s`, `B` everywhere |
| 41 | Problem 5, names: Verbs: Made / Created / Installed / Wrote / Checked; use Created for things created | FIXED | R12; "Created a key pair", "Created endpoint"; "Installed credentials" stays because installing is what happens; files are "wrote"/"copied"/"kept" |
| 42 | Problem 5, names: Grouped view example: `mash who --by-group` with `acme/demo` headings, full address rows, (you) | CHANGED OTHERWISE | Adopted, with headings `owner acme, group demo` (no slash form on screen); mash who (line 1171) |
| 43 | Problem 5, names: If the slash form must not appear at all, use the heading `acme demo` | CHANGED OTHERWISE | Used `owner acme, group demo`, which says what the two words are |
| 44 | Step 3-A, key create: Two-row key table is good; keep | FIXED | Kept (private key / public key rows) in Step 3-A (line 173) |
| 45 | Step 3-A, key create: "(NATS calls it a seed)" puts NATS on a newcomer's first screen; move to --detail and docs | FIXED | Moved to `mash key create --detail`; R2. Why --detail and not "once where a person first meets it" as the earlier rule allowed: the first screen a newcomer sees must not carry a word they cannot use; only a reader of NATS material needs the mapping, and they run --detail or read the docs |
| 46 | Step 3-A, key create: "for bob" claims an owner from a file name; say "Created a key pair (bob.key, bob.pub)" | FIXED | Step 3-A (line 173): first line is "Created a key pair (bob.key, bob.pub)." |
| 47 | Step 3-A, key create: "Made" vs every other "Created" | FIXED | R12; also in make-key and in the quick start |
| 48 | Step 3-A, key create: NEXT wraps; keep "(you, if that is this machine)" on the sentence line | FIXED | R9; the sentence keeps the parenthesis and wraps on its own continuation line, the command is alone |
| 49 | Step 3-A, key create: Refusal is good ("mash never overwrites a key"); keep | FIXED | Kept; one word changed (file name) in Step 3-A refusal |
| 50 | Step 3-C, credential install: "They combine bob.key and the token; ..." is tutorial prose on every run; one line is enough | FIXED | One line: "Keep bob.key: it is the private half. mash signs in with the key and token." Step 3-C (line 254) |
| 51 | Step 3-C, credential install: Does the person still need bob.key and the token after install? Say so once | FIXED | Same line says keep bob.key (it is the private half). The token is not needed again; its file may be deleted, which the quick start cleanup lists |
| 52 | Step 3-C, credential install: Refusals are good; the token-file refusal is weaker: name the expected pattern <owner>.<group>.<name>.token | FIXED | Refusals (line 309): "mashctl writes <owner>.<group>.<name>.token" |
| 53 | Step 3-C, credential install: --force and --out in the "already installed" refusal are fine | FIXED | Kept unchanged |
| 54 | Same machine --make-key: Padded parentheticals are an odd half-table; make it a real table or three plain lines | FIXED | Three plain result lines, then one real table of every file written (Same machine: one command (line 491): one command) |
| 55 | Same machine --make-key: --make-key NEXT wraps | FIXED | R9 shape in the same block |
| 56 | mash version: `mash version` one line and `--detail` adding the commit: good | FIXED | Kept; mash version (line 520) |
| 57 | mash version: Drop `-v` (means verbose elsewhere); keep --version | FIXED | `mash -v` not added; `--version` is new (section 6) |
| 58 | mash status notes: See problem 2 (status) | FIXED | All rows under P2 |
| 59 | mash status notes: --detail: "claude code  mail is delivered by hook; the channel list names mash" presumes "the channel list"; say plugin hooks installed, mash allowed to wake idle sessions | FIXED | Healthy, --detail (line 616): "plugin hooks installed; mash may wake an idle session" |
| 60 | mash status notes: `relay trace` and `name rule` are internal; label them for a person | CHANGED OTHERWISE | `relays seen 0 messages relayed through another endpoint`; `name rule` is kept as the label because docs/name-rule.md calls it that, with "(no list limits which names are accepted)" added |
| 61 | mash status notes: Warning case: right sentence; the NEXT is a doc section number, give a URL or path a person can open | FIXED | `next: See https://github.com/bogworks/mash/blob/main/docs/install.md` (R4); Claude Code is not set up (line 649) (warning, exit 0). The address was not opened (no network check) |
| 62 | mash status notes: Plain URL warning `mash: warning:` has a double prefix; stderr only is right | FIXED | `warning: ...` alone (R6); Plain URL with the override (line 806) (warning, exit 0) |
| 63 | mash status notes: Refused plain URL: good, short, true; keep | FIXED | Kept; the fix additionally names config.json ("url") and says MASH_URL overrides it (R10); Plain URL without the override (line 830) (refused) |
| 64 | mash status notes: `status --debug` error: "fix: run mash help status." trailing period | FIXED | Two-line fix: "see the options with:" then `mash help status` alone; Unknown flag (line 847) |
| 65 | mash send: "Sent your message to acme.demo.alice" is clear; drop "(message #1 in their inbox)" or move to --detail | FIXED | Dropped from default, in --detail (mash send (line 866)) |
| 66 | mash send: "Sent your urgent question" is good | FIXED | Kept |
| 67 | mash send: Duplicate case "Not sent again" is good | FIXED | Kept; the "(message #1)" tail dropped for the same reason as above |
| 68 | mash send: --detail shows `from` (you already know) and `kind task`; size unit differs from inbox | FIXED | Kept behind --detail; unit is `B` (R12) |
| 69 | mash send: Usage error: command and a parenthetical on one line; make two lines | FIXED | "fix: use it like this (more: mash help send):" then the command alone; mash send (line 866) |
| 70 | mash send: Bare-name refusal good; suggest `did you mean acme.demo.bob?` when exactly one endpoint of that name is in the group | FIXED | "fix: send to acme.demo.bob instead:" then `mash send acme.demo.bob "hi"` (mash send (line 866)) |
| 71 | mash send: Plain URL is exit 2 in send and 1 in status; make it 1 | FIXED | Exit 1 for plain-URL refusal in every command; checked today's code (cmd/mash/main.go): all connecting commands exit 2 and status exits 0. List in section 6; serve and hook flagged as N6 |
| 72 | mash inbox: See problem 4 (inbox) | FIXED | All rows under P4 |
| 73 | mash read: The second quoted line "(the sender's text is shown quoted, as sent)" sits inside the quote and reads as the sender's words | FIXED | Removed: the quoted block holds only the sender's text (mash read (line 1099), Changes paragraph). It was a mockup annotation |
| 74 | mash read: "Verified: signed by its key." - "its" is ambiguous | FIXED | "trust  verified (signed by acme.demo.bob's key)" |
| 75 | mash read: Header uses prose; other detail views use an aligned label column; use from/when/trust/kind | FIXED | Aligned label rows `message / from / when / trust / kind`; mash read (line 1099) |
| 76 | mash read: The reply NEXT does not link to message #2; expects `mash reply 2` or an in-reply-to flag; for fyi/result a reply is not next | FIXED | `next:` only for question and blocker from another endpoint, with `mash send <addr> --reply-to 2 "<your answer>"`. Checked: the CLI has no reply flag (the MCP send tool has in_reply_to), so `--reply-to` is NEW (N2); no `mash reply` verb proposed because `reply` is already the MCP tool for live prompts |
| 77 | mash read: --detail: `Replies to: none.   Sent as: acme_demo_bob.` triple-space prose; make rows | FIXED | Rows `answers`, `storage name` in read --detail |
| 78 | mash read: Errors are good; keep | FIXED | Kept; the fix commands now stand on their own line (R9) |
| 79 | mash who: Default table good, pasteable address first; keep | FIXED | Kept; mash who (line 1171) |
| 80 | mash who: Does the list include yourself? the empty text says "No other endpoints" but the example lists acme.demo.bob | FIXED | It includes you, marked `(you)` in a last column (keeps the address column pasteable); the empty text is "No endpoints are registered." |
| 81 | mash who: `online  now` is noise; show `-` or blank | FIXED | LAST SEEN is `-` while online; `never` for an endpoint nobody has seen |
| 82 | mash who: --detail is exactly 80 with a three-letter host; the two-table rule will confuse; either let --detail exceed 80 or use per-endpoint blocks | FIXED | Chose per-endpoint blocks, not exceed-80: R1 has no exception for --detail, the tool is one a person pastes and greps, and a wide table wraps on 80-column terminals. `mash who --detail` prints a block per endpoint; `mash who <address> --detail` (new, N3) prints one. Real widest block line is far below 80 |
| 83 | mash who: `--group`: see problem 5; blank repeated OWNER/GROUP cells break grep | FIXED | `--by-group` keeps the full address on every row, so grep works (mash who (line 1171)) |
| 84 | mash who: Empty: "Create one with mashctl (see mashctl endpoint create)." is a command in disguise; say "They are created on the server with: mashctl endpoint create" | FIXED | "No endpoints are registered. They are created on the server with:" then `mashctl endpoint create <owner>.<group>.<name> --public-key <file>.pub` on its own line |
| 85 | mash prompt: "Asked ... and waited for the answer:" is past tense; before or after the wait? print "Waiting for ... (up to N s)..." on stderr on a terminal, then the answer on stdout | CHANGED OTHERWISE | "Waiting for acme.demo.alice to answer..." on stderr, terminal only, before the wait; answer alone on stdout. The "(up to N s)" is left out: the time limit of `prompt` was not found in the source and no number is invented (mash prompt (line 1254)) |
| 86 | mash prompt: Refusal: content good; the fix line is 80 columns (draft says 79) and embeds "..." mid-sentence; put the command on its own line | FIXED | "fix: send mail instead; they read it later:" then `mash send acme.demo.alice "are you there?"` alone; the line is far under 80. The review's width correction is right: DRAFT1 said 79, it was 80 |
| 87 | plugin export: Good shape; the NEXT command is unverified; ship none until verified or point to the doc | FIXED | Verified against this host and the repo: `claude plugin marketplace add <path>` and `claude plugin install mash@mash` (claude plugin --help, marketplace --help, install --help; README lines 213-215; docs/install.md). Nothing run against Claude Code. mash plugin export DIR (line 1308); Checks run (line 2010). Also corrected the file count (5, not the 11 DRAFT1 claimed, by running the export into a scratch directory) |
| 88 | serve start-up: `mash is listening as acme.demo.bob` has no prefix; use `mash: serving as acme.demo.bob (myserver:4222)` | FIXED | mash serve (line 1339) : exactly that line |
| 89 | serve start-up: Group-missing fix is false (problem 3) | FIXED | "fix: start Claude Code with the group in the environment it starts in:" then `MASH_GROUP=<group> MASH_NAME=bob claude` |
| 90 | Hook text: Hook times say 12:21 UTC while every other block says 00:21 UTC: use 24-hour everywhere | FIXED | 00:21 UTC; R12; The Claude Code hook message (line 1380) (what the model sees) |
| 91 | Help: No usage line; the `mash:` prefix makes the tagline look like an error | FIXED | Help starts "Usage: mash <command> [options]"; the tagline has no prefix (mash --help (line 1430)) |
| 92 | Help: Grouping (Mail / Set up / Other) is good; keep | FIXED | Kept |
| 93 | Help: `--json` exists on several commands and is not mentioned | FIXED | "Options on most commands: --detail for more facts, --json for scripts."; `mash help send` lists --json |
| 94 | Help: `mash help send` --detail text "also show who sent it" is wrong (the sender is the user); omits the reply-to option | FIXED | "--detail also show the kind, the size and the number it has in the recipient's inbox"; `--reply-to #` listed (it is a new flag, N2) |
| 95 | Help: `mash` alone: make it the usage line, not the tagline | FIXED | "Usage: mash <command>. Run mash help for the list." (Unknown command and flag errors (line 1496)) |
| 96 | Help: Unknown command: consider git's "did you mean" | FIXED | `mash sned` suggests send; the removed `credential create` suggests `credential install` |
| 97 | Errors in general: Two-line mash:/fix: shape is good and consistent; keep | FIXED | Kept (R6), with the command on a third line when the fix is one |
| 98 | Errors in general: fix lines inconsistent: sentence in some, bare command in others; pick one, nothing after a command | FIXED | R6/R9: a fix is always a sentence; when it names a command the sentence ends with a colon and the command follows alone |
| 99 | Errors in general: `NEXT:` (upper) beside `fix:` (lower); same case ("Next," or `next:`) | FIXED | Decision recorded in R4: both lower-case, `next:` and `fix:`, applied in every block, the quick start and the packet |
| 100 | Colour: Only colour is on the word NEXT: decoration; colour the problem values in status and urgent/new in inbox, each readable with colour off | FIXED | R4 (no colour on the label) and R11 (red for problem values, yellow for WARNING and `warning:`, `new`/`urgent` in inbox; all legible in capitals or words with colour off); Section 6 last bullet |
| 101 | Colour: Honouring NO_COLOR and non-tty is correct | FIXED | Kept in R11 and section 6 |
| 102 | Widths: Widths: two blocks are exactly 80 only because values are short (who --detail, prompt), and the draft's count for the prompt fix line (79) is wrong (80) | FIXED | Both blocks rewritten; every block re-measured with the script; real widest 80 (inbox --detail, preview cut by design) and 78 for a printed command; Checks run (line 2010) |
| 103 | Answers to the open questions: Q1 status connecting (2 s): yes; print the host so the wait is explained | FIXED | Kept; server host:port on the default screen (mash status (line 550)). DECIDED; removed from section 5 |
| 104 | Answers to the open questions: Q2 group line from directory records only: yes, worded unambiguously | FIXED | "demo has 1 other endpoint; acme has 3 in all" (Healthy (line 601)). DECIDED |
| 105 | Answers to the open questions: Q3 exit 1 when something is wrong; plain-URL refusal 1 in every command, not 2 | FIXED | Section 6 exit-code list; checked in main.go that today it is 2 for all connecting commands and 0 in status; serve/hook exit flagged N6. DECIDED except N6 |
| 106 | Answers to the open questions: Q4 dotted address in one column by default: yes | FIXED | Kept (mash who (line 1171)). DECIDED |
| 107 | Answers to the open questions: Q5 Owner/Group/Endpoint layout: not as `--group`, not with the address split; a grouped view under a different flag that keeps the full address | FIXED | `--by-group` with full addresses (mash who (line 1171)). DECIDED |
| 108 | Answers to the open questions: Q6 a doc as NEXT only with an openable URL or absolute path, phrased "See ..." not as NEXT | CHANGED OTHERWISE | Phrased `next: See <URL>` (R4) rather than moving it out of the next: label, because the line has the same job (what to do now) and the planner asked to keep the label; the address is an https URL. DECIDED |
| 109 | Answers to the open questions: Q7 plugin install command: do not ship a NEXT until someone has run it | CHANGED OTHERWISE | Verified instead of run: help output of `claude plugin` on this host and the README/install.md commands; nothing was run against Claude Code, which the planner's instruction ruled out. The NEXT is shipped on that evidence; the actual run remains a contract test. Checks run (line 2010). DECIDED |
| 110 | Answers to the open questions: Q8 remove `mash admin`: yes | FIXED | Out of the people-facing help (mash --help (line 1430)); deleting the verb from the binary is N5, STILL OPEN |
| 111 | Answers to the open questions: Q9 pointer for `credential create`: the review would keep a one-release "renamed" line; if the hard-cut rule wins, the unknown-command error must suggest the closest command | NOT TAKEN | The planner decided HARD CUT: no alias and no pointer, because migrations are scripts and there is no old-state tolerance. The unknown-command error suggests the closest command (`did you mean credential install?`), which is the review's own fallback. DECIDED (by the planner); Unknown command (line 1496) |
| 112 | Answers to the open questions: Q10 raw `text` in JSON: no view as a terminal critic | FIXED | No change; accepted as in DRAFT1 and no longer an open question (section 4 `read`). DECIDED |
| 113 | Answers to the open questions: Q11 `--json` on `inbox` and `send`: yes, and list it in help | FIXED | --json on both, listed in the help block and in `mash help send`. DECIDED |
| 114 | Answers to the open questions: Q12 `serve` stderr line: yes, with a `mash:` prefix and the server host | FIXED | mash: serving as acme.demo.bob (myserver:4222). DECIDED |
| 115 | Answers to the open questions: Q13 expired or wrong-owner token: refuse with one sentence naming the expiry date or the owner, with `fix:` ask for a new token | CHANGED OTHERWISE | Wording adopted (section 5, 13) but kept STILL OPEN as a question: whether the token carries an expiry mash can read is unknown and needs the CLI team's token format |
| 116 | Found while doing DRAFT2: (not a review finding) DRAFT1 said `mash plugin export` writes 11 files | FIXED | Run into a scratch directory: 5 files; the block says 5 (Checks run (line 2010)) |
| 117 | Found while doing DRAFT2: (not a review finding) planner rules R9 and R10, `mash who --by-group`, 24-hour clock, `--version`, no `mash -v`, `next:`/`fix:` lower-case, the hard cut for `credential create`, README cleanup exact | FIXED | R9/R10 and the places above; README quick start draft, Step 10 |
| 118 | Found while doing DRAFT2: (not a review finding) the README quick start step 8 showed alice online although no session runs | FIXED | Both endpoints are offline at step 8, with `never`; quick start draft Step 8 |

Counts: 118 rows; FIXED 103; CHANGED OTHERWISE 14; NOT TAKEN 1.

## Review of DRAFT2 (critic round 2)

Date: 2026-11-19. Source: the second independent CLI-UX review, of DRAFT2 of
the target outputs and the quick start (sections A to F of the review: command
by command, `mash status`, the quick start, consistency, the five most serious
problems, the open questions and the verdict). Target document: the same file
(now DRAFT3). Line references are by section, not by line, because the file was
rewritten. The planner's rulings L1 to L12 bind this round; they are named in
the last column where a row rests on one. Section C (the README quick start)
and the verdict on it are NOT answered here: another agent rewrites the quick
start from the packet blocks. Dispositions as above.

| # | finding (review section) | disposition | where in DRAFT3, or why not |
|---|---|---|---|
| 2.1 | Summary 1: one address lives in three places (owner in config, group and name in two variables); every hint carries the MASH_GROUP/MASH_NAME prefix; status says "no endpoint chosen" right after install | FIXED | L4: one setting, `MASH_ENDPOINT` / `"endpoint"`, one installed endpoint needs none; R13, section 3; no block carries a prefix |
| 2.2 | Summary 2: rule 26 broken wholesale, 28 errors begin `mash:` (and one success line) | FIXED | L6: every error starts `error:`, R4; the serve success line keeps `mash:` and errors no longer share it (row A.12) |
| 2.3 | Summary 3: `mash status` does not deliver version, server alive/encrypted/last seen, config, creds and nothing else | FIXED | L5: R11 and every status screen in section 5 |
| 2.4 | Summary 4: the README cannot be followed today and is not minimal | NOT TAKEN | Out of scope for this task: the quick start is rewritten by another agent from the packet blocks (keys listed in the report); the review's section C is not answered here |
| 2.5 | Summary 5: the mail tables only fit toy names and show urgency, "you" and never-seen in different forms | FIXED | R14, L7: real-name blocks `inbox-long`, `read-long`, `who-long`; one `URGENCY` column; `*` and footer; `now` and `never` |
| 2.6 | Summary, measured: six lines at 77-80 columns, all built from toy names; real names push the inbox tables and two hints past 80 | FIXED | Real-name case (group 16, names 18 characters) tested in inbox, read and who; widest printed text is 80; section 9 |
| 2.7 | A.1 key create: repetition (file names on line 1 and again in the rows) | FIXED | Step A: line 1 names no file |
| 2.8 | A.1 key create: rule 29, say why on the first line | FIXED | Step A: "Created a key pair that proves this endpoint is who it says it is." |
| 2.9 | A.1 key create: next hint does not mention the same-machine shortcut (`--make-key`) | NOT TAKEN | `--make-key` makes its own key, so offering it after `key create` would leave the new bob.key unused; stated under Step A, Changes |
| 2.10 | A.1 key create: error P22 starts `mash:` and has no runnable fix line | FIXED | Step A refusals: `error:` and `mash key create --out <new file>.key` |
| 2.11 | A.1 key create: missing outputs (`--out` omitted, `.pub` exists but not `.key`, directory not writable) | FIXED | Step A refusals, four blocks (exit 2, 1, 6) |
| 2.12 | A.1 key create: `--detail` label `NATS name` is a label that names nothing | FIXED | `NATS word  seed (NATS's word for the private key)` in Step A, `--detail` |
| 2.13 | A.2 mashctl endpoint create: first line is a verdict, good | FIXED | Kept (Step B) |
| 2.14 | A.2 mashctl endpoint create: "Wrote ... It holds no secret" is the best line; keep | CHANGED OTHERWISE | Kept as "The token says, signed by your server, that bob.pub is this endpoint." plus `(holds no secret)` on the table row, because the same review asks for one layout for files written (next row) |
| 2.15 | A.2 mashctl endpoint create: inconsistent with the `--make-key` form, which prints a `wrote` table; pick one layout | FIXED | Step B and the `--make-key` block both use the `wrote`/`copied`/`kept` table (L11) |
| 2.16 | A.2 mashctl endpoint create: "agent's machine" vs "endpoint's machine"; one noun | FIXED | "the endpoint's machine" everywhere (Step B, R3) |
| 2.17 | A.2 mashctl endpoint create: no mashctl error shown (endpoint exists, bad public key, owner not this server's, unreachable, bad address) | NOT TAKEN | mashctl is the CLI team's file; this document shows it only where mash hands over (section 4). The errors belong to their targets file |
| 2.18 | A.3 credential install: first line is a verdict, yes | FIXED | Kept |
| 2.19 | A.3 credential install: "Keep bob.key: it is the private half" vs R175 and the cleanup; does `.creds` hold the private key | FIXED | L9, verified in `internal/keycred`: it does. Section 4 states once: keep bob.key as the backup; every block says "They hold bob's private key and the token. Keep bob.key as the backup." |
| 2.20 | A.3 credential install: `copied  ca.pem to ...` breaks the path column | FIXED | `copied  ~/.config/mash/ca.pem  (from ca.pem, mode 644)`, Step C |
| 2.21 | A.3 credential install: the next hint carries `MASH_GROUP=demo MASH_NAME=bob` because of two variables; the tool read the address from the token | FIXED | `NEXT: ... mash status` with no variable (L4); a second endpoint on the machine gets the export plus `mash status` (`install-second`) |
| 2.22 | A.3 credential install: P120-122 the fix describes the command and wraps (rule 28 vs 30) | FIXED | L10: the full command is printed on its own line and is exempt from 80 columns (94 columns, section 9) |
| 2.23 | A.3 credential install: "(it starts BEGIN CERTIFICATE)"; print the literal | FIXED | `-----BEGIN CERTIFICATE-----` on its own line (install-refuse) |
| 2.24 | A.3 credential install: `mash: mash connects only over TLS` stutters | FIXED | `error: mash connects only over TLS; nats://myserver:4222 is not encrypted.` |
| 2.25 | A.3 credential install: "choose another place with --out" is unexplained | FIXED | Dropped from the refusal; the fix names `--force` |
| 2.26 | A.3 credential install: `--force` repoints the server for every endpoint on the machine; say so | CHANGED OTHERWISE | L11 splits the flags: `--replace` overwrites config.json (the refusal names both servers), `--force` replaces only the credentials file, so the repointing is a separate, named flag |
| 2.27 | A.3 credential install: `--detail` has `storage name acme_demo_bob` unexplained and `token expires never (provisional)` leaks a note | CHANGED OTHERWISE | "provisional" removed from the block (the PROVISIONAL status stays in the prose and Q13); `storage name` stays, detail only, labelled (R3) |
| 2.28 | A.3 credential install: two names for the setup guide (`tls-and-clusters.md`, `nats-setup.md`) | FIXED | `docs/tls-and-clusters.md`, the file that exists (section 9) |
| 2.29 | A.3 credential install: cert-mismatch `mash send` error has the command inline and wrapped | FIXED | Command on its own line: `cp <server ca file> ~/.config/mash/ca.pem` (trust block) |
| 2.30 | A.5 send: success lines are verdicts and short, good | FIXED | Kept |
| 2.31 | A.5 send: `--detail` has no blank line after the result | FIXED | send-detail has the blank line |
| 2.32 | A.5 send: default kind `task` makes "lunch at noon?" a work assignment | FIXED | L7: the kind of a plain message is `message`; the code's five kinds are listed; N12 (needs the contract) |
| 2.33 | A.5 send: `mash send acme/demo/alice` accepted silently, a second form | FIXED | L3 and R3: refused with the dotted form in the fix (send-refuse) |
| 2.34 | A.5 send: group and name not set, the fix leads with Claude launch and an unlabelled column-0 line | FIXED | The variables are gone; the "several endpoints, none chosen" refusal leads with the shell form and has no column-0 line |
| 2.35 | A.5 send: `mash send bob` error is good, but the sender is also bob | FIXED | Sender is alice in that block |
| 2.36 | A.5 send: missing outputs: server unreachable, empty message, `--file` not found | FIXED | Three new send-refuse blocks |
| 2.37 | A.5 send: missing outputs: message too large; recipient with no running session | CHANGED OTHERWISE | A `recipient` row in `send --detail` is shown as PROVISIONAL; the size limit was not checked in the source: N9 |
| 2.38 | A.6 inbox: header line is a verdict with counts, good | FIXED | Kept |
| 2.39 | A.6 inbox: width with real names, no truncation rule, `--wide` shown nowhere | FIXED | R14: shared prefix said once, MESSAGE cut at 80, `--wide` new (N8), `inbox-long` block |
| 2.40 | A.6 inbox: `--detail` truncates the message more than the default | FIXED | `--detail` puts the message under its row, with the whole line to itself (R7: never less) |
| 2.41 | A.6 inbox: urgency is in STATE in inbox and in KIND in read | FIXED | One `URGENCY` column and row, values urgent/normal (L7) |
| 2.42 | A.6 inbox: `older ones:` is a new hint label | FIXED | `NEXT:` with `mash inbox --before 56` |
| 2.43 | A.6 inbox: no next hint after a list with new mail | CHANGED OTHERWISE | `NEXT: read the urgent one:` is printed when mail is new; this reverses DRAFT2's R4 (no hint under a listing) |
| 2.44 | A.6 inbox: leading-space table vs `who` at column 0; pick one | FIXED | R14: every table starts two columns in |
| 2.45 | A.6 inbox: the trust legend should appear in the default view when a row is not verified | FIXED | `inbox-trust`: TRUST column and legend appear only then |
| 2.46 | A.6 inbox: the README mock says `12m ago` for mail sent one command earlier | FIXED | The inbox mockups use `10s ago` and `8s ago` (README itself is not in scope) |
| 2.47 | A.7 read: no first-line verdict, output starts with an indented row | FIXED | `#2 from acme.demo.bob, 8s ago.` |
| 2.48 | A.7 read: `--detail` drops the next hint the default had | FIXED | `--detail` keeps the `NEXT:` |
| 2.49 | A.7 read: `answers  no earlier message` is awkward | FIXED | `reply to  -` |
| 2.50 | A.7 read: `storage name acme_demo_bob` in a message read from alice's inbox; send `--detail` says alice's is `acme_demo_alice` | FIXED | Each value is labelled whose it is: "(the sender's)" in read, "(the recipient's)" in send |
| 2.51 | A.7 read: does read mark it read? say so | FIXED | `state  read (it was new; reading marks it read)` in `--detail` |
| 2.52 | A.8 who: `(you)` in an unheaded fourth column | FIXED | L7: `*` marker and the footer `* you` |
| 2.53 | A.8 who: you are listed `offline` while running the command; name the column for what it measures | CHANGED OTHERWISE | STATE stays (it is the MCP `state` field and the `--online` filter); "online means a Claude Code session is serving that endpoint" is said under the table and in help |
| 2.54 | A.8 who: LAST SEEN `-` for online and for never | FIXED | `now` and `never`; `-` is not used (L7) |
| 2.55 | A.8 who: `it.prod.carol` appears to `acme.demo.bob` against the group definition; owner `it` and group `prod` unintroduced | FIXED | The cast has `acme.lab.carol`; `who` shows no other owner's endpoints (L7); the directory scope is N13 |
| 2.56 | A.8 who: `--by-group` has no header row and repeats owner and group | FIXED | Each group has a header row (`who-by-group`); the full address stays in every row so grep works |
| 2.57 | A.8 who: `--detail` shows `host  bf2`, never introduced | FIXED | `runs on  myserver` (the cast's machine) |
| 2.58 | A.8 who: empty `who` unreachable in practice and its hint has no label | FIXED | `NEXT: create one on the server:` with the command on its own line; the block stays for a listing run by a registered endpoint with no peers |
| 2.59 | A.9 prompt: the waiting line must go to stderr; the answer is unquoted while read quotes sender text; pick one | FIXED | Section 2 (streams) and the prompt block: waiting line on stderr, answer raw on stdout; `> ` quoting only where an agent reads |
| 2.60 | A.9 prompt: missing timeout, partial answers, Ctrl-C, exit codes | CHANGED OTHERWISE | Timeout (15m, from `cli_timeout`) and exit codes shown; partial answers and Ctrl-C are not specified (N9) |
| 2.61 | A.10 version: `0.5.5` in a 0.5.6 mockup, and seven other places | FIXED | `mash 0.5.6` and `0.5.6` in every mockup; only the real Before blocks keep the old number (L9) |
| 2.62 | A.10 version: `--detail` is not a two-column table; add the plugin version | FIXED | `mash version --detail` is two rows, `commit` and `plugin` (the plugin row is PROVISIONAL, N9) |
| 2.63 | A.11 plugin export: the step after the two commands (start Claude Code with the flag) is not printed | FIXED | Third line of the `NEXT:`: `claude --dangerously-load-development-channels plugin:mash@mash` |
| 2.64 | A.12 serve: the success line begins `mash:`, same prefix as every error | CHANGED OTHERWISE | Errors now start `error:` (L6), so `mash:` marks only the success line; a log is scanned for `error:` |
| 2.65 | A.12 serve: "start Claude Code with the group in the environment it starts in" is hard to parse | FIXED | The group variable is gone; the refusal is two pasteable lines (`export`, then `claude ...`) |
| 2.66 | A.13 hook: newest first in the hook, oldest first in `inbox` and MCP | FIXED | One order, oldest first, in the hook, `inbox` and MCP (section 5, hook) |
| 2.67 | A.14 help: environment variables are not listed (`MASH_URL`, `MASH_CONFIG_DIR`, ...); status should say which are set | FIXED | `help-settings` block; `settings` row of `status --detail`; section 3 |
| 2.68 | A.14 help: `--wide` is in the rules and nowhere in the tool | FIXED | `mash inbox --wide` and `mash help inbox` |
| 2.69 | A.14 help: `--reply-to #` placeholder; use `N` | FIXED | `--reply-to N` in help |
| 2.70 | A.14 help: `credential create --seed --jwt` migration error should name the renamed flags | FIXED | "it is now credential install; --seed is --key and --jwt is --token" |
| 2.71 | A.14 help: bare `mash`, state exit code 2 | FIXED | Section 2 table, row 2 |
| 2.72 | A.15 MCP: one label per thing (instance/name, address/endpoint, seq/#, done/final, in_reply_to/--reply-to/answers) | FIXED | The mapping table at the head of section 6; `instance` becomes `name`, `address` becomes `endpoint`, `final` becomes `done` |
| 2.73 | A.15 MCP: example data disagrees with itself (members/endpoints, last_seen null vs 3h ago, commit short vs full) | FIXED | Counts are in `detail` and agree with the cast; bob's `last_seen` is 3h before the clock; the commit is short in both |
| 2.74 | A.15 MCP: `tagged` unexplained | FIXED | The send table says what it means (`kind.Tag`) |
| 2.75 | A.16 colour, streams, exit codes specified nowhere; colour decoration only, off off-TTY and with NO_COLOR; hints to stderr | FIXED | Section 2. Decision: `NEXT:` is part of the success output, so stdout; `error:`, `fix:`, `warning:` on stderr; `status` is a report on stdout |
| 2.76 | B. status, owner's ask: version missing from the default screen | FIXED | `version` is the first row (R11) |
| 2.77 | B. status, owner's ask: last seen missing in the human output | FIXED | Second line of the server row |
| 2.78 | B. status, owner's ask: nothing else; `group` and `claude code` rows are extra | FIXED | Removed from the default; group counts in `--detail`; the Claude warning is a `warning:` line under the rows (R11) |
| 2.79 | B. status, state by state: Claude warning's hint is a URL, not a step | FIXED | `fix:` with the `claude --dangerously-load-development-channels plugin:mash@mash` command |
| 2.80 | B. status, state by state: nothing configured says one thing twice; `<url>` vs `tls://<server>:4222` | CHANGED OTHERWISE | `server NOT SET / not checked` and `config MISSING (path)` are different facts; the placeholder stays `<url>` in the one command |
| 2.81 | B. status, state by state: no endpoint chosen, two commands and placeholders the tool could fill | FIXED | `status-choose`: the tool names the installed endpoints and fills the example |
| 2.82 | B. status, state by state: no group; first line says `bob`, placeholder `<group>` | FIXED | The state no longer exists (R13) |
| 2.83 | B. status, state by state: no credentials, no pointer to `mash key create` if no key exists | FIXED | Said under `status-nocreds` |
| 2.84 | B. status, state by state: not reachable does not say where the address came from | FIXED | The fix names `"server"` in config.json, or `MASH_SERVER` when the variable is set |
| 2.85 | B. status, state by state: credentials refused, "run install again" fails without `--force` | FIXED | The fix carries `--force` (`status-refused`) |
| 2.86 | B. status, state by state: certificate not trusted: yes / yes | FIXED | Kept, in the new layout (`trust`) |
| 2.87 | B. status, state by state: plain URL allowed: yes / yes | FIXED | Kept, as a report (`status-plain-warning`) |
| 2.88 | B. status, state by state: plain URL refused makes status an error instead of a report; the fix says "unset MASH_URL if it is set" | FIXED | `status-plain-refused`: a report, exit 1; the fix names the variable the address came from (L5, L6) |
| 2.89 | B. status, state by state: unknown option: yes / yes | FIXED | Kept with `error:` |
| 2.90 | B. status, other defects: the row set changes by state (endpoint row appears, group row disappears) | FIXED | R11: the same four rows in every state |
| 2.91 | B. status, other defects: the server is shown as `myserver:4222` without scheme | FIXED | Address as configured, `tls://myserver:4222` |
| 2.92 | B. status, other defects: two warning forms (`WARNING:` in the table, `warning:` below) | FIXED | One form, the `warning:` line under the rows |
| 2.93 | B. status, other defects: "last seen" for a one-shot CLI is "now"; make it mean something via `mash serve` | CHANGED OTHERWISE | `now` after a sign-in, else the time `mash serve` recorded, else `never`; the recording is new (N10) |
| 2.94 | B. status, other defects: `--detail` has `name rule` and `relays seen` unexplained; move version up | FIXED | `version` is a default row; both rows carry an explanation in `status-detail` |
| 2.95 | B. status, rewritten target (healthy and unreachable), exit 0 / 1 | CHANGED OTHERWISE | Adopted: verdict `<endpoint>: mail works.`, the four rows, exit 0/1. Differences: the server row is two lines (the address can be long), and unreachable says `last seen 3h ago` not `last reached` |
| 2.96 | C. README quick start: every step-by-step finding, the action count table, the minimal flow (about 30 bullets) | NOT TAKEN | Out of scope by the brief ("ignore C"); the quick start is rewritten from the packet blocks. Two C notes are met by the targets anyway: status right after step 4 (L4) and a single guide name |
| 2.97 | D. consistency: an endpoint address has seven forms | FIXED | One dotted form (R3, L3) |
| 2.98 | D. consistency: the server address has several forms (`--server`, `"url"`, `MASH_URL`, JSON `server`, with and without scheme) | FIXED | `--server`, `"server"`, `MASH_SERVER`, JSON `server.address`, always with the scheme (section 3) |
| 2.99 | D. consistency: endpoint name is `name` in human text and `instance` in JSON | FIXED | `name` in both |
| 2.100 | D. consistency: message number is `#2`, `seq`, "their number", "number" | FIXED | Mapping table: `#` and "number" in text, `seq` in JSON; "their number" only in `send --detail` |
| 2.101 | D. consistency: reply link is `--reply-to`, `in_reply_to`, "answers", "(it answers #2)" | FIXED | `--reply-to N`, `in_reply_to`, `reply to` row; the send confirmation "it answers #2" is a sentence |
| 2.102 | D. consistency: urgency is STATE in inbox, KIND in read | FIXED | `URGENCY` in both |
| 2.103 | D. consistency: `-` for online and never-seen, `never` in the README | FIXED | `now` and `never` |
| 2.104 | D. consistency: "endpoint's machine" vs "agent's machine" | FIXED | "the endpoint's machine" |
| 2.105 | D. consistency: setup guide named `docs/tls-and-clusters.md` and `docs/nats-setup.md` | FIXED | `docs/tls-and-clusters.md` |
| 2.106 | D. consistency: version 0.5.5 in 8 places of a 0.5.6 mock | FIXED | No 0.5.5 in a mockup |
| 2.107 | D. consistency: hint labels (`next:`, `fix:`, `older ones:`, `warning:`, `WARNING:` in a row, `next: See`, unlabelled column-0 lines) | FIXED | R4: `NEXT:`, `fix:`, `error:`, `warning:`; continuation indented |
| 2.108 | D. consistency: error prefix `mash:` on 28 errors; rule 26 says `error:` | FIXED | `error:` in every block |
| 2.109 | D. consistency: status failure hint is `next:` in status and `fix:` elsewhere for the same fault | FIXED | `fix:` for every failure; `NEXT:` only after a success |
| 2.110 | D. consistency: time forms (`12m ago`, `3h ago`, `2 s`, `41 ms`, `1 s ahead`, ...) | FIXED | R12: `12m ago`, `2s`, `41ms`, absolute `2026-11-19 00:21 UTC` in `--detail` |
| 2.111 | D. consistency: sizes `14 B` with a space but durations without or with | FIXED | R12: no space in both (`14B`, `2s`) |
| 2.112 | D. consistency: tables (inbox indented, who at column 0, `--by-group` headerless, `(you)` unheaded) | FIXED | R14 |
| 2.113 | D. consistency: paths `~/...` by default and absolute in `--detail`; state it as the rule | FIXED | R12 |
| 2.114 | D. consistency: order, inbox oldest first and agent mail newest first | FIXED | Oldest first everywhere |
| 2.115 | D. consistency: tense and capitals consistent; "Labels lower case except `next: See`" | FIXED | Results keep past tense with a capital; labels are `fix:`, `error:`, `warning:` and `NEXT:` by L1 |
| 2.116 | D. consistency: exit codes not specified; duplicate `--id` exit 0; `prompt` timeout its own code | CHANGED OTHERWISE | Section 2 table; duplicate `--id` stays 0 (it is not a failure); the `prompt` timeout exits 1, with no code of its own (3 to 5 stay unused) |
| 2.117 | E.1 One address, three places; the tool asks for what it knows; question on `MASH_GROUP`/`MASH_NAME` compatibility | FIXED | L4 (planner): one variable, auto-pick for one installed endpoint, the `status-choose` screen for several, compatibility dropped with no alias; R13, section 3 |
| 2.118 | E.2 Errors do not begin `error:`; fixes are described, wrapped and false; rule 28 vs 30 | FIXED | L6 and L10: runnable true fixes; commands are exempt from 80 columns and kept short; continuation indented |
| 2.119 | E.3 `mash status` is not the screen the owner asked for | FIXED | R11 and section 5, status; every scenario of L5 has a block |
| 2.120 | E.4 The README quick start cannot be followed, and is not minimal | NOT TAKEN | Out of scope (see section C row) |
| 2.121 | E.5 The mail tables fit only toy names and say one fact two ways | FIXED | R14 and the three real-name blocks |
| 2.122 | Open questions answered: "never" vs "-" for never-seen | FIXED | `never`, `now`; `-` means not applicable (who --detail) |
| 2.123 | Open questions answered: README dependency on `docs/nats-setup.md`; rename one of the two | FIXED | The targets name `docs/tls-and-clusters.md`; the README is not in scope |
| 2.124 | Open questions answered: `.creds` contents (P66 vs R175) | FIXED | Verified: it holds the private key; one recommendation, section 4 |
| 2.125 | Open questions answered: rule 28 vs rule 30 | FIXED | Commands win (L10, R9) |
| 2.126 | Open questions answered: `MASH_GROUP`/`MASH_NAME` compatibility is the owner's call | FIXED | Planner: removed, no compatibility (R13) |
| 2.127 | F. Verdict, command output: not ready; first steps are one address in one variable, `error:` and true fixes, status rebuilt, table width and one-form rules, version 0.5.6 and exit codes | FIXED | All five steps are in DRAFT3: R13, R4, R11, R14, L9 and section 2 |
| 2.128 | F. Verdict, README: not ready; publish the release and the guide, ship mashctl as an asset, use `--make-key`, end with an agent mailing, clean up the server | NOT TAKEN | Out of scope: not about the target outputs |

Counts: 128 rows; FIXED 110; CHANGED OTHERWISE 12; NOT TAKEN 6.

## Security read of the contract (rev 2 and 3)

Date: 2026-11-19. Source: the security read of contract mash-0.5.6-cli-ux
(revision 2 and revision 3, draft: INV-001, INV-003, INV-004, REQ-003,
REQ-004, REQ-006, REQ-009, REQ-010, AT-007) and the lead's binding items.
Target document: `docs/plans/2026-11-19-cli-targets-mash.md` (DRAFT4; rules
R15 to R18 and S1 to S7 are new there). Dispositions: FIXED, CHANGED
OTHERWISE, NOT CONFORMED (with the reason).

| # | requirement | disposition | where in DRAFT4, or why not |
|---|---|---|---|
| S1 | INV-003: no secret on the command line; `--key`, `--token`, `--creds` take files, `-` for stdin; the refusal for a pasted value | FIXED | R16; `install-stdin` (the stdin form, once) and the `install-refuse` block `--token takes a file name, not the token itself` (value shown only as `eyJ...`, never echoed; fix names a file and says to ask for a new token); `--key` and `--creds` say the same; help of `credential install`; JSON and `--detail` print paths only |
| S2 | REQ-003/INV-004: `--ca-fingerprint` required, the first install included; mashctl prints it; the mismatch and missing-flag refusals | CHANGED OTHERWISE | Required whenever the install writes or changes the CA (S2 in section 7): a second endpoint on the same machine and a server with a public CA change no CA, so no flag. Blocks: the server hand-over (`server`: the `Give the person these` lines, `CA fingerprint` header and the value on its own line), `install-main` (flag and the `matches` line), `install-refuse` (missing flag; `the CA in ca.pem has fingerprint ... not ...`, first install) |
| S3 | One fingerprint form; `status --detail` shows it | FIXED | R15: lower-case hex SHA-256, 8 groups of 8 joined by `-`, on its own line; used in mashctl's hand-over, flag, confirmation, errors, JSON `ca_fingerprint`, and `status-detail` (`CA fingerprint` line) |
| S4 | Trust change: old and new server and CA fingerprint, `--replace` AND a typed confirmation on a terminal, no `--yes`, no-terminal refusal, atomic, current user only, precedence | FIXED | R17; `install-trust` (the confirmation screen ending `Type the new CA fingerprint to confirm: `, the success screen, the wrong-typed refusal, the no-terminal refusal); `install-refuse` (`already trusts a different server`); order of checks written under the blocks; precedence flags, token, config, never the environment (section 3) |
| S5 | REQ-009 auto-pick: only the current user's config dir, owned regular 0600, exactly one; zero or several is a choice error; status names how chosen; blocks for several, none, wrong owner or mode | CHANGED OTHERWISE | Section 3 and R13; `status-choose` (several, candidates listed, fix names `"endpoint"` in config.json and `MASH_ENDPOINT`, file first), `status-none` (new), `status-unsafe` (new, `UNSAFE`, `chmod 600`), `send-refuse` (several; `is readable by others`); "how chosen" is a row of `--detail` and in MCP `chosen_by`, not a default row, because REQ-004 fixes the default rows (S4 in section 7) |
| S6 | REQ-010: plain text only by `MASH_INSECURE_PLAINTEXT=1` for that run; the config key removed and refused; the refusal says credentials and mail cross unencrypted, test servers only; every connecting command warns; status shows it on the server row | FIXED | R18; section 3 table; `send-refuse` (`insecure_plaintext is not a setting any more`, and the plain-URL refusal with the cost stated); `send-warning` (the one-line warning, once, under send); `status-plain-warning`, `status-plain-inherited` (new), `status-plain-refused`, `serve`, `install-refuse` (plain `--server`); help-settings (`an agent must never set it`); MCP instructions |
| S7 | Secret files: exclusive creation, no symlink following, 0600 from creation, atomic, refuse an existing different file | CHANGED OTHERWISE | "File safety" in section 2; `key-refuse` (symlink), `install-refuse` (existing different credentials file; symlink). `--force` is REMOVED because it contradicts INV-003 (S1 in section 7); the reissue fix now moves the old file aside (`status-refused`) |
| S8 | Revert the `NEXT:` under the inbox listing: listings print no NEXT | FIXED | All five inbox blocks lose it; the long-list hint is the sentence `Older messages: add --before 56.`; empty `who` also loses its `NEXT:` (a listing) (S7 in section 7) |
| S9 | Rev 3 on the plugin, hook and MCP output (INV-001, REQ-006) | FIXED | The contract has no other line on them. INV-001 is applied: no tool field or hook text holds a token, key or credentials contents (sections 5 and 6); MCP `status` gains `chosen_by` and `server.plaintext_override`, detail gains `ca_fingerprint` (names of settings, never values); new error codes `credentials_unsafe`, `setting_removed`; instructions say an agent never sets the override |
| S10 | REQ-001 R-true-fix: configuration fixes name the file, not a shell export | FIXED | The `send` and `serve` choice errors now give the config.json line first and the `export` after (R10 states it for every command); the same-machine `credential install` hint names config.json first |
| S11 | The `trust` fixes told the person to `cp` a CA over `ca.pem` | FIXED | That would skip the fingerprint and the confirmation (INV-004); the fix is `credential install --creds <installed file> --ca ... --ca-fingerprint ... --replace` (S6 in section 7) |
| S12 | INV-001/INV-003: the token is a secret file (mashctl labelled it "holds no secret") | FIXED | Written mode 600, label `(mode 600)` in the hand-over; the CA copy stays 644 |
| S13 | AT-007 cases that have an output | FIXED | Existing file, different file, symlink (`key-refuse`, `install-refuse`); zero, several, foreign-owned credentials (`status-none`, `status-choose`, `status-unsafe`, `send-refuse`); trust change with no terminal and with a wrong typed fingerprint (`install-trust`); substituted token with the operator's fingerprint (`install-refuse`, mismatch); override inherited (`status-plain-inherited`, `send-warning`). The helper-script cases (swapped parent, substituted download) are not command output of mash |

Counts: 13 rows; FIXED 10; CHANGED OTHERWISE 3 (S2, S5, S7 as numbered in this table).


## Review of the quick start (critic round 1 on DRAFT2, junior developer)

Date: 2026-11-19. Source: the junior developer's read of the README quick
start DRAFT2 (every finding of that review, in the order of its walkthrough,
then its collected "sloppy or inconsistent" list). DRAFT3 is
`docs/plans/2026-11-19-readme-quickstart-targets-mash.md`: a rewrite, with its
output blocks copied from the targets (DRAFT4) and checked by
`lint-quickstart.py` (a design for the contract, kept outside the repository).
Section names below are those of DRAFT3.

| # | finding | disposition | where in DRAFT3, or why not |
|---|---|---|---|
| 1 | The note at the top is for reviewers, but is read first, and uses words the reader does not know ("skill", "After outputs", "R9 and R10", "the target's cast") | FIXED | The README text has no reviewer note. The one note is the last section, "Notes for reviewers"; lint fails if it is not last |
| 2 | Two things the reader needs are missing before step 1 (the guide, the unpublished release) | FIXED | "Prerequisites" 3 and 4 name the release (0.5.6, `v0.5.6`) and the guide (`docs/skills/mash-nats-local/SKILL.md`); dependencies are listed in the notes |
| 3 | "NATS" never explained; is "mash server" the NATS server? | FIXED | "The words this page uses": "mash mail rides on a NATS server; the mash server and the NATS server are the same thing" |
| 4 | "Endpoint" used in the intro, defined in step 3; has a mailbox or is the mailbox? | FIXED | One definition in the words list: an endpoint is one mailbox, with an address; the intro no longer says it "has" one |
| 5 | Where `acme` and `demo` are decided is never said | FIXED | Words list: owner is what the server was set up with (chosen in prerequisite 4); group is the name you give when you create the endpoint; both are in the address |
| 6 | `go version` and `claude --version` checks not given | CHANGED OTHERWISE | No Go any more (finding 14). Prerequisite 1 names `gh auth status` for `gh`; `curl`, `claude` are named, and the install and Step 3 would fail loudly without them. Not a command block, to keep the count down |
| 7 | Go 1.27.4 and `GOBIN`/`GOPATH` paragraph | FIXED | Removed: `mashctl` is a release download (finding 14) |
| 8 | "Channels", "Console sign-in", Bedrock and others unexplained | CHANGED OTHERWISE | Prerequisite 1 keeps the sign-in condition in one clause; "channel" is defined once in Step 3, where it matters |
| 9 | `curl`, `sha256sum`/`shasum`, `gh` not listed; `gh` appears after the block that uses it; `gh` login | FIXED | Prerequisite 1 lists `curl` and `gh` signed in; Prerequisite 3 command 1 sets `SUM` per platform; command 3 says how to skip `gh` (`ok=yes`) |
| 10 | The server is only in another guide; the page does not say what the guide leaves | FIXED | Prerequisite 4 ends "You now hold": server address, `ca.pem` at `~/mash-demo/nats/ca.pem`, the owner, the CA fingerprint; the answers to give the guide are listed |
| 11 | The guide "ends with `mashctl` able to reach the server", but `mashctl` is installed after it | FIXED | Order chosen and explained in Prerequisite 3: install first, then the server (the guide needs nothing of ours until its last step, which runs `mashctl`, and the helper is in the release) |
| 12 | No one-line check that the server is up before step 1 | CHANGED OTHERWISE | The guide has `--check`; the page names it in "If `mash status` says something else". Step 1's first status is the page's own check |
| 13 | The `unset $(env \| sed ...)` line: empty case, and it clears `MASH_CONFIG_DIR` | FIXED | Replaced by one sentence and `env \| grep '^MASH_'`, which prints nothing (Prerequisite 2 command 2) |
| 14 | `<version>` has no value, form unknown, release unpublished, and a literal `<` redirects | FIXED | Written `0.5.6` and `v0.5.6` in the install blocks; no angle bracket in any typed block (lint rule 4). The release is still a dependency (notes) |
| 15 | Comments inside an `&&` chain; macOS hint after the line it changes; six edits to change platform | FIXED | No comment in any typed block (lint); the macOS block comes before the Linux block (Prerequisite 3 command 1); one `P` and one `SUM` carry the platform |
| 16 | The last `case` line is not joined with `&&` | FIXED | The install is one chain that stops on any failure and prints `STOP`; the `case` line is gone; `export PATH` ends the chain, and a sentence covers new terminals |
| 17 | The block promises output and shows none; no good or bad `gh` output | FIXED | Each command has its output: `attested: yes`, the three `OK` lines, `mash version`, `mashctl version`. The `gh` output is silenced and summarised by `attested:` |
| 18 | "command 1 of 2" on ten lines | FIXED | A command is defined as one block pasted; every section counts them (lint rule 6 checks `Command k of m`) |
| 19 | Not told how to put `$BIN` on `PATH` | FIXED | `export PATH` in the chain, and one sentence for `~/.bashrc` and `~/.zshrc` |
| 20 | `SHA256SUMS` left behind though cleanup is called exact | FIXED | "What stays on disk, and why" lists every download, including `SHA256SUMS` and `mine.sums` |
| 21 | `docs/release.md`, `docs/operations.md` not links | FIXED | `docs/operations.md` is a link; `docs/release.md` is no longer mentioned (the attestation is explained in one sentence) |
| 22 | `mashctl` needs Go, public or private repository unknown | FIXED | No Go; both programs are downloads from the release, checked the same way (finding 14); `gh` sign-in is a prerequisite |
| 23 | `mashctl version` shows nothing; no check | FIXED | Prerequisite 3 command 6, with output (marked `external`: not in the mash targets) |
| 24 | How `mashctl` finds the server, and what it needs, is unknown at install | FIXED | Prerequisite 4 says the guide hands the server to `mashctl` (adopt, init) and that `mashctl` writes the address into mash's settings |
| 25 | Which directory for the files; step 10 says "where you ran steps 2 and 3" | FIXED | One working directory, `~/mash-demo`, from Prerequisite 2; every path later is exact |
| 26 | Owner and group "stated, not explained" | FIXED | See 5 |
| 27 | Two `next:` lines (tool and page) look alike and disagree at steps 4, 5, 7 | FIXED | The page writes no `NEXT`/`next:` line at all; the only ones are the tools' own, inside marked blocks (lint rule 3). Links between steps are plain headings |
| 28 | The three-command flow taught first (steps 2-4), then "one command does all three" | FIXED | The quick start uses only `mashctl endpoint create ... --make-key`; the three-command flow is a link to `docs/operations.md`, where `--server` and `--ca` make sense |
| 29 | `--ca ca.pem` and `--server tls://myserver:4222` unclear: where the file is, is the machine called `myserver`, what `tls://` and `4222` are | FIXED | Prerequisite 4 names the file and path; `myserver` is explained once ("use its name") and typed only in the helper command; no later command names the server |
| 30 | "Keep alice.key" contradicts the leftover `rm alice.key` | FIXED | No `alice.key` file exists in this flow; the private key is inside the credentials file, "the backup of the key: guard it" (Step 1), and the credentials files are listed as dead after Step 5 |
| 31 | The group/name "from the token" versus "from the environment" paragraph; when are the variables needed | FIXED | One setting, `MASH_ENDPOINT=acme.demo.alice`, or the `"endpoint"` line of config.json; the words list says when it is needed (more than one endpoint installed); `MASH_GROUP`, `MASH_NAME`, `MASH_OWNER` appear nowhere (lint rule 5) |
| 32 | `credentials`, `credential`, `.creds` are one thing in three spellings | CHANGED OTHERWISE | The prose says "credentials" (the file `*.creds`); `credential install` is no longer used on the page |
| 33 | The three `wrote`/`copied` lines have different shapes | NOT TAKEN | The shape is the target's (`install-make`); the quick start copies it |
| 34 | Alice's key in the working directory, Bob's in `~/.config/mash` | FIXED | No key file outside `~/.config/mash`; all endpoints use `--make-key` |
| 35 | Step 5 repeats `wrote config.json` and `copied ca.pem` for the second endpoint: overwritten? | FIXED | Step 1 command 2 says the second endpoint reports both as `kept` and unchanged (the targets' rule); no second output block is invented |
| 36 | "`mashctl` already knows the server and the CA": how? | FIXED | Prerequisite 4 (the guide's adopt step) and Step 1's introduction |
| 37 | Bob's sign-in never checked | FIXED | Step 1 command 3 shows the refusal that says to choose; Step 2 shows each endpoint working by sending and reading as it; Step 5 checks Bob last |
| 38 | "you, as alice" never defined; first thought `su alice` | FIXED | The words list defines it: `MASH_ENDPOINT` names it, or the only installed one; no Unix user or login |
| 39 | The `tls://myserver:4222` typed versus `myserver:4222` printed | NOT TAKEN | `mash status` prints the address as the target has it (`tls://myserver:4222`); the page no longer types the address, so the two forms never meet |
| 40 | Only the healthy status is shown; failures, and what to do, unknown | FIXED | Step 1 shows the choose refusal with its fix; "If `mash status` says something else" shows the unreachable screen with its fix and the guide's `--check`; Step 5 shows the DELETED screen |
| 41 | `--kind question`: what kinds exist; why is the plain mail a `task` | FIXED | The target now prints kind `message` for plain mail (`inbox` block); the page uses `--kind question` once and the blocks show it |
| 42 | "12m ago" for mail just sent | FIXED | The target prints `10s ago` and `8s ago` |
| 43 | One thing in different forms (`new, urgent`/`question`/`urgent question`) | FIXED | The target separates STATE and URGENCY columns; the page copies it |
| 44 | `TRUST verified`: what else can it be, what to do | FIXED | Step 2: `All verified` explained; a message that is not verified says so and is information, not an instruction |
| 45 | The round trip (Bob answers, Alice reads) never happens | CHANGED OTHERWISE | The page shows one direction in the shell and again in a Claude Code session (the aim of 11 actions); the reply is the `NEXT` line of `mash read`, which the page does not use |
| 46 | I became Bob by changing one variable; what stops that on a real setup? | CHANGED OTHERWISE | Step 2 says every message is signed with the key of the endpoint that sent it; the person who holds the files is that endpoint, as on any host. No claim beyond that |
| 47 | The leading space, the indent differences in the tables | NOT TAKEN | Layout is the target's (`inbox`, `read`) and is copied byte for byte |
| 48 | `mash who` shows the endpoint running the command as offline and never seen | FIXED | `mash who` is no longer on the page |
| 49 | Step 9 words: plugin, marketplace, `mash@mash`, channels, allowlist, wake | FIXED | Step 3 defines plugin, marketplace, `mash@mash` and channel, each in one line |
| 50 | The `--dangerously-...` flag: what is the danger | CHANGED OTHERWISE | Step 3 says what it does and does not do (lets mash wake an idle session; without it mail arrives at the next prompt or tool call) and that Claude Code asks to confirm; it does not claim a danger the page cannot state |
| 51 | No counter and no output for the two `claude plugin` commands | FIXED | Step 3 counts three commands; the two `claude plugin` lines are one paste, with no output block (mash prints nothing; Claude Code does) |
| 52 | The last step stops before an agent sends or receives; the step the page exists for has no proof | FIXED | Step 3 ends with what the agent in Alice's session sees (`hook` block, `verified`); Step 4 sends again from your shell and Alice's open session shows it |
| 53 | Second terminal for Bob: the whole `export` line must be retyped | FIXED | Only Alice's session is started; the launch is two pasteable lines, `export MASH_ENDPOINT=acme.demo.alice` then `claude ...`, with `cd ~/mash-demo` for a new terminal |
| 54 | How does the person get an address of their own? | FIXED | Step 4: `mashctl endpoint create acme.demo.you --make-key`, `export MASH_ENDPOINT=acme.demo.you`, `mash send` |
| 55 | Step 10 removes only local files; nothing removes the endpoints on the server | FIXED | Step 5: `mashctl endpoint delete` for Bob, Alice and you, then the refused credentials |
| 56 | `claude plugin marketplace remove mash`: where did the name come from | FIXED | Step 3 defines `mash@mash` (plugin, then marketplace); the removal lines are in "What stays on disk, and why" |
| 57 | "`mashctl` has no stop verb (checked in its source)" reads as a developer note | FIXED | Dropped; the table says the server is stopped as the guide says (its `--stop`) |
| 58 | The programs themselves and `SHA256SUMS` not removed or mentioned | FIXED | Both are rows of "What stays on disk, and why" |
| 59 | "What is different from 0.5.5" means nothing to a new reader | FIXED | Removed from the page; the section is not in the README text at all |
| 60 | "Nothing is cleared: scroll back at any time" answers a problem the reader never had | FIXED | Removed |
| 61 | Counters inconsistent (1-A, steps 2-6 per step, step 7 with a missing number, none in 9 and 10) | FIXED | One rule everywhere: "Command k of m" per section, counted per paste, checked by lint rule 6 |
| 62 | `new, urgent` vs `question` vs `urgent question` and other forms of one fact | FIXED | See 43 |
| 63 | Column alignment fine, no line over 80 columns | FIXED | Still true: the widest printed line is 76 columns; typed lines are at most 69 |

Counts: 63 rows; FIXED 53; CHANGED OTHERWISE 7 (6, 8, 12, 32, 45, 46, 50);
NOT TAKEN 3 (33, 39, 47).

## Critic round 2, part C (README)

Source: round 2, section C ("The README quick start") and E.4 ("cannot be
followed, and is not minimal"), on DRAFT2. The critic's outline is followed:
install; Bob and Alice with `--make-key`; status; mail; Claude Code; clean-up;
the remote flow only as a link.

| # | finding | disposition | where in DRAFT3, or why not |
|---|---|---|---|
| C1 | The guide "ends with `mashctl` able to reach it" but `mashctl` is installed after | FIXED | Same as 11: the order is chosen and explained (Prerequisite 3) |
| C2 | The guide does not exist yet and is named differently in the packet | FIXED | One name, `docs/skills/mash-nats-local/SKILL.md`, with the helper `scripts/nats-local-setup.sh`; both are in the notes' dependency list |
| C3 | `gh` and `gh auth login` and `curl` missing from the prerequisites | FIXED | Prerequisite 1, and `ok=yes` to skip the attestation |
| C4 | The `unset $(env \| sed ...)` line is clever shell; empty case fails in zsh; `mash status` should name any `MASH_*` override | CHANGED OTHERWISE | Replaced by `env \| grep '^MASH_'` (prints nothing). `mash status` naming an override is the CLI's, not the page's |
| C5 | Step 1-A: thirteen lines of nine commands, linux-amd64 only, macOS edit mid-chain, unpublished release, `<version>` form | FIXED | One `P`/`SUM`/`BIN` block, a macOS block before it, a chain without comments, `0.5.6` and `v0.5.6` stated |
| C6 | Step 1-B: Go 1.27.4 only to get `mashctl`; ship it as a release asset | FIXED | `mashctl-<os>-<arch>` is downloaded and checked beside `mash` (dependency: release lane) |
| C7 | `--ca ca.pem` is a relative path: which directory, which guide step | FIXED | The page no longer passes `--ca`; the guide's `ca.pem` is named at `~/mash-demo/nats/ca.pem` in Prerequisite 4 |
| C8 | After step 4 the tool says `mash status`; the README goes to step 5 | FIXED | The page's order follows the tool's hints; status is run before mail |
| C9 | "mash read the group and name from the token", then types them anyway | FIXED | One setting, `MASH_ENDPOINT`; see 31 |
| C10 | Step 5 output says `wrote` for files step 4 wrote; the packet says `kept` | FIXED | Step 1 command 2 says `kept`, and no invented second block |
| C11 | `bob.key` in `~/.config/mash`, `alice.key` in the working directory | FIXED | See 34 |
| C12 | "`mashctl` already knows the server and the CA": how | FIXED | See 36 |
| C13 | `MASH_GROUP`/`MASH_NAME` exported and nothing unsets them | FIXED | Gone; `MASH_ENDPOINT` is one variable, and the last section says to close the shell or `unset MASH_ENDPOINT` |
| C14 | Step 9 ends at launching; the quick start never shows an agent sending or receiving | FIXED | Step 3 and Step 4. Shown: the agent receiving (the `hook` block). Not shown: the agent sending (the MCP `send` tool); the page has no block for it |
| C15 | Step 10 removes local files, not the server's endpoints: "remove what the steps made, and nothing else" is false | FIXED | Step 5 deletes the endpoints; "What stays on disk, and why" does not claim to remove everything |
| C16 | The README's `next:` lines imitate the tool's | FIXED | See 27 |
| C17 | Clean up the cast and the count: 31 actions where about 11 would do after install | CHANGED OTHERWISE | 14 pastes in 5 steps after the prerequisites. The core the critic counted (two endpoints, status, send, inbox, plugin, launch) is 11; the three the planner and lead added are Alice's session proof, your own address and removing access |
| C18 | The remote three-step flow belongs under "An endpoint on another machine" | FIXED | One link, to `docs/operations.md`, in Step 1 |
| E4.1 | Depends on an unpublished release and an unwritten guide | CHANGED OTHERWISE | Still true, by design: both are named as dependencies in the notes, and the page is written to them |
| E4.2 | Cleanup leaves the server endpoints and the shell exports | FIXED | See C13, C15 |
| E4.3 | "Before you start": a mash server from the guide (it prints the path of `ca.pem`), Claude Code with channels, `curl` and `gh` | FIXED | "Prerequisites" 1 to 4, each ending "You now hold" |

Counts: 21 rows (C1 to C18, E4.1 to E4.3); FIXED 18; CHANGED OTHERWISE 3 (C4,
C17, E4.1); NOT TAKEN 0.


## Critic round 3 (packet DRAFT4 and README DRAFT3)

Source: the CLI-UX critic's cold read of packet DRAFT4 and the README quick
start DRAFT3 (`review2c.md`, sections A to F). Rows D1 to D15 are the numbered
defects of section E; P1 to P41 are its polish list, in the order the critic
gave it; C1, C2 and E1 to E8 are findings of sections C and D that the lists
do not carry; F1 and F2 are the verdicts. Section A found no line over 80 in
a default screen and aligned tables; those passes need no change. Where DRAFT5
means the targets file, the packet or the quick start, it says which. The
lead's binding decisions of 2026-11-19 (D1, D2, D4, D7, the fingerprint, D9 to
D11) shaped several dispositions (CHANGED OTHERWISE where the critic's own
rewrite was not the one taken).

| # | finding | disposition | where in DRAFT5, or why not |
|---|---|---|---|
| D1 | status: "last seen" contradicts "alive" (server row; README Step 5 shows `last seen 3h ago` minutes after Bob sent mail) | CHANGED OTHERWISE | The lead decided the server row, not the credentials row: `alive, encrypted (checked 09:42)` when reached, `NOT REACHABLE (last reached 3h ago)` when not, `not checked` otherwise; never `alive` with a past time (targets R11, every `status-*` block, `status --detail`; MCP `server.state` alive/not_reachable/not_checked, `server.checked_at`, `server.last_reached`; decision S8). `LAST SEEN` means an endpoint's last mail activity, in `mash who` only. The quick start's Step 5 proof uses `status-deleted` and no longer shows the contradiction |
| D2 | status, certificate not trusted: `credentials not checked (the server refused the connection)` has the refusal backwards | FIXED | `trust` blocks: verdict "mash does not trust the server", credentials `not checked (mash did not trust the server)`, and the send error `mash does not trust the server at myserver:4222: its certificate is not from your CA` (S9) |
| D3 | storage name contradicts itself: `send --detail` says the recipient's, `read --detail` the sender's | FIXED | `read --detail` now reads `storage name  acme_demo_alice (yours)`; `send --detail` keeps `(the recipient's)` |
| D4 | a JSON line `"endpoint": ...` printed at command position (second endpoint NEXT, status, send and serve none-chosen) | FIXED | R10 amended (CLI rule 30): `add this line to <path>:` then the line indented four spaces, never in command position; the shell form is a separate item labelled `for this shell only:`. `status-choose`, `send-refuse`, `serve` and the plain-text screens; the second-endpoint NEXT (`install-second`, `install-make-second`) now ends in two real commands (`export`, `mash status`). The lint (rule 9) fails a two-space JSON line after `fix:` or `NEXT:` |
| D5 | a fix uses `credential install --creds ... --ca ... --replace`, a form `mash help credential install` does not allow | FIXED | `help` for `credential install` lists `--key FILE --token FILE [--server URL] [--ca FILE] [--replace]` and the same with `--creds FILE`; the `trust` fixes print `--creds <file> --ca <ca file> --replace` |
| D6 | `--creds-out` appears from nowhere (status deleted fix; `mashctl endpoint delete` NEXT) | CHANGED OTHERWISE | `status-deleted` fix is `mash key create --out <new-name>.key` (a command the page has met); `ctl-delete` has no NEXT at all (D7). `--creds-out` is named only in the mashctl note, never in a `mash` screen (section 8) |
| D7 | `mashctl endpoint delete` hint names the wrong next step (create an endpoint) | FIXED | R19 (new, explicit rule: removing access prints no NEXT, at most the proving command as its own item, never a suggestion to create). `ctl-delete` loses its NEXT and now shows what will be lost, the typed address and the result; quick start Step 5 gives the proof as its own command |
| D8 | pasted-token fix uses the leaked token, then says it is burned | FIXED | `install-refuse` (first block): the fix says the token is in the shell history, ask for a new one, save it to a file, then the command |
| D9 | README prerequisite 4 cannot be done in the order written (run the command or the guide? adopt and init not shown; nothing installs nats-server) | FIXED | Quick start prerequisite 4: one sentence, the helper's own "What you hold" lines, then `mashctl adopt` (the command the helper prints), `mashctl init` and one ready check (`mashctl status`) with their outputs, and the full link to the guide. The helper downloads and checks `nats-server` itself (said in the sentence); prerequisite 1 states its platform (Debian or Ubuntu x86-64) and tools |
| D10 | README `--host myserver` is not pasteable | CHANGED OTHERWISE | No `--host` at all: the helper's default is this machine only, address `tls://localhost:4222` (the helper's own output). The page says where outputs show `myserver`, yours shows `localhost`. `--host $(hostname)` was not used because the helper listens on 127.0.0.1 by default and a host name may resolve elsewhere (Debian maps it to 127.0.1.1); not verified, so not offered |
| D11 | README Step 3: a new terminal without mash on the PATH | FIXED | Prerequisite 3 command 5 (mandatory) writes the PATH line to `~/.zshrc` or `~/.bashrc`, and says what is true: the exported plugin carries no copy and no full path, it runs the first `mash` on the PATH (or `MASH_BIN`) (docs/install.md section 1, `plugin/scripts/mash`); step 3 command 3 checks `command -v mash` in the new terminal and says what to do if empty |
| D12 | README prerequisite 1: wrong cross-reference ("step 3 says how to skip it") | FIXED | "prerequisite 3, command 3, says how to skip it" |
| D13 | README Step 1 command 2: output unspecified and its hint would be wrong | FIXED | New target `install-make-second` (mashctl second `--make-key`: `kept` config and CA, NEXT is the two-command choice); quoted in Step 1 command 3 |
| D14 | README Step 4: "It prints what step 1 printed for Bob" is false | FIXED | "It prints what it printed for Alice, with `you` for `alice`" (Step 4 command 1); the NEXT sentence has no count, so it is true for the third endpoint |
| D15 | README: "endpoints in a group can see each other" contradicted by `mash who` | FIXED | The word list says what a group does: sorts endpoints (`mash who --by-group`) and lets a mail table show a sender in your own group by name alone; nothing is claimed about limiting mail |
| P1 | `colour` in `mash help --detail` (rest is US English) | FIXED | `help-settings`: `NO_COLOR ... set to switch color off` |
| P2 | drop the `NATS word  seed` row of `key create --detail` (a glossary entry, rules 24 and 29) | NOT TAKEN | Contract rev 5 REQ-001 and REQ-003 say `mash key create` explains "NATS calls it a seed" once and `--detail` is where it lives; kept |
| P3 | "CA" and "TLS" first shown without plain words | FIXED | `server` block explains the CA in its own words; the plain-address error says "an encrypted tls:// connection" |
| P4 | `sha256 of ca.pem` is jargon: "a checksum of ca.pem" | FIXED | `server` block: `a checksum of ca.pem; the person types it when installing` |
| P5 | mashctl create's `<fingerprint>` placeholder breaks rule 30 on purpose; say why | FIXED | Gone: there is no fingerprint flag, the hint is a plain command and says mash asks for the fingerprint |
| P6 | help example `--token bob.token`; every other place uses `acme.demo.bob.token` | FIXED | `help credential install` example uses `acme.demo.bob.token` |
| P7 | the newhost CA has the same fingerprint as myserver's; the conflict example replaces oldhost with myserver while the `--replace` example uses newhost | FIXED | Three distinct fingerprints (18b3f713..., 92d4e0a7..., the wrong one 4f6a2459...); both examples use `newhost` |
| P8 | timeouts differ unexplained (status 2s, send 20s, prompt 15m) | FIXED | Section 2, "Timeouts" says why each is what it is; the `send` refusal points to it |
| P9 | status "none installed" and "readable by others" skip a server check the other states make | FIXED | R11: the server is checked in every state with an address and a tls scheme, credentials or not; `status-none` and `status-unsafe` show `alive, encrypted (checked 09:42)` |
| P10 | "deleted or reissued" vs DELETED stated outright elsewhere | FIXED | `status-refused` says `REFUSED (deleted, or its key was reissued)` and is shown only for a server that cannot say which; `status-deleted` is the one that can |
| P11 | `mashctl endpoint show` appears nowhere else | FIXED | `status-refused` fix asks whoever runs the server which of the two it was, and names no command that is not in this document |
| P12 | plaintext-allowed fix should also unset MASH_INSECURE_PLAINTEXT | FIXED | `status-plain-warning` fix: `unset MASH_SERVER MASH_INSECURE_PLAINTEXT` |
| P13 | show MASH_SERVER exported, not inline, where the fix is `unset` | FIXED | `status-plain-warning`, `status-plain-refused`, `send-refuse` (plain URL) set it with `export` |
| P14 | one override warning sentence for status and send | FIXED | `status-plain-inherited` uses `warning: MASH_INSECURE_PLAINTEXT is set; mail may not be encrypted.`, the sentence `send` and `serve` print |
| P15 | status plaintext row repeats the warning (rule 25) | FIXED | `status-plain-warning`: the row says why it is allowed, the warning says what is at stake (`anyone on the network can read your credentials and your mail`) |
| P16 | "already trusts a different server" names `--replace` but prints no command | FIXED | `install-refuse`: the fix is the whole command with `--replace` added, built from what the person typed |
| P17 | "no server known" fix omits `--ca` | FIXED | `install-refuse` (no server known) fix shows `--server tls://<host>:4222 --ca <ca file>`; `status-noconfig` says to add `--ca <ca file>` for a server with its own CA |
| P18 | typing a 71-character fingerprint to confirm `--replace` is hostile | NOT TAKEN | The lead's ruling (b) and contract INV-004 and REQ-003 require the person to type it; pasting what the operator gave is the expected way (R15 says so) |
| P19 | prose interleaved between fix commands | FIXED | `install-refuse` (pasted token), `status-none`, `status-refused`, `status-plain-refused`: the prose comes first, the commands last |
| P20 | `Older messages: add --before 56.` should be a whole command on its own line | FIXED | `inbox` (`--limit 2`): `Older messages are before #56. To see them:` and `mash inbox --limit 2 --before 56`; decision S7 |
| P21 | full five-term trust legend under inbox `--detail` when all are verified | FIXED | The legend appears only when a row is not verified, in the default view and under `--detail` |
| P22 | trailing blank line after the default inbox table | FIXED | Removed from the `inbox` block |
| P23 | `mash send bob "hi"` example suggests mailing bob from an example where you are Bob | FIXED | `send-refuse`: the sender is bob, the bare name is `alice`, the fix is `mash send acme.demo.alice "hi"` |
| P24 | `No endpoints are registered.` cannot happen for a registered caller | FIXED | Block removed; the `who` text says why there is no empty screen |
| P25 | define online and offline in `who` (offline 3h ago on the `*` row) | FIXED | Every `who` block ends with `online: a Claude Code session is serving that endpoint now`; the text says the caller at a shell is offline unless a session also runs as it |
| P26 | `who --by-group` could use short names under the group heading (rule 25) | NOT TAKEN | R14: a person pastes the address into `send`, so every row keeps the full address; said in the text |
| P27 | `init --local` fix prints two file paths at command position, and `scripts/...` assumes a clone | FIXED | `ctl-init-local`: the fix prints the guide's URL, indented under `fix:` text, not a command; the text notes the release ships the script as `nats-local-setup.sh` |
| P28 | MCP `who` example omits listed fields (role, description, labels, caps) | FIXED | The `who` example carries all four on every endpoint (null, `{}`, `[]` when none are recorded) and the field table gives their types |
| P29 | MCP `status` fix for credentials_refused differs from the CLI | FIXED | The failure example's message and fix say the same as `status-refused` (deleted, or reissued; ask whoever runs the server) |
| P30 | `help send` options omit `--file` | FIXED | `help send` lists `--file FILE` |
| P31 | README: "the only place this page names it" (the server address) is not so | FIXED | Sentence rewritten: after the adopt command the address is never typed again |
| P32 | README: "every file goes in ~/mash-demo" vs `~/.local/bin` | FIXED | Prerequisite 2 says apart from `~/.config/mash` and `~/.local/bin` |
| P33 | README: "a `fix:` you can run" vs the unreachable fix, which is prose | FIXED | "a `fix:`"; and the page gives the `--check` and `--start` commands as blocks under the unreachable screen |
| P34 | give `--stop` and `--check` as blocks | FIXED | Quick start: `--stop` in the clean-up, `--check` and `--start` under "If `mash status` says something else" |
| P35 | uninstall the plugin before removing its folder | FIXED | Clean-up block order: `claude plugin uninstall`, `marketplace remove`, then `rm -r` |
| P36 | `mash version` and `mashctl version` in one paste | FIXED | Prerequisite 3 command 6 is one paste with both |
| P37 | avoid the deliberate refusal in Step 1 | CHANGED OTHERWISE | Step 1 now shows the healthy `mash status` first (command 2, one endpoint installed) and the choose-refusal once, after Alice exists (command 4); it is not avoidable once two endpoints exist, and it teaches the fix |
| P38 | README: "because every message is signed" does not follow | FIXED | "mash tells the two apart by the key each one signs with" |
| P39 | README Step 3 tool list omits reply and version | FIXED | "send, inbox, read, who, reply, version and status" |
| P40 | README repeats the "guard that file" line the output already says | FIXED | The repeated sentence is removed from Step 1 |
| P41 | say on the page that the NATS helper is a separate tool of another lane (rule 32) | FIXED | Prerequisite 4 says the helper is a separate tool, not part of mash; `ctl-init-local` note says mash starts and configures nothing |
| C1 | status table: idle-session warning, how a shell knows about a Claude Code session is unexplained | CHANGED OTHERWISE | `status-claude` fix says to start Claude Code with the flag from the terminal you want it in; the quick start's step 3 and prerequisite 3 say how a session finds mash |
| C2 | status table: no configuration, `--server <url>` without `--ca` fails on an own-CA server | FIXED | `status-noconfig` fix adds a sentence naming `--ca <ca file>` for a server with its own CA |
| E1 | D, count: export `MASH_ENDPOINT` before the first status so Step 1 has no refusal (-1) | CHANGED OTHERWISE | Healthy status first with one endpoint (no export needed), refusal once later (item 37); no paste saved, one screen gained |
| E2 | D, count: merge `mash version` and `mashctl version` (-1) | FIXED | See polish 36 |
| E3 | D, count: fold the env check into command 1 of prerequisite 2 (-1) | FIXED | Prerequisite 2 is one command with both lines |
| E4 | D, count: make the PATH line part of command 4 (appended to the shell rc) | CHANGED OTHERWISE | Kept as its own command (prerequisite 3, command 5): it is mandatory now (D11) and a long chain is harder to read; net count unchanged |
| E5 | D, prerequisite 4's "you now hold" list is more than a newcomer needs (address, CA file, owner, fingerprint you will not use) | FIXED | The list is the helper's own three lines (server address, CA file, server directory) with the owner named; no fingerprint |
| E6 | D, the NATS bullet says what mash rides on, not what NATS is | FIXED | "a message server program that stores and delivers mail" |
| E7 | D, the two-machine flow stays four artifacts and two hand-offs | NOT TAKEN | The quick start keeps it off the page by design; one link to docs/operations.md. `server` block now says mash asks for the fingerprint, which removes the 139-column command |
| E8 | D, whole page: 9 prerequisite pastes plus uncounted guide, 14, cleanup: at least 26 actions and the guide; not minimal | CHANGED OTHERWISE | Counted honestly in the opening paragraph (item 1052-6): 32 pastes and 11 other actions, 43 in all, with the guide's steps now on the page |
| F1 | Verdict: command output ready to show the owner NO; fix D1, D2, D4, D5, D3, then D6 to D8 | FIXED | All of D1 to D8 and the polish list are applied in the targets (DRAFT5), in the lead's order |
| F2 | Verdict: README quick start ready NO; fix D9, D10, D11, D13, D7, then D12, D14, D15 | FIXED | All applied in the quick start (DRAFT5), in the lead's order |

Counts: 68 rows (D1 to D15, P1 to P41, C1, C2, E1 to E8, F1, F2); FIXED 56; CHANGED OTHERWISE 8; NOT TAKEN 4.


## Junior read of README DRAFT3

Source: the junior developer's read of the quick start DRAFT3 (`review1-d3.md`).
One row per remark of the walk-through (J1 to J51, in the order of the page),
per hindrance of section (2) (H1 to H5), per task of section (3) (T1 to T3),
the verdict of section (4) (T4) and the counts of section (5) (N1 to N3).

| # | finding | disposition | where in DRAFT5, or why not |
|---|---|---|---|
| J1 | Intro: "5 steps and 14 pastes, after the prerequisites" undersells; the junior counted about 23 pastes and a guide | FIXED | Opening paragraph states the honest total, computed from the page (32 pastes, 11 other actions); more than the junior's 23 because the guide's commands are now on the page |
| J2 | Words: "endpoints in a group can see each other" unclear | FIXED | See critic D15 |
| J3 | Words: "copied from the project's target outputs" unclear | FIXED | "These are the outputs mash is built to print, and a script checks every one" |
| J4 | Words: "what you see should match" is not literal (times, host names, darwin file names) | FIXED | "Times, host names and file names will differ on your machine; where an output says myserver, yours says localhost" |
| J5 | Words: "session" only means a Claude Code window | NOT TAKEN | The reader found it clear enough |
| J6 | Prerequisite 1: the skip of the attestation is in prerequisite 3 command 3, not step 3 | FIXED | See critic D12 |
| J7 | Prerequisite 1: "Claude Code with channels", "Console sign-in", "Bedrock, Agent Platform or Foundry" unexplained; no check or minimum version | FIXED | Prerequisite 1 says what a channel is, the requirements (2.1.312 or newer; claude.ai or Console; owner switches Channels on where managed; Bedrock, Agent Platform and Foundry have none; from docs/install.md section 3) and gives `claude --version` as the check |
| J8 | Prerequisite 1: "You now hold: nothing yet" is slightly odd | CHANGED OTHERWISE | "(1) a checked Claude Code" |
| J9 | Prerequisite 2: `mkdir` fails if the directory exists | FIXED | `mkdir -p` |
| J10 | Prerequisite 3: the sentence "Install first, then the server..." is confusing | FIXED | Replaced: the helper "starts the NATS server in prerequisite 4" |
| J11 | Prerequisite 3: command 1, which of linux/darwin and amd64/arm64 (`uname -m`) | FIXED | Command 1 says what `uname -m` prints for each |
| J12 | Prerequisite 3: variables live only in this shell; a new terminal half way breaks commands 2 to 4 silently | FIXED | The opening paragraph lists the variables and says to start again at command 1 in a new terminal |
| J13 | Prerequisite 3: command 2 prints nothing; a private repository fails; no check | FIXED | "It prints nothing; `ls` shows four new files"; a failure means the release is not reachable |
| J14 | Prerequisite 3: command 3, `attested: no` is not explained and gh's messages are thrown away | FIXED | The usual causes (not signed in, `gh` too old) and the command to read gh's message are given under the block |
| J15 | Prerequisite 3: command 4, `STOP:` gives no next move | FIXED | "paste command 2 again, and if it stops again do not use these files" |
| J16 | Prerequisite 3: the new-terminal PATH note matters most in step 3 and is not repeated there | FIXED | See critic D11 |
| J17 | Prerequisite 4: the link is a relative path to a file the reader does not have | FIXED | The full URL https://github.com/bogworks/mash/blob/main/docs/skills/mash-nats-local/SKILL.md |
| J18 | Prerequisite 4: "SKILL.md" and how a document asks questions are unclear | FIXED | The page calls it "the guide for other setups" and gives the answers inline; the helper command no longer depends on the guide |
| J19 | Prerequisite 4: which name does `myserver` stand for; the next command hard-codes it | CHANGED OTHERWISE | See critic D10 |
| J20 | Prerequisite 4: run the command before, during or instead of the guide's helper step; does the helper download the server program | FIXED | See critic D9: the page runs the helper itself; the sentence says it downloads and checks nats-server |
| J21 | Prerequisite 4: no expected output for the helper | FIXED | The helper's "What you hold" lines follow command 1 |
| J22 | Prerequisite 4: `mashctl adopt` and `mashctl init` named but not shown | FIXED | Commands 2 and 3, with outputs; each says what it does |
| J23 | Prerequisite 4: the "apart from the token" sentence about another machine is unclear | FIXED | Removed |
| J24 | Prerequisite 4: no way to prove the server is ready before step 1 | FIXED | Command 4, `mashctl status`, with its output |
| J25 | Step 1: credentials = key plus token is never said in one sentence | FIXED | "(the credentials are the key and the token in one file)" |
| J26 | Step 1: the private key sentence appears in the output and under it | FIXED | The repeated sentence is removed (critic polish 40) |
| J27 | Step 1: command 2 has no output shown | FIXED | `install-make-second` is shown |
| J28 | Step 1: `mash status` prints "mail does NOT work" and "last seen not checked" is not understood | FIXED | The healthy screen comes first, the refusal once and explained; "last seen" is gone from the server row (D1) |
| J29 | Step 2: `--kind` is not explained | FIXED | Command 2 says what a kind is for and which kinds wake an idle session (docs/install.md, spool_from_start) |
| J30 | Step 2: the page says mail waits until read, yet listing is not reading; what makes a message stop being new; no `mash read` for a person | FIXED | Word list defines new and read; Step 2 command 4 is `mash read 1` and `mash read 2` with the output; "Both are `new`: listing marked nothing" |
| J31 | Step 3: command 2 has no expected output | CHANGED OTHERWISE | The paste adds `claude plugin list`, whose last line lists `mash@mash`; Claude Code's own lines are not reproduced |
| J32 | Step 3: a new terminal may lack `mash` on the PATH; say it here | FIXED | See critic D11 |
| J33 | Step 3: "confirm the flag when Claude Code asks": what does the question look like | CHANGED OTHERWISE | "Claude Code shows a warning about loading a development channel and asks you to confirm it" (docs/install.md says it asks at each launch); the exact wording is not reproduced |
| J34 | Step 3: unclear whether the hook text appears on the reader's screen or only the agent sees it; no example answer | FIXED | "you do not see the text itself, only what the agent does with it"; the tool results are given in words and the agent's answer is described |
| J35 | Step 3: only one agent session is ever started; no agent sends and nobody replies | FIXED | Terminals 2 and 3 run Alice's and Bob's sessions; Bob's agent sends, Alice's is woken and answers, Bob's is woken (new second `hook` block) |
| J36 | Step 4: which terminal for command 1 | FIXED | "Back in terminal 1" |
| J37 | Step 4: "the same reply" suggests Alice replied | FIXED | "mash prints the same result for the same command" |
| J38 | Step 4: the live wake-up is a sentence, not an output | CHANGED OTHERWISE | Described in words with the same shape as step 3; no new target block (the only new hook block is step 3's) |
| J39 | Step 5: the permanent consequence comes after the paste | FIXED | A bold warning precedes command 1: cannot be undone, address never reusable, mail deleted, use only on these three endpoints |
| J40 | Step 5: does the tool ask for confirmation | FIXED | The `ctl-delete` block shows the prompt; the page says typing the address is the confirmation |
| J41 | Step 5: the hint contains `--creds-out`, unexplained | FIXED | See critic D6, D7 |
| J42 | Step 5: only deleting everyone is shown, not deleting one and seeing the others work | CHANGED OTHERWISE | Bob is deleted and proven first (command 2), then Alice and you; Alice's session works until deleted |
| J43 | Step 5: "Alice's open session is refused the same way; close it" is told, not shown | NOT TAKEN | Accepted by the reader as believable; the page asks to close it |
| J44 | What stays on disk: the table is the clearest part | NOT TAKEN | No change; one row added for mashctl's key folders and one for the PATH line |
| J45 | What stays on disk: the server is left running; the exact stop line is a guess | FIXED | `./nats-local-setup.sh --dir ~/mash-demo/nats --stop` is a block |
| J46 | What stays on disk: the clean-up removes the plugin folder before uninstalling | FIXED | See polish 35 |
| J47 | What stays on disk: the clean-up block is a fifteenth uncounted paste | FIXED | Counted: 2 pastes in the honest total |
| J48 | If status says something else: the unreachable fix is not runnable; the start command is left to the guide | FIXED | `--check` and `--start` blocks under the unreachable screen |
| J49 | If status says something else: the healthy screen was never seen | FIXED | Step 1 command 2 shows it |
| J50 | Notes for reviewers: the guide's closing lines are assumed | CHANGED OTHERWISE | Quoted from the helper's DRAFT4 hand-over; the one remaining divergence (MASH_TLS_CA vs MASH_CA, and the mashctl lane's `--ca` adopt) is listed in the notes |
| J51 | (1) Explaining it back in five sentences: understood | NOT TAKEN | No finding; the reader's summary matches the page's intent |
| H1 | (2) Hindrance 1: prerequisite 4, the whole hand-over to the guide (relative link, before/during/instead, adopt and init not shown, no output, `--host myserver`, no ready check) | FIXED | See D9, D10, J17 to J24 |
| H2 | (2) Hindrance 2: Step 3 and 4 thinner for the agent than the shell (one session, no sending, no reply, no picture of the wake, no check, PATH) | FIXED | See D11, J31 to J38; items 1052-3 |
| H3 | (2) Hindrance 3: no healthy `mash status` anywhere; server restart and stop left to the guide | FIXED | See J45, J48, J49; item 1052-1 |
| H4 | (2) Hindrance 4: what "read" means | FIXED | See J30; item 1052-4 |
| H5 | (2) Hindrance 5: the permanent consequence after the paste; wrong cross-reference; channels unexplained | FIXED | See J39, D12, J7; item 1052-5 |
| T1 | (3a) Two agents mailing each other with proof, using only the page: YES but with guessing | FIXED | Step 3 shows both sessions |
| T2 | (3b) If mail stopped, would status tell what to do: YES for the cases shown; no start command; never saw the healthy screen | FIXED | See J48, J49 |
| T3 | (3c) Remove one agent's access and be sure: YES; wants the warning before | FIXED | See J39, J42 |
| T4 | (4) Short and clear enough: the place to stall is prerequisite 4 | FIXED | See H1 |
| N1 | (5) Counts: 9 prerequisite pastes, 14 steps, uncounted others; 23 pastes, about 29 actions plus the guide | FIXED | Recounted from the page by script: 32 pastes (12 prerequisites, 18 steps, 2 clean-up) and 11 other actions; stated in the opening paragraph |
| N2 | (5) Counts: the 14 for the steps is reasonable and feels light; the 6 for the download is heavy but accepted | CHANGED OTHERWISE | Steps are now 18 pastes because step 3 starts two sessions and step 1 shows the healthy status; the download stays 6 |
| N3 | (5) Counts: the intro is honest only with its "after the prerequisites" tail; the guide's size is not stated | FIXED | The tail is gone; the guide's commands are in prerequisite 4 and the total includes them |

Counts: 63 rows; FIXED 51; CHANGED OTHERWISE 8; NOT TAKEN 4.


## Rulings and the lead's items of this round (contract rev 5, #1052, #1053)

Rows B1 to B6: ruling (b) and contract revision 5 (the fingerprint). Rows P1
to P6: the lead's six README items of #1052. Rows L1 to L7: the lead's binding
decisions of #1053. Earlier rows of this file that mention `--ca-fingerprint`
(sections "Security read of the contract" and the DRAFT3 rows) are history and
stand unedited: B1 supersedes them.

| # | finding | disposition | where in DRAFT5, or why not |
|---|---|---|---|
| B1 | Ruling (b): the fingerprint is typed by the person whenever a CA is written or changed, never a flag; remove `--ca-fingerprint` everywhere | FIXED | R15 (revised), R17, section 4 step B and step C, `help credential install`, JSON `ca_checked`, MCP status detail, sections 7 and 8; the lint (rule 9) fails any `--ca-fingerprint`. Rows S2, S3 and S6 of section 7 are marked SUPERSEDED or REVISED; earlier rows of this file stay as history |
| B2 | Contract rev 5: mash never prints the fingerprint it asks the person to type; it says only `fingerprint matches` or `error: the fingerprint does not match the CA in <file>` | FIXED | `install-main`, `install-refuse`, `install-trust`, `install-detail`; `status --detail` and `--json` print the CA path, not a fingerprint (R15); mashctl still prints it for the operator (`server`) |
| B3 | Rev 5: the typed prompt shows the server address, then `Type the CA fingerprint the operator gave you:` | FIXED | First lines of `install-main`, `install-detail`, the refusal and the trust blocks |
| B4 | Rev 5: a trust change shows old and new SERVER ADDRESS (not fingerprints), needs `--replace` and the typed fingerprint of the new CA; no terminal exits 4; no `--yes`, no environment variable | FIXED | `install-trust` (three blocks), `install-refuse` (no terminal), R17 |
| B5 | Rev 5: `credential install` checks that the server at the address presents a certificate from that CA with the matching host name before writing (refusal block) | FIXED | `install-refuse` (otherhost block); `install-main` shows the check line; order of checks in section 4 |
| B6 | Update the exit table (4 = not authorized) | FIXED | Section 2 table row 4; section 8 list |
| P1 | #1052 item 1: healthy `mash status` first, the choose-refusal later, once | FIXED | Step 1 commands 2 and 4 |
| P2 | #1052 item 2: prerequisite 4 (one sentence, the helper's lines, adopt, init, one ready check, full link) | FIXED | See D9 |
| P3 | #1052 item 3: start both sessions; Bob's agent sends, Alice's receives and replies; show the wake-up; PATH in the new terminal; two terminals each with `export MASH_ENDPOINT=...` | FIXED | Step 3 commands 3 to 5 and the text after them |
| P4 | #1052 item 4: say what "read" means; show `mash read` for a person | FIXED | Word list; Step 2 command 4 |
| P5 | #1052 item 5: the irreversible warning before the delete paste; number the "you now hold" items and cross-reference by number; explain channels where first needed with a check from docs/install.md | FIXED | Step 5 warning; every prerequisite ends in a numbered list and the page refers to "prerequisite 3, command 5" and "prerequisite 4" by number; prerequisite 1 |
| P6 | #1052 item 6: the honest total near the top, recounted | FIXED | Opening paragraph, computed by the generator from the page's own typed blocks |
| L1 | #1053 D1: the server row says `alive, encrypted (checked 09:42)` or `NOT REACHABLE (last reached 3h ago)`; never alive with last seen; same shape in every state; MCP fields; `LAST SEEN` only in who | FIXED | See D1 |
| L2 | #1053 D2: an untrusted certificate is mash refusing the server, said in the error and fix | FIXED | See D2 |
| L3 | #1053 D4: configuration fix prints the file path and the line, labelled, never at command position; env test labelled `for this shell only:` | FIXED | See D4; R10 |
| L4 | #1053 D7: no NEXT after delete, or only the proof command; stated as a rule | FIXED | R19; `ctl-delete`; Step 5 |
| L5 | #1053 D11: say how a Claude Code session finds mash, from docs/install.md and the launcher, and show it | FIXED | See D11 |
| L6 | #1053 D10: no hard-coded `--host myserver` | CHANGED OTHERWISE | See D10 |
| L7 | #1053 keep all earlier decisions (MASH_ENDPOINT only, one dotted address, NEXT in capitals after a blank line, errors start `error:`, tool-printed lines at most 80, mkpacket markers and keys, the quick start generated from the targets and linted, notes for reviewers last, install from release downloads) | FIXED | Kept; the only new packet key is `install-make-second`, plus a second block of the existing key `hook`; the URL line of `ctl-init-local` (84 columns) and the typed fingerprint answers (118) are added to the exemptions of R1 |

Counts: 19 rows; FIXED 18; CHANGED OTHERWISE 1; NOT TAKEN 0.


## Check of DRAFT5 and the REQ-011 direction

Sources: the planner's check of packet DRAFT5 and README DRAFT4 (`check5.md`,
findings N1 to N7 and "for the lead"), the junior developer's read of README
DRAFT4 (`review1-d4.md`, verdict NO for a fresh Debian machine), and contract
revision 5, REQ-011 (minimal steps to the first mail). Ids carry the prefix
`CK-` because N1 to N7 already name rows of other sections. The README rows
say what the TARGETS do (the targets own the wording the README quotes); the
README itself is not drafted until the lead says (this round has no README).
Not applied yet, by the lead's order (after round 3): the exit code of an
unreachable server (1 to 6) and the display of the fingerprint (8 groups of
8).

| # | finding | disposition | where in the targets (round 4), or why not |
|---|---|---|---|
| CK-N1 | adopt writes config.json and ca.pem, then the first `--make-key` says `wrote` and `copied` for the same files | FIXED | R20: a command whose job is to put files in place reports each file as `wrote` or `kept (unchanged)`, never `copied` for an unchanged file. The shape is `kept   <path>  (unchanged)` (9 lines: 2 each in `install-creds`, `install-stdin`, `install-second` and `install-make-second`, 1 in `install-trust`). `install-make` now reads `kept (unchanged)` for `config.json` and `ca.pem` (mashctl's configuration after adopt IS mash's folder); with another `MASH_CONFIG_DIR` they read `wrote` and `copied`, said under the block. Audit of every success block for a `kept` or config or server line outside a file-placing command: no `kept` line sat in another command, so none was removed. What remains by earlier decision and is flagged to the lead (N19): the rows of `mash status` (a report) and the `mash serve` start-up line, which names the server (Q12) |
| CK-N2 | README: 11 other actions is one short (the "who wrote to you" message to an agent): 12 and 44 | README | Not in the targets: no count lives in this file. Section 5a states its own counts (install 3 pastes; plugin 1 paste) and the README count is regenerated by script when the README is drafted |
| CK-N3 | README: "every status screen has a fix" is false for the healthy screen | FIXED | The targets never say it (checked: no sentence says every `status` screen has a `fix:`); the packet says every screen has the same verdict line and the same rows, which is true. The README wording is the README's: the healthy screen has no `fix:` and the rows are four on every screen |
| CK-N4 | Packet: "the server row has one shape in every state" lists three forms; four other states print others | FIXED | R11 now lists ALL forms of the state line (`alive, encrypted`, `alive, certificate NOT TRUSTED`, `alive, NOT ENCRYPTED`, `NOT ENCRYPTED (refused; not connected)`, `NOT REACHABLE`, `not checked`) and both third lines (`allowed by MASH_INSECURE_PLAINTEXT` and `MASH_INSECURE_PLAINTEXT is set; nats:// would be allowed`); the status prose in section 5 matches; the packet intro of section 4 (mkpacket) no longer says "one shape" |
| CK-N5 | README: the other two deletes do not print "the same lines" (Bob's block says 1 message; Alice holds 4) | FIXED | Under `ctl-delete`: the count in `its inbox, with N messages` is that endpoint's own (`with 12 messages`, `with 1 message`, `its inbox, empty`); two deletes never print the same lines and no page may promise they do. The README's "the same lines with its own name" is the README's to fix |
| CK-N6 | Packet: the plaintext-allowed fix says "add" a `server` line the config already has | FIXED | `status-plain-warning`: `In ~/.config/mash/config.json, change the server line to:` and the line indented four spaces. R10 gets the form: when the file already has the key the label is `change the <key> line to:` |
| CK-N7 | README times run backwards (`hook.2` 09:44, then `status-deleted` 09:42) | README | The targets' blocks use one clock per block, so none runs backwards within a block; the README orders its own blocks. Not changed here |
| CK-K | For the lead: do `kept ... unchanged` lines break the decision that a file only read is not named in success output? | DECIDED, the lead: kept stands, but only where the command's job is to put files in place | R20 (rules): `wrote` or `kept (unchanged)` for each file such a command is responsible for; no other command names config or the server in success output. The five blocks in question are `credential install` and `endpoint create --make-key`, whose job it is. Two other places still name them (status rows, serve line): N19 |
| CK-N6b | The README's adopt uses `MASH_TLS_CA` while `mash help --detail` names `MASH_CA`; one name | FIXED | `MASH_CA` in `help-settings` and in the settings table (section 3); `MASH_TLS_CA` appears only in "renamed" lines (section 3 table, N8, section 8) and in a Before block that quotes shipped text. The README's adopt step is replaced by `mashctl adopt --from <dir>` (CK-R2) |
| CK-F | The adopt fingerprint prompt: the page never gives the fingerprint to type (check, notes) | FIXED | Section 4, under the hand-overs: the prompt is FED BY THE PAGE. The helper prints the fingerprint, the page shows it where the helper printed it, and the person types it when `mashctl adopt` asks. No flag (N18) |
| CK-R1 | REQ-011 (i): one command installs a matching mash and mashctl; an attested script beside the binaries; the page downloads, checks and runs it; no pipe-to-shell; verifies each binary; `go install` stays | FIXED | Section 5a "Install: the attested script": `install-mash.sh`, 3 pastes (download; check, with `gh attestation verify` or the digest alone, which needs neither `gh` nor Go; run), the run block, `installed` lines, the PATH line labelled by R10, a checksum mismatch (`error:`, nothing installed), a missing `curl` or `sha256sum`, an unsupported platform, a second run (`kept (unchanged)`), help (with the `go install` line), a usage error; macOS and Linux in one script, the platform printed. N14, N15 |
| CK-R2 | REQ-011 (ii): the helper prints one command, `mashctl adopt --from <dir>`, which does what adopt and init do today and prints what it copied | CHANGED OTHERWISE | It is the mashctl lane's output: this file holds a placeholder block `adopt-from` marked "ctl's target: take it from docs/plans/2026-11-19-cli-targets-mashctl.md when it lands" and the dependency (N17). The fingerprint is fed by the page (CK-F) |
| CK-R3 | REQ-011 (iii): `mash plugin install` exports, adds and installs in one command, printing what it ran; the fewest `claude` commands, stated | FIXED | Section 5a "The plugin": exactly ONE `claude` command (`claude plugin install mash --marketplace <dir>`; Claude Code adds the marketplace first), shown with its result; `claude` not on the PATH (error, nothing written), already installed (`kept (unchanged)`), claude refused, usage, help. 1 paste. `mash plugin export DIR` stays as the lower-level command (one line). Help lists `plugin install`. Not run against Claude Code: N16 |
| CK-R4 | REQ-011 (iv): a quick start has no deliberate-refusal paste; refusals on the second page | README | The targets keep every refusal block (they are the second page's material); the quick start's choice is the README's, which is not drafted yet. Nothing in the targets forces a refusal into the happy path |
| CK-R5 | REQ-011 (v): `mashctl endpoint delete` retires the endpoint's credentials on this machine, or prints the one line that does | ctl | mashctl's output. The delete text in section 4 is the CLI team's (it ends with no NEXT, R19) and gets its retire line from the mashctl targets when they land: N17 |
| CK-J1 | Junior, NO on a fresh Debian machine: prerequisite 1 gives no way to get Claude Code, `gh`, `curl`, `unzip`, `openssl`, `ss`, and checks one | PARTLY | The targets remove what the install needs: the script needs only `curl` and `sha256sum` and says so, with the Debian install command alone on its line when one is missing; no `gh`, no Go, no `unzip`, no `openssl` for installing mash. Installing Claude Code and signing in is not a mash output: the README's |
| CK-J2 | Junior: step 3, the wake-up has no output to compare; the plugin takes three operations | PARTLY | `mash plugin install` is one command with its result (section 5a). The hook text is unchanged (section 5, hook) and what Claude Code asks is not a mash output: the README's |
| CK-J3 | Junior: prerequisite 4 adopt is seven flags and a variable, none explained, unsettled | CHANGED OTHERWISE | See CK-R2 and CK-F: one command with a directory, a fingerprint typed from the page; ctl's output |
| CK-J4 | Junior: step 5, two deletes pasted as one (each waits for typed input); close a session; which terminal | CHANGED OTHERWISE | The delete text asks for the typed address (section 4, unchanged); the README must give one delete per paste (and the count changes). `mashctl endpoint delete` retiring the credentials (CK-R5) removes the hand cleanup |
| CK-J5 | Junior: remove everything is not on the page (`--remove`, `~/.config/mash/keys`) | README | Not a mash output; the helper's and mashctl's. Recorded for the README and for ctl (CK-R5) |
| CK-J6 | Junior: six pastes to install two programs; an installer's job | FIXED | CK-R1: 3 pastes, and one of them is the check |
| CK-J7 | Junior: step 1 command 4, a paste whose purpose is to show a refusal | README | REQ-011 (iv): the choose-refusal block stays here (`status-choose`) and moves to the second page |
| CK-J8 | Junior: reviewer notes and `DRAFT5` inside a file named DRAFT4 | README | Not in the targets; the README generator's |

Counts: 23 rows; FIXED 10; CHANGED OTHERWISE 3; PARTLY (the part that is not a mash output goes to the README) 2; README (not a targets matter) 6; ctl 1; DECIDED by the lead 1.


## DRAFT6 rulings

Sources: the lead's brief for DRAFT6 (ten numbered items, cumulative and
binding) and contract revision 11 (`mash-0.5.6-cli-ux`, rev 11 draft, the
newest in `masterplanner/for-herds/mash-0.5.6/` at the start and at the end of
this work; it dropped `trust replace`, so `mash trust replace` and `mash trust
repair`, which an earlier pass of DRAFT6 had written against revision 10, are
removed). Ids are `D6-1` to `D6-10`, the brief's item numbers. X1 to X16 are
the decisions in section 7 of the targets file. Blocks named are packet keys.

| # | item | disposition | where in the targets (DRAFT6), or why not |
|---|---|---|---|
| D6-1 | First install (REQ-013, rev 11): the person TYPES the server address and the CA fingerprint; the server's certificate is checked against the CA and host name first; the token against the account key the CONNECTED server serves; config, CA, `trust.json` and credentials written as ONE GENERATION with one atomic pointer switch and fsyncs (each `wrote` line, `switched to the new generation` as one line; config keys written are `server`, `ca`); later installs of another endpoint `kept (unchanged)`; later installs verify against the record and refuse without touching a file (token from another account than the connected server's or the record's, another server or CA, trust files that disagree: fix `mash trust reset`, then install again, labelled); typed prompts, `matches` / `does not match`; `--replace` removed; an existing credentials file is never overwritten (refuses, names the file to move aside) | FIXED, one line not conformed | Step C, R15, R17, R21: `install-main` and `install-noca` and `install-detail` (the `switched` line), `install-creds`, `install-stdin`, `install-second` (`kept`), and in `install-refuse` the refusals (token not signed by the connected server's account; token from a different account than recorded; a CA that differs; the server presenting another CA's certificate; trust files that disagree; the removed `--server`; no terminal exit 4; an existing different credentials file; a symbolic link). X1: not conformed to REQ-003's `--server` synopsis and precedence sentence (REQ-013 and INV-004 say the address is typed). X2 (chosen, not in the contract): the pointer is the config folder, a link to `~/.config/mash.d/<N>`; a later install adds its credentials file to the selected generation without a switch |
| D6-2 | `mash trust reset` (INV-004: terminal only, the person types the endpoint's address or `reset`, no `--yes`, no variable, no terminal exit 4): removes this machine's endpoint config, trust record and credentials, saying exactly what (paths, what each holds, never contents), what it keeps (the person's key files, everything outside the mash folder), refusal screens, `nothing to reset`, `--detail`, help text and top-level help; success ends with NEXT (`mash credential install --key <key file> --token <token file>`); `trust replace` and `trust repair` DROPPED everywhere (doc, help, packet, MCP, status fixes); every use checks that the trust files agree (refusal under status and under send); `status` `trust` row none / matches config / differs | FIXED | Section 5 "mash trust reset": `trust-reset` (two blocks: one endpoint, several), `trust-reset-detail`, `trust-reset-nothing`, `trust-reset-refuse` (four: no terminal exit 4, `--yes` unknown exit 2, a wrong typed answer, a file that cannot be removed exit 1); `help` (top-level Set up line, `mash help trust reset`, and the `credential install` help); `trust-disagree` (two blocks, under `status` and under `send`) and the `trust` blocks; `status-detail-differs`; the `status-detail-unfinished`, `trust-replace*` and `trust-repair*` blocks are removed; MCP `trust.state` is `matches`, `differs` or `none` and `trust_unfinished` is gone. X16 (chosen): with several endpoints the person types `reset`; `nothing to reset` is exit 0; a failed removal is exit 1 and the same command removes the rest; it works when the files disagree; the NEXT is an exception to R19 (nothing on the server changes) |
| D6-3 | Install script (REQ-011 (i)): verified BEFORE it runs with `gh attestation verify` naming bogworks/mash, release.yml, the tag (`--source-ref refs/tags/v0.5.6`) and the file; or a digest from the release's attested notes with `sha256sum -c` (the page shows the digest line); no sudo, no pipe-to-shell; the script prints every destination and rehashes each binary (`checked  mash  sha256 ok`) | FIXED, lines not conformed | Section 5a: `install-script-download`, `install-script-check` (two blocks: the `gh` paste; the digest line and `sha256sum -c`, the digest an example of the shape), `install-script-run`, `-path`, `-twice`, `-mismatch` (three), `-missing` (two: no `gh` any more), `-platform`, `-help`, `-usage`. Not conformed (X8): REQ-011 (i) in revisions 10 and 11 says a digest in the notes is NOT an alternative and that the script verifies both binaries the same way; the brief allows the digest, so the script holds the digest of each program (written in at release time, covered by the script's attestation), compares each download before it installs either, and rehashes each installed file. UNVERIFIED: how the notes' digest is attested; whether `gh attestation verify` needs a login |
| D6-4 | `mash plugin install`: the plugin embedded in the verified binary (same version, no network), extracted to the user's data dir, Claude Code pointed ONLY at that directory, pinned to the binary's digest (`pinned  plugin  sha256 ok`), user scope, the claude command shown (from `claude plugin install --help` and `claude plugin marketplace --help`, `--help` only); claude's own outputs UNVERIFIED in a boxed note in the doc and in the packet blocks | FIXED (kept from the earlier pass, re-checked against `--help`) | Section 5a "The plugin": one command, `claude plugin install mash --marketplace <dir> --scope user` (`-s, --scope`, default user; `--marketplace` adds the marketplace first); `plugin-install-unverified` (the boxed note, prepended to each of the four claude blocks in the packet), `plugin-install`, `-kept`, `-noclaude`, `-failed`, `-pin` (two), `-usage` (two), `-help`. N16 keeps the unverified items (X9) |
| D6-5 | `mash status --detail` (REQ-012): server by default; detail shows the config file, the CA as its own row (`ca  <path>`, or `ca  this computer's trusted authorities`), the credentials file, trust, the selected endpoint and HOW it was chosen; the SAME rows in every detail state with `none`, `missing`, `not set`; compact detail blocks (no-config, several-none-chosen, unreachable, refused, deleted); MCP detail JSON | FIXED | R11, `status-detail` and `status-detail-noconfig`, `-choose`, `-unreachable`, `-refused`, `-deleted`, `-differs`; section 6: `detail` has `config_path`, `ca_path` (null with no CA), `credentials_path`, `trust_path`, `endpoint`, `chosen_by` (`env`, `config`, `only_one`), `trust {account, server, state: matches, differs or none}`, `server.address`, and a third JSON example. `trust_path` and the `trust file` row go beyond the brief's list |
| D6-6 | `mash serve` start line `mash: serving as acme.demo.bob` (no server) | FIXED (kept) | The `serve` blocks, the decision paragraph, R20, N19, X12 |
| D6-7 | Exit codes: 6 = server unreachable or failed in BOTH tools; a local file error is 1 with its cause; 4 not authorized; 2 usage; update the table and every unreachable block/row | FIXED (kept), one choice flagged | Section 2 table; `status-unreachable` and `status-detail-unreachable` exit 6; `send-refuse` exit 6; file errors exit 1 in `key-refuse`, `install-refuse`, `trust-reset-refuse`; section 8. Kept at 1 and flagged (X10): the server refusing credentials, a certificate mash does not trust, trust files that disagree, a `prompt` nobody answered |
| D6-8 | Fingerprint DISPLAY: eight groups of eight lower-case hex digits joined by `-` where the operator, helper or mashctl shows it; mash never prints what it asks for; typed answers accepted with or without `sha256:`, colons, spaces, dashes, any case; typed answers exempt from 80 columns | FIXED (kept) | R15, R1, the `server` block; section 9 measures the exemption |
| D6-9 | Config names (agreed with ctl, unratified): config.json keys `server`, `ca`, `endpoint`; env `MASH_SERVER`, `MASH_CA`, `MASH_ENDPOINT`, `MASH_CONFIG_DIR`, `MASH_INSECURE_PLAINTEXT` (env only); `account` only in `trust.json`; mash ignores mashctl's own keys; no `owner` written by mash; settings section and help updated | FIXED | Section 3 (the table and the paragraph "What mash writes, reads and ignores"), `help-settings` (a line on the keys), X15; the written keys on the wrote-lines are `(server, CA)` and `(server)` |
| D6-10 | Keep earlier decisions (R20 wrote/kept, `error:` prefix, NEXT rules, no NEXT after delete, UNVERIFIED notes); re-measure widths; update section 9, the packet keys and the checks run | FIXED | R20 (`removed` and `kept` for reset), R19 (one stated exception for reset); section 9: 171 fenced blocks, 151 chosen by markers (84 keys), no printed text over 80 (the widest 80), exempt commands up to 92 (a pasted digest check); the packet is regenerated by `mkpacket.py` |

Counts: 10 rows; FIXED 10 (D6-1 and D6-3 name contract lines not conformed to:
X1, X8; D6-7 flags X10). Contract rev 11 lines not conformed to, in one list:
REQ-003's `--server` synopsis and precedence sentence (X1); REQ-011 (i)'s
"a digest in the notes is NOT an alternative" and "verifies both binaries the
same way" (X8, on the brief).


## Check of DRAFT6: freeze fixes

Source: the planner's check of DRAFT6 (N6, T1 to T14) and the freeze from the
lead and planner: no new command, file format or protocol until the 0.5.6
scope is ruled. Only the three text items that are wrong under the doc's own
rules are fixed; every item that adds a mechanism is HELD.

| # | finding | disposition | where in the targets |
|---|---|---|---|
| CK6-N6 | The plaintext-allowed fix says to change the `server` line of config.json, which already holds `tls://myserver:4222` (config.json must agree with trust.json; install refuses `nats://`) | FIXED | `status-plain-warning`: the warning now says `MASH_SERVER is overriding the server in config.json for this run.` and the fix is the `unset` alone; the prose says why config.json is never edited to a `nats://` address. `status-plain-refused` and the plain-URL `send-refuse` say the same; the "if the address came from the file" sentences are removed (it cannot) |
| CK6-T7 | The environment bypasses the trust record in silence ("the environment wins") | FIXED, one rule | The environment wins for one run and mash says so on every status: a `MASH_SERVER` or `MASH_CA` that differs from config.json and the trust record is reported on the server row (`MASH_SERVER overrides config.json: tls://other:4222` / `instead of tls://myserver:4222`) and the verdict is `mail does NOT work` (exit 1), except the test-server override: with `MASH_INSECURE_PLAINTEXT` set an overriding `nats://` address is allowed, still reported, verdict `mail works (1 warning)`. Applied to: R11 (row forms), section 3 prose, the help text (`Settings (the environment wins for one run, and mash says so on every status)`), `status-plain-warning`, `status-plain-refused`, `send-refuse`, and one new report block `status-env-override` (a tls address, not ready); no new command or file |
| CK6-T6 | Exit 4 means "no terminal" against the rule 4 = the server refused the credentials; the credentials-refused screens print no exit code | FIXED (lead's rev 12 ruling) | One meaning: exit 4 = the server refused the credentials, in both tools. The "no terminal" refusal of the first install and `mash trust reset` is exit 2 (usage: it needs a terminal): section 2 table (rows 1, 2 and 4), R17, the install and reset sections, X10, S12, X16, section 8 and the packet intro. `status-refused`, `status-detail-refused`, `status-deleted` and `status-detail-deleted` say `Exit 4` in their lead-in lines. The mashctl delete refusal is said to exit 2 here; the mashctl targets must agree (flagged to ctl) |
| CK6-T1 to T5, T8 to T13 | Reset loses a `--make-key` endpoint's only key; reset's NEXT needs a deleted token; install's fix goes to the destructive reset; "server in config.json is right"; disagree fix drops `--ca`; plugin install output; the missing-digest fix; `sha256sum` on macOS; `copied` and the generation line; reset's config.json description | HELD | They change or add mechanism (the generation lock, reset order, `--ca` required, plugin install fixes, trust reset changes): not in the freeze scope. T14 (leftovers) held with them |

Re-measured: 172 fenced blocks, 152 chosen by markers (85 keys); no printed text
over 80 (the widest 80); the exemptions are as in section 9.

## Lead's ruling on exit 4 (undoes CK6-T6's second half)

| # | finding | disposition | where in the targets |
|---|---|---|---|
| RL-4 | The lead's ruling: "no terminal" for a confirmation goes BACK to exit 4. INV-004 sets it on purpose, overriding the CLI standard (contract security rev 2 B1). Exit 4 now means not authorized, for one of two causes, each with its own message text that states the reason: the server refused the credentials, or a person must run this at a terminal | FIXED (replaces the second half of CK6-T6) | Section 2 table (row 2 is usage only; row 4 has the two causes), R17, the install refusal lead-in and the `Refusals` heading, the `credential` summary, the mashctl hand-over sentence, the reset intro and its refusal lead-in, S12, X10, X16, section 8 (two places) and the packet intro (`mkpacket.py`). The credentials-refused `Exit 4` lead-ins and the report-only `status-env-override` block (T7) are unchanged. The mashctl delete refusal exits 4 again, as the mashctl targets say |


## DRAFT7: the scope ruling (decision 264, contract rev 15)

Sources: Randy's scope ruling (decision 264, accepted as recommended; the
freeze of DRAFT6 lifts for this scope only), the lead's brief for DRAFT7 (six
numbered items, each split into rows below), and contract revision 15
(`mash-0.5.6-cli-ux`, rev 15 draft, the newest in
`masterplanner/for-herds/mash-0.5.6/` at the start and at the end of this
work; its REQ-003, REQ-004, REQ-010, REQ-011, REQ-013, REQ-015, INV-004,
AT-007 and AT-008 were read for every line about mash command output). Ids are
`D7-1.n` to `D7-6.n`, the brief's item and its parts. Y1 to Y16 are the
decisions in section 7 of the targets file; blocks named are packet keys.

| # | item | disposition | where in the targets (DRAFT7), or why not |
|---|---|---|---|
| D7-1.1 | CUT the trust record (`trust.json`) from the doc, help, packet, MCP, status rows and rules | FIXED | R21 removed (the number is not reused); the `trust` status row and `trust file` detail row gone; MCP `detail.trust` and `trust_path` gone and the `trust_files_disagree` code gone; blocks `trust-disagree` and `status-detail-differs` removed; the `trust` blocks now describe only the certificate check; sections 7 to 9 rewritten |
| D7-1.2 | CUT generations, the pointer and the `switched` line | FIXED | R12 loses `switched`; the install blocks list `wrote`, `copied` and `kept` lines only; X2, X3 and X4 are marked SUPERSEDED in section 7 |
| D7-1.3 | CUT the lock | FIXED | No block, rule or sentence names a lock in `mash`; nothing was there beyond the generation text of R21, which is removed |
| D7-1.4 | CUT `mash trust reset` and `replace` (section, blocks, help, top-level help line, fixes, status screens) | FIXED | Section 5 "mash trust reset" deleted; packet keys `trust-reset`, `trust-reset-detail`, `trust-reset-nothing`, `trust-reset-refuse` removed; the `mash help trust reset` block and the top-level `trust reset` line removed; every fix that named it (the install refusals, `trust`, `status` fixes) now names the file to move aside (R17) |
| D7-1.5 | CUT `mash plugin install` | FIXED | Section 5a "The plugin: one command" and its ten blocks deleted (eight keys, `plugin-install*` and the boxed UNVERIFIED note); the top-level help line removed; R20, R12 and the instructions text no longer name it. The plugin is installed with claude's own commands (D7-6.2) |
| D7-1.6 | CUT the install script (`install-mash.sh`, its helper and skill install) | FIXED | Section 5a "Install: the attested script" and its blocks deleted (the ten keys `install-script-*`); R10 and R20 no longer name it; replaced by D7-5 |
| D7-1.7 | Remove from section 7, 8, 9 counts and the exit-code text that existed only for them; keep exit 4 as the server refusing the credentials and "a person must run this at a terminal" only for what stays in contract scope | FIXED, one line to confirm | Section 2 table row 4 keeps both causes, with (b) now naming mashctl's confirmations only (INV-004 is still in rev 15, so the sentence stays in the output contract); the "no terminal" refusal of `mash credential install` is gone (install asks nothing, Y7); section 8's exit list and section 9's counts (148 blocks, 127 by marker, 66 keys) rewritten |
| D7-2.1 | `mash credential install --key FILE --token FILE --ca FILE`: `--ca` REQUIRED (exit 2 without it: `error: a first install needs --ca <file>`, before anything is read) | FIXED, one line not conformed | `install-refuse` (no `--ca`), R15, `help` for `credential install`. Y2: required on a FIRST install only; rev 15 says "`--ca` is required" without qualification (a second endpoint and `--make-key` would otherwise repeat it) |
| D7-2.2 | Also keep `--creds FILE`? Conform to rev 15 | KEPT (Y3) | Rev 15 names `--creds` nowhere; the ruling does not remove it; `install-creds`, `help`. Easy to drop |
| D7-2.3 | The first install SHOWS the server address and the CA fingerprint (8 groups of 8 lower-case hex digits joined by `-`) and says in ONE line to compare them with what the operator sent; NOTHING IS TYPED BACK | FIXED | `install-main` (and `install-detail`): the two values, the one line `Compare both with what whoever runs your mash server sent you.`; R15 rewritten; the typed-prompt blocks and the "wrong fingerprint" refusal removed |
| D7-2.4 | Where the server address comes from | DECIDED here (Y1), a contract line not conformed | Rev 15's REQ-003 synopsis lists no `--server` while the first-install paragraph and AT-007 ("a substituted server address under the same CA") need an input for the address and the token names none; the flag `--server URL` is restored on the first install: `install-main`, `install-refuse` (no `--server`, a plain `nats://` address), step B's NEXT, the help. The README quick start and Day 2 pages must add it to their `credential install` lines |
| D7-2.5 | It checks the server certificate against the CA and the host name before writing | FIXED | `install-main` (`checked  the server at myserver:4222 presents a certificate from this CA`), `install-refuse` (a server that does not present the CA's certificate for the name); a later install checks again (Y5) |
| D7-2.6 | On any difference from the existing server, CA or credentials it refuses untouched and says which file to move aside (0.5.5's rule) | FIXED, one point UNVERIFIED | R17 rewritten; `install-refuse`: a different server (move `config.json`), a different CA (move `ca.pem`), a different credentials file (existing), a symbolic link (existing). Y6: that moving only that one file lets the same command succeed is UNVERIFIED |
| D7-2.7 | `mash` never prints a fingerprint it asks the person to type: that rule goes away; the display rule stays | FIXED | R15 says the rule is gone because nothing is typed and the display form stays; `status` still prints the CA's path, never its fingerprint |
| D7-2.8 | Update every install block, help, refusals, fixes, hand-over text, quick-start-facing text, rules R15 to R18, settings | FIXED | Step B's `server` block and its NEXT; step C; `install-creds`, `install-stdin`, `install-second`, `install-detail`, `install-json` (new `ca_fingerprint`, no `trust_path`); the `trust` blocks and the status fixes (`--creds <installed file> --ca <ca file>`, `--server <address> --ca <ca file>`); `help`, `errors` (the removed-verb fix); the mashctl hand-overs (no `trust.json` line; adopt makes no record); R15 to R18, R20; section 3 |
| D7-3.1 | RENAME `MASH_INSECURE_PLAINTEXT` to `MASH_DANGEROUSLY_SKIP_NATS_ENCRYPTION` everywhere (variable only; no alias; the refusal names the new variable; the `insecure_plaintext` key stays removed) | FIXED | R18, R11, section 3 table and prose, `status-plain-warning`, `status-plain-inherited`, `status-plain-refused`, `send-warning`, `send-refuse` (the config key and the plain URL), `serve`, `help-settings`, the MCP field table and instructions, sections 7 to 9; no other occurrence of the old name than the statements that it is gone |
| D7-3.2 | Widths after the longer name | FIXED | The warning is `warning: MASH_DANGEROUSLY_SKIP_NATS_ENCRYPTION is set; mail may be unencrypted.` (79 columns); the status rows `allowed by ...` and `... is set (not used)` are 65 and 70; the refusal wraps in three lines; the help settings table puts the name on its own line |
| D7-4.1 | NEW REQ-015: config key `mailbox_limit`, default 15, 0 = off; env `MASH_MAILBOX_LIMIT`? decide and say | FIXED (Y10) | Section 3 table and prose, R22, `help-settings`; environment variable added beside the key, following `idle_wake` / `MASH_IDLE_WAKE` (`config.go:96`, `:414`); range 0 to 1000 (above 1000 serve cannot count) and a refusal block `mailbox-limit-refuse` |
| D7-4.2 | The wake: over the limit, `mash serve` wakes the session whatever the kind, with `mash: <n> messages waiting (limit 15), oldest <age>`; again at most every 10 min while over | FIXED | Section 5 serve, "The backlog wake" and packet key `serve-backlog` (two blocks); R22; Y14 (age form, meta key) |
| D7-4.3 | `mash status` MAILBOX ROW on EVERY screen (`<n> waiting`, `OVER THE LIMIT OF 15` as a warning, `not checked` where it cannot be known); order decided; marked subject to the status-views page | FIXED (Y9) | R11 (five fixed rows, mailbox last), every `status-*` block, new blocks `status-mailbox-over`, `status-mailbox-off`, `status-detail-mailbox-over`; the section's lead-in says "SUBJECT TO THE STATUS-VIEWS PAGE". The contract's form `<n> waiting, OVER THE LIMIT OF <limit>` is used |
| D7-4.4 | The row in `--detail`, MCP status JSON `mailbox {waiting, limit, oldest_age, over_limit}`, the serve start/wake lines, the hook text if relevant | FIXED (Y9, Y11, Y12) | `--detail`: a `mailbox limit` row among six fixed rows and `oldest waiting` under the mailbox row; section 6: the four fields, a fourth JSON example and the `mailbox_limit_from` detail field; serve: the wake block; hook: unchanged, said in a paragraph (O5) |
| D7-4.5 | RESEARCH what count `mash serve` and `mash status` can ACTUALLY get, with `file:line`; does it differ from "not yet shown"; which of the spool and the inbox stream a shell `mash status` can read and with which permission; the exact definition; open questions | FIXED | New section 5b "What REQ-015 can count": it DIFFERS in four ways (shown means acked; a digest line is not a showing; reading is not showing; the 1000 cap and the restart lag); the spool is not the backlog, the consumer's `NumPending + NumAckPending` is, readable with the endpoint's own credentials (no new permission); recommended definition; open questions O1 to O5 |
| D7-5.1 | Install check: ONE pasted offline `gh attestation verify --bundle` (tested: no login; a tampered file exits 1; the online form needs login), no script; download the binary, bundle and `SHA256SUMS`; the verify command and what it prints; `sha256sum -c`; install with plain commands as in 0.5.5's README | FIXED, FILE chosen (Y8) | Section 5a, packet key `install-check` (five blocks): download, verify with `gh`'s real output (re-run here on the v0.5.5 bundle), the real failure output, `sha256sum -c`, install. FILE is `SHA256SUMS` (it covers both binaries, so ONE verify checks mash and mashctl, and `sha256sum -c` then compares the binaries with the attested sums); if the bundle names the binaries the page verifies one the same way |
| D7-5.2 | Replace the install-script blocks | FIXED | The ten `install-script-*` keys are gone from the doc and the packet (D7-1.6) |
| D7-6.1 | Keep everything else of DRAFT6c: R20 wrote/kept, `MASH_ENDPOINT`/`MASH_SERVER`/`MASH_CA` names, exit codes 0/1/2/4/6, the T7 report-only override block with its exception renamed, status fixed rows, DELETED wording | FIXED | R20 (shortened to the commands left); section 3; section 2; `status-env-override` and the exception in R11 and the prose; `status-deleted` says the credentials file is left as it was (the ctl lane's decision) and the delete text has no NEXT |
| D7-6.2 | Keep `mash plugin export DIR` and the 0.5.5 plugin installation via claude commands; UNVERIFIED notes stay | FIXED | Section 5 "mash plugin export DIR" (block `plugin`) unchanged; section 5a "The plugin: claude's own commands" shows `claude plugin marketplace add bogworks/mash` and `claude plugin install mash@mash` as in the 0.5.5 `docs/install.md`; N16 and section 9 keep the UNVERIFIED statement (nothing run against Claude Code) |
| D7-6.3 | Re-measure widths (at most 80 printed; exemptions per R1), update counts, packet keys and the checks run | FIXED | Section 9: 148 fenced blocks, 127 chosen by markers, 66 keys (25 removed, 6 added); no printed text over 80; exemptions: commands (up to 108), one URL (84), one pasted `$` line (84), `gh`'s own report (88 to 138); `mkpacket.py` regenerates the packet and asserts that no dropped word leaks into it |

Counts: 27 rows; FIXED 25 (D7-1.7 and D7-2.1 flag one contract line each to
confirm, D7-2.6 and D7-5.1 one UNVERIFIED point each); KEPT 1 (D7-2.2);
DECIDED here 1 (D7-2.4); CHANGED OTHERWISE 0. Contract rev 15 lines not conformed to, in
one list: REQ-003's synopsis names no `--server` (Y1); REQ-003's unqualified
"`--ca` is required" (Y2, first install only); `--creds` is in no contract line
(Y3, kept). Packet keys removed (25): `install-noca`, the ten
`install-script-*`, the eight `plugin-install*` (the boxed note included), `trust-disagree`,
`status-detail-differs`, `trust-reset`, `trust-reset-detail`, `trust-reset-nothing`,
`trust-reset-refuse`. Added (6): `install-check`, `serve-backlog`,
`status-mailbox-over`, `status-mailbox-off`, `status-detail-mailbox-over`,
`mailbox-limit-refuse`.

## DRAFT8: contract rev 16 rulings

Sources: the lead's brief for DRAFT8 (five numbered items, each split into rows
below) and contract revision 16 (`mash-0.5.6-cli-ux`, rev 16 draft, the newest
in `masterplanner/for-herds/mash-0.5.6/` at the start and at the end of this
work; its REQ-001, REQ-003, REQ-004, REQ-011, REQ-015, INV-003, AT-007 and
AT-008 were read for every line about mash command output). Ids are `D8-1.n`
to `D8-5.n`. Z1 to Z8 are the decisions in section 7 of the targets file;
blocks named are packet keys.

| # | item | disposition | where in the targets (DRAFT8), or why not |
|---|---|---|---|
| D8-1.1 | `--ca` and `--server` BOTH required on a first install; each missing one exits 2, checked before anything is read | FIXED | R15, step C text; `install-refuse` (two blocks); Y2 conformed |
| D8-1.2 | Two refusal blocks `error: a first install needs --ca <file>` and `... --server <address>`, each with a labelled fix and the full command line to run | FIXED | The two `install-refuse` blocks now end in the whole command (with `<ca file>` or `<address>`); both are over 80 columns and exempt (R9, 111 and 98) |
| D8-1.3 | A later install for another endpoint uses the configured server and CA; a flag given must match, else it refuses untouched naming the file to move aside | FIXED | R15, R17, section 3, the second-endpoint text, `help`; the two existing refusals (`newhost`, `newca`) show it |
| D8-1.4 | Update help and every example, block, fix and hand-over text | FIXED | `help` (two usages, text), the status and send fixes keep their later-install form (config exists), the no-configuration and removed-verb fixes carry both flags, the trust fixes |
| D8-2.1 | NEW CHECK (F15-2): the token's signature verifies against the key of the account of the AUTHENTICATED server, before any write; refusal block with the lead's text and a labelled fix | FIXED | the refusal block, SUPERSEDED in DRAFT12 (D12-2): one wording now, `install-refuse-refused`; R15, step C, `trust` text; Y4 reversed |
| D8-2.2 | Success output: no new line; extend the existing `checked` line | FIXED | `checked  the server at myserver:4222: its certificate is from this CA for its name, and it made this token` in every install block (Z1); `--json` gets `token_checked` (Z8) |
| D8-2.3 | WRITE ORDER (F15-6): preflight, then the CA, then the credentials, then `config.json` last as the commit point; success lines in that order | FIXED | File safety (section 2), R20, step C; every install block, and the two `--make-key` blocks quoted from mashctl (Z3) |
| D8-2.4 | A rerun COMPLETES an interrupted install; new block `install-rerun` | FIXED | `install-rerun`: CA copy `kept (unchanged)`, credentials and `config.json` `wrote`, `config.json` last; UNVERIFIED that the screen is the full first-install one (Z2) |
| D8-2.5 | The rule text in the file-safety and output-contract section | FIXED | Section 2, "Write order of `credential install`" |
| D8-3.1 | `--creds FILE` DROPPED from `credential install` everywhere (help, blocks, MCP, mashctl hand-over wording) | FIXED | `install-creds` block and its packet key removed; help, R16, section 4 bullets, the changes paragraph, the hand-overs, sections 7 to 9; Y3 superseded; no MCP field named it. mashctl's `--make-key` and `--creds-out` stay as its own targets |
| D8-3.2 | The trust screens' fix named `--creds` | CHANGED OTHERWISE | Two `mv` lines (`ca.pem`, `config.json`) then a first install with placeholders; UNVERIFIED which files must be moved (Z4) |
| D8-4.1 | REQ-015 count = pending plus ack-pending; O1 digest-listed count; O2 reading does not lower it | FIXED | R22, section 5b ("The definition (DECIDED, the lead, contract rev 16)", differences 2 and 3, O1 and O2 marked DECIDED), MCP `mailbox.waiting`, the serve-backlog text |
| D8-4.2 | Cooldown per endpoint, from the last wake, not surviving a restart; say so in 5b and the serve-backlog text | FIXED | R22, section 5b (wake, difference 4), "The backlog wake (REQ-015)", Z5; packet section 12 |
| D8-4.3 | `mash endpoint status` shows the count only for the caller's own owner, else `unknown` (a `MAILBOX` column) | FIXED, one point UNVERIFIED | Section 5b's table row (the only place the targets name that view), R22, Z6; the permission is UNVERIFIED |
| D8-5.1 | The deliberate refusal leaves the quick start | NOT TAKEN HERE | README only, not edited by this lane |
| D8-5.2 | Fix the `localhost` vs `myserver` mismatch in the blocks where the helper's real address is shown; decide the simplest consistent choice | FIXED (Z7) | One-machine blocks use `tls://localhost:4222` (status, serve, send, the second-endpoint install, the MCP examples, the plain-text override block); `tls://myserver:4222` stays on the other-machine path (steps B and C, their refusals and trust screens, the help example). The cast paragraph says it once; no extra printed rule |
| D8-5.3 | Re-measure widths, update counts, packet keys and checks run | FIXED | Section 9: 144 fenced blocks, 123 chosen by markers, 66 keys (`install-creds` removed, `install-rerun` added); no printed line over 80 except the exemptions |

Counts: 17 rows; FIXED 14 (D8-4.3 with one UNVERIFIED point); CHANGED OTHERWISE
1 (D8-3.2); NOT TAKEN HERE 1 (D8-5.1); FIXED with a decision 1 (D8-5.2).
Contract rev 16 lines not conformed to: none that a target output can show; three
points are UNVERIFIED (REQ-015's owner-wide read with the caller's own
credentials; that a rerun prints the full first-install screen; the file set
named by the trust screens). Packet keys removed (1): `install-creds`. Added
(1): `install-rerun`.

## DRAFT9: contract rev 17 and who signs

Sources: the lead's brief for DRAFT9 (four numbered items) and contract
revision 17 (`mash-0.5.6-cli-ux`, rev 17 draft, the newest in
`masterplanner/for-herds/mash-0.5.6/` at the start of this work; compared line
by line with revision 16). Ids are `D9-1.n` to `D9-4.n`. Z9 to Z13 are the
decisions in section 7 of the targets file; blocks named are packet keys.

| # | item | disposition | where in the targets (DRAFT9), or why not |
|---|---|---|---|
| D9-1.1 | ctl's agreed `--make-key` first lines (`Made a key pair for acme.demo.bob: ...`), wrapped at 80 by hand | FIXED | `install-make`, `install-make-second` (Z9) |
| D9-1.2 | ctl's agreed `--public-key` line (`Your mashctl's signing key signed a token saying this public key is acme.demo.bob.`) | FIXED | `server` (Z9) |
| D9-1.3 | `--make-key` refuses when `config.json` has no `server` and `ca`; writes nothing; new block quoted from ctl, key `ctl-make-key-refuse` | FIXED, one point UNVERIFIED | `ctl-make-key-refuse`; the error line is ctl's, the `fix:` wording is the targets' (Z10) |
| D9-2.1 | Remove "signed by your server" and "it made this token" from the `server` and `install-main` blocks and every other place | FIXED | the `checked` line in `install-main`, `install-rerun`, `install-stdin`, `install-second`, `install-detail`; `server`; R2, R12, R15, step B and C text, the `credential install` change notes, Z1, Z4 |
| D9-2.2 | Also "your server made", "the server made", "the server did not make" | FIXED | `status-noconfig`, `status-detail-noconfig`, `status-nocreds`, `help` (text and `--token`), R2 exception wording; packet prose |
| D9-2.3 | A plain accurate check line | FIXED, chosen here | `its certificate is from this CA for its name, and it accepted this key and token` (Z9) |
| D9-3.1 | An agent never runs `credential install` or the plugin install: the person does | FIXED | `help` (credential install text), MCP initialize instructions (section 6) (Z11) |
| D9-3.2 | README: one plain line in a skill hand-off section, if there is one | NOT TAKEN HERE | the README has no skill hand-off section; nothing invented |
| D9-4.1 | `mailbox_limit` 0 reads `no limit set` | FIXED | `status-mailbox-off`, R11/R22 text, section 5 and 6 mentions (Z12) |
| D9-4.2 | Leave REQ-015's count text; add one line in section 5b: the count is messages not yet delivered; the unacked definition is superseded, see the status-views page | FIXED | section 5b, first lines; Z13 |
| D9-4.3 | Do not rewrite the mailbox blocks; DRAFT10 regenerates them | NOT TAKEN HERE | the status-views agent owns that wording |
| D9-4.4 | Re-measure widths, update counts, packet keys and checks run | FIXED | section 9: 145 fenced blocks, 124 chosen by markers, 67 keys |

Contract rev 17 lines not conformed to: REQ-015's `status says off` for a limit
of 0 (the planner's newer ruling, `no limit set`, is used); REQ-015's unacked
count (superseded by the planner's delivery definition; text left); the
refusal blocks for a connection the server refuses and for an unreachable
server during `credential install` (the contract gives tests, not wording).
Packet keys added (1): `ctl-make-key-refuse`. Removed: none.

## DRAFT10: contract revs 18 to 21

Sources: the lead's brief for DRAFT10 (five numbered items) and contract
revisions 18 to 26 (`mash-0.5.6-cli-ux`). The work began at rev 21 (the newest
in `masterplanner/for-herds/mash-0.5.6/`); the contract was downloaded again
during and at the end, when revs 22 to 26 had appeared, and the differences were applied
(rows D10-6.n). REQ-003, REQ-013, REQ-015, INV-003, INV-004, AT-007 and AT-008
were read for every line about install and the mailbox. Ids are `D10-1.n` to
`D10-6.n`. Z14 to Z21 are the decisions in section 7 of the targets file; blocks
named are packet keys. The status-views page is DRAFT4 and the README pages and
reviewer notes are regenerated.

| # | item | disposition | where in the targets (DRAFT10), or why not |
|---|---|---|---|
| D10-1.1 | First install shows three values BEFORE any write, in ctl's form (`server address`, `CA fingerprint` on two lines of four groups, `account`) | FIXED | R15, step C, `install-confirm`, `install-main`, `install-rerun`, `install-detail`; step B's `server` block gains the `account` line (mashctl's hand-over prints all three, rev 21); the account on the screen is the `--account` value (rev 24) |
| D10-1.2 | The account is the COMPLETE 56-character key on one line, never shortened (rev 21 correction: NOT the first 32 characters) | FIXED | the sample key is 56 characters and the label line is 73 columns; R15; the lint refuses a cut key |
| D10-1.3 | `Do these three match what the operator sent you? (yes/no)`; no or anything but yes: `Nothing was written. Ask the operator who made this endpoint.`, exit 1; no terminal: `error: this needs a person at a terminal ...`, exit 4 | FIXED, wording chosen here for the fix lines | `install-confirm-no`, `install-no-terminal`; R2 gains the exception for `account` and `operator` (Z14) |
| D10-1.4 | Fingerprint display form everywhere = ctl's (groups of 8, space separated, two lines of four): replace the `-` joined form in rules, help, hand-over text, JSON examples, README generator rules and lint | FIXED | R15, `server`, `install-*`, `install-json` (one line, wrapped under its key), the adopt paragraph; `p2/lint-plain.py` (and `p2/neg3.py`, six new cases); packet intro |
| D10-1.5 | Remove typed-answer rules; replace every "compare after the write" wording | FIXED | R15 (nothing typed but yes/no), R17, step B and C text, "How the agent trusts the server", the `credential install` help, S2/S3/S12 and Y7 marked superseded; Day 2 step 5 |
| D10-2.1 | The token's issuer must equal the confirmed account; refusal `error: this token belongs to another account than the one you confirmed.` + labelled fix; nothing written | FIXED, reordered by rev 22 to 24 | `install-refuse-account` (now before the question: the local comparison with `--account` is the first check after the files; see D10-6.1) |
| D10-2.2 | The server must REQUIRE authentication and the connection must succeed; refusal `error: the server at myserver:4222 does not require authentication.` + fix | FIXED | `install-refuse-noauth`; the connection refusal is `install-refuse-refused` (exit 4; DRAFT12, D12-2); an unreachable server (exit 6) has no block: no wording in the contract |
| D10-2.3 | A certificate signed by a DIFFERENT CA than the confirmed one: `error: the server's certificate is not signed by the CA you confirmed.` + fix | FIXED | `install-refuse-ca`; the old combined "CA and name" refusal is now a host-name refusal under `install-refuse` (Z15) |
| D10-2.4 | NO directory lookup; drop any "no matching record" refusal and any directory-check text | FIXED (nothing to drop) | DRAFT9 had neither; R15 and step C say install reads nothing else from the server |
| D10-2.5 | Before connecting refuse a key or token file that is a symlink, foreign-owned or broader than 0600: `error: bob.key is readable by others (mode 644).` + `chmod 600 bob.key` on its own indented line; one block per cause | FIXED, fixes chosen here | `install-refuse-keyfile-mode`, `-symlink`, `-owner` |
| D10-2.6 | Every interrupted state is ignored before the rerun: a rerun starts from the files actually present and never trusts a leftover marker or half-written file | FIXED, UNVERIFIED in code | section 2 write-order rule, step C, `install-rerun` text (the rerun asks again), Z16 |
| D10-2.7 | Keep write order, the "accepted this key and token" wording, exit codes 6, 4, 2, 1 | FIXED | unchanged; the `checked` lines now also say it requires authentication and (first install) a separate line for the account |
| D10-3.1 | REQ-015 count STAYS pending plus ack-pending; delivery acknowledges (full hook context, the `read` tool, `mash read`); a digest line does not acknowledge and still counts; so reading lowers the count | FIXED | section 5b rewritten, R22, the serve-backlog text, O1 and O2, MCP instruction text, hook, `mash read` change text; the DRAFT9 line "superseded by status-views" removed; Z17 |
| D10-3.2 | Status-views page DRAFT4: replace DRAFT3's recommendation (digest counts as delivered) everywhere; list which code changes and what else it touches | FIXED | `docs/plans/2026-11-19-status-views-mash.md`: section 4.4, every mailbox row and explanation, the open question, section 8, section 10 |
| D10-3.3 | The brief's line "`mashctl audit` unread can exceed the status number by the digest-listed count" | NOT TAKEN | under the ruling both count the digest-listed messages, so they agree; 5b ("what else", item 2) and Z17 say so |
| D10-4.1 | A backlog already over the limit at `mash serve` start wakes once, 10 minutes after the start, if still over; same line; cooldown per endpoint from the last wake, not kept across restart | FIXED | R22, 5b "the wake", the serve-backlog text, a note under the serve start block (Z18) |
| D10-4.2 | `mailbox_limit` 0: the count is still shown (rev 21) | FIXED, beyond the brief | `status-mailbox-off` reads `3 waiting, no limit set`; R11, R22, section 3, packet text (Z19) |
| D10-5.1 | Re-measure widths, update section 9 counts, packet keys, checks run | FIXED | section 9: 157 fenced blocks, 136 chosen by markers, 79 keys (twelve added); account line 73 columns; no printed line over 80 except the exemptions (R9: a command on its own line; the longest, a fix with `--account <account key>`, is 121 columns) |
| D10-5.2 | Regenerate the README pages, the step count and the reviewer notes; lint passes | FIXED | quick start and Day 2 regenerated, lint ok on both (37 negative cases pass; the lint's printed-block width check now skips a command-position line, as R9 does, because the hint commands with `--account` are 87 columns); step count unchanged (26 pastes, 60 lines, 10 actions); notes regenerated (stale lines about "signed by your server" and make-key removed; new differences and placeholders added; junior findings and dispositions kept) |
| D10-6.1 | Rev 22 to 24: `--account <key>` on EVERY install, required, a complete valid key, never ignored; a missing one exits 2 before anything is read and nothing is connected; the local issuer comparison comes first and refuses before any write whatever the person answers | FIXED | R15, step C, section 2, the help, every `$` command and every printed `fix:` command carries it; new blocks `install-refuse-noaccount`, `install-refuse-badaccount`; Z21 |
| D10-6.2 | Rev 22 to 24: order = local preflight and issuer comparison, then (first install) the confirmation, then any connection that carries the key or token; a "no" means the server never sees an authentication attempt; a later install does not prompt and still compares first | FIXED | R15, step C text, `install-confirm-no` text, section 2 |
| D10-6.3 | Rev 22 to 24: a later install writes its credentials under `.<address>.creds.tmp` and renames it last; stale temporary files removed; mash ignores `.*.tmp`; the address checked against the grammar | FIXED in the rule, no block | section 2 write-order rule; no printed block shows a temporary file (Z21 (c)) |
| D10-6.4 | Rev 24, REQ-011 (i): the pasted install refuses a destination, or any parent of it, that is a symlink or writable by another user, before copying | NOT TAKEN in DRAFT10; FIXED in DRAFT11 (D11-1) | the shape of the paste was a decision for the lead; listed as not conformed until the lead ruled |
| D10-6.6 | Rev 26: a later install refuses (exit 1, `error: another mash credential install is running`) while another's temporary is younger than 10 minutes; only older or future-dated temporaries are stale; address grammar (1 to 63 a part, at most 191, lowercase); a later install's `--server` and `--ca` byte-identical to the configured ones; a terminal only when it prompts | FIXED | section 2, R15, new block `install-refuse-running` (the lead's words), Z21 (e) |
| D10-6.5 | Newest revision binds over earlier rulings | APPLIED | the brief's "mash computes the account from the token's issuer" yields to rev 24's `--account` (compared with the issuer); the lead's error line `... than the one you confirmed.` is kept word for word (Z21 (a)) |

Where revisions conflicted, the newest won: rev 26 (and 24) over the brief's account
computed from the token (now `--account`); rev 21 over the planner's "not yet
delivered" count (DRAFT9, Z13) and over rev 16's O2 (reading does not lower);
rev 21's count shown with `no limit set` over DRAFT9's row; rev 17's "cooldown
starts at start" over DRAFT8's "after a restart the first wake can come
immediately"; the lead's brief (the whole account) over rev 20 (first 32
characters).

Contract rev 26 lines not conformed to: REQ-011 (i)'s destination and parent
checks for the pasted install (D10-6.4); `mash endpoint status` showing the count
for the caller's own owner (REQ-015) is followed by the targets but not by the
status-views page; refusal wording for a connection the server refuses (written in
DRAFT11, one wording since DRAFT12) and for an unreachable server (the contract names the tests
only); REQ-003's one line in `--make-key` output on why no fingerprint is shown
(mashctl's text, not in `install-make`); AT-007's kill and symlink tests are
behaviour, not output.
Packet keys added (12): `install-confirm`, `install-confirm-no`,
`install-no-terminal`, `install-refuse-account`, `install-refuse-noaccount`,
`install-refuse-badaccount`, `install-refuse-running`, `install-refuse-noauth`,
`install-refuse-ca`, `install-refuse-keyfile-mode`, `install-refuse-keyfile-symlink`,
`install-refuse-keyfile-owner`. Removed: none (the combined certificate refusal
stays under `install-refuse`, reworded to the host name).

## DRAFT11: lead rulings of 11:08Z

Contract rev 26 FINAL (`contract-0.5.6-cli-ux-rev26.md`, the critic's read of
rev 26 clean) was downloaded at the start and the end of this work; it differs
from `rev26-draft.md` only in the critic row, the removal of the `mash endpoint
status` mailbox column and the publication list. Target document:
`docs/plans/2026-11-19-cli-targets-mash.md` (DRAFT11).

| # | item | disposition | where in the targets (DRAFT11), or why not |
|---|---|---|---|
| D11-1 | The pasted install refuses a destination folder, or any parent of it, that is a symlink or writable by another user, before copying; refusal blocks; verify before copy; short enough to paste | FIXED, with one part not taken | section 5a: the paste is one `&&` chain (21 lines in the targets, 31 in the README's whole prerequisite paste, up from 14), the check a subshell loop of `[ -L ]` and `find -maxdepth 0 -perm` (portable, no `stat`), after the verify and right before the copy; blocks `install-check-writable` and `install-check-symlink`; Z22 (a). A second platform is not shown: the README is a Debian or Ubuntu page (the helper runs only there); see D12-1 |
| D11-2 | `mash endpoint status` has NO mailbox column; remove the column and the `unknown` wording from the targets, packet, section 5b | FIXED | R22, section 5b's table, the DRAFT10 summary, the companion's Z6 (superseded) and section 8; the packet never had the column; Z22 (b) |
| D11-3 | The `no` outcome keeps the `error:` label | FIXED | `install-confirm-no` reads `error: you did not confirm the three values.`, then `Nothing was written. Ask the operator who made this endpoint.`, exit 1; R15 and the block's text; Z14, Z22 (c) |
| D11-4 | Write the refusals for a server that REFUSES and one UNREACHABLE | FIXED | `install-refuse-refused` (exit 4) and `install-unreachable` (exit 6, 20s, the open timeout: section 2 Timeouts); the exit-code table's rows 4 and 6 and R15 name them; Z22 (d). The second block DRAFT11 kept for the same exit-4 condition is deleted in DRAFT12 (D12-2) |
| D11-5 | Yes/no read from the terminal, a later install shows no confirmation, `--dry-run` asks nothing | ACCEPTED, nothing to change | unchanged |
| D11-6 | Re-measure widths, update counts, packet keys, checks run | FIXED | section 9 (companion): 162 fenced blocks, 141 chosen by markers, 83 keys |
| D11-7 | Regenerate the README pages, step count and reviewer notes; lint on both | FIXED | see the report of this draft; the status-views page is not edited |

Packet keys added (4): `install-check-writable`, `install-check-symlink`,
`install-refuse-refused`, `install-unreachable`. Removed: none. (`install-check`
has one more block, the `mash version` check on its own, and its install block
is the new chain.)

Contract rev 26 lines not conformed to: REQ-003's one line in `--make-key`
output on why no fingerprint is shown (mashctl's text, not in `install-make`);
AT-007's kill, symlink and foreign-owner tests and AT-008's tests of the
destination check (behaviour, not output: only the refusal texts are shown);
the contract's "a destination that does not exist yet (created by the person's
own user, 0755, then checked)" is applied by `umask 022` and `mkdir -p`, which is
UNVERIFIED against a release (nothing was run against one).

## DRAFT12: lead rulings of 11:17Z

Source: the lead's four rulings of 11:17Z. Target document:
`docs/plans/2026-11-19-cli-targets-mash.md` (DRAFT12). The contract is
`contract-0.5.6-cli-ux-rev26.md`, final; downloaded again at the start and at
the end of this work (the newest in `masterplanner/for-herds/mash-0.5.6/`).

| # | item | disposition | where in the targets (DRAFT12), or why not |
|---|---|---|---|
| D12-1 | No Mac block anywhere in the quick start; it is the Debian or Ubuntu path the helper supports; no Mac or `shasum` text in the page or in the targets' install text; `Linux amd64` stated once; a Day 2 line only if a built command needs one | FIXED | section 5a: the sentences about another platform are gone (the download lead-in states Linux amd64 once; the paragraph after the install block says the README shows this Linux paste alone); the quick start page, Day 2 and the notes carry no such text; no built command needs a Day 2 line, so none was added; Z23 (a) |
| D12-2 | ONE wording for a server that refuses the credentials: keep `install-refuse-refused` (`error: the server at myserver:4222 refused this key and token.`); delete the rev 16 wording everywhere | FIXED | the old `install-refuse` block and its lead-in are deleted from section 5a; the R2 exception for `account` in that refusal is deleted; the Z1, Z15 and Z22 texts that named it are rewritten; D8-2.1 and D10-2.2 above are marked superseded; the packet no longer has the block (the key `install-refuse` keeps its other blocks); the exit-code rows, the JSON error codes (`credentials_refused`) and the README never carried the old words; Z23 (b). SUPERSEDED rows: D8-2.1 (the refusal wording), D10-2.2 (the connection refusal's wording), D11-4 (its last sentence) |
| D12-3 | Contract file is `contract-0.5.6-cli-ux-rev26.md`; fetch the newest final and conform | FIXED | the newest in the folder is still rev 26 (final, 11:08Z); nothing new to conform to |
| D12-4 | Say once on the page why the pasted install refuses a folder others can write to, or that sits under a link | FIXED | section 5a (before the refusals) and the README prerequisite where the refusal block is shown carry the same sentence; the packet's section 0 carries it too; Z23 (d) |
| D12-5 | Re-measure widths, counts, packet keys, checks run; regenerate the README pages, step count and notes; lint | FIXED | section 9 (companion): 161 fenced blocks, 140 chosen by markers, 83 keys; the pages' counts are unchanged (26 pastes, 77 lines, 10 other actions) |

Packet keys added: none. Removed: none (one `install-refuse` block is out, a
block and not a key).

## DRAFT5c: the age trigger (Randy, 11:30Z)

Source: the lead's brief after Randy said yes ("configurable but defaults to
like 1 hour... not a high priority") and the contract, rev 26, REQ-015 (the file
updated about 11:29Z; downloaded at the start and at the end of this work).
Targets: `docs/plans/2026-11-19-cli-targets-mash.md`; status views DRAFT5c.

| # | item | disposition | where |
|---|---|---|---|
| D5c-1 | Config `mailbox_age`, default 1h, 0 = off; env `MASH_MAILBOX_AGE`, the same family as `mailbox_limit` | FIXED | targets section 3 (table, form, range, refusal text), R22, help-settings, Z24 (a) |
| D5c-2 | Wake line over the age limit: `mash: <n> messages waiting (limit 15), oldest 1h 12m, over the age limit of 1h`; both limits: one line | FIXED | `serve-backlog-age` (three blocks), section 5 wake text, Z24 (c) |
| D5c-3 | Mailbox row for the over-age case, with the wake time in the row; `fix:`; the "limit" words for age | FIXED | `status-mailbox-age-over` in the targets and in the status-views page; Z24 (d) |
| D5c-4 | `mailbox_age` 0 in `--detail` and the settings text | FIXED | second line of the `mailbox limit` row in every `--detail` block (`age 1h (the default)`, `no age limit set`), help-settings |
| D5c-5 | MCP `mailbox {waiting, limit, oldest_age, age_limit, over_limit, over_age_limit, woke_at}` | FIXED | targets section 6 (field table, five JSON examples, detail `mailbox_age_from`), status-views JSON block |
| D5c-6 | Status views 4.4: the age predicate, the build plan | FIXED | 4.4 item 5; the table row of section 10 |
| D5c-7 | Does the endpoint's own credentials allow a direct get on its own inbox stream by sequence | READ, NOT RUN | `internal/admin/permissions.go:70-71` grants `DIRECT.GET.<inbox>` and `.>`; "Verify at build" (j) |

Packet keys added: `serve-backlog-age`, `status-mailbox-age-over`. Removed: none.

## DRAFT5d: the planner's last check

Source: the planner's check of status views DRAFT5c (`check-status-views-DRAFT5b.md`) and the lead's rulings; contract rev 26 (final, still the newest). Page: `docs/plans/2026-11-19-status-views-mash.md` (DRAFT5d). Targets: `docs/plans/2026-11-19-cli-targets-mash.md` (Z25 in the decisions file).

| # | item | disposition | where |
|---|---|---|---|
| D5d-1 | Check item 1 / fix 1: `mailbox_limit` 0 with `mailbox_age` on said `no limit set` and "never wakes" | FIXED | page 4.1, `status-mailbox-off` (both off only), new block `status-mailbox-count-off` (`no count limit set; age limit 1h`, mash still wakes), 4.4 items 4 and 5, 7.15, section 8, MCP `limit` 0; targets: row forms, `--detail` forms, `status-mailbox-off` text, MCP field table, settings table; Z25 (a) |
| D5d-2 | Check item 5 / fix 2: age-over fix muddled; config hint not pasteable; wake in a different place | FIXED | `status-mailbox-age-over` (page and targets): `fix: the session is not taking its mail: look at its terminal.`, labelled `next:` with `mash inbox` and `mash read <number>`; config hint REMOVED (noise); the wake is the last words of the first line on the count row and the age row (`OVER BOTH LIMITS` the same); Z25 (b), (c). The targets' count-over block has no wake time in its row (a gap older than this draft; the page's has it) |
| D5d-3 | Check items 2 and 3 / fix 3: `waiting means ... and not read`; D2 row | FIXED | page `status-detail`: `waiting means  not yet acknowledged by your session`, with the no-session case; the D2 row says the count drops when the next session starts when none runs |
| D5d-4 | Check item 4 / fix 4 (ruling): own-endpoint one-address view shows the mailbox row | FIXED | page 4.3 and the D14 row say the contract has it; section 8 consistent |
| D5d-5 | Check item 9 / fix 5 (ruling): `status-plain-warning` prints no server address | CHANGED | page `status-plain-warning`: `plain text is on (MASH_DANGEROUSLY_SKIP_NATS_ENCRYPTION): mail is not encrypted`, `next:` `mash server status`; rule 6 and 7.13 name `status-env-override` as the one screen that names a value. Targets: `status-plain-warning`, `status-plain-inherited` and `status-plain-refused` lost their address lines (refused gained a `next:` `mash server status`). `send-warning`, serve and `send-refuse`: unchanged, they name no host. The README Day 2 page regenerates from the three blocks |
| D5d-6 | Check item 7: D11 row says `heartbeats` | FIXED | the row says `last alive  not recorded` |
| D5d-7 | Check item 8: "every block says so" for `meant to run on` | FIXED | page 4.3 softened to "wherever the label appears, a `note:`" and made true: `endpoint-status-offline` and the `--wide` table gained the note (grep: every block with the label has one) |
| D5d-8 | Check item 6: section 8 lacks the age forms | FIXED | page section 8: the age row forms, `oldest` in R12's `45s`, `12m`, `1h 12m`, `2d`, the `mailbox_age` grammar `30m`, `1h`, `2d`; targets R12 mentions at the wake-line and `read` text now list the same forms (were `45s, 12m, 3h`) |
| D5d-9 | Check item 10: lower-case quote in 4.4 item 5 | FIXED | the row is quoted in capitals; the lower-case text is named as the wake line |
| D5d-10 | Check NOT CLEAN: l.892 "the override set with a tls address" | NOT TAKEN | the sentence names the DANGEROUSLY variable two lines above; one more sentence is not worth it |

Packet keys added: none (`status-mailbox-count-off` is a page block, not a targets block). Removed: none.

## DRAFT12d: conformance pass

Source: the lead's instruction for a conformance pass with no new behaviour. Page: `docs/plans/2026-11-19-status-views-mash.md` (DRAFT5d, one row wording changed). Targets: DRAFT12d.

| # | item | disposition | where |
|---|---|---|---|
| D12d-1 | Over both limits: the row is `22 waiting, OVER BOTH LIMITS (woke the session at 09:40)` and its second line names both, `limit 15, age limit 1h; oldest 1h 12m` | FIXED | page 4.1 (the text) and section 8; targets R11, the age-limit paragraph of "Mail is backing up", section 4 of the packet, decisions Z24 and Z26 (c); the page and the targets have no both-over block (text only) |
| D12d-2 | The count-over row names the wake time: `17 waiting, OVER THE LIMIT OF 15 (woke the session at 09:40)`, in the same place as the age row; "limit" explained once, the number that triggers a wake | FIXED | targets `status-mailbox-over` and `status-detail-mailbox-over` (the page's block, 17 waiting); the old "mailbox_limit" config line is gone with the old fix, as on the page; MCP example 17 waiting |
| D12d-3 | Every `mash status` screen has no server address: the `server` row is a state only (`reachable, encrypted`, `reachable, certificate NOT TRUSTED`, `reachable, NOT ENCRYPTED`, `NOT ENCRYPTED (refused; not connected)`, `NOT REACHABLE`, `not checked` with the page's reasons), no `tls://`, no host, no `checked 09:42`, no detail line | FIXED | 25 keys changed: `status-healthy`, `-claude`, `-mailbox-over`, `-mailbox-age-over`, `-mailbox-off`, `-noconfig`, `-choose`, `-none`, `-nocreds`, `-unsafe`, `-unreachable`, `-refused`, `-deleted`, `-plain-warning`, `-plain-inherited`, `-plain-refused`, `-env-override`, `-detail`, `-detail-mailbox-over`, `-detail-noconfig`, `-detail-choose`, `-detail-unreachable`, `-detail-refused`, `-detail-deleted` and `trust` (its `mash status` block) |
| D12d-4 | The row set is the page's six rows, in its order (`version`, `server`, `config`, `credentials`, `mailbox`, `session`) | FIXED | every block gained `session` (`online`, or `not checked` where nothing was asked); R11 and section 5 say six rows; the layout is the page's (one value column) |
| D12d-5 | `mash status --detail`: no server details | FIXED | the answer time, clock offset and address are gone; every `--detail` block ends its facts with `server details  run: mash server status` (the PAGE's wording of the pointer, kept so the two documents agree, rather than the instruction's `server: see mash server status`); `ca` is `ca file`; `status-detail` is the page's block |
| D12d-6 | MCP `status`: the `server` object is a state only | FIXED | `server` is `{state, encrypted, plaintext_override}`, `state` is `reachable`, `not_reachable` or `not_checked` (was `alive`); `address`, `checked_at`, `last_reached`, `answered_ms` and `detail.server.address` are removed; a `session` field is added; the five examples follow; the section 6 field table loses the `checked 09:42` row |
| D12d-7 | `mash server status` | POINTER | the targets have no such section; a paragraph under "Changes (status)" says it is specified on the status-views page (section 4.2) and keeps the address there; no block is duplicated |
| D12d-8 | Verdict and fix lines that named an address | FIXED | `status-unreachable` fix is the page's `the server did not answer; details, with the address mash tried:` and `mash server status` (the old "check that the server is running" fix is gone from the status screens; `mash send` keeps its own error); `status-plain-refused` says `MASH_SERVER is a plain-text address` and points to `mash server status`; `status-env-override` is the page's block (verdict `not checked`, the page's word; DRAFT12 said `mail does NOT work`) |
| D12d-9 | Rules: R11, R12 (a time on the status screen), R18 (the warning, not the row), R20 (the one place that names the server), N10 (for `mash server status`), S8 (replaced) | FIXED | targets R11 rewritten to the six rows and the forms above; decisions Z26 records the pass |
| D12d-10 | Server-side messages that name the server (install refusals, send errors) | UNCHANGED | not status screens; they keep the address as the instruction says |
| D12d-11 | `status-claude`, `-noconfig`, `-choose`, `-none`, `-nocreds`, `-unsafe`, `-refused`, `-deleted`, `-mailbox-age-over`, `-mailbox-off`: warnings and fixes | UNCHANGED | only the rows changed; the targets' own text stays where the page has no block for the state (and where the page's block differs only in wording: `-noconfig` keeps its one-line command, exempt under R9) |
| D12d-12 | `status-mailbox-over-offline`, `status-offline`, `status-mailbox-count-off` (page blocks the targets do not have) | NOT ADDED | no new behaviour and no new key in a conformance pass; the page keeps them |
| D12d-13 | `docs/plans/2026-11-19-readme-quickstart-targets-mash.md` (the older, hand-pasted quick start page, last regenerated at DRAFT5) | NOT TOUCHED | it is not one of the two generated pages and already differed from the targets; its status blocks are stale |

Packet keys added: none. Removed: none. Changed: the 25 keys in D12d-3, and the `status` tool's five JSON examples.
