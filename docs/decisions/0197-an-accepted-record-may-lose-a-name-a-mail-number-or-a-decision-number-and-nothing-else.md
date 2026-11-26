---
status: proposed
date: 2026-11-26
decision-makers: Randy Bias (mash maintainer)
---

# An accepted record may lose a name, a mail number or a decision number, and nothing else

Amends: ADR-0002

## Context and Problem Statement

[ADR 0002](0002-record-architecture-decisions-as-madr-records.md) says an accepted record is never rewritten; a changed
decision gets a new record that supersedes it. `adrlint` enforces this
([README](README.md)): against the merge-base, an accepted record may change only its status line and one
"Superseded by" line, and may not be deleted.

The repository is public, and its readers are strangers. Some accepted records carry text that means nothing to them
and does not belong in a public record: a person's or agent's name, the number of a private mail message ("mail #52"),
or the number of a private decision log ("decision 229"). Removing them changes no decision, yet the lint refuses
every such edit, and superseding a record to take a number out of it would add a record that decides nothing.

## Decision Drivers

* A stranger reading a record must not meet references they cannot follow.
* The text that decides, the options weighed and the reasons must stay exactly as accepted.
* The exception must be narrow enough that a person can review each use in one look, and the lint must be able to
  refuse anything wider.
* The rule that an accepted record is not rewritten must stay true for everything else.

## Considered Options

* Keep the rule as it is; leave the references in place.
* Supersede each affected record with a copy that has the references removed.
* Allow an edit that only removes a name, a mail number or a decision number, reviewed by a person with `lead-approved` <!-- adrlint:allow vocabulary -->
  and checked by the lint (chosen).
* Allow any edit to an accepted record that a person approves.

## Decision Outcome

Chosen option: "allow an edit that only removes a name, a mail number or a decision number, reviewed by a person
and checked by the lint", because it removes the references without opening the record to rewriting.

A pull request may edit an accepted record under all of these conditions:

* **Removal only.** Comparing the record with its text at the merge-base, every changed line is the old line with
  parts taken out, each taken-out part replaced by nothing or by the fixed marker `[removed]`. No line is added, no
  line is deleted (a line may become empty only if it held nothing but removed parts), and no word is changed.
* **What may be removed.** A taken-out part is a person's or agent's name, a reference to a mail message (`mail #NNN`,
  `#NNN` naming a message), or a reference to a decision-log entry (`decision NNN`, `D-NNN`). A reference to a GitHub
  issue or pull request, an ADR number, a date, a version or a command is never removed under this rule.
* **The front matter is untouched.** The status, date and decision-makers lines stay as they are, so the record
  keeps its accepted status and date.
* **A person approves it.** The lint passes such an edit only when the pull request carries the `lead-approved` label, <!-- adrlint:allow vocabulary -->
  and the reviewer reads the diff, which by the first condition shows removals only. Without the label, the lint
  refuses as it does today.
* **A redaction that would need rewording is not allowed.** If taking a name out leaves a sentence that does not read,
  or changes what is decided, the record is superseded instead, as before.

`adrlint` gets an option that the pull request check sets from the label (the lint itself does not read labels in
its record mode). With the option, `compareAccepted` accepts a record whose changes satisfy the first three
conditions, and refuses the record, naming the first line that does not, otherwise. The implementation is a separate
pull request after this record is accepted.

### Consequences

* Good, because the public records stop carrying references a stranger cannot follow.
* Good, because the decision, its options and its reasons are byte-for-byte what was accepted.
* Good, because a wider edit is still refused by the lint, not by a reviewer's attention alone.
* Bad, because a reference removed from a record can no longer be traced from the record to its source; the history of
  the file still holds it.
* Bad, because `adrlint` gains one more case in its immutability check.

### Confirmation

* Tests in `scripts/adrlint` cover: a removal of each kind passes with the option; the same edit fails without it;
  an added word, a changed word, an added or deleted line, a changed front matter line and a removed issue number each
  fail with it.
* The first redaction pull request is reviewed against the first condition by its diff.

## Pros and Cons of the Options

### Keep the rule as it is

* Good, because nothing changes.
* Bad, because the public records keep references that mean nothing to a reader.

### Supersede each affected record with a copy

* Good, because no accepted record is edited.
* Bad, because it doubles the records without a new decision, and the superseded originals keep the references.

### Allow an edit that only removes a name, a mail number or a decision number

* Good, because the change is mechanical to check and quick to review.
* Bad, because it is one more exception to the immutability rule.

### Allow any edit a person approves

* Good, because it is simple.
* Bad, because it ends the rule that an accepted record is not rewritten; a reviewer cannot be expected to spot a
  changed reason in a long diff.

## More Information

* Amends [ADR 0002](0002-record-architecture-decisions-as-madr-records.md), which says an accepted record is never
  rewritten; the rest of it stands.
* Raised by the planner's re-audit of the public repository, round 2, item 11.
