# 0.5.5 acceptance runs: AT-001 to AT-005

## The runs on the code commit (final)

The code commit is main at `88e2a86b74c4ad22986a69e6479faa013bfa3300` (ruling 16: after it only documents change). AT-001 to AT-003 and
AT-005 were run on exactly that commit (every record names it as its revision, clean checkout), on 2026-11-18, by the core worker, with
the real `mash` and `mashctl` built from it. AT-001 to AT-003 ran on one local server over TLS (`mashctl init --local`'s own CA, no
override: REQ-014); AT-005 ran by hand (`--hosts podman --run-by manual --host-label bf2`) on rootless podman 4.9.3, host Linux
6.17.0-42-generic amd64, nats-server v2.15.1 built from the go.mod pin. The `nats-server` and the binaries are built from this checkout,
not taken from PATH.

| Record | Run id | Result |
|---|---|---|
| `record-at001-88e2a86.md` | 20261118T155357Z-f58f1a20 | pass (61 assertions; the sessions run through the plugin launcher and hooks; REQ-014: no override, `tls: OK`) |
| `record-at002-88e2a86.md` | 20261118T155406Z-af46ea63 | pass (25) |
| `record-at003-88e2a86.md` | 20261118T155409Z-ad1d1697 | pass (114; includes REQ-014's plain-URL refusal and override cases) |
| `record-at004-88e2a86.md` | (CLI team, run 2) | pass (written by the CLI team on `88e2a86`: 19 selected top-level tests and 96 subtests in `cmd/mashctl`, 11 in `cmd/mash`, 14 in `internal/keycred`, 2 in `internal/enroll`; 0 failed, 0 skipped; `go vet ./...` and the whole suite, 46 packages, pass; adds the flock reissue real-kill test and `TestPlainURLRefusalExitsTwoEverywhere`) |
| `record-at005-88e2a86.md` | 20261118T155239Z-075fc831 | pass (93 assertions, 4 not applicable, 0 failed); cleanup verified |
| `sdk-gate-output-88e2a86.txt` | | the SDK compatibility gate on a scratch merge of `88e2a86` and #667's head `3d877bc`, with output |
| `completion-checks.md`, `old-form-search.md`, `forbidden-word-test-output.txt`, `scrubcheck-output.txt` | | the forbidden-word test, command-tree test, scrub check and the old-form search, on `88e2a86` |
| `requirements-to-assertions.md` | | every REQ, INV, NFR and BND mapped to these records and assertion names |

The earlier records below (`a191f08`, `8fa36b8`, `bc96b27`) are kept as the history of the gate; they are not the evidence for the
release.

## History: the runs before the final gate

All five were run on the release candidate, main at `a191f08b7cdb7bbdcaf9c29144c81b8682c37e05` (every record names it as its revision, clean
checkout), on 2026-11-18. The real `mash` and `mashctl` binaries were built from that commit. One run record per scenario (the contract's
section 8: run id, revision, environment, scenario, expected, observed).

| Scenario | Result | Notes |
|---|---|---|
| AT-001, the solo developer | pass (37 assertions) | a fresh scratch home and one local server; includes REQ-005 (the printed set and the issued JWT, both exactly the contract's), REQ-011 (a live session dropped by revocation, a reconnect refused, `mash status` says why) and INV-001 |
| AT-002, the key crosses no machine | pass (25 assertions) | the public key file is carried to the mashctl host; the seed is searched for in every output and in the mashctl host's whole tree |
| AT-003, every refusal can fail | pass (102 assertions) | every refusal asserts its exit code, its refusal code, a message that names what to type, and that nothing was written |
| AT-004, the command contract | pass (written by the CLI team: 17 top-level tests and 96 subtests in `cmd/mashctl`, 11 in `cmd/mash`, 14 in `internal/keycred`, 2 in `internal/enroll`, `go vet ./...` and the whole suite clean) | goldens for every endpoint and key command, every refusal with its exit code, dry-run leaves state byte-identical, the command-tree test, the killed-create tests (`record-at004.md` lists the exact `go test` commands) |
| AT-005, across nodes | pass (79 assertions, 2 not applicable, 0 failed) | run by hand on 2026-11-18 by the core worker with `--hosts podman --run-by manual --host-label bf2`: three rootless podman containers (podman 4.9.3, netavark) built from the checkout, each its own filesystem and address on a private network; a certificate authority and certificates made by the test; a three-server TLS cluster; two mashctl machines set up by `adopt` with the same key material; nothing labelled is left behind (checked by the run and again by hand); the two `endpoint create` acknowledgement checks are recorded as n/a, not as passes (create changes no account record; contract revision 8), and the created key's acceptance on each of the three servers is its own check |

AT-005's record is of harness `67aa3a5` (the same patch is in this pull request as its own commit, so the checked-in harness is the one that ran; it is also #776) (main `a191f08` plus a change in `scripts/liveat` only: the n/a relabelling; the product code under test is identical to `a191f08`, the record names `67aa3a5` as its revision). AT-001 to AT-003 first ran on `2a204ed` (before the removed-command messages merged) and AT-005 on earlier harness revisions; those records are
replaced by these. The AT-005 run is evidence from a developer machine, not from the CI runners, and it does not include a clock-skew case (a
unit test in `internal/enroll` covers skews inside and past the lock bound). Nothing in a record holds a key, a seed or a JWT.

## Reruns after the owner-scoped reply inboxes (#780)

The final gate found that every endpoint could publish to `_INBOX.>` (INV-002); the fix is #780 (contract revision 9, which this pull
request also carries). The acceptance tests were run again, and the records are NEW files beside the `a191f08` ones, not replacements:

| Record | Revision | Result |
|---|---|---|
| `record-at001-8fa36b8.md` | `8fa36b8` (#780's head plus the harness patch of this pull request) | pass (37 assertions) |
| `record-at002-8fa36b8.md` | `8fa36b8` | pass (25 assertions) |
| `record-at003-8fa36b8.md` | `8fa36b8` | pass (106 assertions; includes the cross-owner case: bob's forged discovery answer and replayed presence answer are refused by the server) |
| `record-at005-bc96b27.md` | `bc96b27` (#780's head plus the harness patches of this pull request, including the second-owner case and the owner-derived JWT file names) | pass (92 assertions, 4 not applicable, 0 failed; podman, manual, host label bf2; nothing labelled left) |

AT-005 now has the second-owner case of contract revision 9: an endpoint of a second owner, on one node of the cluster, forges a discovery
answer and replays a presence answer into alice's in-flight reply subject while alice probes from another node; the servers refuse both
(`{"seen_requests":1,"violations":2}`), and alice's discovery does not list her offline endpoint `alice/dev/ghost` as online. The harness
changes after `8fa36b8` touch only AT-005 (`at005.go`, the in-image helper), so AT-001 to AT-003 were not run again on `bc96b27`; this pull request now contains #780's change itself, so its tree is that of the record. The
earlier AT-005 record (`record-at005.md`, on `a191f08`) is kept for the history of the gate.
