# Run record at002: 20261118T190236Z-82391da9

- Run id: 20261118T190236Z-82391da9
- Revision: e26332cdd55342538791c3088c85cc5ca2b09c59 (clean)

## Environment

- linux/amd64
- go: go1.27.4
- nats-server: v2.15.1 (built from the go.mod pin v2.15.1)
- scratch (relative to the run directory): at002/home-a, at002/home-b

## Scenario

AT-002, the key crosses no machine: two scratch homes, A (mashctl host) and B (endpoint). B runs mash key create; A runs mashctl endpoint create alice/dev/design --public-key with B's public key file; B runs mash credential create and connects; the endpoint mails a peer. All captured output and A's home are searched for B's seed, and all captured output for the JWT.

## Expected

- two scratch homes are made
- mashctl init --local --owner alice --start brings up one local server
- mash key create makes design's key on its own machine
- design seed file is mode 0600
- the public key file is carried to the mashctl host
- mashctl endpoint create alice/dev/design --public-key signs for the endpoint's own key
- design JWT file is mode 0600
- mash credential create assembles design's creds file on its machine
- mash key create makes review's key on its own machine
- review seed file is mode 0600
- the public key file is carried to the mashctl host
- mashctl endpoint create alice/dev/review --public-key signs for the endpoint's own key
- review JWT file is mode 0600
- B's design session connects with the creds file it assembled
- B's design session is online
- the endpoint on B mails its peer
- A sees the mail in the peer's inbox (sent by alice.dev.design)
- the seed and the JWT of B's design were read for the search
- a search of A's whole home for B's seed finds nothing
- a search of the server directory for B's seed finds nothing
- a search of all captured output for B's seed finds nothing
- no captured output contains the JWT
- INV-001: no command output holds a seed or a JWT
- INV-001: no command output holds a key or JWT this scenario made
- INV-001: every command's output was captured completely (no capture or read error)

## Observed

- pass: two scratch homes are made
- pass: mashctl init --local --owner alice --start brings up one local server (exit 0)
- pass: mash key create makes design's key on its own machine (exit 0)
- pass: design seed file is mode 0600 (mode 0600)
- pass: the public key file is carried to the mashctl host
- pass: mashctl endpoint create alice/dev/design --public-key signs for the endpoint's own key (exit 0)
- pass: design JWT file is mode 0600 (mode 0600)
- pass: mash credential create assembles design's creds file on its machine (exit 0)
- pass: mash key create makes review's key on its own machine (exit 0)
- pass: review seed file is mode 0600 (mode 0600)
- pass: the public key file is carried to the mashctl host
- pass: mashctl endpoint create alice/dev/review --public-key signs for the endpoint's own key (exit 0)
- pass: review JWT file is mode 0600 (mode 0600)
- pass: B's design session connects with the creds file it assembled
- pass: B's design session is online
- pass: the endpoint on B mails its peer (exit 0)
- pass: A sees the mail in the peer's inbox (sent by alice.dev.design) (exit 0)
- pass: the seed and the JWT of B's design were read for the search
- pass: a search of A's whole home for B's seed finds nothing
- pass: a search of the server directory for B's seed finds nothing
- pass: a search of all captured output for B's seed finds nothing
- pass: no captured output contains the JWT
- pass: INV-001: no command output holds a seed or a JWT
- pass: INV-001: no command output holds a key or JWT this scenario made
- pass: INV-001: every command's output was captured completely (no capture or read error)

## Result

PASS
