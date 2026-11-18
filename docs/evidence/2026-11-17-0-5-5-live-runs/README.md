# 0.5.5 acceptance runs: AT-001 to AT-005

All five were run on the release candidate, main at `a191f08b7cdb7bbdcaf9c29144c81b8682c37e05` (every record names it as its revision, clean
checkout), on 2026-11-18. The real `mash` and `mashctl` binaries were built from that commit. One run record per scenario (the contract's
section 8: run id, revision, environment, scenario, expected, observed).

| Scenario | Result | Notes |
|---|---|---|
| AT-001, the solo developer | pass (37 assertions) | a fresh scratch home and one local server; includes REQ-005 (the printed set and the issued JWT, both exactly the contract's), REQ-011 (a live session dropped by revocation, a reconnect refused, `mash status` says why) and INV-001 |
| AT-002, the key crosses no machine | pass (25 assertions) | the public key file is carried to the mashctl host; the seed is searched for in every output and in the mashctl host's whole tree |
| AT-003, every refusal can fail | pass (102 assertions) | every refusal asserts its exit code, its refusal code, a message that names what to type, and that nothing was written |
| AT-004, the command contract | pass (written by the CLI team: 17 top-level tests and 96 subtests in `cmd/mashctl`, 11 in `cmd/mash`, 14 in `internal/keycred`, 2 in `internal/enroll`, `go vet ./...` and the whole suite clean) | goldens for every endpoint and key command, every refusal with its exit code, dry-run leaves state byte-identical, the command-tree test, the killed-create tests (`record-at004.md` lists the exact `go test` commands) |
| AT-005, across nodes | pass (81 assertions) | run by hand on 2026-11-18 by the core lane with `--hosts podman --run-by manual --host-label bf2`: three rootless podman containers (podman 4.9.3, netavark) built from the checkout, each its own filesystem and address on a private network; a certificate authority and certificates made by the test; a three-server TLS cluster; two mashctl machines set up by `adopt` with the same key material; nothing labelled is left behind (checked by the run and again by hand) |

AT-001 to AT-003 first ran on `2a204ed` (before the removed-command messages merged) and AT-005 on earlier harness revisions; those records are
replaced by these. The AT-005 run is evidence from a developer machine, not from the CI runners, and it does not include a clock-skew case (a
unit test in `internal/enroll` covers skews inside and past the lock bound). Nothing in a record holds a key, a seed or a JWT.
