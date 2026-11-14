`mashctl issue serve` signs a user JWT for a public key over NATS: a wrangler on the sender's host countersigns the key and
address, mash takes the tags from its own table, records the issue, and no private key is made, received or logged. The
wrangler registry (`mashctl wrangler init|add|list|remove`) is for test accounts only, because real wrangler registration
is not decided ([ADR 0104](docs/decisions/0104-an-issue-service-signs-a-credential-for-a-public-key-a-wrangler-countersigned.md)).

A request is refused unless the owner group's key is published in the account JWT as a scoped signer with the list set's
template (`signing_key_not_scoped`, `template_mismatch`); the issue record's write refuses a revoked key or one bound to
another address, so a racing revocation or a second service cannot slip in; a refused request releases its nonce; and
`issue serve` needs the operator's `--test-account` flag and a registry file and directory only the service user can
write (no group or other write bit on the directory, which also holds the lock file and the list set). The list set
file is opened without following a symlink, must be owned by the service user and not writable by others, and is checked
again at every request: a list set swapped or loosened while the service runs is refused with `no_list_set` and nothing is
signed.

`mashctl wrangler init`, `add` and `remove` now hold a lock file (`wranglers.json.lock`, 0600, the key store's lock) from reading the registry to replacing it, so two edits at once cannot undo each other and a removed wrangler's key cannot come back. `wrangler list` and the issue service take no lock and always read a complete file.

`issue serve` takes the declared server count (`--servers N` or `MASH_SERVERS`, as `policy apply`) and signs only when the account JWT was read from every expected server: a silent server may hold a newer JWT that removed or narrowed the signing key. With fewer answers than servers, a lookup error on any server, or a named cluster and no count, it answers `signing_key_unavailable` with a reason, signs and records nothing, and does not keep the failed read.
