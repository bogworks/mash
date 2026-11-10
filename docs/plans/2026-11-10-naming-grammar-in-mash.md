# Plan: the naming grammar in mash

Date: 2026-11-10. Scope: mash's side of the work contract `naming-grammar` (draft-5, ratified 2026-11-09, D180), in
three phases: interim compatibility, the token-shape change, and removal of the old forms with negative tests. The
grammar text, its validator, the shorthand resolver and the rollout belong to homerange and the ops lanes (DEC-007,
REQ-005, REQ-017); none of that is in mash's code, and none of it is planned here. No code for this plan is written until
the lead has cleared it.

## What the code does today

- A mash endpoint is `(owner, name)`. The owner is one setting per deployment (`config.Owner`, `MASH_OWNER`); the name is
  `MASH_NAME`, checked by `config.ValidName` (lower-case words joined by single dashes, 1 to 48 characters, ADR 0036).
- Subjects are SAP's five tokens with a constant agent token: `agents.<verb>.cc.<owner>.<name>` (`sap.For`,
  `sap.AgentToken = "cc"`, ADR 0005). Mail may carry a sixth, zone token: `agents.mail.cc.<owner>.<zone>.<name>` (ADR 0054;
  `sap.ForZone`, `SplitMail`, `ZoneOfMailFilter`); an endpoint with no zone uses the five-token form.
- A directory record already has `name`, `owner`, `agent` (the harness, `claude-code`, SAP's `metadata.agent`),
  `agent_token` (`cc`), `mail_subject` and an optional `zone`. `ParseRecord` requires `name` to equal the key and
  `mail_subject` to equal the subject built from owner, zone and name with the constant `cc`, so a reader from before
  this work rejects a record that carries another agent token or a flat key.
- Everything else is keyed on the name alone: the inbox stream `inbox-<name>` (`sap.InboxStream`), the reply prefix
  `_INBOX.<name>` (`sap.InboxPrefix`), directory and lease KV keys, NATS user names, credential files; the durable consumer is the constant `deliver`, scoped to its stream. Two herds that each
  have a lane called `lead` would collide.
- Code that touches these: `internal/sap`, `internal/store`, `internal/admin`, `internal/endpoint`, `internal/config`,
  `internal/policy`, `internal/policyfile`, `internal/ctl`, `cmd/mash`, `cmd/mashctl`. `ValidName` is called from about
  twenty files.

## Target (REQ-006, REQ-012, BND-005)

For each endpoint, SAP's `agent`, `owner` and `name` tokens are set per endpoint, as homerange supplies them: on a range,
the range, the herd and the lane (`agents.mail.bogworks.x.ops1`, heartbeat `agents.hb.bogworks.x.ops1`). No sixth token.
Where one flat identifier is needed (stream, consumer, KV key, `_INBOX` prefix, NATS user name, credential file name)
it is the three tokens joined by underscores (`inbox-bogworks_x_ops1`), injective because no token contains an
underscore. A herd grant is `agents.mail.<range>.<herd>.>`; no grant wildcards the range token (REQ-027). mash reads the
three tokens as SAP's agent, owner and name, never as range, herd or lane (REQ-007), and does not hard-code the
homerange grammar (REQ-017).

## What mash enforces and what it does not

Mash's own rule for a new-form identity is a token rule: each of the three is one valid subject token (lower-case letters,
digits, single dashes, starts with a letter, no period or underscore) of at most 32 characters, and the canonical form
(three tokens and two separators) at most 64 (REQ-009, REQ-010, which replaces the 48-character cap through one ADR
amending ADR 0036; DEC-004 decided these limits). That is mash's bound on subjects and stream names, not the homerange
grammar. Everything that is the grammar stays out: no dash in a herd, the harness-name list (REQ-028), the shorthand.

A **legacy identity keeps today's rules** until the break: `ValidName` (48 characters, for owner and name alike) and
the zone shapes `ValidZone` accepts. The new rule is never applied to a legacy identity, so nothing valid today becomes
invalid before the approved break (REQ-014, NFR-003).

## Phases and pull requests

Every pull request is small, off `main`, one concern, with tests written before the code they cover, Conventional
Commits, no secrets or Claude session links, and, before it merges, the configured code review and an adversarial review
on a model of another family (INV-005). An ADR takes `docs/decisions/NEXT` and bumps it. Behaviour that new-form
identities enable stays behind one off-by-default setting until Phase 2 turns it on, so no intermediate pull request
exposes a half-built path (NFR-003: the break is deliberate, never accidental). The setting is removed in Phase 3.

### Phase 0: pin today's behaviour (no change)

- **0.1 Characterisation tests.** Golden tests for every identifier derived from `(owner, name, zone)` today (the
  subjects of each verb in the five- and six-token forms, the stream, the reply prefix, KV keys, NATS user names,
  credential file names, the `who --json` form of ADR 0049), plus **boundary vectors for legacy validity** (a 48-character
  owner and name, a 32-character zone, the zone shapes accepted today). Goldens pin spelling and validity only; they
  are not evidence that delivery is preserved. Delivery, durable inboxes, wake and `[verified]` labels are pinned by
  the behavioural tests of 1.4.
- **0.2 ADR.** One ADR for the identity triple and the flat form (when it lands it supersedes the layout part of ADR
  0005 and ADR 0054 and amends ADR 0036). Written `proposed`, accepted by the pull request that makes it true (1.1).

### Phase 1: interim compatibility behind the setting (REQ-014, DEC-013 as amended by D183; AT-004)

The aim: mash accepts the old form and the new side by side, so the mash herd (which starts under temporary old-form
names) can change mash, and the ops lanes can run on new-form names before the break (REQ-018). Readers come before
writers, and nothing writes a new-form record until every reader in the range can read it.

- **1.1 The identity type.** `sap.Identity{Agent, Owner, Name}` (the SAP tokens; `Agent` is the agent *token*, distinct
  from the record's `agent` field that names the harness and stays as it is, BND-005) with subjects, mail filter,
  `Flat()` and validation by the new token rule; a legacy constructor that keeps the old validity. Pure functions, no
  caller changes. Tests first: token vectors mash owns, the flat form injective (generated property test, REQ-012,
  AT-007), flat to tokens and back, the 64 bound including separators (62 token characters is the most), legacy vectors
  from 0.1 still valid.
- **1.2 Reader first: the directory record.** A record is keyed by the flat identity for a new-form endpoint; `name`
  is the lane token and `agent_token` the range token (both fields exist). `ParseRecord` learns to check the key and
  the subject from the identity, and still accepts every legacy record. This pull request ships and is deployed to
  every reader before 1.6 can write one. The supported boundary is stated in the change-log fragment: a reader older than
  this release cannot read a new-form record, which is why the setting stays off until readers are upgraded.
  Tests first: legacy records, new-form records, and an old reader's refusal (documented, not hidden).
- **1.3 Subjects and per-endpoint tokens.** Owner and agent tokens come from the endpoint's identity (all four verbs,
  heartbeat included); stream filters come from the identity; parsing accepts the legacy five-token and zoned six-token
  forms and the new five-token form. Legacy identities produce exactly today's values (0.1 proves it).
- **1.4 Keys by flat identity, and a mail-preserving transition.** A new-form identity takes `inbox-<flat>`,
  `_INBOX.<flat>`, flat KV keys and user names; a legacy identity keeps its keys. The consumer name `deliver` is
  stream-scoped and stays; if the contract meant it to change, that is a clarification to ask for (below). Credential and
  state file names that mash derives take the flat form for new-form identities (REQ-012). **Renaming an endpoint does
  not move mail.** Stored messages keep their original subject because signatures bind to it, so they are never
  rewritten. The mechanism is chosen and proved by a test before anything is enabled: signed mail queued in the legacy
  stream is readable under the new identity with the same `[verified]` label, the old name still delivers to the same
  inbox during the window (AT-004), and counts and last sequence before and after are reported for AT-003 (INV-001 to
  INV-003). Candidates: a new-form stream that sources the legacy stream and takes both subject filters (a source keeps
  subject and headers byte-identical), or an endpoint that drains its legacy stream first. Rollback for a lane is to
  stop using the new identity; the legacy stream is never altered (NFR-002), and the rollback steps are written down.
  This is the riskiest step of the plan.
- **1.5 Grants.** Herd and range grants (`agents.mail.<range>.<herd>.>` or `.*`, `agents.mail.<range>.>`) from the
  configuration homerange supplies; a grant with a wildcard or `>` at or before the range position is refused with an
  error naming REQ-027 (AT-013's accepted and refused lists as vectors; its mutation as a test). A lane of herd x
  receives mail to any lane of herd x and cannot subscribe to herd y's subjects. This lands before any new-form user is
  minted, because it is a security rule. Until the zone question below is ruled, new-form provisioning refuses an
  endpoint that policy gives a zone, with a clear error.
- **1.6 Provisioning and display.** `mash` and `mashctl` provision, start and address an endpoint by its three tokens as
  well as by a legacy name, still behind the setting; `who`, `inbox`, message headers and trust lines show each token
  byte-identical (REQ-006). Provisioning refuses a name that was retired (ADR 0015's tombstone) so a retired old-form
  name is never reassigned (INV-002). The interface is open (see the gaps).
- **1.7 The name-acceptance rule.** DEC-014 chose (c): homerange retires old identities and credentials, and mash applies
  a per-owner rule that homerange supplies as configuration at every entry point (provision, start, send, reply, live
  prompt, directory lookup). This pull request builds the mechanism, with no default rule (SEAM-001), tests first:
  with a rule configured each entry point refuses a name it forbids; with none, a plain name still works outside a range
  (AT-006). The rule's format is open and the seam page needs Randy's approval (D145); see the gaps.
- **1.8 Old-form use log.** Every use of the old form at each entry point is counted so AT-003 can read it
  (REQ-014, AT-004). Built whether or not a window is used.
- Evidence: AT-004 as a test (mail to the old and the new name lands in one inbox under one identity and the use is
  logged); an INV-003 test that no alias delivers under a weaker trust label or lets one endpoint sign as another.

### Phase 2: turn it on (REQ-006; AT-002)

Not new mechanism: the setting is switched on and new endpoints default to the new form.

- **2.1 Demonstration (AT-002).** An endpoint `bogworks.<herd>.<lane-with-dashes>` provisioned, sends, receives, replies,
  is prompted and woken; heartbeat on five tokens; `who`, `inbox`, header and trust line show each token byte-identical
  and `[verified]`; two lanes named `lead` in two herds coexist; one real rename with mail queued before it. On the real
  account this needs a credential re-issue, which is Randy's approval and ops' work (BND-004); mash's side is prepared
  and tested in-process before then.
- **2.2 Stop emitting the zone token.** New mail uses five tokens; the six-token form is still accepted for legacy
  endpoints until Phase 3. Blocked on the zone ruling.
- **2.3 Release notes.** Every wire-visible change is listed in the change-log fragments.

### Phase 3: removal and negative tests (REQ-016, REQ-017, REQ-020, REQ-024; AT-005, AT-012)

One pull request (tests and removal together, since the negative tests cannot pass before the removal), **prepared and
reviewed ahead of the break and held** by the lead until Randy's go, DEC-006 settled and AT-003 clean twice a day apart;
the break is irreversible and is his. Before the break the ops lanes retire old-form credentials and records so old
identities are already unaddressable and unstartable (AT-003 requires that pre-break); mash supplies what it needs to
enumerate directory records and inbox streams. The pull request:

- **3.1 Negative tests, written first** (the commit order shows them failing), checked in with the code they guard, one
  per REQ-016 entry point and per old form: the old endpoint name as a mash name (`x-masterplanner`), the legacy subject
  `agents.mail.cc.homerange.<name>`, ADR 0054's six-token subject, any name under the agent token `cc`, and a grant on
  `agents.mail.*.homerange.*`, each through a direct mash client and, where homerange's path exists, through it. They run
  in CI on every change from then on and are never removed or skipped.
- **3.2 Remove the compatibility.** Old-form acceptance in mash's name rule, the legacy subjects and their grants, the
  constant `cc` token and the one-owner setting, five-or-six token parsing, every alias, the use log, the setting from
  Phase 1, and the mash herd's temporary names.
- **3.3 Loud refusal.** A refused name or grant names the rule it broke (REQ-020); mail to an old-form name is refused to
  the sender, not dropped. Mutation (AT-012): restoring old-form acceptance or an alias makes CI fail, shown once.
- **3.4 ADR and search.** Accepts the supersession of ADR 0005's and ADR 0054's layout parts; a search of the code shows
  no remaining compatibility path.

## Map to the contract

| Contract | Where it is met |
|---|---|
| REQ-001, REQ-002, REQ-009, REQ-010 (mash's token part) | 0.2, 1.1 |
| REQ-006, AT-002 | 1.2 to 1.6, 2.1 |
| REQ-007, AT-006, INV-004 | every phase's review; a test that no code reads a token as range, herd or lane; plain names work outside a range (1.7); AT-006 also needs the scrub's post-scrub verification (plan 2a, step 7) first |
| REQ-012, AT-007 (mash's identifiers) | 1.1 (injective, generated), 1.4 (stream, KV, `_INBOX`, user names, file names); the target-rule checks for each target mash owns |
| REQ-014, AT-004, INV-001 to INV-003, NFR-002 | 0.1, 1.2 to 1.4, 1.6, 1.8, rollback notes in 1.4 |
| REQ-027, AT-013 | 1.5 |
| REQ-016, REQ-017, REQ-020, AT-005 | 1.7, 3.1 to 3.3 |
| REQ-024, AT-012 | 3.1 to 3.4 |
| REQ-015, AT-003, AT-008 to AT-011, REQ-018, REQ-019 | ops lanes and the mash herd's own contract; mash supplies enumeration and the use log |
| REQ-003 to REQ-005, REQ-008, REQ-011, REQ-013, REQ-021 to REQ-023, REQ-025, REQ-026, REQ-028 to REQ-030, AT-001, AT-014 | homerange's validator and resolver; not mash |
| BND-005 | SDK card #129 carries the same per-endpoint tokens; see the dependency below |

## Order and dependencies

Phase 0, then Phase 1 in order, then Phase 2, then Phase 3 held until the break. Plan 2a's inventories (the zone and owner
inventory, the emission-path map) describe the code before this work changes it, so they should merge first or be refreshed
when this changes the same files; the scrub reword touches text, this plan touches `internal/sap`, `internal/store` and
friends.

## Gaps and questions for the lead (each once, with refs)

1. **What replaces the zone gate (REQ-006, DEC-012, ADR 0053, 0054, 0056).** The six-token zone subject is what today
   lets a policy confine a sender to one zone; zone keys and `mashctl policy` compile against it. REQ-006 retires the zone
   token after the transition, and grants are per herd (REQ-027), but the contract does not say what happens to the policy
   language's zones and the compiled permissions of existing policy files. That is a policy-semantics change, outside
   both contracts' decisions. It can meet any step that compiles grants (1.5, and 2.2); until ruled, new-form
   provisioning refuses a zoned endpoint (1.5) and 2.2 waits.
2. **The per-owner name-acceptance rule's format (REQ-017, DEC-014 (c), SEAM-001 sentence 2).** The enforcement location
   is settled (retirement and mash-applied configuration). The configuration's shape is not specified and the seam page
   that would carry it is unwritten and needs Randy's approval (D145). Needed before 1.7.
3. **The provisioning interface (1.6).** The contract delegates how mash lays out the tokens; the CLI redesign is
   frozen (card #9). Do `mash` and `mashctl` gain flags, or is the identity supplied by configuration only? Needed
   before 1.6.
4. **DEC-006 (window length and observation period; open, must settle before the break).** 1.8 builds the use log either
   way; Phase 3 is not merged until DEC-006 is ruled.
5. **Credential re-issue for the live rename (2.1).** Re-issuing credentials is Randy's (contract section 7) and ops'
   (BND-004); the lead schedules it. Nothing in Phases 0 and 1 needs it.
6. **`internal/sap` ownership.** BND-005 says the SDK work (card #129) must carry per-endpoint tokens. If the sdk lane is
   extracting `internal/sap`, 1.1 and 1.3 should land there, or the sdk lane should be told.
7. **The consumer name.** REQ-012 lists consumer names among flat-form targets; today's is the constant `deliver`, unique
   within its own stream. This plan keeps it. Say if the contract meant otherwise.

## Evidence kept as we go

Each pull request names the contract rows it covers, attaches its tests and records both reviews. The final record
carries the executed AT-002, AT-007, AT-013 and AT-012 outputs with commit ids, the rollback notes, the count, last
sequence and trust figures AT-003 reads, the deviations and the residual risks.
