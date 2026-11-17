# Plan: one endpoint model (owner, group, instance), no legacy form, one migration command

Date: 2026-11-17. Status: design for review; no code. Decision record: [ADR 0145](../decisions/0145-an-endpoint-address-is-owner-group-instance-and-existing-endpoints-move-by-one-migration-command.md).
Scope: the 0.5.5 release. Standing orders: the address is a hierarchy of owner, then group, then instance (decision 240);
there are no legacy agents and nothing is built for them beyond how they are moved (maintainer, 2026-11-16); mash ships
wide open; no policy in this release; mash works alone first, so everything below is for a solo developer with
`mashctl init --local` before it is for anyone else.

## 1. The address model

### 1.1 The three slots

SAP's subject has three slots after the verb: `agents.<verb>.<slot1>.<slot2>.<slot3>`. SAP calls them agent, owner and
name. mash fills them with **owner, group, instance**:

| Slot | SAP's word | mash's word | Meaning |
|---|---|---|---|
| 1 | agent | owner | who the endpoint belongs to; the unit of trust |
| 2 | owner | group | a set of an owner's endpoints that belong together (a project, a team, a machine) |
| 3 | name | instance | one endpoint |

Examples: `agents.mail.randy.web.build-1`, `agents.prompt.randy.web.build-1`. Trust follows the path: what a credential
may reach is a prefix of this path, and every level that needs a rule is its own token, so a NATS wildcard (which matches
whole tokens only) can say it. No level is joined into another with a dash.

A solo developer never needs to know that groups exist: `mashctl endpoint create` takes `--owner` (from config) and
defaults `--group` to `main`, and everything under the owner is open by default (section 1.5).

Each token keeps today's rule: `[a-z][a-z0-9-]*`, 1 to 32 characters, no `--`, no trailing dash. The whole address,
dots included, is at most 64 characters. The name rule file (ADR 0077) is read per slot; its keys become `owner`, `group`
and `instance` in a new version of the file (the old keys `agent` and `name` are refused with a message that names the
new keys).

### 1.2 Departure from SAP, stated plainly

SAP's text says the first slot SHOULD carry the agent (the harness, for example `claude-code`). mash does not follow that
for the first slot. Two consequences go in the documentation, in the README's SAP section and in `docs/sap.md`:

* **Finding agents by a wildcard on the harness slot does not work with mash.** A client that subscribes or requests
  `agents.*.claude-code.>` finds nothing, because mash's first slot is the owner. A client that discovers by the
  service name (`$SRV.INFO.agents`) still finds every mash endpoint, and reads `metadata.agent`.
* **The harness stays truthful in the service metadata.** `metadata.agent` stays `claude-code` (the endpoint's real
  harness). The metadata also carries `owner`, `group` and `instance`; `session` stays the flat key. mash does not put the
  harness in a subject token.

No SAP protocol change and no SDK divergence: the subject has the same shape and the same number of tokens; only what mash
puts in them differs, and the one place SAP says SHOULD is documented as a departure.

### 1.3 What each stored thing becomes

| Thing | Today (legacy) | After |
|---|---|---|
| Address | `cc.<owner>.<name>` (the `cc` constant in slot 1) | `<owner>.<group>.<instance>` |
| Flat key (lease, stream, spool and state paths, creds name) | the bare name | `<owner>_<group>_<instance>` (no token holds `_`, so it is unambiguous) |
| Directory record (`mash-directory`) | key = name; fields `name, owner, agent, agent_token, ...` | key = flat key; fields `owner, group, instance`, plus the unchanged `account, user, mail_subject, created, note, role, host, labels, description, caps, alias`; `agent` stays the harness name (`claude-code`); `agent_token` and `name` are gone |
| Inbox stream | `inbox-<name>`, filter `agents.mail.*.<owner>.<name>` | `inbox-<flat>`, filter `agents.mail.<owner>.<group>.<instance>` |
| Lease (`mash-leases`) | key = name | key = flat key; tombstone as today (ADR 0015) |
| Retired names | not burned | burned by flat key for every deprovision (ADR 0088 applies to the whole model) |
| Issue record (`mash-issued`) | `address` = name | `address` = flat key |
| Creds file | `<name>.creds` | `<owner>.<group>.<instance>.creds` |
| Micro service metadata | `agent, owner, session, ...` | `agent` (harness), `owner, group, instance, session, ...` |

The `alias` field of the record (ADR 0076) stays, with one job: it names the legacy identity an endpoint is moving from,
and it exists only during the migration window (section 2).

### 1.4 The credential's permissions

All permission text derives from the address. For an endpoint `O.G.I` with flat key `F`:

* Publish: `agents.mail.O.*.*`, `agents.prompt.O.*.*`, `agents.status.O.*.*` (the whole owner, open by default), its
  own heartbeat `agents.hb.O.G.I`, and the fixed lines it has today: `_INBOX.>`, `$SRV.>`, `$JS.API.INFO`, its own inbox
  stream and consumer (info, next, ack, direct-get), the directory read, and its own lease key `$KV.mash-leases.F`.
* Subscribe: `_INBOX.F.>`, its own `agents.prompt.O.G.I` and `agents.status.O.G.I`, `$SRV.>`, and
  `agents.hb.O.*.*`.

The owner is a literal token in every peer line, so no credential can reach another owner's endpoints. The group and
instance tokens are `*` only because the default is wide open; narrowing to a group (swapping `*` for `G` in the peer
lines) is something a user chooses later, in the reach-policy work, not in this release. `ValidateGrant` now forbids a
wildcard in slot 1 (the owner) instead of the old agent slot. `mash admin permissions` takes an `OWNER.GROUP.INSTANCE`
and prints this set.

### 1.5 What is removed

* `mashctl add`, `reissue` of a legacy name, and the `cc` / two-part legacy form: `add` is replaced by
  `mashctl endpoint create`, `reissue` and `retire` by `endpoint reissue` and `endpoint retire` (one governed path;
  ctl designs the command face).
* `agent_token` / `MASH_AGENT_TOKEN`: there is one form, so there is nothing to choose.
* The legacy parsers (`sap.Legacy`, the `cc` branch of `KeyFromSubject`, `SplitMail`'s legacy acceptance) and the legacy
  record, stream-filter and permission code. The one exception is the migration window's read-only machinery in
  section 2.3, which is deleted in 0.6.0.
* `unzone` (zones are already gone, ADR 0091) stays only while a record still carries one; `migrate` refuses such a
  record and says to run `unzone` first.

### 1.6 Open points inside the model

1. The default group is `main`. A legacy name has no group, so `migrate` needs one; section 2.2 says how it is chosen.
2. Records in the form ADR 0074 built (three tokens, slot 1 an `agent_token`, off by default): the plan maps `(agent,
   owner, name)` to owner = the old owner, group = the old agent token, instance = the old name, because the old slot 1
   was a kind of endpoint and the old owner is the trust unit. I did not find any deployment using them. If one exists the
   mapping is the only judgment call in this plan; flagged for review.

## 2. Migration

### 2.1 What the operator does, in order

The migration is the cut-over of every identity in a deployment, one endpoint at a time. The operator:

1. **Upgrade the central tools.** Put `mashctl` 0.5.5 on the host that runs it. Nothing about running endpoints changes;
   they keep their credentials and their 0.5.0 binary.
2. **Plan.** `mashctl endpoint migrate` with no names plans every legacy endpoint and changes nothing. It prints, per
   endpoint, the new address, whether it is `planned` or `refused` (and why), and what step it would take. `--json` is the
   machine form. `--group G` sets the group for every endpoint in the run; `--instance NAME` (one endpoint only) sets the
   instance when the name does not fit.
3. **Migrate one endpoint.** `mashctl endpoint migrate NAME --apply` does the account-side work (2.2). It prints the
   `next_steps`: where the new creds file is, and that the endpoint's session must be restarted with it under the 0.5.5
   binary.
4. **Move the endpoint.** The operator restarts that endpoint's session with the new creds and the 0.5.5 binary. The new
   endpoint takes the lease for the flat key and the legacy lease for the old name, reads the retained legacy inbox
   (2.3), and carries on. Repeat 3 and 4 for each endpoint, in any order, over any number of days.
5. **Finish.** When every endpoint has moved, `mashctl endpoint migrate --finish` (per name or `--all`) revokes the old
   credentials, ends the bridge (2.3), deletes the legacy streams once they are drained and the legacy records, and
   reports what it removed. `audit` reports the same state while the window is open (which endpoints are legacy, which
   moved, which legacy inboxes are not yet drained) and is clean afterwards.

### 2.2 What `--apply` does to one legacy endpoint

For the legacy endpoint `N` of owner `O` (outcome words: `planned`, `migrated`, `unchanged` when already moved,
`refused`):

1. Resolve the new address `O.<group>.<instance>`: the group is `--group` if given, else the deployment's default group
   `main`; the instance is the legacy name. A legacy name can be up to 48 characters, a token is at most 32: a longer name
   is `refused` with the reason, and the operator gives `--instance`. A name or group the name rule refuses is `refused`
   as well, as is a record that still carries a zone, an address already taken, and a flat key in the retired ledger.
2. Take the name's lock and the admission lock, and check admission (the existing overlap and burn checks).
3. Create the **new identity**: a directory record `O.G.I` whose `alias` names the legacy identity, a new inbox stream and
   its consumer, a new NATS user (a new key pair, and a new creds file next to the old one, mode 0600), recorded in the
   issue record. The record is written last, so a failed run leaves nothing half made; a run again finishes it.
4. Add the **bridge grants** to the new user (2.3).
5. Leave the **legacy record, legacy inbox stream and old credentials untouched**. Nothing is revoked here: the old
   session keeps working until the operator moves it.

Mail is carried over by not moving it. Mail that is in the legacy stream when the endpoint moves, and mail that old
senders send afterwards, is read by the new endpoint through the legacy stream (2.3); nothing is copied, nothing is
lost, and nothing is read twice because the lease on the old name allows one reader. Whether the mail already read stays
read is the consumer's state, unchanged.

The old credentials are revoked at `--finish`, not at `--apply`: revoking at apply would cut a running session off before
it could be moved. `--finish NAME` for one endpoint revokes that endpoint's old key only; it refuses while its legacy
inbox is not drained, with the count, unless `--force` says to drop it.

### 2.3 Mixed old and new: what reaches what while endpoints move

Old and new endpoints address each other by different subjects (`agents.mail.cc.O.N` against `agents.mail.O.G.I`). Left
alone, neither can reach the other: the legacy peer lines name `*` in slot 1 and the owner in slot 2, the new ones name
the owner in slot 1. The window is bridged in both directions, with machinery that already exists (ADR 0076's alias
window), so no new moving part is invented:

* **Old to new.** An old sender still publishes to `agents.mail.cc.O.N`. That is the retained legacy inbox stream, and
  the new endpoint drains it (the alias read of ADR 0076: two streams, one session, the legacy lease so exactly one reader
  holds the old name). So an old endpoint reaches a moved one, with the same delivery and signing as before.
* **New to old.** A moved endpoint addresses an endpoint that is still legacy by its directory record, which still says
  `cc.O.N`. Its credential carries, while the window is open, the bridge grants: publish on `agents.{mail,prompt,status}.cc.O.*`
  and subscribe to `agents.hb.cc.O.*`. These are the alias bridge grants that exist today, widened from one name to the
  owner's legacy names, and they are the whole of what widens.
* **Two moved endpoints** use the new subjects only.
* **What breaks.** (a) An old endpoint under the 0.5.0 binary cannot serve a new-form record: it cannot be migrated in
  place, only moved (step 4), which is why the old and new session never overlap on one name. (b) While the window is open
  the new endpoints hold the wider bridge grants; `--finish` removes them. (c) `who` and the directory show a legacy and
  a moved endpoint under different addresses until the window closes. (d) `mash admin permissions` for a legacy name is gone
  from 0.5.5; the old creds keep their old permissions until revoked. (e) A name the operator forgot is not broken, just
  legacy: it keeps working until `--finish --all`, which lists any endpoint that never moved and refuses to remove its
  record unless told to.

This machinery (the alias read, the legacy lease, the bridge grants, the legacy `audit` drain check) is the only
legacy-aware code left in 0.5.5 and is deleted in 0.6.0.

### 2.4 How it is proven live on one local server

On `mashctl init --local`, one NATS server, in the test environment (the `testenv` that the cluster tests use), with
no second server and no outside system:

1. Make two legacy endpoints `a` and `b` of owner `O` with the legacy minting kept as test-only code (the CLI `add` is
   gone), connect real endpoints with their old credentials, and send `a` to `b` and back (proves the starting state).
2. Leave 3 messages unread in `a`'s inbox. `endpoint migrate a --apply`: assert the new record, stream and creds exist, the
   legacy record, stream and creds are untouched, and `a` (old creds) still connects and receives.
3. Move `a`: start an endpoint with the new creds. Assert: it reads the 3 messages through the legacy stream, once each;
   `b` (legacy) sends to `a` and it arrives; `a` (moved) sends to `b` and it arrives; a second reader of the old name is
   refused by the lease.
4. Migrate and move `b`; assert two moved endpoints exchange mail on the new subjects only.
5. `migrate --finish --all`: assert the old creds are revoked (a connect with them is refused, the revocation visible in the
   account JWT), the legacy records and drained streams are gone, the bridge grants are gone from the new users' JWTs,
   `audit` is clean, and the new endpoints still work.
6. Refusals: a 40-character name is `refused` until `--instance`; a re-run of `--apply` on a moved name says `unchanged`;
   `--finish NAME` with an undrained legacy inbox refuses with the count.
7. Kill the run between steps 3 and 4 of `--apply` and run it again: it finishes and leaves one new identity.

Not tried and said so in the release notes: more than one server, TLS, two central hosts.

## 3. Triage of the held stack

Every PR of the held stack builds the policy path (a list set, its engine, its issue service). 0.5.5 has no policy, so
none of them belongs in it as built. For each: where it belongs, and what survives.

| PR | What it is | Where | Notes |
|---|---|---|---|
| #676 ledger bucket | the policy engine's own bucket, default `mash-policy`, optional setting | policy release | No command uses it in 0.5.5. |
| #683 the old scope word removed (ADR 0132) | the engine's scope is the account, an owner is the group, the address is `owner.name`, the agent token is wildcarded | policy release, **rewritten** | Its address model (`owner.name`, owner = group) conflicts with owner/group/instance; the engine must be re-keyed by the new address. Removing the old scope word from the engine's text is worth keeping and moves with it. |
| #685 requester rename (ADR 0133) | rename of the countersigner inside the issue service | policy release | Names a thing only the policy path has. |
| #686 reconcile fix | a finding in `policy reconcile`'s logic | policy release | Engine-internal; no shipped surface. |
| #688 policydirect | the direct driver of the policy engine, two-server live proof, no verb | policy release | The engine's driver for the later `policy` verb. Carries the `scripts/gate` exception. |
| #698 credential primitive (ADR 0136) | a credential for a public key the endpoint made, so no private key crosses to the central host | policy release as built; **0.5.5 can take a carve-out** | As built it signs only under a list set and refuses `no_list_set`. `endpoint create` in 0.5.5 can offer the same property (the endpoint makes its key, `mashctl` signs the public key) without any list set: that is about 1.5 days of work, lifting `NewKey`, `Assemble` and `Show` out and putting a plain signing path under them. Not done unless the maintainer wants the property in 0.5.5. |
| #702 (closed) | a legacy endpoint governed in place | nowhere | Closed; there is no legacy in 0.5.5. |

Nothing in the stack is needed by 0.5.5 (the model needs `sap`, `store`, `admin`, `endpoint`, `config`, `namerule`,
`mint`, `ctl`; the stack touches none of the first four in ways the model uses). The one cost: they were stacked on a main
that is moving, and the policy release will need to re-key them by the new address; rebasing them now buys nothing.
I close nothing.

## 4. Size

Person-days, one person with Sonnet subagents doing the code under review (the repository's rule); calendar time is less
if two run at once.

| Piece | Person-days |
|---|---|
| Slots and tokens: `sap` identity and subjects, store record and key, admission, name rule version | 2 |
| Permissions, mint, creds naming, lease, inbox stream, spool and state paths, metadata, discovery | 2.5 |
| Remove `add` and legacy `reissue`, `agent_token`, the legacy parsers and record code; rewrite the golden-name and legacy tests | 3 |
| Migration: `migrate` (plan, apply, finish), the bridge grants, `audit` window state | 3 |
| Live proof (2.4), docs (README SAP departure, `docs/sap.md`, `mashctl.md`, `operations.md`, name rule), ADR amendments, release note | 2 |
| **Core total** | **12.5** |
| ctl: the `endpoint` CLI face (create, reissue, retire, migrate) with JSON | ctl's estimate |
| Optional carve-out of the credential primitive (section 3) | +1.5 |

The risk is the third row: the legacy code runs through `admin`, `endpoint`, `store`, `ctl` and the commands, and many tests
carry legacy names. I count 12.5 with the golden-name tests rewritten, not deleted.

## 5. What is decided where

* ADR 0145 records the address model, the SAP departure, the permission shape, the removal of the legacy form and the
  migration contract. It amends ADR 0005 (subjects), 0006 (one owner per deployment becomes: one account, owners are
  slot 1), 0011 (the record), 0074 and 0076 (the alias window becomes the migration window), 0077 (rule file keys) and
  0102 (the group is slot 2, not the owner).
* The command names and flags belong to the CLI design that ctl writes (`mashctl endpoint create|reissue|retire|migrate`);
  the field names in this note (`owner`, `group`, `instance`, `address`, outcome words) are what ctl quotes in `--json`.

## 6. Addendum: the flag-day alternative, costed

The plan above moves endpoints one at a time and bridges the window. With one user and a few endpoints the bridge may not
earn its code. The alternative, **flag day**: no bridge grants, no alias read, no legacy lease are built or kept.

**What the operator does.**

1. `mashctl endpoint migrate` (no names) plans every endpoint and changes nothing, as in 2.1.
2. `mashctl endpoint migrate --all --apply` makes every new identity at once (record, inbox stream, key, creds file) and
   leaves the legacy records, inboxes and old credentials untouched. For each endpoint it copies the unread mail of the
   legacy inbox into the new inbox, so mail is carried over by copy, not by a second read path.
3. The operator stops every session and restarts each with its new creds file under the 0.5.5 binary.
4. `mashctl endpoint migrate --finish --all` runs right after: it revokes every old credential, deletes the legacy inboxes
   and removes the legacy records. It refuses while an endpoint has not connected under its new identity, unless `--force`.

**What it saves.** The bridge grants, the alias read (two streams, one session), the legacy lease, the `audit` window
state and the mixed-window tests are never built for 0.5.5, and the ADR 0076 alias code is deleted now instead of in
0.6.0. The cost moved in is the copy of unread mail at `--apply` and a connected-check at `--finish`.

**What breaks.**

* Between steps 2 and 3, old and new sessions cannot reach each other: old senders publish to `cc.O.N`, which the new
  inboxes do not carry; new credentials do not reach the legacy subjects. A restarted session cannot send to one not yet
  restarted. The operator's restart pass is the whole window, so it should be short: stop all, restart all.
* A session that is **not restarted** keeps working on its old credentials until step 4 and then is cut off: its
  connection is refused and mail sent to its old address after `--finish` has no inbox and is lost to the sender
  (publishing to a subject with no stream). Mail that reached the legacy inbox between `--apply` and `--finish` is not
  lost: `--finish` repeats the copy per endpoint first, and refuses on a legacy inbox it cannot read.
* There is no way to move one endpoint and wait: a single endpoint can be migrated (`NAME --apply`), but it cannot talk to
  the others until they have all moved.
* The move is not reversible after step 4, as before.

**Size, in person-days (core).** Flag day replaces the migration row (3) with 2: plan, `--all --apply`, the mail copy,
the connected-check and `--finish`; the live proof drops from 2 to 1.5 (no mixed-window steps); and the removal row rises
from 3 to 3.5 because the alias machinery goes now. Net: **11.5 days against 12.5** in 0.5.5, and the 0.6.0 deletion of
the alias code (about 1 day) is not needed. The bridge buys the ability to move one session at a time over days; it costs
about 1 day now and 1 day later.

**Size with and without the credential-primitive carve-out (section 3, #698).**

* Without the carve-out: 12.5 days (bridge) or 11.5 days (flag day).
* With the carve-out: 14 days (bridge) or 13 days (flag day).

**Recommendation.** For one user with a few endpoints the flag day is enough: the cost of the bridge is real code that is
deleted again in 0.6.0, and the only thing it buys is moving sessions on different days. If any endpoint cannot be
restarted in one pass, the bridge earns its place; that is the maintainer's call, and the ADR (0145) records whichever is
chosen. The ADR text above describes the bridge and changes if the flag day is chosen.
