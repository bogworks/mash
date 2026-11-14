---
status: accepted
date: 2026-11-13
decision-makers: Randy Bias (mash maintainer)
---

# An adopted account takes its list sets only from its submitter, and mashctl refuses every other policy change

## Context and Problem Statement

An outside system may own an account's list sets (who may mail whom). If operators also change that account's policy
directly through `mashctl`, two writers disagree and the result is undefined. The maintainer's rule (2026-11-11): when
an outside system owns the list sets, mash must refuse direct policy changes on that account, and running one account
both ways is not supported and is documented. Mash needs a record on the account that says so, a way to register the
one key allowed to hand in list sets, and a guard in every command that would change policy another way. This decision
covers the marker and the refusals; the list-set intake and the reconcile of what the account already holds against
the first list set are separate changes.

## Decision Drivers

* The marker must not be writable by an endpoint, or by the outside system itself: only the operator's admin
  connection registers the submitter.
* A refusal must hold when mash cannot tell: an unreadable marker must not read as "not adopted".
* The registered submitter must be a key mash does not hold, or mash could sign as the outside system.
* A lost or leaked submitter key needs a way back that does not wait for the outside system.
* Existing deployments, and accounts nobody adopts, must behave exactly as before.

## Considered Options

* One key in a new hardened bucket, `mash-adoption`, written only over the admin connection; a guard function every
  policy-changing command calls first (chosen).
* A field in the account JWT or the directory bucket.
* A flag in mash's local configuration file.

## Decision Outcome

The adoption marker is the single key `account` in the bucket `mash-adoption`, made like the issue record's bucket
(ADR 0101): file storage, no TTL, history 64 so the revisions are the audit trail, a 1 MiB cap, rollup, delete and
purge refused from the first moment, verified on every use and refused when it is not as mash makes it. `mashctl init`
creates it, and `mashctl policy adopt` creates it when it is missing. No permission list mash gives an endpoint grants
anything on the bucket or its stream subjects, and tests assert that and a live refusal.

The value is JSON: `v` (1), `account` (the account public key), `submitter` (an nkey public key), `next_submitter`
(optional, written only by the later list-set intake), `adopted_at` (RFC 3339, UTC), `by` (the admin user's public key),
`action` (`adopt`, `rotate` or `recover`) and `exempt` (a sorted list of user public keys, described below; a marker
written without it reads as an empty list). A reader refuses a value it cannot parse, a schema other than 1 and a
submitter that is not an nkey public key.

`mashctl policy adopt --submitter KEY` is run with the account's admin credentials. It accepts any valid nkey public key
(the contract says an Ed25519 nkey public key and does not fix the prefix) and refuses, writing nothing, a seed, a
string that is not a key, the account's own key, and any key the key store holds (operator, system, account, a zone
signing key) or the admin user's key. The first run creates the marker (`adopt`). A second run with the same key writes
nothing and says so. A run with a different key replaces the marker by a compare-and-set on its last revision and
records `recover`: the recovery rotation, which refuses the old key at once because readers read the marker each time
and keep no copy. `mashctl policy adopt --show` prints it. There is no un-adopt command: un-adoption is not supported
yet (see the deferred option below).

**Exempt keys.** The later first-list-set reconcile will revoke user credentials that are not issued under the list
set, and it must never touch mash's own admin and system users or the service users an operator made by hand. So the
marker records them, by public key and never by "whatever is connected as admin", at adoption. `policy adopt` records
the admin user's key (read from the admin credentials in use), the system user's key (read from the system credentials
the push uses: `--sys-creds`, `MASH_SYS_CREDS`, or the `sys.creds` beside the admin credentials) and each `--exempt KEY`
given (repeatable; an operator-named service user; it must be an nkey user public key, and the operator, system,
account and zone keys are refused). Only the JWT's subject is read: a seed is never printed or kept. When the system
credentials cannot be found adopt refuses and writes nothing, because the reconcile could otherwise revoke the
credentials the push needs. Running adopt again with the same submitter and the same set writes nothing; with the same
submitter and another set it writes a new revision (action `adopt`, "exempt keys updated"); with a different submitter
it is the recovery rotation and refreshes the set too. `--show` prints the exempt keys (public keys only; `--json`
includes them), and for an older marker without any it says `exempt: none recorded: run adopt again before the
reconcile`.

What the reconcile will do with them is the plan for a later change and is not built here: it removes signing keys
that are not mash's, revokes the recorded issued users that are not in the first list set and the connected keys that
are neither recorded nor exempt, reports anything else it cannot name as a finding with the status `reconciled:
partial (N unknown)`, prints a dry run first and acts only with `--yes`. Exempt keys are skipped by every one of those
steps.

**Adopting a real account is a live-account step**, taken only on an explicit go from the maintainers. This change was
built and tested on test accounts only.

`ctl.GuardAccount` (`ctl.GuardAccountFor` with no connection yet) is the guard. It takes the account lock (see Concurrency), reads the marker and returns `mashctl: <command> is refused: this account is
adopted by an outside system (submitter <first 8 characters>...): list sets come only through the list-set path; nothing
was changed`, or, when the marker cannot be read for any reason other than not existing, `... cannot tell whether this
account is adopted: <error>; nothing was changed`. It is the first thing a command does, before any write, signing, key
creation or file change, in `policy apply`, `policy repair` (both also with `--dry-run`), `add`, `reissue`, `label`,
`unlabel` (with or without `--sync`: the policy-file edit is the policy change), `retire`, `set-meta` when it sets a
label, `provision` and `mash admin provision` when they set a label or a zone, and `set-zone`. Read-only commands are never refused. `deprovision`, a `provision` without a label or a zone, `suspend`, `resume`, `limits`
and a `set-meta` without labels are operator housekeeping of one endpoint, not list sets, and are not refused. A `label`
or `unlabel` run with no server configured at all is a pure edit of the policy file, mash has nothing to ask, and it is
allowed; with a server configured and no way to read the marker it refuses.

Running one account both ways is not supported, and the operations guide says so in those words.

**Concurrency.** Reading the marker and closing the connection before the command's writes would leave a gap in which
`policy adopt` could succeed and an earlier-started `policy apply` (or another guarded command) would still change policy
on the now-adopted account. So policy changes and adoption are serialised by one account lock, the lease-bucket name lock
of ADR 0048 under the pseudo-name `_policy-account` (no endpoint can have it: the underscore is not a valid name
character). `Admin.Adopt` holds it through its read and its write of the marker. A guarded command takes it first, then
reads the marker (refusing, and releasing the lock, when the account is adopted), and holds it through all of its writes,
releasing it on every exit path by `defer`. So an adoption waits for a command that is already running, which finishes
under the old state, and a command that starts after the adoption reads the marker and is refused. A command that cannot
get the lock within 30 seconds refuses with `another policy change or an adoption is in progress; nothing was changed`
(exit 1); adopt refuses the same way and writes nothing. Read-only commands never take it. Unlike the limits locks, the
account lock extends its own expiry in the background every 10 seconds while held (the expiry stays 30 seconds), because
a `policy apply` with its gate, a `reissue` of many names or a `retire` can run for minutes. If the lock is lost anyway (a
holder stalled past its expiry and another took the name), each guarded command checks `Held` before its writes and stops:
`policy apply` before it pushes the account JWT, `add` before it makes a key, the issue record, the directory record, the
creds file and the policy edit (rolling back what it wrote), `reissue` before each of its writes and each name, `retire`
before the revocation and removal and before the policy edit and the creds move, `label` and `unlabel` before the policy
edit and the directory label update, and `set-meta`, `set-zone` and `provision` before their single write.

### Consequences

* Good, because the account's policy has one writer once adopted, and a command that would add a second is refused
  before it changes anything.
* Good, because every failure to read the marker refuses: nothing is lost by a missing connection, and nothing slips
  through.
* Good, because the revisions of the marker record who registered which key and when, and the bucket refuses deletes,
  so the trail stands even against the admin.
* Bad, because until the list-set intake exists an adopted account's policy cannot be changed at all, and there is no
  un-adopt command.
* Bad, because `policy apply` and `policy repair`, which needed only the system credentials, now also need the admin
  credentials to read the marker.
* Bad, because the holder of the admin credentials can replace the submitter; the marker does not protect against them.

### Confirmation

Tests cover the bucket's hardening and its creation by init and by adopt; the marker's fields; the same key as a no-op,
a different key as `recover`, and `--show` after it; refusal of an invalid key, a seed, the account's key and each key
the key store holds; the exempt set (admin and system keys recorded, `--exempt` validated, a missing system credentials file refusing with
nothing written, the re-adopt cases and a marker without the field); a live endpoint connection refused every read, write and listing of the bucket and its stream
subjects, with the marker unchanged; every guarded command refused on an adopted account with the policy file, the key
store, the creds directory, the account JWT, the directory and the issue record unchanged, and each run once on an
account that is not adopted; read-only commands and `deprovision` working on an adopted account; and every guarded
command refusing when the marker's value or bucket is not as mash reads it; the race itself (a guarded command held
between its guard and its write while `policy adopt` starts: adopt blocks until the command is done and the command's write
goes through, and a command started after the adoption is refused), adopt and every guarded command refusing while the
lock stays held, the lock freed on success, failure, refusal and panic, two commands serialising, a refreshed lock alive
past several expiries, and a lost lock stopping `label`, `add` and `policy apply` before their next write.

## Pros and Cons of the Options

### One hardened bucket and a shared guard (chosen)

* Good, because the bucket pattern, its hardening and its tests exist for the issue record.
* Good, because one guard function keeps every command's change to one call.
* Bad, because every guarded command now makes one extra connection to read the marker.

### A field in the account JWT or the directory bucket

* Good, because it needs no new bucket.
* Bad, because the account JWT is pushed by `policy apply`, the command the marker must stop, and the directory bucket
  is writable by more than the admin connection.

### A flag in mash's local configuration file

* Good, because it needs no connection.
* Bad, because it covers only the operator host it is on; a second host, or a copy of the config, would run commands
  the marker was meant to refuse.

## More Information

* Related: [ADR 0101](0101-every-issued-user-jwt-is-recorded-in-a-bucket-only-the-admin-connection-writes.md), [ADR 0093](0093-the-lease-bucket-does-not-allow-rollup-writes.md).
* Not in this change: the list-set intake and the reconcile of the account against the first list set.
* Deferred option, un-adopt: not built. If it is ever built it is admin-only, is logged in the marker's revision
  history, and never goes through a list set. Today the way back is an operator acting on the account directly.
