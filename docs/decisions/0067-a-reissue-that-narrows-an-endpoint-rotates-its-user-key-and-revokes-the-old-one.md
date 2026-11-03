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

A rotating reissue runs in this order: mint the new key in memory, revoke the old key and wait for confirmation, point
the record at the new key (`Admin.SetUser`, a compare-and-set from the old key, idempotent for the new one), then
replace the creds file. A failure before the revocation is confirmed changes nothing and the old creds still work; run
the reissue again. If the record update fails after the revocation, the file and record still agree, so running the
reissue again finishes. If writing the file fails after the record update, the endpoint is cut off and a plain reissue
is refused as the record and file disagree: `mashctl retire NAME` then `mashctl add NAME`.

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
* Bad, because a failed write after a confirmed revoke leaves the endpoint cut off until it is recovered (run the
  reissue again, or retire and add it, as the message says).

### Confirmation

`cmd/mashctl/reissue_rotate_test.go` (rotation, a revoked key rotated, refusal when revocation cannot be read, refusal without a record or with another recorded key, revoke failure
leaving the creds file as it was) and `internal/admin/setuser_test.go` (compare-and-set, idempotence).

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
* Code: `cmd/mashctl/reissue.go`, `cmd/mashctl/revoke.go`, `internal/admin/admin.go` (`SetUser`, `UserOf`),
  `internal/apply/revoke.go`.
* Related: [ADR 0053](0053-who-may-mail-whom-is-a-policy-file-compiled-by-mashctl-into-nats-permissions.md).
