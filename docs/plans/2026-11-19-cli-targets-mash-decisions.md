# mash CLI targets: decisions, behaviour changes and checks (sections 7 to 9)

This file is the decisions, behaviour-changes and checks part of
[the mash CLI target outputs](2026-11-19-cli-targets-mash.md) (DRAFT11). It was
split out only to keep the main file under the repository's size limit for
committed files. Sections 7, 8 and 9 below are unchanged and keep their
numbers; every output block (the ones the reviewer packet and the README pages
are generated from) stays in the main file, and section references such as
"section 9" in either file mean the section here.

## 7. Open questions and decisions

Each item is marked DECIDED (with its source) or STILL OPEN. Items of DRAFT6
that existed only for the trust record, the install script, `mash plugin
install` and the typed fingerprint are SUPERSEDED by Randy's scope ruling
(decision 264, contract revision 15) and are kept as one line each, so that an
old reference still finds its answer.

Rulings of the planner, DECIDED (L1 to L12): `NEXT:` capitals everywhere
(L1, R4); a plain-URL refusal exits 1 in both tools (L2, section 2); one
dotted address form, no slash form printed or accepted (L3, R3); one setting
names the endpoint, `MASH_GROUP`, `MASH_NAME` and `MASH_OWNER` are removed
(L4, R13, section 3); `status` has one fixed screen (L5, R11); errors start
`error:`, hints are `fix:` and `NEXT:`, every fix is true (L6, R4); mail
tables fit real names, one `URGENCY` column, kind `message`, `who` with `now`,
`never` and `*` (L7, R14); one output contract (L8, section 2); no version
number but the build's (L9) and the `.creds` file holds the private key, so
`bob.key` is the backup (L9, section 4); printed commands are exempt from 80
columns (L10, R9); the CLI team's line shapes and delete text (L11, section 4;
its `--creds` is dropped, DRAFT8); `init --local`'s removal message (L12, section 4).

13. STILL OPEN. `mash credential install` refusal text for an expired or
    wrong-owner token: the target wording is settled (one sentence naming
    the expiry date or the owner the token is for, then `fix: ask whoever
    runs your mash server for a new token`), but whether the token carries
    an expiry mash can read is unknown; it needs the CLI team's token
    format. Until then the `token expires` line of `--detail` is
    PROVISIONAL.
14. DECIDED (by an earlier draft; no review objected). Copy the CA file into
    the config directory (`~/.config/mash/ca.pem`, and `config.json` names
    the copy); it is printed as a `copied` line. It holds no secret, mode 644.
15. DECIDED (by an earlier draft). Writing the configuration is flags on
    `credential install`, not a separate `mash config` step. A `mash config`
    verb could come later for changing one value.
16. SUPERSEDED (decision 264): `--replace` and the in-place change of trust
    do not exist; an existing different setting is refused and the fix names
    the file to move aside (R17).
17. STILL OPEN. Where mash finds a server when neither the flag nor
    `config.json` has one: mashctl's configuration on this machine, as the
    same-machine form shows. Needs the CLI team to confirm the lookup order
    (flag, `config.json`, mashctl's configuration) and that mash never reads
    mashctl's admin credentials, only the server address and CA.

N1. STILL OPEN. `mash inbox --before SEQ` (older messages) is a NEW flag: the
    source has only `--limit` and `--after`. The older-messages line needs it.
N2. STILL OPEN. `mash send --reply-to SEQ` is a NEW flag (the MCP tool has
    `in_reply_to`); `mash read`'s `NEXT:` needs it.
N3. `mash who`: `--by-group` DECIDED. `mash who <address>` for one
    endpoint's `--detail` block: STILL OPEN (small, new). The `*` mark with a
    `* you` footer, `now` and `never`: DECIDED, planner (L7).
N4. DECIDED, planner (L4, supersedes DRAFT2). The JSON field for the name
    part is `name` (was `instance`), the full address is `endpoint`: the
    mapping is the table at the head of section 6.
N5. `mash admin` is out of the people-facing help: DECIDED. Whether the verb
    is deleted from the binary: STILL OPEN.
N6. STILL OPEN. The plain-URL refusal exits 1 in every command, `serve` and
    `hook` included (today 2). For `hook` the current code always exits 0;
    a hook that exits 2 can be read as a blocking error. Not verified against
    Claude Code's hook documentation.
N7. SUPERSEDED, planner (L4). DRAFT2 kept group and name in the environment
    because `config.json` had no key for them; the endpoint is now one
    address (`endpoint`), so the file can hold it.
N8. NEW, this draft's decisions beyond the brief, easy to reverse: the
    environment names follow the `config.json` keys, so `MASH_URL` becomes
    `MASH_SERVER` and `MASH_TLS_CA` becomes `MASH_CA` (hard cut, like
    `MASH_GROUP`); `mash inbox --wide` (MESSAGE whole) is new; the slash form
    of an address is refused; `mash status` reports the plain-URL refusal as a
    report (exit 1) rather than as a bare error.
N9. STILL OPEN, small: the `recipient` row of `send --detail`, the plugin
    version in `mash version --detail`, a message size limit, and how
    partial answers of `prompt` show in the CLI were not checked in the
    source.
N10. STILL OPEN. `last reached` in `mash status` is the time `mash serve` last
    recorded an answer from the server (state directory); that record is new.
    Without it the row can only say `checked <now>` or `never reached from
    here`. (DRAFT4 called it "last seen"; the word now belongs to `mash who`
    alone, D1.)
N11. STILL OPEN, needs the CLI team and the server. The agent's `status`
    can say DELETED only if the server's refusal tells a deleted endpoint
    from a reissued key; the shipped code does not.
N12. STILL OPEN, decided in principle by the planner (L7). The kind
    `message`: a send with no `--kind` is stored as `task` today. The target
    stores `message` (waking like `task`; an older receiver reads it as
    `task`). The alternative that needs no protocol change is to display
    `task` as `message` and lose the difference between the two.
S1. DECIDED here. INV-003 says an existing different secret file is refused,
    never truncated or replaced. `--force` (replace an existing credentials
    file) contradicts it, so it is removed. The way back after a reissued key
    is to move the old file aside (the fix names the `mv`).
S2, S3, S12. SUPERSEDED (decision 264). The typed fingerprint and the typed
    server address are gone: nothing is typed, and the first install SHOWS the
    address and the fingerprint (R15, contract rev 15). (DRAFT10: it asks a yes
    or a no BEFORE writing and shows the account too; the
    display form is now 8 groups of 8 separated by spaces, on two lines, R15.)
S4. DECIDED here, for the lead to confirm. REQ-004 fixes the default rows
    of `status` and REQ-009 says status names how the endpoint was chosen.
    The targets keep the rows, name the endpoint in the verdict, and put
    "chosen by" in `--detail`, on the choice screens, and in the MCP `status`
    field `chosen_by`.
S5. DECIDED here. The token is written mode 600 and no longer labelled "holds no
    secret" (INV-001, INV-003 list tokens with seeds). The CA copy and the
    public key are not secrets.
S6. SUPERSEDED (decision 264): after a CA change on purpose the fix is to move
    the old CA copy aside and install again (R17).
S7. DECIDED here. The older-messages hint under a long inbox is a sentence and
    a whole command (`Older messages are before #56. To see them:` then `mash
    inbox --limit 2 --before 56`), not a `NEXT:` (listings print none). There is
    no empty `who`: the caller is always listed.
S8. DECIDED, the lead, D1. The server row of `status` never says "last seen":
    `alive, encrypted (checked 09:42)`, `NOT REACHABLE (last reached 3h ago)` or
    `not checked`; MCP `server.state` is `alive`, `not_reachable` or
    `not_checked`, with `server.checked_at` and `server.last_reached`; `LAST
    SEEN` means an endpoint's last mail activity in `mash who` only. The
    server is checked in every state that has an address.
S9. DECIDED, the lead, D2. A certificate the trusted CA did not sign is mash
    refusing the server, and the error says so: `mash does not trust the
    server at myserver:4222: its certificate is not from your CA`.
S10. DECIDED, the lead, D4 (CLI rule 30 amended). A configuration fix prints
    `add this line to <path>:` and the line, indented four spaces, never in
    command position; a one-shell test is a separate item labelled `for this
    shell only:` (R10).
S11. DECIDED, the lead, D7. Removing access prints no NEXT (R19); `mashctl
    endpoint delete`'s quoted text has no NEXT.
N13. STILL OPEN. `mash who` lists the endpoints of the caller's owner; the
    directory's scope was read from the code in DRAFT2 and not re-checked.
N14, N15. SUPERSEDED (decision 264): there is no install script. The install
    check is one pasted `gh attestation verify --bundle` (section 5a).
N16. STILL OPEN, UNVERIFIED. The plugin is installed with Claude Code's own
    commands, `claude plugin marketplace add bogworks/mash` and `claude plugin
    install mash@mash` (the 0.5.5 `docs/install.md`; `claude plugin --help` on
    this host for the flags). Nothing was run against Claude Code: what
    `claude` prints, and its exit code when the plugin is already installed,
    are not shown anywhere in this file, and the quick start says the plugin
    is not verified. `mash plugin install` does not exist.
N17. STILL OPEN, ctl's. REQ-011 (ii) `mashctl adopt --from <dir>` and (v)
    `mashctl endpoint delete` leaving this machine's credentials file as it was
    are mashctl output. The `adopt-from` block is a placeholder until
    `docs/plans/2026-11-19-cli-targets-mashctl.md` lands; the delete text in
    section 4 is the CLI team's.
N18. SUPERSEDED (decision 264). `mashctl adopt` asks no fingerprint (REQ-011
    (ii)).
N19. FOR THE LEAD. R20 says no other command names config or the server in
    success output. One block still does: the rows of `mash status` (a report;
    its job is exactly that), and the status-views page may drop the server row.
    The `mash serve` start-up line names the endpoint only (X12).

DRAFT6 decisions that SURVIVE the scope ruling: X7 (STILL OPEN, ctl's:
`--make-key` on a machine that has no `config.json`), X10 (exit codes, below),
X11 (MCP `chosen_by` is `env`, `config` or `only_one`), X12 (`mash serve`
prints `mash: serving as <endpoint>` and no server), X15 (config names:
`server`, `ca`, `endpoint`; `MASH_SERVER`, `MASH_CA`, `MASH_ENDPOINT`,
`MASH_CONFIG_DIR`, now `MASH_DANGEROUSLY_SKIP_NATS_ENCRYPTION`). SUPERSEDED:
X1 (the typed server address), X2 (generations and the pointer), X3 and X4
(`trust repair`, `trust replace`), X5, X6, X8 (the install script), X9 (`mash
plugin install`), X13 (trust files that disagree), X14 (the trust-screen
vocabulary) and X16 (`mash trust reset`). X10, kept: 6 when the server is
unreachable or failed; 1 for a local file error, with its cause; 2 for usage;
4 for not authorized, with two message texts (the server refused the
credentials; and a person must run it at a terminal, which in 0.5.6 is
mashctl's confirmations and the first `credential install`'s, INV-004, R15);
kept at 1: a certificate mash does not
trust, and a `prompt` nobody answered.

DRAFT7, decisions beyond the brief or between two contract lines (each is easy
to reverse; the dispositions file has one row per item of the ruling). Contract
revision 15 (the newest in the shared folder at the start and at the end of
this work) is conformed to except where a line below says so.

Y1. DECIDED here, for the lead to confirm. `credential install` has a `--server
    URL` flag on the first install. Rev 15 says the first install SHOWS "the
    server address" and checks "the server at that address", and AT-007 tests
    "a substituted server address under the same CA", but REQ-003's synopsis
    lists only `--key --token --ca` and the token names no server (B2, R5-4),
    so the address needs an input; with nothing typed, a flag is the only one
    that works for another machine. DRAFT8: CONFORMED, contract rev 16's REQ-003
    synopsis names `--server`. The README quick start and Day 2 pages (not
    changed here) must carry it on their `credential install` lines.
Y2. DECIDED here; DRAFT8: CONFORMED to rev 16 (F15-9). `--server` and `--ca` are
    BOTH required on a FIRST install; each missing one exits 2 before anything
    is read; a later install for another endpoint uses the configured server and
    CA, and a flag that is given must match them (R17). (Rev 15's unqualified
    "`--ca` is required" is gone: rev 16 says "on a first install".)
Y3. SUPERSEDED, DRAFT8 (the lead, rev 16). `--creds FILE` is DROPPED from
    `credential install`: the other-machine path is key plus token. No help
    line, block, MCP field or hand-over text names it; mashctl's own
    `--make-key` and `--creds-out` are its targets' business.
Y4. DECIDED here in DRAFT7, REVERSED in DRAFT8 (the lead, rev 16, security
    F15-2). The token's signature must verify against the key of the account
    the authenticated server serves, before any write; a valid token of another
    account or server is refused at install (`install-refuse`), not left to the
    server's refusal of the credentials (`status`, exit 4).
Y5. DECIDED here. A later install checks the server's certificate again
    (the `checked` line), as the contract says "before writing".
Y6. DECIDED here, UNVERIFIED. The file to move aside is `config.json` for a
    different server and `ca.pem` for a different CA; whether moving only that
    one file lets the same command succeed depends on the code of 0.5.6.
Y7. SUPERSEDED by Z14 (DRAFT10). `credential install --json` carried
    `ca_fingerprint` and ran without a terminal; a first install now needs a
    terminal for its yes or no, `--json` included (exit 4 without one).
Y8. DECIDED here, UNVERIFIED. The ONE `gh attestation verify --bundle` is run on
    `SHA256SUMS` (it names both binaries); `sha256sum -c` then checks the two
    binaries against the sums the attestation covered. If the 0.5.6 bundle names
    the binaries, the page verifies `mash-linux-amd64` the same way.
Y9. DECIDED here. The mailbox row is the FIFTH and last row (after the
    credentials it needs), on every screen; `--detail` adds a `mailbox limit`
    row (the sixth fixed row) and, when something waits, a line `oldest waiting
    12m ago` under the mailbox row. SUBJECT TO THE STATUS-VIEWS PAGE (it may
    drop the `server` row; the order of the others stays).
Y10. DECIDED here. The setting is `mailbox_limit` / `MASH_MAILBOX_LIMIT`
    (the second follows `idle_wake` / `MASH_IDLE_WAKE`), a whole number from 0
    to 1000 (O4); a value outside it is refused (`mailbox-limit-refuse`).
Y11. DECIDED here. MCP `status` carries `mailbox {waiting, limit, oldest_age,
    over_limit}`; `oldest_age` is a string in R12's form (`"12m"`) or `null`;
    an over-limit mailbox is NOT an entry of `problems[]` (it is a warning, and
    `ok` stays true).
Y12. DECIDED here. The hook text is unchanged: the wake line is `mash
    serve`'s, and the hook drains up to 10 messages a run (O5, section 5b).
Y13. DECIDED here. R2's exception for `operator` and `account` is removed with
    the trust screens; the install screen says "whoever runs your mash
    server".
Y14. DECIDED here. The wake line is the contract's, word for word: `mash: <n>
    messages waiting (limit <limit>), oldest <age>`; `<age>` is R12's duration
    (`45s`, `12m`, `3h`). `<n>` is always at least 2, so the plural is always
    right. The channel event carries the meta key `backlog: true` (this draft's
    choice, beside `wake: true`).
Y15. DECIDED here. The first-install hand-over of mashctl (step B) now carries
    `--server` and `--ca` in its NEXT command and no longer says the person
    types anything; the mashctl lane's own targets must say the same.
Y16. STILL OPEN. The comparison after the first install is the person's and
    happens after mash has written (mash prints the two values first, then
    checks and writes); a person who finds a difference removes the three
    files it listed (DRAFT8: `config.json` goes first, then the credentials and
    the CA copy). Nothing prompts, by the ruling. The Day 2 page says so.

DRAFT8, decisions beyond the brief or between two contract lines (each is easy
to reverse; the dispositions file has one row per item of the ruling).
Contract revision 16 (the newest in the shared folder at the start and at the
end of that work) was conformed to except where a line below says so. Z9 to Z13
are DRAFT9's (contract revision 17).

Z1. DECIDED here (the `checked` wording is superseded by Z15 and the
    install blocks of DRAFT10). The new token check prints no line of its own
    on success: it
    is part of the one `checked` line, which now reads `checked  the server at
    myserver:4222: its certificate is from this CA for its name, and it
    accepted this key and token` (two lines, the second indented). On refusal it is the block
    `error: this token was not signed by the account of the server at
    myserver:4222. Nothing was written.` with a labelled `fix:` and the whole
    command to run. The word `account` is the lead's text and the contract's;
    R2 lists it as the one exception.
Z2. DECIDED here. WRITE ORDER (F15-6), shown in every success block in this
    order: the CA copy (`copied`, or `kept` when it is already there), the
    credentials (`wrote`), `config.json` last (`wrote ... written last`, or
    `kept`). `install-rerun` shows a rerun after a kill between the CA copy and
    the credentials: the CA copy `kept (unchanged)`, the other two `wrote`. The
    rerun prints the same first-install screen, because `config.json` (the
    commit point) was not written and so nothing was installed. UNVERIFIED: that
    the 0.5.6 code prints the full first-install screen on a rerun, and whether
    it says anywhere that it is completing an earlier run.
Z3. DECIDED here. The two `mashctl endpoint create --make-key` blocks quoted in
    section 4 list their files in the same write order (they are the CLI team's
    text and the mashctl lane may differ; the rule is R20's).
Z4. DECIDED here, UNVERIFIED. With `--creds` gone, the fix of the "certificate
    not trusted" screens moves the old `ca.pem` and `config.json` aside and
    prints a first install with `<key file>`, `<token file>`, `<address>` and
    `<ca file>`: after a CA change the person needs a token for the new server
    (the new CA is a new server), and the credentials file already installed
    may need moving aside too (R17); that is not said on these screens.
Z5. DECIDED, the lead (rev 16; the digest and reading parts and the restart
    sentence are SUPERSEDED by Z17 and Z18): REQ-015's count is unacked,
    pending plus
    ack-pending (section 5b); O1 yes (digest-listed messages count); O2 no
    (reading does not lower it). The COOLDOWN is per endpoint, from the last
    wake, and does not survive a restart of `mash serve` (R22, the
    `serve-backlog` text, section 5b): after a restart the first wake can come
    immediately.
Z6. SUPERSEDED by Z22 (DRAFT11). Rev 16 had `mash endpoint status` show the
    count in a `MAILBOX` column for the caller's own owner's endpoints; rev 26
    removes the column (an endpoint's credentials read only its own inbox).
Z7. DECIDED here. The example server is `tls://localhost:4222` in every
    ONE-MACHINE block (status, serve, send, the second-endpoint install, the
    MCP examples), because that is what the NATS helper prints (its DRAFT7
    closing box: `Server address     tls://localhost:4222`) and what the README
    quick start shows; `tls://myserver:4222` stays only on the OTHER-MACHINE
    path (step B, step C, its first-install refusals and its trust screens) and
    in the `mash help credential install` example, where the host is the
    operator's `myserver` by design. The machine name `myserver` stays in the
    `runs on` and `host` fields of `who`, where it is a host name, not an
    address. No separate "myserver vs localhost" rule is printed for readers;
    the cast paragraph says it once. The status-views page (not edited here)
    still shows `tls://myserver:4222` on its screens.
Z8. DECIDED here. The `--json` of `credential install` gets `token_checked:
    true`, beside `ca_checked`.
Z9. DECIDED, the lead (DRAFT9), conformed to contract rev 17. WHO SIGNS: the
    token is signed by mashctl's account signing key. No block or sentence says
    the server "made" or "signed" it. The one phrase for the install `checked`
    line is `its certificate is from this CA for its name, and it accepted this
    key and token` (chosen here: rev 17 has install connect with the new key
    and token, and a server in operator mode accepts a user token only when an
    account it holds signed it, so "accepted" is exact and "made" was not).
    mashctl's agreed first lines are quoted word for word, wrapped by hand at
    80: `--make-key` (`Made a key pair for acme.demo.bob: ...`) and
    `--public-key` (`Your mashctl's signing key signed a token saying this
    public key is acme.demo.bob.`); the rest of mashctl's quoted blocks is kept.
Z10. DECIDED, the lead (DRAFT9). `mashctl endpoint create --make-key` refuses
    when `config.json` has no `server` and `ca`, writes nothing, and names the
    two ways in (`mashctl adopt --from <dir>`, or `mash credential install`
    with `--server` and `--ca`): block `ctl-make-key-refuse`. UNVERIFIED: the
    brief relayed mashctl's error line and the two commands, not the full
    block; the `fix:` wording is the targets' and mashctl's text wins.
Z11. DECIDED, contract rev 17 (R16-1, R16-3). An agent never runs `mash
    credential install` or installs the plugin; the person does. Said in the
    `mash help credential install` text and in the MCP initialize instructions
    (section 6). The README has no skill hand-off section, so nothing is
    added there.
Z12. DECIDED, the planner's newest ruling (DRAFT9). `mailbox_limit` 0 reads
    `no limit set` in the mailbox row (and in the `mailbox limit` row of
    `--detail`), not `off`; the setting's own meaning ("0 turns the check
    off") is unchanged.
Z13. SUPERSEDED by Z17 (DRAFT10). DRAFT9 had the planner's "not yet delivered"
    count and left the unacked text in place with a one-line note in section
    5b; DRAFT10 rewrites section 5b, R22 and the serve text to the lead's
    ruling (rev 21: unacked, and delivery acknowledges).

DRAFT10's decisions (contract revisions 18 to 21; the newest revision binds over
an earlier ruling, and the dispositions file says where they conflicted). Each
is easy to reverse.

Z14. DECIDED, the lead (rev 18, 20, 21; order and `--account` from rev 22 to 24).
    The first install shows three values and asks `Do these three match what the
    operator sent you? (yes/no)` BEFORE any write and before any connection that
    carries the key or token (R15, `install-confirm`). Chosen here: the question
    and its answer are read from the terminal itself, never from standard input,
    so `--token -` and a piped token still work on a first install; the `no`
    outcome keeps the `error:` label (DRAFT11, Z22: `error: you did not confirm the
    three values.`, then the lead's text `Nothing was written. Ask the operator
    who made this endpoint.`); `--json`
    asks the same question; `--dry-run` writes nothing and asks nothing; a LATER
    install (config and CA already there) shows and asks nothing, since the
    contract says "first install" and no record of the confirmed account exists
    (it still needs `--account`); a rerun after an interruption is still a first
    install and asks again. R2 gets the exception for the words `account` and
    `operator`.
Z15. DECIDED, the lead (rev 18 to 24), blocks chosen here. New refusal blocks,
    each writing nothing: `install-confirm-no`, `install-no-terminal` (exit 4),
    `install-refuse-account`, `install-refuse-noauth`, `install-refuse-ca`,
    `install-refuse-keyfile-mode`, `-symlink`, `-owner`. The error lines are
    the lead's words; every `fix:` is written here (UNVERIFIED against the
    built code). The earlier refusal for a certificate that was not from the CA
    for the name is now two: `install-refuse-ca` (a different CA than the
    confirmed one) and a host-name block (`not for the name otherhost`, under
    `install-refuse`), the second reworded because the CA half moved. The lead's
    rev 16 line `this token was not signed by the account of the server at
    <host>:<port>` stays, as the refusal for a server that does not accept the
    key and token (exit 4). The three file refusals are shown with the KEY file;
    a token file says the same with its own name. A server that cannot be
    reached (exit 6) and a connection the server refuses beyond the line above
    have no block of their own: the contract names the tests and gives no
    wording.
Z16. DECIDED, the lead (rev 21), UNVERIFIED in code. Every interrupted state is
    ignored before the rerun: the rerun starts from the files actually present
    and never trusts a leftover marker or half-written file; a temporary file a
    killed run left is never read and never counted as installed. Said in
    section 2's write-order rule and in `install-rerun`'s text.
Z17. DECIDED, the lead (rev 21). REQ-015's count STAYS pending plus ack-pending
    and DELIVERY ACKNOWLEDGES: shown in full, read with the `read` tool and read
    with `mash read` all acknowledge and lower the count; a digest line does not
    and still counts. Section 5b, R22 and the serve text say it and list the
    code that changes (`hookless.go:144-185`, `cli.go:134-148`, the lease's
    `backlog` and `backlog_woke_at`) and what else it touches. The line DRAFT9
    added to 5b ("superseded by the status-views page") is gone. The brief's
    sentence that `mashctl audit` can exceed the status number by the
    digest-listed count is NOT carried: under this ruling both count the
    digest-listed messages, so they agree (5b, "what else", item 2).
Z18. DECIDED, the lead (rev 19). A backlog already over the limit when `mash
    serve` starts wakes once, at 10 minutes after the start, if still over;
    the cooldown is per endpoint, from the last wake, and starts at serve's
    start (the clock is not kept across a restart). This REPLACES the DRAFT8
    wording ("after a restart the first wake can come immediately"), which
    rev 17's text (cooldown starts at start) already contradicted.
Z19. DECIDED, the lead (rev 21 against the planner's DRAFT9 note). With
    `mailbox_limit` 0 the row keeps the count (`3 waiting, no limit set`): the
    contract says "the count is still shown", and the older block said the
    count was not. `status-mailbox-off` changes.
Z20. DECIDED here. The fingerprint's JSON form is the eight groups on ONE line
    separated by spaces (a JSON string wrapped under its key at 78 columns);
    `--json` also gains `account`, `confirmed` and `account_checked`.
Z21. DECIDED, the lead and the newest contract (rev 22 to 24), applied while
    this draft was being written. `--account <key>` is on EVERY command line of
    `credential install`, first or later: every `$` command, every printed `fix:`
    command and the NEXT of step B carry it (a real key where the rest of the
    line is real, `<account key>` where it holds placeholders); new blocks
    `install-refuse-noaccount` and `install-refuse-badaccount` (exit 2, nothing
    read, no connection). Decisions here: (a) the account comparison is the
    first check after the files, so `install-refuse-account` has no question
    before it, and no answer could change it; the lead's error line
    (`... than the one you confirmed.`) is kept word for word although, by then,
    "confirmed" means the account the person gave and will confirm; (b) with
    several flags missing the order of mention is `--ca`, `--server`, then
    `--account`; (c) the later install's temporary name `.<address>.creds.tmp`
    and the rule that mash ignores `.*.tmp` are in section 2's write-order rule
    only: no printed block shows a temporary file; (d) the `checked` line of
    the token's account stays after the question, as a confirmation of what was
    compared before it (UNVERIFIED how the built code words it); (e) rev 26
    adds the refusal while another install's temporary is younger than 10
    minutes (`install-refuse-running`, the lead's words, exit 1), the address
    grammar (1 to 63 characters a part, at most 191, lowercase only), that a
    later install's `--server` and `--ca` must be byte-identical to the
    configured ones, and that only a first install needs a terminal; all in
    section 2 and R15's later-install sentence, one new block.

DRAFT11's decisions (the lead's rulings of 11:08Z, contract rev 26 final). Each
is easy to reverse.

Z22. DECIDED, the lead. (a) The pasted install checks its destination (rev 24,
    REQ-011 (i)): before it copies, it refuses a destination folder, or any
    parent of it, that is a symbolic link or writable by the group or the
    world, with `error:` and `fix:` lines the paste itself prints (blocks
    `install-check-writable` and `install-check-symlink`, shown for
    `/home/ada/.local/bin`). Chosen here: the check is a subshell loop from the
    destination up to `/` using `[ -L ]` and `find ... -maxdepth 0 -perm`
    (portable to macOS, where `stat` differs); it comes right before the copy
    in the same `&&` chain, after the verify; the folder is made first with
    `umask 022` (rev 24: "created by the person's own user, 0755, then
    checked"); the symlink refusal gives no command, because only the person
    knows the real folder; a folder owned by ANOTHER user but not writable by
    others is not refused (rev 24 asks for "symlink or writable by another
    user" only); a parent that is a symbolic link is refused even when it is
    the system's (`/home` on some distributions), because the rev 24 test
    names a symlinked parent. Measured on the author's own Linux account: the
    ordinary `~/.local/bin` there is `0775`, so the refusal will fire on many
    Linux accounts; the `chmod go-w` fix is the answer. The macOS block of the
    earlier README (Mac before Linux) is NOT restored: the quick start is a
    Debian or Ubuntu page (decided at the README v3 rewrite, its server helper
    runs there only), so the targets (section 5a) say the same paste works on
    a Mac with `P` and `shasum -a 256 -c -`, and the README shows Linux alone.
    (b) `mash endpoint status` has no mailbox column (rev 26): removed from R22,
    section 5b's table, the DRAFT10 summary and Z6; a caller's own count is in
    its own `mash status`. (c) The answer `no` keeps the `error:` label:
    `error: you did not confirm the three values.` then `Nothing was written.
    Ask the operator who made this endpoint.`, exit 1 (`install-confirm-no`,
    Z14). (d) The two refusals without contract wording are written, both after
    a `yes`, both writing nothing: `install-refuse-refused` (exit 4) and
    `install-unreachable` (exit 6, `did not answer within 20s`, the open
    timeout a connecting command uses, section 2 Timeouts, not status's 2
    seconds). The older block for a token "not signed by the account of the
    server" (rev 16's text) is KEPT beside the new `install-refuse-refused`:
    two words for one exit-4 condition; the lead may pick one (mash cannot tell
    the causes apart). (e) Accepted as is: yes and no are read from the
    terminal, a later install shows no confirmation, and `--dry-run` asks
    nothing.

## 8. Behaviour changes the contract must decide

- Settings (section 3): `MASH_ENDPOINT` and `"endpoint"` are new;
  `MASH_OWNER`, `MASH_GROUP`, `MASH_NAME` and the `owner` key are removed;
  `MASH_URL` / `url` become `MASH_SERVER` / `server`, `MASH_TLS_CA` /
  `tls_ca` become `MASH_CA` / `ca` (N8); `mailbox_limit` / `MASH_MAILBOX_LIMIT`
  is new (REQ-015). One installed endpoint needs no setting. `credential
  install` never writes `endpoint`. The automatic pick counts only regular
  files owned by the user with mode 600; none or several is a choice error. The
  `insecure_plaintext` key of config.json is removed (a file that has it is
  refused); `MASH_DANGEROUSLY_SKIP_NATS_ENCRYPTION=1` for one run is the only
  override, and `MASH_INSECURE_PLAINTEXT` is gone (hard cut, no alias).
- `mash status` connects (once, 2s budget); it may take 2s and exit non-zero
  when the server is down (new network behaviour). Its output is one fixed
  screen (R11) with a mailbox row; `--detail` is new.
- Exit codes, all changes in one list (section 2):
  - New: 4 for not authorized (both tools): the server refused the credentials,
    or a person must run it at a terminal (mashctl's confirmations and the first
    `credential install`'s yes or no).
  - New meaning of 6 (contract rev 7): the server is unreachable or failed,
    in both tools (`status` and `send` when it does not answer; was 1). A
    local file error is 1 with its cause (it was 6).
  - `mash status`: 0 mail works (warnings included); 1 it does not (was 0);
    2 usage.
  - Plain URL refused: 1 in every command that connects and in `status`
    (was 2, and 0 in `status`; N6 for `serve` and `hook`).
  - Unknown flag or missing argument: 2 for every command (today `inbox`,
    `read`, `send` usage errors exit 1), a first `credential install` with no
    `--server`, no `--ca` or no `--account` included (a later install needs
    `--account` too).
  - `serve` with several endpoints and none chosen: 1 (a missing group was
    2). `serve` with no credentials: 0, unchanged.
  - `credential install` with a missing token file or an unreadable `--ca`
    file: 1, with its cause (was 6 for the token).
  - Every other exit code is unchanged.
- Errors start `error:` in both tools (was `mash:`); hints are `fix:` and
  `NEXT:`; the label `next:` is gone.
- Plain-text warning: `warning: MASH_DANGEROUSLY_SKIP_NATS_ENCRYPTION is set;
  ...` once, on stderr, in every command that connects whenever the variable is
  set; in the server row of `status`.
- Secrets (INV-003, R16): `--key` and `--token` take file names or `-`;
  a pasted token or key is refused unprinted; every secret file is created
  exclusively, mode 600, without following links, by atomic rename; an
  existing different file or a symlink is refused; `--force` is removed.
- Install (R15, R17): EVERY `credential install` takes `--account <key>` (the
operator's account key, whole; missing or incomplete is exit 2, and a token
whose issuer differs refuses before any write, whatever the person would
answer); the first one on a machine needs BOTH `--server` and `--ca` (each
missing one is exit 2, before anything is read),
  refuses a key or token file that is a symbolic link, foreign-owned or broader
  than 0600 before it connects, and SHOWS three values before any write: the
  server address, the CA fingerprint (groups of 8, two lines) and the account
  (the whole 56-character key); it asks `Do these three match what the operator
  sent you? (yes/no)` on a terminal (anything but `yes`: nothing written, exit
  1; no terminal: exit 4). After the `yes` it checks that the token's issuer is
  the confirmed account, that the server's certificate is signed by the
  confirmed CA for that host name, that the server requires authentication and
  that it accepts a connection made with the key and the token (F15-2, R16-2;
  no directory lookup), before it writes anything; then it writes the CA copy,
  the credentials and `config.json` (server, CA) last, and a rerun starts from
  the files that are really there and completes an interrupted install (F15-6).
  A later install for another endpoint uses the configured server and CA, shows
  and asks nothing, and a flag given must match them. An existing server, CA or
  credentials file is never changed: any difference refuses without touching a
  file and names the file to move aside. mashctl prints the three values for
  the operator. There is no trust record, no generation, no `mash trust reset`,
  no `--replace`.
- `mash status`: the server row says when mash checked or last reached the
  server (no "last seen"); the mailbox row is new on every screen; `--detail`
  shows six rows that are the same in every state (config file, the CA as its
  own row, credentials file, endpoint, how it was chosen, mailbox limit); new
  screens for no endpoint installed, a credentials file that is not private
  (`UNSAFE`), the override set with a tls address, and mail backing up.
- REQ-015 (section 5b): `mash serve` wakes the session once when more than
  `mailbox_limit` (default 15) messages are unacked (pending plus ack-pending),
  whatever their kind, with `mash: <n> messages waiting (limit 15), oldest
  <age>`, then at most once in 10 minutes per endpoint, from the last wake (the
  clock starts when serve starts and is not kept across a restart); a backlog
  over the limit at the start wakes once, at 10 minutes. DELIVERY
  ACKNOWLEDGES (new in 0.5.6: the `read` tool drains any waiting message,
  `mash read` writes the drained tombstone, serve writes `backlog` and
  `backlog_woke_at` into the lease; an ADR amends ADR 0041); a digest line does
  not acknowledge and still counts. `mash status` and the MCP `status` show the
  count (`<n> waiting, no limit set` for a limit of 0), and `mash endpoint
  status` has no mailbox column (rev 26).
- Listings (`inbox`, `who`) print no `NEXT:`.
- `mash status --debug` stays unsupported (exit 2); one flag, `--detail`.
- `--detail` is added to `version`, `status`, `send`, `inbox`, `read`,
  `who`, `key create`, and the help.
- `mash --version` is new and prints what `mash version` prints.
- `mash who --long` renamed `--detail` and shown as blocks; `--names`
  removed; `--by-group` and `mash who <address>` added; `*` and the footer;
  JSON field names change (`identity` to `endpoint`, `online` to `state`,
  `agent` to `program`, `lease.host` to `host`, `lease.version` to `version`,
  `instance` to `name`, the storage `name` to `key` behind detail).
- `mash inbox`: new columns, a header with counts, relative times, no `NEXT:` (a
  listing), `--before SEQ` (N1), `--wide`, `--json`. Oldest first in
  the CLI, the hook and MCP.
- `mash send`: `--reply-to N` (N2), `--detail`, `--json`; the default output
  drops the recipient's inbox number; the slash form is refused; the default
  kind is `message` (N12).
- `mash read`: first line, aligned rows with `urgency`; `NEXT:` only for a
`question` or `blocker` from another endpoint; reading acknowledges the message
and lowers the mailbox count (CHANGED, rev 21: today `mash read` never acks).
- `mash credential create` renamed `mash credential install` (hard cut);
  flags `--seed`/`--jwt` renamed `--key`/`--token`; new `--ca` and `--server`
  (no `--creds`); no `--replace`, no `--force`; JSON schemas bump to `@2`; the first
  install writes `config.json` (server, CA) and copies the CA; it lists every
  file written. `mashctl endpoint create --make-key` prints the same
  wrote-lines; `mashctl endpoint delete` prints the delete text without a NEXT
  (R19) and leaves the credentials file as it was; `--creds-out` is new in
  mashctl and is not named in any `mash` screen.
- A server certificate the trusted CA did not sign is refused with a plain
  message in `status` (exit 1) and in every command that connects.
- `mash key create`: output text, `.key` naming; `seed_path` becomes
  `private_key_path` (`mash-key@2`); the word seed only in `--detail`.
- `--dry-run` of `key create` and `credential install`: the `NEXT:` names the
  same command without `--dry-run`.
- `mash prompt`: first line (stderr, terminal only) is new; the timeout is an
  `error:` with exit 1.
- `mash plugin export`: prints a file count and a three-command `NEXT:`. There
  is no `mash plugin install`, no `mash trust reset` and no `install-mash.sh`.
- Install check (section 5a): one pasted `gh attestation verify --bundle`, then
  `sha256sum -c`, then plain commands; no script is executed.
- `mash serve`: one stderr line at start-up, `mash: serving as <endpoint>`
  (no server); refusal texts are plain lines; the backlog wake line above.
- `mash` with no command prints one usage line (exit 2 unchanged); an unknown
  command suggests the closest one.
- Top-level help: starts with a `Usage:` line, grouped, at most 80 columns,
  documents `MASH_ENDPOINT` and `MASH_MAILBOX_LIMIT`; `serve`, `hook` and the
  settings table only behind `--detail`; `mash admin` out of it.
- Hook text: no `instance` id, trust in words, oldest first, no hook jargon.
- Every printed command follows R9 and every configuration fix follows R10.
- MCP: every tool returns JSON (text plus `structuredContent` and
  `outputSchema`); errors are an object with `code`, `message` and `fix`;
  `who` loses `format`; `inbox` gains `after` and `before`; `status` gains
  `mailbox`; `version` drops the `client:` line and `owner`; the field mapping
  at the head of section 6.
- Colour (section 2): new in both tools; none exists today.

## 9. Checks run

Width (re-measured for DRAFT11, contract revision 26 final). A script (`width.py`, run
over the file, kept outside the repo) walks the file, tracks the fenced blocks
and keeps every line over 80 columns. Result for this file: 162 fenced blocks
(every mockup, Before block and JSON example and the `adopt-from` placeholder),
141 of them chosen by a `packet:` marker (83 distinct keys; DRAFT10 had 157,
136 and 79: four keys are new, `install-check-writable`, `install-check-symlink`,
`install-refuse-refused` and `install-unreachable`, and none is out; the
`install-check` key has one more block, the `mash version` check on its own,
and its install block is now the destination-checked chain; DRAFT10's twelve
new keys were `install-confirm`, `install-confirm-no`,
`install-no-terminal`, `install-refuse-account`, `install-refuse-noaccount`,
`install-refuse-badaccount`, `install-refuse-running`, `install-refuse-noauth`,
`install-refuse-ca`, `install-refuse-keyfile-mode`, `-symlink` and `-owner`,
and none was out). The widest line of text
that a tool prints is 80 columns (the real-name inbox rows and the trust-column
inbox row, whose message is cut with `...` at column 80 by design, and the
`--detail` install screen's `config.json` line); NO printed text is over 80,
with these exemptions (R1, R9): a command a person is told to run or can run
again, on a line of its own (`--account` is on every `credential install`
command, so they are longer: the widest are 175 columns, the NEXT of the
server-side step and the fixes (DRAFT11 adds two, `install-refuse-refused` and
`install-unreachable`) that repeat the whole command with the real
56-character account key; then 168, 135, 134, 132 and 125 for the other such
fixes with real values, 122 for the one that has a token placeholder; 121 for
the no-configuration, trust, `ctl-make-key-refuse`, removed-verb and status
fixes that use `<key file>` placeholders and `--account <account key>`; 87 for
the shortest placeholder form; every other printed command is 81 or less, the
reply command with an 18-character sender name); a URL (the one link to the
guide, 84 columns); a
pasted `$` line (the send warning's, 84 columns, with the variable in front);
the install paste's own `echo` lines (76 columns at most, in a pasted block,
which is a command a person pastes); and `gh`'s own report on the install page (88, 107 and 138 columns, `gh`'s
words, not mash's). The JSON of `credential install --json` wraps its
fingerprint and the account onto lines of their own (78 columns). The account
line of the confirmation is 17 + 56 = 73 columns, so the whole key fits (never
shortened); the fingerprint lines are 17 + 35 = 52. `localhost` is one column
longer
than `myserver`: the status screens that follow the helper stay at 79. Widest
per category: status screens 79, `credential install` 80 (the `--detail`
screen; the refusals 78, the new `install-refuse-refused` and
`install-unreachable` 74; the destination refusals of the pasted install 75; the first-install screen 73; the rerun 73), `key
create` 75, the server-side step 75 (its command is the exemption), the trust
screens 75, send 79, hook 76, help 78, serve and the backlog wake 79 (the wake
line itself is 48), mashctl hand-overs 72, plugin export 74, prompt 73, who 62,
read 57 (its reply command 81, exempt), version 23, mailbox 74.

Not covered by the 80-column rule: 51 prose lines that are rows of the field
tables in sections 2, 3 and 6 (up to 770 columns, the exit-code table's fourth
row). They are a specification for the contract, not output a person or an
agent reads from mash.

Checked against the shipped source (the v0.5.5 tag, cloned at
`scratchpad/catalog/src`) and the host:

- `.creds` contents: `keycred.CreateCredentials` writes
  `jwt.FormatUserConfig(token, seed)`: the file holds the private key (the
  seed) and the token, mode 600. So `bob.key` and the credentials file are the
  same private key (section 4).
- Settings (`internal/config/config.go`): config.json keys `url`, `owner`,
  `tls_ca`, `insecure_plaintext` (and others); environment `MASH_URL`,
  `MASH_OWNER`, `MASH_GROUP`, `MASH_NAME`, `MASH_TLS_CA`, `MASH_CONFIG_DIR`,
  `MASH_INSECURE_PLAINTEXT`. `group` was never a config.json key. The wake
  setting `idle_wake` has a key and `MASH_IDLE_WAKE` (`config.go:96`, `:414`):
  the pattern `mailbox_limit` follows. Section 3 says which are new and which
  are renamed.
- Exit codes today: 0, 1, 2 and 6 (`cmd/mash/main.go`, `keycred.go`; mashctl's
  usage text says "0 done or planned, 1 refused, 2 usage, 6 error"); 3, 4 and
  5 are unused. `hook` always exits 0. Plain URL: 2 today in every connecting
  command (`main.go`), `status` 0. 6 is a file error in the shipped source, and
  becomes "server unreachable or failed"; a file error becomes 1.
- Kinds (`internal/kind/kind.go`): `blocker`, `question`, `task`, `result`,
  `fyi`; the zero kind and an unknown one read as `task`; `Wakes` is true for
  blocker, question and task. `message` is not a kind today.
- `mashctl endpoint create --make-key` writes `<config dir>/<address>.creds`
  and no separate key or token file (`endpoint_create.go` usage); `--creds-out`
  is not in the shipped usage (new, from the CLI team); `mashctl endpoint
  reissue` exists (`endpoint_reissue.go`); `parseEndpointRef` accepts the
  dotted form.
- `docs/tls-and-clusters.md` exists; `docs/nats-setup.md` does not. The text
  uses the first.
- Paging flags of `mash inbox`: only `--limit` and `--after`; `mash send` has
  no reply flag (the MCP tool has `in_reply_to`); the CLI deadline
  (`cli_timeout`, default 15m) and the open timeout (20s) are in
  `internal/config`.
- REQ-015 (section 5b, with `file:line`): the waking tracker (`internal/endpoint/
  waking.go`) already holds every unacked message and counts only the ones that
  wake; the consumer is explicit-ack with 1000 unacked at most
  (`internal/store/store.go:130-137`); the endpoint's own credentials may read
  stream and consumer info and get messages of its own inbox
  (`internal/listset/compile.go:186-191`); v0.5.5's `mash status` from a shell
  connects to nothing (`cmd/mash/status.go:586-595`) and the shipped inbox line
  (`internal/endpoint/eviction.go:25-42`) shows the bounded read the count would
  reuse.
- The install check was RUN, not only read: on this host `gh` 2.103.3, with no
  login (a clean config directory, no token in the environment), verified the
  v0.5.5 release's `SHA256SUMS` against its `mash.intoto.jsonl` bundle with
  `gh attestation verify SHA256SUMS -R bogworks/mash --bundle mash.intoto.jsonl
  --signer-workflow bogworks/mash/.github/workflows/release.yml --source-ref
  refs/tags/v0.5.5` (exit 0; the report in section 5a is that run's, with the
  tag changed to v0.5.6), and the same command on a copy of `SHA256SUMS` with
  one digit changed failed with exit 1 (`Sigstore verification failed`). Not run
  for v0.5.6 (it does not exist yet).
- `claude plugin --help`, `plugin install --help`, `plugin marketplace --help`
  and `marketplace add --help` on this host (rounds 4 and DRAFT6): `plugin
  install` takes `--marketplace <source>` and a scope flag `-s, --scope
  <scope>` with `user`, `project` or `local` (default `user`). The two commands
  of 0.5.5's `docs/install.md` (lines 40 and 41) are what section 5a shows. No
  command was run against Claude Code, so nothing claude prints is shown or
  claimed. The plugin export writes 5 files.
- Contract revisions 18 to 26 (`contract-0.5.6-cli-ux-rev26.md`, rev 26 FINAL,
  which differs from `rev26-draft.md` only in the critic row (the read of rev 26
  was clean), the sentence that removes the `mash endpoint status` mailbox
  column and the publication list; the newest in
  `masterplanner/for-herds/mash-0.5.6/` at the end of DRAFT11; the
  work began at rev 21 and revs 22 to 26 appeared during it, so the contract was
  downloaded to the scratchpad at the start and again at the end, and the
  differences were applied). Read (the diff of rev 24 to rev 26 too) for
  REQ-003, REQ-013, REQ-015, INV-003,
  INV-004, AT-007, AT-008 and every line about install. Conformed to, line by
  line: `--account <key>` on every install (required, complete and valid, never
  ignored; a missing one exits 2 before anything is read; the local issuer
  comparison first, refusing before any write whatever the person answers);
  the three values and their form (the account whole, never its first 32
  characters); the question and its two outcomes (`no`: nothing written, the
  server never sees an authentication attempt, exit 1; no terminal: exit 4)
  BEFORE any write and before any connection that carries the key or token; the
  checks (the server requires authentication and accepts the connection; the
  certificate is from the confirmed CA; no directory lookup); the key and token
  file refusals before connecting; the write order, that every interrupted
  state is ignored before the rerun, and the later install's temporary name,
  its stale-file rule, the 10-minute refusal while another install runs (one
  block), the address grammar and the byte-identical `--server` and `--ca` of a
  later install (stated in section 2); REQ-015's
  unacked count with acknowledgement on delivery (shown in full, `read` tool,
  `mash read`; a digest line does not acknowledge), the wake 10 minutes after a
  start over the limit, and `no limit set` with the count still shown. Where
  revs conflicted, the newest won: rev 24 over the brief's "mash computes the
  account from the token's issuer" (the account is now the person's
  `--account`, compared with the issuer); rev 21 over the planner's "not yet
  delivered" count (DRAFT9) and over rev 16's "reading does not lower it"; rev
  21's `3 waiting, no limit set` over DRAFT9's row without a count. Lines NOT
  conformed to in DRAFT10, now closed by DRAFT11 (Z22): (1) REQ-011 (i), rev
  24, the destination check of the pasted install; (2) `mash endpoint status`
  and its count, which rev 26 settles as no column; (3) the refusals for a
  connection the server refuses and for an unreachable server. Still not
  conformed to: (4) REQ-003's one line in `--make-key` output saying no
  fingerprint is shown because the CA came from the person's own setup is
  mashctl's text and is not in `install-make`; (5) the tests of AT-007 (a kill
  after each file, the temporary-name cases, the symlink and foreign-owner
  cases, "the server observes no authentication attempt") are behaviour, not
  output: only their refusal texts are shown.
- Contract revision 17 (`contract-0.5.6-cli-ux-rev17-draft.md`, the newest in
  the shared folder at the start of an earlier round of this work; it was read in
  full and compared
  line by line with revision 16): REQ-001's new exception (the address and
  config path in `credential install`'s first-install screen and in
  `endpoint create --public-key`'s hand-over), REQ-003's connection check and
  its refusals (a refused or unreachable server writes nothing), REQ-003's
  rule that `credential install` is a person's command, REQ-011 (iii)'s rule
  that an agent never installs the plugin, and the section on what an agent
  may run were applied to the help text, the MCP instructions and the blocks.
  Revision 16, read the same way before, was:
  REQ-001, REQ-003, REQ-004, REQ-009 to REQ-013, REQ-015, INV-003, INV-004, the
  exit-code table, AT-007 and AT-008 were read for every line about mash command
  output. Conformed to, line by line: REQ-003's synopsis (`--key --token
  --server --ca`), both flags required on a first install with exit 2 before
  anything is read, the later install using the configured server and CA with a
  given flag matching them, the token check against the account key the
  authenticated server serves (F15-2), the write order and the rerun that
  completes an interrupted install (F15-6), REQ-015's unacked count, the
  cooldown per endpoint from the last wake not surviving a restart (F15-5), and
  (F15-8, which rev 26 later reduced to no mailbox column). Lines of
  rev 17 this file did not conform to are closed by rev 21 (`no limit set`, the
  unacked count), except the refusal blocks for a connection the server refuses
  and for an unreachable server during `credential install` (the contract names
  the tests, no wording is given; not invented here). Rev 16: none that a
  target output can show. What a target
  cannot settle and is UNVERIFIED (section 7): REQ-015's "read with the caller's
  own credentials" for the owner's other endpoints (today's permissions are
  own-inbox only; Z6); that a rerun prints the full first-install screen (Z2);
  the exact fix of the trust screens now that `--creds` is gone (Z4). Lines the
  contract does not say and this file chose: Y5 to Y16 and Z1 to Z20
  (section 7).

Not checked: colours (mockups are plain, and no ANSI exists in either binary
today), the real widths with a long home directory (the mockups use `~`), that
`docs/skills/mash-nats-local/SKILL.md` and `scripts/nats-local-setup.sh` exist
(they do not in this tree), the `https://github.com/bogworks/mash/blob/main/
docs/install.md` address (no network check), the directory scope behind `mash
who` (N13), the token's expiry (Q13), whether a hook that exits 1 behaves as
intended in Claude Code (N6), that the server can say "deleted" (N11), and any
command on a live server: nothing here was run, these are mockups. Also not
checked: anything claude prints (UNVERIFIED, N16), that the 0.5.6 release
carries `mashctl-<os>-<arch>` and puts `SHA256SUMS` and both binaries in the
bundle (Y8), that moving only `config.json` or only `ca.pem` aside lets the same
install succeed (Y6), that an owner may read another endpoint's lease for `mash
endpoint status` (section 5b), and the mailbox row's order and the server rows,
which follow the status-views page.
