# Issue 130: suspend endpoint inbound delivery — already implemented

## Summary

GitHub issue [#130](https://github.com/bogworks/mash/issues/130) asks for admin verbs to stop an endpoint's inbound mail, keep stored mail readable, and report whether the endpoint is suspended (for a safe drain-before-deprovision path).

**That work already shipped on `main`.** This document is a status/spec for the factory gate only. It is not a redesign and not a plan to re-implement suspend.

## What shipped

| Piece | Location |
| --- | --- |
| Decision | [ADR 0057](../decisions/0057-an-admin-can-suspend-an-endpoints-inbound-mail-by-parking-its-inbox-stream-subjects.md) (accepted 2026-11-02) |
| Implementation PR | [#154](https://github.com/bogworks/mash/pull/154) (merged; commit `fa66e65`) |
| Admin API | `Admin.Suspend` / `Resume` / `Suspended` in `internal/admin/suspend.go` |
| Store | Stream metadata keys (`mash_suspended_subjects`, `mash_suspended_at`, suspension id); subjects parked to `mash.suspended.<name>` |
| CLI | `mashctl suspend` \| `resume` \| `suspended`; deprecated `mash admin` same verbs |
| Exit codes (`suspended`) | `0` if suspended, `3` if not, `1` on error |
| Drain guard | `deprovision --suspension ID --drained-to N` |
| Operator docs | [operations.md — Suspend](../operations.md#suspend-an-endpoints-inbound-mail), [mashctl.md — Suspending](../mashctl.md#suspending-an-endpoints-inbound-mail) |
| Changelog | [0.3.3 Added](../../CHANGELOG.md) names `#130` and ADR 0057 |
| Follow-ups already on main | Drain/`--drained-to` safeguards; suspend settles `last_seq` before print (`#282` / `#283`) |

## Product behavior (as shipped)

1. `mashctl suspend NAME` parks the inbox stream subjects so every sender's publish is refused; stored mail stays readable; prints suspension `id`, `last_seq`, and `msgs`.
2. `mashctl resume NAME` restores the saved subjects and clears suspension metadata (idempotent with suspend).
3. `mashctl suspended NAME` reports state from the stream; exits `0` / `3` / `1` as above.
4. Endpoint session, owner reads, and `limits` keep working while suspended; `set-zone` refuses on a suspended endpoint.
5. `mashctl deprovision NAME --suspension ID --drained-to N` refuses unless the inbox is still suspended under that id at exactly sequence `N` (and the directory user key still matches suspend-time).
6. Suspend does not revoke credentials; revocation remains part of deprovision/retire.

## Why issue 130 is still open

PR #154 referenced the issue as `Refs #130`, not `Closes #130` / `Fixes #130`. Docs and changelog already describe the feature. No product gap remains for the original request.

## Decisions (this gate)

| Decision | Options | Choice |
| --- | --- | --- |
| Implementation scope after approval | Re-implement suspend; or docs-only close-out | **Docs-only close-out** — code is complete |
| Spec shape | Greenfield product/tech spec; or already-implemented status | **Already-implemented status** (this file) |
| Post-approval change size | Broad docs rewrite; or minimal cross-ref + close | **Minimal** — see below |

## Assumptions

- No change to suspend/resume/suspended semantics, exit codes, or drain flags is requested by this factory run.
- Human approval of this status/spec is required before the docs-only close-out PR is opened.
- The factory feature PR title requested in the original automation brief does **not** apply to a re-implementation; after approval the follow-up is docs-only and still draft / do-not-merge unless the maintainer says otherwise.

## Out of scope

- Any new suspend implementation or API change.
- Redesign of parking vs seal vs policy-deny (settled in ADR 0057).
- Closing or merging without human approval of this spec.
- Unrelated suspend follow-ups (already fixed settle/`last_seq` work stays as-is).

## Post-approval deliverable (docs-only)

After this status/spec is approved, open one **minimal docs-only** draft PR on `main` that:

1. Updates ADR 0057 **More Information** (or one adjacent docs cross-ref) to state that the behavior shipped in PR #154 / mash 0.3.3 and that issue #130 is satisfied.
2. Uses a closing keyword (`Closes #130` or `Fixes #130`) so the issue closes when that PR merges.
3. Does **not** change Go code, CLI behavior, or tests.
4. Stays draft titled clearly as docs close-out for #130; do not merge unless the maintainer asks.

No new changelog fragment is required for the original feature: `CHANGELOG.md` [0.3.3] already links `#130`. Add a fragment only if the close-out PR itself needs a Docs note under Unreleased (optional; prefer skipping if the ADR line alone is enough).

## Validation criteria (post-approval PR)

- [ ] Diff is documentation (and optional changelog fragment) only — `git diff --stat` shows no `*.go` / test changes.
- [ ] ADR 0057 (or chosen cross-ref) names PR #154 and issue #130 as shipped/satisfied.
- [ ] PR body contains `Closes #130` or `Fixes #130`.
- [ ] `gh pr view` shows draft; factory label applied; not merged by the factory.
- [ ] After merge (human-driven): issue #130 is closed and points at the close-out PR.
