# mashctl audit: the population, from its sources

`mashctl audit` lists mash's side of the population as the server and the creds directories hold it (its state, not its use, see [What it cannot see](#what-it-cannot-see)): every directory
record and every inbox stream in the account, which are running, which have the old form and which the new, the
state of each inbox, the orphans, and for every identity that carries an alias the drain state of its legacy inbox.
It is read only: it writes nothing to the server and nothing to disk, and it can be run at any time, on any number of
machines. It needs the same admin credentials and settings as the other `mashctl` verbs (`MASH_URL`,
`MASH_ADMIN_CREDS`, `MASH_OWNER`), and unlike the writing verbs it also runs when `agent_token` is set.

```text
mashctl audit [--json] [--strict] [--creds-dir DIR] [--compare EARLIER.json]
```

- `--json` prints the report as JSON; without it, the same facts as tab-separated text lines.
- `--creds-dir DIR` scans DIR for creds files instead of the directories `mashctl add` writes to (the config
  directory, and the legacy `~/.config/mash` when it is there).
- `--compare EARLIER.json` reads the JSON of an earlier `audit --json` and adds a comparison with it (see
  [`--compare`](#compare)). A file that is not JSON, or is another report version, is refused with exit 3.
- `--strict` exits 1 when there is a finding (see [Exit codes](#exit-codes)).

The output is the evidence for an audit, so it is deterministic: records, renames, orphans and problems are sorted
(by directory key, stream name, lease key and file path), and two runs against an unchanged account differ only in
`generated`, and in `running` when a lease expired or was taken between them.

## The report (`--json`)

Top level: `version` (always 1; it changes only when a field is removed or changes meaning), `generated` (RFC 3339,
UTC; the only timestamp, and the one a comparison ignores), `owner`, `account` (the account's public key, taken from the admin credentials' user JWT; a public key only, never a seed
or token; it ties a `--compare` file to one deployment), `creds_dirs` (the directories scanned),
`records`, `renames`, `orphans`, `fenced_leases`, `problems`, `notes`, `summary` and, only with `--compare`, `compare`.

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
when `false` the counts are 0), `msgs`, `first_seq`, `last_seq` (the sequence of the last message ever stored, which
only grows), `last_time` (when the stream last stored a message, RFC 3339 UTC; absent for a stream that never held
one), `subjects` (as configured, sorted; a suspended
stream shows the one parked subject), `suspended`, and `deliver`: the stream's durable consumer `deliver` with
`present`, `pending` (stored and not yet delivered), `ack_pending` (delivered and not yet acknowledged), and the
consumer's configuration: `deliver_policy` (`all`, `last`, `new`, `by_start_sequence`, `by_start_time` or
`last_per_subject`), `start_seq`, `start_time` (only when set), `filter_subjects` (empty when unfiltered), `ack_floor`
(the stream sequence up to which everything is acknowledged) and `delivered` (the highest stream sequence delivered).
For the comparison of two audits the consumer also shows `delivered_consumer_seq` and `ack_floor_consumer_seq` (the
same two points as consumer sequences), `delivered_last` and `ack_floor_last` (when each last moved, RFC 3339 UTC;
absent when the server reports none) and `num_redelivered` (messages redelivered and not yet acknowledged). The two
`*_last` times and `last_time` are the only fields besides `generated` that differ between two runs for a reason of
time alone; everything else is a sequence or a count.

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
- the consumer has no filter at all (`filter_subjects` is empty). A filter equal to the stream's current subjects still
  does not count: the stream may have captured other subjects earlier, and a filtered consumer skips the stored
  messages from them, so zero `pending` and an `ack_floor` at `last_seq` can hide unread mail. mash creates `deliver`
  unfiltered, so a filter means the consumer was recreated by hand;
- `ack_floor` has reached the stream's `last_seq`.

`drained` is not `unused`. A legacy stream can show `msgs` 0 (everything was read and acknowledged, or purged) while its
`last_seq` is still moving, because mail is still being sent to the old name and read as it arrives. `drained` says
only that nothing is waiting at the moment of the read. Whether the old name is still used is shown only by the
`last_seq` and the consumer's `delivered` of two audits, which is what `--compare` reports.

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
  `file`, `user` and `staged`. Read it as: a leftover from an interrupted `add`, a retired endpoint, or a rotation that never
  finished. The admin's and the system account's creds files (`MASH_ADMIN_CREDS`, `MASH_SYS_CREDS`) are found by
  their user key and are not listed. Only the user public key of the JWT is read from a creds file; the seed and the
  token are never kept or printed. A staged `*.creds.next` file (the credentials of an interrupted
  rotation) is read the same way, only its public user key, and treated like a creds file: it is an orphan when its
  user matches no record, and then has `staged: true`; one that cannot be read is a problem.

`fenced_leases` lists lease keys that hold only a deprovision tombstone and have no record. That is what a
deprovision leaves for up to 90 seconds, so it is not an orphan.

### `problems`

Things the audit could not read, each with `what` (`record` or `creds`), `item` and `error`: a directory value that
does not parse as a record, a record that names no identity, a record whose alias names no legacy identity (the record stays in `records`, and the inbox the alias's name would give is not reported as an orphan), a creds file or staged creds file that cannot be read (for example one that
other users can read). A problem is not a verdict on the item: fix it and run again. A directory entry that does not
parse still exists, so the audit claims by its key the inbox stream and the lease it would own, and by its raw `user`
field the creds file of that user; those are not listed as orphans (the problem is the finding).

### `notes`

A sorted list of strings for things skipped or absent that are not findings and never change the exit code: a
`*.creds` or `*.creds.next` file that is a symbolic link (skipped, so its user key is not matched and its user can
show up as an orphan elsewhere), and a default creds directory that does not exist (nothing was scanned there). They
are also printed as `note:` lines.

### `summary`

Counts: `records`, `legacy`, `new`, `running`, `renamed`, `undrained`, `orphan_streams`, `orphan_leases`,
`orphan_creds`, `problems`.

<a id="compare"></a>
## `--compare`: what moved between two audits

`mashctl audit --compare EARLIER.json` reads the JSON an earlier `audit --json` wrote and adds a `compare` object to
the report (and `compare` lines to the text form). It reads nothing else from disk and changes nothing. The earlier file's `version` must be
the one this mashctl writes; a file that is not JSON, is another version, or cannot be read exits 3.

The earlier file must also be a complete audit of this deployment, or a stub such as `{"version":1}` would pass for a
baseline with no legacy streams. It needs a non-empty `generated` and `owner`, the `records` and `renames` arrays (empty
is fine, absent or `null` is not) and, for each legacy inbox it lists, every field in the table below. A field counts
as present when its key is there and not `null`, so `"msgs": 0` is present and a missing `msgs` is not (a missing
count would otherwise read as 0 and make an inbox that later vanished look drained). Its `owner` must equal the current owner and,
when it has an `account`, that must equal the current account. Anything else is refused with exit 3, a message on
standard error naming what is wrong, and nothing on standard output. An earlier audit made before `account` existed
is accepted but cannot be tied to this deployment: `compare.account_unverified` is `true` and the text form says
`account not verified`. Without `--strict` it is only shown. With `--strict` it fails (exit 1) and standard error says
to regenerate the earlier audit with this mashctl.

<a id="earlier-fields"></a>
Fields an earlier report must carry for each legacy inbox (`records[].inbox` of form `legacy`, and
`renames[].legacy_inbox`). The refusal message names the JSON path of the first one missing, for example
`records[0].inbox.msgs`.

| Field | Required when | Read for |
| --- | --- | --- |
| `stream` | always | names the stream the inbox is compared by |
| `present` | always | whether the stream existed |
| `msgs` | the inbox is present | stored message count: 0 makes the inbox drained |
| `first_seq` | the inbox is present | drain rule: a start sequence at or below it |
| `last_seq` | the inbox is present | movement; the ack floor must reach it |
| `created` | the inbox is present | recreation check for the stream |
| `suspended` | the inbox is present | state of the stream the earlier audit recorded |
| `deliver` | the inbox is present | the `deliver` consumer object |
| `deliver.present` | the inbox is present | whether the consumer existed |
| `deliver.pending` | the consumer is present | drain rule: nothing pending |
| `deliver.ack_pending` | the consumer is present | drain rule: nothing unacknowledged |
| `deliver.delivered` | the consumer is present | movement of the delivered stream sequence |
| `deliver.ack_floor` | the consumer is present | drain rule: reaches last_seq |
| `deliver.created` | the consumer is present | recreation check for the consumer |
| `deliver.deliver_policy` | the consumer is present | drain rule: all or a start sequence |
| `deliver.start_seq` | the consumer is present | drain rule: with by_start_sequence |
| `deliver.filter_subjects` | the consumer is present | drain rule: any filter disqualifies |

Other fields the writer omits when empty or unknown, and that the comparison does not read (`last_time`, `subjects`,
`start_time`, `delivered_last`), are not required.

`compare` holds `against_generated`, `moved`, `identities_removed` and `identities_added` (directory keys present
before and not now, and the reverse) and `streams`: one entry per legacy inbox stream in either audit (the inbox of an
old-form record and the legacy inbox of a renamed identity), sorted, each with `present_before`, `present_now` and a
before, now and moved triple for `last_seq`, for the `deliver` consumer's `delivered` (stream sequence) and for its
`ack_floor`, and two flags, `vanished` and `retired`. A stream has moved when it is present now and its `last_seq` or
its `delivered` differs: mail was stored or mail was read. The ack floor is shown but does not count by itself.
`moved` is the number of streams that moved.

A legacy inbox that was present in the earlier audit and is absent now has either `vanished` or `retired`. The
`*_before` fields keep what the earlier audit showed.

- `retired`: the inbox was drained in the earlier audit and its directory record is gone now as well (it was
  deprovisioned). It is shown and does not fail `--strict`. "Drained" is the audit's own rule for a legacy inbox (see
  `renames[].drained`): the stream held no message, or its `deliver` consumer had nothing pending, nothing
  unacknowledged and an ack floor that covered every stored message. For a plain legacy record the earlier JSON has
  no `drained` field, so the rule is applied to its earlier `inbox`.
- `vanished`: every other inbox that disappeared: it held mail that was not drained, or its record is still in the
  directory (so the stream was deleted or purged under a live record). A vanished inbox is the audit's signal that
  mail may have been lost. `vanished` in the report is the number of such streams.

A stream that is present now and was absent before is not a vanished one; its record shows in `identities_added`.

### Recreated streams

Equal sequences do not prove a stream is the same stream: a legacy stream deleted and made again can be refilled to
the same `last_seq`, `delivered` and `ack_floor`. So each inbox in the audit JSON carries `created`, when the server
created the stream, and its `deliver` object carries `created`, when the server created the consumer. Both are RFC 3339
in UTC with the nanoseconds the server gives, and are left out only when the server gives a zero time.

When a stream is present in both audits and its `created`, or its `deliver` consumer's `created`, differs, the
`streams` entry has `recreated: true` (with `created_before`, `created_now`, `deliver_created_before` and
`deliver_created_now` shown), `compare.recreated` counts it, the text form prints `RECREATED`, and `--strict --compare`
exits 1 whatever the sequences say.

An earlier audit made before these fields existed cannot prove anything, so it is refused (exit 3) with the path of
the missing field and a note to regenerate it: run `mashctl audit --json` again with this mashctl, and compare a later
audit against that file. When the server gives no creation time in the current audit for a stream present in both, the
entry has `creation_unknown: true`, `compare.creation_unknown` counts it and the text form prints `UNKNOWN`; without
`--strict` it is only shown and with `--strict` it fails (exit 1).

The `created` fields are additive, so the report `version` stays 1.

With `--strict`, `--compare` fails (exit 1) when `moved`, `vanished`, `recreated` or `creation_unknown` is above 0 or `account_unverified` is true, and on every other finding: any
orphan, any alias whose legacy inbox is not drained, any problem.

## What it cannot see

The audit reads the state of the server: the directory, the streams, the consumers, the leases and the creds files.
It does not see use. In particular it cannot see:

- publishes to a legacy subject that has no stream: the mail goes nowhere and leaves no trace here;
- connections: who is connected, with which credentials, and to which subjects they publish or subscribe;
- an endpoint that has no stream, and a client that publishes or consumes without being an endpoint.

What it can show is that a legacy stream stored a message (`last_seq`, `last_time`) or had one read (`delivered`,
`delivered_last`) between two audits. An old-form sender writing to a name with no stream is invisible to it.

So "two clean audits a day apart" means exactly this: both runs were `--strict` clean (exit 0), and `--compare` of the
second against the first shows no legacy movement (`moved`, `vanished`, `recreated` and `creation_unknown` all 0, with `--strict` the second exits 0). That is evidence
that no stream stored or delivered mail in that day. It is not evidence about publishes or connections the audit
cannot see.

## Exit codes

| code | meaning |
| --- | --- |
| 0 | the audit completed and is clean, or it has findings and `--strict` was not given |
| 1 | `--strict` and a finding: an orphan of any kind, an alias whose legacy inbox is not drained, a problem (an item the audit could not read, which it cannot call clean) or, with `--compare`, a legacy stream whose `last_seq` or `delivered` moved, a legacy inbox that vanished, a legacy stream or its `deliver` consumer that was recreated, or an earlier audit that lacks the account, or a current audit with no creation time for a stream present in both. Standard error says what was counted |
| 2 | a usage error |
| 3 | the audit could not complete: no server or no credentials, an unreadable required creds directory (`--creds-dir`), or a `--compare` file that is unreadable, not JSON, another version, incomplete (including one without creation times), or from another owner or account. Nothing is printed on standard output |

## The drain check before a break

Before the transition grant on a legacy stream is revoked, run `mashctl audit --strict --json > day1.json` and read
`renames[].drained`: every renamed identity must show `drained: true`, that is the rule above on its
legacy inbox (nothing pending, nothing unacknowledged, and a consumer that covers every stored message). A day later run
`mashctl audit --strict --compare day1.json`. Two clean audits a day apart are both exits 0 and a `compare` that shows
no legacy movement, with the limits in [What it cannot see](#what-it-cannot-see).
