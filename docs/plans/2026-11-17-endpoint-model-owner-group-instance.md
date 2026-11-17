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

There is no default group (the maintainer's ruling). `mashctl endpoint create` takes `--owner` (from
config) and needs `--group`; a session names its group in `MASH_GROUP`. A missing group is a readable refusal with the
code `group_missing`. A solo developer names one group once and everything under the owner is open by default
(section 1.5).

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

The `alias` field of the record (ADR 0076) goes with the rest of the legacy code: the migration is one sitting with no
mixed window (section 2), so nothing reads two identities.

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
  record, stream-filter and permission code, and with them the alias read, the legacy lease and the bridge grants of
  ADR 0074 and 0076: 0.5.5 has no legacy-aware reader. The `migrate` command alone knows the legacy record shape, to
  read it once.
* `unzone` (zones are already gone, ADR 0091) stays only while a record still carries one; `migrate` refuses such a
  record and says to run `unzone` first.

### 1.6 Open points inside the model

1. There is no default group. A legacy name has no group, so `migrate` needs an explicit `--group`; a run without one is
   refused with `group_missing`.
2. Records in the form ADR 0074 built (three tokens, slot 1 an `agent_token`, off by default): the plan maps `(agent,
   owner, name)` to owner = the old owner, group = the old agent token, instance = the old name, because the old slot 1
   was a kind of endpoint and the old owner is the trust unit. I did not find any deployment using them. If one exists the
   mapping is the only judgment call in this plan; flagged for review.

## 2. Migration

The migration is **one sitting** (the maintainer's ruling): every endpoint of a deployment moves in one pass, by an
operator, with a clean rollback until the last step. There is no mixed window: no bridge grants, no alias read and no
legacy lease are built.

### 2.1 What the operator does, in order

1. **Upgrade the central tools.** Put `mashctl` 0.5.5 on the host that runs it and keep the 0.5.0 `mashctl`, `mash` and
   the plugin where they are: they are the rollback. Running endpoints change nothing.
2. **Plan.** `mashctl endpoint migrate --all --dry-run` plans every legacy endpoint and changes nothing (bare `migrate`
   exits 2). It prints, per endpoint, the new address, `planned` or `refused` (and why), and the step it would take.
   `--group G` is required (no default; `group_missing` otherwise) and applies to every endpoint in the run; `--name N`
   (one endpoint only) sets the instance when the legacy name exceeds 32 characters. `--json` is the machine form.
3. **Apply.** `mashctl endpoint migrate --all --apply --group G` makes the new identities (2.2) and leaves everything
   legacy untouched. To keep the endpoint's private key off the central host, each endpoint makes its own key first
   and the operator gives the folder of public halves, `<name>.pub`, to `--apply` (ctl names the flag; the endpoint-made
   key is the credential primitive of #698, section 3). Without a public half for a name, `--apply` mints the key and
   writes `<owner>.<group>.<instance>.creds` (mode 0600) as `add` does today.
4. **Sit.** Stop every session, then restart each with its new credentials under the 0.5.5 binary. Until a session is
   restarted it is old: old and new endpoints cannot reach each other (2.3), so the restart pass is the whole window.
5. **Check.** `mashctl audit` and the connected-check (2.2, `--finish`) show which new identities have connected.
6. **Finish, or roll back.** `mashctl endpoint migrate --finish --all` is the last step (2.2). Before it, `migrate
   --rollback --all` (ctl names the verb; it is `endpoint delete` on each new identity) undoes steps 3 and 4.

**Rollback is a requirement.** Before `--finish`: the old binaries and old credentials work unchanged (they are never
touched), every legacy inbox is intact with all its mail, and the new identities can be removed: record, stream and user
(revoked), each issue record marked, and **no flat key burned** (a rolled-back address can be migrated again). Rollback
refuses while a new inbox holds unread mail, with the count, unless `--force`: that mail is addressed to the new identity
and does not go back. **After `--finish` nothing is reversible**: the old keys are revoked, the legacy inboxes and records
are deleted.

### 2.2 What each step does

`--apply`, per legacy endpoint `N` of owner `O` (outcomes: `planned`, `migrated`, `unchanged` when already moved,
`refused`):

1. Resolve `O.<group>.<instance>`: the group is the required `--group`; the instance is the legacy name. A name over 32
   characters is `refused` until `--name`. A name or group the name rule refuses, a record that still carries a zone, an
   address already taken, and a flat key in the retired ledger are `refused` as well.
2. Take the name's lock and the admission lock; check admission (the overlap and burn checks).
3. Create the **new identity**: directory record `O.G.I`, inbox stream and consumer, the user (the public half signed by the
   account signing key, or a minted key), an issue record keyed by the user's public key with the flat key as address.
   **Copy the unread mail** of the legacy inbox into the new inbox. The record is written last, so a failed run leaves
   nothing half made and a run again finishes it.
4. Leave the **legacy record, legacy inbox and old credentials untouched.**

`--finish --all`: for each endpoint, repeat the mail copy (mail old senders sent after `--apply`), then revoke the old key,
delete the legacy inbox and the legacy record. It refuses an endpoint whose new identity has not connected, or whose
legacy inbox it cannot read, unless `--force`. It prints what it removed.

### 2.3 Old and new during the sitting: what breaks

Old and new use different subjects (`agents.mail.cc.O.N` against `agents.mail.O.G.I`) and nothing bridges them:

* Between `--apply` and a session's restart that session is still old; a restarted session cannot send to it and it cannot
  send to the new ones. Old senders' mail to `cc.O.N` lands in the legacy inbox and is copied at `--finish`.
* A session that is **not restarted** keeps working on its old credentials until `--finish`, then its connection is
  refused (2.4, step 6) and mail sent to its old address has no inbox and is lost to the sender. `--finish` refuses while
  a new identity has not connected, to catch this.
* `mash admin permissions` for a legacy name is gone in 0.5.5.

### 2.4 How it is proven live on one local server

On `mashctl init --local`, one NATS server, in the test environment the cluster tests use, with the 0.5.0 binaries
available for the old side:

1. Make two legacy endpoints `a` and `b` of owner `O` (legacy minting kept as test-only code), connect real endpoints
   with their old credentials, send `a` to `b` and back, and leave 3 messages unread in `a`'s inbox.
2. **The sitting.** `endpoint migrate --all --apply --group G`, giving `a` a public half (the endpoint made its key) and
   letting `b` be minted. Assert the new records, streams, users and issue records exist, `a`'s 3 messages are in its new
   inbox, and the legacy record, inbox and credentials are untouched. Restart both under the new credentials: each reads
   its mail once and they exchange mail on the new subjects.
3. **Rollback.** `--rollback --all` (with the new inboxes drained, and again with unread mail to see the refusal). Assert
   the old binaries and old credentials connect, `a` and `b` have all their mail, 0.5.0 `list`, `retire` and `audit` work
   with the new records gone, the new users are revoked, and no flat key is burned.
4. **The sitting again.** Repeat step 2; assert it succeeds on the same addresses.
5. **Finish.** `--finish --all`: assert the old credentials are revoked (a connect is refused), the legacy records and
   inboxes are gone, `audit` is clean, the new endpoints work, and the rollback is now refused.
6. **Revocation drops the connection.** Connect an endpoint, revoke its key, assert the connection closes and a reconnect
   is refused with an authorization error.
7. **Refusals.** A 40-character name is `refused` until `--name`; a missing `--group` is `group_missing`; `--apply` on a
   moved name is `unchanged`; `--finish` with a new identity that never connected refuses.
8. Kill `--apply` between its steps and run it again: it finishes and leaves one new identity.

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
| #698 credential primitive (ADR 0136) | a credential for a public key the endpoint made, so no private key crosses to the central host | **0.5.5 carve-out (maintainer's ruling)**, the rest to the policy release | As built it signs only under a list set and refuses `no_list_set`. 0.5.5 lifts `NewKey`, `Assemble` and `Show` out and puts a plain signing path under them, so `endpoint create` and `migrate --apply` take the endpoint's public key without any list set: about 1.5 days. The list-set path stays held. |
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
| Remove `add` and legacy `reissue`, `agent_token`, the legacy parsers, record code and alias machinery; legacy records in readers are reported, not errors; rewrite the golden-name and legacy tests | 4 |
| Migration: `migrate` (plan, apply with public halves and mail copy, rollback, finish and the connected-check) | 2.5 |
| Live proof (2.4: sitting, rollback, sitting again, revocation drop), docs (README SAP departure, `docs/sap.md`, `mashctl.md`, `operations.md`, name rule), ADR amendments, release note | 2 |
| Credential primitive carve-out (section 3, in 0.5.5) | 1.5 |
| **Core total** | **14** |
| ctl: the `endpoint` CLI face (create, reissue, retire, migrate) with JSON | ctl's estimate |

The risk is the third row: the legacy code runs through `admin`, `endpoint`, `store`, `ctl` and the commands, and many tests
carry legacy names. I count 12.5 with the golden-name tests rewritten, not deleted.

## 5. What is decided where

* ADR 0145 records the address model, the SAP departure, the permission shape, the removal of the legacy form and the
  migration contract. It amends ADR 0005 (subjects), 0006 (one owner per deployment becomes: one account, owners are
  slot 1), 0011 (the record), 0074 and 0076 (their alias machinery is removed), 0077 (rule file keys) and
  0102 (the group is slot 2, not the owner).
* The command names and flags belong to the CLI design that ctl writes (`mashctl endpoint create|reissue|retire|migrate`);
  the field names in this note (`owner`, `group`, `instance`, `address`, outcome words) are what ctl quotes in `--json`.

## 6. Addendum: the flag day, the alternatives costed, and the ruling

The first draft of this plan moved endpoints one at a time and bridged the window. The maintainer ruled for **one
sitting with a clean rollback** (this plan, section 2) and the endpoint-made key in 0.5.5. The costing behind it:

* **Bridge** (one at a time, over days): 12.5 core days without the credential carve-out, 14 with it.
* **Flag day** (`migrate --all --apply`, restart everyone, `--finish`; no bridge, alias read or legacy lease): 11.5 without
  the carve-out. The ruling adds the rollback (+0.5 in the migration row) and a longer live proof (+0.5), and the
  carve-out (+1.5): **14 days** in section 4. The 0.6.0 deletion of the alias code (about 1 day) is not needed.
* **No `migrate` verb at all** (the operator lists with the 0.5.0 `list --json`, creates each endpoint with
  `endpoint create`, restarts, reads leftovers with 0.5.0, retires with 0.5.0): about 11 to 11.5 days, so it saves
  nothing: readers must report legacy records instead of failing (about 1 day, at about 15 `ParseRecord` call sites), and
  `create` must carry over labels, description and caps. It removes the one command that shows the operator what will
  happen. Open (maintainer, option C); this plan keeps `migrate`.

What is not reversible: after `--finish` nothing is (section 2.1).
