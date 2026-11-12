`mashctl issue serve` signs a user JWT for a public key over NATS: a wrangler on the sender's host countersigns the key and
address, mash takes the tags from its own table, records the issue, and no private key is made, received or logged. The
wrangler registry (`mashctl wrangler init|add|list|remove`) is for test accounts only, because real wrangler registration
is not decided ([ADR 0104](docs/decisions/0104-an-issue-service-signs-a-credential-for-a-public-key-a-wrangler-countersigned.md)).
