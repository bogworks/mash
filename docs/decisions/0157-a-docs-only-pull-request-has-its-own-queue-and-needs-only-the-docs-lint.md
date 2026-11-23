---
status: accepted
date: 2026-11-21
decision-makers: Randy Bias (mash maintainer)
---

# A docs-only pull request has its own merge queue and needs only the docs lint

Amends: ADR-0078, ADR-0116

## Context and Problem Statement

A six-line README change sat in the merge queue for over ten minutes, behind the full test set and three reviews. The maintainer's
ruling (2026-11-21): "I think linting docs is all that's required. As long as they properly conform to their syntax, I don't see why
anything else needs to run." ADR 0078 and ADR 0116 make queue entry depend on an approval or the `lead-approved` marker on the pull <!-- adrlint:allow vocabulary -->
request and on the `lead-review` status, and `codex-review` is a queue condition too. Those are right for code. For a change to <!-- adrlint:allow vocabulary -->
documentation alone they cost minutes and add nothing a syntax check does not.

## Decision Drivers

* A documentation-only change should clear the queue in about the time of its lint.
* "Documentation alone" must be exactly what `scripts/ci-changes` already calls docs-only, from one definition, and must never include a
  CI definition, the queue configuration, a script, the specification, source, or anything a build, test or release tool reads.
* Text an agent executes is code in effect, wherever it sits.
* The decision must be pinned by a test, so a future edit cannot widen it quietly.

## Considered Options

* Keep one queue and the full conditions for every pull request (today).
* A second queue, first in order, with only the docs lint (`pr-check`, which carries `docs-lint`) and `adr-lint`, entered when no changed file
  matches the code regular expression; the approval rule is inactive for such a pull request.
* Skip the heavy jobs in the one queue but keep the approval and `lead-review` conditions. <!-- adrlint:allow vocabulary -->

## Decision Outcome

Chosen option: the second queue.

* **The docs queue** (`queue_rules`, first) needs `base = main`, not a draft, no conflict, no `hold` label, at least one changed file, no
  changed file matching the code regular expression, `pr-check` and `adr-lint`. It is never batched with the default queue's pull
  requests, so a batch with one code change runs the full set, and it has the highest priority. It has no `codex-review`, no
  `lead-review`, no approval and no `lead-approved`. <!-- adrlint:allow vocabulary -->
* **The code regular expression** is `scripts/ci-changes`' docs-only definition as one expression, written in three places in
  `.mergify.yml` (the docs queue, the `merge_protections` rule's `if`, the priority rule) as the same text.
  `internal/release/mergify_test.go` fails if the three differ and fails if the expression and `scripts/ci-changes` disagree on any of
  about eighty paths and a sample of every tracked file. Agent instructions and what an agent or a tool executes are code, at any depth and in any case: `AGENTS.md`, `CLAUDE.md`, `GEMINI.md`,
  every `SKILL.md`, `.cursorrules`, `*.tmpl`, and anything under a `skills`, `commands`, `agents`, `hooks`, `prompts`, `briefings`, `plugins`,
  `.claude-plugin` or `mash-setup` directory (planner #2845). `README.md`, `docs/install.md`, `SECURITY.md` and `CONTRIBUTING.md` stay documentation. Under `docs/` only plain-text formats are documentation (`.md`, `.txt`, `.rst`, `.adoc`, `.csv`, `.log`): a script, a data or configuration file, an `.svg` or a `.cast` there is runnable or active material and is code (security-review F1).
* **This reverses an earlier rule.** `ci.yml`, `lint.yml` and `security.yml` said that a push to `main`, a merge group and a queue draft
  always run the full set, so that `main` is always tested and the release gate always finds the test runs on its candidate. A push
  and a queue draft are now classified like a pull request; a merge group and a schedule still always run the full set. The
  release gate no longer requires the required checks on the candidate commit itself when the candidate is docs-only: it walks back
  along the first-parent history to the newest commit whose required checks (`test (linux)`, `reproducible`) ran and succeeded, and accepts
  the candidate only if every commit between is docs-only by the same classifier (and refuses past a bounded number of commits).
  `internal/release` is security-critical (ADR 0080): the change is `CheckCIWalk` and its tests, `docs/release.md` states the rule,
  and a docs commit can neither break nor fake the gate. <!-- adrlint:allow vocabulary -->
* **The docs lint** (`scripts/doclint`, the `docs-lint` job in `ci.yml`, in `pr-check`'s needs) checks Markdown syntax: closed code
  fences and relative links that resolve.
* **The approval rule** (`merge_protections`) is inactive for a pull request with no changed file matching the code regular expression.
  The `hold` label and a conflict still block.
* **In the queue and on `main`** the same classifier applies: a queue draft is classified by its diff against `main` (the whole batch), a
  push by the paths it added. The release gate judges a docs-only head of `main` by the newest commit below it whose tests ran, provided
  every commit between is docs-only.

### Residual risk

A docs-only change reaches `main` with no review beyond the syntax lint, and no approval or `lead-review`. It includes a change to <!-- adrlint:allow vocabulary -->
`README.md`, `docs/install.md` and `SECURITY.md`, which carry the install and verify commands users run; the maintainer ruled
(2026-11-21, through the lead) that they stay in the docs queue, lint only, because the complaint that started this was a six-line README <!-- adrlint:allow vocabulary -->
change and the ruling is that linting docs is all that is required. The residual risk is bounded: a pull request from a fork does not
run workflows, and cannot reach the queue, until a maintainer approves its run; every merge is attributable to its author and revertable;
what the definition excludes (CI definitions, the queue configuration, scripts, the specification, source and module files, the change
log, agent instructions, skills, commands, hooks, prompts and templates) still gets the full set, the approval and the reviews; and the
`hold` label and `adr-lint` still apply.

### Consequences

* A docs-only pull request clears in about the time of the lint, ahead of queued code.
* More of main's history is untested commits; the release gate's walk-back and `ci.yml` header say so.
* The code regular expression is the one place that decides; widening the docs queue is a change to it and to its test.

### Confirmation

`TestMergify_docsQueue`, `TestMergify_docsRegexAgreesWithTheClassifier`, `TestMergify_docsOnlyPriorityIsHighest` and
`TestDocsOnly_classifier` in `internal/release`; `scripts/doclint`'s tests; the release gate's walk-back tests.

## Pros and Cons of the Options

* One queue, full conditions: simple and uniform, but a README line waits behind the full test set and three reviews.
* A second queue with only the docs lint: clears in minutes and is pinned by tests, at the cost of the residual risk below.
* Skip only the heavy jobs: saves the test time but keeps the approval and review waits, which are most of the ten minutes.

## More Information

The maintainer's words, 2026-11-21: "Why are we running full CI on docs changes?... totally unwarranted." and "I think linting docs
is all that's required." The standard is `dev-process` rule 20 (bogworks-dev-standards). The release gate change is in
`internal/release` (`CheckCIWalk`).
