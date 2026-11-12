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

**The service.** `mashctl issue serve [--list-set FILE] [--registry FILE]` registers a NATS micro service named
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
`record_failed`, `address_subject_mismatch`, `busy`, `internal`. Any other field in the request, such as tags,
permissions, limits, a name or a JWT expiry, is not decoded and never read.

**The checks.** All must pass; the first that fails refuses, writes one structured log line (code, address, public key,
host, purpose; each only if it has the shape it should, so a seed put in a field by mistake is not logged; never a JWT)
and signs nothing:

1. The request is well formed and within the size cap, and its subject names the address's owner group.
2. `expires` is in the future and no more than five minutes ahead (`MaxWindow`).
3. The wrangler key is registered for exactly this host and this owner group, and the proof verifies against it over the
   canonical bytes. The address is inside the signed bytes, so a proof valid for `foo.c.core` fails for `foo.c.sdk`.
4. The nonce has not been seen. It is taken only after the proof verifies, so a forged request cannot use up a real
   nonce.
5. The address is a sender in the list set in force.
6. The public key is not marked revoked in the issue record and is not bound there to a different address.
7. The owner group's scoped signing key is available and is an account signing key other than the account identity key.

Then the service signs with the JWT shape `mint` uses for scoped-key users (name is the address's name part, issuer
account is the account public key, no permissions or limits, no expiry, tags from the table's compiled user) and writes
the issue record (kind `endpoint`, purpose `issue`, the full address, the owner group as zone) **before** it replies. If
the write fails nothing is returned. One issue runs at a time per service, so two requests for one key cannot both pass
the binding check; two services on one account are not supported.

**What the countersignature proves, and what it trusts.** It proves that the holder of the wrangler's private key asked
for this public key to be bound to this address, from this host name, now. It trusts the wrangler to tell its senders
apart locally and to ask only for the address a key pair really belongs to, and it trusts the host, since a wrangler
that is compromised can ask for any address registered to it. It does not prove that the public key's private half
exists or where it is; the sender proves that when it connects.

**Replay.** Nonces are held in memory until their request's expiry, at most five minutes, in a set capped at 65536. A
restart forgets the set, so a request captured before a restart can be replayed until its own expiry; after that it is
refused as expired. The expiry window is the bound on a replay across a restart, and the test named for it checks both
halves.

**The registry is a test account's.** How a real wrangler's key is registered for a host and an owner group is not
decided; decision 206 does not say, and this is DEC-002's open part. Until it is, mash ships only a static registry: the
file `wranglers.json` in the key store directory (`{"account", "test_account": true, "entries": [{"host", "group",
"wrangler_key"}]}`, 0600, replaced atomically), managed by `mashctl wrangler init --test-account`, `add`, `list` and
`remove`. `issue serve` refuses to start, with `no wrangler registry for this account: the static registry is for test
accounts only; real wrangler registration is not decided yet`, on an account with no registry, a registry that names
another account, or one not flagged `test_account`, and so does every later lookup if the file stops being such a
registry. The file is read at each request, so a removal takes effect without a restart. This is not a statement that
wrangler keys should be registered this way on a real account.

**What is left open behind interfaces.** `WranglerRegistry` (host and group to wrangler keys) is filled by the static
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
* Bad, because on a real account the service cannot run until wrangler registration is decided.
* Bad, because the replay set does not survive a restart; the expiry window bounds the exposure to five minutes.
* Bad, because the service runs with the admin user's credentials until a dedicated user exists.
* Bad, because a compromised wrangler can have any address registered to its host and group signed for a key it chooses.

### Confirmation

`cmd/mashctl/issuecmd_test.go` runs the service against an in-process nats-server in operator mode (AT-012): a JWT that
connects with its own private key, tags equal to the table's, every refusal named above logged with nothing signed and
nothing recorded, a scan of the NATS payloads and the log for seeds, the issue record's purpose and kind, and the refusal
to start. `internal/issue` tests the replay set, the restart and the record failure with a fake clock.

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
