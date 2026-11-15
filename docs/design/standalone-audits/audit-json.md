# mashctl JSON audit

Repo: the mash repository at origin/main 5e610e5. Read-only. I built the binary into the scratchpad and ran only commands that need no server (`help`, `permissions`, `limits --show`, and each verb with an empty config to read its error and exit code).

Marks: **[F]** fact, read in code (file:line) or run. **[I]** inferred from code, not run against a server.

Paths are relative to the mash repository. `ctl.go` is internal/ctl/ctl.go. Most commands are in cmd/mashctl/.

## 0. Things true of every command

- **[F] No command reads stdin.** No grep hit for `os.Stdin` in cmd/mashctl or internal/ctl. No prompt anywhere. The only confirm flag is `reissue --all|--changed --yes` (reissue.go:50, 163). Every command is non-interactive.
- **[F] Input is flags, environment and files.** Environment: `MASH_URL`, `MASH_OWNER`, `MASH_ADMIN_CREDS`, `MASH_CONFIG_DIR`, `MASH_KEYS_DIR` (ctlkeys.go:63), `MASH_SYS_CREDS` (syscreds.go:35), `MASH_SERVERS` (revoke.go:100), `MASH_POLICY_BUCKET`, TLS settings (internal/config/config.go). The same settings can sit in `config.json` in the config dir. Values come from the process environment, so a wrapper sets them per call.
- **[F] The only JSON mashctl takes in is two files:** a list-set document for `policy diff` (schema `mash.listset/1`, decode.go:30, or a compact JWS) and an earlier `audit --json` report for `audit --compare` (audit_compare.go:184-203). Every other input is a flag.
- **[F] Errors are plain text on stderr.** There is no JSON error object in any command. Errors start with `mashctl: ` (some with the verb and name). Most verbs also print one line `mashctl: target server URL (from SOURCE), owner ..., admin creds ...` to stderr first (target.go:238-268, main.go:76-78).
- **[F] Exit codes differ per command and the same number means different things** (see section 2).
- **[F] Secrets never go to stdout.** `add` and `reissue` write creds to a 0600 file. `adopt` reads seeds only from 0600 files and does not print them (operations.md:45; adopt.go:205 "seeds not printed"). A bad key argument is named, not echoed (policy_adopt.go:99-105, ctl.go:637-653).
- **[F] Where creds go.** `add` writes `<config dir>/NAME.creds` (add.go:134-141, 232, 307). There is no `--out` flag. The wrapper picks the directory by setting `MASH_CONFIG_DIR` for the call.
- **[F] `mash admin`** is the same code as these ten verbs: init, init --local, limits, permissions, provision, set-meta, suspend, resume, suspended, deprovision, list (`ctl.Run`, called from cmd/mash/admin.go:19). Differences: it prints a deprecation line on stderr, and `RevokeUsers` is nil there, so `mash admin deprovision` does not revoke the user key (ctl.go:28-31, 431, 467). Everything below about those verbs holds for `mash admin`.

## 1. Per command

Format: **Input / Output / Errors and exit codes / Idempotence**.

### init
- Input: no args; env only. Extra args exit 2 (ctl.go:228-230). Also records the admin user in the issue record (ctl.go:274, 774).
- Output: text only. One line `mashctl: directory and lease buckets ready` (ctl.go:277). No JSON.
- Exit: 0 ok; 1 any failure (no owner, no URL, connect, bucket); 2 extra args. [F]
- Rerun: safe. Same line, exit 0; raises caps only (admin.go:154-184). [F]

### init --local
- Input: flags `--dir --owner --port --start` (local.go:38-53). Needs no server and no config.json. Writes keys, server config, creds.
- Output: text only. Prints paths of server config, admin creds, sys creds, key dir, config.json (local.go:99-111). Prints no seed. Ends with `Next:` prose. No JSON. A wrapper must parse paths from prose, or compute them (`<dir>/admin.creds`-style names come from internal/bootstrap).
- Exit: 0; 1 failure; 2 bad flags. With `--start`, if the server starts but buckets fail, exit 1 while the server runs (local.go:134-137, message names the pid). [F]
- Rerun: `bootstrap.Build` and `pl.Write` run again; an existing config.json is left alone (local.go:106-110). Whether a second server config write is a no-op is not read. [I]
- Long-running: `--start` launches a detached server and returns its pid in text (local.go:125-129).

### limits [name...]
- Input: names as args; env. Output: one text line per inbox from `ApplyLimits` (ctl.go:333-336). No JSON.
- Exit: 0; 1; 2 on a bad name. Never lowers a limit. Rerun safe. [F]

### limits --show
- Output: five `key value` lines (`inbox_max_bytes 16MiB` ...) (ctl.go:252-258). Needs no server. Run: exit 0. No JSON. Values are formatted (`16MiB`, `30d`), not raw numbers.

### permissions NAME [--nsc]
- Output: **JSON by default**, always (`json.MarshalIndent` of `madmin.Permissions`, ctl.go:248). It is a NATS `{"pub":{"allow":[...]},"sub":{...}}` object. No version or schema field. `--nsc` prints one flag per line for `mapfile` (ctl.go:245). A note goes to stderr (ctl.go:242). Run: confirmed.
- Exit: 0; 2 bad usage; 1 no owner. No server needed.
- **[F] The stderr note is wrong.** It says `add` gives narrower permissions (ctl.go:666). `add` mints with the same `admin.Permissions(owner, name)` (mint.go:73, add.go:264-266).

### provision NAME --user U --account A [--note] [--role] [--host] [--label k=v]... [--describe] [--cap]...
- Input: flags only. Takes public keys; it makes no credentials. The user key must already exist (made by nsc or by the wrapper). Three-token form needs `agent_token` (newform.go).
- Output: one text line `provisioned NAME: inbox inbox-KEY, mail SUBJECT...` (ctl.go:326). No JSON.
- Exit: 0; 1 refusal or failure (including `already provisioned`); 2 usage. With `--label` it takes the account lock and is refused on an adopted account (ctl.go:294-300; mashctl.md:148). [F]
- Rerun: **not idempotent**: exit 1 `endpoint NAME is already provisioned` (admin.go:325). [F]

### set-meta NAME [--role] [--host] [--label k=v]... [--describe] [--cap]... [--uncap]...
- Input: flags only; `-` clears a field (ctl.go:80, 685-711).
- Output: one text line `updated NAME: role "R", host "H", labels k=v ...` (ctl.go:362). No JSON.
- Exit: 0; 1; 2. `--label` is refused on an adopted account. Rerun safe, same output. [F]

### suspend / resume / suspended NAME
- Output (text): `suspended NAME: id ID, last_seq N, msgs M (...)`; `resumed NAME`; `suspended NAME since T: id ID, last_seq N, msgs M` or `NAME is not suspended` (ctl.go:373, 378, 385-388). The `id` and `last_seq` from `suspend` are the inputs `deprovision --drained-to --suspension` needs, so a wrapper must parse them from text.
- Exit: suspend 0/1/2; resume 0/1/2; **suspended: 0 suspended, 3 not suspended, 1 error, 2 usage** (ctl.go:384-386). [F]
- Rerun: `suspend` again returns the existing id, exit 0 (suspend.go:81, 103-104). `resume` of a not-suspended inbox returns nil, exit 0 (suspend.go:201-202), and still prints `resumed`. [F]

### deprovision NAME [--user U | --drained-to SEQ --suspension ID] [--no-revoke]
- Input: flags only. Revokes the user key in the account JWT first (needs sys creds, `MASH_SYS_CREDS`, and `MASH_SERVERS` for a cluster), then removes record and inbox (ctl.go:430-447). `--no-revoke` skips it and warns on stderr (ctl.go:35).
- Output: text `deprovisioned NAME (user key U... revoked in the account JWT)` and revoke lines (`revoked user key ...: N server(s) acknowledged`, revoke.go:52-59). No JSON. Only 8 characters of the user key are printed.
- Exit: 0; 1 any refusal or failure (also name not found: `no endpoint named NAME`, ctl.go:542-546); 2 usage. An aliased legacy endpoint is refused with exit 1 (ctl.go:450-453).
- Rerun: second run exits 1 `no endpoint named` (ctl.go:545-546); the admin call itself is idempotent, the verb is not (mashctl.md:344). Allowed on an adopted account and still revokes (mashctl.md:152). [F]

### list
- Output: tab-separated text, no header: `KEY<TAB>USER<TAB>CREATED<TAB>NOTE` (ctl.go:564). **No JSON, and no labels, role, host, description or caps.** `list` does not show what `who --json` shows.
- Exit: 0; 1; 2 on args. Read only.

### audit [--json] [--strict] [--creds-dir D] [--compare EARLIER.json]
- Input: flags; optional earlier report as a JSON file.
- Output: `--json` prints one report, `version: 1` (audit.go:30, 258). It is documented (docs/audit.md; mashctl.md:272) and has a golden file (cmd/mashctl/testdata/audit.golden.json). Version changes only when a field is removed or changes meaning (audit.go:29). Fields: `generated`, `owner`, `account` (public key), `creds_dirs`, `records`, `renames`, `orphans`, `fenced_leases`, `retired`, `problems`, `notes`, `summary`, `compare`. Text form is the same data. JSON carries creds file names of orphan creds (`orphans.creds[].file`) but never a seed.
- Exit: 0 clean, or findings without `--strict`; 1 `--strict` findings; 2 usage; 3 could not complete (no server, bad `--compare` file, unreadable required input) (audit.go:24-25, 646-682). Documented (mashctl.md:272, audit.md). [F]
- **With `--strict` and findings, the JSON is on stdout and exit is 1 and the count line is on stderr** (audit.go:716-742). [F]
- Idempotent, read only. It is the only machine-readable listing of the directory (records have `key`, `form`, `user`, `running`, `inbox`) but has no labels, role or host (audit.go:96-103).

### inbox inspect ADDRESS [--json] [--legacy] [--from] [--to] [--limit]
- Output: `--json` has `schema: "mash-inbox-inspect@1"` (admin/inbox_inspect.go:20; inbox_inspect.go:149). Golden file inbox_inspect.golden.json. Documented in mashctl.md:274-310. `delivery_count` and `ack_time` are always null (inbox_inspect.go:143-144). Text words a state differently from JSON (`delivered, ack not confirmed` vs the raw state, inbox_inspect.go:26).
- Exit: 0; 1 refusal (unknown endpoint, no stream); 2 usage; 3 could not complete (inbox_inspect.go:21-23, 218-234). [F]
- Read only; never creates a consumer.

### adopt --server URL --operator-key F --account-key F --sys-creds F [--account N] [--admin-creds F | --mint-admin] [--owner O] [--dry-run]
- Input: flags and three seed files (0600). Not JSON.
- Output: text only, a numbered check list, then steps, then `Next:` prose (adopt.go:181-431). No JSON. Seeds are not printed. Writes: imports keys into the key store, copies sys creds, writes config.json if absent; with `--mint-admin` writes the admin creds file and records it in the issue record.
- Exit: 0; 1 failure or refused check; 2 missing flag. `--dry-run` exits 0 and writes nothing. [F]
- Rerun: safe; each step says `already stored` or `already there` (adopt.go:344-362). [F]

### add NAME [--label k=v]... [--tool] [--note] [--describe] [--cap]...
- Input: flags only; legacy-form name only (`config.ValidName`, add.go:197). Needs: admin creds, key store with an account key, **and sys creds** (the zone guard connects with them, add.go:260, zoneguard.go:401-404; wrapper must set `MASH_SYS_CREDS`).
- Output: one text line `added NAME creds /path/NAME.creds` (add.go:312). **The creds path is in the text only.** Creds go to the file, never to stdout. No JSON.
- Exit: 0; 1 any failure; 2 bad usage or bad name (add.go:190, 199). Refused on an adopted account (mashctl.md:148). [F]
- Rerun: not idempotent. Exit 1 `endpoint "NAME" already exists ... (to rotate its user, run: mashctl reissue NAME)` (add.go:250-251), or `creds file ... exists` (254-256). A killed add can leave an orphan; the fix is `retire NAME --force --delete-creds` then `add` again (add.go:119-122). Add rolls back its own record if the creds write fails (add.go:292-309). [F]

### reissue NAME... [--widen] | --all --yes | --changed --yes
- Input: flags; reads the creds file of each name. No JSON.
- Output: text. `reissued NAME` or `reissued NAME (new user key, old key revoked)` (reissue.go:195-197). Bulk: `will reissue NAME: REASON` lines, then `nothing to reissue` when none (reissue.go:153-160). Creds go to the creds file; a staged copy goes to `NAME.creds.next` (reissue.go:120).
- Exit: 0; 1 any per-name failure (the run goes on to the other names and exits 1 at the end, reissue.go:189-199); 2 usage. **Exit 2 is also the answer for a bulk run without `--yes`** after it prints the list (reissue.go:163-166). [F]
- Rerun: safe; a key-keeping reissue prints `reissued NAME` each time. An interrupted rotation is finished by running it again (reissue.go:31-40). Refused on an adopted account. [F]

### retire NAME [--delete-creds] [--force] [--no-revoke]
- Input: flags only; needs sys creds and `MASH_SERVERS` for a cluster (retire.go:70, 154).
- Output: text. `retired NAME`, `creds file renamed to PATH ...`, then revoke lines (retire.go:235-247). No JSON. The renamed creds path is in the text only (`NAME.creds.retired-<UTC time>`, retire.go:223).
- Exit: 0; 1 failure; 2 usage. **Mixed outcome:** if the server removal worked and the creds file move failed, it prints `retired NAME` and exits 1 (retire.go:202-248). [F]
- Rerun: second run exits 1 `no directory record on the server` unless `--force` (retire.go:194-198). With `--force` it only handles the creds file and exits 0. Refused on an adopted account. [F]

### label NAME k=v... [--dry-run] / unlabel NAME k... [--dry-run]
- Output: text: `labeled NAME: k=v`, `NAME: nothing to change`, or `dry run: would have labeled NAME (nothing written)` (label.go:147, 152, 164). No JSON. `--dry-run` does not say which labels.
- Exit: 0 for changed, unchanged and dry run alike; 1; 2. `unlabel` of an unset label warns on stderr and still exits 0 (label.go:127). Refused on an adopted account. Rerun safe. [F]

### unzone NAME [--sys-creds FILE]
- Output: text: `unzoned NAME: ...` plus a next-step line, or `NAME is already five-token` (unzone.go:265-269). No JSON.
- Exit: 0 (both outcomes); 1; 2. Rerun safe. Needs sys creds always (unzone.go:251). [F]

### issued list [--json]
- Output: `--json` prints `{"present","count","issued":[...]}` (issued.go:88-93), each entry `public_key, address, owner, kind, tags, generation, issued_at, purpose, revoked_at` (store/issued.go:103-122). No `version` or `schema` field. Documented as field names only (mashctl.md:67-78); no golden file for it. Text has the same fields as tab columns plus `N issued`.
- Exit: 0; **2 for every failure including bad config and no server** (issued.go:104, 123, 127); there is no code 1 for `list`. [F]
- Read only.

### issued check [--json] [--sys-creds F] [--servers N]
- Output: `--json` has `clean, complete, incomplete, account, record_present, servers, unrecorded, revoked_connected, recorded_not_connected, summary` (issued.go:219-231; mashctl.md:78). Documented. Slices never nil. No version field. No golden file.
- Exit: 0 clean and complete; 1 finding (even if incomplete); 2 nothing found but incomplete, or any failure (issued.go:27-37). Documented. [F]
- Read only; scans connections on every server.

### issue serve --test-account [--list-set F] [--registry F] [--sys-creds F] [--servers N]
- Long running. Stops on SIGINT or SIGTERM and exits 0 (issuecmd.go:48, 207-216). Needs `--test-account` (issuecmd.go:131). **`mashctl help` omits `--test-account` for this verb** (ctl.go:139 vs issuecmd.go:30, 131). [F]
- Input: list set file (default `<config dir>/listset.json`, JSON) and wrangler registry file (JSON, `wranglers.json` in key dir).
- Output: nothing on stdout. Log lines go to stderr as slog text (`issue service listening` ... `issue service stopped`, issuecmd.go:179, 211, 215). No health endpoint, no status command, no ready file.
- Exit: 0 clean stop; 1 failure; 2 usage or config load failure (issuecmd.go:148-149). A wrapper supervises by process liveness and by reading stderr. [F]

### wrangler init --test-account | add --host H --group G --key PUB | list | remove --host H --group G
- Input: flags; edits `wranglers.json` in the key dir under a lock.
- Output: text. `list` prints `HOST<TAB>GROUP<TAB>KEY` (issuecmd.go:318). `add` prints `registered wrangler key K... for host H, group G`. No JSON on the command. The registry file itself is JSON `{account, test_account, entries[{host,group,wrangler_key}]}` (wranglerreg.go:42-51), no version field, and a wrapper could edit it directly. [I]
- Exit: 0; 1 failure; 2 usage and config errors (issuecmd.go:257-258) but 1 for a missing key store (issuecmd.go:262-266).
- Rerun: `init` 0 `already exists`; `add` of an existing entry is a silent no-op, exit 0 (wranglerreg.go:237); `remove` of a missing entry exits 1 (issuecmd.go:339). [F]

### policy adopt --submitter KEY [--sys-creds F] [--exempt USER_KEY]... | --show [--json]
- Output: set mode prints text only (adopted / already adopted / exempt updated / recovery rotation, then `adoptedNote`, policy_adopt.go:167-181). `--show --json` prints `{"adopted": bool, "marker": {...}}` (policy_adopt.go:190-193). Documented (mashctl.md:138). No version field. No golden file.
- Exit: 0 all success outcomes (adopted, unchanged, exempt updated, recovery); 1 failure; 2 usage. `--show` exits 1 when the marker cannot be read. [F]
- Rerun: same submitter and exempt set: 0 and `nothing was changed`. A different submitter is a recovery rotation. **There is no un-adopt** (policy_adopt.go:186-187). Taking this step changes what other commands do (see section 4).

### policy serve --range R --servers N [--sys-creds F]
- Long running; exits 0 on SIGINT or SIGTERM (policy_serve.go:43-45, 217). Refuses to start unless the account is adopted, the policy bucket exists and the directory bucket exists (policy_serve.go:149-169).
- Output: nothing on stdout; slog text on stderr (policy_serve.go:171, 203, 216). Per-document outcome is **written to the KV key `status.<account>`** in the policy bucket, not printed (mashctl.md:176). No ready signal other than the log line `policy service listening`.
- Exit: 0 clean stop; 1 any failure including a failed `intake.Run`; 2 usage and config load failure (policy_serve.go:98). [F]
- Supervision: a wrapper reads process state, stderr, and `policy show --json` for the generation and per-server state. [I]

### policy reconcile [--json] [--listset F] [--range] [--bucket] [--account] [--sys-creds] [--servers] | --yes --plan DIGEST
- Dry run: `--json` prints the plan: `account, source, generation, signing_keys, revocations, findings, issuer_problems, actions, unknown, status, plan_digest, one_way` (reconcile/plan.go:142-156, render.go:89-95). Documented as "stable JSON (snake_case, sorted, slices never null)" (mashctl.md:221). Golden file policy_reconcile_dryrun.golden.json. No version field. The text form ends with `plan: sha256:HEX`; JSON has it as `plan_digest`.
- `--yes --plan`: text only (`revoked KEY: REASON`, `reconciled: N revoked`, policy_reconcile.go:424-439). `--json` with `--yes` is a usage error (line 144-145). One way.
- Exit: dry run 0 whether the plan has actions or not (the status is in the JSON `status`); 1 issuer check failed or any failure; 2 usage. JSON is on stdout and the error on stderr when exit is 1 (policy_reconcile.go:296-307). `--yes`: 0 done; 1 refusal, plan changed, or revocations on every server but a later step failed (policy_reconcile.go:432-436). [F]
- **The `mashctl help` line says `--yes needs a signer and is refused in this version`** (ctl.go:151) but the code and docs execute `--yes` (policy_reconcile.go:369-439; mashctl.md:200). Stale help text. [F]
- Rerun of `--yes`: the digest must match a fresh plan or it refuses (exit 1, `the plan changed since the dry run`). A plan with nothing to revoke prints `reconciled: 0 revoked`.

### policy show [--json] [--range] [--bucket] [--account]
- Output: `--json` prints `{list_set_in_force, header{in_force_generation, latest{generation,state,applied}, servers[{server,generation}], partial, notice}, view{...}}` (policydoc/read.go:35-69). Golden file policy_listset_show_json.golden. Documented in prose only (mashctl.md:100-124). No version field. The `view` object has `describes: "list_set"`.
- Exit: 0; 1 failure (no bucket, no connection); 2 usage. Needs `MASH_POLICY_BUCKET` or `--bucket` (and `--range` if the pattern has `{range}`). Read only. [F]

### policy explain SENDER RECEIVER [--json] ...
- Output: `--json` has `list_set_in_force, header, explanation{describes, sender, receiver, verbs[{verb, allowed, reason, detail}], open_subjects, raw_client_limit}` (policydoc/read.go:67-69; listset/explain.go:92-108). No golden for JSON; text goldens exist.
- Exit: **0 whether the answer is allow or deny** (test policy_listset_test.go:292-303). Failure 1, usage 2. The answer is in `verbs[].allowed`. [F]

### policy diff FILE [--json] [--submitter PUBKEY] ...
- Input: **FILE is read with `os.ReadFile`; no `-` for stdin** (policy_listset.go:282). JSON document with `schema: "mash.listset/1"` or compact JWS (decode.go:30, 84-92).
- Output: `--json` prints the plan: `refused, refusals[{stage,entry,reason}], receivers, templates, tags, rotate_and_revoke, new_credential, revoke, first_credential, size_limits, next_submitter, rotation` (listset/dryrun.go:37-61; policydoc/read.go:201-206). Documented in prose (mashctl.md:108-124). The JSON has no golden (text goldens only). No digest. No version field. Size is checked against default limits, not each server's (policy_listset.go:267-269).
- Exit: 0 plan accepted; **1 plan refused (JSON on stdout) and also 1 for every failure (unreadable file, no connection)**; 2 usage, or a first list set with no `--range` (policy_listset.go:305-308, 316-318). To tell refusal from failure a wrapper reads stdout. [F]

## 2. Exit code map (what each number means, per command)

| Code | Meaning, by command |
|---|---|
| 0 | success in all; also: `label` unchanged/dry run, `unzone` already done, `audit` with findings (no `--strict`), `reconcile` dry run with actions, `explain` deny, `policy adopt` unchanged |
| 1 | failure in all. Also: `audit --strict` findings; `issued check` finding; `retire` after the server removal worked but the creds file step failed; `policy diff` plan refused; `reconcile` issuer check failed or `--yes` partial; `deprovision`/`retire` of an unknown name; `reissue` any name failed |
| 2 | usage in most. **Also:** `issued` any failure; `issued check` incomplete; `issue serve` and `policy serve` and `wrangler` config-load failure; `reissue --all/--changed` without `--yes` |
| 3 | `suspended` = not suspended (not an error); `audit` and `inbox inspect` = could not complete |

[F] for each cell (sources above). The same fault gives different codes: no owner configured gives 1 for most verbs, 2 for `issued`, 3 for `audit` and `inbox inspect` (run: confirmed).

## 3. Summary table

JSON in = takes JSON or a file as input. JSON out = has `--json` or prints JSON. Schema = documented shape. Exit docs = exit codes documented. NI = non-interactive.

| Command | JSON/file in | JSON out | Schema documented | Exit codes documented | NI | Gap for a wrapper |
|---|---|---|---|---|---|---|
| init | no | no | n/a | partly | yes | one text line; no machine state |
| init --local | no | no | n/a | no | yes | paths only in prose; wrapper must compute them |
| limits | no | no | n/a | partly | yes | text lines |
| limits --show | no | no | n/a | no | yes | text, formatted units |
| permissions | no | yes (always, no flag) | no version | no | yes | wrong stderr note; no version |
| provision | no | no | n/a | partly | yes | text; second run is an error |
| set-meta | no | no | n/a | partly | yes | text only |
| suspend | no | no | n/a | partly | yes | `id` and `last_seq` only in text |
| resume | no | no | n/a | partly | yes | text only |
| suspended | no | no | n/a | yes (3) | yes | text; 3 is a state |
| deprovision | no | no | n/a | partly | yes | text; not idempotent; needs `id`/`last_seq` parsed from `suspend` |
| list | no | no | n/a | no | yes | tab text; no labels/role/host |
| audit | file (`--compare`) | yes | yes, `version`, golden | yes | yes | no labels/role/host; strict prints JSON plus exit 1 |
| inbox inspect | no | yes | yes, `schema`, golden | yes | yes | none beyond text-only error |
| adopt | seed files | no | n/a | no | yes | text only; many steps |
| add | no | no | n/a | partly | yes | creds path only in text; no `--out`; needs sys creds |
| reissue | no | no | n/a | yes | yes (needs `--yes`) | text; exit 2 means "confirm"; partial results by name |
| retire | no | no | n/a | partly | yes | exit 1 after the server removal worked; renamed path in text |
| label / unlabel | no | no | n/a | yes | yes | no JSON; unchanged and changed both exit 0 |
| unzone | no | no | n/a | partly | yes | text; needs sys creds |
| issued list | no | yes | field names only, no version, no golden | partly | yes | all failures exit 2 |
| issued check | no | yes | field names only, no version, no golden | yes | yes | no version |
| issue serve | file (JSON) | no | no | no | yes | no ready or health signal; help omits `--test-account` |
| wrangler * | no | no (file is JSON) | no | no | yes | text; no version in file |
| policy adopt | no | `--show` only | prose only | partly | yes | set mode is text; irreversible |
| policy serve | no | no (status in KV) | prose only | no | yes | status only in a KV key and stderr logs |
| policy reconcile | no | yes (dry run) | prose "stable", golden, no version | yes | yes (needs `--plan`) | `--yes` result is text; stale help text |
| policy show | no | yes | prose, golden, no version | yes | yes | needs bucket setting |
| policy explain | no | yes | prose, no JSON golden | yes | yes | exit 0 for allow and deny |
| policy diff | file (JSON or JWS) | yes | prose, no JSON golden, no version | yes | yes | file only, no stdin; exit 1 mixes refusal and failure |

### Endpoint side (one short section)
- **`mash status`**: text lines only, no `--json`, takes no arguments (main.go:165-168); exit 0 even with a config failure, since it is a report (status.go:620-624, main.go:212-214). A `status` MCP tool exists with the same text. [F]
- **`mash who --json`**: array of entries with `name, owner, online, role, host, note, description, ...`; `--long` adds labels and lease (whofilter.go:89-130). Stable schema stated in the code and docs (operations.md:189-195). Golden test golden_who_test.go. No version field. Exit 0 with `[]` for no match; 2 for bad flags. It needs endpoint credentials (`MASH_NAME`, creds), not admin creds, and it is asked of every endpoint (online state is live). It is the only JSON listing that shows labels, role and host. [F]
- **`mash policy show|explain --json`**: asks the range's responder with a signed request as the calling endpoint; prints that sender's own reach (policy.go:25-221). Exit 0 / 1 / 2. Needs `MASH_NAME` and a list-set credential; otherwise it answers locally with `noResponderText` (policy.go:28, 84-90). It is not the admin view that `mashctl policy show` gives. [F]

## 4. Gaps, ordered by how much each blocks a wrapper

1. **No machine-readable errors anywhere.** Every error is stderr text. A wrapper sees only an exit code and a string. The codes are not shared between commands (section 2).
2. **No JSON output for the verbs that change things:** `add, reissue, retire, deprovision, provision, set-meta, suspend, resume, label, unlabel, unzone, init, limits, adopt, policy adopt (set), policy reconcile --yes, wrangler *, issue serve`. A wrapper must parse sentences such as `added NAME creds PATH` (add.go:312) and `suspended NAME: id ID, last_seq N` (ctl.go:373).
3. **`add` has no way to name the creds path and prints it only in text.** The wrapper must set `MASH_CONFIG_DIR` per call or guess `<dir>/NAME.creds`, then read and ship the file. `add` also needs sys creds for the zone check (add.go:260). Creds files for 200 herds sit on the central host until moved.
4. **`list` is text and has no labels, role or host.** There is no admin command that returns the full directory record as JSON. `audit --json` is the closest and lacks labels. `who --json` has them but needs an endpoint identity and live presence.
5. **An adopted account refuses most of what the wrapper does.** After `policy adopt`: `add`, `reissue`, `retire`, `label`, `unlabel`, `set-meta --label`, `provision --label` are refused with exit 1 (mashctl.md:143-157). There is no un-adopt (policy_adopt.go:186-187). On an adopted account the wrapper can still `deprovision` (revokes), `suspend`, `set-meta` without labels. Onboarding on an adopted account has no mashctl path: `add` is refused; the doc says endpoints are made first, or through the issue service (mashctl.md:186).
6. **Ambiguous or reused exit codes.** `reissue --all` without `--yes` exits 2 (same as usage). `retire` exits 1 after the server side succeeded. `policy diff` exits 1 for both refused plan and failure. `issued` returns 2 for failures. `3` means different things (section 2). `explain` and `reconcile` dry run exit 0 for every answer. `label` and `unzone` exit 0 for changed and unchanged, with the difference only in text.
7. **Non-idempotent verbs give errors that mean "already done".** `provision`, `add`, `deprovision`, `retire`, `wrangler remove` exit 1 on a second run with text only to tell it from a real failure. A wrapper that retries must parse the message.
8. **Long-running services have no machine signal.** `policy serve` and `issue serve` print no ready line to stdout, have no health or status command, and log to stderr in slog text. The per-document result of `policy serve` is a KV key (`status.<account>`) that mashctl has no verb to read (only `policy show` shows it, in part, as `header.latest`). [I for the last clause; I did not read all of `policydoc.Show`.]
9. **JSON shapes without `version` or `schema`:** `permissions, issued list, issued check, policy adopt --show, policy show, explain, diff, reconcile` (only `audit` has `version`, only `inbox inspect` has `schema`). Documented in prose; `issued list`, `issued check`, `policy diff --json` and `explain --json` have no golden test.
10. **Input is flags only.** Labels, caps and many endpoints per call cannot be given as a JSON batch. 50 hosts and 200+ herds mean many process starts, each opening an admin NATS connection (ctl.go:195) with a 30 s default deadline (`admin_timeout`, ctl.go:734-738) and a 30 s account-lock wait (mashctl.md:157). `policy diff` reads only a file path. `reissue --all` is the only bulk verb.
11. **Config is process environment.** There is no `--url`, `--creds` or `--owner` on `add`, `retire`, `label`; a wrapper driving several servers or ranges sets a full environment per call. `MASH_SERVERS` must be set for any revoking command on a cluster (revoke.go:100; operations.md:177). [F]
12. **Help text and notes that disagree with the code:** `permissions` note (ctl.go:666 vs mint.go:73); `reconcile --yes` "refused in this version" (ctl.go:151 vs policy_reconcile.go:369); `issue serve` missing `--test-account` (ctl.go:139). Minor, but a wrapper author reading `help` gets the wrong picture.
13. **`init --local --start` can leave a running server with exit 1** (local.go:134-137). Minor for the large setup; matters for a test harness.

## 5. What a wrapper has to do today, for the three core jobs

### A. Onboard an endpoint (creds file plus directory record)

1. [F] Set the environment: `MASH_URL`, `MASH_OWNER`, `MASH_ADMIN_CREDS`, `MASH_KEYS_DIR`, `MASH_SYS_CREDS`, `MASH_SERVERS` (cluster), TLS, and `MASH_CONFIG_DIR` set to a directory it will read the creds from (add.go:134-141).
2. [F] Run `mashctl add NAME [--label k=v]... [--cap C] [--note N]`. Legacy-form names only: 1-48 lower-case letters, digits, single hyphens (add.go:197). Not available on an adopted account.
3. [F] Read the result from the exit code and the line `added NAME creds PATH` on stdout. Exit 1 plus `already exists` on stderr means the record is there.
4. [F] Read `PATH` (mode 0600). The file holds the seed. Move it to the host over the wrapper's own secure channel. mashctl has no export command. Then delete or archive the central copy (the wrapper's choice; `retire` renames it later).
5. [F] To verify: `mashctl audit --json` and find the record by `key`; or `mashctl list` and split on tabs. Neither shows labels. Labels can be read only from `mash who --long --json` run as an endpoint.
6. [F] On failure after a kill, run `mashctl retire NAME --force --delete-creds`, then `add` again (add.go:119-122).
7. [F] Alternative with keys made elsewhere: the wrapper makes the user key and JWT itself, then runs `mashctl provision NAME --user U --account A --label ...` for the record and inbox only. That path does not write the issue record, so `issued check` will report the key as unrecorded unless the wrapper records it. [I for the issued-check consequence: `add` records before provisioning, add.go:277; `provision` does not call RecordIssued, ctl.go:322.]
8. [F] To change labels afterwards: `mashctl label NAME k=v` (not on an adopted account) or `set-meta NAME --label k=v` (same).

### B. Apply or change policy

1. [F] One-time: choose a submitter key the wrapper (or the outside system) holds; mash must not hold it. Run `mashctl policy adopt --submitter PUBKEY --sys-creds FILE [--exempt USER_KEY]...`. Text output, exit 0. Check with `policy adopt --show --json` (`adopted: true`). There is no way back.
2. [F] Start `mashctl policy serve --range R --servers N` as a supervised process (set `MASH_POLICY_BUCKET`). It runs until SIGINT or SIGTERM. The wrapper must create the policy bucket itself; mash never does (mashctl.md:176).
3. [F] Build the list-set document: JSON with `schema: "mash.listset/1"`, `account`, `range`, `generation`, `base_generation`, `groups`, `senders`, `issued_at` (decode.go:84-92), or a compact JWS signed by the submitter key.
4. [F] Dry run: `mashctl policy diff FILE --json --submitter PUBKEY --range R`. Exit 0 accepted. Exit 1 refused: read `refused` and `refusals[]` from stdout; if stdout is empty it was a failure, not a refusal [I for this last test]. The file must be a path on disk.
5. [F] Submit: write the document to the KV key `listset.<account public key>` in the policy bucket, using NATS with the submitter's own credentials. **mashctl has no command for this step** (mashctl.md:176, 186; policy.go:86 lists no apply or push verb).
6. [F] Read the outcome: `mashctl policy show --json` (generation, state, per-server generation, `partial`) or the value at `status.<account>` in the same bucket. The stage names of a refusal are `adoption, signature, document, account, range, size, apply`.
7. [F] Test reach: `mashctl policy explain SENDER RECEIVER --json` and read `explanation.verbs[].allowed`; the exit code is 0 for both answers.
8. [F] After the first applied list set: `mashctl policy reconcile --json` (dry run, exit 0), read `status`, `actions`, `findings`, `plan_digest`; then `mashctl policy reconcile --yes --plan <plan_digest>`. Text result; exit 0 means done. The digest changes if the account changes between the two calls.
9. [F] Endpoints named in a list set must have an inbox first (mashctl.md:188): onboard them before adopting, or by the issue service.

### C. Revoke or retire an endpoint

1. [F] Set the same environment as A, with sys creds and `MASH_SERVERS` for a cluster (needed to push the account JWT to every server).
2. [F] Full retire: `mashctl retire NAME [--delete-creds]`. Revokes the user key, removes record and inbox, renames the creds file to `NAME.creds.retired-<UTC time>` (or deletes it). Text output, exit 0. Exit 1 after a printed `retired NAME` means the creds file step failed; the server side is done. Not available on an adopted account.
3. [F] On an adopted account use `mashctl deprovision NAME`: it still revokes (mashctl.md:152) but does not touch the creds file; the wrapper handles the file itself.
4. [F] To stop mail without revoking: `mashctl suspend NAME` (text: parse `id` and `last_seq`). The creds stay valid. Later, `mashctl deprovision NAME --drained-to SEQ --suspension ID` removes the endpoint only if nothing arrived since.
5. [F] A rerun after success exits 1 (`no directory record` or `no endpoint named`). The wrapper must treat that message as "already gone"; there is no structured way to tell.
6. [F] To confirm the revoke took effect: `mashctl issued check --json` (exit 1 and `revoked_connected` non-empty means the key is still connected) and `mashctl issued list --json` (`revoked_at` set).
7. [F] If revocation cannot be confirmed, the command exits 1 with `not confirmed`; running it again is safe (mashctl.md:330).

## 6. Ten-line summary

1. No mashctl command reads stdin or prompts; all can run non-interactively. The only confirm is `reissue --yes`.
2. Only `audit`, `inbox inspect`, `issued list|check`, `policy show|explain|diff|reconcile (dry run)|adopt --show` and `permissions` print JSON. Every command that changes state prints text only.
3. Only `audit` (`version: 1`) and `inbox inspect` (`schema: mash-inbox-inspect@1`) carry a version marker and a golden file. The other JSON shapes are documented in prose, or only partly.
4. No command returns a JSON error. Errors are stderr text, and the same fault gives exit 1, 2 or 3 depending on the command.
5. Exit 3 means "not suspended" for `suspended` and "could not complete" for `audit` and `inbox inspect`; exit 2 means "failed" for `issued`; `reissue --all` without `--yes` exits 2.
6. `add` writes creds only to `<config dir>/NAME.creds`, prints the path in text, needs sys creds, and is refused on an adopted account, as are `retire`, `reissue` and `label`.
7. `list` is tab text without labels, role or host; no admin command gives the full record as JSON.
8. Policy submission is a KV write by the submitter; mashctl has no verb for it. `policy serve` and `issue serve` give no ready or health signal beyond stderr logs.
9. Second runs of `provision`, `add`, `deprovision`, `retire` exit 1 with text that means "already done".
10. Details with file:line are in standalone/audit-json.md sections 0 to 5.
