# The command tree of mash and mashctl, and the move to `~/.mash`

## 1. Status and scope

**Status: a proposal for review.** This document changes no code. It maps every command of `mash` and `mashctl` as the
code has them today to a place in the tree that the adopted CLI standard asks for, and it says how an existing install
and an installed plugin get there.

- **The standard.** `cli/STANDARD.md` 1.0.1 (adopted 2026-11-12, master-planning decision 215): `<tool> <noun> <verb>
  [name] [flags]`, a fixed verb set, no aliases, `--json` on reads, `--dry-run` on changes, exit codes 0 to 4, and one
  per-user directory `~/.<tool>` shared by `mash` and `mashctl`. Rules are cited below as "rule N". The standard says
  mash changes "at its major revision, where the old spelling stops working (rule 6) and the release notes list each
  renamed command".
- **"Today"** means this repository at `origin/main` (merge of PR #514). Where a command exists only on a branch that
  has not merged, the table says so (section 3.3 lists those branches). Everything was read from the code, the docs and
  the plugin; nothing was run.
- **Out of scope:** the format of configuration files (`configuration-format/STANDARD.md`, which this author did not
  have: see section 11), how reference pages are generated, and tools we do not own.
- **One PR, one file.** This PR adds only this file. Any ADR the work needs (section 9.6) comes with the PR that makes
  the change, as `AGENTS.md` asks.

## 2. The rules that decide the shape

| Rule | What it forces here |
|---|---|
| 1, 2 | Every command is `<tool> <noun> <verb>`; at most one sub-noun. `send`, `read`, `who`, `add`, `retire`, `audit` and the rest lose their top-level place. |
| 3 | Top level keeps only `version`, `help`, `completion` and a daemon's `run`/`ensure`/`serve`. `mash serve` stays. `mash hook` and `mash status` do not qualify (see OP-2, and `mash endpoint status` in 3.1). `completion` is new in both tools. |
| 4 | Verbs come from the fixed set. `send`, `inspect`, `set-meta`, `reissue`, `suspend` and others are outside it: each is placed in the set where it fits, or listed in section 10 where it does not. |
| 5 | `serve`, `run`, `ensure` appear only at the top level of a daemon. Only `mash` is a daemon here. |
| 6 | No aliases, including the old spelling of a renamed command. An error that names the new spelling is not an alias: it runs nothing (section 9.1). |
| 7 | One positional, the object's name; scope is a flag. Several things today are variadic or take two positionals (`send <to> text...`, `reissue NAME...`, `label NAME k=v...`, `limits [name...]`): they move to flags (OP-9). |
| 9 | Every flag has a long name; booleans are flags. Today's `-h` is the only short form and stays optional. |
| 10, 11 | `--json` on every read command, `--dry-run` on every changing command. Section 6 lists the gaps. |
| 14 | Exit codes 0, 1, 2, 3, 4. Today's 3 means "not suspended" and "could not complete", which the standard reserves for "cancelled" (section 5). |
| 15 | Prompts only on a terminal, each with a flag. Today no command prompts (a search for terminal checks and `y/N` finds none), so nothing to convert. |
| 16 | Every level answers `--help` and `help`, with a usage line, one line, and an example. Today only `mash VERB --help` for seven verbs does. |
| 17 | Configuration is YAML. Today it is `config.json`. |
| 18, 19, 20, 21 | One directory `~/.mash` with `config/`, `state/`, `cache/`; `MASH_HOME` moves it and nothing else does; `mash` and `mashctl` share it; nothing is written in a work tree unless the user names the path. |
| 22 | A test walks the whole command tree. The repository has none today (section 9.5). |

## 3. Nouns and verbs: the new tree

Each line gives the usage and what it replaces (the row id points into the table in section 4). A command marked
`OPEN` needs a decision in section 10 before it can be built; the line shows the best fit, not a settled spelling.
Every read command also takes `--json`, every changing command also takes `--dry-run`, as section 6 requires; those
flags are written only where today's command already has them or where they change the meaning.

### 3.1 `mash`

```text
mash
  version                      mash version [--json]                                   M3
  help [command]               mash help [command]    (and --help at every level)      M2
  completion                   mash completion bash|zsh|fish                           new
  serve                        mash serve                                              M4   daemon verb, rule 5; no change
  hook                         mash hook [--dry-run]                                   M5   OPEN OP-2: recorded exception for one revision
  message
    send <to>                  mash message send <to> (--text TEXT | --file FILE | --file -)
                                  [--urgent] [--kind K] [--id KEY] [--dry-run]          M6   OPEN OP-1 (verb)
    list                       mash message list [--limit N] [--after SEQ] [--json]    M8
    show <seq>                 mash message show <seq> [--json]                        M7
  prompt
    send <to>                  mash prompt send <to> --text TEXT [--dry-run]           M10  OPEN OP-1 (verb)
  endpoint
    list                       mash endpoint list [--long | --names] [--match T] [--name SUB] [--owner O]
                                  [--role SUB] [--label K[=V]]... [--cap C]... [--online | --offline] [--json]   M9
    status                     mash endpoint status [--json]                           M11  this session's endpoint (rule 8: says which)
  plugin
    create <dir>               mash plugin create <dir> [--dry-run]                    M12  OPEN OP-6 (the standard's table says `print`)
```

`mash admin ...` (M13 to M27) is removed, not moved: it has been deprecated since `mashctl` took the verbs. Every verb
gets the "renamed" error pointing at `mashctl` (section 9.1).

### 3.2 `mashctl`

```text
mashctl
  version                      mashctl version [--json]                                C2
  help [command]               mashctl help [command]                                  C3
  completion                   mashctl completion bash|zsh|fish                        new
  directory
    init                       mashctl directory init [--dry-run]                      C4   the directory and lease buckets
  server
    init                       mashctl server init [--dir D] [--owner O] [--port P] [--start] [--dry-run]    C5   was `init --local`
    adopt <url>                mashctl server adopt <url> --operator-key F --account-key F --sys-creds F
                                  [--account N] [--admin-creds F | --mint-admin] [--owner O] [--dry-run]     C23  was `adopt --server URL`
  endpoint
    list                       mashctl endpoint list [--json]                          C18
    create <name>              mashctl endpoint create <name> [--label k=v]... [--tool] [--note N]
                                  [--describe T] [--cap C]... [--dry-run]               C24  was `add`
    adopt <name>               mashctl endpoint adopt <name> --user U --account A [--note N] [--role R] [--host H]
                                  [--label k=v]... [--describe T] [--cap C]... [--dry-run]    C9  was `provision`
                                  (three-token form: --agent A [--owner O] [--alias OWNER/NAME])      C10
    <update> <name>            mashctl endpoint <update> <name> [--role R] [--host H] [--label k=v]...
                                  [--describe T] [--cap C]... [--uncap C]... [--dry-run]   C11, C29, C30   OPEN OP-3
    delete <name>              mashctl endpoint delete <name> [--user U | --drained-to SEQ --suspension ID]
                                  [--delete-creds] [--force] [--skip-revoke] [--dry-run]  C16, C17, C28   OPEN OP-4 (one command for two)
    <migrate> <name>           mashctl endpoint <migrate> <name> [--sys-creds F] [--dry-run]   C31   OPEN OP-3 (transitional)
  credential
    list                       mashctl credential list [--json]                        C21  was `issued list`
    status                     mashctl credential status [--sys-creds F] [--servers N] [--json]   C22  was `issued check`
    create <name>              mashctl credential create (<name> | --changed | --all) [--dry-run]   C25 to C27  was `reissue`   OPEN OP-4
  inbox
    stop <name>                mashctl inbox stop <name> [--dry-run]                   C13  was `suspend`    OPEN OP-5
    start <name>               mashctl inbox start <name> [--dry-run]                  C14  was `resume`
    status <name>              mashctl inbox status <name> [--json]                    C15  was `suspended`
    show <address>             mashctl inbox show <address> [--json] [--legacy] [--from SEQ] [--to SEQ] [--limit N]   C41  was `inbox inspect` (unmerged branch)
  limit
    show                       mashctl limit show [--json]                             C7   was `limits --show`
    apply                      mashctl limit apply [--endpoint NAME]... [--dry-run]    C6   was `limits [name...]`   OPEN OP-9
  permission
    print <name>               mashctl permission print <name> [--nsc]                 C8   was `permissions`
  audit
    show                       mashctl audit show [--strict] [--creds-dir DIR] [--compare EARLIER.json] [--json]   C19, C20   OPEN OP-7 (`show` or `list`)
  policy                       no verbs until the list-set verbs land (ADR 0102); the group is empty today on the zone-removal branch   C40
```

### 3.3 Moving target

The tree above is built from `origin/main`. Work in flight changes the starting point, and the order of landing decides
which rows exist:

| Branch (not merged) | What it changes in the command set |
|---|---|
| `pr/ctl/zone-removal-z1` to `z4` (ADR 0091) | Removes `set-zone` and the `--zone` flags (Z1); deletes the policy file workflow: the whole `policy check/test/explain/show/apply/diff/drift/repair` group, `--policy`, `--sync`; `label`, `unlabel`, `add`, `reissue` and `retire` act on the directory record instead (Z3); adds `reissue --all` and `unzone`; leaves `mashctl policy` as an empty group (Z4). |
| `pr/ctl/inbox-inspect` | Adds `mashctl inbox inspect <address> [--json] [--legacy] [--from SEQ] [--to SEQ] [--limit N]`, exit 0 / 1 refusal / 2 usage / 3 could not complete. |
| `issue-service-work`, `pr/core/issue-service` | Add `mashctl issue serve [--list-set FILE] [--registry FILE]` and `mashctl wrangler init --test-account | add --host H --group G --key PUB | list | remove --host H --group G`. |
| `pr/mash/pi-adapter` and siblings | `MASH_HOST` takes `pi` besides `claude`; the config struct names the state directory `StateHome` there. No new command. |

Recommendation: land the zone-removal chain first (it deletes about a third of the `mashctl` surface, so there is less to
rename), then this work. Rows C12 and C32 to C39 are marked "removed by the Z chain": they are mapped here so the
mapping is complete if the order is different, but they get no new spelling.

## 4. The mapping table

Ids: **M** = `mash`, **A** = `mash admin`, **L** = the plugin launcher, **T** = an MCP tool, **C** = `mashctl`.
"Where" gives the file that defines the command today. `json` and `dry` say whether the command has `--json` or
`--dry-run` today ("-" = none, "yes" = has it). Exit codes are in section 5.

### 4.1 `mash` (`cmd/mash/main.go` dispatch, `cmd/mash/cli.go`)

| Id | Current | New | Where | json / dry | Notes |
|---|---|---|---|---|---|
| M1 | `mash` (no arguments) | `mash --help` text on stderr, exit 2 | main.go `runLookup` | - | Unchanged: exit 2 is rule 14. The usage text changes to the noun list (the standard's example). |
| M2 | `mash help`, `mash -h`, `mash --help` | `mash help [command]`, `mash --help`, and `--help` / `help` at every noun and verb | main.go | - | Today `mash VERB --help` prints a usage line for `status version send read inbox who prompt` only (`verbUsage`); `admin`, `hook`, `serve`, `plugin` have none. Rule 16 asks for a one-line description and an example at every level. |
| M3 | `mash version` | `mash version [--json]` | main.go | - / n/a | Stays top level (rule 3). Takes no arguments and refuses extras today. The plugin launcher and CI run it (L1, section 8): its first line `mash <version> (commit <rev>)` is a compatibility contract (ADR 0034). `--json` is new and must leave the text line alone. |
| M4 | `mash serve` | `mash serve` | main.go, serve.go | - / - | Conforms (rule 5). Started by the plugin as the MCP server (section 8). Takes no flags today. Rule 11 would want `--dry-run` on a changing command: OP-10 proposes an exemption for daemon verbs. |
| M5 | `mash hook` | `mash hook` kept for one revision as a recorded exception (OP-2) | hook.go | - / - | Not allowed at top level by rule 3. Reads the host's hook input on stdin, prints the spooled mail, marks it drained; always exits 0 by design. Spelled in the installed plugin's `hooks.json`. |
| M6 | `mash send <to> [--urgent] [--kind K] [--id KEY] [text... \| --file F \| -]` | `mash message send <to> (--text TEXT \| --file FILE \| --file -) [--urgent] [--kind K] [--id KEY] [--dry-run]` | cli.go `case "send"` | - / - | Verb `send` is not in the set (OP-1). Text words move to `--text` (rule 7: one positional); `-` (stdin) becomes `--file -`. `--urgent`, `--kind blocker\|question\|task\|result\|fyi`, `--id KEY` keep their names and meaning (ADR 0029, 0027, 0031). Prints `sent #N from X to Y` on stdout. `--dry-run` is new: resolve the recipient and print what would be sent, send nothing. |
| M7 | `mash read <seq>` | `mash message show <seq> [--json]` | cli.go `case "read"` | - / n/a | `#12` and `12` both accepted today; keep. Output is `render.One`, a pinned text contract (ADR 0034). `--json` is new. |
| M8 | `mash inbox [--limit N] [--after SEQ]` | `mash message list [--limit N] [--after SEQ] [--json]` | cli.go `inboxArgs` | - / n/a | Default limit 20, capped at 1000 (`cliMaxLimit`); `--after` pages oldest first. Output is `render.Line`, pinned. Acts on the inbox of `MASH_NAME` and says which (rule 8). `--json` is new. |
| M9 | `mash who [--long] [--json] [--names] [--match TEXT] [--name SUB] [--owner O] [--role SUB] [--label K[=V]]... [--cap C]... [--online\|--offline]` | `mash endpoint list` with the same flags | whofilter.go `ParseWhoFlags` | yes (`--json`) / n/a | Filters and `--json` are a stable contract (ADR 0049): keep every flag, keep the JSON field names and the default line format unchanged. `--cap` exists in `whoUsage` but is missing from the long help in `usage`; fix when the help is rewritten. `--json` and `--names` exclude each other. The MCP `who` tool shares `endpoint.WhoFilter` and keeps its name (T4). |
| M10 | `mash prompt <to> <text...>` | `mash prompt send <to> --text TEXT [--dry-run]` | cli.go `case "prompt"` | - / - | A live, signed SAP prompt; prints the reply. Verb outside the set (OP-1). Text words become `--text`. |
| M11 | `mash status` | `mash endpoint status [--json]` | status.go `statusCmd` | - / n/a | A report of what a session would see (name, config, creds, channel allowlist); connects to nothing. Not a rule-3 top-level verb (mash is a mesh, not a thing with its own status), so it moves under the one noun it describes. Takes no arguments. Text output is parsed by people and by docs (`mash status` appears 12 times in operations.md). `--json` is new. The MCP `status` tool (T7) renders the same report. |
| M12 | `mash plugin export <dir>` | `mash plugin create <dir> [--dry-run]` (OP-6) | plugin.go `pluginCmd` | - / - | Runs before any config exists (it is part of installing). Writes `.claude-plugin/marketplace.json` and `plugin/...` files atomically into `<dir>`, prints the absolute dir. It writes files, so it is not `print` (rule 4: `print` writes to standard output "without installing it"), though the standard's table says `print`. The named `<dir>` satisfies rule 21. |

### 4.2 `mash admin` (`cmd/mash/admin.go`, `internal/ctl/ctl.go`)

`mash admin` runs the `mashctl` verb set under the old name, prints `mash admin is deprecated and will be removed; use mashctl
(same verbs, e.g. mashctl list)` on stderr, and refuses when `agent_token` is set unless the verb is the three-token
form of `provision` or `deprovision`. It does not carry `audit`, `adopt`, `add`, `reissue`, `retire`, `label`,
`unlabel`, `issued`, `policy`: those answer `mash: unknown admin command "X"`, exit 2. Plan: remove `mash admin`
completely. Every row gets the "renamed" error with the `mashctl` spelling, exit 2.

| Id | Current | New | Notes |
|---|---|---|---|
| A1 | `mash admin` (no verb) | error: `mash admin was removed; use mashctl` , exit 2 | Prints the usage today, exit 2. |
| A2 | `mash admin init` | `mashctl directory init` | Same as C4. |
| A3 | `mash admin init --local ...` | `mashctl server init ...` | Same as C5. `mash admin init --local` works today through `ctl.Run`. |
| A4 | `mash admin limits [name...]` | `mashctl limit apply` | C6. |
| A5 | `mash admin limits --show` | `mashctl limit show` | C7. |
| A6 | `mash admin permissions <name> [--nsc]` | `mashctl permission print <name> [--nsc]` | C8. `scripts/quickstart-synadia.sh` calls this and parses its JSON (section 8). |
| A7 | `mash admin provision <name> ...` (and the three-token forms) | `mashctl endpoint adopt <name> ...` | C9, C10. Called by `scripts/quickstart-synadia.sh`. |
| A8 | `mash admin set-meta <name> ...` | `mashctl endpoint <update> <name> ...` | C11. |
| A9 | `mash admin set-zone <name> <zone>` | none (removed by ADR 0091) | C12. |
| A10 | `mash admin suspend <name>` | `mashctl inbox stop <name>` | C13. |
| A11 | `mash admin resume <name>` | `mashctl inbox start <name>` | C14. |
| A12 | `mash admin suspended <name>` | `mashctl inbox status <name>` | C15. |
| A13 | `mash admin deprovision <name> [--user U \| --drained-to SEQ --suspension ID]` | `mashctl endpoint delete <name> ...` | C16, C17. Here (unlike `mashctl`) `RevokeUsers` is nil, so it only advises that the user is not revoked. Called by `scripts/quickstart-synadia.sh` for cleanup. |
| A14 | `mash admin list` | `mashctl endpoint list` | C18. Called by `scripts/quickstart-synadia.sh`. |
| A15 | `mash admin <add, audit, adopt, reissue, retire, label, unlabel, issued, policy>` | `mashctl ...` (see C-rows) | Already "unknown admin command", exit 2. Gets the same renamed error. |

### 4.3 The plugin launcher and the MCP tools

The launcher `plugin/scripts/mash` is a shell program, the first thing the plugin runs. Its first argument is a `mash`
command, except `__prefetch`.

| Id | Current | New | Notes |
|---|---|---|---|
| L1 | `sh scripts/mash __prefetch` (SessionStart hook) | unchanged | Not a `mash` command: the launcher answers it itself and exits 0 without running a binary. A hidden name for the launcher's own use; whether rule 16 applies to a launcher is OP-12. The launcher also runs `<downloaded binary> version` to check a pinned download, so `mash version` must keep printing the version. |
| T1 | MCP tool `send` | unchanged | Not shell commands (serve.go lines 629 to 771). Names are what agents call; renaming would break every session's instructions and cost nothing to keep. CLI and MCP `who` share one filter (ADR 0049), so the CLI can rename without touching the tool. |
| T2 | MCP tool `read` | unchanged | |
| T3 | MCP tool `inbox` | unchanged | |
| T4 | MCP tool `who` | unchanged | Same fields as `mash endpoint list --json`. |
| T5 | MCP tool `reply` | unchanged | Answers a live prompt by `request_id`; has no CLI form. |
| T6 | MCP tool `version` | unchanged | The inert server (no `MASH_NAME`) has only `status` and `version`. |
| T7 | MCP tool `status` | unchanged | |

### 4.4 `mashctl` (`cmd/mashctl/main.go` dispatch, `internal/ctl/ctl.go` for the common verbs)

| Id | Current | New | Where | json / dry | Notes |
|---|---|---|---|---|---|
| C1 | `mashctl` (no arguments) | usage on stderr, exit 2 | main.go | - | Unchanged. |
| C2 | `mashctl version` | `mashctl version [--json]` | main.go | - | Prints `mashctl <version>`. |
| C3 | `mashctl help`, `-h`, `--help` | `mashctl help [command]`, `--help` at every level | main.go, `ctl.Usage` | - | Today only the whole listing; per-verb usage appears only on a usage error. |
| C4 | `mashctl init` | `mashctl directory init [--dry-run]` | ctl.go | - / - | Creates the directory and lease buckets or raises their caps; records the admin user in the issue record. Any argument is a usage error today (`initUsage`). |
| C5 | `mashctl init --local [--dir D] [--owner O] [--port P] [--start]` | `mashctl server init [--dir D] [--owner O] [--port P] [--start] [--dry-run]` | ctl/local.go | - / - | Writes a loopback operator-mode NATS server, keys and creds for one developer; `--start` also starts it and creates buckets. `--dir` default `<config dir>/server` becomes `~/.mash/state/server` (section 7). `--local` is dropped: the noun says it. Offline unless `--start`. |
| C6 | `mashctl limits [name...]` | `mashctl limit apply [--endpoint NAME]... [--dry-run]` | ctl.go | - / - | Raises existing inboxes to the configured limits, never lowers. Variadic names become a repeatable scope flag (rule 7). Verb choice is OP-9. |
| C7 | `mashctl limits --show` | `mashctl limit show [--json]` | ctl.go | - / n/a | Offline. Prints five `key value` lines. `--json` is new. |
| C8 | `mashctl permissions <name> [--nsc]` | `mashctl permission print <name> [--nsc]` | ctl.go | JSON by default / n/a | Prints the legacy wide permission set as JSON, or with `--nsc` one `nsc` flag per line; a note goes to stderr. Offline. `--json` is accepted as the default; it excludes `--nsc` (OP-8). |
| C9 | `mashctl provision <name> --user U --account A [--note N] [--role R] [--host H] [--label k=v]... [--describe T] [--cap C]... [--zone Z]` | `mashctl endpoint adopt <name> --user U --account A ...` | ctl.go `case "provision"` | - / - | Records an endpoint for a NATS user made elsewhere (`nsc`): creates the directory record and inbox. `--zone` removed by ADR 0091. Fits `adopt` ("take an existing object under management"). |
| C10 | `mashctl provision` three-token forms: `<name> --agent A [--owner O] [--alias OWNER/NAME]`, or `agent.owner.name` | `mashctl endpoint adopt` with the same forms | ctl/newform.go | - / - | Needs the `agent_token` setting; with it set, only these forms of `provision` and `deprovision` run (`NewFormRequested`). Name syntax is the naming grammar's. |
| C11 | `mashctl set-meta <name> [--role R] [--host H] [--label k=v]... [--describe T] [--cap C]... [--uncap C]...` | `mashctl endpoint <update> <name> ...` | ctl.go | - / - | Verb outside the set (OP-3). A value of `-` clears a field; `--label k=-` clears a label; `--cap -` clears all. |
| C12 | `mashctl set-zone <name> <zone>` | none | ctl.go | - / - | Exists on `origin/main` only; removed by Z1 (ADR 0091). |
| C13 | `mashctl suspend <name>` | `mashctl inbox stop <name> [--dry-run]` | ctl.go | - / - | Parks new mail; prints id and `last_seq`; credentials stay valid. OP-5. |
| C14 | `mashctl resume <name>` | `mashctl inbox start <name> [--dry-run]` | ctl.go | - / - | |
| C15 | `mashctl suspended <name>` | `mashctl inbox status <name> [--json]` | ctl.go | - / n/a | Exit code today: 0 suspended, **3 not suspended**, 1 error. New: 0 whenever it could answer, the state in the output. A script that tests `$? -eq 3` breaks: release-note item. |
| C16 | `mashctl deprovision <name> [--user U] [--no-revoke]` | `mashctl endpoint delete <name> [--user U] [--skip-revoke]` | ctl.go | - / - | Revokes the endpoint's user in the account JWT (system creds, `MASH_SYS_CREDS`, `MASH_SERVERS`), then removes record and inbox. `--no-revoke` is called "a temporary flag name" in `NoRevokeWarning` and docs/mashctl.md: rename now (OP-4). An unknown name prints `no endpoint named NAME`, exit 1. |
| C17 | `mashctl deprovision <name> --drained-to SEQ --suspension ID` | `mashctl endpoint delete <name> --drained-to SEQ --suspension ID` | ctl.go | - / - | The guarded form after `suspend`: removes only if still that suspension at that `last_seq`. The two flags go together. |
| C18 | `mashctl list` | `mashctl endpoint list [--json]` | ctl.go | - / n/a | Prints tab-separated key, user, created, note. Any argument is a usage error. `--json` is new. |
| C19 | `mashctl audit [--json] [--strict] [--creds-dir DIR]` | `mashctl audit show [--json] [--strict] [--creds-dir DIR]` | audit.go | yes / n/a | Read only: lists directory records, inbox streams, leases, creds files, orphans, legacy drain state. `--strict` exits 1 on findings. Exit codes need a decision (OP-10). |
| C20 | `mashctl audit --compare EARLIER.json` | `mashctl audit show --compare EARLIER.json` | audit_compare.go | yes / n/a | Reads an earlier `--json` report; adds `moved`, `vanished`, `recreated`. The report's `version` field (`auditVersion`) is a contract and does not change with the command name. |
| C21 | `mashctl issued list [--json]` | `mashctl credential list [--json]` | issued.go | yes / n/a | The issue record: every user JWT mashctl issued. `issued` is not a noun; the objects are the issued credentials. |
| C22 | `mashctl issued check [--json] [--sys-creds FILE] [--servers N]` | `mashctl credential status [--sys-creds FILE] [--servers N] [--json]` | issued.go | yes / n/a | Compares connected user keys with the record. Exit today: 0 clean, **1 a finding**, **2 could not complete** (collides with usage 2). `check` is not in the verb set; `status` ("live state, or a summary of all with no name") fits. |
| C23 | `mashctl adopt --server URL --operator-key F --account-key F --sys-creds F [--account N] [--admin-creds F \| --mint-admin] [--owner O] [--dry-run]` | `mashctl server adopt <url> ...` | adopt.go | - / yes | Takes over a server already run: checks authority, keeps the keys, writes config. The URL becomes the positional (it is the object's name). Changes nothing on the server except `--mint-admin` recording the admin user. Already has `--dry-run`. |
| C24 | `mashctl add NAME [--label k=v]... [--tool] [--zone Z] [--policy FILE] [--note N]` | `mashctl endpoint create <name> [--label k=v]... [--tool] [--note N] [--describe T] [--cap C]... [--dry-run]` | add.go | - / - | Mints the user, records it in the issue record, provisions record and inbox, writes the creds file. `--zone` and `--policy` go with the Z chain (on that branch: `--describe`, `--cap` are added). With the Z chain it needs the system credentials in one case (see its changelog fragment). |
| C25 | `mashctl reissue NAME... [--policy FILE]` | `mashctl credential create <name> [--dry-run]` | reissue.go | - / - | Mints the user JWT again; a narrowing reissue rotates the user key and revokes the old one (ADR 0067). Several names become several commands, or `--all` / `--changed`. |
| C26 | `mashctl reissue --changed [--policy FILE]` | `mashctl credential create --changed [--dry-run]` | reissue.go | - / - | Every endpoint whose creds file differs from what a mint now issues, plus staged `.next` files and revoked keys. |
| C27 | `mashctl reissue --all` | `mashctl credential create --all [--dry-run]` | Z4 branch only | - / - | New on the Z chain. |
| C28 | `mashctl retire NAME [--policy FILE] [--delete-creds] [--force] [--no-revoke]` | `mashctl endpoint delete <name> [--delete-creds] [--force] [--skip-revoke] [--dry-run]` | retire.go | - / - | Revokes, removes record and inbox, renames the creds file to `NAME.creds.retired-<time>` (or deletes it). `--policy` goes with the Z chain. Two commands to one spelling: OP-4. |
| C29 | `mashctl label NAME k=v... [--policy FILE] [--dry-run] [--sync]` | `mashctl endpoint <update> <name> --label k=v... [--dry-run]` | label.go | - / yes | On `origin/main` it edits the policy file (offline unless `--sync`); on the Z chain it sets labels on the directory record. Variadic `k=v` becomes the repeatable `--label`, which `set-meta` already has. |
| C30 | `mashctl unlabel NAME k... [--policy FILE] [--dry-run] [--sync]` | `mashctl endpoint <update> <name> --label k=-` | label.go | - / yes | `set-meta --label k=-` already clears a label. |
| C31 | `mashctl unzone NAME [--sys-creds FILE]` | transitional: `mashctl endpoint <migrate> <name>` | Z4 branch only | - / - | One-time rewrite of a six-token record to five-token (ADR 0091). Remove it from the CLI in the release after the zone chain; until then verb outside the set (OP-3). |
| C32 | `mashctl policy check [--policy FILE]` | none | policy.go | - | Removed by Z3. |
| C33 | `mashctl policy test [--policy FILE]` | none | policy.go | - | Removed by Z3. |
| C34 | `mashctl policy explain FROM TO [--policy FILE]` | none now; `mashctl policy explain` returns with the list-set verbs | policy.go | - | The standard's table lists `mashctl policy show`, `explain`, `diff`, `adopt` as conforming "none"; none of `diff`/`adopt` exists as a policy verb in the code (`adopt` is the top-level server one, C23). Correct that table (OP-11). |
| C35 | `mashctl policy show [--json] [--budget] [--policy FILE]` | none now | policy.go | yes | Removed by Z3. |
| C36 | `mashctl policy apply [--dry-run] [--policy FILE] [--sys-creds FILE] [--replace-zone-key Z]... [--remove-zone Z]... [--keep-zone Z]... [--servers N]` | none | apply.go | - / yes | Removed by Z3. |
| C37 | `mashctl policy diff [--policy FILE] [--sys-creds FILE] ...` | none | apply.go | - / n/a | Removed by Z3. |
| C38 | `mashctl policy drift [--policy FILE] [--sys-creds FILE] [--creds-dir DIR] [--json]` | none | drift.go | yes / n/a | Removed by Z3. docs also say `mashctl drift`; that top-level spelling does not exist in the dispatch. |
| C39 | `mashctl policy repair [--dry-run] [--policy FILE] [--sys-creds FILE] [--servers N] [--keep-zone Z]...` | none | drift.go | - / yes | Removed by Z3. |
| C40 | `mashctl policy` (no verb) | no `policy` noun until its verbs land | Z4 policy.go | - | An empty group prints "no subcommands yet" and exits 2; a noun with no verb breaks rule 16 and the tree test, so it is not registered until it has verbs. |

In-flight commands (section 3.3), mapped so the tree stays complete:

| Id | Current | New (provisional) | Notes |
|---|---|---|---|
| C41 | `mashctl inbox inspect <address> [--json] [--legacy] [--from SEQ] [--to SEQ] [--limit N]` (`pr/ctl/inbox-inspect`) | `mashctl inbox show <address> ...` | The standard's table already says this. It is the only `inbox` command on that branch. JSON schema `mash-inbox-inspect@1` is a stable contract: keep the schema string, or change it deliberately with a note. Exit 3 "could not complete" (OP-10). |
| C42 | `mashctl issue serve [--list-set FILE] [--registry FILE] [--sys-creds FILE] --test-account` (issue-service branches) | needs a decision | A daemon, but `mashctl` is not one (rule 5 limits `serve` to a daemon's top level). Either the issue service becomes its own program with `serve` at the top, or this is a recorded exception. OP-13. |
| C43 | `mashctl wrangler init --test-account`, `add --host H --group G --key PUB`, `list`, `remove --host H --group G` (issue-service branches) | `mashctl wrangler init`, `create`, `list`, `delete` | `remove` is outside the set: `delete`. Scope flags `--host`, `--group` are fine; `--key` as the object name (positional) per rule 7. |

**Count.** `mash`: 12 command rows (M1 to M12). `mash admin`: 15 rows (A1 to A15). `mashctl`: 40 rows on `origin/main` plus the Z
branch (C1 to C40), plus 3 in-flight rows (C41 to C43). One launcher row (L1) and 7 MCP tools (T1 to T7), which are
not shell commands. 70 shell rows in all, 78 with launcher and tools.

## 5. Exit codes

The standard: 0 success; 1 failure; 2 usage (unknown command, bad flag or argument); 3 cancelled; 4 not authorized.

| Command | Today | Standard | Change |
|---|---|---|---|
| any unknown command (`mash`, `mashctl`) | 2 | 2 | none |
| any bad flag or bad argument, `mashctl` (nearly all verbs) | 2 | 2 | none. The code takes care never to echo a rejected argument (`config.RejectedArg`): keep that when the parser changes. |
| `mash send` with no body | 1 (`fail(sendUsage)`) | 2 | change |
| `mash send` bad `--kind`, bad or missing `--id` | 2 | 2 | none |
| `mash read` wrong argument count or a non-number | 1 | 2 | change |
| `mash inbox` bad `--limit` / `--after` / unknown argument | 1 | 2 | change |
| `mash prompt` too few arguments | 1 | 2 | change |
| `mash who` bad flag | 2 | 2 | none |
| `mash plugin export` wrong arguments | 2 | 2 | none |
| `mash` without `MASH_NAME` (`cfg.Inert()`) for send/read/inbox/who/prompt | 1 (`set MASH_NAME ...`) | 2 | change: a missing required setting is a usage error, naming the variable |
| config error (bad `config.json`) | 1 | 1 | none |
| `mash status` | always 0, even when it prints `config: FAILED` | 0 report | none; OP-14 asks whether a failing report should exit 1 |
| `mash hook` | always 0 by design (a failing hook must not block the session) | 0 on success | the recorded exception (OP-2) covers this too |
| `mash serve` | 1 when it cannot start, 0 after a clean stop | same | none |
| a server refusal (permissions violation, expired or rejected credentials) | 1 | 4 | new: map NATS authorization errors on connect or publish to 4, in `mash` and `mashctl` |
| `mashctl` without admin credentials (`admin commands need MASH_URL and MASH_ADMIN_CREDS`) | 1 | 2 or 4 | OP-10: missing is a setting (2); present but rejected is 4 |
| `mashctl suspended` | 0 suspended, **3 not suspended**, 1 error | 0 | change: becomes `inbox status`; 3 is no longer used for a state |
| `mashctl audit` | 0 clean (or findings without `--strict`), 1 `--strict` findings, 2 usage, **3 could not complete** | 0, 1, 2 | 3 is the standard's "cancelled"; needs a code for "ran but incomplete" (OP-10) |
| `mashctl audit --compare` bad file | 3 | 1 or the incomplete code | same |
| `mashctl issued list` | 0, 2 on any failure (it uses `fail` = 2 for every error) | 0, 1 | change: failures are 1, 2 only for usage |
| `mashctl issued check` | 0 clean, **1 finding**, **2 incomplete or bad arguments** | 0, 1, 2 | 2 means two things today; usage keeps 2, "incomplete" gets the code from OP-10 |
| `mashctl inbox inspect` (branch) | 0, 1 refusal, 2 usage, 3 could not complete | | same as audit |
| `mashctl policy diff`, `apply --dry-run` | 1 when they refuse, 0 when they only report differences | | removed with the policy group |
| `mashctl retire`, `deprovision` | 1 on any failure, including `PARTIAL`; `not confirmed` is non-zero | 1 | none |
| `mashctl reissue --changed` | 1 when any endpoint failed, others still processed | 1 | none |
| a user interrupt (SIGINT, SIGTERM) | `mash` cancels its context and returns the command's failure code (1) | 3 | new: map a cancelled context to 3 |
| `mash`'s release tooling (`scripts/release-check` uses 3 refused, 4 failed or timed out) | | | not these tools; see OP-12 |

Differences to list in the release notes: C15 (3 gone), C19/C20/C22/C41 (3 and 2 as "incomplete"), the five usage cases
that are now 2 instead of 1, `issued list` failures, and 4 for authorization.

## 6. Output: `--json` and `--dry-run`

### 6.1 Read commands (rule 10)

| Command (new) | `--json` today | Needed |
|---|---|---|
| `mash version` | no | add (keep the text line) |
| `mash message list` | no | add |
| `mash message show` | no | add |
| `mash endpoint list` | yes (`who --json`, ADR 0049) | keep, field names frozen |
| `mash endpoint status` | no | add |
| `mashctl version` | no | add |
| `mashctl endpoint list` | no | add |
| `mashctl limit show` | no | add |
| `mashctl permission print` | JSON is the default; `--nsc` is lines | accept `--json`; `--nsc` is the other format (OP-8) |
| `mashctl inbox status` | no | add |
| `mashctl inbox show` | yes (branch) | keep |
| `mashctl credential list` | yes | keep |
| `mashctl credential status` | yes | keep |
| `mashctl audit show` | yes | keep; `--compare` output is inside the same document |
| `mash hook` | not a read command (it drains); its stdout is the host's hook format | exempt (OP-2) |

Rule 10 also says `--json` prints one document and nothing else: audit and issued already keep notes on stderr; the others
must too (check `mash who --json` with a notice).

### 6.2 Changing commands (rule 11)

Today `--dry-run` exists on: `mashctl adopt`, `label`/`unlabel` (Z chain), and the removed `policy apply`/`repair`. Everything
below lacks it and needs it, plus the test that a dry run leaves state untouched:

`mash message send`, `mash prompt send`, `mash plugin create`, `mash hook`, `mashctl directory init`,
`mashctl server init`, `mashctl endpoint create`, `adopt`, `<update>`, `delete`, `<migrate>`, `mashctl credential create`,
`mashctl inbox stop`, `inbox start`, `mashctl limit apply`. `mash serve` is a daemon verb (OP-10 asks for the exemption).
`mashctl server adopt` already has it and changes nothing on the server, so its dry run is a pure preview already.

`--dry-run` is hardest for `credential create` (a rotation revokes a key: the dry run must read the record and the JWT
and print which key would rotate) and `endpoint delete` (print the revocation that would be pushed). Both can reuse the
reads the real command does first.

### 6.3 Standard output and standard error (rule 12)

Spot-checked in the code: `mashctl` prints the target announcement, notes and the `permissions` note on stderr; results
on stdout; `mash admin` prints its deprecation on stderr. A test per command with standard error discarded does not
exist and is part of rule 22's follow-through.

## 7. The per-user directory

### 7.1 Today

The directory comes from `internal/config/config.go` (`ConfigDir`, `Load`, `FindFile`, `Find`), `internal/spool`,
`internal/endpoint` and `internal/ctlkeys`.

| What | Where today | How it is chosen |
|---|---|---|
| `config.json` (url, owner, creds, admin_creds, host, tls_*, limits, timeouts, `agent_token`, `spool_*`, `name_rule_file`, ...) | `<config dir>/config.json` | config dir: `$MASH_CONFIG_DIR` (absolute, no legacy fallback), else `$XDG_CONFIG_HOME/mash` when absolute and it holds a `config.json`, else `~/.config/mash` |
| endpoint creds `<name>.creds`, and `<name>.creds.next` (staged rotation), `<name>.creds.retired-<time>` | `<config dir>`; `creds` key or `MASH_CREDS` overrides one path | `Find` looks in the config dir, then the legacy `~/.config/mash` |
| admin creds `mashadmin.creds` and system creds `sys.creds` | `<config dir>/server/` (after `init --local` or `adopt`); or the path in `admin_creds` / `MASH_ADMIN_CREDS` / `MASH_SYS_CREDS` | |
| local NATS server dir (`init --local`) | `<config dir>/server/` | `--dir D` |
| `policy.md` (text added to a session's instructions; read by `mash serve`) | `<config dir>/policy.md` (XDG copy wins over legacy) | `Find` |
| `policy.yaml` (the policy file), `policy.yaml.lock` | `<config dir>/policy.yaml` | `--policy`, `MASH_POLICY`; removed by the Z chain |
| key store: `operator.nk`, `system.nk`, `account.nk`, zone keys, operator JWT, `apply.lock` | `<config dir>/keys/` (mode 0700, owned by the user, a symlink is refused) | `MASH_KEYS_DIR` (absolute) |
| the name rule file | the path in `name_rule_file` / `MASH_NAME_RULE_FILE` (relative to the config dir) | |
| TLS files | paths in `tls_ca`, `tls_cert`, `tls_key` / `MASH_TLS_*` | |
| spool of one endpoint: `spool/<owner>/<name>/` with `drained/`, `shown/`, `listed/`, `.hooked`, `.stream`, `.session`, `.owner`, `.refused`, `.told`, `.alive`, `.waiting.*`, and the relay counters file | `<state dir>/spool/...` | state dir: `$MASH_STATE_DIR` (absolute), else `$XDG_STATE_HOME/mash` when absolute, else `~/.local/state/mash` |
| `hook.log` | `<state dir>/hook.log`, **computed in the plugin's `hooks.json` shell command**, not by the binary | `${MASH_STATE_DIR:-${XDG_STATE_HOME:-$HOME/.local/state}/mash}` |
| the plugin's downloaded binaries `bin/mash-<version>`, and the `.dl-failed-<version>` marker | `$CLAUDE_PLUGIN_DATA/bin`, else `${XDG_CACHE_HOME:-~/.cache}/mash-plugin/bin` | computed in the launcher script; note it is `mash-plugin`, not `mash` |
| anything under `~/.cache/mash` | nothing: the `mash` binary writes no cache | |

Environment variables that decide places or behaviour of the above: `MASH_CONFIG_DIR`, `MASH_STATE_DIR`, `MASH_KEYS_DIR`,
`MASH_POLICY`, `MASH_CREDS`, `MASH_ADMIN_CREDS`, `MASH_SYS_CREDS`, `MASH_NAME_RULE_FILE`, `MASH_TLS_CA|CERT|KEY`,
`XDG_CONFIG_HOME`, `XDG_STATE_HOME`, `XDG_CACHE_HOME`, `CLAUDE_PLUGIN_DATA`, `HOME`. Settings that are not places (all read
by `mash`, most also settable in `config.json`): `MASH_URL`, `MASH_OWNER`, `MASH_NAME`, `MASH_AGENT_TOKEN`,
`MASH_LAUNCH_TOKEN`, `MASH_HOST`, `MASH_SERVERS`, `MASH_INSECURE_LOOPBACK`, `MASH_SPOOL_FROM_START`, `MASH_SPOOL_REQUIRED`,
`MASH_NAME_RULE_REQUIRED`, `MASH_OPEN_TIMEOUT`, `MASH_CALL_TIMEOUT`, `MASH_LEASE_RETRY`, `MASH_TRANSIENT_BUDGET`,
`MASH_TRANSIENT_MAX`, `MASH_CLI_TIMEOUT`, `MASH_ADMIN_TIMEOUT`, `MASH_IDLE_WAKE`, `MASH_SETTLE_MS`, `MASH_BATCH_MSGS`,
`MASH_BATCH_BYTES`, `MASH_RELAY_WINDOW`, `MASH_PLUGIN_ROOT`, `MASH_MANAGED_SETTINGS_DIR`. Launcher only: `MASH_BIN`,
`MASH_RELEASE_BASE`. Test and release tooling: the `MASH_CONSUMER_*`, `MASH_LIVE_*`, `MASH_GO_SEED*`, `MASH_CI_WARM`,
`MASH_*_FAILPOINT`, `MASH_HOOK_TRACE` families.

### 7.2 New layout

`~/.mash/{config,state,cache}` (rules 18 to 20), shared by `mash` and `mashctl`. Placement follows rule 18: `config/`
is what a person writes, `state/` is what the tool must keep (including a local copy it depends on while the server
is unreachable), `cache/` is what can be deleted.

| Today | New | Class | Notes |
|---|---|---|---|
| `~/.config/mash/config.json` | `~/.mash/config/config.yaml` | config | Rule 17 makes it YAML. JSON is valid YAML, so existing content can be copied byte for byte; the file name and the reader change (OP-12). Key names (`url`, `owner`, ...) stay. |
| `~/.config/mash/policy.md` | `~/.mash/config/policy.md` | config | Free text written by a person. |
| `~/.config/mash/policy.yaml` (+ `.lock`) | none | | The policy file is gone with the Z chain; leave a stale one alone and say so. |
| name rule file | where `name_rule_file` points; default location `~/.mash/config/` | config | A relative path resolves against `config/`. |
| `~/.config/mash/<name>.creds` | `~/.mash/state/creds/<name>.creds` | state | `mashctl` writes them; an operator who brings creds from `nsc` still names the path with `creds` / `MASH_CREDS`. Alternative: keep them in `config/`: OP-12. |
| `<name>.creds.next`, `.retired-<time>` | `~/.mash/state/creds/` | state | Same directory as the file they belong to (audit scans this directory). |
| `~/.config/mash/keys/` | `~/.mash/state/keys/` | state | Signing seeds. Keep mode 0700, owner check, symlink refusal. |
| `~/.config/mash/server/` | `~/.mash/state/server/` | state | Written by `server init` and `server adopt`. |
| `~/.local/state/mash/spool/` | `~/.mash/state/spool/` | state | |
| `~/.local/state/mash/hook.log` | `~/.mash/state/hook.log` | state | Today the plugin's `hooks.json` computes this path (section 8). |
| relay counters (inside each spool directory) | `~/.mash/state/spool/<owner>/<name>/` | state | Moves with the spool. |
| `$CLAUDE_PLUGIN_DATA/bin/...` | unchanged when `CLAUDE_PLUGIN_DATA` is set (the host owns that directory); otherwise `~/.mash/cache/plugin/bin/...` | cache | Replaces `~/.cache/mash-plugin`. Deletable: the launcher downloads again. OP-12. |
| `$MASH_CONFIG_DIR`, `$MASH_STATE_DIR`, `$XDG_CONFIG_HOME`, `$XDG_STATE_HOME`, `$XDG_CACHE_HOME` | `$MASH_HOME` (default `~/.mash`) | | Rule 19: "nothing else moves the directory". |
| `$MASH_KEYS_DIR` | `$MASH_HOME/state/keys` | | Moves the key store alone: a second variable that moves a part of the directory. OP-11 asks whether to keep it. |
| `$MASH_POLICY`, `$MASH_CREDS`, `$MASH_ADMIN_CREDS`, `$MASH_SYS_CREDS`, `$MASH_TLS_*`, `name_rule_file` | unchanged | | These name a single file; the user names the path, so rule 21 allows it. They are not "moving the directory". |

`MASH_HOME`: absolute path required (as `MASH_CONFIG_DIR` is today: `Load` rejects a relative one). Tests set it instead of
`XDG_CONFIG_HOME` and `HOME` (`internal/testenv` sets `XDG_CONFIG_HOME` today).

Rule 21 (never write in a work tree): `mashctl` writes only under `~/.mash` or at paths the user names (`--dir`, `--policy`,
`--creds-dir`, `plugin create <dir>`). The fresh-home test must run inside a git work tree.

### 7.3 Migration of an existing install: recommendation

What the standard says: old command spellings stop working. It says nothing on files. Four ways to treat the old
locations, with the facts of this code that decide between them:

| Option | For | Against |
|---|---|---|
| A. Move automatically on first run | One step, nothing for the user | A session of the previous release (running with N-1 binary) keeps reading `~/.config/mash` and `~/.local/state/mash` while it runs: a move under it breaks reconnects (it re-reads creds and config) and splits the spool. Several sessions of one user share the config directory. A move cannot be undone if it is half done. |
| B. Read the old locations forever | Nothing breaks | Two layouts forever; rule 18 to 20 are not met; `mashctl` would have to pick where to write. |
| C. Refuse until the user migrates | Nothing splits | Takes every unattended session offline on upgrade day (the hook swallows errors and exits 0, so mail delivery just stops with a line in `hook.log`). |
| D. Read-through for readers, an explicit step for everything that writes (recommended) | No session breaks; the writing side is explicit; old sessions keep working | Two commands to explain; a warning until done. |

**Recommendation: D.**

1. **`mash` (serve, hook, message, endpoint, prompt)**, which only reads configuration: if `~/.mash` does not exist and the
   old layout does, read the old `config.json`, creds and `policy.md` through the old lookup, and print one stderr
   warning per run (`mash: reading the old layout under ~/.config/mash; run "mash home adopt"`; in `status`, a line). Spool
   and `hook.log` are written under `~/.mash/state/` from the first run: they are scratch the new binary can create. This is
   safe by design, not luck: undrained mail is not acked, so it stays in the JetStream inbox and is delivered again
   (`hook.go` header: "marks what was shown drained, which serve then acks"); a new serve that starts with an empty spool
   loses nothing. The relay counters restart at zero (diagnostic only; not verified beyond reading `relay.go`).
2. **`mashctl`**, which writes keys, creds and records: refuse with exit 2 and the same one-line message until the
   layout is adopted. An admin tool run by a person is the place to stop.
3. **The explicit step**: a one-time command, `mash home adopt [--from DIR] [--dry-run]` (the noun `home` and the verb
   `adopt`, "take an existing object under management", are a suggestion: OP-12). It
   - **copies** `config.json` to `config/config.yaml`, `policy.md`, and the creds files (so sessions of the previous
     release keep reading the old copies until they exit); it rewrites relative paths in the config (`creds`,
     `name_rule_file`) to absolute ones when the file they name was not copied, because relative paths resolve against
     the config directory, which has changed;
   - **moves** the key store, and leaves in its place a symlink, which the previous `mashctl` refuses (`checkDir`:
     "key directory ... is a symlink: refusing it"), so an old `mashctl` fails safe instead of writing a second set of
     keys. It never leaves two live copies of the signing seeds;
   - **leaves `server/` where it is** (a NATS server may be running from it, its config holds absolute paths) and
     records the path in the config, then prints the move to do by hand;
   - does not touch `~/.local/state/mash`, and prints that it can be deleted when every session of the previous release
     has exited;
   - prints what it did, one line per file, and is safe to run again.
4. **After the step**, the read-through stops (the old location is ignored, with one warning if it still holds a
   `config.json`).

What happens to sessions of the previous release: they run the old binary against the old paths and are unaffected by
steps 1 to 4, except an old `mashctl`, which fails safe on the key store. They deliver mail through their own old spool. Their hooks
write to the old `hook.log`. Nothing the new binary does reaches them.

`MASH_CONFIG_DIR` and `MASH_STATE_DIR` set in the environment: with rule 19 they must stop working. Silently ignoring
them would move the state of a configured install without a word, so recommend **refusing** (exit 2, naming `MASH_HOME`) in `mash` and `mashctl`, and **warning once to
`hook.log`** in `hook` (which never fails). `XDG_*` is simply no longer read.

## 8. Plugin, hooks, MCP and docs impact

### 8.1 What the plugin runs today (exact)

From `plugin/hooks/hooks.json`, `plugin/.mcp.json` and the golden in `cmd/mash/plugin_hook_test.go`
(`pluginCommandsGolden`, `pluginMinMash = "0.2.0"`):

| Where | Command | Binary command | Survives the rename? |
|---|---|---|---|
| SessionStart hook (timeout 45) | `sh "${CLAUDE_PLUGIN_ROOT}/scripts/mash" __prefetch` | none (the launcher answers) | yes: not a `mash` command |
| PostToolUse `*` hook (timeout 10) | `d="${MASH_STATE_DIR:-${XDG_STATE_HOME:-$HOME/.local/state}/mash}"; mkdir -p "$d"; l="$d/hook.log"; ...; sh "${CLAUDE_PLUGIN_ROOT}/scripts/mash" hook 2>>"$l"; exit 0` | `mash hook` | **only if `hook` keeps its spelling** (OP-2); the shell also hard-codes the old state path |
| UserPromptSubmit hook (timeout 10) | the same line | `mash hook` | same |
| MCP server `mash` | `${CLAUDE_PLUGIN_ROOT}/scripts/mash serve`, env `MASH_SPOOL_FROM_START=1`, `MASH_PLUGIN_ROOT=${CLAUDE_PLUGIN_ROOT}` | `mash serve` | yes (conforms) |
| launcher internals | `<binary> version` after a pinned download | `mash version` | yes (stays top level) |

No other command of the plugin calls `mash`. The plugin's MCP instructions (the server's `instructions` text) name the
MCP tools, not shell commands.

### 8.2 The N and N-1 constraint

`cmd/mash/plugin_hook_test.go` says: several sessions of one user share one Claude Code config directory, so the
installed plugin at version N runs against binaries N and N-1; a new command may ship only once the previous release has
it. ADR 0034 makes the plugin's command lines a compatibility contract, and the release check runs the plugin against both the
candidate and the previous binaries (`scripts/releasecheck/main.go`, `latency.go`: `hook` exit 0 and nothing on stdout when idle, `unknown command "hook"`
tolerated for releases older than `HookSince`).

What a rename does to that:

- **Plugin N-1, binary N** (an old plugin, a new binary): the old plugin calls `mash hook`; with `hook` renamed and no
  alias, binary N answers exit 2. The hook command ends `; exit 0` and sends stderr to `hook.log`, so the session sees
  nothing: **mail delivery by hook stops silently**, and the channel wake then loses its target (ADR 0024, 0041 say a
  session whose hooks never run stops being woken and falls back to `read`). When can this pair occur? The launcher
  pins its binary (`mash.pins`), so a pinned plugin N-1 downloads binary N-1. The pair occurs when the download fails and
  the launcher falls back to `mash` on `PATH`, when `MASH_BIN` is set, and for an unpinned plugin (a checkout, or the
  output of `mash plugin export`), which always uses the binary on `PATH`.
- **Plugin N, binary N-1**: a plugin N that calls a new spelling fails against binary N-1 in the same way.
- Because of rule 6 there can be no overlap period in which both spellings work.

Therefore the plugin-facing commands are the hard constraint on the order of work:

| Plugin command | Rename in the major revision? |
|---|---|
| `mash serve` | no change needed (conforms) |
| `mash version` | no change needed (stays top level) |
| `mash hook` | the one problem: keep it as a recorded one-revision exception (OP-2). Moving it later needs a plugin hook line that works with the old and the new spelling (for example `<new> \|\| <old>`), released in a plugin version that ships after every binary that has the new spelling. |
| the `hook.log` path inside `hooks.json` | the path changes with the layout. Plugin N-1 (old path) with binary N: only the log lands elsewhere; harmless. Plugin N (new path) with binary N-1: also only the log. Safe either way, but update it with `MASH_HOME`: `${MASH_HOME:-$HOME/.mash}/state/hook.log`. |

### 8.3 Places that call a current spelling

| Place | What it calls | Count (command lines, not ADR text) |
|---|---|---|
| `plugin/hooks/hooks.json`, `plugin/.mcp.json`, `plugin/scripts/mash` (launcher) | `hook`, `serve`, `version`, `__prefetch` | 4 |
| `cmd/mash/plugin_hook_test.go` golden, `internal/release/plugin.go` (`HookSince`, the hook checks) | the same four | |
| `scripts/releasecheck/main.go` and `latency.go` | `mash hook` (run against candidate and previous binaries, so both spellings must be known per release), `mash send` (latency probe, `latency.go:397`), `mash serve` (MCP client), `mash version` | 4 verbs |
| `scripts/quickstart-synadia.sh` | `mash admin init`, `mash admin list`, `mash admin permissions`, `mash admin provision`, `mash admin deprovision`, `mash send`, `mash inbox` (and mentions in its header comment and error texts) | 7 verbs, about 15 lines |
| `.github/workflows/ci.yml` (line 174) | `mash version` output string | 1 |
| `.github/ISSUE_TEMPLATE/bug_report.yml` | `mash version`, `mash status` | 2 |
| `README.md` | `mash serve`, `send --kind`, `prompt`, `inbox`, `plugin export`, `version` | 7 mentions |
| `docs/install.md` | `serve`, `status`, `plugin export`, `send`, `prompt`, `who`, `inbox --after`, `hook`, `version`, `mash admin init/provision`, `mashctl init --local` | 20 |
| `docs/operations.md` | `mash status` (12), `serve`, `who`, `hook`, `mash admin init/provision/permissions/deprovision/set-meta/limits/list`, `mashctl add/reissue/retire/audit/issued check/policy apply/drift/repair/explain/diff/check/init/provision/deprovision/suspend/resume/revoke` | 113 |
| `docs/mashctl.md` | `add`, `reissue`, `retire`, `init`, `deprovision`, `suspend`, `set-meta`, `set-zone`, `label`, `unlabel`, `issued list/check`, `audit`, `adopt`, `policy ...` | 46 |
| `docs/audit.md`, `docs/architecture.md`, `docs/troubleshooting.md`, `docs/synadia.md`, `docs/security/threat-model.md`, `docs/faq.md`, `docs/comparison.md`, `docs/release.md`, `docs/demo/README.md`, `SECURITY.md` | mixed | 8, 22, 25, 8, 22, 3, 3, 4, 1, 5 |
| `docs/policy-file.md` | the policy verbs | 10 (the file goes with the Z chain) |
| `CHANGELOG.md`, `docs/decisions/*` (about 80 ADRs), `docs/plans`, `docs/specs`, `docs/evidence` | historical | leave as written (they record what was true) |
| error, status and help strings in code | about 50 lines in `cmd/mash`, `internal/{endpoint,render,host,ctl,admin}` name a command (for example `Next: ... mash status`, `run mashctl init`, `mashctl set-meta NAME --label k=v`) | find with a search for `mash(ctl)? <verb>`; each is a user-visible string and most are pinned by golden tests |

Documentation errors found while reading, to fix in the same PRs: `docs/operations.md:202` and `docs/mashctl.md` name
`mashctl revoke` and `mashctl drift`, which are not commands (the first is an internal function; the second is
`policy drift`); `docs/install.md` says `mashctl` "is not yet in the release assets" (check before the major revision).

### 8.4 ADR 0034 and the golden fixtures

`internal/render/testdata/contract.txt` pins the text of `render.One` and `render.Line`, and release check refuses a
change without a `render-contract:` commit. The command rename changes no rendered mail. The `mash who` default lines and
`--json` fields (ADR 0049) must not change. The golden list of plugin commands and `pluginMinMash` change only if OP-2 is
decided the other way. A new ADR will amend or supersede ADR 0034's plugin-command clause (section 9.6).

## 9. Migration story

### 9.1 The old spelling fails, and says what to use

Rule 6 forbids aliases. An alias runs the old command under a new name. An error that names the new spelling runs
nothing, takes no old arguments, and ends in exit 2: it is a usage error with a helpful message (rule 13: what failed and
what to do next, in one or two sentences), not a second spelling. The tree test (rule 22) can verify both: no command
declares an alias, and every old spelling in the table of renames exits 2 without side effects.

Pattern, one table in the code (`old path -> new path`), consulted only when the normal parse finds an unknown command:

```text
mash: "mash send" was renamed to "mash message send". The text now follows --text; run "mash message send --help".
mashctl: "mashctl retire" was renamed to "mashctl endpoint delete". Run "mashctl endpoint delete --help".
mash: "mash admin" was removed. Use "mashctl"; "mashctl help" lists its commands.
```

Rules for the table: the message never echoes the old arguments (the code takes care not to echo rejected arguments:
`config.RejectedArg`); it is printed to stderr; it exits 2; it runs nothing. Keep the table for at least two minor
releases after the major revision, then let those spellings fall to `unknown command`. A table entry for each row of
section 4 whose spelling changes, with a test that each old spelling prints a message containing a path that exists in the
tree (this check also guards the table against going stale).

### 9.2 Release notes

`changelog.d` fragments (`<slug>.<kind>.md`, kind `changed` and `removed`; the release gate wants a summary and an
Upgrading section, ADR 0105) must list every renamed command. One fragment per noun, each with a two-column list. The
complete list is the "New" column of section 4 for every row where it differs from "Current". In the Upgrading section:

1. every renamed command (the full list);
2. the flags that changed meaning: text of `send` and `prompt` after `--text`; `--file -` for stdin; `reissue NAME...`
   one name per command; `limits [name...]` to `--endpoint`; `--no-revoke` to its new name; `--label k=v` replaces
   `label` and `unlabel`; `init --local` to `server init`;
3. the exit-code changes of section 5, naming `suspended` and `audit` first, since scripts test those;
4. `MASH_CONFIG_DIR`, `MASH_STATE_DIR`, `XDG_*` no longer read; `MASH_HOME`;
5. the directory move and the one explicit step, `mash home adopt` (section 7.3);
6. `mash admin` is removed;
7. a line for plugin users: update the plugin and the binary together (the pinned launcher does this).

### 9.3 Order of work

Stacked PRs, one noun at a time, each targeting `main` with `Depends-On: #N` (the watcher skips non-main bases). Each PR adds
its ADR line (`Decision: none` or `Decision: ADR-NNNN`, alone on the last line).

| Step | PR | Content |
|---|---|---|
| 0 | this design | the document |
| 1 | the ADR | adopts the standard for `mash` and `mashctl` and records the exceptions (`hook`, daemon `--dry-run`); amends ADR 0034 |
| 2 | the framework | the command tree (section 9.5), `--help` at every level, `completion`, the exit-code mapper, the renamed-command table, and the command-tree test with an allowlist of commands not yet converted. No spelling changes yet |
| 3 | `MASH_HOME` and the layout | `internal/config` reads `~/.mash`, the read-through, `mash home adopt`, the key-store move, plugin launcher and `hooks.json` log path. Done before the verbs so that no later PR writes a new path in the old place |
| 4 | `mash message`, `mash endpoint`, `mash prompt` | M6 to M11; `--json` on the reads; the stdin and text flags; update the docs and `quickstart-synadia.sh` |
| 5 | `mash plugin`, `mash version` | M3, M12; the plugin's own files |
| 6 | `mashctl directory`, `server`, `limit`, `permission` | C4 to C8, C23 |
| 7 | `mashctl endpoint`, `credential`, `inbox` | C9 to C31 |
| 8 | `mashctl audit` | C19, C20 and the incomplete exit code |
| 9 | remove `mash admin`; delete the allowlist from the test | A1 to A15 |
| 10 | the docs sweep and the plugin release | README, install, operations, mashctl, audit, troubleshooting; the plugin is released **after** the binaries (section 8.2) |

Steps 4 to 8 can run in parallel once step 2 lands, because they touch different nouns; the docs files overlap, so
each PR edits only the rows of its own noun in the shared docs, and step 10 does the rest. The Z chain should merge before
step 7 (it deletes `policy`, `set-zone`, `--policy`, `--zone`, `--sync`).

### 9.4 Dispatch today and the recommendation

**Today.** `mash` and `mashctl` use a hand-rolled `switch args[0]` (`cmd/mash/main.go` `runLookup`, `cmd/mashctl/main.go`
`run`), then each verb parses its own flags by hand (`parseWhoFlags`, `inboxArgs`, `parseAddArgs`, `parseAuditArgs`,
`parsePolicyArgs`, ...). `go.mod` has no `cobra` or `pflag`. The hand parsing is deliberate in places: rejected arguments are never
echoed (`RejectedArg`), `--flag=value` and `--flag value` are both handled, and the tests pin the usage text and error texts
(about 100 test files in the two commands).

**Recommendation: adopt cobra, and keep the command bodies.** Reasons: the standard's rule 22 describes the test as a
walk over `Commands()`; rules 14 and 16 and the `completion` command come with cobra almost free; `Aliases` is checkable.
Costs and controls: a new module dependency (the repository's gates for dependencies apply: `scripts/scrubcheck`,
`dependabot`, the SLSA claims in ADR 0051); cobra prints the offending flag in its errors, so set `SetFlagErrorFunc` to
route through `RejectedArg`; set exit 2 for every parse error; keep the existing `runX` bodies behind thin cobra
commands during the move, so the diff of each noun PR is the tree and the flags, not the behaviour. The alternative, a small
in-house table (`Command{Noun, Verb, Positional, Flags, Read, Changing, Run}`) walked by the same test, avoids the dependency and is about the
same amount of code as the flags layer; choose it if the maintainer does not want cobra (OP-12). Either way the table of old spellings
(section 9.1) is separate from the tree.

### 9.5 The command-tree test (rule 22)

One test per tool, in the gate, that walks the tree and fails on: a command without exactly one noun and one verb or a
legal top-level verb (rules 1, 3, 5); a noun that is not in the tool's noun list or has two sub-nouns (rule 2); a verb not
in the set (rule 4); an alias (rule 6); more than one positional, or a positional not named in the usage line (rule 7);
a flag without a long name or a boolean taken positionally (rule 9); a read command whose `--json` output is not one JSON document
(rule 10: the test runs each read command against a fake or in-process server; `mash` and `mashctl` already have in-process
NATS test servers); a changing command without `--dry-run` or a dry run that changes state (rule 11); a bad flag at every
level that does not exit 2 (rule 14); a level without `--help` text, a usage line and an example (rule 16). During the
move the test carries an allowlist of old commands, which shrinks to empty at step 9. Also the fresh-home test of rules 18, 19
and 21 (run with an empty `MASH_HOME`, inside a git work tree, list what was written) and a test that `MASH_CONFIG_DIR`
and `MASH_STATE_DIR` are refused.

### 9.6 ADRs this needs

- one decision: mash and mashctl adopt `cli/STANDARD.md` 1.0.1, the tree in section 3, no aliases, a renamed-command error table;
- one decision: the per-user directory and the migration (section 7.3);
- an amendment or supersession of ADR 0034 (the plugin's commands are frozen: the new rule is "the plugin-facing commands are `serve`, `version`,
  and the recorded exception");
- ADR 0049 stays (the `who` JSON contract), with its command name updated in a note.

## 10. Open points and questions for the maintainer

| Id | Question | Recommendation |
|---|---|---|
| OP-1 | `send` is not in the verb set, though the standard's own table writes `mash message send`. `mash prompt <to> <text>` needs the same. | Ask the planner to add `send` to the set ("deliver a message to a recipient"; changes something). Fallback: `message create` and `prompt create`. The tree above uses `send` and must change if the set does not. |
| OP-2 | `mash hook` is outside rule 3 (top level) and has no obvious noun or verb in the set (it shows spooled mail and marks it drained; `print` is read-only, `run` and `ensure` are daemon-only). | Record it as the one exception for this revision (the standard's Exceptions section: for one revision, in the decisions file, flagged to the planner), keep the spelling `mash hook`, and move it at the next revision with a plugin line that tries both. Alternative: ask for a verb. Reason: section 8.2, a rename silently stops hook delivery in any pairing of an old plugin with a new binary. |
| OP-3 | No verb for "change fields of an existing object" (`set-meta`, `label`, `unlabel`), nor for the one-time `unzone`. | Ask for `update` in the set. Until ruled, nothing is built for those three. `unzone` should not outlive the zone removal release. |
| OP-4 | `retire` and `deprovision` both revoke and remove the record and inbox. `retire` adds the creds-file handling (rename or `--delete-creds`) and, on `origin/main`, the policy entry; `deprovision` adds the guarded forms (`--user`, `--drained-to`, `--suspension`). One spelling means one command. Also `reissue` rotates keys: `credential create` is the nearest verb. | Merge both into `endpoint delete`: the guards stay as flags, the creds handling stays as `--delete-creds` and the default rename. Needs the maintainer to confirm the merge changes `deprovision`'s default (it touches no creds file today). Rename `--no-revoke` now to `--skip-revoke`, since the code calls it temporary. |
| OP-5 | `suspend`, `resume`, `suspended` against `stop`, `start`, `status`, `restore`. | `inbox stop`, `inbox start`, `inbox status`; `suspend` parks mail, it does not stop a process, so a reviewer may prefer `restore` for `resume`. |
| OP-6 | `mash plugin export <dir>` writes files; the standard's table says `print` (rule 4: standard output, "without installing it") or `export` added to the set. | `plugin create <dir>`; or `plugin print` that writes a tar stream to stdout (`mash plugin print \| tar -x -C DIR`), which keeps the verb but changes how it is used and what the launcher mode bit (0755 on `scripts/mash`) means. |
| OP-7 | `audit show` or `audit list`. | `show`: the audit is one report; `list` fits if the report becomes a list of findings. |
| OP-8 | `permission print` has two formats and `--json` is the default one. | Accept `--json` (no-op), keep `--nsc` as the other format, document that `--nsc` is not JSON. |
| OP-9 | Variadic positionals (`limits [name...]`, `reissue NAME...`, `label NAME k=v...`, `send` text) and `limit apply` taking no file. | Names become a repeatable scope flag or one command per name; `apply` with no positional because the definition is the config's `limits`. Say so in the standard's next revision. |
| OP-10 | Exit codes: "could not complete" (3 in `audit`, `inbox inspect`; 2 in `issued check`), `suspended` 3, authorization (4) unused, daemons and `--dry-run`. | Reserve a documented code, 5, for "ran but could not complete" (the standard allows extra codes on the reference page); 4 for authorization failures from NATS; `serve` and `hook` exempt from `--dry-run`. |
| OP-11 | Corrections to the standard's "Where it applies today" table: `mash policy show` and `explain` do not exist; `mashctl policy diff` and `adopt` are not policy verbs; `mash admin init` is not an `admin init` to keep (`mash admin` is removed); `mashctl audit` has `--compare`. Also whether `MASH_KEYS_DIR` and the per-file overrides are consistent with rule 19. | Send the list to the planner. Keep per-file overrides (`MASH_CREDS`, `MASH_ADMIN_CREDS`, `MASH_SYS_CREDS`, `MASH_POLICY`, `MASH_TLS_*`); drop `MASH_KEYS_DIR` (tests use `MASH_HOME`). |
| OP-12 | Several smaller placements: config file name and format under rule 17 (`config.yaml`; `configuration-format/STANDARD.md` unread); creds under `state/creds` or `config/`; the plugin download cache (`CLAUDE_PLUGIN_DATA` is the host's); the migration noun and verb (`mash home adopt`); whether rule 16 applies to the launcher shell script and its `__prefetch`; whether `scripts/release-check` and other dev tools are "programs a component ships for a shell"; cobra or an in-house table. | As written in sections 7 and 9; decide together. |
| OP-13 | `mashctl issue serve` (in flight) is a daemon inside a tool that is not one. | A separate program (`issue serve`), or a recorded exception. |
| OP-14 | `mash endpoint status` exits 0 even when its report says `config: FAILED`. | Keep 0: it is a report; the JSON carries the result. Exit 1 only if the maintainer wants `gh auth status` behaviour. |
| OP-15 | The MCP tool names (`send`, `read`, `inbox`, `who`, `reply`, `version`, `status`) now differ from the CLI nouns. | Keep the tool names; document the correspondence in `docs/architecture.md`. |

## 11. Risks

- **Silent loss of hook delivery** in any pairing of an old plugin with a new binary (section 8.2). Mitigation: OP-2, release
  the plugin after the binaries, a release-check step that runs the previous plugin's hook line against the candidate.
- **Release tooling must know two spellings**: `scripts/releasecheck` runs `hook` and `send` against the previous and
  the candidate binary; the previous has the old spellings. It needs a per-version spelling map for the length of
  one release cycle.
- **Scripts in the wild**: `mash who --json` (frozen contract), `mash inbox`, `mash send`, `mashctl suspended` (exit 3),
  `mashctl audit --strict` in CI jobs, `mashctl issued check` (exit 1 and 2). Each breaks at once, by design. The renamed
  error and the release notes are the only help; the exit-code changes do not announce themselves.
- **Two key stores** if an old `mashctl` and a new one are both used after migration. Mitigation: the key store moves and
  a symlink replaces it (7.3), which the old `mashctl` refuses. Verify that `checkDir`'s symlink refusal is reached
  before any write in every old `mashctl` code path that opens the key store (this author read `ctlkeys.Open` and
  `checkDir`, not each caller).
- **Config copied, files left behind**: relative paths in a copied config (`creds`, `name_rule_file`) resolve against the
  new config directory. The adopt step rewrites them; a test must cover it.
- **The Z chain and in-flight branches change the surface under this plan** (section 3.3). Landing order matters more
  than the order of nouns.
- **Test cost**: about 100 test files pin the old usage text, error text and spellings. The renames are mechanical but large;
  the stacked PRs by noun keep each one reviewable.
- **A hidden dependency on the old spellings in the docs the MCP `status` and error strings print** ("Next: ... `mash status`"). A
  missed string tells a user to run a command that no longer exists. The renamed-command table's test catches the
  spelling if the string is scanned: add a test that every command named in a `Next:` string exists in the tree.
- **Standard drift**: this proposal asks for four changes to the standard (OP-1, OP-3, OP-10, OP-11). If the planner declines
  one, the tree line that uses it moves (`send` to `create`, `<update>` to `apply` with a file).

## Appendix: how this inventory was made

Read: `cmd/mash/main.go`, `cli.go`, `admin.go`, `plugin.go`, `hook.go`, `status.go`, `whofilter.go` and the MCP tool list in
`serve.go`; `cmd/mashctl/main.go` and the verb files; `internal/ctl/ctl.go` (the help text and `Run`);
`internal/config/config.go`, `internal/ctlkeys`, `internal/spool`, `internal/endpoint/relay.go`; `plugin/` (hooks, MCP,
launcher, pins); `scripts/quickstart-synadia.sh`, `scripts/releasecheck`; `docs/mashctl.md`, `operations.md`, `install.md`,
`audit.md`; ADR 0034 and 0049; and the branches named in section 3.3 by their diffs against `origin/main`.
Not verified: that no other test or script calls an old spelling (the counts in 8.3 come from a text search for
`mash`/`mashctl` followed by a verb, in tracked non-test files); runtime behaviour of any command (nothing was run); the
glossary terms for the nouns other than `endpoint` and `message`; `configuration-format/STANDARD.md`.
