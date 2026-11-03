---
status: accepted
date: 2026-11-03
decision-makers: Randy Bias (mash maintainer)
---

# A reissue that narrows an endpoint rotates its user key and revokes the old one

## Context and Problem Statement

`mashctl reissue` re-signed the creds file's JWT under the same user key ([ADR 0053](0053-who-may-mail-whom-is-a-policy-file-compiled-by-mashctl-into-nats-permissions.md)).
User JWTs carry no expiry, so after a policy narrowed an endpoint (a tag removed, a zone moved) the old, wider JWT
stayed valid for anyone holding it (GHSA-xr7f-xmcm-w98h). NATS revokes a user by public key and time, so re-signing
under the same key cannot cancel the old token.

## Decision Drivers

* Narrowing must take effect, not only be recorded.
* A reissue that only widens must stay cheap (it keeps the key and needs no revocation).
* A failure part way must leave the endpoint recoverable by running the command again.

## Considered Options

* Rotate the user key and revoke the old one when the reissue narrows (chosen).
* Rotate and revoke on every reissue.
* Give user JWTs a bounded expiry and renew them.

## Decision Outcome

Every reissue, rotating or not, first requires the zone key that will sign the new JWT to be published: its public key
must be a scoped signing key with the zone's role in the account JWT on every server (`apply.SignerPublished`, same
every-server read and refusals as `apply.KeyRevoked`). Otherwise it refuses, changing nothing (`run mashctl policy
apply first`), before it stages, revokes or writes anything, also when it resumes a staged rotation. Without this a
rotation could revoke the working old key and leave only creds that cannot connect.

A reissue whose creds file carries a tag the policy no longer gives, another signer, account or name mints a new
user key and revokes the old public key in the account JWT (`apply.RevokeUsers`, with the system account credentials,
as `retire`). A reissue that adds a `deny:<peer>` tag rotates too, because the old JWT lacks that deny and would stay valid. Only added
`allow:` tags (and the `deny:_none` placeholder, which addresses nothing) keep the key, unless the account JWT already
revokes that key. A creds file whose JWT was issued for another account is refused, changing nothing, because mashctl
can revoke only in its own account: the old JWT must be revoked where it was issued, then the endpoint retired and added again.

Revocation is by key and time, so a JWT re-signed under a revoked key would work again. Every reissue therefore reads
the account JWT (system account credentials, verified against the operator trust) before it mints anything. A key
found in the revocation list, at any time, is rotated as above (the revocation is repeated, which is idempotent and
raises the timestamp); a reissue never re-signs a revoked key. If the revocation state cannot be established, the
reissue refuses and changes nothing.

Every reissue, rotating or not, first connects with the admin credentials and reads the endpoint's directory record,
because it signs with the seed stored in the creds file. It refuses, changing nothing, when the name has no record
(retired or never added), when the record's account is not the account key's, when the creds file's seed is not the
key its JWT names, or when the record's user is not that key. A leftover creds file of a retired name, or one whose
record names another key, is therefore never reissued.

The revocation read covers every server: `apply.KeyRevoked` requires a complete server set (as `RevokeUsers` does),
looks up and verifies the account JWT of every expected server, and reports the key revoked if any copy names it. One
copy without the revocation proves nothing in a cluster whose servers hold different copies; a silent, unverifiable
or incomplete set refuses.

The record check, the revocation check, the mint, the revocation, the record update and the creds file rewrite all run
under the endpoint's name lock (`Admin.WithNameLock`, [ADR 0058](0058-an-operation-under-the-name-lock-stops-before-the-lock-expires-and-rechecks-it-before-writes.md)),
so a concurrent reissue of the same name cannot rotate the record and write its creds between this reissue's check
and its write. The func runs on a context that ends before the lock expires, and the record update
(`Admin.SetUserLocked`) and the file rewrite re-check the lock first.

A rotating reissue runs in this order: mint the new key and stage its creds (JWT and seed) in `<creds file>.next`
(0600, synced) before anything irreversible, revoke the old key and wait for confirmation, point the record at the new
key (`Admin.SetUser`, a compare-and-set from the old key, idempotent for the new one), then replace the creds file
with the staged creds and remove the staged file. A failure before the revocation is confirmed leaves the record and
the creds file unchanged and the old creds still work. A failure after it (the record update, the lock check or the
file write) leaves the endpoint cut off but the new seed on disk, so running `mashctl reissue NAME` again finishes the
rotation. That reissue finds the staged file and resumes when the record names the creds file's key (rotation not yet
recorded) or the staged key (recorded, file not swapped): it re-signs the staged key with the current policy, repeats
the revocation of the old key (idempotent), accepts a record that already names the staged key, and replaces the
creds file. A staged file that cannot be read, is loose, holds no user seed, whose JWT does not name its seed, or was
issued for another account or name is refused with the instruction to remove it. A staged file whose key is the creds
file's own (left when removing it failed after a completed rotation) is ignored. Retire and add is never the
recovery: retiring deletes the endpoint's inbox and can lose queued mail.

A bounded expiry with renewal is not adopted: mash has no renewal path for a running endpoint, so an expiry would
cut endpoints off on a clock, and it would not shorten the window for a JWT already issued. It may be revisited with
a renewal design.

The user JWT budget ([#179](https://github.com/bogworks/mash/issues/179)) moves from 4,000 to 3,750 bytes, since JWTs
of 3,838 bytes and above fail at the default 4,096 byte control line.

### Consequences

* Good, because narrowing cuts off the old credentials; the old key's live connections close when the server takes
  the revocation.
* Good, because a leftover creds file of a retired or replaced name is refused instead of being re-signed.
* Bad, because no reissue is offline any more: each needs the server for the record check and the system account
  credentials to read the account JWT; a revocation is permanent for that key.
* Bad, because a failed record update or write after a confirmed revoke leaves the endpoint cut off until the same
  reissue is run again, and a `<creds file>.next` holding a seed sits beside the creds file until then.

### Confirmation

`TestReissueRefusesUnpublishedZoneKey` (`cmd/mashctl/reissue_rotate_test.go`: rotating and non-rotating, no revoke, no staged file, creds and record unchanged) and `TestSignerPublishedCoversEveryServer` (`internal/apply/revoked_test.go`: published on all, one server lacking it, wrong role, silent server).

`cmd/mashctl/reissue_rotate_test.go` (rotation, a revoked key rotated, refusal when revocation cannot be read, refusal without a record or with another recorded key, revoke failure
leaving the creds file as it was) and `internal/admin/setuser_test.go` (compare-and-set, idempotence, the held name lock), `cmd/mashctl/reissue_lock_test.go` (concurrent reissues of one name leave the file and the record agreeing; the lock is held during the file rewrite) and `internal/apply/revoked_test.go` (a revocation on any one server's copy counts; a silent server refuses).

## Pros and Cons of the Options

### Rotate and revoke when the reissue narrows (chosen)

* Good, because narrowing takes effect and a widening reissue keeps its key.
* Bad, because the narrowing case needs the revocation and the system account credentials.

### Rotate and revoke on every reissue

* Good, because there is one path.
* Bad, because every tag addition costs a permanent revocation and a new key for no gain.

### Bounded expiry with renewal

* Good, because every old JWT dies on its own.
* Bad, because mash has no renewal path, and it does not shorten the window of a JWT already issued.

## More Information

* Advisory GHSA-xr7f-xmcm-w98h; issue #179 (budget).
* Code: `cmd/mashctl/reissue.go`, `cmd/mashctl/revoke.go`, `internal/admin/admin.go` (`WithNameLock`, `SetUser`, `UserOf`),
  `internal/apply/revoke.go`, `internal/apply/revoked.go`.
* Related: [ADR 0053](0053-who-may-mail-whom-is-a-policy-file-compiled-by-mashctl-into-nats-permissions.md).
