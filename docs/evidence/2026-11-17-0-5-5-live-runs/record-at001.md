# Run record at001: 20261118T014439Z-e4f369ac

- Run id: 20261118T014439Z-e4f369ac
- Revision: a191f08b7cdb7bbdcaf9c29144c81b8682c37e05 (clean)

## Environment

- linux/amd64
- go: go1.27.4
- nats-server: nats-server: v2.15.1
- scratch (relative to the run directory): at001/home

## Scenario

AT-001, the solo developer, standalone: a fresh scratch home; mashctl init --local --owner alice --start; for design and review, mash key create, mashctl endpoint create alice/dev/<name> --public-key, mash credential create; two sessions (mash serve) with MASH_OWNER=alice MASH_GROUP=dev MASH_NAME=design|review; mail each way; endpoint list --json; service metadata (mash who --json); mash admin permissions alice.dev.design; mashctl endpoint delete alice/dev/review, then the review session's connection, a reconnect and mash status.

## Expected

- a fresh scratch home is made
- mashctl init --local --owner alice --start brings up one local server
- mash key create makes design's key on its own machine
- design seed file is mode 0600
- mashctl endpoint create alice/dev/design --public-key signs for the endpoint's own key
- design JWT file is mode 0600
- mash credential create assembles design's creds file on its machine
- mash key create makes review's key on its own machine
- review seed file is mode 0600
- mashctl endpoint create alice/dev/review --public-key signs for the endpoint's own key
- review JWT file is mode 0600
- mash credential create assembles review's creds file on its machine
- the design session starts with MASH_GROUP=dev and completes the MCP handshake
- the review session starts with MASH_GROUP=dev and completes the MCP handshake
- both sessions are online
- mail design-to-review: mash send succeeds
- mail design-to-review: the recipient reads the text
- mail design-to-review: the recipient sees it labelled verified
- mail review-to-design: mash send succeeds
- mail review-to-design: the recipient reads the text
- mail review-to-design: the recipient sees it labelled verified
- mashctl endpoint list --json succeeds
- endpoint list --json shows design and review, each with group dev
- REQ-006: the service metadata of design shows agent claude-code and its owner, group and instance
- REQ-006: the service metadata of review shows agent claude-code and its owner, group and instance
- mash admin permissions alice.dev.design succeeds
- REQ-005: the credential's publish and subscribe set is exactly the contract's
- INV-002: no line names another owner or a wildcard in the owner slot
- REQ-005: the issued credential's publish and subscribe set is exactly the contract's (decoded from the JWT)
- review's creds file is copied to a private path before the delete
- mashctl endpoint delete alice/dev/review succeeds
- REQ-011: the review session's connection is closed (its serve exits or reports the authorization failure)
- REQ-011: a reconnect by the review key is refused as not authorized
- REQ-011: mash status says why (an authorization failure)
- INV-001: no command output holds a seed or a JWT
- INV-001: no command output holds a key or JWT this scenario made
- INV-001: every command's output was captured completely (no capture or read error)

## Observed

- pass: a fresh scratch home is made
- pass: mashctl init --local --owner alice --start brings up one local server (exit 0)
- pass: mash key create makes design's key on its own machine (exit 0)
- pass: design seed file is mode 0600 (mode 0600)
- pass: mashctl endpoint create alice/dev/design --public-key signs for the endpoint's own key (exit 0)
- pass: design JWT file is mode 0600 (mode 0600)
- pass: mash credential create assembles design's creds file on its machine (exit 0)
- pass: mash key create makes review's key on its own machine (exit 0)
- pass: review seed file is mode 0600 (mode 0600)
- pass: mashctl endpoint create alice/dev/review --public-key signs for the endpoint's own key (exit 0)
- pass: review JWT file is mode 0600 (mode 0600)
- pass: mash credential create assembles review's creds file on its machine (exit 0)
- pass: the design session starts with MASH_GROUP=dev and completes the MCP handshake
- pass: the review session starts with MASH_GROUP=dev and completes the MCP handshake
- pass: both sessions are online
- pass: mail design-to-review: mash send succeeds (exit 0)
- pass: mail design-to-review: the recipient reads the text (exit 0)
- pass: mail design-to-review: the recipient sees it labelled verified
- pass: mail review-to-design: mash send succeeds (exit 0)
- pass: mail review-to-design: the recipient reads the text (exit 0)
- pass: mail review-to-design: the recipient sees it labelled verified
- pass: mashctl endpoint list --json succeeds (exit 0)
- pass: endpoint list --json shows design and review, each with group dev (groups seen: map[design:dev review:dev])
- pass: REQ-006: the service metadata of design shows agent claude-code and its owner, group and instance (agent "claude-code" owner "alice" group "dev" instance "design")
- pass: REQ-006: the service metadata of review shows agent claude-code and its owner, group and instance (agent "claude-code" owner "alice" group "dev" instance "review")
- pass: mash admin permissions alice.dev.design succeeds (exit 0)
- pass: REQ-005: the credential's publish and subscribe set is exactly the contract's
- pass: INV-002: no line names another owner or a wildcard in the owner slot
- pass: REQ-005: the issued credential's publish and subscribe set is exactly the contract's (decoded from the JWT)
- pass: review's creds file is copied to a private path before the delete
- pass: mashctl endpoint delete alice/dev/review succeeds (exit 0)
- pass: REQ-011: the review session's connection is closed (its serve exits or reports the authorization failure)
- pass: REQ-011: a reconnect by the review key is refused as not authorized (exit 1)
- pass: REQ-011: mash status says why (an authorization failure) (exit 0)
- pass: INV-001: no command output holds a seed or a JWT
- pass: INV-001: no command output holds a key or JWT this scenario made
- pass: INV-001: every command's output was captured completely (no capture or read error)

## Result

PASS
