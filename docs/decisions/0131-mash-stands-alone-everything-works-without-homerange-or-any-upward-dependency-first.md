---
status: proposed
date: 2026-11-15
decision-makers: Randy Bias (mash maintainer)
---

# mash stands alone: everything works without homerange or any upward dependency, first

## Context and Problem Statement

mash is used on its own and, in larger deployments, under a system that runs it for many people. Over the v0.5.0
work, the policy path was built the other way round: the only way to apply a list set was a service that took list
sets from a key-value bucket shared with an outside range manager, under an adoption lock
([ADR 0111](0111-an-adopted-account-takes-its-list-sets-only-from-its-submitter-and-mashctl-refuses-every-other-policy-change.md)).
The engine under it (compile, check, size, generations, the applied gate, rotation, revocation, the issue record) is
generic, but a solo developer with one NATS server and `mashctl` had no way to apply a list set at all. The maintainer's rule (2026-11-15), verbatim:

> "ALL functionality works first for mash in isolation WITHOUT homerange or any other upwards dependency. You MUST NOT
> implement functionality that has been designed for homerange without first having the functionality work without
> homerange."

Restated:

* Everything works for mash in isolation, without homerange or any other upward dependency, before anything is
  implemented that was designed for homerange.
* homerange knows mash and wraps `mashctl`; mash does not know homerange.
* A solo developer runs everything locally with a human `mashctl`. A large deployment drives `mashctl` with JSON in and
  out. `mashctl` is not assumed to run on the same host as mash.
* A per-group agent on each host is the wrong place for account-wide policy.

This is a standing decision, so it is recorded.

## Decision Drivers

* A feature that only an outside system can use is not a feature of mash.
* mash must be complete and tested without the thing that wraps it.
* An outside system must be able to use mash through its public surface (`mashctl`: for a person, and with JSON in and
  out for a program), with no special case in mash.
* No release depends on a component that is not in this repository.

## Considered Options

For v0.5.0:

* (a) Ship the policy commands as built, with the seam as the only driver (rejected: it fails the rule).
* (b) Take the policy commands out of v0.5.0 and keep the engine internal until it works standalone (chosen by the
  maintainer; [ADR 0129](0129-the-policy-commands-are-not-part-of-v0-5-0-and-the-engine-stays-internal-until-it-works-standalone.md)).

For the policy design:

* Keep the seam as the only driver and document it (rejected: it fails the rule).
* Standalone first: a direct driver for a person and for a program wrapping `mashctl`, and every later feature passes
  the standalone test first (chosen).

## Decision Outcome

Chosen: for v0.5.0, option (b); for the policy design, standalone first.

1. **No upward dependency.** mash does not read, name or depend on homerange, a wrangler or any other system above it. A name of such a system in mash's code or in a permission decision is a defect. (This restates the
   boundary of the messaging-policy contract: mash does not know the roles an outside system gives its members.)
2. **Standalone first.** A feature whose purpose is an outside system's use is implemented only after the same
   capability works with nothing above mash: one NATS server, a human at `mashctl` holding the operator keys, applying
   directly. A pull request that adds such a feature says in its body how the standalone path does the same thing, or
   links the pull request that does.
3. **The public surface is `mashctl`.** A person uses it directly; a program, including an outside system, wraps it with
   JSON in and JSON out (every verb that a program needs takes JSON input and gives JSON output). mash adds no code
   path that only one outside system can use. Whether a policy document is signed, and whether any other channel into
   mash exists, is open for discussion and not decided here.
4. **`mashctl` is not assumed to be co-resident with mash.** It reaches the server over the network with the
   credentials it holds.
5. **Outside-system paths are not carried forward.** Adoption, the signed list-set intake, `policy serve`, the issue
   service and the requester registry do not ship (ADR 0129), and none returns unless a standalone use needs it and it
   passes rule 2. The code may stay in the tree until the next release's work removes or reuses it. The first-apply
   reconcile and the rotation interface stay only as parts of the standalone engine.

### What follows for v0.5.0

[ADR 0129](0129-the-policy-commands-are-not-part-of-v0-5-0-and-the-engine-stays-internal-until-it-works-standalone.md)
takes the policy commands out of v0.5.0: the engine stays internal until it works standalone. The standalone path is
the next release's work: a direct driver that runs the existing engine with the keys `mashctl` holds, a ledger bucket
that mash creates, the first-apply reconcile available to any account, and `show`, `explain` and `diff` reading mash's
own ledger. Its verbs, its default for a first policy and the set of endpoints a policy covers are for the maintainer
to decide; nothing here presumes them.

### Consequences

* Good, because a solo developer can use the policy engine.
* Good, because an outside system and a person use the same engine through the same verbs, so what one tests, the other
  gets.
* Bad, because code for adoption, the intake and the issue service stays in the tree, unshipped, until it is removed or
  reused.
* Neutral, because the engine, its tests and its release-gate rows (listset and policyapply acceptance tests) are
  unchanged.

### Confirmation

* A review check on every pull request that adds an outside-system feature: the standalone path is named.
* A code search for the words of such systems in the permission path stays part of the policy
  tests (contract BND-001 inspection).
* The standalone flow has a live test over a two-server cluster: apply a list set from a file with no bucket, no marker
  and no service running, and see the gate pass.

## Pros and Cons of the Options

### (a) Ship the policy commands as built

* Good, because it is built and tested.
* Bad, because it fails the rule, and a person cannot use it.

### (b) Take the policy commands out of v0.5.0 (chosen for v0.5.0)

* Good, because the release obeys the rule and nothing unshipped is promised.
* Bad, because the engine has no command until the standalone work lands.

### Standalone first (chosen for the policy design)

* Good, because a person and a program meet the same engine.
* Bad, because the standalone driver is new work with its own live proof.

## More Information

The assessment that sizes the work is a scratchpad document (standalone-inventory). Related: ADR 0111 (adoption),
ADR 0104 (issue service), ADR 0112 (intake), ADR 0121 (reconcile), ADR 0129 (the v0.5.0 decision). The messaging-policy contract (draft-3, under
revision) carries the boundary BND-001 and the two requirements written for an outside system, REQ-013 and REQ-016,
which this decision does not carry forward.
