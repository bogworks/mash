# The in-session review loop

[ADR 0080](../decisions/0080-review-is-a-gate-in-the-implementers-loop-adopted-from-a-sibling-project.md): an author
reviews their own diff before pushing, so findings come back into the same session in minutes instead of through a pull
request comment hours later. The process is adopted from ADR 0047 of the sibling project at `randybias/rangehand`
(`docs/decisions/0047-review-is-a-gate-in-the-implementers-loop.md`), whose gate this one is a port of; this page says what
differs for mash.

Needs on the host: `git`, `go`, `jq`, `flock` and [`ocr`](https://github.com/alibaba/open-code-review) (the review
engine), signed in to whichever provider the models use. `codex` and `claude` are needed only by the full merge gate
(`scripts/gate <ref>` with no mode flag), never by `--review`.

```sh
bash scripts/gate <branch-or-sha> --review            # one OCR run per model in policy.json
bash scripts/gate <branch-or-sha> --review --model openrouter:deepseek/deepseek-v4-flash --model openrouter:z-ai/glm-5.3-flash
bash scripts/gate <branch-or-sha> --pre-pr            # the fast CI guards, before opening the pull request
```

What `--review` does, on a private worktree of `origin/main` merged with your ref:

1. **Conformance:** `go build`, `go vet` and gofmt on the changed files (skipped for a diff with no Go). A failure stops
   here and no model is spent.
2. **Coverage:** OCR leaves Markdown and test files out by default, so the gate first runs `ocr review --preview` to
   see how many of the changed files OCR will review. If it will review **none** (a docs-only or test-only diff), the
   gate prints `REVIEW SKIPPED`, exits 0, runs no model and writes **no record**, so the server-side review reviews
   that diff in full. A preview that fails or has no `Preview:` header is exit 2: unknown coverage is never treated as
   none or as all. Otherwise the pass prints `covered N of M files` and, when N < M, that the server-side review covers
   the other M-N files.
3. **OCR, once per model** (`ocr review --from <base> --to <ref> --format json`, in parallel). The models are the
   `--model` flags, else `models` in `policy.json`, else ocr's own default. Variance comes from several models.
4. **The verdict** (`scripts/reviewgate`): findings from all models are merged (same file, nearby lines, same category
   count once, with how many models agreed), and the gate is the strictest **tier** any changed file matches in
   [policy.json](policy.json), not the first one listed: `security-critical` blocks blocker, high and medium (round cap
   6); `standard` blocks blocker and high (cap 4); `tests-and-docs` blocks blocker (cap 2). A blocker always blocks.

## Tiers for mash

| Tier | Paths | Blocks | Round cap |
| --- | --- | --- | --- |
| `security-critical` | `internal/admin/**`, `internal/policy/**`, `internal/sap/**`, `cmd/mashctl/**`, `internal/release/**`, `scripts/buildrelease/**`, `scripts/release-check`, `scripts/build-twice`, `docs/allowed_signers`, `ci/release.allowed_signers`, `internal/natsauth/**`, `internal/mint/**`, `internal/store/**`, `.github/**`, `.mergify.yml` | blocker, high, medium | 6 |
| `tests-and-docs` | `**/*_test.go`, `**/testdata/**`, `docs/**`, `**/*.md` | blocker | 2 |
| `standard` | `cmd/**`, `internal/**`, `scripts/**`, `plugin/**`, `ci/**`, `go.mod`, `go.sum`, and any file no tier names | blocker, high | 4 |

A file can match several tiers (a test under `internal/sap/` matches all three): the strictest wins, and the verdict
header names the others. The policy file is read from the base ref, never from the branch under review, for the
standing questions; the gate script reads the checkout's copy for the tiers and the model list.

The record (in the store, named by the patch-id) carries `covered:` and `total:` from the preview, one `covered-file: <path>`
line for each file under the preview's `Will review (N):` (`reviewgate record -covered-files FILE`; the paths must be
repo-relative with no `..`, and `-covered` must equal their number), and its evidence:
each model run's OCR JSON is copied next to it as `<record>.evidence-<i>.json` and the record lists `evidence: <count>`
and, per file, `run-<i>: <sha256> <exact_range> <source_artifact_sha256>` (the last two from the OCR manifest, empty if
it has none). The evidence files are written before the record, which is the commit point: a reader that finds the
record finds its evidence.

A receipt does not waive the server-side `codex-review` run. It is checked against hashes held in the same writable
store, so a forged receipt passes those checks. Until a trusted attestation exists, `docs/review/policy.json` sets
`receipts_waive_review: false` (the default when absent) and `reviewgate verify` never reports verified: it runs every
check, then exits 1 saying the receipt checks out but does not waive review. The policy loader rejects `true` unless an
`attestation` object names a non-empty `verifier`. See ADR 0080, "Known weakness inherited from the source".

Exit codes: `0` pass, or skipped for zero coverage with no receipt (a receipt is recorded for the diff's patch-id; the server-side `codex-review` still reviews, because receipts do not waive review under this policy), `1` blocked (fix and rerun; a rerun of the same diff is the same round), `2` error (a model
run failed and fewer than `min_complete` finished, or no policy), `3` round cap reached with blocking findings left. At the cap fix only correctness on the supported path,
answer everything else as deferred with a follow-up issue each, and push; a blocker is never deferred, send it to the maintainer.

## How this relates to the server-side `codex-review`

`codex-review` is the commit status that `.mergify.yml` requires before a pull request is queued (with `pr-check` and
`adr-lint`). It is posted by the review watcher (`scripts/review-pr` in the orchestrator's repository, not this one),
which reviews each pull request head with Codex.

- **The gate produces a receipt, not a status.** A passing `--review` writes a record for the diff's patch-id into the
  store. It posts nothing to GitHub and is not a check. A diff that OCR covers none of, or that changes a
  `security-critical` path, is never waived.
- **No mash CI job verifies a receipt.** `ci.yml` has no reviewgate job, and neither does the source project's. CI runs
  `go vet ./...` and `go test -race ./...`, which build and test `scripts/reviewgate` and check this policy file
  (`TestRepoPolicy`) like any other package. `pr-check` and the queue conditions are unchanged.
- **Verification is the watcher's, and optional.** `reviewgate verify` is the function the watcher calls before it reviews:
  the record is found by the diff's patch-id, but a patch-id match is only the lookup. The watcher also passes the
  checkout it is verifying (`-base` and `-head`, the full commits of the range the gate reviewed, and `-source-sha`, the
  source artifact hash OCR reports for it), and every run's recorded `exact_range` must be exactly `base..head` and its
  source hash exactly that one, so a branch rebased onto a new base (the same patch, a new range) is not verified. The
  evidence files must match their recorded hashes, every run must be complete, and the verdict recomputed from the evidence under the base branch's policy must pass; the record's `total`
  must equal the number of files the diff changes. Even then `verify` exits 1 unless the base policy sets
  `receipts_waive_review` (with an attestation verifier), which the repository's policy does not: the output tells
  "receipt invalid" from "receipt checks out, but receipts do not waive review". Either way the watcher reviews the
  diff in full, as it does for any head with no receipt, so a mash pull request is never less reviewed than before.
- **The watcher must look where the gate writes.** The defaults now agree: with no environment set, the gate writes
  its records to the path the watcher reads (`${XDG_DATA_HOME:-$HOME/.local/share}/<the watcher's directory>/reviews/inloop-v1`,
  with a suffix when the base policy has standing questions; the gate and the watcher derive it the same way). The
  path is per-user, under the user's XDG data directory, so the gate and the watcher must run as the same user on the
  same host. Otherwise set `GATE_REVIEW_STORE` for the gate and `REVIEW_INLOOP_STORE` for the watcher to one shared
  path. Records are keyed by the repository slug (`bogworks_mash`), so sharing a store between repositories is safe.
- **Base branch, not the pull request.** The watcher reads `docs/review/policy.json` and builds `scripts/reviewgate`
  from the fetched `origin/<base branch>` (`git show`, `git archive`), never from the pull request's own tree, so a
  pull request cannot verify its receipt with its own policy or verifier. A base without either skips verification, so
  nothing is verified until this change is on `main`.
- **The tiers govern the push, not the status.** The tier table says when the author may stop and push. What
  `codex-review` posts, and when an open finding fails it, is the watcher's rule and is unchanged by this page.
- **An outside pull request never trusts a receipt,** and a contributor session is not a security boundary
  (ADR 0020 of the source project): the binding is the patch-id, the evidence hashes, the manifest range and the
  recomputed verdict, and a forgery is out of scope.

- **Partial coverage** (`covered < total`): the files OCR did not review are the changed files minus the recorded
  `covered-file` names. The receipt stands only when every one of them is documentation: a Markdown file (`**/*.md`) or
  anything under `docs/**`. OCR excludes tests and Markdown, but a test file is code that nobody reviewed, so only pure
  documentation may be left to the in-session review. Any other uncovered file, or a partial record that names no
  covered files, is not verified and the server reviews the diff itself (the reason lists the first three such files).
  Full coverage verifies with or without names.
- **The manifest range and source hash:** each run's `exact_range` (`A..B`, resolved shas, on the record's `run-<i>` line)
  must equal the checkout's `-base..-head` and its `source_artifact_sha256` must equal `-source-sha`, and both must agree
  with the manifest inside the evidence file. A run with no range or source hash, or another range or hash, is not
  verified (`run <i> reviewed a different range`, `run <i> reviewed a different source`).

A rebase keeps the patch-id but changes the base and head, so it needs a new review; the patch-id is only the key the
record is stored and looked up under, and the round counter's identity of a head.

## Models

`models` in [policy.json](policy.json) is the variance reviewer(s) chosen by the source project's bake-off: GLM 5.3 Flash
(`openrouter:z-ai/glm-5.3-flash`) found 4 of 17 known issues, codex plus Flash 6 of 17, for about $0.05 a pull request.
`fallback_models` (GLM 5.3) is the watcher's replacement when Codex is out; `gate --review` never runs it. Both lists
are validated by `reviewgate` (a non-empty provider and model each). An empty `models` runs OCR once with its own default.
These figures were measured on the source project's code, not mash's.

OCR runs through the provider's API and is metered, unlike the Codex subscription. The gate passes no token budget and no
timeout (`--max-tokens-budget 0 --timeout 0`), as the source gate does; the spend limit is a credit limit on the
provider key, which the operator sets. The spend caps, ledger and alarm that the source project's watcher puts around its
own OCR runs belong to the watcher and are not part of this repository.

## The findings log

After the verdict (exit 0, 1 or 3), `gate --review` appends one NDJSON line per OCR comment of every complete model run to
`${GATE_FINDINGS_LOG:-${XDG_DATA_HOME:-$HOME/.local/share}/mash/reviews/findings-v1/<owner_name>.ndjson}` (`/` in
the slug becomes `_`). Writing is best effort: a failure never changes the gate's result. Fields: `ts` (UTC ISO),
`slug`, `ref` (the short branch name or sha), `patchid`, `round`, `tier`, `model` (`provider:model` from the OCR JSON's
`llm`, else the requested model), `file`, `line` (the comment's `start_line`), `severity`, `category`, `title` (first
line of the comment, at most 200 characters), `confirmed` and `source` (`inloop`). `confirmed` is always `null` here:
a later re-score fills it in, by judging which findings were real.

State the gate keeps: round counters under `${XDG_STATE_HOME:-~/.local/state}/mash/review-loop` (`GATE_ROUNDS_DIR`), the
slot locks of the full gate under `.../mash/gate-slots` (`GATE_SLOT_DIR`, `GATE_SLOTS` at once, default 7), and the
receipt store and findings log above. Every one is relocatable by its variable.

## Docs and test files (the OCR gap)

OCR skips `*.md` and `*_test.go`, whatever the model. Decision: the server-side `codex-review` stays the reviewer for them. A docs-only or tests-only diff has no OCR coverage, so `gate --review` writes no receipt and the watcher reviews it with Codex as before. In a mixed diff the code is reviewed by OCR and the docs and test files by nobody in the loop; the tests-and-docs tier blocks only a blocker, so that residual risk is accepted.

## Differences from the source project's gate

Only what mash does not have was removed; the review mode and `reviewgate` are the source's, unchanged but for text and
the policy. In the full gate (no mode flag) the failpoints build tag, the scenario tier, the web reader and the
`--no-tier-b` flag are gone (mash has none of them), and the test runs take CI's `-timeout 20m`. In `--pre-pr` the
repository-lint, file-size and golangci-lint checks are replaced by what mash's CI runs: staticcheck (advisory, as in
`ci.yml`) and the ADR lint (`go run ./scripts/adrlint docs`). State and data directories use `mash` where the source uses
its own project's name. `scripts/gate_review_test.sh` runs the `--review` path against a stub `ocr` and throwaway
repositories; it needs no network and no model.
