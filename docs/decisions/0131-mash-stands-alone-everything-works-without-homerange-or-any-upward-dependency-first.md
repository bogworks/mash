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
generic, but a solo developer with one NATS server and `mashctl` had no way to apply a list set at all. The maintainer's
rule (2026-11-15):

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
* An outside system must be able to use mash through its public surface (`mashctl` verbs with JSON input and output,
  and the signed submission channel), with no special case in mash.
* No release depends on a component that is not in this repository.

## Considered Options

* Keep the seam as the only driver and document it (rejected: it fails the rule).
* Remove the outside-system paths from v0.5.0 and ship the engine alone (rejected: the engine would have no way to be
  applied).
* Build the standalone path first, keep the outside-system paths as optional integration surfaces over the same engine,
  and require every future feature to pass the standalone test first (chosen).

## Decision Outcome

Chosen option: "standalone first; the outside-system paths are optional integration surfaces".

1. **No upward dependency.** mash does not read, name or depend on homerange, a wrangler or any other system above it. A name of such a system in mash's code or in a permission decision is a defect. (This restates the
   boundary of the messaging-policy contract: mash does not know the roles an outside system gives its members.)
2. **Standalone first.** A feature whose purpose is an outside system's use is implemented only after the same
   capability works with nothing above mash: one NATS server, a human at `mashctl` holding the operator keys, applying
   directly. A pull request that adds such a feature says in its body how the standalone path does the same thing, or
   links the pull request that does.
3. **The public surface is `mashctl` and the submission channel.** An outside system wraps `mashctl` (every verb that
   an outside system needs takes JSON input and gives JSON output) and, where it cannot run `mashctl`, writes a signed
   document to the submission bucket that mash owns. mash adds no code path that only one outside system can use.
4. **`mashctl` is not assumed to be co-resident with mash.** It reaches the server over the network with the
   credentials it holds.
5. **The existing outside-system paths are reclassified, not deleted.** The adoption marker and guard, the list-set
   intake and `policy serve`, the first-apply reconcile, the issue service and its requester registry, and the rotation
   delivery interface stay as optional integration surfaces, named without such systems' words in the interface (managed account,
   requester) and documented after the standalone flow. None of them is required by the standalone flow.

### What follows for v0.5.0

[ADR 0129](0129-the-policy-commands-are-not-part-of-v0-5-0-and-the-engine-stays-internal-until-it-works-standalone.md)
takes the policy commands out of v0.5.0: the engine stays internal until it works standalone. The standalone path is
the next release's work: a direct driver that runs the existing engine with the keys `mashctl` holds, a ledger bucket
that mash creates, the first-apply reconcile available to any account, and `show`, `explain` and `diff` reading mash's
own ledger. Its verbs, its default for a first policy and the set of endpoints a policy covers are for the maintainer
to decide; nothing here presumes them.

### Consequences

* Good, because a solo developer can use the policy engine.
* Good, because an outside system and a human use the same engine through the same verbs, so what one tests, the other
  gets.
* Bad, because the release is delayed and some documentation and interface names change (adoption, wrangler).
* Neutral, because the engine, its tests and its release-gate rows (listset and policyapply acceptance tests) are
  unchanged.

### Confirmation

* A review check on every pull request that adds an outside-system feature: the standalone path is named.
* A code search for the words of such systems in the permission path stays part of the policy
  tests (contract BND-001 inspection).
* The standalone flow has a live test over a two-server cluster: apply a list set from a file with no bucket, no marker
  and no service running, and see the gate pass.

## Pros and Cons of the Options

### Keep the seam as the only driver

* Good, because it is built and tested.
* Bad, because it fails the rule, and a person cannot use it.

### Remove the outside-system paths from v0.5.0

* Good, because it is the smallest release that obeys the rule.
* Bad, because without the standalone driver there is nothing to apply policy with, so the release is not useful; and
  it discards work that is correct as an optional surface.

## More Information

The assessment that sizes the work is a scratchpad document (standalone-inventory). Related: ADR 0111 (adoption),
ADR 0104 (issue service), ADR 0112 (intake), ADR 0121 (reconcile). The messaging-policy contract (draft-3, under
revision) carries the boundary BND-001 and the two homerange-shaped requirements REQ-013 and REQ-016, which this
decision reclassifies as optional.
