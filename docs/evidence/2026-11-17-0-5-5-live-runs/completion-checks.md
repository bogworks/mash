# 0.5.5 completion checks (contract section 8)

Code commit: `e26332cdd55342538791c3088c85cc5ca2b09c59` (main; the release/code commit). Run 2026-11-18 on branch `pr/core/records-e26332c`, whose code equals that commit; only docs differ (`git diff e26332c --stat -- . ':!docs'` is empty). The records of the acceptance tests are beside this file.

## The forbidden-word test (BND-001)

```
$ MASH_FORBIDDEN_WORDS="rangehand,herdr,herd,lane,lead" go test ./cmd/mash -run 'Standalone|ForbiddenWordsHelpers' -count=1 -v
--- PASS: TestStandaloneServeWithoutAHost (1.40s)
--- PASS: TestStandaloneLivePrompt (1.09s)
--- PASS: TestStandaloneHookDelivery (1.18s)
--- PASS: TestStandaloneOnlyNATS (0.39s)
--- PASS: TestStandaloneEnvOnlyConfig (0.37s)
--- PASS: TestForbiddenWordsHelpers (0.00s)
PASS
ok  	github.com/bogworks/mash/cmd/mash	4.439s
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
    --- PASS: TestEndpointCommandTree/reissue (0.01s)
    --- PASS: TestEndpointCommandTree/delete (0.01s)
    --- PASS: TestEndpointCommandTree/list (0.00s)
    --- PASS: TestEndpointCommandTree/show (0.00s)
PASS
ok  	github.com/bogworks/mash/cmd/mashctl	0.094s
exit 0
```

(The mashctl run also prints one line per subtest, `mash: WARNING: plain-text connection to 127.0.0.1 (insecure_plaintext is set); ...`, from the test's own plain local server; those lines are left out above and are not failures.)

## go vet and the package tests

```
$ go vet ./...
(no output; exit 0)

$ go test ./internal/release ./internal/portable ./scripts/scrubcheck ./cmd/mash ./cmd/mashctl -count=1
ok  	github.com/bogworks/mash/internal/release	17.644s
ok  	github.com/bogworks/mash/internal/portable	0.086s
ok  	github.com/bogworks/mash/scripts/scrubcheck	0.047s
ok  	github.com/bogworks/mash/cmd/mash	238.207s
ok  	github.com/bogworks/mash/cmd/mashctl	174.596s
exit 0
```

## The scrub check

`go run ./scripts/scrubcheck` (its own words list says it is a one-time list, not a CI gate; run with no arguments from the repository root): full output in `scrubcheck-output.txt`: **397 hits, 37 excepted, 5 exception problems; exit 1.** Exit 1 is the same state as on the earlier revision (the exceptions file lists entries for text that no longer exists: `exceptions.tsv` lines 13 to 16 and 40); it is not a pass. The hits by place (the 37 excepted hits are counted in the total and sit elsewhere):

| Place | Hits |
|---|---|
| `docs/decisions/` (history of decisions, with `INDEX.md`) | 27 |
| `docs/plans/` (design plans) | 69 |
| `contracts/` (the contract and its build plan) | 11 |
| `receipts/` (the three gate review records; one new since the earlier run) | 3 |
| `docs/evidence/2026-11-17-0-5-5-live-runs/` | 250 |

The 250 in this directory are all in the record files of this check, not in findings: the committed copy of `scrubcheck-output.txt` (240; the output lists each word it found, with the path it found it in, so it contains the words again), `forbidden-word-test-output.txt` (4: the environment value in the command), `completion-checks.md` (4: the same environment value in the command) and `old-form-search.md` (2: the forbidden-name list row). The scan reads the files as they stood when it ran: `scrubcheck-output.txt` was the earlier copy, and the two files written before the run were the new ones. Because the check scans its own earlier record, each later run counts more in this directory until an exception covers these record files; that is not changed here. Without this directory the count is 110 (the earlier run: 109, plus the one new receipt, the gate review record of the previous code commit). No hit is in shipped code, a test, a flag, a field or an error.

## The SDK compatibility gate (BND-002)

See `sdk-gate-output-e26332c.txt`, which holds the code commit, the merged gate commit (`pr/ctl/sdk-compat-gate` at `3d877bcd34d00ab590b6c4996e8f14242ec6c3f0`, the CLI team's pull request #667) and the output of the gate run: the compat tests of `internal/sap`, `internal/host`, `internal/spool` and `internal/endpoint` pass (`TestCompatCorpusStored`, `TestCompatCorpusLive`, `TestProtocolConstants`, `TestCompatRenderGolden`, `TestCompatTrustLabelStrings`, `TestCompatSpoolEntryBytes`, `TestCompatSpoolReadsGoldens`, `TestCompatStoredNewFormInbox` and the trust-constant test). The gate is not on main, so it was run on a scratch merge, not committed or pushed; the one change to it is a port of `internal/endpoint/compat_stored_test.go` (shown in the file: the legacy-inbox test is dropped and the new-form test uses the current fixture calls). `TestCompatStoredLegacyInbox` is not applicable: it replays an inbox of the old form, which 0.5.5 removes (the form is refused, not supported).

## The old-form source search

`old-form-search.md`: the earlier terms (searches 1 to 3) and, new, the names REQ-014 removed (`insecure_loopback`, `MASH_INSECURE_LOOPBACK`, `require_tls`, `MASH_REQUIRE_TLS`; search 4, whole tree): each hit with file:line and its class. Result: no hit in a non-test source file; every hit is a test asserting the old key is ignored, an ADR, changelog or release-note text, the contract, a past record, or a gate receipt. Search 4 excludes only the two files that are this record (`old-form-search.md` and `completion-checks.md`, which quote the terms). Counts: search 1 = 26, search 2 = 34 (one new row since 88e2a86: the previous code commit's gate receipt), search 3 = 0, search 4 = 38.

## Run records

`record-at001-e26332c.md`, `record-at002-e26332c.md`, `record-at003-e26332c.md` and `record-at005-e26332c.md` (on the code commit). AT-004 is `record-at004-e26332c.md` (the CLI team's run, on the earlier code commit 88e2a86; no AT-004 record of e26332c is in this directory). The earlier-revision records stay beside them, named by their commit.
