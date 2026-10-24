# Releasing mash

A release is a **signed tag `vN.N.N` at a candidate commit X on main**. X is built from fresh clones in different
directories with a pinned toolchain; the build is byte-reproducible and `mash version` prints
`mash <N.N.N> (commit X)`. The lead tags; CI builds and drafts; the lead publishes after the gate.

## Procedure

1. **Candidate.** X is a commit on `main` of `randybias/mash` with CI green: `test (ubuntu-latest)`,
   `test (macos-latest)` and `reproducible` (all from GitHub Actions) on X itself.
2. **Gate, before tagging:** `scripts/release-check <X> <version>` (for example `… 3f2a… 0.1.0`). It fetches main from
   the release repository (never a configured origin), checks X is an ancestor, reads X's check runs through `gh`
   (every page, latest run per name, from `github-actions` only), builds X twice in fresh clones, and runs the binary to
   compare `mash <version> (commit X)` exactly. It prints the pin for Rangehand's `compat.json`. Exit 0 passed, 3 refused,
   4 could not decide (gh, the network), 2 usage.
3. **Tag:** `git tag -s v<version> X && git push origin v<version>` (annotated and signed; the lead's key).
4. **CI:** `release.yml` guards the tag (name `vN.N.N`, annotated, signature present, X on main), builds the four
   targets (`mash-{linux,darwin}-{amd64,arm64}`) twice each in fresh clones and fails on any mismatch, then creates a
   **draft** release with the binaries, `SHA256SUMS` and a changelog.
5. **Gate, before publishing:** `scripts/release-check --assets <version> <X>`: the remote tag exists, is annotated,
   verifies against this machine's keys, peels to X and is still that object after the assets were read; the release
   carries exactly the four binaries and `SHA256SUMS`, each equal to a fresh build of X made here. Then publish the draft.
6. **Pin:** in Rangehand's `compat.json` set `mash` to `{"version": "<version>", "commit": "<X>"}`.

## What makes the build reproducible

`scripts/buildrelease -src <clone> -out <file> -version <N.N.N> -commit <X>` (also used by CI) builds `./cmd/mash` with
`GOTOOLCHAIN=go1.27.3 GOENV=off GOWORK=off GOFLAGS= CGO_ENABLED=0`, every other `GO*`/`CGO_*` variable removed, `-trimpath
-buildvcs=false -mod=readonly` and `-ldflags "-X …/internal/endpoint.Version=<N.N.N> -X main.commit=<X>"`. It refuses a
source tree whose HEAD is not X, that holds any untracked or ignored file, or that contains the output, and runs
`go mod download` + `go mod verify` first, so the module cache is what `go.sum` names.

## Versions

`mash version` (and the SAP service version) is, in order: the release ldflag; the module version of a clean
`go install github.com/randybias/mash/cmd/mash@vN.N.N`; else `0.0.0-dev` (a development build; SAP requires SemVer).

## Not covered

Public publication is manual and follows the gate. Third-party GitHub Actions: none are used; the first-party
`actions/*` are referenced by major tag (as in Rangehand), not by commit SHA.
