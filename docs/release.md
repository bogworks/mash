# Releasing mash

A release is a **signed tag `vN.N.N` at a candidate commit X on main**. X is built from fresh clones in different
directories with a pinned toolchain; the build is byte-reproducible and `mash version` prints
`mash <N.N.N> (commit X)`. The maintainer tags; CI builds and drafts; the maintainer publishes after the gate.

## Publishing: one command

From the maintainer's machine (it signs the tag with the release SSH key), with `gh` authenticated for the release repository:

    scripts/release-check publish [--wait 45m] <version> <X>

It runs the pre-tag gate, the signed tag and push, waits for `release.yml`'s draft, runs the `--assets` gate, publishes
the draft, and verifies the published release. It finds its state from the world and resumes there, so after a
failure or an interruption you rerun the same command: no tag means the gate and the tag; a tag at X and no release
means waiting for the draft (`--wait`, default 45 minutes; it stops early if the tag's `release.yml` run failed); a
draft means the pre-tag gate (again, for a tag pushed by hand; not twice in one run), the `--assets` gate and publishing; a published release is only verified, and nothing is created. A tag at
a commit other than X is refused and nothing is changed. The pre-tag gate also refuses a version that is not strictly
above the latest published release, and the same ordering checks run again immediately before publishing (another version may have been published during the asset gate; a violation is refused, exit 3). The tag is signed with `git -c gpg.format=ssh -c user.signingkey=$RELEASE_SIGNING_KEY` (an SSH public key path, default
`~/.ssh/id_ed25519.pub`; a missing file fails before any tag is made), independent of the machine's git signing
config. Before pushing, the local tag is verified against `$RELEASE_SIGNERS` (default `docs/allowed_signers`); a tag that fails is refused (exit 3), never pushed, and deleted if this run
created it. Exit 0 published and verified, 2 usage, 3 refused, 4 the command failed or timed
out (rerun to resume). The reasoning is in [decision 0001](decisions/0001-publishing-is-one-resumable-command.md).
Then do step 7 (record the version and commit, optional).

## Signing

The `--assets` gate signs `SHA256SUMS`, and `publish` always runs it. There is no flag to skip it. The signature is made
last: the build provenance attestations, the SBOMs and the attached build provenance are verified first, and if any of them fails
nothing is signed or uploaded (a draft that already carries a `SHA256SUMS.sig` still has them verified). Then it checks
every line of `SHA256SUMS`. Each `mash-<os>-<arch>` line must equal a fresh, reproducible build of X made on the maintainer's
machine with the release recipe, and the `quickstart-synadia.sh` line must equal the script at X, so the signature covers the script too. A malformed line, a duplicate, a name that is not attached, or an attached `mash-*`
asset that is not listed is refused. Then it signs with `ssh-keygen -Y sign -n mash-release` and uploads
`SHA256SUMS.sig` to the draft. It verifies the signature against `ci/release.allowed_signers` as it stands at X, before
the upload and again on the draft read back. The key is the maintainer's release key, the one that signs the tag, named the one way the tag's is: `RELEASE_SIGNING_KEY`
(default `~/.ssh/id_ed25519.pub`, signing through ssh-agent; a private key file also works). It is looked up only
when the draft has no signature yet. `RELEASE_SIGNERS` is for the tag only: the sums are checked against the committed
`ci/release.allowed_signers`. A draft that already
has a `SHA256SUMS.sig` that verifies keeps it, and the rerun needs no key. One that does not verify is refused, never
replaced. Publishing is refused unless the draft carries a verifying `SHA256SUMS.sig`, and a published release is never
amended. Anyone can verify a download:

    ssh-keygen -Y verify -f ci/release.allowed_signers -I release -n mash-release -s SHA256SUMS.sig < SHA256SUMS

The reasoning is in [decision 0042](decisions/0042-sign-sha256sums-with-sshsig.md).

## Procedure by state

The numbered steps are what the command does in each state, and the manual path when it cannot run.

1. **Candidate.** X is a commit on `main` of the release repository with CI green: `test (linux)`,
   `test (macos)` and `reproducible` (all from GitHub Actions, run on the maintainers' self-hosted runners) on X itself.
2. **Prepare the release commit** (a pull request like any other, before the gate). Release preparation is where the
   shared files are written ([ADR 0068](decisions/0068-a-change-log-entry-is-a-fragment-file-and-the-adr-index-is-generated.md)):
   ```sh
   go run ./scripts/changelog assemble --version X.Y.Z --date YYYY-MM-DD   # fragments and Unreleased into CHANGELOG.md
   go run ./scripts/adrlint index --write                                    # docs/decisions/INDEX.md
   ```
   and set `plugin/.claude-plugin/plugin.json`'s `version` to `X.Y.Z`. `assemble --dry-run` prints the section first.
   Merge it and take its merge commit as X.
3. **Gate, before tagging:** `scripts/release-check <X> <version>` (for example `… 3f2a… 0.3.0`). It fetches main from
   the release repository (never a configured origin), checks X is an ancestor, reads X's check runs through `gh`
   (every page, latest run per name, from `github-actions` only), builds X twice in fresh clones, and runs the binary to
   compare `mash <version> (commit X)` exactly. It prints the version and commit in a neutral form, `version: <version> commit: <X>`, that any consumer can copy. Exit 0 passed, 3 refused,
   4 could not decide (gh, the network), 2 usage.
   The release repository (owner/name) is `$RELEASE_CHECK_REPO` if set, else taken from the checkout's `origin` when that
   is a github.com URL (https, `git@github.com:` or `ssh://git@github.com/`), else `bogworks/mash`. Main and the tag
   are always read from `https://github.com/<owner/name>.git`, never from origin's own URL.
   It also checks that mail reaches a fresh session no slower than in the previous release (v0.2.6 held it ~45 s
   where v0.2.5 took ~0.1 s). It runs `mash serve` as the plugin's `.mcp.json` launches it (args and env), as a bare MCP
   client against an embedded NATS server, with the candidate's and the previous release's binaries, which it builds and
   downloads once for this and the plugin check. Unkinded mail to a fresh session must arrive within twice the previous
   release's time (never less than a second more), `--urgent` mail within 10 s, and `--kind fyi` mail must not wake a
   session whose hook has run (5 s). The previous release's own violations are notes. The baseline is the previous release as shipped, its binary under its own plugin's `.mcp.json` args and env. The candidate is also measured launched as the previous release's plugin launches serve (its `.mcp.json` args and env at its tag), judged as strictly as the candidate (an older plugin without `MASH_SPOOL_FROM_START` must not make mail late); that is skipped with a note when there is no previous release or its `.mcp.json` cannot be read. It adds about the previous
   release's first-mail time (up to ~45 s) to the gate, since the two are measured at once.
   It also pins the render contract: `internal/render/testdata/contract.txt` (what `render.One` and `render.Line` print
   for a fixed set of messages, which a downstream parser, such as a downstream consumer's, may depend on) must equal its
   content at the previous release, or some commit since then must have a subject starting with `render-contract:`
   (that subject lands in the release notes). Changing the file (`go test ./internal/render -run TestRenderContract
   -update`) therefore takes a `render-contract:` commit describing the change, and any downstream parser updating its fixtures.
   It runs the plugin (`hooks.json` and `.mcp.json`, with its env block) against the candidate's binary and the previous
   release's, and the previous release's plugin (read at its tag) against the candidate's binary too, since Claude Code
   updates an installed plugin only on a version change and users run an older plugin with a newer mash; a previous
   plugin that cannot be run (no `hooks.json`, an unreadable file) is a note, not a refusal.
   Before tagging v`X.Y.Z`, `plugin/.claude-plugin/plugin.json`'s `version` must be `X.Y.Z`: release-check refuses
   otherwise, because Claude Code updates an installed plugin only on a version change.
   It refuses a candidate that still has a file under `changelog.d/` other than `README.md` ("changelog fragments were
   not assembled"), and one whose `docs/decisions/INDEX.md` differs from what `adrlint index` generates from the
   records at X ("is stale"); both are read from git at X, not the working tree, and a candidate without
   `docs/decisions/` has no index to check.
   It also refuses while any earlier `vN.N.N` tag has a release that is a draft or missing, unless `docs/skipped-releases.txt` at X lists that tag with a reason (a bare tag line without one does not count; publish it with `gh release edit <tag> --draft=false`); pre-release tags are ignored.
4. **Tag** (state: no tag): `git tag -s v<version> X && git push origin v<version>` (annotated and signed; the maintainer's key).
5. **CI** (state: tag at X, no release): `release.yml` guards the tag (name `vN.N.N`, annotated, signature present, X on main), builds the four
   targets (`mash-{linux,darwin}-{amd64,arm64}`) twice each in fresh clones and fails on any mismatch, and an `attest` job
   attests build provenance for the four binaries (see "Build provenance"). From 0.3.2 the workflow also generates a
   CycloneDX SBOM per binary and attests it, and keeps the Sigstore bundle of its build provenance attestation as `mash.intoto.jsonl` (see "SBOM and
   build provenance"). Only after all of these succeed does it create a **draft** release with the four binaries, the four
   `mash-<os>-<arch>.cdx.json` SBOMs, `mash.intoto.jsonl`, `quickstart-synadia.sh` (the tagged `scripts/quickstart-synadia.sh`),
   `SHA256SUMS` (which lists the binaries, the SBOMs, `mash.intoto.jsonl` and the script) and a changelog.
   A failed attestation leaves no draft; fix the cause and rerun the workflow.
6. **Gate, before publishing** (states: draft, then published, where it only verifies): `scripts/release-check --assets <version> <X>`: the remote tag exists, is annotated,
   verifies against `docs/allowed_signers` (the release key; never this machine's own git config), peels to X and is still that object after the assets were read; the release
   carries exactly the four binaries, the four `mash-<os>-<arch>.cdx.json` SBOMs, `mash.intoto.jsonl`, `quickstart-synadia.sh` and `SHA256SUMS`: each binary equal to a fresh build of X made here, the script byte-equal to `scripts/quickstart-synadia.sh` at X, and `SHA256SUMS` listing the binaries, the SBOMs, `mash.intoto.jsonl` and the script; and, for each of the four binaries, `gh attestation verify <binary> -R bogworks/mash --signer-workflow bogworks/mash/.github/workflows/release.yml --source-ref refs/tags/v<version> --deny-self-hosted-runners` (build provenance) and the same with `--predicate-type https://cyclonedx.org/bom` (SBOM; the attached `<binary>.cdx.json` must equal the attested predicate) pass on the files downloaded from the release, and the same with `--bundle mash.intoto.jsonl` passes on each binary (and `mash.intoto.jsonl` must name `.github/workflows/release.yml` as its workflow entry point) (a missing or failing check is refused, exit 3, naming the file; there is no skip flag; this covers an older or hand-made draft that the workflow never saw, and runs only after the tag, asset and build checks passed); only when all of these pass is `SHA256SUMS.sig` made, verified and uploaded (see Signing), so a draft that fails any of them is never signed. Then publish the draft: `gh release edit v<version> --draft=false`.
   Anyone can check a tag the same way: `git -c gpg.ssh.allowedSignersFile=docs/allowed_signers verify-tag v<version>`.
7. **Record the version and commit (optional):** anyone who tracks mash releases can copy the `version: <version> commit: <X>` line the gate printed into their own records; mash prescribes no format.

## Optional: check a downstream consumer

This step is for mash's maintainers who have a checkout of a downstream consumer of mash (a project that runs its own
live checks of mash). It is not part of the normal gate, and mash knows nothing about any particular consumer: the
consumer's checkout says how it is run.

`scripts/release-check --consumer <consumer checkout> <X> <version>` is the gate above plus one last step, "the
consumer's check passes against the candidate". It is **opt-in**: without the flag nothing changes, and the step can take
30+ minutes (a 2 hour limit) if the consumer starts a container and real agent sessions. The flag must be the first
argument and does not combine with `--assets`.

**The manifest.** The checkout must carry a committed file `.mash-consumer-check` in its root. Without it (or if it is
invalid, or its command is not a file) the step is exit 4, could not decide, and nothing is run. It is read from the
clone below, so an uncommitted manifest is never used. Lines are `key=value`; blank lines and `#` comments are ignored and
a value may be wrapped in single or double quotes:

| key | meaning |
| --- | --- |
| `command` | the script, a path inside the checkout, run as `sh <command> <commit> <version>` |
| `bin_env` | the variable that receives the absolute path of the binary under test |
| `plugin_env` | the variable that receives mash's plugin directory (`plugin/` of a fresh clone of X) |
| `set_env` | the variable that receives the value of `set` |
| `set` | the test set to run, the part of the consumer's tests that exercise mash |
| `results_env` | the variable that receives the path the script writes its JSONL results to |
| `marketplace_env` | optional: the variable that receives the root of that clone of X |
| `drop_env` | optional: comma-separated variables removed from the inherited environment (those the script rejects together with `set_env`) |
| `parts` | comma-separated parts of the set that must each appear in the results |

All keys but `marketplace_env` and `drop_env` are required. For example:

    command=ci/live/run.sh
    bin_env=MASH_CONSUMER_BIN
    plugin_env=MASH_CONSUMER_PLUGIN
    set_env=MASH_CONSUMER_SET
    set=mash
    results_env=MASH_CONSUMER_RESULTS
    marketplace_env=MASH_CONSUMER_MARKETPLACE
    drop_env=MASH_CONSUMER_ONLY
    parts=PartA,PartB

**The run.** mash runs the manifest's command from a fresh clone of the checkout at its HEAD commit (uncommitted changes
in the checkout never reach the run; the clone is removed afterwards), with the commit and, as the version label, the
highest `vMAJOR.MINOR.PATCH` tag reachable from the checkout (compared numerically, so v1.10.0 is above v1.9.0;
pre-release tags are ignored) as its two arguments. The candidate is handed over by the environment variables the manifest
names: the binary (X built for linux on the host's architecture; a linux host reuses the build of the plugin step), the
plugin directory and, if named, the marketplace root (a fresh clone of X, never the checkout running the gate), the set,
and the results path. The script's output streams to the terminal. No receipt variable is ever set.

**The results and exit codes.** The script writes one JSON object per line: `id`, `part`, `result` (`start`, `pass`, or
anything else for a failure) and `candidate_mash`. Exit 0 passes only if the results file exists, every part in it passed,
every line's `candidate_mash` equals the exact `bin_env` value mash passed, and every part in the manifest's `parts` appears
(a 0 with no evidence is a refusal). Exit 3 is a refusal naming the failing parts, only when a failed part can be read from
the results (a script also exits 3 for setup failures such as missing credentials, which is could not decide). Anything else
(exit 2; no consumer release tag; no or an invalid manifest; a failure to start; the limit) is exit 4, could not decide.
On the limit the script's shell, which runs in its own process group, gets TERM (a well-behaved script forwards it and
cleans up its containers); after a 10 minute grace the whole group is killed.

The results (`results.jsonl`, part logs in `results.jsonl.logs/` if the script writes them) are in a
`release-check-consumer-*` directory under the temp directory whose path is printed whatever the outcome; it is kept.
This never makes a consumer receipt: only the downstream consumer's own pin certifies, and a candidate run keeps results alone.

## Before 0.3.0 and later minors: the live stranger tier

Before tagging a minor release (0.3.0 and after), run the live tier of the "setups unlike ours" matrix against the
installed Claude Code. It is opt-in and spends a few short haiku turns (at most 8 turns and 5 minutes per scenario):

    MASH_LIVE_CLAUDE=1 MASH_LIVE_CLAUDE_CREDS=<path to a Claude Code .credentials.json> \
      MASH_LIVE_RECEIPT_DIR=docs/evidence/<date>-stranger-matrix-v<version> \
      go test ./cmd/mash -run LiveStranger -count=1 -v -timeout 40m

`MASH_LIVE_CLAUDE_CREDS` is copied into each scenario's isolated config (mode 0600) and deleted afterwards; no key is ever
put in the child's environment. Without it, or without `MASH_LIVE_CLAUDE=1`, the tests skip. The scenarios are L1 (plain
claude), L3 (minimal environment) and L4 (hooks disabled; it skips until the DEGRADED status line exists); L2 needs an
organisation account and always reports "not run: no organisation account". The receipt directory (a fresh temp dir if
`MASH_LIVE_RECEIPT_DIR` is unset; the path is logged) gets `timeline.log` and `RECEIPT.md` only, both scrubbed; never commit
raw streams or transcripts. Fill in the result line and the manual add-ons (VS Code, desktop app, `/clear` and `--resume`
session ids) in `RECEIPT.md`, and commit the directory. The run fails if the installed Claude Code's version, resolved
path or sha256 changes during it.

Alternatively, set `MASH_LIVE_CLAUDE_CONFIG_DIR` to an existing directory that already holds a logged-in Claude Code
config (a dedicated seat) instead of `MASH_LIVE_CLAUDE_CREDS`. Then nothing is copied: every scenario, L3 included, runs
with `CLAUDE_CONFIG_DIR` set to that directory, the plugin is installed into it at user scope, and each scenario's cleanup
runs `claude plugin uninstall mash@mash` and `claude plugin marketplace remove mash` (failures are logged, not fatal). The
directory's path is scrubbed from the receipt. It must have a single consumer for the whole run, and the tests must not
run with `-parallel`.

## Pinning the plugin after a release

The launcher `plugin/scripts/mash` downloads a binary only when `plugin/scripts/mash.pins` names a version and the
sha256 of each platform binary. After the release is published:

1. Download the release's `SHA256SUMS` and `SHA256SUMS.sig` (the armored SSHSIG made with
   `ssh-keygen -Y sign -f <key> -n mash-release SHA256SUMS`).
2. Run `plugin/scripts/pin-plugin <version> <SHA256SUMS> --sig <SHA256SUMS.sig>`. It verifies the signature
   (`ssh-keygen -Y verify`, principal `release`, namespace `mash-release`) against `ci/release.allowed_signers` (override with
   `--signers`), validates the four assets and 64-hex sums, and writes `plugin/scripts/mash.pins` and also bumps `plugin/.claude-plugin/plugin.json`'s `version` to `<version>-pin.1`
   (nothing else in the manifest changes), so installed plugins update and receive the pin (Claude Code updates a plugin only when
   its version changes, ADR 0033). The next release's gate requires plugin.json to be the new tag's version again, which the
   normal pre-tag edit sets. It is idempotent.
   After attestation (#158) is available, use `plugin/scripts/pin-plugin <version> --attested <dir of the four binaries>
   [--repo owner/name]` instead: it runs `gh attestation verify` on each binary and pins the sums it computes itself.
3. Commit `plugin/scripts/mash.pins` and `plugin/.claude-plugin/plugin.json` to main through a PR.

pin-plugin fails closed: with no verification mode it exits 2, and a missing or bad signature, a signers file with no key
(`ci/release.allowed_signers`, added with the signing work in #156) or with `sk-*` hardware-token keys, a failed
attestation, a missing binary, or a manifest without a single `"version"` line exits 1 and writes nothing (neither file). `--unverified` exists for local testing only; it warns loudly
and must never be used for a release.

The plugin download (#135) must not ship in a release until signed `SHA256SUMS` (#156) or attestation (#158) is
available: pins taken from an unauthenticated `SHA256SUMS` give integrity only, not authenticity.

The pins live outside the embedded files on purpose. `plugin/scripts/mash` is embedded in the mash binary
(`plugin/embed.go`), so pins written into it would change the binary and with it the checksums being pinned: a cycle.
`mash.pins` is not embedded, so the plugin exported by `mash plugin export` is unpinned and uses `mash` from PATH.

## What makes the build reproducible

`scripts/buildrelease -src <clone> -out <file> -version <N.N.N> -commit <X>` (also used by CI) builds `./cmd/mash` with
`GOTOOLCHAIN=go1.27.3 GOENV=off GOWORK=off GOFLAGS= CGO_ENABLED=0`, every other `GO*`/`CGO_*` variable removed, `-trimpath
-buildvcs=false -mod=readonly` and `-ldflags "-X …/internal/endpoint.Version=<N.N.N> -X main.commit=<X>"`. It refuses a
source tree whose HEAD is not X, that holds any untracked or ignored file, or that contains the output, and runs
`go mod download` + `go mod verify` first, so the module cache is what `go.sum` names.

## Build provenance

The `attest` job attests each `dist/mash-*` binary with `actions/attest-build-provenance` (pinned to a commit; the job,
and only that job, holds `id-token: write` and `attestations: write`). Consumers verify a downloaded binary with:

    gh attestation verify mash-linux-amd64 -R bogworks/mash --signer-workflow bogworks/mash/.github/workflows/release.yml \
      --source-ref refs/tags/v<version> --deny-self-hosted-runners

Attestations require a public repository (a private one needs GitHub Enterprise Cloud); this repository is public, so the
attestation is recorded in the public Sigstore transparency log. The `draft` job needs `attest`, so a draft exists only once provenance does: publishing can never precede
or outlive a failed attestation, and a failed attestation leaves no draft (rerun the workflow).

## SBOM and build provenance

From 0.3.2 each release adds, beside build provenance, a CycloneDX 1.5 SBOM per binary and the provenance bundle. The SBOM
(`mash-<os>-<arch>.cdx.json`) is generated from the built binary by `cyclonedx-gomod bin`, which reads the module build
information embedded in the binary, so it lists what is inside the artifact. It is attached, listed in the signed
`SHA256SUMS`, and attested with `actions/attest-sbom` (predicate type `https://cyclonedx.org/bom`). The provenance
(`mash.intoto.jsonl`) is the Sigstore bundle of the one `actions/attest-build-provenance` attestation the `attest` job
makes in-job, on the self-hosted runner, with the four binaries as its subjects; the job uploads it as the `provenance`
artifact and the draft job attaches it. No reusable workflow of another repository runs: a test fails on any job-level
`uses:` outside this repository, because such a job would run on a runner this repository does not pick
([ADR 0079](decisions/0079-ci-and-release-jobs-run-on-self-hosted-runners-only.md)). mash meets SLSA v1.0 Build L2, not L3: the
attestation is made by a step inside mash's own workflow, with the signer identity of `release.yml`. The `attest` job holds
`id-token: write`, `attestations: write` and `contents: read`, and no other job but `attest-sbom` holds the signing
permissions. The release assets from 0.3.2 are the four binaries, the four `.cdx.json` files, `mash.intoto.jsonl`,
`quickstart-synadia.sh`, `SHA256SUMS` and `SHA256SUMS.sig`. The reasoning, and what is not yet true, is in
[decision 0051](decisions/0051-releases-carry-an-sbom-per-binary-and-slsa-provenance.md) (whose isolated generator ADR 0079 replaces
with the in-job attestation).

## Dry run

The SBOM and attestation jobs only run for a real release, so a change to the pipeline can be tried without tagging one:

    gh workflow run release.yml --ref main
    gh run watch

A `workflow_dispatch` run on `main` builds the four binaries twice and compares them, makes the SBOMs, attests the binaries and
the SBOMs, and keeps the provenance bundle, with version `0.0.0-dryrun` at the dispatched HEAD. The `dry-run-verify` job then runs
a check that the provenance's workflow entry point is `release.yml`, `gh attestation verify` (with
`--signer-workflow`, `--source-ref refs/heads/main` and `--deny-self-hosted-runners`) for the attached bundle (`--bundle mash.intoto.jsonl`), for build provenance and for the CycloneDX SBOM, and checks that the attested SBOM predicate equals the `.cdx.json`, as
`release-check --assets` does. It creates no release and no tag and uploads nothing to a release. A dispatch from any other
ref builds nothing.

A dry run is only meaningful if the verify job ran: check that `dry-run-verify` shows success, not skipped. A skipped job anywhere
in a job's `needs` chain skips everything below it unless the job has its own status-function `if`, which is why each job below
`build` has one; a green run in which `dry-run-verify` was skipped verified nothing.

It does not exercise the tag guard (tag shape, signature, on main, older drafts), the maintainer's signing, the draft job or
publishing, and it does not run `release-check`. It must be green on `main` before a release that changes the pipeline is tagged.

## Verify a release

These commands apply to releases from 0.3.2 on. 0.3.1 and older have no SBOM and no `mash.intoto.jsonl`, and 0.3.1 is the
first release with build provenance. Download the assets of `v<version>` into one directory (for example
`gh release download v<version> -R bogworks/mash`) and run the commands from there. Each proves something different;
none of them replaces the others.

**1. The signed checksums.** From a checkout of mash at the tag (for `ci/release.allowed_signers`):

    ssh-keygen -Y verify -f ci/release.allowed_signers -I release -n mash-release -s SHA256SUMS.sig < SHA256SUMS
    sha256sum -c --ignore-missing SHA256SUMS

(on macOS use `shasum -a 256 -c --ignore-missing SHA256SUMS`, or compare by hand). This proves the maintainer's release key
signed these sums, and that each downloaded file matches them. Before signing, the maintainer rebuilt each binary from the
tagged commit and compared it byte for byte. It does not show who built the file or what is in it.

**2. Build provenance.**

    gh attestation verify mash-linux-amd64 -R bogworks/mash --signer-workflow bogworks/mash/.github/workflows/release.yml \
      --source-ref refs/tags/v<version> --deny-self-hosted-runners

This proves the release workflow (`release.yml`) of the `bogworks/mash` repository, run at the tag `v<version>` on a
runner of the repository, built and attested these exact bytes, recorded in Sigstore. `-R` alone would accept any workflow of
the repository on any ref, so the signer workflow, the source ref and the runner flag are what make the check mean
something. The release jobs now run on self-hosted runners ([ADR 0079](decisions/0079-ci-and-release-jobs-run-on-self-hosted-runners-only.md)),
which `--deny-self-hosted-runners` refuses: the flag stays until the maintainer decides what replaces it, and until then the
verification refuses attestations produced on these runners. It does not prove the workflow's steps were not tampered with: that attestation is made by a step inside that
workflow (SLSA v1.0 Build Level 2).

**3. SBOM.**

    gh attestation verify mash-linux-amd64 -R bogworks/mash --signer-workflow bogworks/mash/.github/workflows/release.yml \
      --source-ref refs/tags/v<version> --deny-self-hosted-runners --predicate-type https://cyclonedx.org/bom
    jq '.components | length' mash-linux-amd64.cdx.json

The first command proves the SBOM was attested for these exact bytes by the same release workflow at the same tag. The second counts the
modules listed. Any scanner that reads CycloneDX can take the file as input, for example `grype sbom:mash-linux-amd64.cdx.json`
or `osv-scanner --sbom mash-linux-amd64.cdx.json`. The SBOM lists Go modules as recorded in the binary; it is generated
and attested by mash's own workflow (Build L2-grade), and it says nothing about vulnerabilities by itself.

**4. The attached provenance.** Verify each binary against the one provenance file, offline:

    gh attestation verify mash-linux-amd64 -R bogworks/mash --signer-workflow bogworks/mash/.github/workflows/release.yml \
      --source-ref refs/tags/v<version> --deny-self-hosted-runners --bundle mash.intoto.jsonl

This proves `mash.intoto.jsonl` is the Sigstore bundle of an attestation by the release workflow at that tag, with this binary
among its subjects. Also check that the provenance names `release.yml` as its workflow entry point:

    jq -r '.dsseEnvelope.payload // .payload' mash.intoto.jsonl | base64 -d | jq '.predicate.buildDefinition.externalParameters.workflow.path'

The level met is SLSA v1.0 **Build L2**, not L3. The attestation is made by a step in mash's own workflow, so a compromised
build step would still get a genuine signature. The control beyond L2 is the two-clone reproducibility check in CI plus the
maintainer's independent rebuild before signing `SHA256SUMS`. The trust roots are GitHub's OIDC and Sigstore and the
maintainer's key. The release provenance now comes from a self-hosted runner, so `--deny-self-hosted-runners` refuses it too, like
the attestations of step 2: the flag stays until the maintainer rules on the attestation question (ADR 0079).

## Versions

`mash version` (and the SAP service version) is, in order: the release ldflag; the module version of a clean
`go install github.com/bogworks/mash/cmd/mash@vN.N.N`; else `0.0.0-dev` (a development build; SAP requires SemVer).

## Not covered

Public publication is `release-check publish` (or its steps by hand) and follows the gate. Every `uses:` of an action in the workflows is pinned to a full commit SHA (a test enforces it), and no job calls a reusable workflow of another repository (a test enforces that too). See decisions 0051 and 0079.

## Daily publish check

`.github/workflows/publish-check.yml` runs every day and on demand. It audits the repository with its own `github.token`
(no secret) through `scripts/publishcheck`: every `v*` tag has a release (an rc tag with none is exempt), published
non-rc releases went out in version order, and no draft is older than 2 hours. A violation opens or updates an issue and
ends the run red. Fix it by publishing the release or deleting the stale draft.

## New repository setup

A checklist so a new repository for this project comes up complete in one pass. Do it before the first pull request,
and tick it off in the repository's setup issue.

**GitHub settings** (Settings of the repository):

- [ ] Private vulnerability reporting: on (it is the reporting route in [SECURITY.md](../SECURITY.md)).
- [ ] Dependabot alerts and Dependabot security updates: on.
- [ ] Secret scanning with push protection: on.
- [ ] CodeQL: code scanning runs from the repository's own workflow (`.github/workflows/codeql.yml`), so the **default
      setup must be off**: GitHub refuses to process both ("CodeQL analyses from advanced configurations cannot be
      processed when the default setup is enabled").
- [ ] Actions: require approval for fork pull requests from all outside collaborators; the default workflow token is
      read-only.
- [ ] Ruleset for `main`, as specified in [branch-protection.md](branch-protection.md) (follow that file; the points
      that are easy to miss): require a pull request, with the Mergify app as the bypass actor for its tested
      fast-forward merges; require the status checks `test (linux)`, `test (macos)`, `reproducible`,
      `lint`, `govulncheck`, `codeql` and `Mergify Merge Protections` (Merge Protections is only enforced once the
      ruleset requires that check), plus code scanning results; block deletion and force pushes; linear history.
      Do **not** require signed commits: contributors' and automation's commits are not all signed. (Releases are
      signed with the key in `docs/allowed_signers`, see "Signing".)
- [ ] Repository variable `MASH_FORBIDDEN_WORDS`: the words internal to the maintainers that must not appear in the
      tree; set by the maintainers (comma-separated, whole words, case-insensitive). `adr-lint` and the standalone
      tests in `cmd/mash` read it, so the guard needs no names in the tree.
- [ ] Ruleset for `v*` tags: no deletion, no update.
- [ ] Immutable releases: switch on after the first release is published.

**Merge queue (Mergify):**

- [ ] Install the Mergify app on the repository.
- [ ] In its dashboard enable **both** Merge Queue and Merge Protections: `auto_merge_conditions` in `.mergify.yml`
      need Merge Protections.

**Labels:** `critical`, `pipeline`, `docs`, `hold`, `queued`, `dequeued`, `safe-to-review`, `security-ok`.

**Maintainer automation** (kept outside this repository; add the repository to each):

- [ ] The review watcher's checkout of the repository.
- [ ] The pull-request notifier's instance.
- [ ] The repository list of the velocity metrics.

**CI:**

- [ ] Self-hosted runners only ([ADR 0079](decisions/0079-ci-and-release-jobs-run-on-self-hosted-runners-only.md)): every
      job runs on `[self-hosted, rh-linux]` or `[self-hosted, rh-macos]`, with no GitHub-hosted fallback. Register runners
      with those labels only on machines the maintainers control. Pull requests from forks run on them too, so keep the
      Actions setting that holds an outside contributor's run for a person's approval (the item above).
