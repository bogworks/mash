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
Then do step 6 (the pin).

## Signing

The `--assets` gate signs `SHA256SUMS`, and `publish` always runs it. There is no flag to skip it. First it checks
every line of `SHA256SUMS`. Each `mash-<os>-<arch>` line must equal a fresh, reproducible build of X made on the lead's
machine with the release recipe, and the `quickstart-synadia.sh` line must equal the script at X, so the signature covers the script too. A malformed line, a duplicate, a name that is not attached, or an attached `mash-*`
asset that is not listed is refused. Then it signs with `ssh-keygen -Y sign -n mash-release` and uploads
`SHA256SUMS.sig` to the draft. It verifies the signature against `ci/release.allowed_signers` as it stands at X, before
the upload and again on the draft read back. The key is the maintainer's release key, the one that signs the tag, named the one way the tag's is: `RELEASE_SIGNING_KEY`
(default `~/.ssh/id_ed25519_rdev.pub`, signing through ssh-agent; a private key file also works). It is looked up only
when the draft has no signature yet. `RELEASE_SIGNERS` is for the tag only: the sums are checked against the committed
`ci/release.allowed_signers`. A draft that already
has a `SHA256SUMS.sig` that verifies keeps it, and the rerun needs no key. One that does not verify is refused, never
replaced. Publishing is refused unless the draft carries a verifying `SHA256SUMS.sig`, and a published release is never
amended. Anyone can verify a download:

    ssh-keygen -Y verify -f ci/release.allowed_signers -I release -n mash-release -s SHA256SUMS.sig < SHA256SUMS

The reasoning is in [decision 0042](decisions/0042-sign-sha256sums-with-sshsig.md).

## Procedure by state

The numbered steps are what the command does in each state, and the manual path when it cannot run.

1. **Candidate.** X is a commit on `main` of the release repository with CI green: `test (ubuntu-latest)`,
   `test (macos-latest)` and `reproducible` (all from GitHub Actions) on X itself.
2. **Gate, before tagging:** `scripts/release-check <X> <version>` (for example `… 3f2a… 0.3.0`). It fetches main from
   the release repository (never a configured origin), checks X is an ancestor, reads X's check runs through `gh`
   (every page, latest run per name, from `github-actions` only), builds X twice in fresh clones, and runs the binary to
   compare `mash <version> (commit X)` exactly. It prints the pin for the orchestrator's `compat.json`. Exit 0 passed, 3 refused,
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
   for a fixed set of messages, which a downstream parser, such as an orchestrator's, may depend on) must equal its
   content at the previous release, or some commit since then must have a subject starting with `render-contract:`
   (that subject lands in the release notes). Changing the file (`go test ./internal/render -run TestRenderContract
   -update`) therefore takes a `render-contract:` commit describing the change, and any downstream parser updating its fixtures.
   It runs the plugin (`hooks.json` and `.mcp.json`, with its env block) against the candidate's binary and the previous
   release's, and the previous release's plugin (read at its tag) against the candidate's binary too, since Claude Code
   updates an installed plugin only on a version change and users run an older plugin with a newer mash; a previous
   plugin that cannot be run (no `hooks.json`, an unreadable file) is a note, not a refusal.
   Before tagging v`X.Y.Z`, `plugin/.claude-plugin/plugin.json`'s `version` must be `X.Y.Z`: release-check refuses
   otherwise, because Claude Code updates an installed plugin only on a version change.
   It also refuses while any earlier `vN.N.N` tag has a release that is a draft or missing, unless `docs/skipped-releases.txt` at X lists that tag with a reason (a bare tag line without one does not count; publish it with `gh release edit <tag> --draft=false`); pre-release tags are ignored.
3. **Tag** (state: no tag): `git tag -s v<version> X && git push origin v<version>` (annotated and signed; the maintainer's key).
4. **CI** (state: tag at X, no release): `release.yml` guards the tag (name `vN.N.N`, annotated, signature present, X on main), builds the four
   targets (`mash-{linux,darwin}-{amd64,arm64}`) twice each in fresh clones and fails on any mismatch, then creates a
   **draft** release with the binaries, `quickstart-synadia.sh` (the tagged `scripts/quickstart-synadia.sh`), `SHA256SUMS` and a changelog.
5. **Gate, before publishing** (states: draft, then published, where it only verifies): `scripts/release-check --assets <version> <X>`: the remote tag exists, is annotated,
   verifies against `docs/allowed_signers` (the release key; never this machine's own git config), peels to X and is still that object after the assets were read; the release
   carries exactly the four binaries, `quickstart-synadia.sh` and `SHA256SUMS`: each binary equal to a fresh build of X made here, the script byte-equal to `scripts/quickstart-synadia.sh` at X, and `SHA256SUMS` listing all five; and `SHA256SUMS.sig` is made, verified and uploaded (see Signing). Then publish the draft: `gh release edit v<version> --draft=false`.
   Anyone can check a tag the same way: `git -c gpg.ssh.allowedSignersFile=docs/allowed_signers verify-tag v<version>`.
6. **Pin:** in the orchestrator's `compat.json` set `mash` to `{"version": "<version>", "commit": "<X>"}`.

## Optional, maintainers only (needs access to the rangehand repo): rangehand's tier c against the candidate

This step is for mash's maintainers: it needs access to the private rangehand repository and is not part of the normal gate.

`scripts/release-check --rangehand <rangehand checkout> <X> <version>` is the gate above plus one last step, "rangehand's
tier c (mash set) passes against the candidate". It is **opt-in**: without the flag nothing changes, and the step takes
30+ minutes (a 2 hour limit) because it runs real Claude sessions in a container. The flag must be the first argument and
does not combine with `--assets`.

It runs rangehand's `test/scenarios/live/run-in-container.sh` (the parts that exercise mash: mail, the hook and channel
delivery, the crash and the multi-endpoint mesh, `RANGE_LIVE_SET=mash`) from a fresh clone of the checkout at its HEAD commit
(uncommitted changes in the checkout never reach the run; the clone is removed afterwards) and, as the version label, the highest
`vMAJOR.MINOR.PATCH` tag reachable from the checkout (compared numerically, so v1.10.0 is above v1.9.0; pre-release tags are ignored), with the candidate handed over by `RANGE_MASH_BIN` (X built for linux on the host's
architecture; a linux host reuses the build of the plugin step), `RANGE_MASH_PLUGIN` (`plugin/` of a fresh clone of X)
and `RANGE_LIVE_MASH_MARKETPLACE` (that clone), never the checkout running the gate. The runner's output streams to the
terminal. Exit 0 passes only if the results file exists and every part in it passed with every line's `candidate_mash` equal
to the exact `RANGE_MASH_BIN` the gate passed (a 0 with no evidence is a refusal); exit 3 is a refusal naming the failing parts only when a failed part can be read from the results (the runner also exits 3 for setup failures such as missing credentials or a docker build failure, which is could not decide); an inherited `RANGE_LIVE_ONLY` is dropped from the runner's environment, since rangehand rejects it with `RANGE_LIVE_SET`; anything else (exit 2:
docker missing or an invalid variable; no rangehand tag; a failure to start; the limit) is exit 4, could not decide.
On the limit the runner's shell, which runs in its own process group, gets TERM (it forwards it to the live run, which removes
its containers); after a 10 minute grace the whole group is killed.

Prerequisites: docker; a rangehand checkout at a commit that has 053c5ff (the candidate run); the host's Claude login,
which rangehand's container uses. The results (`results.jsonl`, part logs in `results.jsonl.logs/`) are in a
`release-check-rangehand-*` directory under the temp directory whose path is printed whatever the outcome; it is kept.
On a non-linux host, rangehand (as of 053c5ff) runs `mash version` on the host before starting the container, so the linux candidate cannot run there and the step reports "could not decide"; use a linux host until rangehand handles that.
This never makes a rangehand receipt: only the compat.json pin certifies, and a candidate run keeps results alone.

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

## Versions

`mash version` (and the SAP service version) is, in order: the release ldflag; the module version of a clean
`go install github.com/bogworks/mash/cmd/mash@vN.N.N`; else `0.0.0-dev` (a development build; SAP requires SemVer).

## Not covered

Public publication is `release-check publish` (or its steps by hand) and follows the gate. Third-party GitHub Actions: none are used; the first-party
`actions/*` are referenced by major tag (as in the orchestrator), not by commit SHA.

## Daily publish check

`.github/workflows/publish-check.yml` runs every day and on demand. It audits the repository with its own `github.token`
(no secret) through `scripts/publishcheck`: every `v*` tag has a release (an rc tag with none is exempt), published
non-rc releases went out in version order, and no draft is older than 2 hours. A violation opens or updates an issue and
ends the run red. Fix it by publishing the release or deleting the stale draft.
