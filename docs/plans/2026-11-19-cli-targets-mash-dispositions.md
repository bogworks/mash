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
