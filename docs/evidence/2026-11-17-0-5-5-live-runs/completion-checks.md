# 0.5.5 completion checks (contract section 8)

Code commit: `88e2a86b74c4ad22986a69e6479faa013bfa3300` (main; the release/code commit). Run 2026-11-18 on branch `pr/core/records-88e2a86`, whose tree equals that commit apart from docs/evidence (`git diff 88e2a86 --stat -- . ':!docs/evidence'` is empty). The records of the acceptance tests are beside this file.

## The forbidden-word test (BND-001)

```
$ MASH_FORBIDDEN_WORDS="rangehand,herdr,herd,lane,lead" go test ./cmd/mash -run 'Standalone|ForbiddenWordsHelpers' -count=1 -v
--- PASS: TestStandaloneServeWithoutAHost (1.47s)
--- PASS: TestStandaloneLivePrompt (1.07s)
--- PASS: TestStandaloneHookDelivery (1.11s)
--- PASS: TestStandaloneOnlyNATS (0.39s)
--- PASS: TestStandaloneEnvOnlyConfig (0.36s)
--- PASS: TestForbiddenWordsHelpers (0.00s)
PASS
ok  	github.com/bogworks/mash/cmd/mash	4.398s
```

The form CI runs, with the maintainer's list in the variable. Full output: `forbidden-word-test-output.txt`. All six tests pass (`TestStandaloneServeWithoutAHost`, `TestStandaloneLivePrompt`, `TestStandaloneHookDelivery`, `TestStandaloneOnlyNATS`, `TestStandaloneEnvOnlyConfig`, `TestForbiddenWordsHelpers`): a mash with no layer above reads no variable and no word of one, and no output carries one. The forbidden-name list that `internal/portable` enforces is `internal/portable/portable_test.go:36` (`personalRE`) and its samples at `:482`; `go test ./internal/portable` passes (below).

## The command-tree tests

Found with `grep -rniE 'command tree|CommandTree' cmd`: `TestKeyCredentialCommandTree` (`cmd/mash/keycred_tree_test.go:74`) and `TestEndpointCommandTree` (`cmd/mashctl/endpoint_tree_test.go:52`). Both pass.

```
$ go test ./cmd/mash -run 'TestKeyCredentialCommandTree' -count=1 -v
=== RUN   TestKeyCredentialCommandTree
=== RUN   TestKeyCredentialCommandTree/key_create
=== RUN   TestKeyCredentialCommandTree/credential_create
--- PASS: TestKeyCredentialCommandTree (0.00s)
    --- PASS: TestKeyCredentialCommandTree/key_create (0.00s)
    --- PASS: TestKeyCredentialCommandTree/credential_create (0.00s)
PASS
ok  	github.com/bogworks/mash/cmd/mash	0.004s
exit 0

$ go test ./cmd/mashctl -run 'TestEndpointCommandTree' -count=1 -v
=== RUN   TestEndpointCommandTree
=== RUN   TestEndpointCommandTree/create
=== RUN   TestEndpointCommandTree/reissue
=== RUN   TestEndpointCommandTree/delete
=== RUN   TestEndpointCommandTree/list
=== RUN   TestEndpointCommandTree/show
--- PASS: TestEndpointCommandTree (0.09s)
    --- PASS: TestEndpointCommandTree/create (0.01s)
    --- PASS: TestEndpointCommandTree/reissue (0.00s)
    --- PASS: TestEndpointCommandTree/delete (0.00s)
    --- PASS: TestEndpointCommandTree/list (0.00s)
    --- PASS: TestEndpointCommandTree/show (0.00s)
PASS
ok  	github.com/bogworks/mash/cmd/mashctl	0.091s
exit 0
```

(The mashctl run also prints one line per subtest, `mash: WARNING: plain-text connection to 127.0.0.1 (insecure_plaintext is set); ...`, from the test's own plain local server; those lines are left out above and are not failures.)

## go vet and the package tests

```
$ go vet ./...
(no output; exit 0)

$ go test ./internal/release ./internal/portable ./scripts/scrubcheck ./cmd/mash ./cmd/mashctl -count=1
ok  	github.com/bogworks/mash/internal/release	17.713s
ok  	github.com/bogworks/mash/internal/portable	0.091s
ok  	github.com/bogworks/mash/scripts/scrubcheck	0.050s
ok  	github.com/bogworks/mash/cmd/mash	238.690s
ok  	github.com/bogworks/mash/cmd/mashctl	174.048s
exit 0
```

## The scrub check

`go run ./scripts/scrubcheck` (its own words list says it is a one-time list, not a CI gate; run with no arguments from the repository root): full output in `scrubcheck-output.txt`: **268 hits, 37 excepted, 5 exception problems; exit 1.** Exit 1 is the same state as on the earlier revision (the exceptions file lists entries for text that no longer exists: `exceptions.tsv` lines 13 to 16 and 40). The hits by place:

| Place | Hits |
|---|---|
| `docs/decisions/` (history of decisions, with `INDEX.md`) | 27 |
| `docs/plans/` (design plans) | 69 |
| `contracts/` (the contract and its build plan) | 11 |
| `receipts/` (the two gate review records; new since the earlier run) | 2 |
| `docs/evidence/2026-11-17-0-5-5-live-runs/` | 122 |

The 122 in this directory are all in the files this run replaces: the committed copies of `scrubcheck-output.txt` (116, the earlier output lists the words it found), `completion-checks.md` (4: the environment value in the forbidden-word command) and `old-form-search.md` (2: the forbidden-name list row). They are the check scanning its own earlier record, not a finding; without them the count is 146 (144 before, plus the 2 receipts). The files written by this run name the same words again (the command above, the output file and the forbidden-name row), so the same self-reference recurs on any later run until an exception covers these record files; that is not changed here. No hit is in shipped code, a test, a flag, a field or an error.

## The SDK compatibility gate (BND-002)

See `sdk-gate-output-88e2a86.txt`, which holds both commits and the output of the gate run. The gate (the CLI team's pull request #667) is not on main, so it was run on a scratch merge, not committed or pushed; the one change to it is a one-line port of the endpoint fixture call in `internal/endpoint/compat_stored_test.go` (shown in the file). `TestCompatStoredLegacyInbox` is not applicable: it replays an inbox of the old form, which 0.5.5 removes (the form is refused, not supported).

## The old-form source search

`old-form-search.md`: the earlier terms (searches 1 to 3) and, new, the names REQ-014 removed (`insecure_loopback`, `MASH_INSECURE_LOOPBACK`, `require_tls`, `MASH_REQUIRE_TLS`; search 4, whole tree): each hit with file:line and its class. Result: no hit in a non-test source file; every hit is a test asserting the old key is ignored, an ADR, changelog or release-note text, the contract, a past record, or a gate receipt.

## Run records

`record-at001-88e2a86.md`, `record-at002-88e2a86.md`, `record-at003-88e2a86.md` and `record-at005-88e2a86.md` (on the code commit); AT-004 is `record-at004.md`. The earlier-revision records stay beside them, named by their commit.
