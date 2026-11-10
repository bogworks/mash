# mashctl audit: the population, from its sources

`mashctl audit` lists mash's side of the population as the server and the creds directories hold it: every directory
record and every inbox stream in the account, which are running, which have the old form and which the new, the
state of each inbox, the orphans, and for every identity that carries an alias the drain state of its legacy inbox.
It is read only: it writes nothing to the server and nothing to disk, and it can be run at any time, on any number of
machines. It needs the same admin credentials and settings as the other `mashctl` verbs (`MASH_URL`,
`MASH_ADMIN_CREDS`, `MASH_OWNER`), and unlike the writing verbs it also runs when `agent_token` is set.

```text
mashctl audit [--json] [--strict] [--creds-dir DIR]
```

- `--json` prints the report as JSON; without it, the same facts as tab-separated text lines.
- `--creds-dir DIR` scans DIR for creds files instead of the directories `mashctl add` writes to (the config
  directory, and the legacy `~/.config/mash` when it is there).
- `--strict` exits 1 when there is a finding (see [Exit codes](#exit-codes)).

The output is the evidence for an audit, so it is deterministic: records, renames, orphans and problems are sorted
(by directory key, stream name, lease key and file path), and two runs against an unchanged account differ only in
`generated`, and in `running` when a lease expired or was taken between them.

## The report (`--json`)

Top level: `version` (always 1; it changes only when a field is removed or changes meaning), `generated` (RFC 3339,
UTC; the only timestamp, and the one a comparison ignores), `owner`, `creds_dirs` (the directories scanned),
`records`, `renames`, `orphans`, `fenced_leases`, `problems` and `summary`.

### `records`: one per directory record, sorted by `key`

| field | meaning |
| --- | --- |
| `key` / `flat` | the directory key: the bare name for an old-form record, `agent_owner_name` for a new-form one. Also the key of its lease and the suffix of its inbox stream |
| `identity` | `agent`, `owner` and `name`. An old-form record has the agent token `cc` |
| `form` | `legacy` (the old form: a bare name under the account's owner) or `new` (three tokens) |
| `user` | the user public key the record names (a public key, never a seed) |
| `zone` | the record's zone, only when it has one |
| `alias` | `owner` and `name` of the old-form identity a new-form record also answers to; only when it has one |
| `running` | `true` when the lease bucket holds a live lease under the record's key, `false` when it holds none or only a deprovision tombstone. A lease expires 90 seconds after its last refresh, so `running` is the state at the moment of the read |
| `inbox` | the record's inbox stream, below |

The directory record has no field that says where it came from, so the report has no `source` field.

`inbox` (also `legacy_inbox` and `new_inbox` under `renames`): `stream` (`inbox-<key>`), `present` (the stream exists;
when `false` the counts are 0), `msgs`, `first_seq`, `last_seq`, `subjects` (as configured, sorted; a suspended
stream shows the one parked subject), `suspended`, and `deliver`: the stream's durable consumer `deliver` with
`present`, `pending` (stored and not yet delivered), `ack_pending` (delivered and not yet acknowledged), and the
consumer's configuration: `deliver_policy` (`all`, `last`, `new`, `by_start_sequence`, `by_start_time` or
`last_per_subject`), `start_seq`, `start_time` (only when set), `filter_subjects` (empty when unfiltered), `ack_floor`
(the stream sequence up to which everything is acknowledged) and `delivered` (the highest stream sequence delivered).

The text form shows the same per inbox on one line: `suspended`, `deliver` (`true` or `false`) and, when a consumer
exists, `policy`, `start_seq`, `filter`, `ack_floor` (and `start_time` when set). An orphan stream line shows
`suspended` too.

### `renames`: one per record that carries an alias, sorted by key

Each entry puts the two inboxes of one renamed identity side by side, so two audits can be compared for what must not
change (INV-001 to INV-003): `identity` (`agent.owner.name`), `flat`, `alias`, `legacy_inbox` (the stream of the
old-form identity, "before"), `new_inbox` ("after"), `drained`, `trust_label` and `trust_label_note`.

`trust_label` is always `not-available`: the server holds no trust label. The receiving endpoint evaluates it from
the signed sender header when it reads a message, so it cannot be listed here. What the server can show is the
message count and the first and last sequence of each stream, and those are in the two inboxes.

`drained` is the drain check of a break procedure. It is `true` when the legacy stream is gone or holds no message.
Otherwise every one of these must hold, and `drained` is `false` if any fails:

- the `deliver` consumer exists (a stream that still holds messages but has no consumer has nothing to read them);
- `pending` is 0 and `ack_pending` is 0;
- the consumer's configuration would have delivered every stored message: `deliver_policy` is `all`, or
  `by_start_sequence` with `start_seq` at or below the stream's `first_seq`. `new`, `last`, `last_per_subject` and
  `by_start_time` skip stored messages (a start time cannot be checked against them), so they never count as drained
  while messages are stored;
- the consumer has no filter other than the stream's own subjects;
- `ack_floor` has reached the stream's `last_seq`.

The counts alone are not the rule: a consumer deleted and recreated to skip the stored messages shows `pending` 0 and
`ack_pending` 0 while those messages are unread. That is why the policy, start sequence and filter are in the report.

### `orphans`: things with no directory record

- `streams`: an inbox stream that is neither a record's inbox nor the legacy inbox an alias names. Read it as: an
  endpoint was removed without its inbox, or an inbox was created by hand. Its messages are still stored and still
  readable by an admin. Each has `stream`, `msgs`, `first_seq`, `last_seq`, `subjects` and `suspended`.
- `leases`: a lease key that is no record's key and is not a deprovision tombstone. Read it as: a session holds or
  recently held a name nobody provisioned (or the record was deleted by hand while it ran). The admin's own lock keys
  (`limits-lock.*`) are not leases and are not listed.
- `creds`: a `*.creds` file in the scanned directories whose user public key matches no record's `user`. Each has
  `file` and `user`. Read it as: a leftover from an interrupted `add`, a retired endpoint, or a rotation that never
  finished. The admin's and the system account's creds files (`MASH_ADMIN_CREDS`, `MASH_SYS_CREDS`) are found by
  their user key and are not listed. Only the user public key of the JWT is read from a creds file; the seed and the
  token are never kept or printed. Staged `*.creds.next` files are not scanned.

`fenced_leases` lists lease keys that hold only a deprovision tombstone and have no record. That is what a
deprovision leaves for up to 90 seconds, so it is not an orphan.

### `problems`

Things the audit could not read, each with `what` (`record` or `creds`), `item` and `error`: a directory value that
does not parse as a record, a record that names no identity, a creds file that cannot be read (for example one that
other users can read). A problem is not a verdict on the item: fix it and run again. A directory entry that does not
parse still exists, so the audit claims by its key the inbox stream and the lease it would own, and by its raw `user`
field the creds file of that user; those are not listed as orphans (the problem is the finding).

### `summary`

Counts: `records`, `legacy`, `new`, `running`, `renamed`, `undrained`, `orphan_streams`, `orphan_leases`,
`orphan_creds`, `problems`.

## Exit codes

- 0: the audit completed, whatever it found.
- 1: the audit could not complete (no server, no credentials, an unreadable creds directory), or `--strict` found
  something: an orphan of any kind, an alias whose legacy inbox is not drained, or a problem (an item the audit could
  not read, which it cannot call clean). A `--strict` run says what it counted on standard error.
- 2: a usage error.

## The drain check before a break

Before the transition grant on a legacy stream is revoked, run `mashctl audit --strict` (or `--json` and read
`renames[].drained`): every renamed identity must show `drained: true`, that is the rule above on its
legacy inbox (nothing pending, nothing unacknowledged, and a consumer that covers every stored message). Two clean audits a day apart (`--strict` exit 0 both times, and equal output apart from `generated`) are
the evidence.
