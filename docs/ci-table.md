# The CI table: which job runs on which event (rule 21)

One table, pinned by `internal/release/ci_table_test.go`: the test reads `.github/workflows/ci.yml`, `release.yml` and `nightly.yml`, works out the events each job runs on from the workflow's triggers and the job's `if`, and fails when the result differs from this table (ADR 0160). To change what runs when, change the workflow and this table in the same pull request.

Events: **pr** an ordinary pull request push; **draft** a Mergify merge-queue draft (a pull request on a `mergify/` branch, tested once per batch); **main** a push to main; **tag** a `v*` tag push (and the release dry run); **nightly** the schedule on main. A docs-only change (every path a Markdown file or under `docs/`, `scripts/ci-changes`) skips the heavy jobs (`test (linux)`, `reproducible`, `conformance`) at job level; the cheap jobs below still report.

| File | Job (check name) | Events | Gates | Notes |
|---|---|---|---|---|
| ci.yml | changes | pr, draft, main | yes | the classifier (`scripts/ci-changes`) |
| ci.yml | test (linux) | pr, draft, main | yes (required check) | heavy; skipped on a docs-only change |
| ci.yml | reproducible | draft, main | yes (required check) | heavy; not on an ordinary pull request push |
| ci.yml | conformance | pr | no | fast static slice; the review starts after it; never on a queue draft |
| ci.yml | adr-numbers | pr, draft, main | yes | cheap |
| ci.yml | docs-lint | pr, draft, main | yes | cheap; the only job a docs-only change needs |
| ci.yml | pr-check | pr, draft, main | yes | the one verdict the queue and the review read |
| release.yml | test (macos) | tag | no (advisory, ADR 0079) | macOS runs on tagged releases only (a tag push, not the workflow_dispatch dry run); nothing needs it, so a macOS runner outage never blocks a tag |
| nightly.yml | test (linux, eph) | nightly | no (advisory, ADR 0123) | the ephemeral-runner pilot, once a day on main; in no required check and no Mergify condition |

Not in the table, by rule: no macOS and no eph job runs on a pull request, a queue draft or a push to main.
