# Plan: the naming grammar in mash

Date: 2026-11-10. Scope: mash's side of the work contract `naming-grammar` (draft-5, ratified 2026-11-09, D180), in
three phases: interim compatibility, the token-shape change, and removal of the old forms with negative tests. The
grammar text, its validator, the shorthand resolver and the rollout belong to homerange and the ops lanes (DEC-007,
REQ-005, REQ-017); none of that is in mash's code, and none of it is planned here. No code for this plan is written until
the lead has cleared it.

## Amendments of 2026-11-10 (after the planner's check)

The planner's second opinion, which the maintainer's delegate adopted, checked this plan against the contract and found it conforming with the fixes below. Where an amendment and a
step further down differ, the amendment rules. Cleared for code by the maintainer's delegate now: Phase 0, 1.1 to 1.3, and 1.4 for unzoned endpoints.
1.5 is not cleared until this amendment merges; 1.7 holds for the rule's format (see the proposal below); 2.1 waits for
the credential re-issue; Phase 3 is held.

1. **1.5 covers every verb, and the window needs a bridge (REQ-006, REQ-014, REQ-018, REQ-027).** Grants are for mail,
   prompt, status and heartbeat, all in the new five-token shape. A legacy user's grant `agents.mail.*.<owner>.*` cannot
   publish to a new-form subject whose herd token differs from the legacy owner, so new-form and legacy endpoints would
   not reach each other. The bridge, built in Phase 1 and removed in 3.2, is kept as narrow as NATS allows, because a
   grant does not know whether it is "legacy": it matches tokens.
   - **Alias subjects are exact.** A new-form endpoint takes its legacy alias (the legacy name, AT-004) as exact
     subjects with the literal agent token `cc` (`agents.<verb>.cc.<legacy-owner>.<legacy-name>`), never a wildcard in the
     agent position, and publishes its heartbeat on both its new subject and its alias. Its user also gets exact **subscribe** grants for
     the alias's `prompt` and `status` subjects (`agents.prompt.cc.<legacy-owner>.<legacy-name>` and
     `agents.status.cc.<legacy-owner>.<legacy-name>`; mail reaches it through the legacy stream, so no mail
     subscription is needed), tested with provisioned credentials for both verbs: a legacy peer's prompt and status
     request to the alias is received and answered, and nothing else under `cc` is subscribable. A new-form sender's window grant
     to reach legacy endpoints is `agents.<verb>.cc.<legacy-owner>.*` for the verbs `mail`, `prompt` and `status` only
     (a wildcard only in the name position); heartbeat publication is granted on the endpoint's own exact subjects
     (new and alias) and never on a legacy endpoint's. Neither
     overlaps a new-form subject, because REQ-028 forbids a range named `cc`.
   - **Overlap is refused by mash's own admission, not by the optional rule file.** Existing legacy users hold
     `agents.mail.*.<legacy-owner>.*`, which also matches `agents.mail.<range>.<legacy-owner>.<lane>`. Keeping new-form
     subjects disjoint from legacy ones is mash's own subject-disjointness, not homerange grammar, so REQ-017 does not
     stop it, and it holds with or without a rule file. During the window mash's provisioning and admission refuse
     (a) any new-form identity for which `sap.Overlaps` finds an overlap with an existing directory record, and (b) a
     new-form owner token equal to any legacy owner present in the account. This lands in 1.3 part 2b, before any
     new-form record can be written (until then every path that would write one fails closed). The window rule in the
     name-rule file is belt and braces, not the only guard. AT-003's enumeration (1.9) also lists every credential that
     still carries such a wildcard. Tests first: an overlap test that a new-form subject with an owner token equal to a
     legacy owner is refused with and without a rule file; that a legacy wildcard grant cannot reach a new-form
     subject of any other owner; and a delivery test with both a legacy and a new-form record present.
     Admission is symmetric: provisioning a legacy identity during the window also runs `sap.Overlaps` against the
     existing new-form records (and the owner pairing is refused in either order), with a test; it runs under a
     directory-wide admission lock so two writers of different names cannot both pass on a stale snapshot.
   - **Streams do not overlap, and nothing is copied.** The legacy stream keeps its own subject filter and is never
     altered; legacy-alias mail keeps arriving in it. The new-form endpoint's stream `inbox-<flat>` takes the new subject
     only. During the window the endpoint reads **both** streams: the legacy stream through its existing durable
     consumer, whose acknowledgement state (ack floor and pending set) is the record of what was already shown, and the
     new stream through a new consumer. No two streams share a subject, no message is copied, and stored messages keep
     their subjects and headers, so signatures still verify. **Why not a stream source:** a source copies messages
     into a second stream, and a new consumer there would show already-acknowledged mail again as new. Starting the
     source after the legacy consumer's ack floor (`OptStartSeq` = floor + 1) does not fix it: the floor is only the
     lowest contiguous acknowledgement. mash's consumer is `AckExplicit` and its spool acknowledges a message once a hook
     has shown it, so acknowledgements can arrive out of order (an urgent message rings first, a batch is drained in
     the order the hook lists it), leaving acknowledged messages above the floor; a source starting at floor + 1 would
     copy those too. Reading the old consumer avoids the question, because it already knows which messages are acked.
     The spike (2026-11-10, nats-server 2.15.1, nats.go 1.54.0, run against the production permissions) settled the open
     points. **Numbering:** `StreamConfig.FirstSeq` exists but is creation-time only and cannot keep numbers unique,
     because the legacy stream keeps receiving alias mail during the window (legacy `#4` and new `#4` collide). The two
     streams' messages therefore carry stream-qualified numbers, decided as follows (lead, 2026-11-10): the new
     stream's numbers stay bare (`#n`, the endpoint's identity from here on); during the window only, a legacy-stream
     message shows a qualified number (`#L5`); `read`, `inbox` and every place a `#seq` is accepted take both forms; a
     bare `#n` never resolves to the legacy stream once the identity is new-form; 3.2 removes the qualified form; the
     new text goes into the emission-path map in the same pull request; a legacy-only endpoint (no `agent_token`) sees
     exactly today's numbers (golden test). Internally the seen and waking sets, acknowledgements and the batch maps key on (stream, sequence)
     instead of the bare sequence; `spool.Bind` is single-stream today and becomes per stream. **Credentials:** the
     transition lines below were verified: with them a provisioned user reads and acknowledges the legacy stream
     through its `deliver` consumer and cannot touch any other inbox or create or delete anything; without them the
     read times out (the violation shows only in the server log), so the endpoint logs asynchronous permission errors.
     **No redelivery:** reading the old durable consumer after an out-of-order acknowledgement returns only the
     unacknowledged messages; a new `DeliverAll` consumer on a copy returns all of them. Tests first: signed legacy mail is read under the new identity with the same
     `[verified]` label, **run with the provisioned user's credentials, not admin credentials** (a test that passes only
     with admin creds proves nothing: see the transition grant below); **mail that was acknowledged before the rename, including one acknowledged out of order above the
     ack floor, is not shown again after it**; unacked mail is shown once; and no stream subject overlaps another
     (INV-001, INV-003).
   - **The transition grant: the new-form user can read the legacy stream, and nothing else.** A new-form user's
     JetStream permissions name only `inbox-<flat>`, so as specified it could not read its own queued legacy mail. During
     the window its user also gets exactly the lines `fixedPub` gives a legacy user for the legacy inbox, for that one
     stream and no other: `$JS.API.STREAM.INFO.inbox-<legacy-name>`, `$JS.API.CONSUMER.INFO.inbox-<legacy-name>.deliver`,
     `$JS.API.CONSUMER.MSG.NEXT.inbox-<legacy-name>.deliver`, `$JS.ACK.inbox-<legacy-name>.deliver.>` and the
     `$JS.API.DIRECT.GET.inbox-<legacy-name>` pair. Its connection keeps the single reply prefix `_INBOX.<flat>`, which
     already covers fetches on both streams. No wildcard stream name, no create or delete subject. 3.2 removes the
     transition lines with the rest of the bridge; the overlap tests also assert that a new-form user cannot name any
     other inbox stream.
     The window user also gets the legacy alias's own lease key lines, exactly the two `fixedPub` gives a legacy user
     (`$KV.mash-leases.<legacy-name>` and `$JS.API.DIRECT.GET.KV_mash-leases.$KV.mash-leases.<legacy-name>`), because it
     holds the legacy lease while it reads the legacy stream; no wildcard key and no other lease key.
   - **The refusal is for new-form grants only.** The REQ-027 refusal never refuses the legacy grant the window
     needs; AT-012 expects it after the break (3.2 removes the legacy grants and aliases). Window isolation is shown
     by the overlap tests above, not by AT-012.
2. **1.5 is limited to the default, un-policied permission set.** ADR 0053's compiler uses the zone as its wildcard unit
   and puts every policy-compiled user in a zone. New-form provisioning therefore refuses every policy-compiled user
   (not only a zoned endpoint) until Randy rules on the zone question; it supplies the default permission set only.
3. **1.4 also covers** the suspended subject (`mash.suspended.<name>`, ADR 0057) and the lease tombstone (ADR 0015),
   both keyed on the flat identity, so two lanes named `lead` in two herds coexist, are suspended and deprovisioned
   independently, and one's tombstone never fences the other (AT-002, REQ-012).
4. **Phase 3's hold** reads: the observation period DEC-006 sets, plus AT-003 run immediately before the break, plus
   Randy's go. (Not "a day apart", which assumed option (b).)
5. **New step 1.9: AT-003's enumeration (NFR-004).** A command, runnable at any time, that lists mash's side of the
   population from its sources: every directory record and inbox stream in the account, with stored name, source,
   running or stopped, old or new form, and per renamed identity the message count, last sequence and trust label before
   and after (INV-001 to INV-003). The ops lanes add the credential files, herd definitions and launch configurations.
   Tests first: a fixture account with legacy and new-form identities.
6. **New step 1.10: the rule-file negative tests land in Phase 1** (D183: "don't lose this"). Configuration A (a rule
   file that refuses `cc`, and `mash` inside a range) passes today, so every A test (each old form and the plain name,
   at every REQ-016 entry point) is in CI before the break and stays there permanently. The configuration B tests
   (what mash's own removals refuse: the constant-`cc` parsing and the six-token subject) cannot pass until the code
   they test is removed, so they are written first, failing, in Phase 3's single held pull request with the removal.
   This replaces 3.1's "written first" for the A tests with "already running".
7. **2.2 no longer waits for new-form endpoints.** Only zoned legacy endpoints and policy-compiled users wait for the
   zone ruling.
8. **Clarifications to record under the completion record (contract section 8).** (a) The identity is supplied by
   configuration (environment and `config.json`, beside `MASH_OWNER` and `MASH_NAME`); `mash` gains no flags while the
   CLI freeze holds. The recipient argument of `send` must accept the new form. Provisioning several endpoints may need
   the name argument of `mashctl` to change; if so, stop and tell the lead. Whether removing `set-zone` and `--zone`
   falls under the freeze is not yet verified. (b) The consumer `deliver` is a stream-scoped constant and is not derived
   from the identity, so REQ-012's flat form has nothing to apply to.
9. **Vectors and wording.** Mash's token vectors add "does not end with a dash" and `lead-`. This plan, being a dated
   record, names the three tokens as range, herd and lane when it describes the target; REQ-007 forbids that in mash's
   code, help and docs, not in a plan. This plan's occurrences are proposed as per-occurrence exceptions, decided
   (D186): the six dated lines are kept as written and each is listed as a scrub exception with a reason.
10. **Not in this plan.** The homerange validator, its vectors and the derived-identifier mapping belong to the mash
    herd under DEC-007 but are never in mash's code; they get their own plan once the planner settles DEC-011 (where
    they live). This plan's earlier "not planned here" is not a statement that nobody owns them.

### Proposed format of the range-level name-acceptance rule (REQ-017, DEC-014 (c); for the seam page)

Offered as the consumer's proposal, revised after the planner's second pass. The seam page's wording is "a range-level
rule that can match the agent, owner and name tokens". mash ships no default rule (SEAM-001). The rule is a standalone
file that homerange renders; mash reads it once at start, so a change takes a restart (a directory value or the policy
file are not used: anyone who can write the store could change the first, and the second is tied to the open zone
question). `reply` and `lookup` are separate entry points, tested separately.

- **Where it comes from.** A JSON file named by `MASH_NAME_RULE_FILE` (or `name_rule_file` in `config.json`), read by
  `mash` and `mashctl` alike through the configuration that already gives them their owner, so every invocation gets
  the same rule. No file named: no rule, and `status` says "no name rule set" so a missing file is visible. A value that
  is set but empty is invalid, not absent. A file that is unreadable or invalid, or has an unknown key, an unknown
  version, a duplicate `id` or an empty `match`: the endpoint refuses to start, naming the file and the problem.
- **Shape.** `{"version": 1, "rules": [ ... ]}`. Each rule: `id` (a short name), `match` and `message` (the refusal
  text). `match` is an object over any of `agent`, `owner`, `name`; each key takes `{"is": RE}` or `{"is_not": RE}`
  (RE2); `is` is true when the token matches, `is_not` when it does not. All given keys must hold for the rule to match,
  and a name that matches any rule is refused. mash wraps every pattern as a whole-token match itself (`^(?:RE)$`), so
  a pattern with an alternation cannot match part of a token; a test covers it. `is` and `is_not` on the same key, an empty object under a key, an empty `match`, and a missing or empty `id` or
  `message` each refuse start. There are no accept rules and no ordering, so the file cannot grant what mash's own
  syntax forbids. The window rule that refuses a new-form owner
  equal to the legacy owner is `{"agent": {"is_not": "cc"}, "owner": {"is": "<legacy-owner>"}}`; the old-form rule is
  `{"agent": {"is": "cc"}}`. Inside a range the file also refuses the reserved plain-name token with
  `{"agent": {"is": "mash"}}`, so a plain name cannot be used there (AT-012).
- **Which file is in force when.** The window rule and the old-form rule are never in force together (together they
  refuse the legacy aliases the window needs). During the window, from Phase 1 until the break, the file holds the
  window rule (new-form owner equal to the legacy owner refused) and the `mash` rule. At the break the break
  procedure replaces the file with the old-form rule (`cc`, and `mash` inside a range) and withdraws the window rule in
  the same step; every endpoint is restarted before AT-003 runs.
- **Entry points.** The rule applies at every one, with no per-rule list, so a rule for old forms cannot be narrowed to
  fewer: provision, start, send, reply, prompt, lookup, and the compatibility alias (REQ-016, AT-005). For `send` and
  `prompt` both the addressed identity and the sender's own identity are checked; `reply` checks the identity being
  answered; `lookup` of a refused identity fails loudly with the named-rule error (REQ-020, AT-005); it never silently omits it.
  The requested identity is checked **before** the directory is read, so an old-form identity whose record is gone
  still gets the named-rule error and not "not found".
- **What a name is.** The identity's three tokens. A legacy identity is `(cc, <owner>, <name>)`, so a rule on `agent`
  equal to `cc` refuses the old form without mash knowing why. **After the break a plain name still has three tokens**
  (REQ-024 removes the constant `cc` and the one-owner setting; AT-006 needs a plain name to work outside a range, AT-012
  needs the old form refused inside one): the owner token is the configured owner, the name token is the name, and the
  agent token is the configured agent token, which defaults to the neutral `mash` when no agent token is configured (mash
  cannot know whether there is a range). The configuration key is `agent_token` (environment `MASH_AGENT_TOKEN`; the
  environment wins over the file, as for the owner). `mash` is a **reserved** token (planner, confirmed): no deployment
  uses it as a range, and the range's rule file refuses it by exact match inside a range (below). Reserving it adds
  `mash` to the contract's reserved list (REQ-028); the planner amends the contract with the seam page, and until then
  the contract still permits a range named `mash`. Inside a range
  homerange supplies all three tokens.
- **What a refusal says (REQ-020).** The error names the rule (`id`) and carries its `message`; for `send`, `reply` and
  `prompt` the sender is told and nothing is dropped silently.
- **What it is not.** Not a grant (REQ-027 is separate, in grant compilation) and not a validator of the homerange
  grammar: homerange chooses the rules and supplies the file; mash applies them as given (REQ-017). It binds honest
  clients; a client with its own credentials that skips mash's code is stopped by retired credentials and by grants,
  which is why DEC-014 chose both.
- **The negative tests' two configurations (1.10, 3.1) are pinned.** (A) a rule file that refuses the old forms
  (`cc`, and `mash` inside a range): every entry point above expects refusal, including the `cc` and plain-name cases of
  AT-012. A **stays permanently**, in CI, after the break. (B) no rule file, with mash's own removals in force (the
  constant-`cc` parsing and the six-token subject gone): it covers only what mash's own syntax removes. mash cannot
  refuse `cc` or a plain name by itself without hard-coding the grammar (REQ-017), and AT-006 needs a plain name to
  work with no rule, so Phase 3 does **not** make B the only configuration.
- **A missing file must not reopen the old forms later.** Inside a range homerange also sets `name_rule_required`
  (environment `MASH_NAME_RULE_REQUIRED`, in the same configuration): when set, mash refuses to start without a loaded
  rule file (fail closed), so a later restart with no file cannot reopen `cc` or the plain name. The check that every
  endpoint reports a rule loaded is also a recurring one (AT-003's enumeration is runnable at any time, NFR-004), not only
  a step of the break.
- **Visibility and the break procedure.** With no file named, `status` says so, and when a file is loaded it names the
  file and a digest of its content. The file is read once at start, so the break restarts every endpoint before AT-003
  runs, and the break procedure and AT-003 check that every endpoint reports a rule loaded (and the expected digest);
  an endpoint with no rule inside a range fails that check.

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
  forms and the new five-token form. Legacy identities produce exactly today's values (0.1 proves it). **Admission** is in 1.3 part 2b (see amendment 1,
  "Overlap is refused by mash's own admission"): before any new-form record is written, mash refuses an identity that
  `sap.Overlaps` an existing directory record and a new-form owner token equal to any legacy owner in the account,
  with or without a rule file; a directory record read back is not proof of disjointness.
- **1.4 Keys by flat identity, and a mail-preserving transition.** A new-form identity takes `inbox-<flat>`,
  `_INBOX.<flat>`, flat KV keys and user names; a legacy identity keeps its keys. The consumer name `deliver` is
  stream-scoped and stays; if the contract meant it to change, that is a clarification to ask for (below). Credential and
  state file names that mash derives take the flat form for new-form identities (REQ-012). **Renaming an endpoint does
  not move mail.** Stored messages keep their original subject because signatures bind to it, so they are never
  rewritten. The mechanism is chosen and proved by a test before anything is enabled: signed mail queued in the legacy
  stream is readable under the new identity with the same `[verified]` label, the old name still delivers to the same
  inbox during the window (AT-004), and counts and last sequence before and after are reported for AT-003 (INV-001 to
  INV-003). The mechanism is the one in amendment 1: no copy; the endpoint reads
  the legacy stream through its existing durable consumer and the new stream through a new one, so nothing already
  acknowledged is shown again. Rollback for a lane is to
  stop using the new identity; the legacy stream is never altered (NFR-002). Mail that arrived only on `inbox-<flat>`
  stays stored but the legacy user cannot read it, so rollback keeps the new-form credentials until `inbox-<flat>` has
  zero pending and zero unacknowledged messages (or the legacy user is re-issued with the mirrored transition lines
  for `inbox-<flat>`); the rollback steps say so and a test proves no mail is left unreadable. This is the same rule as
  drain-before-revoke for the legacy grant (3.2), applied in both directions. 1.4 is split into three reviewable
  pull requests: (a) the spool and numbering keyed by (stream, seq) with no behaviour change for one stream (Phase 0's
  goldens and the existing suite pass unchanged); (b) two-stream reading behind `agent_token`, with the no-redelivery
  test (including out-of-order acknowledgements) run with provisioned credentials; (c) the transition grant lines, plus
  asynchronous permission errors logged loudly as an operator-visible error instead of a silent timeout, with a test.
  This is the riskiest step of the plan.
- **1.5 Grants.** Herd and range grants (`agents.mail.<range>.<herd>.>` or `.*`, `agents.mail.<range>.>`) from the
  configuration homerange supplies; a grant with a wildcard or `>` at or before the range position is refused with an
  error naming REQ-027 (AT-013's accepted and refused lists as vectors; its mutation as a test). A lane of herd x
  receives mail to any lane of herd x and cannot subscribe to herd y's subjects. This lands before any new-form user is
  minted, because it is a security rule. Until the zone question below is ruled, new-form provisioning supplies only the
  default, un-policied permission set and refuses every policy-compiled user (amendment 2), with a clear error.
- **1.6 Provisioning and display.** `mash` and `mashctl` provision, start and address an endpoint by its three tokens as
  well as by a legacy name, still behind the setting; `who`, `inbox`, message headers and trust lines show each token
  byte-identical (REQ-006). Provisioning refuses a retired new-form identity, so a retired name is never reassigned (INV-002). The plan first said
  to rely on ADR 0015's tombstone; that tombstone lives in the lease bucket and expires after the lease TTL (90 seconds),
  so the refusal would lapse. For new-form identities mash therefore writes a durable retired marker instead (the
  no-TTL bucket `mash-retired`, one key per flat identity, with two states). Both deprovision paths, the plain one and
  the user-key-guarded one, run the same order: the marker is written `pending` first, before the user is revoked or
  anything is purged (a full or failing bucket fails the call before it changes anything), then the user is revoked and
  the record purged (guarded by revision for the user-key path), then the marker is made `final`. A pending marker blocks
  provisioning like a final one (INV-002). The pending marker is deleted only on a definitive refusal (a check before the purge, or the server refusing the
  revision-guarded purge, even after the revoke), revision-guarded and only while the call still holds its locks; a purge
  with an unknown outcome, a crash or a lost lock leaves it pending, and running the command again finishes it (a missing
  marker is rewritten final). A pending marker for another user key than a live record is superseded and deleted; a final
  one with such a record refuses (INV-002). The bucket is opened fail-closed (a TTL, another history, or an evicting
  stream, meaning discard other than new, a max age or a max message count, is refused, never modified), and provisioning
  refuses a bucket with no room for one more marker. Provisioning checks the marker under the admission lock; there is
  no release path. `mashctl audit` listing pending and final markers is a follow-up on the audit branch. A legacy deprovision of a name
  that a new-form record names as its alias is refused, so no alias is left dangling. Legacy names stay reusable:
  INV-002's scope is the new-form identity, and the break's name rule, which refuses `cc`, ends old-form use. This still
  passes the contract's tests (AT-002 and INV-002 for new-form names). The interface is open (see the gaps).
- **1.7 The name-acceptance rule.** DEC-014 chose (c): homerange retires old identities and credentials, and mash applies
  a range-level rule that homerange supplies as configuration (it can match the agent, owner and name tokens) at every
  entry point (provision, start, send, reply, live prompt, directory lookup, the compatibility alias). This pull request builds the mechanism, with no default rule (SEAM-001), tests first:
  with a rule configured each entry point refuses a name it forbids; with none, a plain name still works outside a range
  (AT-006). The format is the proposal in the amendments section above; the seam page that carries it needs Randy's
  approval (D145), and the hold on 1.7 is released: the seam page is approved at 1.0.0 (D193) and the format is `name-rule@1` (`docs/name-rule.md`).
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
reviewed ahead of the break and held** by the lead until Randy's go, DEC-006 settled and the period DEC-006 sets and AT-003 run immediately before the break;
the break is irreversible and is his. Before the break the ops lanes retire old-form credentials and records so old
identities are already unaddressable and unstartable (AT-003 requires that pre-break); mash supplies what it needs to
enumerate directory records and inbox streams. The pull request:

- **3.1 Negative tests.** The A tests are already running (1.10). The B tests are written first in this pull request (the
  commit order shows them failing) and checked in with the code they guard, one
  per REQ-016 entry point and per old form, in the two configurations pinned above: the old endpoint name as a mash name
  (`x-masterplanner`), the legacy subject `agents.mail.cc.homerange.<name>`, ADR 0054's six-token subject, any name
  under the agent token `cc`, a plain name inside a range, and a grant on `agents.mail.*.homerange.*`, each through a
  direct mash client and, where homerange's path exists, through it. They run
  in CI on every change from then on and are never removed or skipped.
- **3.2 Remove the compatibility.** **Break order (no race between the drain check and revocation):** (1) stop alias
  publication first: revoke the senders' window grants and the aliases' publish lines; (2) confirm that took effect on
  every server in the cluster (a publish to an alias from a legacy sender is refused everywhere); (3) only then run the
  final drain check; (4) only then revoke the read grant. Steps are in the break procedure and the 3.2 test runs them
  in that order and fails if the read grant is revoked while an alias can still be published to.
  **Drain before revoking:** the transition grant on a legacy stream stays until
  that stream's durable consumer has zero pending and zero unacknowledged messages; 1.9's enumeration reports it per
  identity, the break procedure checks it, and 3.2 refuses to remove a grant whose legacy consumer is not drained (a
  test proves it). Then: old-form acceptance in mash's name rule, the legacy subjects and their grants, the
  constant `cc` token and the one-owner setting, five-or-six token parsing (the six-token subject form and its
  acceptance), every alias, the use log, the setting from Phase 1, and the mash herd's temporary names. The zone CLI and
  the zone-key files stay (3.5).
- **3.3 Loud refusal.** A refused name or grant names the rule it broke (REQ-020); mail to an old-form name is refused to
  the sender, not dropped. Mutation (AT-012): restoring old-form acceptance or an alias makes CI fail, shown once.
- **3.4 ADR and search.** Accepts the supersession of ADR 0005's and ADR 0054's layout parts; a search of the code shows
  no remaining compatibility path.
- **3.5 The zone CLI surface stays (the CLI freeze waits on Randy's ruling, D188).** None of these is removed
  by Phase 3, and none changes until D188 is settled; Phase 3 removes only the six-token subject form and its
  acceptance. Once subjects carry no zone, each item would behave as follows (our proposal; the behaviour is ours to
  propose, and the freeze waits on Randy's ruling on D188):
  1. `mashctl set-zone <name> <zone>` (`internal/ctl/ctl.go:71,296`, `cmd/mashctl/main.go:46`): **refuse** with a clear
     message that a zone no longer addresses mail and that `set-meta --label` is the way to label an endpoint, because
     its whole meaning was to move an endpoint's mail subject.
  2. `--zone Z` on `mashctl provision` (`internal/ctl/ctl.go:68`, `provisionArgs` at `:262`): **refuse** with the same
     message (it provisions a zoned inbox). On `mashctl add` (`cmd/mashctl/add.go:24,77`): **work on the label only**: it
     still sets the policy's subject label, prints that the mail subject does not change, and the policy compiler keeps
     grouping by it until the zone ruling.
  3. `--replace-zone-key`, `--remove-zone` and `--keep-zone` on `policy diff`, `policy apply` and `policy repair`
     (`cmd/mashctl/apply.go:41-78`, `cmd/mashctl/drift.go:34`): **unchanged**; they act on the account's signing keys,
     which are the zone gate and wait for the zone ruling.
  4. The zone sections of `mashctl policy show` (`cmd/mashctl/policy.go:272-276`): **unchanged**.
  5. The `zone` field of `mash who --json` (`cmd/mash/whofilter.go:104`, ADR 0049): **stays**, additive; it reads the
     record's field, which new-form records leave empty.
  6. Docs (`docs/mashctl.md`, `docs/policy-file.md`, `docs/install.md`) follow whatever each item does.
  Nothing else in the CLI names a zone.

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
2. **The name-acceptance rule (REQ-017, DEC-014 (c), SEAM-001 sentence 2).** Enforcement location is settled
   (retirement and mash-applied configuration) and the format is proposed above; what remains is the seam page, which
   the planner writes and Randy approves. Needed before 1.7.
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

## Rulings recorded 2026-11-10 (after the amendments)

- **Both forms stay until the last deployment has moved.** Phase 3 is not near, and it still waits on Randy's go.
- **D187: old names end by watch-then-cut.** At least 24 hours of real traffic with no old-form use, two clean audits a
  day apart, then Randy's go. A renamed message kind is accepted with a warning for one release, then refused. This
  governs Phase 3 and the 1.9 enumeration.
- **D186** (scrub plan, amendment 4) is decided as in amendment 9; **D193** approves the seam page at 1.0.0; **D188**
  (zones) keeps the zone CLI frozen until Randy rules.
