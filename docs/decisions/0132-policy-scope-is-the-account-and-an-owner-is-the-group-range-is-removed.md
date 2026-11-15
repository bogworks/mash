---
status: proposed
date: 2026-11-15
decision-makers: Randy Bias (mash maintainer)
---

# Policy scope is the account and an owner is the group: range is removed

Amends: ADR-0102

## Context and Problem Statement

A list set today carries a `range`. It is the first part of a three-part address (`range.owner.name`), the first part of
an owner group (`range.owner`), the third token of an address subject
(`agents.<verb>.<range>.<owner>.<name>`), a field of the list set document, a placeholder in the policy bucket name, a
part of the scoped signing key's file name (`groups/<range>.<owner>.nk`) and an argument of every `mashctl policy`
verb. [ADR 0102](0102-list-set-permissions-are-one-template-per-owner-group-with-allow-and-deny-tags-and-a-deny-sentinel.md)
fixed it as a literal in the template, so that no tag could name an address outside the account's range.

It carries nothing the account does not. A list set is for one NATS account, the range is constant for the whole
account, and the permission already comes from the account's own scoped signing keys. The range made the grammar longer
(every tag, every size figure, every command), forced a rule for accounts that hold more than one range (the signer
keeps "the keys of other ranges' groups", ADR 0122 and ADR 0124 carry the same exception), and gave the same constant a
second name in three places: the bucket, the key store and the subject. The maintainer ruled that the word and the
concept leave mash.

Removing it from policy is mechanical. The third subject token is harder. It is also part of the identity of every
new-form endpoint (`agent_owner_name`, the flat key of the inbox stream and consumer, the lease key and the reply
prefix), and the SAP agent token of the endpoint's program lives there. What does a list set do with that token, and how
does a template still name a sender's own inbox, lease and replies when the token is no longer a constant?

## Decision Drivers

* Policy scope is already the account; a second scope inside it has no reader.
* A list set must never name the agent token: it is a fact about a program, chosen per endpoint, and no policy decision.
* A sender must reach only its own inbox, lease and reply names (REQ-001). The names embed the flat key as one subject
  token, and NATS cannot wildcard part of a token, so some exact value has to reach the template from outside the list
  set.
* That value must not come from the sender or from a request: a sender that could choose it could read another
  endpoint's inbox.
* Fail closed: a credential missing that value must lose access, never gain it.
* Nothing that carries a range was deployed, so there is nothing to migrate.
* The identity of an endpoint (the flat key, the inbox names, the directory) is out of scope: a larger change that the
  maintainer decides separately.

## Considered Options

* Remove range; the scope is the account, the group is the owner, the address is `owner.name`; the agent token is a
  wildcard in subjects; the issuer supplies an `ep` tag with the flat key for the names that embed it.
* A name claim in the user JWT that carries the flat key.
* Inbox, lease and reply names without the agent token.
* Keep range.
* Drop the agent token from endpoint identities.

## Decision Outcome

Chosen option: the first. The pieces:

* **Scope, group and address.** A list set is for the NATS account. An owner group is the owner. An address is
  `owner.name`, so a tag is `allow:<owner>.<name>` or `deny:<owner>.<name>` (as before: the tag never held the range).
  One scoped signing key per owner, kept as `groups/<owner>.nk`. The `range` field of the list set document is gone: a
  document with a `range` member is refused as an unknown field. The policy bucket has no `{range}` placeholder; it is
  `mash-policy` unless the optional `MASH_POLICY_BUCKET` setting names another. Every `--range` flag and argument of
  `mashctl policy` is gone. The signer's tolerance for "keys of other ranges" (`KeptSigners`) is removed: a multi-range
  account no longer exists.
* **Subjects of mash's own services.** `mash.policy` (the policy responder) and `mash.issue.<owner>` (the issue service;
  it listens on `mash.issue.*`). Neither begins with `agents`, so no address subject and no template line reaches them
  except the one `mash.policy` publish line every template carries (ADR 0110). One responder serves the account.
* **The agent token is wildcarded in subjects.** Every template line that names an address subject has `*` where the
  token was: `agents.<verb>.*.<owner>.<name>`, with the owner line `agents.<verb>.*.<owner>.*`. This is sound because
  the token identifies a program, not an authority: the owner and name still pin the receiver, and the lists decide
  which owner and name. The deny sentinel expands to `agents.<verb>.*._none`, four tokens, which no five-token address
  matches.
* **The `ep` tag.** The inbox stream and consumer (`inbox-<flat>`), the lease key and the reply prefix
  (`_INBOX.<flat>`) embed the flat key (`agent_owner_name`) as one subject token. NATS cannot wildcard part of a
  token, so the template needs the exact key, and it takes it from a tag, `ep:<flat key>`, with `{{tag(ep)}}`. The
  rules:
  * (a) A list set and `listset.Compiled.Users` never name the agent token or the `ep` tag.
  * (b) The tag comes from mash's own table of the endpoint's identity at issue time, through one shared function
    (`listset.WithEndpointTag`, on `EndpointTag`). The issue service, rotation and the probe credentials all call it.
  * (c) The issuer refuses to sign when the endpoint's owner and name differ from the address's, and refuses a legacy
    identity. A request never supplies tags. The issue request now names the address as `owner.name` and the endpoint
    as `agent.owner.name`; both sit inside the wrangler's proof (the proof prefix is `mash-issue-v2`), so neither can be
    changed after it is signed. The issuer builds the `ep` tag from the endpoint and answers `endpoint_mismatch` before
    it reserves a nonce, signs or records anything. The template trusts the tag (a live test shows a credential that
    carries another endpoint's tag gets that endpoint's inbox lines), so this refusal, and the custody of the group
    key, are what protect an inbox.
  * (d) `listset.Check` models the `ep` tag as issuer-supplied and expands each sender with two different agent tokens,
    proving that the inbox, lease and reply lines match only that sender's own flat key.
  * (e) A credential without an `ep` tag gets no inbox, lease or reply access: the server drops the lines whose tag is
    missing. That is fail closed.
  * (f) The self-subject lines (a sender's own heartbeat, prompt and status) wildcard the token, so a sender could
    publish its own heartbeat under another agent token. This is accepted: it is still the sender's own owner and
    name, and a heartbeat carries no more authority than the sender already has.
* **Digests.** The list set and compiled digests no longer encode a range, and their domain tags move to
  `mash.listset.v2` and `mash.compiled.v2`, so no old digest can be taken for a new one.
* **No migration.** Nothing that carries a range was deployed, so there is no reading of the old forms: a document with
  a `range` member, a `groups/<range>.<owner>.nk` key and a `{range}` bucket name are not read.
* **The engine acceptance tests are re-derived.** AT-001, AT-003, AT-004, AT-005 and AT-020 are run against the new
  templates and their expected sets read again, not only renamed: the `ep` lines are now part of what each one
  compares, and the tests that existed only because of a second range (another range, a range part refused, a signer
  kept for another range) are removed.

### Consequences

* Good, because every address, tag, command and size figure is one part shorter, and the account has one name for its
  scope.
* Good, because the account no longer needs a rule for other ranges, in the signer or in the gate.
* Good, because a sender's inbox, lease and reply access depends on a value the issuer alone writes.
* Bad, because the server's template expansion limit now counts the `ep` lines: a key's publish list holds 3N + the
  shared verbs + 16 fixed lines, so 1359 allow tags fit where 1365 did.
* Bad, because the user JWT carries one more tag. `listset.SizeCheck` measures it with a 32-character agent token, and
  the size tables in `docs/policy-enforcement.md` section 6.2 are measured again: at an owner part of 4 and names of
  14 characters, 81 exceptions fit (83 before).
* Bad, because a holder of a group's scoped signing key can sign any `ep` tag, and so point a credential at another
  endpoint's inbox. That holder could already sign any tag, so the custody rules of the group key are unchanged
  (threat model, owner group signing keys). What such a holder can do, stated plainly:
  * mint a user JWT for any key it holds with any tag set of that group: any allow or deny tag, so any reach the list
    set gives any sender of that owner group, and any `ep` tag, so the inbox, lease and reply lines of any endpoint
    of any agent token, in that owner group or another (the template's inbox lines are keyed on the tag, not on the
    group);
  * read, fetch from, acknowledge and delete mail in the inbox of the endpoint it names, and take over that endpoint's
    lease key and reply subjects;
  * not widen what the template itself grants (the template is in the account JWT, signed by the account key), not
    sign for another owner group's key, and not change the list set or its generation.
  The rest of the account is unaffected, and the same holder could already mint any tag set, so what the `ep` tag adds
  is the inbox reach of other owner groups' endpoints. It is the same trust the account key already carries, which
  holds all of this and more. Mitigations are unchanged: the group keys stay with the minting authority, are never
  on a sender's host, and a revoked key is refused by every server (ADR 0100, 0115). The issue record lists every
  credential signed through mash; a credential signed outside mash is found by the reconcile (ADR 0121).
* Bad, because the digest encoding changes: a stored `mash.listset.v1` digest no longer matches, which is acceptable
  because none was deployed.
* Neutral: the identity of an endpoint is unchanged.

### Confirmation

* `listset.Check` runs on every compile and refuses any template whose inbox, lease or reply line can match another
  sender's flat key, or whose credential without an `ep` tag keeps any of them.
* `TestLiveMatchesCheck` requires a real server's outcomes to equal the simulation, including the `ep` expansion.
* The engine acceptance tests AT-001, AT-003, AT-004, AT-005 and AT-020, listed in `scripts/releasecheck/policytests.txt`,
  pass and block a release when they fail.
* The known-answer digest tests carry the `v2` tags; `policydoc` refuses a document with a `range` member.
* `go test ./internal/portable/` and `adrlint docs` pass on the change.

## Pros and Cons of the Options

### Remove range; wildcard the token; the issuer supplies the `ep` tag

* Good, because the list set says only what policy decides.
* Good, because the one exact value the template needs comes from mash's own table and cannot be requested.
* Bad, because the issuer, rotation and probes must all go through the shared function, and a new issuer that forgets it
  produces a credential with no inbox (fail closed, so visible at once).

### A name claim that carries the flat key

* Good, because there is no new tag.
* Bad, because `{{name()}}` already expands to the name part in the heartbeat, prompt and status lines. Making it carry
  the flat key would change every one of those lines and the meaning of the claim.

### Inbox names without the agent token

* Good, because the template would need no `ep` tag.
* Bad, because it changes the inbox stream names, the lease keys and the directory: an identity change.

### Keep range

* Good, because nothing changes.
* Bad, because the maintainer ruled it out, and the constant keeps costing a part in every name and a rule for accounts
  that do not exist.

### Drop the agent token from endpoint identities

* Good, because the address would be `owner.name` everywhere and no `ep` tag would be needed.
* Bad, because it changes subjects, inbox stream names, credentials and the directory, and breaks every existing
  new-form endpoint. It is the larger identity change, left to the maintainer.

## More Information

Amends [ADR 0102](0102-list-set-permissions-are-one-template-per-owner-group-with-allow-and-deny-tags-and-a-deny-sentinel.md):
the template layout, the owner group's name and the address's parts change as above; the allow and deny tags and the
sentinel stand. Where [ADR 0098](0098-a-list-set-has-a-generation-and-three-sha-256-digests-and-is-refused-unless-its-base-is-in-force.md),
[ADR 0104](0104-an-issue-service-signs-a-credential-for-a-public-key-a-wrangler-countersigned.md),
[ADR 0110](0110-a-sender-sees-only-its-own-reach-through-a-signed-policy-view-request.md),
[ADR 0122](0122-applying-a-list-set-checks-that-an-added-address-has-its-inbox-and-makes-nothing.md) and
[ADR 0124](0124-the-reconciles-yes-acts-only-on-the-plan-the-person-read-and-an-adopted-accounts-first-apply-drops-the-signing-keys-mash-did-not-make.md)
say range, `range.owner`, `agents.policy.<range>`, `agents.issue.<range>.<owner>` or keys of other ranges, this record
replaces the word and the subject; their other decisions stand and their text is unchanged.
