---
status: accepted
date: 2026-10-31
decision-makers: Randy Bias (mash maintainer)
recorded: 2026-10-31
---

# Record architecture decisions as MADR records

## Context and Problem Statement

mash's design decisions were spread across the design spec (`docs/specs/2026-10-21-mash-design.md`), plans, pull
request bodies, issues and maintainer discussions. A reader who wants to know why mash works the way it does, or
whether a question is already settled, has to reconstruct the answer from all of them. Before the repository goes
public, mash needs one place where each decision, its date and its reasons can be found, and a rule that keeps that
place current.

## Decision Drivers

* A reader outside the project must be able to learn how, when and why a decision was made.
* The options that were weighed, not only the winner, should be on record.
* A settled decision should not be silently re-decided or rewritten.
* Decisions made before the records existed still need a record, without pretending to know more than the sources say.

## Considered Options (reconstructed 2026-10-31; not recorded at the time)

* MADR 4.0 records, one decision per file in `docs/decisions/`
* Short free-form decision records (context, decision, consequences)
* Keep decisions in the design spec and pull request bodies only

## Decision Outcome

Chosen option: "MADR 4.0 records, one decision per file in `docs/decisions/`", because MADR (Markdown Architectural
Decision Records) records how, when and why a decision was made, including the options weighed.

The rules:

* One decision per file in `docs/decisions/`, named `NNNN-short-slug.md`, in MADR 4.0 format.
* A record is never rewritten once accepted. A changed decision gets a new record that supersedes the old one, and the
  old record's status becomes "superseded by ADR-NNNN".
* A pull request that makes or reverses a decision carries its record in the same pull request.
* Records 0003-0037 were backfilled on 2026-10-31 from the design spec, pull requests, issues and maintainer
  discussions. Each carries `recorded: 2026-10-31 (backfilled)` next to the date the decision was made. Where the
  options weighed were not written down at the time, the section is titled "reconstructed" and lists only the
  alternatives a reader would weigh. Where no reason was written down, the record says so instead of supplying one.

### Consequences

* Good, because each decision has a stable number other documents (the architecture document, code comments, pull
  requests) can link to.
* Good, because superseding instead of editing keeps the history of a decision readable.
* Good, because labelling reconstructed options and missing reasons keeps the backfilled records honest.
* Bad, because the backfilled records are only as good as the surviving sources; some reasons are lost for good.
* Neutral, because the design spec stays the description of how mash works; the records explain why.

### Confirmation

No automated test confirms this today. Review of each pull request that changes behaviour checks that it carries a
record when it makes or reverses a decision.

## Pros and Cons of the Options

### MADR 4.0 records

* Good, because its sections (drivers, options, outcome, consequences, confirmation) force the reasons and the
  alternatives onto the page.
* Good, because it is a published, widely used template, so readers recognise it.
* Bad, because it is longer than a free-form record.

### Short free-form decision records

* Good, because they are quick to write.
* Bad, because nothing prompts the writer to record the options weighed or how the decision is confirmed.

### Design spec and pull request bodies only

* Good, because it needs no new documents.
* Bad, because the spec is edited in place, so the reason for an earlier choice disappears when the text changes.
* Bad, because pull request bodies are hard to find later and are not in the repository.

## More Information

* MADR 4.0: <https://adr.github.io/madr/>.
* Record 0001 (publishing a release is one resumable command) is in open pull request #159, which also adds the index
  `docs/decisions/README.md`.
* Records 0003-0037: the backfilled decisions, from the design spec of 2026-10-21 onward.
* **Not yet true:** record 0001 in pull request #159 uses its own section layout, not MADR's.
