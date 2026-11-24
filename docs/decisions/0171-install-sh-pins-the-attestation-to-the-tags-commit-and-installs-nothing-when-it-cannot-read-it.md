---
status: accepted
date: 2026-11-24
decision-makers: Randy Bias (mash maintainer; contract 0.5.9 REQ-014), mash-lead (the refusal when the commit cannot be read)
---

# install.sh pins the attestation to the tag's commit, and installs nothing when it cannot read it

## Context and Problem Statement

[ADR 0150](0150-releases-carry-install-and-local-nats-setup-scripts-as-attested-assets.md) has `install.sh`, with a `gh` new enough to verify attestations, check `SHA256SUMS` against the release attestation pinned to the repository, the release workflow as signer and `--source-ref refs/tags/<tag>`. A tag ref names no fixed commit: an attestation made by the release workflow at that ref on some other commit passes the same check (#1352). Contract 0.5.9 revision 6, REQ-014, asks for the pin to the commit the tag points to. The commit has to be read from GitHub at install time, and that read can fail. What does the installer do then?

## Decision Drivers

* The check `install.sh` runs is the one the documentation tells people to run by hand, and both pin the same things.
* A failed check leaves nothing installed (ADR 0150).
* A hint is a command that fixes the problem, or there is no hint ([ADR 0168](0168-a-hint-is-a-command-that-fixes-the-problem-or-there-is-no-hint.md)).
* A `gh` that is not logged in must still be able to install.

## Considered Options

* When the commit cannot be read, verify without the commit pin and say so.
* When the commit cannot be read, install nothing and exit 1 (chosen).

## Decision Outcome

Chosen: install nothing. With `gh` 2.102.0 or newer, `install.sh` reads the commit the release tag points to with `gh api repos/<repo>/commits/<tag>`, and if that gives no 40-character commit, from the public GitHub API (`${MASH_API_BASE:-https://api.github.com}`, for a `gh` that is not logged in). It passes the commit to `gh attestation verify` as `--source-digest`, beside `--source-ref refs/tags/<tag>`. If neither read gives a commit, it prints `error: could not read the commit of <repo> <tag>; nothing was installed` on stderr and exits 1. When `gh auth status` fails, it adds `hint: gh auth login, then run the install again`: a logged-in `gh` is not held to the public API's limit of 60 lookups an hour per address. Without a `gh` new enough, nothing changes from ADR 0167.

The manual checks in `docs/install.md` and `docs/security/verify-your-download.md` read the commit the same way, from the public API, and pass `--source-digest`. A reader with a reviewed clone can set the commit from it instead.

### Confirmation

`scripts/install_sh_test.go` runs the installer against a fake `gh`. It checks that the verify call carries `--source-digest` with the tag's commit, from `gh api` or from the public API. It also checks that an unreadable commit installs nothing and exits 1, with the hint only when `gh auth status` fails. The case `installer-commit-unread` in `spec/next.json` holds the refusal's text.

### Consequences

* Good, because an attestation from another commit at the same tag ref is refused.
* Good, because a failed read installs nothing, as a failed check does.
* Bad, because an install with a `gh` that is not logged in also depends on the public API's rate limit; the hint names the command that lifts it.

## More Information

Issue #1352; contract 0.5.9 revision 6, REQ-014. This extends ADR 0150's step on the installer's check; the rest of ADR 0150 stands.
