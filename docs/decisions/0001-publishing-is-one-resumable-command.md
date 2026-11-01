---
status: accepted
date: 2026-10-31
decision-makers: Randy Bias (project owner)
---

# Publishing a release is one resumable command, and a draft exists only while it runs

## Context and Problem Statement

Publishing was five hand steps: the pre-tag gate, a signed tag and push, waiting for `release.yml`'s draft,
`release-check --assets`, and `gh release edit --draft=false`. Each is safe alone and nothing forced the next, so
drafts were left behind: v0.2.8 and v0.2.9 stayed drafts while v0.2.10 was published, and a version was cut over an
unpublished one. The release notes were also built from the last *tag*, so the notes for the release after an
unpublished one skipped what the unpublished one carried. A consumer project had reached the same decision for its
own releases.

How should publishing be shaped so a release cannot skip an unpublished one and no draft is left behind?

## Decision Drivers

* A release must never be cut over an unpublished one.
* After any failure or interruption the next run must continue safely, without repeating a finished step or
  repeating anything that cannot be undone.
* The signature that matters is the signed tag, so signing stays on the maintainer's machine.
* The by-hand steps in [release.md](../release.md) should stay valid as the manual path.

## Considered Options

* Keep the five by-hand steps and document them better.
* One resumable command that finds its state from the world (`release-check publish`).
* Move the whole path into CI.

## Decision Outcome

Chosen option: "One resumable command", because it is the only option that makes the safe order the only order
without moving the signing key off the maintainer's machine.

1. **One command.** `release-check publish [--wait <duration>] <version> <X>` runs the whole path on the
   maintainer's machine, because it signs the tag with the maintainer's key.
2. **Fixed states, found from the world.** It reads what exists (the tag on the remote, the release through `gh`) and
   resumes there, so a rerun after any failure or interruption continues, and never repeats a step that is done:

   | Found | Next |
   | --- | --- |
   | no tag `v<version>` | pre-tag gate (the existing `release-check <X> <version>`, plus rule 3), `git tag -s`, push, verify the remote tag |
   | tag at X, no release | wait for `release.yml`'s draft (`--wait`, default 45 min; stops early if the tag's workflow run failed) |
   | draft | pre-tag gate (skipped when this run just ran it before tagging), then `--assets` gate, then `gh release edit --draft=false` |
   | published | verify the published release (`--assets`, which only reads) |

   A tag at a commit other than X is refused and nothing is changed. Every step re-checks its own precondition, so a
   resumed run is judged exactly as a fresh one. After publishing, the release is verified once more with the
   read-only `--assets` gate. Immediately before publishing it re-runs the two ordering checks of rule 3 (strictly
   above the latest published; earlier releases published or skipped), because another version can publish during the
   asset gate; a violation is refused (exit 3) and nothing is published.
3. **Order is enforced before tagging.** The pre-tag gate refuses `v<version>` unless (a) every lower `vN.N.N` tag has
   a published release or is recorded in `docs/skipped-releases.txt` (the earlier-releases step, unchanged), and (b)
   `<version>` is strictly above the latest published (non-draft) release.
4. **Release notes come from the last published release, not the last tag.** Implemented in the same change by
   `scripts/notes-base`, used by `release.yml`.
5. **A draft exists only while the command runs.** `release.yml` guards the draft it creates (`scripts/draft-guard`:
   it refuses to draft while a draft of a lower version exists; an earlier tag with no release at all is refused by
   the pre-tag gate). That check and the draft's creation are not atomic and the concurrency group is per tag, so two
   tags' runs can both pass it. Right after `gh release create` the workflow therefore re-checks
   (`scripts/draft-guard --after-create <version>`): if a draft of a lower version or a published release of a higher
   version now exists, the guard prints which release stands in the way and fails the run, and it never deletes
   anything: this run's draft stays for the maintainer to resolve (`gh release delete <tag>` after checking its state,
   or publish the lower one first, then rerun the workflow). The command
   thus ends in published, or in a failure that the next run resumes.
6. **Exit codes are release-check's:** 0 published and verified, 2 usage, 3 refused (a judgment), 4 the command itself
   failed or timed out (resumable).

What the command does not do: there are no release candidates (mash tags are `vN.N.N` only, so no candidate
exemption and no patch-branch comparison), no review range to move, and no signing of `SHA256SUMS` (CI builds the
binaries and `SHA256SUMS`; the signature the command relies on is the signed tag, verified against
`docs/allowed_signers`). Pinning the released version in a consumer is not part of the command.

### Consequences

* Good, because a release can no longer skip an unpublished one, and a leftover draft blocks the next version until
  the maintainer publishes or deletes it, on purpose.
* Good, because the after-create guard never deletes anything: a race leaves a draft, never a lost release.
* Bad, because a draft left by such a race blocks the next version's draft-guard until the maintainer resolves it, the same stance as a stale draft.
* Good, because the by-hand steps in release.md stay as the description of each state, and as the manual path when
  the command cannot run.
* Bad, because the command needs the maintainer's SSH signing key (`RELEASE_SIGNING_KEY`; it passes
  `-c gpg.format=ssh -c user.signingkey=` explicitly, so the machine's git signing config does not matter), `gh`
  authenticated for the release repository, and push access to it. It verifies the new tag against
  `RELEASE_SIGNERS` (default `docs/allowed_signers`) before pushing.
* Bad, because publishing across versions is not locked: the recheck before publishing narrows the window but does
  not close it, and a single maintainer runs the command.
* Neutral, because the one step that cannot be undone, publishing, is preceded by a fresh check that the tag is still
  at X and the release is still a draft.

### Confirmation

`scripts/releasecheck/publish_test.go` drives each state, including resumption after a failure at every step;
`internal/release/workflows_test.go` and `release_notes_test.go` check the draft guard and the notes base; the daily
publish check (`publish-check.yml`) flags a draft or an out-of-order release after the fact.

## Pros and Cons of the Options

### Keep the five by-hand steps

* Good, because nothing new to build.
* Bad, because it is what left v0.2.8 and v0.2.9 as drafts: nothing forces the next step.

### One resumable command

* Good, because the order is enforced and a rerun resumes from what exists.
* Good, because signing stays on the maintainer's machine.
* Bad, because it is more code to maintain, and it ties publishing to one machine's key.

### Move the whole path into CI

* Good, because no one's machine is involved.
* Bad, because the signing key would have to live in CI, which weakens the signed tag as the thing that
  authorises a release.

## More Information

* The v0.2.8, v0.2.9 and v0.2.10 releases (drafts left under a published release).
* [release.md](../release.md); `scripts/releasecheck`; `scripts/draft-guard`; `scripts/notes-base`.
