# Plan: the rest of the scrub

Date: 2026-11-10. Scope: the work contract `mash-shed-herd-idioms` (draft-4, ratified 2026-11-09) after its first item
(PR #300: word list, exceptions, verification command, inventory seed, ADR 0073). Every step is its own small pull
request off `main`.

**Semantic-preservation gate (INV-002, BND-001, "Existing behavior to preserve").** No step may change a subject,
payload, trust-label meaning, wake or delivery behaviour, or policy compilation. A step only rewords text, renames what
the contract lets us rename without changing behaviour, or adds inventories and tests. Each pull request runs the
existing suite as it stands (tests edited only where one asserts a removed word, each edit listed) and carries a diff
review that no protocol or policy file changed meaning. If a step finds that a fix needs a semantic change (a role
assumption in a kind, a mapping in the code), it stops and the lead raises a contract gap or an amendment; it is not
fixed under this plan. The retirement of the zone token in mail subjects is the naming-grammar work (plan 2b), not this
scrub.

## Where we start

`go run ./scripts/scrubcheck` on `6dff55f` finds 37 hits in 12 files (`scripts/scrubcheck/inventory.md`). They fall into
groups, and the groups are the pull requests:

| Group | Files | Hits | Nature |
|---|---|---|---|
| G1 shipped prose | `docs/security/threat-model.md` | 1 | the word "coordinator" in an open question; ships, so reword |
| G2 test and comment wording | `internal/endpoint/waking_test.go`, `internal/store/store_test.go`, `cmd/mash/standalone_test.go`, `cmd/mash/standalone_serve_test.go` | 9 | comments and fixtures; `standalone_*` names foreign tools on purpose (it proves mash runs without them) |
| G3 tools that carry the words | `scripts/adrlint/main.go`, `scripts/adrlint/main_test.go`, `internal/portable/portable_test.go` | 20 | a regexp and its tests that look for the words |
| G4 dated records | `CHANGELOG.md`, `docs/evidence/…`, `docs/plans/2026-11-03-next-tranche.md` | 7 | history; not rewritten lightly |
| G5 outside the scan | the release archives, the plugin package, `scripts/scrubcheck/` itself | not yet run | AT-001 needs the archives; the tool skips its own directory |

The scan finds no `rangehand` or `compat.json` in release scripts, their tests, workflows or `docs/release.md`, although
the contract records 32 pins from the audit. Step 6 re-runs AT-003 to confirm that on the delivered tree; it is not
assumed here.

The scan cannot see what the contract judges by behaviour: REQ-001 (kinds), REQ-003 (model-facing text, including the
sentence about the operator and fleet, which uses no listed word) and REQ-009 (no zone mapping). Those need their own
inventories (AT-004, AT-006, AT-007) and they come first, because they can change code and the scrub of words then runs
over the result.

## Order of pull requests

Each row: what, contract refs, tests-first note, and the evidence it adds to the inventory or the completion record.

1. **Emission-path map and the verified sentence** (REQ-003, AT-004). Add `docs/evidence/scrub/emission-paths.md`:
   every path by which mash gives text to a session (instructions with and without a policy file, inert and error text,
   tool names and descriptions, channel and hook notifications, single message, digest, and each trust label: verified,
   claimed, unknown-signer, absent, stale), each with source location and extracted string. Tests first: a test that
   extracts the strings of the verified-label sentence and asserts today's meaning (act on verified mail without asking,
   configurable, D120) so the reword is checked against the meaning, then reword `internal/endpoint/instructions.go`.
   Attach the code search for text sent to a session; every hit is on the map. A reviewer other than the implementer
   adds a line per sentence.
2. **Kind inventory** (REQ-001, AT-006). `docs/evidence/scrub/kinds.md`: each kind (today `task`, `question`, `blocker`,
   `result`, `fyi`), its delivery behaviour (wakes or not, reply expected or not) and what it assumes of sender and
   recipient; a verdict of kept needs "endpoint to endpoint". No code change if every kind passes. A kind that cannot lose
   a role assumption without changing wake or delivery semantics stops and goes to Randy as an amendment. A rename
   would raise DEC-006 (below). Reviewed by someone other than the implementer.
3. **Zone and owner inventory and test** (REQ-009, AT-007). The contract's note of 2026-11-09 says to read "zone" as
   "owner" where it says a herd appears in mash as a zone, and REQ-009 covers translating an outside identifier into
   any mash primitive. So `docs/evidence/scrub/zones.md` lists every zone-related and owner-related input and
   transformation (the owner setting, the zone in subjects and policy, `mash` name rules, `mashctl` flags), with where
   the value comes from and what mash does with it; plus a diff review of every config field, flag, environment variable
   and policy input added or changed by this work (none is expected). Tests first: a zone, and an owner, given through
   an existing interface are used unchanged. A translation or alias found in existing code is not removed here if
   removing it changes behaviour: it is reported as a contract gap (see the gate above).
4. **Reword shipped and test text** (REQ-002, groups G1 and G2). Reword `docs/security/threat-model.md` (use "owner and
   endpoints"; meaning unchanged). In test files, reword comments and fixture names. `standalone_test.go` and
   `standalone_serve_test.go` assert that mash runs with no foreign tools and name them as the test subject; those
   lines become exceptions with that reason, unless a neutral spelling reads as well. INV-002: existing tests pass
   unmodified except where one asserts a removed word; each such edit is listed in the pull request.
5. **Tools that carry the words** (G3, and the open questions below). Decide, per occurrence, removed, excepted or kept
   with a reason, for:
   - `scripts/adrlint` `vocabRE` and its tests: the rule rejects some of the listed words (and also `lead` and
     `orchestrator`, which are not on the list) in new ADRs, so it is a lint of the kind REQ-005 allows ("MAY: keep a
     lightweight lint"). Plan: keep it and record per-occurrence exceptions with a reason (the regexp and the test rows
     that exercise it). Removing it is a separate governance decision with its own rationale and ADR, not a contract
     requirement; it is listed here as an option for the lead to raise, not as the plan.
   - `internal/portable/portable_test.go`: its personal-name regexp must name `homerange` and `rangehand` to detect them
     (R1 in ADR 0044). That is a detector, not a use. Recommended: exception by occurrence, with that reason; it is
     product-code hygiene about a person's names, which the scrub does not remove.
   - `scripts/scrubcheck/` is skipped as its own directory because it holds the list. After the verification run it is
     kept as the record of the one-time aid, or removed with an ADR; the plan keeps it until Randy or the lead says
     otherwise, and the final pull request states which.
6. **Dated records** (G4, DEC-004). The seven hits in dated history (`CHANGELOG.md`, `docs/evidence/…`,
   `docs/plans/2026-11-03-next-tranche.md`) sit on six lines (`CHANGELOG.md:146` has two). This pull request proposes
   per-occurrence exceptions with reasons but does not close those rows until Randy's disposition on DEC-004 is
   recorded (the lead carries it); if he rules for rewriting, the six lines are reworded instead. The rows this
   plan itself adds to the inventory (it names the words in order to plan their removal) get the same disposition.
   `CHANGELOG.md` is generated at release, so its occurrence is an exception, not an edit.
7. **Release surfaces and the final run** (AT-001, AT-002, AT-003, JRN-001). Build the release archives and the plugin
   package, then run `go run ./scripts/scrubcheck` once, over the delivered source, documentation and plugin package and
   every archive at the same time (`-archive` repeated for each), so that an exception that names an archive member is
   not reported as unmatched by a run that lacks that archive. Attach that one output, with its missing-reason and
   unmatched-exception checks. Run the AT-003 search for
   `rangehand` and `compat.json` over the release scripts, their tests, workflows and `docs/release.md`, and a release
   dry run showing version and commit neutrally; close `scripts/scrubcheck/inventory.md` (no `open` row, each with a
   verdict and commit); the reader's report (JRN-001) from someone who has not seen the fleet and is not the executing
   herd; the requirement-to-evidence map; the review records (pre-merge review and adversarial review on another model
   family, per change). This is the pull request that makes the contract's "Accept if" checkable.

Steps 1 to 3 are independent and may run in parallel; 4 to 6 depend only on 1 (the sentence) not changing the same
files; 7 is last, and re-checks every earlier inventory against the final tree, since parallel steps can change code
after another step's document was reviewed. Each pull request is one concern, docs-only ones skip CI, and each carries
"Decision: none" or its ADR (an ADR takes `docs/decisions/NEXT` and bumps it).

**Every pull request, before it merges (INV-003, NFR-003).** The configured pre-merge code review, and an adversarial
review on a model of another family (Codex), both on the pull request's own diff; the pull request names both. Tests
before implementation where the standard says so; Conventional Commits; written as if public, no secrets and no Claude
session links. Step 7 collects the review records; it does not replace doing them per change.

## Open decisions and gaps

- **DEC-004 (dated records: rewrite or except by path), owner Randy, not decided.** The contract text recommends
  exceptions "by path"; REQ-008 forbids whole-file and whole-directory exceptions, so an exception is per occurrence
  whatever Randy rules. Step 6 prepares the per-occurrence proposals and waits for his disposition before closing the
  rows. Gap to raise once: whether "by path" was meant to allow a path-level entry for the evidence directory; as
  written the contract does not allow one.
- **DEC-006 (renamed kind still accepted on receive for one release), owner Randy, not decided.** It matters only if
  step 2 finds a kind that needs a rename; the kinds are judged by behaviour (REQ-001), not by whether their words are
  on the list, so none is assumed safe in advance. If a rename is needed, NFR-002 (no shim unless DEC-006 says
  otherwise) and its rule that every wire-visible rename is listed in the release notes apply, and the plan stops at
  that pull request for a ruling.
- **The two lead notes on PR #300.** (1) Handled in step 5: what happens to `scripts/scrubcheck/` and to adrlint's rule
  after the verification run. (2) ADR 0073 says the tool is "not in `go test`"; its Confirmation section names the
  unit tests, and only the scan is outside `go test`. That is a clarification, not a scrub requirement; an accepted ADR
  may change only its status line and a supersession link (adrlint rule e), so a clarification would be a new ADR. The
  plan does not spend one on it; the lead decides.
- **Scrub of the verified sentence (REQ-003).** The contract fixes the meaning and frees the wording. No gap.

## Completion obligations carried by step 7

Beyond the evidence listed in the steps: NFR-001 (no new tool or runtime dependency; a kept lint runs inside
`go test ./...`), NFR-002 (every wire-visible rename listed in the release notes, if any), and the contract's section 8
record of deviations and residual risks.

## Evidence kept as we go

Each pull request adds its rows to `scripts/scrubcheck/inventory.md` (hit, verdict, commit) and, for steps 1 to 3, its
document under `docs/evidence/scrub/`. The completion record (contract section 8) is assembled in step 7 from those
files and the review records.
