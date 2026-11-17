# 0.5.5 live runs on one machine: AT-001, AT-002, AT-003

Run on main at `2a204ed` (the merge of the endpoint noun), 2026-11-17, with `go run ./scripts/liveat all --port <P>`: the real `mash` and
`mashctl` binaries built from that commit, a scratch home per scenario, one local NATS server from `mashctl init --local --start`, every
command's output captured and scanned for a seed or a JWT. One run record per scenario (the contract's section 8: run id, revision,
environment, scenario, expected, observed).

| Scenario | Result | Notes |
|---|---|---|
| AT-001, the solo developer | pass (37 assertions) | includes REQ-005 (the printed set and the issued JWT, both exactly the contract's), REQ-011 (a live session dropped by revocation, a reconnect refused, `mash status` says why) and INV-001 |
| AT-002, the key crosses no machine | pass (25 assertions) | the public key file is carried to the mashctl host; the seed is searched for in every output and in the mashctl host's whole tree |
| AT-003, every refusal can fail | 3 assertions fail, the rest pass | `mashctl add`, `reissue` and `retire` print `unknown command` (and `retire` a usage error) and do not name `endpoint create`, `endpoint reissue`, `endpoint delete`: the removed-command messages land with the CLI team's follow-up (#754); AT-003 is to be re-run when it merges |

This is the one-machine evidence only. AT-004 (the command contract) is the CLI team's golden tests; AT-005 (across machines) has its own record
when it runs. Nothing in a record holds a key, a seed or a JWT.
