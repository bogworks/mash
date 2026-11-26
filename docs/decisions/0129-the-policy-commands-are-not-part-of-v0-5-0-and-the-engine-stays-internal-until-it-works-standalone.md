---
status: proposed
date: 2026-11-15
decision-makers: Randy Bias (mash maintainer)
---

# The policy commands are not part of v0.5.0 and the engine stays internal until it works standalone

Amends: ADR-0104, ADR-0110, ADR-0111

This amends the command-line parts of those three decisions for v0.5.0. Their design text is unchanged.

## Context and Problem Statement

The v0.5.0 branch carried a policy command line. It was built for an outside owner of the account's policy: `mashctl policy adopt` registers one submitter key, `mashctl policy serve` takes a signed document from that submitter and applies it, `mashctl policy reconcile` revokes credentials the document does not name, `mashctl issue serve` and `mashctl wrangler` sign credentials for a wrangler's request, and `mash policy show` and `mash policy explain` answer an endpoint about its own reach. Once an account is adopted, `mashctl add`, `reissue`, `retire` and `label` refuse, and nothing undoes it.

The maintainer's rule is that mash works alone; a layer above may wrap `mashctl` through its documented interface. A feature designed for a layer above mash is built only once the same capability works with nothing above mash ([ADR 0131](0131-mash-stands-alone-everything-works-without-homerange-or-any-upward-dependency-first.md)).

An audit of the branch found that the engine (the list-set compile, the apply gate, generations and the explain library) is driven by one path only: the outside-owner path above. Nothing lets an operator who runs mash alone set or read a policy. The commands are therefore functionality designed for an outside system, shipped before a standalone form exists.

The maintainer ruled that v0.5.0 is zone removal and hardening, and that standalone policy follows in 0.5.5. What happens to the policy command line in v0.5.0?

## Decision Drivers

* The rule above: mash must work alone first.
* An adoption has no undo. A command that takes an account away from the operator should not ship before the operator has a way to use the account's policy.
* Zone removal and hardening are finished and tested, and do not depend on the policy commands.
* The engine and its tests are sound and are needed for the work that follows. Deleting them would throw that work away.
* A release should not ship a surface it expects to change.

## Considered Options

* Hide the commands behind a flag or an environment setting.
* Delete the command wiring and keep the engine (chosen).
* Ship the commands as they are.

## Decision Outcome

Chosen option: "Delete the command wiring and keep the engine".

**Removed from the v0.5.0 surface.**

* `mashctl policy` and all its verbs: `adopt`, `serve`, `reconcile`, `show`, `explain`, `diff`.
* `mashctl issue serve` and `mashctl wrangler`.
* `mash policy show` and `mash policy explain`.
* The refusals that `add`, `reissue`, `retire`, `label` and the other guarded commands gave on an adopted account, and the account lock that serialised them. Nothing can set an adoption marker in this release, so no account is adopted and the refusals have nothing to guard.

**Stays.**

* The internal packages: the list-set engine, the apply gate, generations, the issue record and the explain library, with their tests. No command calls them.
* The release-gate engine tests, listed in `scripts/releasecheck/policytests.txt`. The gate still runs them.
* `mashctl issued list` and `mashctl issued check`.
* `mashctl adopt`, the first-time takeover of an existing operator-mode server. It is a different thing from adopting an account's policy.
* Zone removal, `mashctl unzone`, and every hardening change: the issue record, revocation checks, `MASH_REQUIRE_TLS`, `MASH_SPOOL_REQUIRED`, `MASH_PLUGIN_REQUIRE_VERIFIED` and `mashctl inbox inspect`.

**Docs.** [docs/policy-enforcement.md](../policy-enforcement.md) stays, because decision records link to it, and carries a note that the commands it describes are not shipped. The other docs drop the commands.

**What follows.** A command line for the engine that works for an operator running mash alone follows in 0.5.5. This decision says nothing more about that release.

### Consequences

* Good, because v0.5.0 holds only work that functions without an outside system.
* Good, because no account can be put into a state that has no undo.
* Good, because the engine and its tests are kept and the gate still guards them.
* Good, because the changelog and the docs describe what ships.
* Bad, because the engine has no driver in this release, so its code is untested by any command-level test and can drift from its callers until 0.5.5.
* Bad, because ADRs 0104, 0110 and 0111 describe commands that do not exist in this release, and a reader must follow this decision to see that.
* Neutral, because the issue record, `issued` and the revocation checks do not depend on the removed commands and are unchanged.

### Confirmation

* `mashctl policy`, `mashctl issue`, `mashctl wrangler`, `mash policy show` and `mash policy explain` answer `unknown command`.
* `mashctl add`, `reissue`, `retire` and `label` do not read an adoption marker.
* The engine tests still run in the gate: the step `policy-tests` runs the rows of `scripts/releasecheck/policytests.txt`, and `go test ./scripts/releasecheck` checks that the list is intact.
* `scripts/changelog check-notes` passes for the 0.5.0 notes, which name none of the removed commands.

## Pros and Cons of the Options

### Hide the commands behind a flag or an environment setting

* Good, because the code stays wired and tested through its commands.
* Bad, because the commands still ship, and a flag does not change what a mistaken `adopt` does.
* Bad, because a hidden surface is still a surface that someone depends on.

### Delete the command wiring and keep the engine (chosen)

* Good, because it removes the surface and keeps the work behind it.
* Good, because it meets the rule: nothing built for an outside system ships first.
* Bad, because the engine runs under tests only until a command line returns.

### Ship the commands as they are

* Good, because no work is removed.
* Bad, because it ships functionality built for an outside owner before it works alone, which the rule forbids.
* Bad, because an adoption cannot be undone and the known limits would ship with it.

## More Information

* [ADR 0104](0104-an-issue-service-signs-a-credential-for-a-public-key-a-wrangler-countersigned.md): the issue service and the wrangler registry. Their commands are not in v0.5.0.
* [ADR 0110](0110-a-sender-sees-only-its-own-reach-through-a-signed-policy-view-request.md): the signed policy view request. `mash policy show` and `mash policy explain` are not in v0.5.0.
* [ADR 0111](0111-an-adopted-account-takes-its-list-sets-only-from-its-submitter-and-mashctl-refuses-every-other-policy-change.md): adoption of an account's policy and the refusals it causes. Neither is in v0.5.0.
* [ADR 0091](0091-there-are-no-zones-mail-keeps-the-five-token-subject-and-the-address-is-the-fence.md): zone removal, which v0.5.0 ships.
* [docs/policy-enforcement.md](../policy-enforcement.md): the design of the engine.
* [docs/release.md](../release.md#policy-tests-on-every-release): the gate step that runs the engine tests.
