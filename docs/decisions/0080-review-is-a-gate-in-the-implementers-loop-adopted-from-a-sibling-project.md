---
status: accepted
date: 2026-11-10
decision-makers: Randy Bias (mash maintainer); ruling relayed on 2026-11-10 by the session that coordinates his work
---

# Review is a gate in the implementer's loop, adopted from a sibling project

## Context and Problem Statement

A review round on a mash pull request waits for the pull request to be pushed, for CI, and for the server-side review
(`codex-review`, the status `.mergify.yml` requires) to post a verdict that the author's session never sees directly.
The maintainer's sibling project, `randybias/rangehand`, measured the same delay and removed it by moving the first <!-- adrlint:allow vocabulary -->
review into the author's own session, before the push: its [ADR 0047](https://github.com/randybias/rangehand/blob/main/docs/decisions/0047-review-is-a-gate-in-the-implementers-loop.md) <!-- adrlint:allow vocabulary -->
records that decision and the tooling that carries it. mash has no such gate. Should mash adopt that process, and in what form?

## Decision Drivers

* The maintainer ruled that mash adopts the sibling project's existing review process: adapt it, do not redesign it.
* The server-side `codex-review` status and the Mergify queue conditions stay as they are.
* Nothing in the mash product depends on the gate: it is repository tooling, so it carries no personal convention
  ([ADR 0045](0045-product-code-carries-no-personal-convention-a-ratchet-lint-enforces-it.md)) and every location is an environment variable.

## Considered Options

* Port the sibling project's gate, verdict engine and policy file, with mash's paths (chosen).
* Keep only the server-side review.
* Design a mash-specific loop.

## Decision Outcome

mash adopts the process of the sibling project's ADR 0047, whose decisions 1 to 4 and 7 apply as written there; this record
does not restate their reasoning.

1. **The author reviews their own diff before pushing.** After build, vet and gofmt pass, `bash scripts/gate <ref> --review`
   runs `ocr review` once per model in `docs/review/policy.json` over `merge-base..<ref>`, merges the runs, and
   decides. Findings return to the same session.
2. **Gates are per path, from one policy file.** `docs/review/policy.json` maps paths to a tier and the strictest tier a
   diff touches applies, regardless of the order the tiers are listed in. The tiers keep the source project's names, blocking
   severities and round caps. `docs/review/policy.json` is the source of truth; this table is its state when this record
   was accepted:

   | Tier | Paths | Blocks the push | Round cap |
   | --- | --- | --- | --- |
   | `security-critical` | `internal/admin/**`, `internal/policy/**`, `internal/sap/**`, `internal/store/**`, `internal/natsauth/**`, `internal/mint/**`, `cmd/mashctl/**`, `internal/release/**`, `scripts/buildrelease/**`, `scripts/release-check`, `scripts/build-twice`, `docs/allowed_signers`, `ci/release.allowed_signers`, `.github/**`, `.mergify.yml` | blocker, high, medium | 6 |
   | `standard` | `cmd/**`, `internal/**`, `scripts/**`, `plugin/**`, `ci/**`, `go.mod`, `go.sum`, and any file no tier names | blocker, high | 4 |
   | `tests-and-docs` | `**/*_test.go`, `**/testdata/**`, `docs/**`, `**/*.md` | blocker | 2 |

3. **The loop ends** when the gate passes, or at the round cap with every remaining finding answered as deferred with a
   follow-up issue. A blocker is never deferred: it goes to the maintainer.
4. **The server side verifies, and does not depend on the gate.** A passing gate writes a receipt keyed by the diff's
   patch-id, with each model's OCR output as evidence. `reviewgate verify` recomputes the verdict from that evidence under
   the base branch's policy. It is called by the review watcher that posts `codex-review`, which lives outside this
   repository; a diff with no verified receipt gets the full server-side review as before. A diff that changes a
   `security-critical` path, or that OCR covers none of, is never waived. No mash CI job verifies a receipt, and
   `.mergify.yml`, `ci.yml` and the queue conditions are unchanged.
5. **What is not adopted.** The source ADR's decisions on review volume and on the fallback reviewer concern the
   watcher, not this repository, and are not decided here. Neither is the rule that a pull request may pass
   `codex-review` with an open finding in the tests-and-docs tier: the tiers say when an author may push, and what
   `codex-review` posts stays the watcher's rule.

Files: `scripts/gate` (the merge gate; `--review` is this loop, `--pre-pr` the fast CI guards), `scripts/reviewgate/`
(the verdict engine, standard library only), `scripts/gate_review_test.sh` (the `--review` path against a stub `ocr`),
`docs/review/policy.json` and `docs/review/in-session-loop.md` (how it works and how it ties to `codex-review`).

One behaviour change from the source: `--pre-pr` runs staticcheck (advisory, as in mash's CI) and the ADR lint, where
the source runs its own file check, repository lint and golangci-lint, so that it matches the checks mash's CI runs; and
the source's tier-b, failpoints and web-reader (rangeview) checks are dropped because mash has none of them. <!-- adrlint:allow vocabulary -->

Other changes from the source, none to the review behaviour: mash's paths and tier table; state and data directories under
`mash`; the source's failpoints, scenario, web-reader and tier-b steps removed from the full gate because mash has none of
them, and `--pre-pr` runs staticcheck (advisory, as in CI) and the ADR lint where the source runs its own repository checks.
The standing question in the policy says "the configured workspace" where the source names its own.

### Consequences

* Good, because a review round happens in the author's session in minutes, and CI runs once on a head that has been reviewed.
* Good, because the tiers put the strictest scrutiny on the identity, policy, admission and release-control paths.
* Good, because nothing server-side changes, so a mash pull request is never less reviewed than before.
* Bad, because the gate needs `ocr` and a provider login on the author's host, and OCR runs are metered.
* Bad, because the receipt saves a round only when the watcher is pointed at the store the gate writes (`GATE_REVIEW_STORE`
  and `REVIEW_INLOOP_STORE`); until then the receipt is unused and the server reviews as before.
* Bad, because OCR skips Markdown and test files, so in a mixed diff nobody reviews those in the loop; the tests-and-docs
  tier blocks only a blocker and the server-side review remains the reviewer for them.
* Bad, because the tier paths and the model list were chosen on the source project's code and may need tuning for mash.

### Confirmation

`go test ./scripts/reviewgate/` covers the verdict engine and, in `TestRepoPolicy`, this policy file: its tiers, blocking
severities, round caps and the tier each representative path resolves to. `bash scripts/gate_review_test.sh` runs the
`--review` path with a stub `ocr`: pass, block, round counting, the cap, coverage and skipped diffs, a failed preview,
questions in the background and the findings log. `go run ./scripts/adrlint docs` checks this record. A review by a real
`ocr` run and the watcher's verification of a mash receipt are not covered by any test in this repository.

### Known weaknesses inherited from the source

A Codex adversarial review of pull request #352 (gpt-6-sol, 2026-11-10) found two weaknesses in the receipt mechanism
this record adopts. Both are in the source project's design, and mash carries them unchanged.

* **Forged receipts.** `reviewgate verify` checks a receipt's evidence against hashes stored in that same writable
  receipt. Anyone who can write the store the watcher reads can write a `pass` record and a matching evidence file for an
  unreviewed patch, and `verify` recomputes a pass.
* **Stale receipts.** A receipt is looked up by patch-id and changed file names only. OCR's `exact_range` and source
  artifact hash are recorded but never checked, so after a rebase onto changed surrounding code an old clean review can
  waive review of the new combination. The patch-id also omits hunk line numbers.

mash does not diverge from the source: the fix belongs at the source, and mash pulls it. Until it is fixed, a receipt is
evidence of a local review and not a substitute for the server-side `codex-review` run. `TestKnownWeakness_ForgedReceiptVerifies`
and `TestKnownWeakness_ReceiptIgnoresRange` in `scripts/reviewgate/main_test.go` pin today's behaviour; when the source
fixes these, those tests must be flipped deliberately.

## Pros and Cons of the Options

### Port the sibling project's gate (chosen)

* Good, because it is a working, tested process the maintainer already uses.
* Good, because one process serves both repositories and the same watcher.
* Bad, because the mash tier table is a first mapping and is untried.

### Keep only the server-side review

* Good, because there is nothing to maintain here.
* Bad, because every round still waits for a push, CI and a verdict the author does not see.

### Design a mash-specific loop

* Good, because it could fit mash's code more closely.
* Bad, because the maintainer ruled against redesign, and the watcher already speaks the source format.

## More Information

* Source: the sibling project's ADR 0047, linked above, and its `docs/review/in-session-loop.md`.
* Related: [ADR 0002](0002-record-architecture-decisions-as-madr-records.md), [ADR 0068](0068-a-change-log-entry-is-a-fragment-file-and-the-adr-index-is-generated.md) (the ADR index is generated at release).
* How it works: [`docs/review/in-session-loop.md`](../review/in-session-loop.md).
