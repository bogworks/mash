# AT-004 record: the command contract

- Revision: e26332cdd55342538791c3088c85cc5ca2b09c59 (main, the 0.5.5 code commit)
- Run id: at004-run3, 2026-11-18, host bogfactory2, go1.27.4 linux/amd64, worktree detached at the revision, tree clean (`git status --short` empty)
- Run by: ctl. MASH_* variables were unset for every command. No key, seed or JWT appears in any output (outputs scanned for `eyJ` and seed-shaped strings: none).

## Expected (contract AT-004)
Golden text and JSON for `endpoint create` (planned, created, each refusal), `reissue`, `delete`, `list`, `show`, `mash key create`, `mash credential create` and the removed-command messages; `--dry-run` leaves the directory, the issue record and the config directory byte-identical; the command-tree test walks the `endpoint`, `key` and `credential` nouns (at most one positional, `--json` valid, `--dry-run` changes nothing, a bad flag exits 2, `--help` has an example, no aliases) with no exceptions; every refusal code has a golden case and an asserted exit code; a `create` killed between the user being signed and the record being written, run again, completes with one identity (NFR-001). Added since the first run: a killed `reissue` leaves nothing to remove and the identical retry succeeds; a plain URL is refused with exit 2 in every connecting verb; a `reissue --make-key` stopped after the new creds were written, or whose readback failed, is finished by the identical command without revoking the new key (two tests, both kill-then-retry).

## Commands (run from the repository root at the revision)

- `go test -count=1 -v ./cmd/mashctl -run 'TestEndpointCreateGolden|TestEndpointGoldens|TestEndpointBulkGolden|TestEndpointRefusals|TestEndpointRefusalCodesAreAllCovered|TestRemovedCommands|TestEndpointCommandTree|TestEndpointKilledCreate|TestEndpointCreateAfterKill|TestEndpointCreateMakeKeyKilled|TestEndpointReissueMakeKeyKilled|TestEndpointReissuePublicKeyResumesAfterKill|TestEndpointDeleteFinishes|TestEndpointDryRunRefusals|TestPlainURLRefusalExitsTwoEverywhere|TestEndpointReissueLockAfterRealKill|TestEndpointReissueMakeKeyKilledAfterPublish|TestEndpointReissueMakeKeyReadbackFailsThenRetry'`
- `go test -count=1 -v ./cmd/mash -run 'TestKeyCredentialCommandTree|TestKey|TestCred'`
- `go test -count=1 -v ./internal/keycred`
- `go test -count=1 -v ./internal/enroll -run 'KilledAfterTheIssueRecord|AfterIssue|Rerun'`
- `go vet ./...`
- `go test -count=1 ./...`

## Observed

| Command | Exit | Result |
|---|---|---|
| `./cmd/mashctl` (selected) | 0 | 21 top-level tests, 96 subtests, 0 failures, 0 skipped |
| `./cmd/mash` (selected) | 0 | 11 top-level tests, 2 subtests, 0 failures |
| `./internal/keycred` | 0 | 14 top-level tests, 6 subtests, 0 failures |
| `./internal/enroll` (selected) | 0 | 2 tests, 0 failures |
| `go vet ./...` | 0 | no output |
| `go test ./...` | 0 | 46 packages ok, none failed |

`./cmd/mashctl` tests passed: TestEndpointBulkGolden, TestEndpointCreateMakeKeyKilledAfterEnroll, TestEndpointCreateMakeKeyKilledAfterStage, TestEndpointReissueMakeKeyKilledAfterEnroll, TestEndpointDeleteFinishesAfterKill, TestEndpointCreateGolden, TestEndpointDeleteFinishesAfterKilledPurge, TestEndpointKilledCreateRerunCompletesWithOneIdentity, TestEndpointKilledCreateJSONPrintsNothing, TestEndpointGoldens, TestEndpointRefusals, TestEndpointRefusalCodesAreAllCovered, TestEndpointReissuePublicKeyResumesAfterKill, TestEndpointReissueLockAfterRealKill, TestEndpointReissueMakeKeyKilledAfterPublish, TestEndpointReissueMakeKeyReadbackFailsThenRetry, TestEndpointDryRunRefusals, TestEndpointCommandTree, TestEndpointCreateAfterKill, TestRemovedCommands, TestPlainURLRefusalExitsTwoEverywhere.

`./cmd/mash` tests passed: TestKeyCreateCLI, TestKeyCreateJSONGolden, TestCredentialCreateCLI, TestCredentialEmptyNameNeedsOut, TestCredentialJSON, TestKeycredNeverPrintsSecrets, TestKeycredUsageErrors, TestKeycredHelp, TestCredentialHelpSaysWhatIsChecked, TestKeyCreateMakesParentDirectory, TestKeyCredentialCommandTree.

`./internal/enroll` tests passed: TestRerunAfterKillCompletes, TestCreateKilledAfterTheIssueRecordCompletesOnRerun.

## Verdict
Every AT-004 assertion passed on e26332c. Not covered by this record: the live runs (AT-001 to AT-003, AT-005), the SDK gate, and the review gate. The kill tests stop the command at a seam in one process (a panic at the named point); the reissue lock test also kills a real child process with SIGKILL. Nothing here ran against a real separate host.
