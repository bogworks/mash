# Plan: one endpoint model (owner, group, instance), no legacy form, a hard cut

Date: 2026-11-17. Status: design for review; no code. Decision record: [ADR 0145](../decisions/0145-an-endpoint-address-is-owner-group-instance-and-0-5-5-is-a-hard-cut-with-no-migration-in-mash.md).
Scope: the 0.5.5 release. Standing orders: the address is a hierarchy of owner, then group, then instance (decision 240);
there are no legacy agents and nothing is built for them, not even a migration (maintainer, 2026-11-16 and 2026-11-17); mash ships
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

The `alias` field of the record (ADR 0076) goes with the rest of the legacy code (section 2).

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
* The legacy parsers (`sap.Legacy`, the `cc` branch of `KeyFromSubject`, `SplitMail`'s legacy acceptance), the legacy
  record, stream-filter and permission code, and with them the alias read, the legacy lease and the bridge grants of
  ADR 0074 and 0076. 0.5.5 does not read, convert or tolerate an old record, address or credential.

### 1.6 Open points inside the model

1. There is no default group (the maintainer's ruling): a missing group is refused with `group_missing`.
2. Records in the form ADR 0074 built (three tokens, slot 1 an `agent_token`, off by default): the plan maps `(agent,
   owner, name)` to owner = the old owner, group = the old agent token, instance = the old name, because the old slot 1
   was a kind of endpoint and the old owner is the trust unit. I did not find any deployment using them. If one exists the
   mapping is the only judgment call in this plan; flagged for review.

## 2. Migration: there is none in mash

The maintainer's ruling: the old endpoints are disappearing and will exist nowhere; no one uses mash yet; mash has no
command for a one-time use. 0.5.5 is a hard cut. It has no `endpoint migrate`, no rollback, no mail copy, no connected
check and no bridge, and it does not read, convert or tolerate an old record, address or credential. Moving the
maintainer's own endpoints is a one-time script outside mash, owned by operations. The release note says first that every
identity changes and every credentials file is replaced.

How it is proven live, on `mashctl init --local`, one NATS server: create endpoints of one owner in two groups, each
with an endpoint-made key; mail crosses between them; revoke a key and assert the live connection closes and the
reconnect is refused; retire an endpoint; a missing group is `group_missing`. Not tried: more than one server, TLS, two
central hosts.

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
| #698 credential primitive (ADR 0136) | a credential for a public key the endpoint made, so no private key crosses to the central host | **0.5.5 carve-out (maintainer's ruling)**, the rest to the policy release | As built it signs only under a list set and refuses `no_list_set`. 0.5.5 lifts `NewKey`, `Assemble` and `Show` out and puts a plain signing path under them, so `endpoint create` takes the endpoint's public key without any list set: about 1.5 days. The list-set path stays held. |
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
| Remove `add` and legacy `reissue`, `agent_token`, the legacy parsers, record code and alias machinery; rewrite the golden-name and legacy tests | 3 |
| Live proof (section 2), docs (README SAP departure, `docs/sap.md`, `mashctl.md`, `operations.md`, name rule), ADR amendments, release note | 1.5 |
| Credential primitive carve-out (section 3, in 0.5.5) | 1.5 |
| **Core total** | **10.5** |
| ctl: the `endpoint` CLI face (create, reissue, retire) with JSON | ctl's estimate |

The risk is the third row: the legacy code runs through `admin`, `endpoint`, `store`, `ctl` and the commands, and many tests
carry legacy names. I count 10.5 with the golden-name tests rewritten, not deleted. The earlier 14 included 2.5 for
`migrate`, 1 for classifying legacy records in readers and 0.5 for a longer proof, all of which go with the hard cut.

## 5. What is decided where

* ADR 0145 records the address model, the SAP departure, the permission shape, the removal of the legacy form and the
  hard cut. It amends ADR 0005 (subjects), 0006 (one owner per deployment becomes: one account, owners are
  slot 1), 0011 (the record), 0074 and 0076 (their alias machinery is removed), 0077 (rule file keys) and
  0102 (the group is slot 2, not the owner).
* The command names and flags belong to the CLI design that ctl writes (`mashctl endpoint create|reissue|retire`);
  the field names in this note (`owner`, `group`, `instance`, `address`) are what ctl quotes in `--json`.
* Key handling (from the 0.5.5 contract, revision 3; the verbs are ctl's): the endpoint-made key is the default, through
  `mash key create` and `mash credential create`; the same-host exception (proposed `--make-key`) is pending DEC-002.
  The JWT always goes to a file (`--jwt-out`) and is never printed. There is no stored group: no config key holds it, it
  is given on each create and by `MASH_GROUP` in a session. `credential revoke` is out: replacing a key goes through
  `reissue --public-key`.
