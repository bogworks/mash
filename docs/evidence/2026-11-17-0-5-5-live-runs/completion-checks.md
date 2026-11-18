# 0.5.5 completion checks (contract section 8)

Run 2026-11-18 on main at `a191f08` plus this pull request (the records of the acceptance tests are beside this file).

## The forbidden-word test (BND-001)

`MASH_FORBIDDEN_WORDS="rangehand,herdr,herd,lane,lead" go test ./cmd/mash -run 'Standalone|ForbiddenWordsHelpers' -count=1 -v`
(the form CI runs, with the maintainer's list in the variable). Output: `forbidden-word-test-output.txt`: all six tests pass
(`TestStandaloneServeWithoutAHost`, `TestStandaloneLivePrompt`, `TestStandaloneHookDelivery`, `TestStandaloneOnlyNATS`,
`TestStandaloneEnvOnlyConfig`, `TestForbiddenWordsHelpers`): a mash with no layer above reads no variable and no word of one,
and no output carries one. `go test ./scripts/scrubcheck ./internal/portable` also passes.

The one-time scrub tool, `go run ./scripts/scrubcheck` (its own words list says it is a one-time list, not a CI gate), was
also run: full output in `scrubcheck-output.txt`: 144 hits, 37 excepted, 5 exception problems (the exceptions file lists
entries for text that no longer exists). None of the hits is in shipped code, a test, a flag, a field or an error: they
are in the history of decisions (`docs/decisions/`, including `INDEX.md`, which carries those records' titles), in design
plans (`docs/plans/`) and in the text of the contract and its build plan (`contracts/`). The three hits that were in
shipped surfaces (a comment in `scripts/liveat/cluster.go`, a comment in `cmd/mash/naming21_cli_test.go` and a word in the
evidence README) are fixed in this pull request.

## The SDK compatibility gate (BND-002)

The gate the contract calls "the gate that exists today" is the CLI team's pull request #667 (corpus replay through mash's
and the SDK's classifiers, stored-inbox replay, spool label identity, rendered labels). It is **not on main**. What was run:
a scratch merge of main (`a191f08`) and the branch of #667, not committed or pushed.

| Package | Tests run | Result |
|---|---|---|
| `internal/sap` | `TestCompatCorpusStored`, `TestCompatCorpusLive`, `TestCompatTrustConstantsMatchSpoolGolden`, `TestProtocolConstants` | pass |
| `internal/host` | `TestCompatRenderGolden` | pass |
| `internal/spool` | `TestCompatTrustLabelStrings`, `TestCompatSpoolEntryBytes`, `TestCompatSpoolReadsGoldens` | pass |
| `internal/endpoint` | `TestCompatStoredNewFormInbox` (after the one-line fixture port: it called a fixture helper the address change removed) | pass |
| `internal/endpoint` | `TestCompatStoredLegacyInbox` | not applicable: it replays an inbox of the old form, which 0.5.5 removes; the form is refused, not supported |

The merged branch also changes `go.mod` to depend on the SAP Go SDK; that part was not changed here. #667 needs a rebase onto
main (the endpoint fixture port and dropping the legacy-inbox case) before it can become the gate on main; until then the
protocol is covered on main by the known-answer and golden tests of `internal/sap` (`TestSignKnownAnswerB13`,
`TestVerifyStoredClasses`, `TestGoldenAddressIdentifiers`), which pass in every run of the suite. No change to the SAP protocol
and no divergence from the SDK was made by 0.5.5: the subject keeps its shape and its token count, and the departure from the
SDK's recommendation for the first slot is documented.

## The old-form source search

`old-form-search.md`, with its full output and one row for each hit.

## Run records

`record-at001.md` to `record-at005.md` (on `a191f08`) and the `-8fa36b8` files (after the owner-scoped reply inboxes); AT-004 is
`record-at004.md`.
