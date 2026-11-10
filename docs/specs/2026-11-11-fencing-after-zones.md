# What fences mail once the zone token leaves the subject

**Status:** proposal, for the maintainer's ruling. `Decision: none`: this document proposes, it decides nothing and
changes no code. The six zone command-line items stay frozen until it is ruled on.

**Date:** 2026-11-11. **Facts as of:** `main` at `6dbb9ca`.

## Summary for the maintainer

- **Was SAP broken, and quietly?** Only for mail subjects. The six-token mail subject is outside SAP 0.3 section 2's
  five-token shape. It was recorded the day it landed (ADR 0052, line 72, as a "Bad" consequence) but never escalated
  as a departure; SAP's own verbs (prompt, status, heartbeat) stayed five-token.
- **Broken as conceived or as built?** As built, no defect found (tests pass; the compiler is exact). As conceived, yes
  in one respect: the zone makes a policy attribute part of an address, which the three-token naming contract forbids.
- **What enforces:** the permissions in each endpoint's NATS user JWT, which the server applies. **What labels do:**
  select. Only `mashctl`, at compile time, reads a label; the server never does.
- **"ACL"** is not a mash concept; the nearest thing is the publish permission list in the user JWT.
- **Live today:** per x-lead, 0 zoned records of 68, no policy file deployed; removing the zone costs nothing to migrate.
- **Recommendation:** A now (remove the zone addressing). Add F1, a key per owner with bounded deny and cross-owner
  exceptions as tags, when a within-owner requirement appears. F2, one key with agent and owner as signed tags, only if
  the measured owner count or account-JWT size forces it, after its live test and an accepted ruling on its wider
  signing authority. F1 gives up `default: deny` inside an owner and broad label-based allows.

## 1. Who this is for, and the vocabulary

You have not read the mash code. This section defines every term the rest uses, with a small example each.

- **Endpoint.** One mash identity: a named mailbox that a session (or tool) connects as. Example: `build-bot`.
- **Subject.** NATS routes every message by a dot-separated name called a subject. Example:
  `agents.mail.cc.acme.build-bot`. Each dot-separated piece is a **token** (so that subject has five: `agents`, `mail`,
  `cc`, `acme`, `build-bot`). In `agents.<verb>.<agent>.<owner>.<name>` (SAP's shape, section 2), the tokens after
  `agents` are the verb (`mail`, `prompt`, `status`, `hb`), the **agent** token (`cc`), the **owner** and the **name**.
- **Wildcard.** In a subject pattern `*` stands for exactly one token and `>` for "one or more tokens, to the end".
  `agents.mail.*.acme.*` matches mail to every endpoint of owner `acme`.
- **SAP.** The Synadia Agent Protocol, version 0.3.0, the open protocol whose subject shape mash adopted.
- **NATS user JWT.** A signed token the NATS server reads when an endpoint connects. It names the user and lists its
  **publish permissions** (subjects it may publish to) and **subscribe permissions** (subjects it may receive from),
  each as allow and deny lists of subject patterns. The server enforces them; the client cannot talk around them.
  Example: publish allow `agents.mail.*.acme.*` lets that user mail any `acme` endpoint.
- **Account, signing key.** An account is NATS's tenancy unit; mash runs one shared account. The **account signing key**
  signs user JWTs. A **scoped signing key** is a signing key that carries a permission set: every user signed by it gets
  exactly that set (and cannot be given more by the user's own JWT). The permission set may hold templates:
  `{{name()}}` becomes the user's name and `{{tag(allow)}}` becomes one line per `allow:` tag on the user.
- **Tag.** A short `key:value` string stored in a user's JWT, lower-cased by the JWT library. With a scoped key, a tag
  is a permission: `allow:sales.hr-agent` expands, through `agents.mail.*.acme.{{tag(allow)}}`, to a publish allow line.
- **Label.** A `key=value` string on an endpoint's directory record (the shared registry mash keeps of endpoints), up to
  64 per record, set by the administrator, never by the endpoint about itself. Example: `team=billing`.
- **Zone.** The one label, named by `subject_label` (default `zone`), whose value mash puts into the mail subject as an
  extra token. Example: zone `billing` gives `agents.mail.cc.acme.billing.build-bot`. An endpoint is in exactly one zone.
- **Policy file.** A YAML file the administrator writes: `default: allow|deny`, endpoints with labels, and rules (`allow`
  or `deny`, a `from` selector and a `to` selector that match labels), plus tests. `mashctl` (the operator tool)
  compiles it into the scoped keys and tags above. Example rule: deny `from {team: contractors}` `to {team: finance}`.
- **Fence.** What this document asks about: the mechanism that stops endpoint A publishing mail to endpoint B when the
  administrator has said it may not, in a way A cannot bypass.

## 2. Findings (answering the three questions)

### 2.1 How the zone got into the subject

The first design fixed mail as SAP's five tokens: ADR 0005 (accepted 2026-10-21, recorded 2026-10-31) chose
`agents.<verb>.cc.<owner>.<name>` and named "Pure SAP" a driver (`docs/decisions/0005-subjects-follow-sap-hierarchy.md`;
design goal 5, `docs/specs/2026-10-21-mash-design.md:23`).

On 2026-11-01 and 2026-11-02 the maintainer ruled that mash gains an operator tool that mints users and compiles "who
may mail whom" into permissions (ADR 0052, ADR 0053). The constraint that forced structure into the subject is size:
spelling out each recipient in each sender's permissions was measured to exhaust the account JWT's 1 MiB ceiling at 87
endpoints (ADR 0052, "Measured 2026-11-02"; ADR 0053, Context). A wildcard is the compact way to allow many
recipients; the existing agent and owner tokens already give compact grants per owner, so the zone token was added for
groups defined by a label *within* an owner, which needed a token of their own to stand on. ADR 0053 therefore put one label, the zone, into the mail subject:
`agents.mail.cc.<owner>.<zone>.<name>` (ADR 0053, Decision Outcome). ADR 0054 made it backward compatible: an endpoint
with no zone keeps the five-token subject, a zoned inbox stream takes both the legacy subject and its own zone's
subject, and the permission line comes in two forms (`agents.mail.*.<owner>.*` and `agents.mail.*.<owner>.*.*`).

The code landed on 2026-11-02: ADRs 0052 and 0053 with the `mashctl` skeleton (PR #117, `bfa98e3`); the zone subject
and ADR 0054 (PR #123, `4e6810e`, merged as `acf6694`); zone-wildcard compilation (PR #119, `d2fa678`, merged `900288e`);
mail-only policing and ADR 0055 (PR #142, `0abf634`); the placeholder tag and ADR 0056 (PR #148, `8a44020`).

The mash design said the cost out loud the same day: "the mail subject gains a zone token (ADR 0053), which SAP tools
would not expect" (ADR 0052, line 72; repeated at ADR 0053, line 77).

**Does it depart from SAP 0.3? Yes, for the mail subject.** SAP 0.3 section 2 (`core-protocol.md`, commit `c48cea5f`)
says: "Every agent instance occupies a subject tree of the form: `agents.{verb}.{agent}.{owner}.{name}`", five tokens,
and that "`agents.hb.*.*.*`" is the one fixed subject callers may subscribe to without a lookup. ADR 0005 recorded the
same shape ("SAP fixes a five-token shape for agent subjects"). `agents.mail.cc.<owner>.<zone>.<name>` has six. SAP
reserves four verbs (`prompt`, `hb`, `status`, `attachments`) and `mail` is not one of them, so SAP gives `mail` no
rule and does not forbid the verb; the design relies on that (`2026-10-21-mash-design.md:57`). But the six-token subject
is outside the one shape section 2 defines for an agent's subjects, and ADR 0054 itself says it "amends ADR 0005 for the
mail verb". What stays conformant is the part SAP actually specifies: prompt, status and heartbeat keep five tokens
(ADR 0054, ADR 0055), so a SAP tool still discovers, probes and prompts a mash endpoint. What does not: a tool listing
mail with `agents.mail.*.*.*` misses zoned endpoints, and mash already resolves mail subjects through its own directory
rather than SAP's `$SRV.INFO`, which the design calls a bend of section 2 (`2026-10-21-mash-design.md`, "Addressing
mail"). The claim "pure SAP" is true of the SAP verbs and false of mail.

### 2.2 Broken as conceived, or as implemented?

**As conceived, in one respect; as implemented, not on the evidence I could run.**

*Implementation.* `go test ./internal/policy ./internal/admin ./internal/sap` passes on `6dbb9ca`, including the live
tests that start nats-server with the compiled keys. The compile is exact by construction: a property test asserts the
expanded permissions let a sender publish to its recipients, no more and no fewer (`internal/policy/compile.go:118`,
`Effective`). I found no defect in what it was asked to do. Two gaps are unfinished work, not bugs: ADR 0053 left
prompt and status unpoliced "until the maintainer rules" (ADR 0055, still the state), and the compiled zone lines are
six-token only (`internal/policy/compile.go:57`), so a compiled user cannot publish to an
endpoint whose directory record has no zone (it has the five-token subject). This is the seam between ADR 0054 ("no
zone" and zone `default` are different facts) and `docs/policy-file.md` (a missing label means zone `default`). It is
live code, so I checked it. Established: `TestApplyPushesZonesAndUsersGetExactlyTheirPermissions`
(`cmd/mashctl/apply_test.go:323-340`, run and passing on this commit) shows a compiled user publishing to a
five-token mail subject is refused by the server with a permissions violation and nothing is delivered. And
`mashctl drift` reports exactly this state ("the record has no zone (the legacy mail subject); the file says zone Z",
`cmd/mashctl/drift.go:303`), with `repair` running `SetZone` for it. So the design closes the seam by making the
administrator move every declared endpoint into its zone before it can be mailed; until then it is unreachable by
compiled users. Not established: what `mash send` tells the sender in that case (ADR 0053 promises a fast "refused by
policy" message; I did not run the send path against a legacy recipient), and whether `apply` warns before the
restrictive policy strands a legacy endpoint. Both need a live-server run with a deployed legacy record, which this
documentation-only change does not do.

*Conception.* The zone is a single partition baked into the address. That has four costs the design already carried or
the later naming work exposed:

1. It moves a policy attribute into identity. An endpoint's address changes when its zone does; ADR 0054 allows no
   change of zone and no return to legacy "in this version".
2. Only one label can be the zone. Every other label compiles to per-user tags, capped at about 135 by the 4 KiB connect
   line (`docs/policy-file.md`, "Limits of the encoding"), so the structure that makes size independent of endpoints
   serves exactly one grouping.
3. It breaks SAP's shape for mail (section 2.1).
4. It collides with the naming contract. The 2026-11-10 naming plan fixes every endpoint to three tokens, SAP's agent,
   owner and name, with "No sixth token" (`docs/plans/2026-11-10-naming-grammar-in-mash.md`, "Target"). The code agrees
   today: `RefuseCompiledForNewForm` refuses a policy-compiled user for a three-token identity "until the zone ruling"
   (`internal/admin/permissions_newform.go:57`). So the compiler, as designed, cannot serve the form mash is moving to.

The conclusion I draw: the compiler is a sound implementation of a design whose premise (the zone is an address token)
the naming contract has since ruled out.

### 2.3 What enforces mail today

**The NATS server, from the user JWT.** An endpoint's user JWT carries its publish and subscribe permissions. The
server refuses a publish that matches no allow line or matches a deny line. Nothing of mash's is in the message path;
ADR 0053 rejected a router and an auth callout for that reason. The JWT is produced by `mashctl` (ADR 0052) as follows.

- Without a policy: one wildcard line lets every endpoint of an owner mail every other (`agents.mail.*.<owner>.*`, plus
  the six-token twin), design section 8, `internal/admin/permissions.go:32`.
- With a policy file: `mashctl policy apply` compiles it into one **scoped signing key per zone** pushed into the account
  JWT, and mints each user under its zone's key (ADR 0052, ADR 0053; `internal/policy/compile.go:70`, `Compile`). The
  key's permission set holds the fixed per-endpoint lines as `{{name()}}` templates, one mail wildcard per recipient zone
  that the whole sender zone may mail (`agents.mail.*.<owner>.<zoneB>.*`), and two tag templates for exceptions.
- Exceptions are tags on the user: `allow:<zone>.<name>` and `deny:<zone>.<name>` (or `.*`), expanded by the key's
  templates `agents.mail.*.<owner>.{{tag(allow)}}` and `...{{tag(deny)}}`. NATS lets a deny beat an allow.
- ADR 0056: every zone key carries both templates and every user the placeholder tag `deny:_none` (a subject token no
  name can equal), because nats-server refuses a user with no `deny:` tag under a key that has the deny template. This
  keeps a first deny exception from re-signing a whole zone.
- ADR 0055: only mail is policed. Each zone key carries two open lines for prompt and status
  (`agents.prompt.*.<owner>.*`, `agents.status.*.<owner>.*`), so a deny rule does not stop a prompt or a status probe.

**What labels do and do not do.** A label is a field on a directory record. The server never reads it. A label has an
effect only because `mashctl` reads it, at compile time, and turns the result into something the server does read:
the label named by `subject_label` becomes the zone token and the zone key an endpoint is signed under; any other label
becomes a selector that picks endpoints for a rule, and the rule's effect appears as tags on those endpoints' users.
Labels *select*; subjects and tags *enforce*. Changing a label with no re-apply changes nothing the server does, and
`mashctl drift` exists to report when the two disagree (ADR 0053). Elsewhere labels only display (`who --long`) and
filter (`who`).

**"ACL".** mash has no ACL object. The word does not occur in the code or the ADRs; it appears once in
`docs/comparison.md:146`, describing a rival's broker. The nearest mash concept is the compiled publish permission:
what an ACL would hold (who may reach whom) is the publish allow and deny lists in the user JWT, written from the policy
file. If "ACL" means a server-side allow list, mash's is those lines; if it means a per-endpoint list the endpoint
consults, mash has none.

**What is live.** Per x-lead: 0 zoned records of 68 and no policy files deployed. So today no
deployment runs zones or a compiled policy; every endpoint uses the single owner-wide mail line. That is why removing
the zone token has near-zero migration cost.

## 3. The question

The zone token is to leave the mail subject. Mail will have SAP's five tokens, `agents.mail.<agent>.<owner>.<name>`,
and the default grants for a three-token endpoint are scoped to its owner token, with extras limited to mail, prompt and
status (`internal/admin/permissions_newform.go:77`, `NewFormPermissions`: peer lines
`agents.<verb>.<agent>.<owner>.*`, extras validated by `ValidateGrant` at line 21, which refuses a wildcard in the agent
position). Without a zone token, **what stops endpoint A mailing endpoint B inside an owner?**

## 4. Alternatives

Each is judged on what it enforces and where, what it costs, what breaks, and how to get there from today. Sizes are
ADR 0052's and 0053's measurements (nats-server 2.15.x, jwt/v2 2.8.2): account JWT ceiling 1 MiB; user JWT must fit the
4096-byte connect line, about 135 tags or 24 literal recipients.

### A. The owner token is the fence

The permission boundary is the agent and owner tokens together (the third and fourth). Default grants say an endpoint mails, prompts and probes
endpoints with its own agent and owner tokens and no one else's; reaching across owners is an explicit extra grant. No labels, tags or zones
are involved inside an owner.

- *Enforces:* at the server, in the user JWT. Already built: `NewFormPermissions` is exactly this.
- *Cost:* near zero. One fixed permission set, no per-pair state, no signing-key proliferation, no account growth. A
  cross-owner extra is one literal line in the user JWT; a change reissues that one user.
- *Breaks:* nothing, today; and nothing at the owner boundary. It cannot separate two endpoints of the same owner, so
  an owner must be sliced into several owners to get a boundary there; every extra is a grant per user.
- *Migration:* none for live mail (no zoned records). Remove `set-zone` and `provision --zone` and the six-token lines.
- *Naming contract:* fits exactly: the naming plan already defines an owner grant as `agents.mail.<agent>.<owner>.>` and
  forbids a wildcard in the agent token.

### B. Labels compiled into exact per-endpoint allow lists, no subject token

Keep the policy file. `mashctl` compiles each sender's allowed recipients into literal publish lines or into
`allow:<name>` tags (the tag templates work without a zone: `agents.mail.<agent>.<owner>.{{tag(allow)}}`), one line per
permitted recipient.

- *Enforces:* at the server, in the user JWT. Fine-grained to the single pair.
- *Cost:* the size problem that created zones returns. Literal lists are quadratic: the per-endpoint-key layout crossed
  1 MiB at 87 endpoints; user-JWT lists outgrow the connect line at about 24 recipients (ADR 0052, measured). Tags
  stretch that to about 135 per user. Every join or leave reissues every sender that may mail the changed endpoint, and
  re-delivers its credentials file. Operational load scales with the number of pairs.
- *Breaks:* an owner with more than about 135 recipients per sender cannot be expressed; a label-group rule with many
  members spends tags per member. Prompt and status remain open (ADR 0055) unless compiled too.
- *Migration:* rewrite the compiler's zone-wildcard stage into tag-only output, drop `subject_label`, reissue users
  (none are zoned live). Moderate engineering, no flag day.
- *Naming contract:* fits (five tokens), with the compiler's wildcard unit gone; `RefuseCompiledForNewForm` can be lifted.

### C. No server-side fence inside an owner

All endpoints of an owner may mail each other. Boundaries exist only between owners (alternative A) and between NATS
accounts. Inside an owner, authenticity rests on what mash already has: every message is signed (ADR 0017), mail from
a `verified` sender is a delegated request, the rest is information (ADR 0019, 0020), and optionally a receiver-side
filter discards mail from senders its directory labels do not admit.

- *Enforces:* the owner boundary at the server; everything finer at the client, on the receiver, after delivery.
- *Cost:* lowest. Nothing to compile or reissue. A receiver-side label check is small code and no JWT growth.
- *Breaks:* a client check is not a fence: a modified or compromised receiver ignores it, and the message has been
  delivered and stored before it runs. ADR 0053 built the compiler because "a client cannot ignore the policy" was a
  stated requirement. Also relay by asking (A mails B to forward to C) stays unaddressed, as it is under every option.
- *Migration:* none.

### D. Hard isolation by NATS account

Where a real boundary is needed inside what is now one owner, give the group its own NATS account and connect the two
with explicit exports and imports (ADR 0052 named this "the later step for hard isolation between organisations").

- *Enforces:* at the server, strongest form: an account cannot see another's subjects at all.
- *Cost:* heaviest: an account, JetStream limits and imports per group, a stream export and import for each permitted
  subject (a service import is for request and reply), an explicit design for cross-account JetStream storage and
  acknowledgement, and a silent denial (the sender sees no refusal). mash's one-shared-account design
  (ADR 0006) would have to change.
- *Breaks:* directory, leases and the shared inbox model assume one account.
- *Migration:* large; only justified by a tenancy requirement mash does not have today.

### E. Alternatives considered and rejected

A router or relay evaluating policy per message, and an auth callout deciding at connect time, were rejected in
ADR 0053 because mash is limited to what NATS does by itself and a component of ours in the path reads payloads. Nothing
since has changed that.

### F. A hybrid I think is better than any single one: A as the default, B for exceptions only

Make the owner token the fence (A) and treat the policy file as an exception tool, not a mesh partition. The default
is the owner-wide line. An administrator who needs to *narrow* inside an owner writes a few `deny` rules, compiled to
`deny:<agent>.<owner>.<name>` tags on the named senders (the full recipient identity, since a name can repeat across owners), using the templates that exist today, with a hard cap and a `check` error if a
sender would exceed it; an administrator who needs to *widen* across owners writes a cross-owner extra. Anything larger
than that is a signal to split the owner (A) or the account (D), not to grow the compiler.

- *Enforces:* at the server (A's boundary plus B's tags).
- *Cost, two builds.* (F1) One scoped key per owner. Under the naming contract the owner token is the group a
  deployment assigns, so one account holds many owners, and the account JWT grows with their number. Arithmetic from
  ADR 0052's measurement (5.9, 21 and 102 KB at 5, 20 and 100 zone keys with sparse rules, about 1 KB per key): an
  owner key with only the fixed lines and the two tag templates is about 1 KB, so 1 MiB (1,048,576 bytes) is reached at
  roughly a thousand owners, before the rest of the account. That is far away but not unbounded, and it is a
  per-owner estimate, not a measurement of this key. (F2) One scoped key with the agent and owner as
  administrator-signed tags in its templates, so the account JWT is one key whatever the number of owners; this is
  pending a live-server test of template expansion and of the missing-tag case, and a trust cost: whoever holds that
  one key can mint a user for any owner, where a key per owner limits that authority; the tags are signed claims
  and the server does not check them against a directory identity. Cross-owner extras must also be written into its
  templates and tags, since a scoped user's own permission lists do not apply. Either way most users carry only the
  placeholder tag, and reissue is limited to the senders an exception names.
- *Breaks:* fine-grained deny across hundreds of endpoints in one owner is unsupported, by design, and `check` says so.
- *Migration:* as B, but smaller: delete the zone-wildcard stage and the zone labels, keep the tag templates and the
  deny placeholder (ADR 0056).

## 5. The opinion of Codex (Daybreak)

### Round 1

Asked for an independent assessment of sections 2 and 4 and a recommendation, given this document as it stood before
this section.

- Command: `codex exec -m gpt-6-sol -c daybreak=true -s read-only --skip-git-repo-check` (document on standard input)
- Setting: `daybreak=true`, model `gpt-6-sol`; CLI `codex-cli 0.166.0`
- Date: 2026-11-10, 16:35 UTC

The answer, unedited:

> **I recommend F, with a narrower contract:** endpoints may mail peers in the same agent and owner by default; administrators may add a bounded number of exact recipient denies and explicit cross-owner grants. NATS enforces the publish rules, and its deny rules override a broad allow. Keep the five-token mail subject. A is a reasonable choice if the maintainer no longer requires any boundary within an owner; C largely repeats A, while its receiver filter cannot prevent delivery or storage. B recreates the scaling problem, and D is disproportionate without a tenancy requirement. E remains a viable future design if policy must be evaluated per message. [NATS authorization](https://docs.nats.io/learn/security/authorization)
>
> The document needs these corrections and qualifications:
>
> - **F changes policy semantics.** A few denies can express exceptions to an open owner, but cannot implement the current policy file’s general `default: deny` or broad label-based allow rules. Say explicitly which rules the new compiler will reject.
> - **F’s costs are understated.** One scoped key *per owner* makes account-JWT size grow with owner count. A label rule can require reissuing every matching sender, including when a newly labeled recipient joins. Check the expanded permission lists and actual `CONNECT` size; “135 tags” and “24 recipients” are measurements for one encoding and configuration, not NATS protocol limits. The server’s `max_control_line` is configurable. [NATS configuration](https://github.com/nats-io/nats.docs/blob/master/running-a-nats-service/configuration/README.md)
> - **A permission change needs a rollout plan.** A newly issued sender JWT does not replace credentials already held by a running endpoint. Specify how existing connections are made to use the new deny and how to verify enforcement before calling a policy applied. Account updates can also cause existing connections to be reevaluated; the behavior depends on what changed. [NATS JWT administration](https://github.com/nats-io/nats.docs/blob/master/running-a-nats-service/nats_admin/jwt.md)
> - **“Mail fence” is narrower than “cannot contact B.”** Prompt and status remain open under the stated design. If the administrator’s rule means *no interaction*, those verbs need an explicit ruling too. Also test that the compiled five-token permissions cannot publish to any remaining six-token inbox subscription.
>
> A potentially simpler variant of F is **one scoped signing key with administrator-signed agent and owner tags in its templates**, plus per-sender deny tags. That could avoid a key per owner, but it needs a live-server test of template expansion and missing-tag behavior before adoption. The document’s tests establish the current zone compiler’s behavior; they do not establish that this replacement works. [NATS scoped signing keys](https://docs.nats.io/learn/security/decentralized-auth)

### Round 2, on the revised sections 2, 4 and 6

Run after the maintainer's review, on the document with the summary, the owner-count arithmetic and the seam check
added (section 5 omitted from the input). Same command, setting and CLI (`codex-cli 0.166.0`); date 2026-11-10,
18:55 UTC. The answer, unedited:

> **I recommend A now, with a narrow version of F only if administrators actually need exceptions within an owner.** A gives a compact, server-enforced boundary using the existing agent and owner tokens. F is reasonable for a small number of denies, but its proposed shared-key design is not ready to recommend over A: it needs a complete permission and credential design, not just a template expansion test.
>
> The main corrections are:
>
> - **Section 2 overstates the SAP violation.** SAP 0.3 defines five-token subjects for its protocol endpoints; `mail` is not one of those endpoints. Six-token mail hurts predictable wildcard use, but five-token mail does not by itself make mail a SAP service or make it discoverable through `$SRV.INFO`. Call this an extension and an interoperability issue, rather than a clear violation of a specified mail format. [SAP §2](https://github.com/synadia-ai/synadia-agent-sdk-docs/blob/main/core-protocol.md)
> - **“A wildcard needs a zone token” is too broad.** The existing agent and owner tokens already support compact group grants. The zone was needed for *label-defined groups within an owner*, not for compact permissions in general.
> - **F2 has an unstated trust cost.** One scoped key whose permissions depend on user `agent` and `owner` tags lets anyone holding that signing key mint a user for any such group. Per-owner keys in F1 limit that authority. Tags are signed claims, but the server does not independently check that they match a directory identity. Also, permissions placed in a scoped key replace the scoped user’s permission lists: cross-owner extras must be designed into its templates and tags, not assumed to work as ordinary user-JWT grants. [NATS scoped-key documentation](https://docs.nats.io/learn/security/decentralized-auth)
> - **The missing-tag test is necessary, but the document should specify the expected result.** Current nats-server code adds `deny >` if template expansion empties a previously nonempty allow list; it rejects a missing tag used in a deny template. Test the exact supported server version and every required tag combination. [nats-server template code](https://github.com/nats-io/nats-server/blob/main/server/auth.go)
> - **F’s exceptions need full recipient identity.** A `deny:<name>` is insufficient if names repeat across agent or owner groups. Define the compiled pattern using agent, owner, and name, and test that deny precedence and all cross-owner allows produce exactly the intended subjects.
> - **D describes the bridge too simply.** A service import is for request/reply; ordinary cross-account pub/sub uses stream exports/imports. Cross-account JetStream mail also needs an explicit storage and acknowledgement design. [NATS cross-account JetStream reference](https://docs.nats.io/reference/2.12/jetstream/cross-account-subjects)
>
> Section 6’s first reason is simply false: A, B, and C also need no new subject token. Its operational caution about revoking old, wider credentials is sound. A successful denied-publish check is useful evidence, but should cover each server and the old credential; a NATS publish permission error is asynchronous and can otherwise be missed by the sending client. [NATS authorization documentation](https://docs.nats.io/learn/security/authorization)
>
> A better staged decision is: adopt **A** and remove unused zone addressing; retain the policy-file concept only when there is a concrete within-owner requirement. If that arrives, implement **F with per-owner scoped keys first**, bounded exceptions, and tests for credential revocation and effective permissions. Reconsider F2 only if measured owner count or account-JWT size justifies its wider signing authority.

### Where I agree and disagree

*Round 1 on owner count.* I first disagreed with Codex that a key per owner grows the account JWT with the number of
owners, because ADR 0006 gives one owner per deployment. That was wrong: ADR 0006 describes the legacy form. Under the
naming contract the owner token is the group a deployment assigns, so one account holds many owners and Codex was
right. Section 4F now gives the arithmetic. I also adopted round 1's other corrections (the rules F refuses,
prompt and status outside the fence, the narrowing-needs-revocation rollout).

*Round 2.* I agree with these and changed the text: the wildcard point (the zone was needed for label groups inside an
owner, not for compact grants in general); the deny pattern must carry agent, owner and name; D needs stream exports,
not a service import, and a JetStream design; section 6's first reason applied to A, B and C equally. I also agree that
F2 has a trust cost I had not stated: the holder of the one signing key can mint a user for any owner, where a key per
owner limits that authority (section 4F).

I do not accept, and have not changed, the SAP point. SAP section 2 gives the five-token shape for "every agent
instance"; mash's own design claimed "Pure SAP"; the six-token mail subject is outside that shape. Codex is right that
`mail` is not a SAP endpoint, so the departure is from the subject shape and from mash's claim, not from a mail format
SAP specifies; section 2.1 already says exactly that and I have kept it.

On the recommendation the two rounds differ: round 1 said F; round 2 says A now and F with a key per owner first, if a
within-owner requirement appears. The maintainer's review asked me to prefer F2 pending its test. Section 6 states
both and sets out the order: A first, and the choice between F1 and F2 on the test and on the authority question.

## 6. Recommendation: A now; F1 when a within-owner requirement appears; F2 only if measurement forces it

**Remove the zone addressing and make the agent and owner tokens the fence (A). When an administrator needs finer
control inside an owner, add F1: a scoped key per owner, with the policy file as an exception tool (bounded deny and
cross-owner exceptions as tags), not a mesh partition. Take F2 (one key, agent and owner as signed tags) only if the
measured owner count or account-JWT size forces it, after its live-server test and an accepted ruling on its wider
signing authority (whoever holds that one key can mint for every owner).**

Reasons:

1. A delivers these now. Like B and C it needs no new subject token, so mail keeps SAP's five tokens, the six-token
   departure (section 2.1) goes away, and the compiler can serve three-token endpoints (section 2.2, point 4).
2. A keeps a server-side fence, which ADR 0053 required and C does not give, and it already exists in code
   (`NewFormPermissions`).
3. A stays small: nothing to size per pair (the reason B fails), no per-group account (D). Zones are unused live
   (0 of 68), so the migration is deleting unused paths.
4. F1 adds, only when needed, the ability to narrow inside an owner and widen across owners, the realistic need of a
   solo developer or a small team, while keeping the authority to mint limited to one owner per key.

What F1 must say out loud, because the policy file's current promise is larger:

- The rules the compiler accepts: `deny` rules between named endpoints or small label groups, and cross-owner
  allows. It refuses `default: deny` for an owner and any rule whose compiled tags would exceed the per-user budget,
  with a `check` error that names the count. A deployment that needs a general deny-by-default mesh inside an owner
  should split the owner or, if it needs hard isolation, use D.
- Prompt and status stay outside the fence until the maintainer rules (ADR 0055); "fence" means mail.
- Applying a change: a key change reaches connected endpoints on an account push; a tag change needs the named users
  re-signed, their credentials re-delivered and the endpoints reconnected. A change that narrows (adding a `deny:`
  tag, removing an `allow:` tag) must also rotate the user's key and revoke the old one, as ADR 0067 requires, because
  user JWTs carry no expiry and the old, wider JWT stays valid for anyone holding it; revocation must be confirmed on
  every server before the policy counts as applied. A policy counts as applied only after a live check that a denied
  publish is refused, on each server and with the old credential. The existing `drift` and `apply` verification do
  this for zones and would be reused.
- Before building F2: a live-server test of template expansion and the missing-tag case, with the expected result
  written down. Before building either: a test that compiled five-token permissions cannot publish to any six-token
  inbox subscription left in a zoned stream. The section 2.2 seam is already covered by the existing test it cites;
  the open part is the sender's message and an `apply` warning.

Next steps if ruled: an ADR that supersedes 0053 and 0054 for the subject (and amends 0055's framing), then the code
change as small pull requests. Nothing here is ruled.
