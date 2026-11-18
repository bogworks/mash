# AT-004 record: the command contract

- Revision: a191f08b7cdb7bbdcaf9c29144c81b8682c37e05 (main, the 0.5.5 release candidate; merge of #749)
- Run id: at004-run1, 2026-11-18, host bf2, go1.27.4 linux/amd64, worktree detached at the revision, tree clean
- Run by: ctl. No key, seed or JWT appears in any output below (outputs scanned for `eyJ` and seed-shaped strings: none).

## Expected (contract AT-004)
Golden text and JSON for `endpoint create` (planned, created, each refusal), `reissue`, `delete`, `list`, `show`, `mash key create`, `mash credential create` and the removed-command messages; `--dry-run` leaves the directory, the issue record and the config directory byte-identical; the command-tree test of rule 22 walks the `endpoint`, `key` and `credential` nouns (at most one positional, `--json` valid, `--dry-run` changes nothing, a bad flag exits 2, `--help` has an example, no aliases) with no exceptions; every refusal code has a golden case and an asserted exit code; a `create` killed between the user being signed and the record being written, run again, completes with one identity (NFR-001).

## Commands (run from the repository root at the revision)

- `go test -count=1 -v ./cmd/mashctl -run 'TestEndpointCreateGolden|TestEndpointGoldens|TestEndpointBulkGolden|TestEndpointRefusals|TestEndpointRefusalCodesAreAllCovered|TestRemovedCommands|TestEndpointCommandTree|TestEndpointKilledCreate|TestEndpointCreateAfterKill|TestEndpointCreateMakeKeyKilled|TestEndpointReissueMakeKeyKilled|TestEndpointReissuePublicKeyResumesAfterKill|TestEndpointDeleteFinishes|TestEndpointDryRunRefusals'` (output: mashctl.txt)
- `go test -count=1 -v ./cmd/mash -run 'TestKeyCredentialCommandTree|TestKey|TestCred'` (output: mash.txt)
- `go test -count=1 -v ./internal/keycred` (output: keycred.txt)
- `go test -count=1 -v ./internal/enroll -run 'KilledAfterTheIssueRecord|AfterIssue|Rerun'` (output: enroll.txt)
- `go vet ./...` (output: vet.txt)
- `go test -count=1 ./...` (output: full.txt)

## Observed

### ./cmd/mashctl: 17 top-level tests, 96 subtests, 0 failures, exit 0

- PASS TestEndpointBulkGolden (0.15s)
- PASS TestEndpointCreateMakeKeyKilledAfterEnroll (0.09s)
- PASS TestEndpointCreateMakeKeyKilledAfterStage (0.09s)
- PASS TestEndpointReissueMakeKeyKilledAfterEnroll (0.10s)
- PASS TestEndpointDeleteFinishesAfterKill (0.07s)
- PASS TestEndpointCreateGolden (0.07s)
- PASS TestEndpointDeleteFinishesAfterKilledPurge (0.22s)
- PASS TestEndpointKilledCreateRerunCompletesWithOneIdentity (0.15s)
- PASS TestEndpointKilledCreateJSONPrintsNothing (0.07s)
- PASS TestEndpointGoldens (0.07s)
- PASS TestEndpointRefusals (4.46s)
- PASS TestEndpointRefusalCodesAreAllCovered (0.00s)
- PASS TestEndpointReissuePublicKeyResumesAfterKill (0.09s)
- PASS TestEndpointDryRunRefusals (0.09s)
- PASS TestEndpointCommandTree (0.08s)
- PASS TestEndpointCreateAfterKill (0.10s)
- PASS TestRemovedCommands (0.00s)

### ./cmd/mash: 11 top-level tests, 2 subtests, 0 failures, exit 0

- PASS TestKeyCreateCLI (0.00s)
- PASS TestKeyCreateJSONGolden (0.00s)
- PASS TestCredentialCreateCLI (0.00s)
- PASS TestCredentialEmptyNameNeedsOut (0.00s)
- PASS TestCredentialJSON (0.00s)
- PASS TestKeycredNeverPrintsSecrets (0.00s)
- PASS TestKeycredUsageErrors (0.00s)
- PASS TestKeycredHelp (0.00s)
- PASS TestCredentialHelpSaysWhatIsChecked (0.00s)
- PASS TestKeyCreateMakesParentDirectory (0.00s)
- PASS TestKeyCredentialCommandTree (0.00s)

### ./internal/keycred: 14 top-level tests, 6 subtests, 0 failures, exit 0

- PASS TestPublicPathFor (0.00s)
- PASS TestCreateKey (0.00s)
- PASS TestCreateKeyRefusesOverwrite (0.00s)
- PASS TestCreateKeyPublicFailsLeavesNothing (0.00s)
- PASS TestCreateKeyNoFinalFileOnWriteFailure (0.00s)
- PASS TestCreateKeyStaleTempDoesNotBlock (0.00s)
- PASS TestCreateKeyLeavesNoTempFile (0.00s)
- PASS TestCreateKeyMakesParentDirectories (0.00s)
- PASS TestCreateCredentialsRoundTrip (0.00s)
- PASS TestCreateCredentialsMismatch (0.00s)
- PASS TestCreateCredentialsExistingAndForce (0.00s)
- PASS TestCreateCredentialsDefaultOut (0.00s)
- PASS TestCreateCredentialsDryRun (0.00s)
- PASS TestCreateCredentialsRefusesInputAsOutput (0.00s)

### ./internal/enroll: 2 top-level tests, 0 subtests, 0 failures, exit 0

- PASS TestRerunAfterKillCompletes (0.08s)
- PASS TestCreateKilledAfterTheIssueRecordCompletesOnRerun (0.06s)

### `go vet ./...`: exit 0, no output

### `go test -count=1 ./...`: exit 0, 46 packages ok, 0 FAIL lines

## Result
PASS: every expected item above is covered by a named test that passed on the revision; failures: 0.
