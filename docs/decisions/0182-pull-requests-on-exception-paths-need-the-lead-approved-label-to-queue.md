---
status: accepted
date: 2026-11-25
decision-makers: Randy Bias (mash maintainer)
---

# Pull requests on exception paths need the lead-approved label to queue <!-- adrlint:allow vocabulary -->

Amends: ADR-0177

## Context and Problem Statement

[ADR 0177](0177-a-pull-request-enters-the-queue-by-itself-when-its-checks-and-codex-are-clean.md) lets a clean pull
request enter the queue by itself and keeps `hold` as a deliberate brake for changes that need a person's judgement:
contract requirements, ADRs that change one, spec files, CI and queue configuration, and order-16 code. Rule 27 says
pull requests are opened ready and never held by their author, so the brake rests on someone noticing. Whether a change
"changes a requirement" cannot be decided mechanically, but where it is made can. The question is how to stop those
changes at the queue without a person having to notice.

## Decision Drivers

* Rule 27 keeps the default: open ready, no `hold`, queue by itself.
* The stop must be mechanical: a path list the queue evaluates, not a judgement.
* The label must not outlive the code it covered.
* The queue must not deadlock (#424): nothing new goes into `merge_protections`.

## Considered Options

* Keep `hold` as the only brake, set by whoever notices.
* Require the `lead-approved` label to enter the queue for a pull request that touches an exception path. <!-- adrlint:allow vocabulary -->

## Decision Outcome

Chosen option: a pull request that changes a file on an exception path needs the `lead-approved` label to enter the <!-- adrlint:allow vocabulary -->
queue; every other pull request queues by itself as in ADR 0177.

* **Condition.** The `default` and `docs` queues' `queue_conditions` gain `or: [-files ~= <regex>, label = lead-approved]`. <!-- adrlint:allow vocabulary -->
  The `docs` queue needs it too, because a pull request that changes only `docs/decisions/` is docs-only.
* **Exception paths** (one regex, the same text in both queues, matched against each changed path):
  * CI and queue configuration: `.github/`, `.mergify.yml`, `scripts/ci-*` (including `scripts/ci-changes`), `scripts/gate*`, and every script a workflow runs from the pull request (`scripts/build-twice`, `scripts/buildrelease/` (which it builds and runs), `scripts/adrlint`, `scripts/changelog`, `scripts/doclint`, `scripts/draft-guard`, `scripts/lead-approve`, `scripts/notes-base`, `scripts/publishcheck`, `scripts/repofiles`, `scripts/review*`), since a change to one can make its own check pass; the shipped assets (`install.sh`, the `mash-*` scripts) are not included; <!-- adrlint:allow vocabulary -->
  * release tooling: `scripts/release*` (including `scripts/releasecheck/`), `internal/release/`;
  * `spec/`;
  * `docs/decisions/`, the whole folder;
  * `contracts/`;
  * order-16 guarded paths, taken from `scripts/releasecheck/unlinked.go`: every `internal/policy*` directory, and
    `internal/requesterreg`, `internal/issue`, `internal/reconcile` and `internal/listset` with their subpackages.
* **A pull request of 3000 files or more also needs the label,** because GitHub lists at most 3000 files of a pull request and Mergify cannot see an exception path beyond that.
* **The label is removed on every push.** `label-reset` strips `lead-approved` on `opened`, `reopened` and <!-- adrlint:allow vocabulary -->
  `synchronize`, so approval covers the head it was given on and no later one.
* **`hold` is unchanged** as a deliberate brake with its reason written on the pull request.

### Consequences

* Good: the changes ADR 0177 said a person must see are stopped by the queue, not by someone remembering to set `hold`.
* Good: every other pull request is unaffected.
* Bad: the list is a mechanical stand-in for "changes a requirement"; a requirement change outside it still needs `hold`.
* Bad: a new engine package must be added to the regex as well as to `unlinked.go`; a test fails if it is not.

### Confirmation

* `internal/release/mergify_test.go`: `TestMergify_exceptionRegexIsTheSameInBothQueues`, `TestMergify_exceptionPaths`,
  `TestMergify_exceptionRegexCoversTheEngineList` (reads `policyPrefix` and `policyNamedElsewhere`) and
  `TestMergify_exceptionPathsNeedTheLabelToQueue`.

## Pros and Cons of the Options

### Keep `hold` as the only brake

* Good, because it adds no condition.
* Bad, because nothing stops a change on these paths unless someone notices and sets it.

### Require the label on exception paths (chosen)

* Good, because the stop is mechanical and resets on every push.
* Bad, because the path list must be kept in step with the code.

## More Information

* Amends [ADR 0177](0177-a-pull-request-enters-the-queue-by-itself-when-its-checks-and-codex-are-clean.md); that file is not changed.
* `.mergify.yml`, `docs/branch-protection.md`.
