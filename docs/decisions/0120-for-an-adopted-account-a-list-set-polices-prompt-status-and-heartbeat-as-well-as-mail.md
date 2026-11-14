---
status: proposed
date: 2026-11-14
decision-makers: Randy Bias (mash maintainer), to rule; drafted by the sdk lane at the mash coordinator's request while preparing the v0.5.0 review
---

# For an adopted account, a list set polices prompt, status and heartbeat as well as mail

## Context and Problem Statement

[ADR 0055](0055-until-the-maintainer-rules-only-mail-is-policed-prompt-and-status-stay-open.md) says that until the
maintainer rules (its "F8"), only mail is policed. Under it, each zone key carried two open lines,
`agents.prompt.*.<owner>.*` and `agents.status.*.<owner>.*`, so a deny rule never stopped a prompt or a status probe.
[ADR 0091](0091-there-are-no-zones-mail-keeps-the-five-token-subject-and-the-address-is-the-fence.md) removed zones and
repeated, at its line 66, that prompt and status stay outside the fence as 0055 says.

The list-set model came after both. Contract mash-messaging-policy draft-2 (D206) has a list set name, per sender, who it
may reach, and [ADR 0102](0102-list-set-permissions-are-one-template-per-owner-group-with-allow-and-deny-tags-and-a-deny-sentinel.md)
compiles it with "one tag per exception, covering mail, prompt, status and heartbeat". The compiled templates
(`internal/listset/compile.go:132-147`) give prompt and status the same owner line and allow and deny tags as mail, and
heartbeat the same on the subscribe side. There is no open prompt or status line. So an adopted account, one whose list
set is in force, polices all four verbs. No record says that 0055's interim rule has ended, and 0102 does not name 0055.
This record states the departure so the maintainer can rule on it.

## Decision Drivers

* A list set is read as "who may reach whom". A sender refused mail to a receiver but still able to prompt it, probe its
  status and read its heartbeat is not fenced in any sense an operator would expect.
* 0055's reason, that the zone layout of prompt and status was undecided (F8), is gone: there are no zones (ADR 0091), and
  prompt, status and heartbeat keep their five-token SAP subjects, which the templates name directly.
* Applying a policy must not break what works. A pair the list set allows keeps its prompt, status and heartbeat.
* The interim rule should not be ended by accident, in code only.

## Considered Options

* For an adopted account, the list set polices all four verbs, as built (proposed).
* Keep 0055 for adopted accounts too: compile open prompt and status lines beside the list set's mail lines.
* Police prompt and status, but leave heartbeat open.

## Decision Outcome

Proposed option: "the list set polices all four verbs", for adopted accounts only.

When a list set is in force, a sender may mail, prompt, probe the status of, and receive the heartbeat of exactly the
receivers the list set gives it for that verb: the owner line when the verb is shared within its owner group, plus its
allow tags, minus its deny tags (ADR 0102). A sender always keeps its own prompt and status subjects and its own
heartbeat (the fixed lines). `mashctl policy show`, `explain` and `diff` report reach per verb, and so do `mash policy
show` and `explain`.

This record supersedes ADR 0055 for adopted accounts. For an account with no list set in force, nothing changes:

* There is no policy to apply. The policy file and its compiler are gone (ADR 0091), so 0055's open zone-key lines are
  not emitted anywhere.
* An endpoint's default grant (`internal/admin/permissions.go:27-36`, `Permissions`/`PermissionsTo`) gives mail, prompt
  and status the same lines, `agents.<verb>.*.<owner>.*`. A new-form endpoint (`internal/admin/permissions_newform.go:89-100`,
  `NewFormPermissions`) gets `agents.<verb>.<agent>.<owner>.*` for the three verbs, plus the heartbeats of its own owner
  and agent token. No verb is policed differently from another.
* So 0055's distinction, mail policed and prompt and status open, has nothing to apply to in an unadopted account
  either. Its file stays as it is, as the history of the zone era.

### Consequences

* Good, because a list set means the same thing for every verb, and `explain` answers one question.
* Good, because the departure is on record and the maintainer rules on it, instead of it standing in code alone.
* Bad, because a pair the list set does not name loses prompt, status and heartbeat as well as mail when an account is
  adopted. An operator who relied on 0055 must add the pair to the list set.
* Neutral, because heartbeat is policed on the subscribe side (who may read it), not on publish: a sender still publishes
  only its own heartbeat.

### Confirmation

* The list-set compiler's golden files and AT-008's dry-run golden (`internal/listset/testdata/dryrun_at008.golden.json`)
  show per-verb reach for prompt, status and heartbeat.
* The checker's simulation (`internal/listset/check.go:137-138, 174`) checks prompt and status reach per pair, and the
  live list-set tests (`internal/listset/at_live_test.go`) send on each verb against a server.
* If the maintainer rules the other way, this record is rejected, and a new record and the compiler change put back open
  prompt and status lines.

## Pros and Cons of the Options

### The list set polices all four verbs (proposed)

* Good, because "who may reach whom" covers every way to reach a peer.
* Good, because it is what ships today, so ruling for it changes no code.
* Bad, because adoption closes prompt and status for pairs the list set does not name.

### Keep 0055 for adopted accounts

* Good, because adoption never closes a prompt or a status probe.
* Bad, because a deny entry does not stop a prompt to the denied receiver, and a reader of the list set would assume it does.
* Bad, because it needs two more template lines per owner group and a compiler change.

### Police prompt and status, leave heartbeat open

* Good, because presence stays visible to everyone in the owner group.
* Bad, because a sender denied a receiver still learns when that receiver is up and busy.
* Bad, because a list set would then mean different things for different verbs.

## More Information

* Supersedes, for adopted accounts only: ADR 0055. Related: ADR 0091 (no zones), ADR 0102 (templates and tags), ADR 0053.
* Contract mash-messaging-policy draft-2 (D206), section 7, and draft-3.
* Status stays `proposed` until the maintainer rules on his review page.
