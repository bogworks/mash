---
status: accepted
date: 2026-11-12
decision-makers: Randy Bias (mash maintainer)
---

# Every issued user JWT is recorded in a bucket only the admin connection writes

## Context and Problem Statement

A user JWT signed with a copy of a zone's scoped signing key is accepted by the account, takes the zone's permission
template and connects. mash had no record of the users it issued, so such a user could not be told from one `mashctl`
made. The signing keys stay with `mashctl`'s key store, but a leaked or copied key is the case to find. What should mash
keep, where, who may write it, and how does an operator compare it with what is connected?

## Decision Drivers

* The record must be written by the minting authority only, and an endpoint must not be able to read or change it.
* A user that can connect must always be in the record, so the record is written before the creds are handed out.
* The record must survive a re-issue, a rotation and a retirement: it is a history, and revocation is a separate fact.
* mash's own admin and system users must not be reported as foreign.
* A deployment from before the record must not be broken by it.
* The comparison must work on a cluster and must not claim a clean answer from a partial one.

## Considered Options

* A KV bucket `mash-issued`, keyed by the user's public key, written over the admin connection, with a `mashctl issued
  check` that compares it with the system account's connection list (chosen).
* A file beside the key store.
* Putting the record in the directory bucket.
* Reading the issuer key from each connection and flagging any user not signed by a zone key mash holds.

## Decision Outcome

The record is the bucket `mash-issued`. The key is the user's public key and the value is `{public_key, address, owner,
zone, kind, tags, generation, issued_at, purpose, revoked_at}`: public information only, never a seed or a JWT. `kind` is
`endpoint` for a policy endpoint's user and `admin` for the admin user. `generation` is 0, because mash has no policy
generation yet. `purpose` is why the credential was issued, a closed set: `issue` (`add`, a reissue that keeps the key,
the admin user at `init`), `rotation` (the new key of a reissue that rotates the user key) and `probe` (reserved for the
policy gate's probe credentials; no mash path writes it yet). A write with another purpose is refused, and a record
with no purpose (one written before the field) is read as `issue`. `revoked_at` is RFC 3339 in UTC and is left out while
unset.

Writers. `mashctl add` and `mashctl reissue` write the entry after the user is minted in memory and before the directory
record, the staged file or the creds file, so a failed write issues nothing. The admin user that `mashctl init` connects
with is recorded by `init`, which covers `init --local --start`. `adopt --mint-admin` records the admin user it mints itself,
right after the minted user connected and before the creds file is written (kind `admin`, purpose `issue`, the bucket
created on demand); if the write fails, adopt fails, says the admin credentials were not recorded and writes no creds
file. An admin creds file given with `--admin-creds` is recorded by the next `mashctl init`.
`mint.Mint` stays free of network I/O, and the callers write.

Append-only. A re-issue of the same key adds a revision under that key; a rotation adds an entry for the new key and
leaves the old key's. Nothing in mash deletes an entry. The one change to an existing entry is `revoked_at`: after a revocation of a user key
succeeds (`mashctl retire`, `mashctl deprovision`, and the old key of a rotating `mashctl reissue`), mash sets it with a
compare-and-set write (`Admin.MarkRevoked`), never clears or moves it, and a write of the same key by `RecordIssued`
keeps it. Marking is best effort: a failure prints a warning and never fails or undoes the revocation, and a key mash
never recorded is not an error. The account JWT stays the authority on what is revoked; `revoked_at` is the record's note
of it.

The bucket. It has file storage, no TTL, a history of 64 revisions per key (the most a KV bucket allows), an 8 MiB cap and discard-new when full, so a full bucket refuses
new issues and never loses a record. Its stream refuses rollup, message delete and purge, set at creation
(`store.CreateIssuedBucket`, the way the lease bucket is made). `mashctl init` creates it. A deployment from before the
record gets it on the first `add` or `reissue`, created over the admin connection; an existing bucket that is not as mash
makes it is refused for writing (fail closed) and `init` reports it. The one change `init` makes to an existing bucket is
to raise a history below 64 (the first version kept 8) to 64, a stream update that loses no record; a bucket whose history
is 8 or more is accepted by verification.

Access. No permission list mash makes for an endpoint, its zone key's template or a new-form endpoint grants anything on
`$KV.mash-issued.>` or the stream's `$JS.API` subjects. A test asserts this for each list and against a live endpoint
connection, which is refused a write, a read, a listing and a purge. The admin connection can write the bucket, so a
holder of the admin creds can add a record: the record guards against signing-key copies, not against the admin creds.

The check. `mashctl issued check` connects with the system account creds and sends `$SYS.REQ.SERVER.PING.CONNZ` with auth
on and the MASH account as filter. The server reports a JWT user's public key as `authorized_user`, with the connection
name, the signing key and the tags. It asks for a page large enough that a typical scan is one reply, and pages through
a server with more connections (further requests on the server's own subject). A scan of one page is one snapshot and is accepted when its count matches the server's total. Each page of a longer scan is a fresh snapshot, so a connection that closes and another that opens between two pages can repeat or skip connections, even while the total stays the same and no id repeats. mash collects the connections of a server into a set keyed by connection id; a multi-page scan is accepted only when the set is as large as the total the server reported on its first and on its last page, no connection came twice, and the scan before it (also complete) collected exactly the same set. Otherwise it scans that server again, up to 3 scans in all, and then treats the server like one that did not answer (incomplete). It reports every connected key that
is not in the record. No key is exempt: the admin user the command connects with is in the account and must be recorded
(by `init` or `adopt --mint-admin`), and the system account's users, the system creds among them, are never in the MASH
account's connection list. Recorded keys that are not connected are informational. A connected key that the record
holds as revoked is a separate finding, `revoked key still connected`, because the revocation did not take effect. Exit 0:
no unrecorded key and no revoked key connected. Exit 1: at least one of either. A recorded key of any purpose counts as
issued by mash. Exit 2: nothing was found, but the scan was incomplete or failed: usage, connection failure, a server that did not
answer or whose list could not be read whole, or a server set that cannot be shown complete (a cluster needs `--servers N`
or `MASH_SERVERS`, as `policy apply` does). A finding wins over incompleteness: a key found on the servers that answered is
certain, so it exits 1 even when the scan was incomplete. The text and the JSON (`incomplete`, with the reason) always say
when the scan was incomplete, so exit 1 never hides it.

Users issued before the record are not in it. They show as unrecorded until `mashctl reissue NAME` records them.

### Consequences

* Good, because a user signed outside `mashctl` with a copied key is reported by its public key, server and signer.
* Good, because the record is written before the creds exist, so a connectable user is never missing from it.
* Good, because endpoints have no access to the record and the stream refuses rollup, delete and purge.
* Good, because a deployment from before the record keeps working.
* Bad, because the check sees only connections open when it runs.
* Bad, because the record rests on the admin creds as well as on the signing keys.
* Bad, because users issued before the record are reported until they are reissued.
* Bad, because an issue now needs one more write to the server, and is refused when it fails.
* Bad, because the history is capped: the last 64 revisions of a key are kept, and a key is re-recorded only on a reissue
  that keeps it, so reaching the cap would take 64 such reissues of one key; the 65th would drop the oldest revision.

### Confirmation

Tests cover: the record written by `add` with every field and by `reissue` as a revision, a rotation recording the new
key and keeping the old one, and the admin user recorded by `init`; an add refused with nothing issued when the record
cannot be written; the endpoint permission lists granting nothing on the bucket and a live endpoint refused every
operation on it; the stream's configuration and the refusal of an unhardened bucket; the check against an operator-mode
server, clean for issued users and reporting exactly one user signed with a zone key outside `mashctl`, with exit codes,
the text and JSON forms, an incomplete server set refused, paging over several CONNZ pages and a scan whose pages shift
reported as incomplete, never clean; the admin user recorded by `adopt --mint-admin` (and adopt failing without a creds
file when the record cannot be written); more than 8 revisions of one key kept up to the cap and an old history-8 bucket
raised by `init`; an admin key that is connected and not recorded reported, with no exemption; the incomplete flag shown
beside a finding; the purpose written by each
path and refused when unknown; `revoked_at` set once, idempotently and without losing a field to a concurrent write; the
revoking paths marking after success and tolerating a failed mark; and the check reporting a revoked key that is still
connected.

## Pros and Cons of the Options

### A KV bucket written over the admin connection, with a check (chosen)

* Good, because it lives where mash's other shared state lives, with the same admin connection and hardening.
* Good, because it works for a cluster and for any `mashctl` host.
* Bad, because it depends on a server write at issue time.

### A file beside the key store

* Good, because no server write is needed.
* Bad, because it is not shared between `mashctl` hosts and a copy of the key store would not carry later entries.

### The directory bucket

* Good, because no new bucket is made.
* Bad, because endpoints read the directory, and the record would be readable by them and mixed with endpoint state; the
  directory is also keyed by name, not by key, and holds one value per name.

### Flag any user not signed by a key mash holds

* Good, because it needs no record.
* Bad, because the case to find is a user signed by a key mash does hold, copied elsewhere.

## More Information

* Related: [ADR 0093](0093-the-lease-bucket-does-not-allow-rollup-writes.md), [ADR 0088](0088-a-retired-new-form-name-is-burned-in-a-durable-bucket-by-a-two-phase-marker-that-is-never-deleted.md).
