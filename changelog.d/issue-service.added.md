`mashctl issue serve` signs a user JWT for a public key over NATS: a wrangler on the sender's host countersigns the key and
address, mash takes the tags from its own table, records the issue, and no private key is made, received or logged. The
wrangler registry (`mashctl wrangler init|add|list|remove`) is for test accounts only, because real wrangler registration
is not decided ([ADR 0104](docs/decisions/0104-an-issue-service-signs-a-credential-for-a-public-key-a-wrangler-countersigned.md)).

A request is refused unless the owner group's key is published in the account JWT as a scoped signer with the list set's
template (`signing_key_not_scoped`, `template_mismatch`); the issue record's write refuses a revoked key or one bound to
another address, so a racing revocation or a second service cannot slip in; a refused request releases its nonce; and
`issue serve` needs the operator's `--test-account` flag and a registry file and directory only the service user can
write.
