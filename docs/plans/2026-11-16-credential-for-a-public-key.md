# A credential for a public key, with no outside system (design note)

Lane core, 2026-11-16. Task: the credential primitive "public key up, credential down" (spike 2), built on the direct policy driver (#688). Standalone first (ADR 0131). No verb is wired here: the CLI face is ctl's, later.

## What exists

- `mashctl add` and `reissue` make the user's private key on the host that holds the signing keys, write it into a creds file there, and sign with the account key (`mint.Mint`). The seed travels.
- The issue service (ADR 0104, `internal/issue`) already does "public key up, credential down", but only for a countersigned request from a registered requester over NATS. Its steps after the proof are the whole primitive: find the sender in the list set in force, find the owner group's template, check the group's scoped key is published in the account JWT with that template, refuse a revoked or bound key, sign a JWT with no permissions of its own (the scoped key's template applies) carrying the sender's tags and the `ep` tag, write the issue record as a compare-and-set, return the JWT only if the write succeeded.

## The primitive

One function, one request, one JSON result:

    credential.Issue(ctx, cfg, Request) (Result, error)

    Request  {address "owner.name", endpoint "agent.owner.name", public_key "U...", purpose "issue"|"rotation"}
    Result   {outcome, address, public_key, jwt, tags, generation, issued_at, refusal{code,detail}}

- The caller holds the operator keys (it is `mashctl` on a person's machine, or a program wrapping `mashctl`). That is the trust: holding the owner group's scoped signing key. There is no requester, registry, proof or nonce. Those exist only to let a third party ask a service; they stay in `internal/issue` for the unshipped service.
- The private key never reaches mash. A public key that is not a valid user public key is refused, and a seed in any field is refused and never logged (the existing `logRefusal` shape rules carry over).
- The signing core moves out of `Issuer.issue` into `issue.Authority.Sign` (list set, template, scope check, revoked check, signing, checked record write). `Issuer.issue` keeps its proof and nonce steps and then calls it, so the service and the primitive share one signing path and one set of refusals. No behaviour of the service changes (its tests are the proof).
- Inputs the primitive needs: the list set in force (the ledger the direct driver wrote, `policydirect`'s `InForce`), the group key (`ctlkeys` `groups/<owner>.nk`), the account JWT (to verify the key is a scoped signer with the list set's template), and the issue record (`mash-issued`). All four exist.
- No list set in force: refuse `no_list_set`. It presumes no default for a first policy (that is Randy's answer on #681).
- The endpoint tag comes from mash's own function over `endpoint` (`listset.EndpointTag`), refused as `endpoint_mismatch` when its owner and name are not the address's. The caller states the endpoint; it is a fact, not a permission.

## The endpoint side

Two pure helpers in the same package, no I/O beyond the caller's own file:

- `NewKey() (seed []byte, publicKey string)` makes a user key locally.
- `Assemble(jwt string, seed []byte) ([]byte, error)` makes the creds file contents from the returned JWT and the endpoint's own seed (`jwt.FormatUserConfig`), refusing a JWT whose subject is not the seed's public key.

## Reissue, rotation, audit, from the public key alone

- **Reissue** is `Issue` again with the same address and public key: a new JWT under the current list set (new tags, current generation). The record's compare-and-set already accepts the same key at the same address, and refuses a revoked key or one bound to another address. The JWT carries no permissions, so a policy change that only edits templates needs no new credential; a tag or `ep` change does.
- **Rotation** is `Issue` for a new public key at the same address with purpose `rotation`; the old key is then revoked with the existing revoke path (ADRs 0067, 0114). The primitive does not revoke in the same call: the caller revokes by public key, so a failed revoke never loses the new credential.
- **Audit** is `credential.Show(ctx, cfg, publicKey) (Report, error)`: the issue record and history of that key (`IssuedByKey`, `IssuedHistoryByKey`), whether the account JWT revokes it, and whether it is connected now (the existing connection scan, with the "partial answer is not clean" rule). It needs no creds file and no address.

## JSON and exit codes

JSON in on stdin or as a file, JSON out; both are structs that the package marshals. Outcome words and exit codes follow `policydirect` (issued 0, refused 1, error 6). The refusal codes are the issue service's, so a program learns one vocabulary.

## Live proof

Two-server cluster, no service running, no requester: apply a list set with `policydirect`, make a key with `NewKey`, `Issue` with the group key, connect with `Assemble`d creds on server B, see the sender reach its own inbox and be refused another's; a seed never appears in any output; `Show` finds it by public key; revoke it and see the connection closed and `Issue` refuse. A second run with a key bound to another address is refused.

## Not in this change

A `mashctl` verb, a replacement for `mashctl add` (it stays as it is until ctl and Randy decide), key custody on the endpoint beyond a 0600 file, expiry of a credential, and any signed request.
