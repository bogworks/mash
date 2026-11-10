# Zone and owner inventory (REQ-009, AT-007)

Scrub plan step 3 (`docs/plans/2026-11-10-scrub-remainder.md`). Tree: branch `pr/core/scrub-zones`, on top of `main`
at 0597c0c. Read "zone" also as "owner" where the contract says a grouping of an outside system appears in mash as a
zone; so this lists both.

REQ-009: mash must not add or keep a configuration field, flag, environment variable, policy input or code path that
translates, aliases or infers a zone (or another mash primitive: owner, agent token, name) from an outside identifier,
a name's parts, or a grouping mash does not define. A zone or an owner reaches mash as itself, through an interface
mash already has, and is used as given.

This pull request changes no behaviour. Where a place below derives a value from something else, it is flagged in the
last column and collected in "Candidate contract gaps"; none is removed here.

How to read the "Does" column: **given** = used as given (compared or placed in a subject, byte for byte);
**validated** = checked against a syntax and refused when it does not match, never repaired; **transformed** = the
value becomes something else.

## 1. Configuration (owner)

| Input | Source | Comes from | Does | Translation, alias or inference of an outside identifier? |
|---|---|---|---|---|
| `Config.Owner` (SAP owner token) | `internal/config/config.go:38` | resolved config | given | No |
| `"owner"` in config.json | `internal/config/config.go:84`, copied at `:319` | user config file | given (then validated, row below) | No |
| `MASH_OWNER` | `internal/config/config.go:78` (name map), `:362` | environment, overrides the file | given | No |
| owner required, validated (only when `MASH_NAME` is set: `Load` returns before these checks when the name is empty, `internal/config/config.go:453`; command-specific owner checks are in the admin commands) | `internal/config/config.go:465-473` (`ErrNoOwner` at `:132`) | the two rows above | validated by `ValidName` (1-48 chars, lower-case letters, digits, single hyphens, letter first); an empty owner is refused, not defaulted | No. Nothing fills a missing owner from `MASH_NAME`, the host or the directory (test `TestOwnerNotDerived`) |
| `MASH_NAME` / endpoint name | `internal/config/config.go:40`, `:363`, `:456` | environment | validated by `ValidName` | No. The name is not split into owner and name parts |
| `MASH_HOST` / `"host"` | `internal/config/config.go:319`, `:78` | user config | given, as record metadata only | No. It never feeds owner or zone (test `TestOwnerUsedAsGiven` sets a host that differs from the owner) |
| owner as the state-directory path | `internal/spool/spool.go:56` (`Dir`), uses at `cmd/mash/hook.go:42`, `cmd/mash/serve.go:271,803`, `cmd/mash/status.go:384`, `internal/endpoint/relay.go:307` | `cfg.Owner`, `cfg.Name` | transformed into a path `<state>/spool/<owner>/<name>`; each token placed as given | No. A path built from two mash values |
| `mashctl` target line (owner and its source) | `cmd/mashctl/target.go:46-49,70-75` | `cfg.Owner` | given (printed with its source) | No |

## 2. Flags and commands

| Input | Source | Comes from | Does | Translation, alias or inference of an outside identifier? |
|---|---|---|---|---|
| `mash who --owner O` | `cmd/mash/whofilter.go:12,43,56`, filter at `internal/endpoint/whofilter.go:16,72` | flag | given: exact string equality with a record's owner (the other fields are substring matches) | No |
| `mash who --json` fields `owner`, `zone` | `cmd/mash/whofilter.go:95,104,114` | directory record | given (output only) | No |
| `mash who` long output, `zone` | `cmd/mash/serve.go:733` | directory record | given (output only) | No |
| `mashctl adopt --owner O` | `cmd/mashctl/adopt.go:39,43,52,139-147` | flag, else `cfg.Owner` | given, then validated by `ValidName`; written to config.json at `:385` | No for the owner. **See flag A1 for `--account`** |
| `mashctl adopt` default account | `cmd/mashctl/adopt.go:149-152` | **derived from the owner** | `account = owner` when `--account` is not given; the account name becomes the owner string | **Yes, derivation of another primitive (the account name) from the owner. Flag A1.** The check at `:449` compares it with the account JWT's name and fails with "pass --account", so a mismatch is refused, not silently used |
| `mashctl init --local --owner O` | `internal/ctl/local.go:30,42,48,64-71` | flag, else `cfg.Owner` | given, validated; written to config.json (`:116`, `:137`) and to the bootstrap plan (`internal/bootstrap/bootstrap.go:62,140,223`) | No |
| `mashctl provision NAME --zone Z` | `internal/ctl/ctl.go:68,99,538,547,565` | flag | validated by `sap.ValidZone` (1-32, lower-case letters, digits, hyphens); passed as `Meta.Zone` | No |
| `mashctl set-zone NAME ZONE` | `internal/ctl/ctl.go:71,101,296-310`, `internal/admin/admin.go:930-991`; listed in `cmd/mashctl/main.go:46` | argument | validated by `ValidZone`; the argument `-` is refused (no return to the legacy subject); a record already in another zone is refused | No |
| `mashctl add NAME --zone Z` | `cmd/mashctl/add.go:24,31,77-81,442-465` | flag | transformed into a label: `labels[<subject_label>] = Z`, then the policy file's label is the zone (row in section 4). A conflicting `--label zone=...` is an error | **Partly. Flag A2**: the zone is given as itself, but it travels as a label whose key the policy file chooses (`subject_label`) |
| `mashctl add` zone change notice | `cmd/mashctl/add.go:209-250` | policy compile | given (messages only) | No |
| `mashctl policy apply|diff --replace-zone-key Z`, `--remove-zone Z`, `--keep-zone Z` | `cmd/mashctl/apply.go:33-35,41,43,66-79`, `internal/apply/apply.go:140-145` | flags | given: compared with the zone names found in the policy file, key store and account JWT | No |
| `mashctl policy repair --keep-zone Z` | `cmd/mashctl/drift.go:34`, `:571-574` | flag | given | No |
| `mashctl reissue` zone key lookup | `cmd/mashctl/reissue.go:270,443-457` | the compiled endpoint's zone | given: `keys.ZoneKey(ep.Zone)` | No (but see flag A3 for where `ep.Zone` can be `default`) |
| `mashctl policy check` owner fallback | `cmd/mashctl/policy.go:164,187-190` | **literal `"owner"`** | when no owner is configured, the compile of a check uses the string `owner` as the owner token | **Yes, a made-up owner. Flag A4.** Used for `check` only (offline syntax and rule check); `policy show` instead refuses without an owner (`:226-229`) |
| `mashctl policy apply`, `repair`, `add`, `retire`, `label`, `reissue`: owner from config | `cmd/mashctl/apply.go:240-249`, `drift.go:183-189`, `add.go:291,311`, `retire.go:135,165`, `label.go:124,175`, `reissue.go:87` | `cfg.Owner` | given to `policy.Compile`; an empty owner is refused (`apply`, `repair`) | No |
| `mashctl` commands that need `cfg.Owner` | `internal/ctl/ctl.go:152,175,209,220-223` | `cfg.Owner` | given to `admin.New` and the permission builders; empty is refused | No |
| `mash` CLI `Discover(owner, name)` | `cmd/mash/cli.go:183`, `internal/endpoint/service.go:392-410` | `cfg.Owner` and an argument name | given: builds `sap.For(owner,name).Prompt` and matches the answer's `owner` metadata exactly | No |

## 3. Directory record, subjects and the sender path

| Item | Source | Comes from | Does | Translation, alias or inference of an outside identifier? |
|---|---|---|---|---|
| `Record.Owner` | `internal/store/store.go:163` | `admin.Provision` writes `a.owner` (`internal/admin/admin.go:219`) | given | No |
| `Record.Zone` | `internal/store/store.go:184` | `Meta.Zone` at provision (`admin.go:220`), `SetZone` (`admin.go:973-975`) | given; an empty zone means the legacy 5-token subject, not the default zone (comment `store.go:181`) | No |
| `Record.MailSubject` | `internal/store/store.go:187-192` (`MailSubjectFor`), check at `:283` | derived from owner, zone and name | transformed (joined by dots); a record whose stored subject differs from the derived one is refused | No. Built from three mash values; never read back to set a zone |
| `ParseRecord` zone check | `internal/store/store.go:280-282` | stored record | validated by `ValidZone`; not repaired | No |
| inbox stream subjects | `internal/store/store.go:97-107` (`InboxConfigZoned`), `internal/admin/admin.go:239-262,334-362` | owner, zone, name | given, joined by dots | No |
| zone recovered from a stream filter | `sap.ZoneOfMailFilter` (`internal/sap/subjects.go:79`), used at `internal/admin/admin.go:259,359` | an existing inbox stream's subject list | **the zone is read back from a subject token** and then re-added to the stream config as that same filter | **Yes, in form: a zone is read out of a subject.** It is mash's own stream filter for the same owner and name, exact 6-token form, validated by `ValidZone`; the value only restores what is already there. Flag A5 (low) |
| `sap.For(owner,name)` | `internal/sap/subjects.go:23` | owner, name | given | No |
| `sap.ForZone(owner,zone,name)` | `internal/sap/subjects.go:63` | owner, zone, name | given | No |
| `sap.MailFilter`, `sap.MailFilterZone` | `internal/sap/subjects.go:32,73` | owner, zone, name | given | No |
| `sap.SplitMail` | `internal/sap/subjects.go:89` | a mail subject | splits on dots into owner, zone, name (5 tokens: zone empty) and returns the tokens unchanged. It has no caller outside tests | No inference: it does not split inside a token. Nothing calls it in shipped code (checked with grep, only tests) |
| `sap.NameFromSubject` | `internal/sap/subjects.go:116`, use at `internal/endpoint/directory.go:72` | the `subject` of a verified `$SRV.INFO` answer | **the endpoint name is read from the 5th token of a subject** | **Yes, in form. Flag A6.** Done only for an answer whose `owner` metadata equals this endpoint's owner and whose `id_sig` over that subject verifies (`directory.go:64,73`); the name is then matched with the directory's key for it. It is a mash subject, not an outside identifier |
| `sap.ValidZone`, `MaxZoneLen` | `internal/sap/subjects.go:42-58` | any zone value | validated (1-32 of a-z, 0-9, `-`); a rejected zone is refused | No |
| `sap.DefaultZone` = `"default"` | `internal/sap/subjects.go:39` | constant | **assigned by nothing.** No non-test code in `internal/sap` or elsewhere reads it (grep: the only uses of the identifier outside tests are its definition and the same-named constant in `internal/policy`, row 4). The comment says a record without a zone is not in it. Tests use it only as an example value | No (but see flag A3 for the other constant of the same name) |
| sender identity `id_sig` over owner | `internal/sap/sender.go:153-170` | the endpoint's config | given: the owner is signed as given | No |
| sender name shown | `internal/endpoint/mail.go:55-66` (`shownName`), `:694` | sender header | validated by `ValidName`, else a short form of the key | No |
| sender's owner | `internal/endpoint/mail.go:334,722-726` (`ownerOf`) | the live directory copy, looked up by sender name | given; empty when the directory does not know the sender | No. The owner is the record's, not derived from the name |
| display "owner/name" | `internal/render/render.go:229-238` (`Sender`) | sender's directory owner, receiver's owner | transformed only by joining `owner + "/" + from` when the owners differ; the name alone when the sender's owner is unknown or equal | No. Two mash values joined with a slash; nothing is parsed back from it |
| instruction text | `internal/endpoint/instructions.go:12-28` | `cfg.Name`, `cfg.Owner` | given (printed) | No |
| `version` and `status` text | `cmd/mash/serve.go:643-644`, `cmd/mash/status.go:86-89,141-166` | `cfg.Owner` | given (printed) | No |
| `mash hook` "owner" | `cmd/mash/hook.go:57-79`, `internal/endpoint/hook.go:65-104`, `internal/spool/spool.go:64,441,494`, `cmd/mash/serve.go:271` (`adapter.OwnerToken`) | the session that holds the spool | **not the mash owner.** This "owner" is the process or session that owns a spool (a different meaning of the word) | No. Listed so a reader does not confuse it with the SAP owner |

## 4. Policy file and the compiler

| Input | Source | Comes from | Does | Translation, alias or inference of an outside identifier? |
|---|---|---|---|---|
| `subject_label` | `internal/policy/policy.go:38,111-116`, set to `"zone"` at `internal/policy/parse.go:53`, checked at `internal/policy/check.go:63` | policy file | the **key of the label** whose value is the zone; any valid label key is accepted | **Yes, an alias of a kind. Flag A2.** An operator can name any label (for example `team`) and its value becomes the zone. The value is not translated, but the grouping that defines the zone is the operator's choice of label, not a zone field. Existing behaviour (ADR 0054) |
| endpoint label `<subject_label>` | `internal/policy/policy.go:119-124` (`zoneOf`) | `endpoints.<name>.labels` in the policy file | given; validated by `ValidZone` at `check.go:82` | No |
| implicit zone `"default"` | `internal/policy/policy.go:21-22,119-124,127-137` (`zoneOf`, `effectiveLabels`); used by `internal/policy/plan.go:47-48` | **derived: an endpoint with no label of the subject label's key** | transformed: its zone is the constant `DefaultZone` = `"default"`; selectors match it as if the label were set; the stored labels stay unchanged (`internal/policy/implicit_zone_test.go`) | **Yes. Flag A3.** A zone is assigned where the file names none. Reaches the account JWT (a zone key `default`), the endpoint's tags and permissions, and, through `mashctl add`, the directory record: `add` passes `Zone: ep.Zone` (`cmd/mashctl/add.go:368`), so an endpoint declared with no zone is provisioned in zone `default` with the 6-token mail subject |
| zones of the compile | `internal/policy/plan.go:42-54`, `internal/policy/compile.go:70-90` | the set of `zoneOf` values | given: one scoped signing key per zone; `p.zones` is the set of values seen | No beyond A3 |
| compile owner | `internal/policy/compile.go:70-78` | `owner` argument | validated by `ValidName`; stored in `Compiled.Owner` | No (but see A4 for `policy check`) |
| zone subject patterns | `internal/policy/compile.go:57-62` (`MailSubjectPattern`) | owner, zone, name | given, joined by dots | No |
| zone permissions | `internal/admin/permissions.go:137-156` (`ZonePermissions`) | owner, zone, wildcard zones | given, joined by dots | No |
| per-endpoint permissions | `internal/admin/permissions.go:18-50,63-95` (`Permissions`, `PermissionsTo`, `NSCFlags`) | owner, name, recipients | given | No |
| tags (`allow`, `deny` tags, `NoDenyTag`) | `internal/policy/plan.go:25-60`, `internal/policy/compile.go:137-165`, `internal/admin/permissions.go:143-156` | derived from the rules | transformed: a zone pair (Zs to Zr) becomes allow or deny tags of the form `Zr.*` or `Zr.name`; derived from policy rules, not from an outside identifier | No: the tag text is built from zone and endpoint names the file declares |
| per-zone user JWT | `internal/mint/mint.go:19-137` | the compiled endpoint's zone | given: the zone key is `keys.ZoneKey(ep.Zone)` and signs the user | No |
| zone key store | `internal/ctlkeys/ctlkeys.go:40,59-61,388-435` | zone name | validated (`validZone`: 1-32 of a-z, 0-9, `-`); the zone becomes the file name `zones/<zone>.seed` | No. A path built from the zone as given. (`validZone` here is a second, looser copy of the check: it does not require a letter first or forbid a leading hyphen, and neither does `sap.ValidZone`; both accept the same set) |
| name rules | `internal/config/config.go:117-123` (`ValidName`), regexp at `:116`; copy at `internal/sap/sender.go:443-446` (`validOriginName`) | any endpoint or owner name | validated only: no `.`, no `*`, no `>`, no repair. Used for the owner (`config.go:471`, `internal/admin/admin.go:99`, `internal/policy/compile.go:71`, `internal/bootstrap/bootstrap.go:140`, `cmd/mashctl/adopt.go:146`, `internal/ctl/local.go:70`) and for names (`config.go:456`, `internal/admin/admin.go:173,286,525,758,774,840,931,1114`, `internal/admin/suspend.go:75,162,218`, `internal/ctl/ctl.go:213,272,284,297,312,348,555`, `internal/policy/check.go:70`, `internal/policyfile/policyfile.go:318`, `cmd/mashctl/{add:259,retire:110,label:105,policy:141}`, `cmd/mash/status.go:134`, `internal/endpoint/{mail.go:58,694,client.go:272,402}`) | No. The rule is a syntax check; it does not derive an owner from a name's dashes |
| policy file editing | `internal/policyfile/edit.go`, `policyfile.go:318` | `mashctl add`, `label`, `retire` | name validated; labels written as given | No |

## 5. Who filters (`mash who`)

`cmd/mash/whofilter.go:12-57` parses `--owner O` (and `--name`, `--match`, `--role`, `--label`, `--cap`); the owner
filter is exact equality (`internal/endpoint/whofilter.go:72`). There is no zone filter flag. Output only: it shows a
record's owner and zone as stored (`whofilter.go:95,104,114`, `serve.go:733`).

## Candidate contract gaps (reported, not changed)

| ID | Where | What it does | Why it may be a gap |
|---|---|---|---|
| A1 | `cmd/mashctl/adopt.go:149-152` | with no `--account`, the account name is taken from the owner | derives one mash primitive (account name) from another (owner); guarded by the account JWT name check at `:449` |
| A2 | `internal/policy/policy.go:38,111-116`; `cmd/mashctl/add.go:442-465` (flag `--zone` becomes a label); `check.go:63` | `subject_label` names which label's value is the zone | a policy input that selects a grouping (a label key) to serve as the zone; arguably an alias for a zone field |
| A3 | `internal/policy/policy.go:21-22,119-137`, `plan.go:47`; consequence at `cmd/mashctl/add.go:368`; and policy repair at `cmd/mashctl/drift.go:301` and `:729` (`SetZone`), which gives an existing legacy record with no zone the compiled endpoint's zone, `default` when the policy has no zone label | an endpoint with no zone label is put in zone `default` | the zone is assigned, not given; the stored file stays zone-less but the record, JWT tags and key are in `default`. `sap.DefaultZone` (`internal/sap/subjects.go:39`) is assigned by nothing; the policy package's own `DefaultZone` constant is what does it |
| A4 | `cmd/mashctl/policy.go:187-190` | `policy check` with no owner compiles with the owner `owner` | an invented owner; offline check only, nothing is written |
| A5 | `internal/admin/admin.go:259,359` with `sap.ZoneOfMailFilter` | the zone of an existing exact-zone stream filter is read back out of the subject to rebuild the stream's filters | zone read from a subject token; same owner and name, mash's own stream, `ValidZone` checked (low) |
| A6 | `internal/endpoint/directory.go:72` with `sap.NameFromSubject` | the endpoint name is read from the 5th token of a verified presence answer's subject | a name read from a subject's parts; owner-matched and signature-checked (low) |
| A7 | `cmd/mashctl/drift.go:211` (`signersByRole`), `internal/apply/apply.go:540` | an account user-scope role in the account JWT is read as the zone grouping and reported as `outside.zone` or `conflict.zone`, including roles the policy does not name | a zone read from an account's own data; it is compared and reported, never written back as a mapping. Not an outside identifier, listed for completeness |
| A8 | `internal/ctlkeys/ctlkeys.go:415` (`Authority.Zones`) | zone names are recovered by stripping `.seed` from the file names in the key directory and validating the rest | the reverse of the zone-to-file-name operation in the key rows above; a zone read back from a name mash itself wrote. Same class as A5 |

None of A1 to A6 takes an identifier from outside mash. A2 and A3 are the two that could let an outside grouping
become a zone (an operator can set a label from any source and call it the zone). They are existing behaviour; the
gate for this scrub forbids changing it here, so the lead decides whether they stay, change or get an amendment.

## Tests added (characterization; they pass against unchanged code)

- `internal/sap/zone_passthrough_test.go`: `TestZonePassthroughSubjects`, `TestSplitMailDoesNotSplitTokens`,
  `TestValidZoneValidatesOnly`. Owners, zones and names with dashes, digits, a 32-character zone, an owner equal to the
  zone, an owner with parts; `For`, `ForZone`, `MailFilter`, `MailFilterZone`, `SplitMail` and `ZoneOfMailFilter`
  return the tokens byte for byte.
- `internal/config/config_zone_passthrough_test.go`: `TestOwnerUsedAsGiven` (file and `MASH_OWNER`, with a name and a
  host that differ), `TestOwnerNotDerived`, `TestOwnerValidatedNotRepaired`.
- `internal/store/store_zone_passthrough_test.go`: `TestParseRecordOwnerZoneUnchanged`, `TestParseRecordZoneNotInferred`.

## Diff review

This pull request adds or changes no configuration field, flag, environment variable or policy input, and no code
path. Files changed:

- `docs/evidence/scrub/zones.md` (new)
- `internal/sap/zone_passthrough_test.go` (new)
- `internal/config/config_zone_passthrough_test.go` (new)
- `internal/store/store_zone_passthrough_test.go` (new)

Only tests and documentation. No existing test is edited and no non-test Go file is touched.

## Mutation

Suppose a change added a field such as `zone_alias` (a map from an outside identifier, for example a team or project
name, to a zone), or a flag or environment variable that does the same. This review rejects it: it is an input that
translates an outside identifier into a zone, which REQ-009 forbids, and it would appear in the diff-review list above
as an added input. Such a field would have to be added in one of these places, each of which this inventory lists:
`internal/config/config.go` (a `Config` field, its json tag, the env name map at `:78` and the `str(...)` loader at
`:362`), `internal/policy/policy.go` (a `File` field next to `SubjectLabel`, and a lookup inside `zoneOf` or
`effectiveLabels`, `:119-137`), `cmd/mashctl/add.go` or `internal/ctl/ctl.go` (a flag next to `--zone`), or
`internal/sap/subjects.go` (a mapping inside the subject builders). The passthrough tests in section "Tests added"
are sampled characterisation of today's code: they fail for an unconditional change inside the subject builders, the
config loader's owner handling or `ParseRecord`, because they assert that the owner and zone come out byte for byte as
they went in. They would not catch a mapping that only acts on an input the fixtures omit, or one in the policy compiler
or the CLI handling. Rejecting a new mapping input is therefore the job of this inventory and the diff review, which
list every input, not of the tests; the tests only pin that the existing paths pass values through.
