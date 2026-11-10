# Directory readers: keyed by Record.KeyOrName()

Before any new-form record is written (naming plan 1.6), every reader that indexes the directory, the lease bucket or
a live-presence map by a `store.Record` was moved off `Record.Name`. `Record.KeyOrName()` (new, `internal/store/store.go`)
is `Key()`, or `Name` for a bare value that names no identity (test fixtures that never went through `ParseRecord`).
For every legacy record it is `Name`, so legacy behaviour is identical.

Classes: (a) keys a map, lookup or KV key by the record: must use the key. (b) display only: the name token may stay.
(c) policy-file name: unchanged, legacy-only for now.

| Site | What it indexes | Class | Change |
|---|---|---|---|
| internal/endpoint/directory.go (Who sort) | sort order of the listing; `e.dir`, `e.lastSeen`, `e.byUser` are already keyed by the KV key | a | sort by `KeyOrName()` (a sort on Name ties for two records with one name) |
| internal/endpoint/client.go `learn`, `installDirectory`; directory.go `installDirectory` | `e.dir[key]`, `e.byUser[user]=key` | a | none: the map key is the KV key already |
| cmd/mash/serve.go `whoText` (name column, live join) | printed name; `live[...]` lease join | a | `KeyOrName()` for both (the name column is what a caller addresses mail to) |
| cmd/mash/whofilter.go `whoNames`, `whoJSON` (name, live join) | same | a | `KeyOrName()` |
| cmd/mash/serve.go `liveLeases` | map keyed by `ep.Name()` (the config name = lease key) | a | none: legacy config name; the lease key for a new-form endpoint comes with 1.4 |
| cmd/mashctl/drift.go `directoryFindings` (byName, declared, finding subjects) | records joined to policy endpoints | a/c | records keyed by `KeyOrName()`; policy endpoint names (`e.Name`) are legacy names and match only a legacy key. A new-form record is reported as an extra record, never as the declared endpoint. `MailSubjectFor(r.Owner, r.Zone, r.Name)` is reached only for a matched (legacy) record |
| cmd/mashctl/label.go `syncLabels` | existence check before `SetMeta(name)` | a | `findRecord` (key match) |
| cmd/mashctl/retire.go `looksInterrupted`, `runRetire` | record lookup by name before the guarded removal | a | `findRecord` (key match) |
| internal/admin/admin.go `ApplyLimits` | inbox names for the whole directory | a | `KeyOrName()` (the inbox is `inbox-<key>`) |
| internal/ctl/ctl.go (provision, set-meta, set-zone output; deprovision `recordUser`; `list`) | KV key shown or matched | a | `KeyOrName()` |
| internal/endpoint/whofilter.go | `--name` / `--match` substring on the name token | b | none: a filter on the name token; a new-form record is also found by its name |
| internal/ctl/ctl.go `Provision`, internal/admin `Provision` | write a legacy record under `name` | a | none: writes only legacy records until 1.6 |
| internal/policy/*.go (`r.Name`) | policy rule names, not records | c | none |
| internal/apply/*.go (`z.Name`, `e.Name`) | policy zones and endpoints | c | none |
| internal/store/store.go (ParseRecord, Identity, Key) | the parser itself | - | none |

## Not Record.Name, but keyed by a name, left for 1.3 / 1.4

These index by a name taken from a subject or a signed header, not from a record, so they are the subjects and keys
work and not this change. They are correct for legacy identities, which is all that exists today:

- internal/endpoint/directory.go `applyPresence`: `sap.NameFromSubject` against `e.byUser` (the key), so a new-form
  endpoint would not match until its subject carries the identity (1.3).
- internal/endpoint/mail.go `classify`, `loadSenders`, and service.go: `h.Name` (signed header) against `e.byUser`.
- internal/endpoint/client.go `ownLease`: lease key is `e.cfg.Name` (1.4).

## Tests

- internal/store/keyorname_test.go, cmd/mash/whokey_test.go, internal/endpoint/whokey_test.go,
  cmd/mashctl/dirkey_test.go: a new-form record (key `agent_owner_name`) and a legacy record with the same name token
  stay distinct in who, the live join, the drift join and the label/retire lookup. `ApplyLimits`, `ctl list` and
  deprovision need a NATS server and are covered by the existing integration tests (legacy behaviour unchanged).

`go run ./scripts/scrubcheck | grep directory-readers` must print nothing after `git add`.
