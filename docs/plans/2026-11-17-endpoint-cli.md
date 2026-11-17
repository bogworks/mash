# Plan: the `mashctl endpoint` command face for 0.5.5

Date: 2026-11-17. Status: design for review; no code. Scope: the command line for the model in
`docs/plans/2026-11-17-endpoint-model-owner-group-instance.md` (ADR 0145): how an endpoint is created, how the existing
ones are migrated, what happens to `init --local` and the README, and what replaces `add`, `reissue` and `retire`.
The model, the field names and the migration's account-side steps are that plan's. This note quotes its field names
(`owner`, `group`, `instance`, `address`, the outcome words) and does not redefine them.

Inputs: The maintainer's rulings of 2026-11-16 (the canonical form `--owner alice --group dev --name design`, the shortcut
`alice/dev/design`; commands are for an agent first, with shortcuts for people; "you should be able to read a CLI
command and its args and have some sense of what it means"), and the Bogworks command-line standard (version 1.0.1).

## 1. Rules this design follows

1. **mashctl makes endpoints.** The word "agent" is not used for what mashctl creates or lists. `agent` stays what it is
   in the model: the harness name in the service metadata.
2. **`--json` is the contract; the text is a convenience.** Every read command takes `--json` and prints one document to
   standard output and nothing else. Every changing command takes `--dry-run` and `--json`. Text may be reworded between
   releases; the JSON fields are versioned (`schema`).
3. **Shape: `mashctl <noun> <verb> [one positional] [flags]`** (standard rules 1, 7). The noun is `endpoint`. The one
   positional argument is the endpoint's address in its shortcut form. Scope and everything else are flags.
4. **A command reads as what it does.** The verb is the one in the standard's table where one fits (`create`, `delete`,
   `list`, `show`). Where none fits (section 6) the verb is the plain word and the gap is recorded.
5. **One spelling, no aliases** (standard rule 6). A removed spelling does nothing except exit 2 and name its
   replacement (standard rule 13).
6. **Results to standard output; progress, warnings and errors to standard error.** Exit codes: 0 success, 1 refused,
   2 wrong usage, 3 cancelled, 4 not authorized (never given standalone), 5 partial, 6 error: the same table as
   `policy` and `credential` (ADR 0144). Outcome words are the model plan's: `created`, `planned`, `migrated`,
   `unchanged`, `deleted`, `refused`, `partial`, `error`.
7. **Nothing prints a seed, a JWT or the contents of a creds file.** A path is printed; the file is mode 0600.
8. **mash works alone first.** Everything here runs against one local server made by `mashctl init --local`, with no
   other tool. Nothing in this note names a layer above mash.

## 2. `mashctl endpoint create`

```text
mashctl endpoint create [ADDRESS] [--owner O] [--group G] [--name N]
                        [--label k=v]... [--tool] [--note N] [--describe T] [--cap C]...
                        [--servers N] [--sys-creds FILE] [--dry-run] [--json]
```

### 2.1 Naming the endpoint

Two forms, one meaning. Mixing them is a usage error (exit 2) unless they agree.

| You write | Owner | Group | Instance |
|---|---|---|---|
| `mashctl endpoint create --owner alice --group dev --name design` (canonical) | alice | dev | design |
| `mashctl endpoint create alice/dev/design` (shortcut) | alice | dev | design |
| `mashctl endpoint create --group dev --name design` | the configured owner | dev | design |
| `mashctl endpoint create design` | the configured owner | `main` | design |
| `mashctl endpoint create --name design` | the configured owner | `main` | design |

- The shortcut has one token or three, separated by `/`. Two tokens are refused: `alice/design` could be owner and
  instance or group and instance, and the command does not guess (`refused: two tokens are ambiguous; write
  alice/main/design or --owner alice --name design`, exit 2).
- Defaults: the owner is the configured one (`MASH_OWNER` or `config.json`); the group is `main`. A solo developer writes
  `mashctl endpoint create design` and never meets a group.
- Each token follows the token rule of the model (`[a-z][a-z0-9-]*`, 1 to 32 characters, no `--`, no trailing dash;
  the whole address at most 64) and the name rule file. The flag is `--name` because that is the ruling; the JSON field
  is `instance` (section 8).
- What the command prints for the address is the model's form: `alice.main.design` (dots, as it appears in a subject).
  What a person types is `alice/main/design`. Both are shown in the output so that neither has to be converted by hand.

### 2.2 What it does

The order of operations is `add`'s (docs/mashctl.md, "Adding an endpoint"), on the new identity: check names and the
account key; mint the user (a new key pair) with the owner-open permission set of the model plan, section 1.4; record it
in the issue record; provision the directory record and inbox; write `<owner>.<group>.<instance>.creds` in the config
directory (mode 0600, never over an existing file); report. A failure at any step undoes the earlier ones. A run that is
killed leaves what `audit` reports, as `add` does today.

`--tool` adds the label `kind=tool`. `--servers` and `--sys-creds` are the several-servers flags `add` has. A flag value
that starts with `-` must be written `--flag=VALUE`.

### 2.3 Output

Text (a convenience; standard error carries progress, standard output carries this):

```text
$ mashctl endpoint create design
created endpoint alice/main/design
  address  alice.main.design
  creds    /home/alice/.config/mash/alice.main.design.creds
next: start a session as this endpoint (see the quick start)
```

`--dry-run` prints `would create endpoint alice/main/design` and the same lines, and writes nothing:

```text
$ mashctl endpoint create alice/dev/design --dry-run --json
```

```json
{
  "schema": "mashctl-endpoint@1",
  "outcome": "planned",
  "endpoint": {
    "owner": "alice",
    "group": "dev",
    "instance": "design",
    "address": "alice.dev.design",
    "key": "alice_dev_design",
    "labels": {},
    "caps": [],
    "description": "",
    "note": ""
  },
  "creds": { "path": "/home/alice/.config/mash/alice.dev.design.creds", "written": false },
  "next_steps": [
    { "sender": "operator", "step": "start", "action": "start a session as this endpoint",
      "address": "alice.dev.design", "purpose": "use", "detail": "creds /home/alice/.config/mash/alice.dev.design.creds" }
  ],
  "refusal": null
}
```

A created endpoint has `"outcome": "created"`, `"creds": { ..., "written": true }` and `"user_public_key"`. A refusal has
`"outcome": "refused"` and `"refusal": { "code": "...", "detail": "..." }` with exit 1. Codes: `name_invalid`,
`name_rule`, `address_taken`, `key_retired` (the flat key is in the retired ledger), `creds_exists`, `zone_record`,
`no_account_key`. An error (a server failure) has `"outcome": "error"` and exit 6; the text says what to run next.
`next_steps` has the shape of `policydirect.Result.next_steps` (`sender`, `step`, `action`, `address`, `purpose`,
`detail`), so one reader handles both.

### 2.4 Readable refusals (standard rule 13)

```text
mashctl: refused: alice.dev.design already exists in the directory.
  mashctl endpoint show alice/dev/design shows it; mashctl endpoint delete alice/dev/design removes it.
```

## 3. `mashctl endpoint migrate`

The model plan (section 2) gives the account-side steps. This is how an operator drives them.

```text
mashctl endpoint migrate [NAME | --all] [--group G] [--name N]
                         [--finish] [--force] [--yes] [--dry-run] [--json]
                         [--servers N] [--sys-creds FILE]
```

`NAME` is a legacy endpoint's name as `mashctl list` shows it today. The command changes one thing per step:

| Command | What it does |
|---|---|
| `mashctl endpoint migrate --all --dry-run` | Plans every legacy endpoint. Changes nothing. |
| `mashctl endpoint migrate design` | Step 1 for one endpoint: makes the new identity beside the old one (record, inbox, user, creds file). Revokes nothing. |
| `mashctl endpoint migrate design --finish` | Step 2 for one endpoint: revokes the old credentials, ends the bridge, deletes the drained legacy inbox and record. |
| `mashctl endpoint migrate --all --yes` | Step 1 for every legacy endpoint (it lists first, and acts only with `--yes`, as `reissue --all` does). |
| `mashctl endpoint migrate --all --finish --yes` | Step 2 for every endpoint that has moved. Lists any that has not, and leaves its record. |

- With no `NAME` and no `--all` the command exits 2 and says to name an endpoint or give `--all`. (The model plan has
  "no names plans all" as the default. The standard says a changing command changes nothing only under `--dry-run`
  (rule 11), and a bare `migrate` that does nothing is a trap for an agent that expects it to migrate. See section 8.)
- `--group G` sets the group for the endpoints in the run (default `main`). `--name N` sets the instance for one
  endpoint, for the case where the legacy name is longer than 32 characters. Without it such an endpoint is `refused`.
- `--finish` on an endpoint whose legacy inbox is not drained refuses with the count of unread messages, unless
  `--force` says to drop them.
- Order within a run: endpoints are handled one at a time in name order; a refusal on one does not stop the others.

### 3.1 Output

Per endpoint, one line; the last line is the count.

```text
$ mashctl endpoint migrate --all --dry-run
planned  design     -> alice/main/design   make the new identity; the old one keeps working
planned  review     -> alice/main/review   make the new identity; the old one keeps working
refused  a-very-long-legacy-endpoint-name-of-forty-one -> (instance over 32 characters; pass --name)
2 planned, 1 refused, 0 unchanged. Nothing was changed.
next: mashctl endpoint migrate design
```

```json
{
  "schema": "mashctl-migrate@1",
  "outcome": "partial",
  "step": "plan",
  "counts": { "planned": 2, "migrated": 0, "unchanged": 0, "refused": 1 },
  "endpoints": [
    { "legacy": "design", "owner": "alice", "group": "main", "instance": "design",
      "address": "alice.main.design", "outcome": "planned", "refusal": null },
    { "legacy": "review", "owner": "alice", "group": "main", "instance": "review",
      "address": "alice.main.review", "outcome": "planned", "refusal": null },
    { "legacy": "a-very-long-legacy-endpoint-name-of-forty-one", "owner": "alice", "group": "main",
      "instance": null, "address": null, "outcome": "refused",
      "refusal": { "code": "instance_too_long", "detail": "pass --name" } }
  ],
  "next_steps": [
    { "sender": "operator", "step": "migrate", "action": "make the new identity for design",
      "address": "alice.main.design", "purpose": "migrate", "detail": "mashctl endpoint migrate design" }
  ]
}
```

- `step` is `plan`, `migrate` or `finish`. The run's `outcome`: `planned` (every endpoint planned), `migrated`,
  `unchanged`, `refused` (every endpoint refused), `partial` (a mix of refused and the rest), `error`. Exit: 0 for
  `planned`, `migrated` and `unchanged`; 1 for `refused`; 5 for `partial`; 6 for `error`.
- Each endpoint has `outcome` (`planned`, `migrated`, `unchanged`, `refused`) and, after a step 1, `creds.path` and
  `user_public_key`. After step 1 the `next_steps` entry says where the new creds file is and that the endpoint's
  session must be restarted with it under the 0.5.5 binary.
- Refusal codes: `instance_too_long`, `name_invalid`, `name_rule`, `address_taken`, `key_retired`, `zone_record`
  (run `unzone` first), `legacy_inbox_undrained` (with `unread`), `not_moved`.

### 3.2 What the operator does, in order, and what breaks if endpoints move one at a time

The migration is the cut-over of every identity in a deployment, mine included. It is safe to do one endpoint at a time,
over days, because the model plan keeps old and new reaching each other for the whole window (the bridge, section 2.3
of that plan). In order:

1. **Put the 0.5.5 `mashctl` on the host that runs it.** Nothing changes for running endpoints.
2. **`mashctl endpoint migrate --all --dry-run`.** Read the plan. Fix each `refused` (a longer name needs `--name`; a
   zoned record needs `mashctl unzone` first).
3. **For one endpoint: `mashctl endpoint migrate NAME`.** The old session is untouched and keeps working.
4. **Restart that endpoint's session with the new creds file and the 0.5.5 binary, together.** Both are needed: a 0.5.0
   binary cannot serve the new form, and the new creds do not work for a 0.5.0 binary's subjects. Do not run the old
   session and the new one on the same endpoint at once; the lease allows one reader of the old name.
5. **Repeat 3 and 4** for every endpoint, in any order.
6. **`mashctl endpoint migrate --all --finish --yes`.** It revokes the old credentials of every endpoint that moved, and
   lists, without touching, any that did not. Run `mashctl audit` afterwards; it is clean when nothing is legacy.

What breaks, from the model plan and the order above:

- **Running `--finish` before an endpoint has moved** revokes the credentials its running session holds: the session
  loses its connection. Its mail is not lost (it waits in the legacy inbox) but `--finish` refuses while that inbox is
  not drained, so the command stops it unless `--force` is given. Never give `--force` for an endpoint that has not
  moved.
- **A session restarted on the 0.5.5 binary before step 3** has no new creds and cannot connect as a new-form endpoint.
- **While the window is open** the moved endpoints hold wider grants (the bridge) and `mashctl list` shows a moved and
  a legacy endpoint under different addresses. Closing the window removes both.
- **An endpoint nobody moves** keeps working until `--finish --all`, which lists it and leaves its record. It is not
  deleted by accident.
- **Several central hosts** each need the 0.5.5 `mashctl` before anyone runs `migrate`, because `migrate`, `create`
  and the old `add` write different record forms and a 0.5.0 `mashctl add` after step 3 makes a legacy record again.

## 4. `reissue` and `retire`

| Today | After | Notes |
|---|---|---|
| `mashctl add NAME ...` | `mashctl endpoint create [ADDRESS] ...` | Section 2. Same flags except the naming ones. |
| `mashctl reissue NAME... [--widen]` | `mashctl endpoint reissue ADDRESS [--widen] [--dry-run] [--json]` | One positional (standard rule 7). The bulk forms become flags: `--all --yes`, `--changed --yes`; a bulk run lists first and acts only with `--yes`, as today. |
| `mashctl reissue --all` / `--changed` | `mashctl endpoint reissue --all` / `--changed` (with `--yes`) | Same. |
| `mashctl retire NAME [--delete-creds] [--force] [--no-revoke]` | `mashctl endpoint delete ADDRESS [--delete-creds] [--force] [--no-revoke] [--dry-run] [--json]` | `delete` is the standard's verb for "remove an object from where this tool manages it". The behaviour is `retire`'s: revoke the key, remove the record and inbox, rename the creds file to `.retired-<time>` (or remove it with `--delete-creds`). |
| `mashctl deprovision NAME ...` | not an endpoint verb | It is the lower layer under `retire` and `retire` remains the safe path; the model plan's removal of the legacy record code leaves `deprovision` as a repair only where it keys by the new address (section 8). |

After 0.5.5 the old spellings do nothing:

```text
$ mashctl add design
mashctl: add was removed. Use: mashctl endpoint create design
$ echo $?
2
```

The same for `mashctl reissue` and `mashctl retire`, each naming its replacement (`endpoint reissue`, `endpoint delete`). They
are not aliases: they run nothing and exit 2.

`endpoint delete` and `endpoint reissue` take the shortcut form as the positional address (`alice/main/design`) and the
same `--servers` and `--sys-creds` flags `retire` and `reissue` have. Their outcomes keep today's words in the model's
vocabulary: `deleted`, `unchanged`, `refused`, `partial`, `error`; `reissued`, `unchanged`, `refused`, `partial`, `error`.

### 4.1 The reading commands that go with them

`mashctl endpoint list [--json]` (what `mashctl list` prints today, in the model's fields) and `mashctl endpoint show
ADDRESS [--json]` (one record in full, with whether it has moved, which creds file belongs to it, and its issue-record
state) complete the noun. They change nothing. `show` exists so that a refusal can say "`mashctl endpoint show ... shows
it`". Their JSON uses the endpoint object of section 2.3 plus `state` (`current`, `legacy`, `moved`) during the window.

## 5. `init --local`, the quick start and the README

`mashctl init --local` keeps its spelling and its flags in 0.5.5. The standard puts `init` under a noun; that rename is the
major revision's (section 6). What changes:

- Its last line becomes `Ready: ... Add an endpoint with: mashctl endpoint create NAME`.
- A fresh `init --local` writes no group. The first `endpoint create design` makes `<owner>.main.design`.
- It still never touches `config.json` beyond what it wrote, and still prints the config directory.

The full five-minute path, as the README will carry it (no account, one machine, no group named):

```sh
# 1. Install mash (a release download, verified) and mashctl (go install), as the README's Install section says.
# 2. A local NATS server, keys and credentials:
mashctl init --local --owner alice --start
# 3. Two endpoints:
mashctl endpoint create design
mashctl endpoint create review
# 4. The plugin:
mash plugin export ~/.mash-plugin
claude plugin marketplace add ~/.mash-plugin
claude plugin install mash@mash
# 5. Two Claude Code sessions, one per endpoint (`MASH_OWNER` and `MASH_GROUP` complete the address; the group defaults to `main`):
#      terminal 1:  MASH_NAME=design claude --dangerously-load-development-channels plugin:mash@mash
#      terminal 2:  MASH_NAME=review claude --dangerously-load-development-channels plugin:mash@mash
# 6. A third shell:
MASH_NAME=design mash send review "hello"
MASH_NAME=review mash inbox
# 7. Stop the local server:
kill "$(cat "$D/server.pid")"      # D is the server directory init --local printed
```

The README's other changes for 0.5.5:

- The words "agent" and "endpoint" are used as the glossary will fix them; `mashctl` is described as the tool that
  creates endpoints.
- An "Upgrading from 0.5.0" paragraph: put 0.5.5 `mashctl` on the central host, run `mashctl endpoint migrate --all
  --dry-run`, and follow section 3.2. It links the operations page for the full order.
- The SAP departure (the first slot is the owner) in the README's SAP section, as the model plan says.
- `docs/mashctl.md` replaces "Adding an endpoint", "Re-issuing" and "Retiring an endpoint" with the sections of this note
  that apply; `docs/operations.md` gets the migration order.
- The first-launch notes, the third-shell label and the stop line that the README has now stay.

## 6. The command-line standard: where this conforms and where it does not

| Command | Shape | Standard | Status |
|---|---|---|---|
| `mashctl endpoint create ADDRESS` | noun verb positional | rules 1, 4, 7 | conforms |
| `mashctl endpoint delete ADDRESS` | noun verb positional | rules 1, 4, 7 | conforms (`retire` is not in the verb set; `delete` is) |
| `mashctl endpoint list`, `show ADDRESS` | noun verb | rules 1, 4 | conforms |
| `mashctl endpoint reissue ADDRESS` | noun verb positional | rule 4: `reissue` is not in the verb set | gap |
| `mashctl endpoint migrate NAME` | noun verb positional | rule 4: `migrate` is not in the verb set | gap |
| `--all`, `--changed`, `--finish`, `--force`, `--yes` | flags | rule 9 (long names), 15 (`--yes`) | conforms |

Two verbs are outside the standard's table: `reissue` and `migrate`. The standard's set grows only by a change to the
standard, and it lets a command break a rule for one revision when the decision is recorded and flagged. The
recommendation, in order: (1) ask for `reissue` and `migrate` to be added to the table (one makes a new credential for an
existing object; the other is a one-time cut-over); (2) until then ship them as written, record the exception in the
ADR for this release, and let it expire at mash's major revision. The alternatives that conform now are worse to read:
`endpoint create --replace` for `reissue` hides a revocation under a creating verb, and `endpoint apply` for `migrate`
does not take a definition file.

The rest of mashctl (`init`, `list`, `audit`, `adopt`, `issued`, `inbox`, `limits`, `suspend`, `label`, `credential`) keeps
its 0.5.0 spelling in 0.5.5. They move under nouns at the major revision, with the standard's own table of renames; the
command-tree test of standard rule 22 is added then, and covers the whole tree. For 0.5.5 the command-tree test walks the
`endpoint` noun only: usage line with at most one positional, `--json` on each read command and valid JSON, `--dry-run`
on each changing command and no state change under it, a bad flag exits 2, and `--help` carries an example.

## 7. Tests

- A table test of the naming forms in section 2.1 (the five rows, the two-token refusal, a mixed form that disagrees).
- Golden files for the text and the JSON of `create` (planned, created, each refusal code), `migrate` (plan, step 1,
  step 2, partial) and the removed-verb messages. The goldens are the contract that `--json` consumers read.
- The live proof of the model plan (its section 2.4) driven through these commands, not the library: two legacy
  endpoints, `--dry-run`, step 1, the move, step 2, the refusals, a killed run.
- `--dry-run` leaves the directory, the issue record and the config directory byte-identical.
- The exit codes of section 1 for each outcome.

## 8. Questions: settled and open

Settled with the model's owner on 2026-11-17:

1. **`--name`** is the flag everywhere, `migrate` included (it names the new instance when a legacy name is longer than
   32 characters). The JSON field and the prose say `instance`.
2. **Slash for typing, dots stored.** A person types `alice/main/design`; the subject and the stored address are
   `alice.main.design`. Both are shown in output.
3. **A bare `migrate` exits 2.** The plan is `--all --dry-run`.
4. **A session names its endpoint with three variables:** `MASH_NAME=design`, `MASH_OWNER` (as today) and `MASH_GROUP`
   (default `main`). One form, no `owner/group/instance` in `MASH_NAME`. The quick start in section 5 uses it.
5. **`provision`, `deprovision`, `set-meta` and `permissions`** survive as repairs only where they key by the new
   address (the flat key `owner_group_instance`). Any that takes a legacy name goes. The list is for the maintainer's
   lead to confirm.

Open:

6. **The credential carve-out.** The model plan offers the property of #698 (the endpoint makes its key and only the
   public key reaches `mashctl`) in 0.5.5 without a list set, for 1.5 days. If taken, `endpoint create` gets
   `--public-key FILE` and writes no creds file, and the `credential install` verb of the credential CLI design
   completes the path. If not, `endpoint create` mints the user on the central host, as `add` does today.
7. **The two verbs outside the standard** (section 6): add them to the table, or record the exception.
8. **A flag-day alternative.** The model plan's section 6 costs a migration without the bridge. If that is chosen the
   `migrate` flags may change (`--all --apply` and `--finish --all` stay); this note follows the bridge version until then.
