---
status: accepted
date: 2026-11-02
decision-makers: Randy Bias (mash maintainer)
---

# Until the maintainer rules on prompt and status, only mail is policed; they stay open on their five-token subjects

## Context and Problem Statement

[ADR 0053](0053-who-may-mail-whom-is-a-policy-file-compiled-by-mashctl-into-nats-permissions.md) says one rule covers
every subject an endpoint may address to a peer: mail, live prompt and status probe. It also leaves the prompt and status
subjects as they are until the maintainer rules ("F8" in the policy design document: whether prompt and status follow
the zone layout of the mail subject, `agents.mail.cc.<owner>.<zone>.<name>`, or stay as SAP defines them). Running
endpoints publish, subscribe and answer on the five-token forms, `agents.prompt.cc.<owner>.<name>` and
`agents.status.cc.<owner>.<name>`
([ADR 0054](0054-mail-subjects-carry-a-zone-token-legacy-subjects-stay-valid.md)). A compiler that emits six-token
prompt and status lines under the zone keys therefore breaks live prompts and status probes for every user minted under
them, because the permission lines name subjects no endpoint uses. Both records cannot hold at once.

## Decision Drivers

* Applying a policy must not break a feature that works today.
* The decision between the layouts is the maintainer's (F8), not ours, and is still open.
* Keep what a client cannot talk around: mail is what the policy exists to restrict.
* Do not let the interim choice become the answer by accident.

## Considered Options

* Compile prompt and status with the zone token now (ADR 0053 as written): the permission lines name subjects no endpoint
  uses yet.
* Compile no prompt or status permission under the zone keys: live prompts and probes stop working.
* Until F8 is ruled, the zone keys carry two open fixed lines for prompt and status on the five-token subjects, and only
  mail is zone-scoped.

## Decision Outcome

Chosen option: until F8 is ruled, only mail is policed. Each zone key carries two fixed lines,
`agents.prompt.*.<owner>.*` and `agents.status.*.<owner>.*`, so every endpoint of the owner may prompt and probe every
other, as it can today. The zone wildcards and the allow and deny tag templates apply to the mail subject only. An
endpoint's own prompt and status subscriptions are the five-token forms. A policy rule still reads as one rule per
sender-recipient pair, and `explain` and `show` say plainly that prompt and status are not covered by it yet.

This record amends ADR 0053 for prompt and status only; its file is unchanged. It is interim by design: when the
maintainer rules on F8, a later record either brings prompt and status under the zone layout (and the compiler emits the
zoned forms together with a subject change like ADR 0054's, so no endpoint is left unreachable) or keeps this behaviour
as the answer.

### Consequences

* Good: applying a policy breaks nothing that works; mail is restricted exactly as ADR 0053 describes.
* Bad: a policy that denies A mailing B does not stop A from prompting or probing B, and a reader of the file may assume
  it does. `check`, `explain` and `show` say so.
* Bad: the account JWT carries two more fixed lines per zone key, which the size budget counts.
* The open question stays named: F8 is the maintainer's call.

### Confirmation

* The policy package's live test has each zone user mail with the six-token subject as compiled, and prompt and
  status-probe every peer on the five-token subjects, all allowed.
* Golden files of the compiled zone keys show the two open lines and mail-only wildcards and templates.
* `mashctl policy apply`'s live test (in a later change) repeats the prompt and status checks against a pushed account.

## Pros and Cons of the Options

### Zone token on prompt and status now

* Good, because one layout for all three verbs.
* Bad, because no endpoint uses those subjects, so live prompts and probes break on apply.

### No prompt or status permission

* Good, because nothing is open.
* Bad, because live prompts and status probes stop working.

### Only mail is policed until F8

* Good, because nothing that works today breaks, and the maintainer's decision stays open.
* Bad, because a deny rule does not cover prompt and status.

## More Information

* amends: [0053](0053-who-may-mail-whom-is-a-policy-file-compiled-by-mashctl-into-nats-permissions.md) (prompt and status
  only; its file is unchanged).
* Related: [0054](0054-mail-subjects-carry-a-zone-token-legacy-subjects-stay-valid.md).
* Raised by the review of the `mashctl policy apply` change; implemented by the policy compiler change that makes only
  mail zone-scoped.
