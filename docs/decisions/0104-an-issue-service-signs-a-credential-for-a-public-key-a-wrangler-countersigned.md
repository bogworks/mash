---
status: accepted
date: 2026-11-12
decision-makers: Randy Bias (mash maintainer)
---

# An issue service signs a credential for a public key a wrangler countersigned

## Context and Problem Statement

Today `mashctl add` makes each sender's private key on the machine that holds the signing keys, writes it into a creds
file and keeps a copy of the key there. The messaging policy contract (REQ-013) wants the opposite for an adopted
account: the sender's key pair is made on its own host, only the public half travels, and a signed credential comes back.
Two questions follow. How does mash accept a public key and sign for it without making, receiving or logging a private
key? And how does a request prove that it speaks for exactly one address, so that anyone who can reach the service, or any
requester trusted for an owner group, cannot bind its own key to a listed address (DEC-002, settled 2026-11-12: the
wrangler on the sender's host countersigns)?

## Decision Drivers

* No private key is made, received, stored or logged on this path.
* A request must not be able to choose tags, permissions, limits, a name or an expiry: the table decides.
* A proof made for one address must be useless for another address, even in the same owner group.
* Every issue must be recorded before it is returned, and a revoked key must never be issued to again.
* How a wrangler's key comes to be registered for a host and an owner group is not decided. Nothing in this change may
  pretend it is.
* The same signing code must serve first issue and rotation (REQ-014).

## Considered Options

* A request and reply on NATS, countersigned by the wrangler, checked against a wrangler registry (chosen).
* A one-time enrollment token minted when the address is created and bound to it (DEC-002 option 2).
* A renewal request signed by the sender's previous user key (DEC-002 option 3).

## Decision Outcome

Chosen option: the countersigned request and reply, because DEC-002 settled on it.

**The service.** `mashctl issue serve --test-account [--list-set FILE] [--registry FILE] [--sys-creds FILE]` registers a NATS micro service named
`mash-issue` on the admin connection `mashctl` already uses, and answers on `agents.issue.<range>.*`. A wrangler asks on
`agents.issue.<range>.<owner>`, the owner group of the address. The second token, `issue`, is none of the verbs `mail`,
`prompt`, `status` and `hb`, and no permission template the list set compiles has a line that reaches it (a test pins
this), so an endpoint cannot publish to it. The wrangler's permission to publish on its subject and to receive the reply
is account setup and is not made here. A dedicated user for the service is better than the admin user and is left to
operations; the admin user is what `mashctl` has today and what writes the issue record.

**The request** is a JSON object of at most 4096 bytes:

```json
{"version": 1, "address": "range.owner.name", "public_key": "U...", "host": "host-name",
 "nonce": "<base64url, 16 to 64 random bytes>", "expires": 1760000000,
 "proof": "<base64url ed25519 signature>", "wrangler": "U..."}
```

`public_key` is a NATS user public key and is checked with `nkeys`; a seed in that field is refused and is not logged.
`proof` is the wrangler's signature over these bytes:

```text
mash-issue-v1\n<address>\n<public_key>\n<host>\n<nonce>\n<expires>
```

The reply is `{"jwt": "..."}` or `{"error": "...", "code": "..."}`. The codes are stable: `malformed_request`,
`request_too_large`, `invalid_public_key`, `expired`, `expiry_too_far`, `replayed_nonce`, `wrangler_not_registered`,
`bad_proof`, `address_not_listed`, `key_revoked`, `key_bound_elsewhere`, `signing_key_unavailable`, `no_list_set`,
`record_failed`, `address_subject_mismatch`, `busy`, `internal`, and two for the signing key's scope, `signing_key_not_scoped` and
`template_mismatch`. Any other field in the request, such as tags,
permissions, limits, a name or a JWT expiry, is not decoded and never read.

**The checks.** All must pass; the first that fails refuses, writes one structured log line (code, address, public key,
host, purpose; each only if it has the shape it should, so a seed put in a field by mistake is not logged; never a JWT)
and signs nothing:

1. The request is well formed and within the size cap, and its subject names the address's owner group.
2. `expires` is in the future and no more than five minutes ahead (`MaxWindow`).
3. The wrangler key is registered for exactly this host and this owner group, and the proof verifies against it over the
   canonical bytes. The address is inside the signed bytes, so a proof valid for `foo.c.core` fails for `foo.c.sdk`.
4. The nonce has not been seen. It is reserved only after the proof verifies, so a forged request cannot use up a real
   nonce, and it is released again by every refusal below, so a corrected request with the same nonce (the proof covers
   the nonce, so the wrangler signs the corrected request afresh) is not a replay and refusals cannot fill the replay
   set. Only a request that returns a credential keeps its nonce used.
5. The address is a sender in the list set in force.
6. The public key is not marked revoked in the issue record and is not bound there to a different address. This read is
   an early refusal; the write in the next paragraph decides.
7. The owner group's scoped signing key is available and is an account signing key other than the account identity key.
8. That key is published in the account JWT as a scoped signer whose template equals the list set's template for the
   group (`signing_key_not_scoped` when it is absent or published unscoped, `template_mismatch` when its template
   differs). A key published unscoped, or with another template, would give the issued user more than the list set
   allows, because a user JWT with no limits takes whatever the signer's template says. The account JWT is the newest
   one the servers hold, verified against the operator key as `policy apply` reads it, which is why `issue serve` takes
   system account credentials (`--sys-creds`, `MASH_SYS_CREDS`). A verdict is kept per owner group for `ScopeTTL` (3
   seconds) and is dropped sooner if the key or the list set's template changes, so a change of the account JWT is seen
   within that time at the latest and the service does not read it on every request.

Then the service signs with the JWT shape `mint` uses for scoped-key users (name is the address's name part, issuer
account is the account public key, no permissions or limits, no expiry, tags from the table's compiled user) and writes
the issue record (kind `endpoint`, purpose `issue`, the full address, the owner group as zone) **before** it replies. The
write is the arbiter: a compare-and-set that, at the revision it writes at and on every retry, refuses a key whose record
has `revoked_at` set (`key_revoked`) or is bound to another address (`key_bound_elsewhere`). The token is returned only
after that write succeeded; a token signed before a refused or failed write is discarded and never returned or logged.
A revocation that lands after the early read, or a second issuer writing for the same key, is therefore seen by the
write, and of two requests for one key and two addresses exactly one succeeds. The service holds its lock for the replay
set only, never across signing or I/O, and the replay set is a heap ordered by expiry, so a request at the cap of 65536
is refused without scanning the set.

**What the countersignature proves, and what it trusts.** It proves that the holder of the wrangler's private key asked
for this public key to be bound to this address, from this host name, now. It trusts the wrangler to tell its senders
apart locally and to ask only for the address a key pair really belongs to, and it trusts the host, since a wrangler
that is compromised can ask for any address registered to it. It does not prove that the public key's private half
exists or where it is; the sender proves that when it connects.

**Replay.** Nonces are held in memory until their request's expiry, at most five minutes, in a set capped at 65536. A
restart forgets the set, so a request captured before a restart can be replayed until its own expiry; after that it is
refused as expired. The expiry window is the bound on a replay across a restart, and the test named for it checks both
halves.

**The registry is a test account's.** `issue serve` also needs `--test-account`, the operator's own declaration that the
account is a test account, and it starts only when the registry file says `test_account: true` as well: the file alone
cannot turn the service on. The registry file, at the default path or one given with `--registry`, is held to the key
store's custody: the file is a regular file owned by the effective user that no group or other user can write, and its
directory is not a symlink, is owned by the effective user and is not writable by every user. How a real wrangler's key is registered for a host and an owner group is not
decided; decision 206 does not say, and this is DEC-002's open part. Until it is, mash ships only a static registry: the
file `wranglers.json` in the key store directory (`{"account", "test_account": true, "entries": [{"host", "group",
"wrangler_key"}]}`, 0600, replaced atomically), managed by `mashctl wrangler init --test-account`, `add`, `list` and
`remove`. `issue serve` refuses to start, with `no wrangler registry for this account: the static registry is for test
accounts only; real wrangler registration is not decided yet`, on an account with no registry, a registry that names
another account, or one not flagged `test_account`, and so does every later lookup if the file stops being such a
registry. The file is read at each request, so a removal takes effect without a restart. This is not a statement that
wrangler keys should be registered this way on a real account.

**What is left open behind interfaces.** Real registration of wrangler keys stays open. `AccountClaims` (the account JWT, read over the system account) is filled by the same lookup `policy apply` uses. `WranglerRegistry` (host and group to wrangler keys) is filled by the static
registry only. `ListSetInForce` (the compiled list set and a generation) is filled by a JSON file shaped like
`listset.ListSet`, since DEC-004 leaves the file format open; the generation is 0 until the policy gate lands. `Signer`
(owner group to scoped signing key) is filled by the key store, which now keeps an owner group's key in
`groups/<range>.<owner>.nk` beside the zone keys under the same custody rules (the service never creates one). The
issue core is a Go function, `Issuer.Issue(ctx, request, purpose)`, so the rotation code of REQ-014 calls it with purpose
`rotation`; the NATS handler always uses `issue`.

### Consequences

* Good, because no private key is made, received or logged: the traffic and the log of a test hold no seed.
* Good, because tags come only from mash's own table; a request that carries tags gets the table's.
* Good, because a proof is bound to one address and a revoked key is never issued to again.
* Good, because the pieces whose design is open sit behind three small interfaces.
* Bad, because on a real account the service cannot run until wrangler registration is decided; the operator's flag says so, and a file cannot override it.
* Good, because a signing key that is not published scoped with the list set's template signs nothing, and a revocation or a second issuer cannot slip between the check and the record.
* Bad, because a change of the account JWT is seen after up to `ScopeTTL` (3 seconds), and the service needs system account credentials as well as admin ones.
* Bad, because the replay set does not survive a restart; the expiry window bounds the exposure to five minutes.
* Bad, because the service runs with the admin user's credentials until a dedicated user exists.
* Bad, because a compromised wrangler can have any address registered to its host and group signed for a key it chooses.

### Confirmation

`cmd/mashctl/issuecmd_test.go` runs the service against an in-process nats-server in operator mode (AT-012): a JWT that
connects with its own private key, tags equal to the table's, every refusal named above logged with nothing signed and
nothing recorded, a scan of the NATS payloads and the log for seeds, the issue record's purpose and kind, and the refusal
to start (without `--test-account`, and for a registry whose directory or file is open to others), and a group key published unscoped or with another template. `internal/issue` tests the replay set, the restart and the record failure with a fake clock.

## Pros and Cons of the Options

### Enrollment token

* Good, because it is the simplest.
* Bad, because it trusts out-of-band delivery of the token until first use, and every sender's host needs a secret
  delivered before it can ask.

### Renewal signed by the previous key

* Good, because it is cheap and the wrangler is not involved after the first issue.
* Bad, because a first issue still needs another proof, and a leaked previous key could renew forever.

## More Information

* Contract: mash-messaging-policy draft-3, REQ-013, AT-012, DEC-001 (the authority runs with `mashctl`, settled
  2026-11-12) and DEC-002 (the wrangler countersigns, settled 2026-11-12; how a wrangler's key is registered is open).
* [ADR 0101](0101-every-issued-user-jwt-is-recorded-in-a-bucket-only-the-admin-connection-writes.md): the issue record
  this service writes and reads.
* [ADR 0102](0102-list-set-permissions-are-one-template-per-owner-group-with-allow-and-deny-tags-and-a-deny-sentinel.md):
  the templates and tags the signed JWT takes.
* `docs/mashctl.md` and `docs/operations.md` describe the commands and the account setup.
