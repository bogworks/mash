# mashctl audit: the population, from its sources

`mashctl audit` lists mash's side of the population as the server and the creds directories hold it (its state, not its use, see [What it cannot see](#what-it-cannot-see)): every directory
record and every inbox stream in the account, which are running, the state of each inbox, and the orphans.
It is read only: it writes nothing to the server and nothing to disk, and it can be run at any time, on any number of
machines. It needs the same admin credentials and settings as the other `mashctl` verbs (`MASH_SERVER`,
`MASH_ADMIN_CREDS`, `MASH_ENDPOINT`).

```text
mashctl audit [--json] [--strict] [--creds-dir DIR] [--compare EARLIER.json]
```

- `--json` prints the report as JSON; without it, the same facts as tab-separated text lines.
- `--creds-dir DIR` scans DIR for creds files instead of the config directory, where creds files are written.
- `--compare EARLIER.json` reads the JSON of an earlier `audit --json` and adds a comparison with it (see
  [`--compare`](#compare)). A file that is not JSON, or is another report version, is refused with exit 1.
- `--strict` exits 1 when there is a finding (see [Exit codes](#exit-codes)).

The output is the evidence for an audit, so it is deterministic: records, orphans and problems are sorted
(by directory key, stream name, lease key and file path), and two runs against an unchanged account differ only in
`generated`, and in `running` when a lease expired or was taken between them.

## The report (`--json`)

Top level: `version` (always 2; it changes only when a field is removed or changes meaning; `--compare` refuses a version 1 file), `generated` (RFC 3339,
UTC; the only timestamp, and the one a comparison ignores), `owner`, `account` (the account's public key, taken from the admin credentials' user JWT; a public key only, never a seed
or token; it ties a `--compare` file to one deployment), `creds_dirs` (the directories scanned),
`records`, `orphans`, `fenced_leases`, `retired`, `problems`, `notes`, `summary` and, only with `--compare`, `compare`.

### `records`: one per directory record, sorted by `key`

| field | meaning |
| --- | --- |
| `key` | the directory key: the flat key `owner_group_instance`. Also the key of its lease and the suffix of its inbox stream `inbox-<key>` |
| `address`, `owner`, `group`, `instance` | the endpoint's address `owner.group.instance` and its three tokens |
| `user` | the user public key the record names (a public key, never a seed) |
| `running` | `true` when the lease bucket holds a live lease under the record's key, `false` when it holds none or only a deprovision tombstone. A lease expires 90 seconds after its last refresh, so `running` is the state at the moment of the read |
| `inbox` | the record's inbox stream, below |

The directory record has no field that says where it came from, so the report has no `source` field.

`inbox`: `stream` (`inbox-<key>`), `present` (the stream exists;
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

Each inbox also has `bytes`, `max_bytes`, `fill_percent`, `evicted` and `evicted_unread`. `evicted_unread` is a lower
bound: it comes from the consumer's sequences, so mail that was fetched but never shown, or that was redelivered, can
make it read low, and 0 does not prove nothing was lost unread.

The text form shows the same per inbox on one line: `suspended`, `deliver` (`true` or `false`) and, when a consumer
exists, `policy`, `start_seq`, `filter`, `ack_floor` (and `start_time` when set). An orphan stream line shows
`suspended` too.

### `orphans`: things with no directory record

- `streams`: an inbox stream that is not a record's inbox. Read it as: an
  endpoint was removed without its inbox, or an inbox was created by hand. Its messages are still stored and still
  readable by an admin. Each has `stream`, `msgs`, `first_seq`, `last_seq`, `subjects` and `suspended`.
- `leases`: a lease key that is no record's key and is not a deprovision tombstone. Read it as: a session holds or
  recently held a name nobody provisioned (or the record was deleted by hand while it ran). The admin's own lock keys
  (`limits-lock.*`) are not leases and are not listed.
- `creds`: a `*.creds` file in the scanned directories whose user public key matches no record's `user`. Each has
  `file`, `user` and `staged`. Read it as: a leftover from an interrupted endpoint creation, a retired endpoint, or a rotation that never
  finished. The admin's and the system account's creds files (`MASH_ADMIN_CREDS`, `system_creds` in `config.json`) are found by
  their user key and are not listed. Only the user public key of the JWT is read from a creds file; the seed and the
  token are never kept or printed. A staged `*.creds.next` file (the credentials of an interrupted
  rotation) is read the same way, only its public user key, and treated like a creds file: it is an orphan when its
  user matches no record, and then has `staged: true`; one that cannot be read is a problem.

`fenced_leases` lists lease keys that hold only a deprovision tombstone and have no record. That is what a
deprovision leaves for up to 90 seconds, so it is not an orphan.

### `retired`

The retired-identity bucket `mash-retired` ([ADR 0088](decisions/0088-a-retired-new-form-name-is-burned-in-a-durable-bucket-by-a-two-phase-marker-that-is-never-deleted.md)): one marker per address that was deprovisioned, never deleted once `final`. The audit only reads it; it never creates the bucket. Always present in the report:

| field | meaning |
| --- | --- |
| `present` | `false` when there is no bucket: no retirement was ever recorded. Not an error; the other fields are 0 and `markers` is empty. The text form says `no retired bucket` |
| `final` / `pending` | the number of markers in each state. A marker with no state (written before the two states existed) counts as `final` |
| `bytes` | bytes the bucket's stream stores |
| `max_bytes` | the bucket's cap: `67108864` (64 MiB, `store.RetiredMaxBytes`) for a bucket mash made. The audit reads the cap from the bucket, so one recreated with a larger cap is measured against its own. A bucket with no cap (`MaxBytes` 0 or -1) reports `max_bytes` 0, `percent_used` 0 and `alarm` false: the audit does not invent a cap |
| `percent_used` | `bytes` over `max_bytes`, 0 to 100, two decimals |
| `alarm` | `true` when `bytes` is at or over 50% of `max_bytes` |
| `markers` | one per marker, sorted by `key`: `key` (the flat key), `address`, `owner`, `group`, `instance`, `user` (the retired user public key; a public key, never a seed; left out when the marker has none), `state` (`final` or `pending`) and `retired_at` (RFC 3339 UTC, as the marker holds it) |

Findings, all in `problems` with `what` `retired` (so `--strict` exits 1):

- the alarm: `item` is `mash-retired` and `error` starts `ALARM`. A full bucket makes provisioning refuse new identities for good and mash never changes an existing bucket, so recreate it with a larger cap and its markers copied before it fills;
- a `pending` marker: `item` is its key and the error says to rerun `deprovision` for it (the retirement was started and not finished; the name stays refused meanwhile);
- a marker whose `state` is neither `pending`, `final` nor empty (a tampered or future-format marker): `item` is its key and `error` says it has an unrecognized state, shown quoted and cut at 40 characters. It counts as `final` (the name stays refused) and the report keeps `state` `final`, so reports stay comparable; the finding is what stops it passing as a completed retirement. An empty `state` is `final`, with no finding;
- a bucket with no size cap: `item` is `mash-retired` and `error` says it has no size cap. ADR 0088 gives it 64 MiB so a full bucket refuses new markers, and an uncapped one never alarms; recreate it with the cap, copying its markers. Bytes are still reported, and the text form says `bytes N, no cap`;
- a bucket whose configuration fails the fail-closed check (a TTL, another history, memory storage, an evicting stream; `item` is `mash-retired`) and a marker whose value cannot be read (`item` is its key). The audit reports them and goes on; it does not crash.

The text form prints `retired markers N (final F, pending P)` with the bucket, bytes, cap and percent, an `ALARM` line when it applies, and one `retired marker` line per marker. `summary` gains `retired_final` and `retired_pending`.

### `problems`

Things the audit could not read, each with `what` (`record`, `creds` or `retired`), `item` and `error`: a directory value that
does not parse as a record, a record that names no address, a creds file or staged creds file that cannot be read (for example one that
other users can read). A problem is not a verdict on the item: fix it and run again. A directory entry that does not
parse still exists, so the audit claims by its key the inbox stream and the lease it would own, and by its raw `user`
field the creds file of that user; those are not listed as orphans (the problem is the finding).

### `notes`

A sorted list of strings for things skipped or absent that are not findings and never change the exit code: a
`*.creds` or `*.creds.next` file that is a symbolic link (skipped, so its user key is not matched and its user can
show up as an orphan elsewhere), and a default creds directory that does not exist (nothing was scanned there). They
are also printed as `note:` lines.

### `summary`

Counts: `records`, `running`, `orphan_streams`, `orphan_leases`,
`orphan_creds`, `problems`, `retired_final`, `retired_pending`.

<a id="compare"></a>
## `--compare`: what moved between two audits

`mashctl audit --compare EARLIER.json` reads the JSON an earlier `audit --json` wrote and adds a `compare` object to
the report (and `compare` lines to the text form). It reads nothing else from disk and changes nothing. The earlier file's `version` must be
the one this mashctl writes; a file that is not JSON, is another version, or cannot be read exits 1.

The earlier file must also be a complete audit of this deployment, or a stub such as `{"version":1}` would pass for a
baseline with no inboxes. It needs a non-empty `generated` and `owner`, the `records` array (empty
is fine, absent or `null` is not) and, for each record's inbox, every field in the table below. A field counts
as present when its key is there and not `null`, so `"msgs": 0` is present and a missing `msgs` is not (a missing
count would otherwise read as 0 and make an inbox that later vanished look drained). Its `owner` must equal the current owner and,
when it has an `account`, that must equal the current account. Anything else is refused with exit 1, a message on
standard error naming what is wrong, and nothing on standard output. An earlier audit made before `account` existed
is accepted but cannot be tied to this deployment: `compare.account_unverified` is `true` and the text form says
`account not verified`. Without `--strict` it is only shown. With `--strict` it fails (exit 1) and standard error says
to regenerate the earlier audit with this mashctl.

<a id="earlier-fields"></a>
Fields an earlier report must carry for each record's inbox (`records[].inbox`). The refusal message names the JSON path of the first one missing, for example
`records[0].inbox.msgs`.

| Field | Required when | Read for |
| --- | --- | --- |
| `stream` | always | names the stream the inbox is compared by |
| `present` | always | whether the stream existed |
| `msgs` | the inbox is present | stored message count |
| `first_seq` | the inbox is present | a start sequence at or below it |
| `last_seq` | the inbox is present | movement; the ack floor must reach it |
| `created` | the inbox is present | recreation check for the stream |
| `suspended` | the inbox is present | state of the stream the earlier audit recorded |
| `deliver` | the inbox is present | the `deliver` consumer object |
| `deliver.present` | the inbox is present | whether the consumer existed |
| `deliver.pending` | the consumer is present | nothing pending |
| `deliver.ack_pending` | the consumer is present | nothing unacknowledged |
| `deliver.delivered` | the consumer is present | movement of the delivered stream sequence |
| `deliver.delivered_consumer_seq` | the consumer is present | movement of the delivered consumer sequence (a redelivery) |
| `deliver.ack_floor` | the consumer is present | must reach last_seq |
| `deliver.created` | the consumer is present | recreation check for the consumer |
| `deliver.deliver_policy` | the consumer is present | all or a start sequence |
| `deliver.start_seq` | the consumer is present | used with by_start_sequence |
| `deliver.filter_subjects` | the consumer is present | any filter is a finding |

Other fields the writer omits when empty or unknown, and that the comparison does not read (`last_time`, `subjects`,
`start_time`, `delivered_last`), are not required.

`compare` holds `against_generated`, `moved`, `identities_removed` and `identities_added` (directory keys present
before and not now, and the reverse) and `streams`: one entry per inbox stream in either audit, sorted, each with `present_before`, `present_now` and a
before, now and moved triple for `last_seq`, for the `deliver` consumer's `delivered` (stream sequence), for its
`delivered_consumer_seq` (`consumer_seq` in the compare) and for its `ack_floor`, and two flags, `vanished` and `retired`. A stream has moved when it is present now and its `last_seq` or
its `delivered` or `delivered_consumer_seq` differs: mail was stored, read, or read again (a redelivery advances
the consumer sequence and leaves the stream sequence alone). The ack floor is shown but does not count by itself.
`moved` is the number of streams that moved.

An inbox that was present in the earlier audit and is absent now has either `vanished` or `retired`. The
`*_before` fields keep what the earlier audit showed.

- `retired`: the inbox was empty in the earlier audit (no stored message, or a `deliver` consumer with nothing pending, nothing unacknowledged and an ack floor that covered every stored message) and its directory record is gone now as well (it was deprovisioned). It is shown and does not fail `--strict`.
- `vanished`: every other inbox that disappeared: it held unread mail, or its record is still in the
  directory (so the stream was deleted or purged under a live record). A vanished inbox is the audit's signal that
  mail may have been lost. `vanished` in the report is the number of such streams.

A stream that is present now and was absent before is not a vanished one; its record shows in `identities_added`.

### Retired markers in a comparison

The `retired` set of the two reports is compared too (ADR 0088: a `final` marker is never deleted). `compare` gains
`retired_compared`, `retired_added`, `retired_final_lost` and `retired_pending_gone` (lists of flat identities, sorted):

- `retired_added`: a marker present now and not before. Fine; shown only.
- `retired_final_lost`: a marker that was `final` before and is absent now (or the whole bucket is gone), or is no longer `final`. Markers are never deleted, so this fails `--strict` (exit 1) and standard error counts it.
- `retired_pending_gone`: a marker that was `pending` before and is absent now, with no marker at its key. Shown only: `deprovision` deletes a pending marker when the retirement did not happen. A pending marker that became `final` is in none of the lists.
- `retired_compared` is `false` and `retired_unverified` is `true` when the earlier report has no `retired` section (made before it existed, or `null`): it cannot show that no final marker was deleted since, so nothing is compared and the three lists are empty. Without `--strict` the comparison stays readable and the text form prints `WARNING retired comparison unverified: the earlier report has no retired section`. With `--strict` the file is refused like one that lacks a required field: exit 1, `retired comparison unverified: the earlier report has no retired section` on standard error and nothing on standard output; regenerate the earlier audit with this mashctl. This applies whatever the current report holds (an absent bucket now may be a deleted one). `retired_unverified` is `false` otherwise. The earlier-report fields table below is about legacy inboxes and does not change. A `retired` section that is present must be whole: `present`, a `markers` array and a `key` and `state` in each marker, or the file is refused with exit 1 naming the missing path (a stripped section would hide a deleted final marker). A marker whose `state` is not exactly `final` or `pending` (for example `Final` or `done`), or whose `key` repeats an earlier marker's, is refused the same way (exit 1, naming the path): the comparison knows only those two states, and with a duplicate the last entry would win and could hide a lost `final` marker.

### Recreated streams

Equal sequences do not prove a stream is the same stream: a stream deleted and made again can be refilled to
the same `last_seq`, `delivered` and `ack_floor`. So each inbox in the audit JSON carries `created`, when the server
created the stream, and its `deliver` object carries `created`, when the server created the consumer. Both are RFC 3339
in UTC with the nanoseconds the server gives, and are left out only when the server gives a zero time.

When a stream is present in both audits and its `created`, or its `deliver` consumer's `created`, differs, the
`streams` entry has `recreated: true` (with `created_before`, `created_now`, `deliver_created_before` and
`deliver_created_now` shown), `compare.recreated` counts it, the text form prints `RECREATED`, and `--strict --compare`
exits 1 whatever the sequences say.

An earlier audit made before these fields existed cannot prove anything, so it is refused (exit 1) with the path of
the missing field and a note to regenerate it: run `mashctl audit --json` again with this mashctl, and compare a later
audit against that file. When the server gives no creation time in the current audit for a stream present in both, the
entry has `creation_unknown: true`, `compare.creation_unknown` counts it and the text form prints `UNKNOWN`; without
`--strict` it is only shown and with `--strict` it fails (exit 1).

With `--strict`, `--compare` fails (exit 1) when `moved`, `vanished`, `recreated` or `creation_unknown` is above 0 or `account_unverified` is true, and on every other finding: any
orphan, any problem.

To see the stored messages of one inbox and their delivery state, use [`mashctl endpoint inbox`](mashctl.md#mashctl-endpoint-inbox).

## What it cannot see

The audit reads the state of the server: the directory, the streams, the consumers, the leases and the creds files.
It does not see use. In particular it cannot see:

- publishes to a subject that has no stream: the mail goes nowhere and leaves no trace here;
- connections: who is connected, with which credentials, and to which subjects they publish or subscribe;
- an endpoint that has no stream, and a client that publishes or consumes without being an endpoint.

What it can show is that a stream stored a message (`last_seq`, `last_time`) or had one read (`delivered`,
`delivered_last`) between two audits.

So "two clean audits a day apart" means exactly this: both runs were `--strict` clean (exit 0), and `--compare` of the
second against the first shows no movement (`moved`, `vanished`, `recreated` and `creation_unknown` all 0, with `--strict` the second exits 0). That is evidence
that no stream stored or delivered mail in that day. It is not evidence about publishes or connections the audit
cannot see.

## Exit codes

| code | meaning |
| --- | --- |
| 0 | the audit completed and is clean, or it has findings and `--strict` was not given |
| 1 | `--strict` and a finding: an orphan of any kind, a problem (an item the audit could not read, which it cannot call clean) or, with `--compare`, a stream whose `last_seq`, `delivered` or `delivered_consumer_seq` moved, an inbox that vanished, a stream or its `deliver` consumer that was recreated, a final retired marker that is gone since the earlier audit, a retired-bucket finding (the 50% alarm, a pending marker, a bucket or marker that cannot be read), or an earlier audit that lacks the account, or a current audit with no creation time for a stream present in both. Standard error says what was counted |
| 2 | a usage error |
| 1 | the audit could not complete: no server or no credentials, an unreadable required creds directory (`--creds-dir`), or a `--compare` file that is unreadable, not JSON, another version, incomplete (including one without creation times), or from another owner or account, or, with `--strict`, one with no `retired` section. Nothing is printed on standard output |
