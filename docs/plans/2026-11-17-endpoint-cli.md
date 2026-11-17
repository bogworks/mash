# Plan: the `mashctl endpoint` command face for 0.5.5

Date: 2026-11-17. Status: design for review; no code. Scope: the command line for the model in
`docs/plans/2026-11-17-endpoint-model-owner-group-instance.md` (ADR 0145): how an endpoint is created, what happens to `init --local` and the README, and what replaces `add`, `reissue` and `retire`.
The model and the field names are that plan's. This note quotes its field names
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
   `policy` and `credential` (ADR 0144). Outcome words are the model plan's: `created`, `planned`,
   `unchanged`, `deleted`, `refused`, `partial`, `error`.
7. **Nothing prints a seed, a JWT or the contents of a creds file.** A path is printed; the file is mode 0600.
8. **mash works alone first.** Everything here runs against one local server made by `mashctl init --local`, with no
   other tool. Nothing in this note names a layer above mash.

## 2. `mashctl endpoint create`

```text
mashctl endpoint create [ADDRESS] [--owner O] [--group G] [--name N]
                        [--label k=v]... [--tool] [--note N] [--describe T] [--cap C]...
                        (--public-key FILE | --make-key) [--jwt-out FILE]
                        [--servers N] [--sys-creds FILE] [--dry-run] [--json]
```

The key is made on the endpoint's own machine. `mash key create --out design.seed` writes the seed there and the public
half to `design.pub`; only `design.pub` goes to the mashctl host. `--public-key FILE` takes that file: `create` signs the
key as the endpoint's user and writes the JWT to `--jwt-out FILE` (default `<owner>.<group>.<instance>.jwt` in the
working directory, mode 0600, never over an existing file). The JWT is never printed. The endpoint then runs
`mash credential create --seed design.seed --jwt alice.dev.design.jwt` to make its creds file.

`--make-key` is the explicit same-host path: `create` makes the key itself and writes the creds file, as `add` does today.
Its name and whether it stays are DEC-002 (pending). With neither flag `create` is refused: `key_missing`, exit 2, and
the message names both. A command line that gives both is a usage error (exit 2).

### 2.1 Naming the endpoint

Two forms, one meaning. Mixing them is a usage error (exit 2) unless they agree.

| You write | Owner | Group | Instance |
|---|---|---|---|
| `mashctl endpoint create --owner alice --group dev --name design` (canonical) | alice | dev | design |
| `mashctl endpoint create alice/dev/design` (shortcut) | alice | dev | design |
| `mashctl endpoint create --group dev --name design` | the configured owner | dev | design |
| `mashctl endpoint create design --group dev` | the configured owner | dev | design |
| `mashctl endpoint create design` | refused, exit 2: no group | | |
| `mashctl endpoint create --name design` | refused, exit 2: no group | | |

- The shortcut is three tokens separated by `/`, or one token with `--group`. Two tokens are refused: `alice/design`
  could be owner and instance or group and instance, and the command does not guess (`refused: two tokens are
  ambiguous; write alice/dev/design or --group dev design`, exit 2).
- There is no default group (the maintainer's ruling of 2026-11-17). A command with no group exits 2 with `refused: an
  endpoint needs a group; write --group dev or alice/dev/design`. The owner is the configured one (`MASH_OWNER` or
  `config.json`) when not given. There is no group config key: the group is always written, by flag, shortcut or `MASH_GROUP`.
- Each token follows the token rule of the model (`[a-z][a-z0-9-]*`, 1 to 32 characters, no `--`, no trailing dash;
  the whole address at most 64) and the name rule file. The flag is `--name` because that is the ruling; the JSON field
  is `instance` (section 8).
- What the command prints for the address is the model's form: `alice.dev.design` (dots, as it appears in a subject).
  What a person types is `alice/dev/design`. Both are shown in the output so that neither has to be converted by hand.

### 2.2 What it does

The order of operations is `add`'s (docs/mashctl.md, "Adding an endpoint"), on the new identity: check names and the
account key; sign the user key (with `--make-key`: mint a new key pair) with the owner-open permission set of the model plan, section 1.4; record it
in the issue record; provision the directory record and inbox; write the JWT to `--jwt-out` (with `--make-key`: write
`<owner>.<group>.<instance>.creds` in the config directory instead, mode 0600, never over an existing file); report. A failure at any step undoes the earlier ones. A run that is
killed leaves what `audit` reports, as `add` does today.

`--tool` adds the label `kind=tool`. `--servers` and `--sys-creds` are the several-servers flags `add` has. A flag value
that starts with `-` must be written `--flag=VALUE`.

### 2.3 Output

Text (a convenience; standard error carries progress, standard output carries this):

```text
$ mashctl endpoint create design --group dev --public-key design.pub
created endpoint alice/dev/design
  address  alice.dev.design
  jwt      ./alice.dev.design.jwt
next: on the endpoint's machine, run: mash credential create --seed design.seed --jwt alice.dev.design.jwt
```

`--dry-run` prints `would create endpoint alice/dev/design` and the same lines, and writes nothing:

```text
$ mashctl endpoint create alice/dev/design --public-key design.pub --dry-run --json
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
  "jwt": { "path": "./alice.dev.design.jwt", "written": false },
  "next_steps": [
    { "sender": "endpoint", "step": "credential", "action": "make the creds file on the endpoint's machine",
      "address": "alice.dev.design", "purpose": "use", "detail": "mash credential create --seed design.seed --jwt alice.dev.design.jwt" }
  ],
  "refusal": null
}
```

A created endpoint has `"outcome": "created"`, `"jwt": { ..., "written": true }` and `"user_public_key"` (with `--make-key`, `"creds"` replaces `"jwt"`). No field holds a seed or a JWT. A refusal has
`"outcome": "refused"` and `"refusal": { "code": "...", "detail": "..." }` with exit 1. Usage errors exit 2: `group_missing`, `key_missing`, a two-token or disagreeing form. Refusal codes (exit 1): `name_invalid`,
`name_rule`, `address_taken`, `key_retired` (the flat key is in the retired ledger), `file_exists` (the JWT or creds file), `public_key_issued`, `zone_record`,
`no_account_key`. An error (a server failure) has `"outcome": "error"` and exit 6; the text says what to run next.
`next_steps` has the shape of `policydirect.Result.next_steps` (`sender`, `step`, `action`, `address`, `purpose`,
`detail`), so one reader handles both.

### 2.4 Readable refusals (standard rule 13)

```text
mashctl: refused: alice.dev.design already exists in the directory.
  mashctl endpoint show alice/dev/design shows it; mashctl endpoint delete alice/dev/design removes it.
```

## 3. Moving existing endpoints

There is no `mashctl` command for it (the maintainer's ruling of 2026-11-17: no command-line arguments for one-time
use). 0.5.5 is a clean break from 0.5.0. An operator makes the new endpoints with `endpoint create` and removes the old
ones with a one-time script of their own, outside mash. The README says only that.

## 4. `reissue` and `retire`

| Today | After | Notes |
|---|---|---|
| `mashctl add NAME ...` | `mashctl endpoint create [ADDRESS] ...` | Section 2. Same flags except the naming ones. |
| `mashctl reissue NAME... [--widen]` | `mashctl endpoint reissue ADDRESS --public-key FILE [--widen] [--dry-run] [--json]` | One positional (standard rule 7). The bulk forms become flags: `--all --yes`, `--changed --yes`, and they take only `--make-key` (many public keys cannot arrive in one command); a bulk run lists first and acts only with `--yes`, as today. |
| `mashctl reissue --all` / `--changed` | `mashctl endpoint reissue --all --make-key` / `--changed --make-key` (with `--yes`) | Same. |
| `mashctl credential revoke` | not in 0.5.5 | A key is replaced by `endpoint reissue` and ended by `endpoint delete`. |
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

`endpoint delete` and `endpoint reissue` take the shortcut form as the positional address (`alice/dev/design`) and the
same `--servers` and `--sys-creds` flags `retire` and `reissue` have. Their outcomes keep today's words in the model's
vocabulary: `deleted`, `unchanged`, `refused`, `partial`, `error`; `reissued`, `unchanged`, `refused`, `partial`, `error`.

### 4.1 The reading commands that go with them

`mashctl endpoint list [--json]` (what `mashctl list` prints today, in the model's fields) and `mashctl endpoint show
ADDRESS [--json]` (one record in full, with which creds file belongs to it and its issue-record state) complete the noun. They change nothing. `show` exists so that a refusal can say "`mashctl endpoint show ... shows
it`". Their JSON uses the endpoint object of section 2.3.

## 5. `init --local`, the quick start and the README

`mashctl init --local` keeps its spelling and its flags in 0.5.5. The standard puts `init` under a noun; that rename is the
major revision's (section 6). What changes:

- Its last line becomes `Ready: ... Add an endpoint with: mash key create, then mashctl endpoint create ADDRESS --public-key FILE`.
- A fresh `init --local` writes no group, and none is assumed. The first `endpoint create design` makes `<owner>.dev.design`.
- It still never touches `config.json` beyond what it wrote, and still prints the config directory.

The full five-minute path, as the README will carry it (no account, one machine, group `dev`):

```sh
# 1. Install mash (a release download, verified) and mashctl (go install), as the README's Install section says.
# 2. A local NATS server, keys and credentials:
mashctl init --local --owner alice --start
# 3. Two endpoints, three commands each (the key is made on the endpoint's machine; here that is this one):
mash key create --out design.seed                                        # writes design.seed and design.pub
mashctl endpoint create alice/dev/design --public-key design.pub         # writes alice.dev.design.jwt
mash credential create --seed design.seed --jwt alice.dev.design.jwt     # writes the creds file
#    the same for review: mash key create --out review.seed; mashctl endpoint create alice/dev/review --public-key review.pub;
#    mash credential create --seed review.seed --jwt alice.dev.review.jwt
# 4. The plugin:
mash plugin export ~/.mash-plugin
claude plugin marketplace add ~/.mash-plugin
claude plugin install mash@mash
# 5. Two Claude Code sessions, one per endpoint (`MASH_OWNER` and `MASH_GROUP` complete the address; `MASH_GROUP` is required):
#      terminal 1:  MASH_GROUP=dev MASH_NAME=design claude --dangerously-load-development-channels plugin:mash@mash
#      terminal 2:  MASH_GROUP=dev MASH_NAME=review claude --dangerously-load-development-channels plugin:mash@mash
# 6. A third shell:
MASH_GROUP=dev MASH_NAME=design mash send review "hello"
MASH_GROUP=dev MASH_NAME=review mash inbox
# 7. Stop the local server:
kill "$(cat "$D/server.pid")"      # D is the server directory init --local printed
```

The README's other changes for 0.5.5:

- The words "agent" and "endpoint" are used as the glossary will fix them; `mashctl` is described as the tool that
  creates endpoints.
- One sentence in "Upgrading": 0.5.5 is a clean break from 0.5.0. Make new endpoints with `mashctl endpoint create` and
  remove the old ones. No migration command exists.
- The SAP departure (the first slot is the owner) in the README's SAP section, as the model plan says.
- `docs/mashctl.md` replaces "Adding an endpoint", "Re-issuing" and "Retiring an endpoint" with the sections of this note
  that apply.
- The first-launch notes, the third-shell label and the stop line that the README has now stay.

## 6. The command-line standard: where this conforms and where it does not

| Command | Shape | Standard | Status |
|---|---|---|---|
| `mashctl endpoint create ADDRESS` | noun verb positional | rules 1, 4, 7 | conforms |
| `mashctl endpoint delete ADDRESS` | noun verb positional | rules 1, 4, 7 | conforms (`retire` is not in the verb set; `delete` is) |
| `mashctl endpoint list`, `show ADDRESS` | noun verb | rules 1, 4 | conforms |
| `mashctl endpoint reissue ADDRESS` | noun verb positional | rules 1, 4, 7 (`reissue` is in the verb table of standard 1.1.1) | conforms |
| `mash key create`, `mash credential create` | noun verb | rules 1, 4 | conforms |
| `--all`, `--changed`, `--force`, `--yes` | flags | rule 9 (long names), 15 (`--yes`) | conforms |

Every new command uses a verb from the standard's table (1.1.1 added `reissue`, DEC-003).

The rest of mashctl (`init`, `list`, `audit`, `adopt`, `issued`, `inbox`, `limits`, `suspend`, `label`, `credential`) keeps
its 0.5.0 spelling in 0.5.5. They move under nouns at the major revision, with the standard's own table of renames; the
command-tree test of standard rule 22 is added then, and covers the whole tree. For 0.5.5 the command-tree test walks the
`endpoint`, `key` and `credential` nouns, with named exceptions only: usage line with at most one positional, `--json` on each read command and valid JSON, `--dry-run`
on each changing command and no state change under it, a bad flag exits 2, and `--help` carries an example.

## 7. Tests

- A table test of the naming forms in section 2.1 (the rows, the key-missing refusal, the no-group refusal, the two-token refusal, a mixed form that disagrees).
- Golden files for the text and the JSON of `create` (planned, created, each refusal code), `reissue`, `delete` and the removed-verb messages. The goldens are the contract that `--json` consumers read.
- A live proof through these commands, not the library: `init --local`, two `create` runs (one with `--public-key`),
  the two sessions exchanging a message, `reissue`, `delete`, the refusals, a killed run.
- `--dry-run` leaves the directory, the issue record and the config directory byte-identical.
- The exit codes of section 1 for each outcome.

## 8. Questions: settled and open

Settled with the model's owner on 2026-11-17:

1. **`--name`** is the flag everywhere. The JSON field and the prose say `instance`.
2. **Slash for typing, dots stored.** A person types `alice/dev/design`; the subject and the stored address are
   `alice.dev.design`. Both are shown in output.
3. **No migrate command.** Moving existing endpoints is a one-time operator script outside mash (ruling of
   2026-11-17); section 3.
4. **A session names its endpoint with three variables:** `MASH_NAME=design`, `MASH_OWNER` (as today) and `MASH_GROUP`
   (required, no default; its absence is a readable error at session start). One form, no `owner/group/instance` in `MASH_NAME`. The quick start in section 5 uses it.
5. **`provision`, `deprovision`, `set-meta` and `permissions`** survive as repairs only where they key by the new
   address (the flat key `owner_group_instance`). Any that takes a legacy name goes. The list is for the maintainer's
   lead to confirm.

Ruled by the maintainer:

6. **The endpoint makes its own key in 0.5.5.** `mash key create --out FILE` and `mash credential create --seed --jwt
   [--out]` run on the endpoint's machine; `endpoint create` requires `--public-key FILE` or the same-host `--make-key`.
7. **No default group, and no group config key** (section 2.1). `group_missing` is exit 2 everywhere.
8. **`reissue`** is in the standard's table (DEC-003, closed).

Open:

9. **The same-host flag** (`--make-key`, DEC-002): its name, and whether it stays.
