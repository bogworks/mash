# Change-log fragments

A pull request that changes what a user sees adds one file here. It does not edit `CHANGELOG.md`: two pull requests
that both did would conflict, and release preparation writes that file once ([ADR 0068](../docs/decisions/0068-a-change-log-entry-is-a-fragment-file-and-the-adr-index-is-generated.md)).

## Name

`<slug>.<kind>.md`

- `slug`: lower-case letters, digits, hyphens and underscores, for example `heartbeat-on-demand`. Pick one that is
  unlikely to collide with another pull request's (the branch name works). Fragments are folded in slug order.
- `kind`: one of `added`, `changed`, `deprecated`, `removed`, `fixed`, `security`: the
  [Keep a Changelog](https://keepachangelog.com/en/1.1.0/) sections.

## Text

The entry as it should read in the change log, without the leading `- ` and without a heading: the file name carries
the section. It may run to several lines; `assemble` indents the continuation lines by two spaces. Say what a user
sees and, where it helps, link the ADR or issue.

```text
`mash who` shows `seen X ago` from the last answer to a presence probe, not the last heartbeat
([ADR 0066](docs/decisions/0066-...md)).
```

## Check and assemble

- `go run ./scripts/changelog check` lints the fragments (name, kind, not empty, no leading bullet or heading). CI runs
  it. `README.md` in this directory is ignored.
- `go run ./scripts/changelog assemble --version X.Y.Z --date YYYY-MM-DD` runs in release preparation, never in an
  ordinary pull request. It folds every fragment, and any bullets already under `## [Unreleased]`, into a new
  `## [X.Y.Z] - date` section below an emptied Unreleased heading, in the order Added, Changed, Deprecated, Removed,
  Fixed, Security (only the sections that have entries), then removes the fragment files. Entries that were already
  under Unreleased come first in their section, then the fragments by slug. `--dry-run` prints the section and changes
  nothing. It refuses a version that already has a section.
- `release-check` refuses a release commit that still has fragments here.
