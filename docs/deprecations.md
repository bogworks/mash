# Deprecations

Every behaviour mash keeps working after something has replaced it has one row here: what the old behaviour is, the
release that deprecated it, what replaces it, the release that removes it, and the test that proves it still works
until then. A row with no removal release says so. A row whose test is missing says so in the test column.

| Old behaviour | Deprecated in | Replaced by | Removed in | Test that proves it still works |
| --- | --- | --- | --- | --- |
| The one-time 0.5.5 migration script, `scripts/mash-migrate-0.5.5.sh`, which rewrites a 0.5.5 `config.json` (`url` becomes `servers`, `tls_ca` becomes `ca`) | none recorded; it ships under REQ-028 ([ADR 0170](decisions/0170-the-0-5-5-migration-script-and-the-refusal-of-a-0-5-5-configuration-folder-ship-again.md)) | the 0.5.6 configuration keys; the script is the only way across | proposed 0.5.10 ([#1185](https://github.com/bogworks/mash/pull/1185)), not accepted | `scripts/mash-migrate-0.5.5_test.sh`; `TestMigrate055WritesLandInHeldFolder` in `scripts/mash_migrate_055_test.go` |
| The refusal of a configuration folder left by mash 0.5.5: `mash` and `mashctl` stop with one error that points to the script | none recorded; kept with the script ([ADR 0170](decisions/0170-the-0-5-5-migration-script-and-the-refusal-of-a-0-5-5-configuration-folder-ship-again.md)) | the migrated folder | proposed 0.5.10 with the script ([#1185](https://github.com/bogworks/mash/pull/1185)), not accepted | `TestRefuseLegacy055` in `internal/cliframe/legacy055_test.go`; `TestLegacy055FolderRefused` in `cmd/mash/legacy055_test.go` and `cmd/mashctl/legacy055_test.go` |
| `mashctl add`, `mashctl reissue` and `mashctl retire` still answer, with a message that names the replacement and runs nothing | 0.5.5 (the verbs moved under `endpoint`; first shipped in v0.5.5) | `mashctl endpoint create`, `endpoint reissue`, `endpoint delete` | none set | `TestRemovedCommands` in `cmd/mashctl/removed_commands_test.go` |
| Heartbeats, published every 30 seconds by each running endpoint, though `mash serve` no longer subscribes to them | 0.3.3 for serve's use ([ADR 0066](decisions/0066-mash-serve-asks-who-is-online-when-someone-asks-instead-of-listening-to-every-heartbeat.md)) | the presence request that `mash who` sends | none set; another SAP tool may listen to them | `TestHeartbeatSigned` in `internal/endpoint/service_test.go` (it proves they are published and signed; `TestServeHoldsNoHeartbeatSubscription` in `internal/endpoint/presence_test.go` covers the serve side) |

## Checked and not kept

The 0.5.6 to 0.5.9 search for anything marked deprecated, kept for compatibility, aliased or moved with a note found
nothing else that still works. These were removed outright, and the old form is an error, not an alias, so they have no
row:

- `mash admin` (all of it), `mash plugin export`, `mash credential create` and the 0.5.6 draft commands `mash server
  status` and `mash endpoint status` ([mash reference](reference/mash.md#removed-in-058)). Each answers as an unknown
  command does (`TestAdminIsAnUnknownCommand`, `TestCredentialIsRemoved` in `cmd/mash`).
- The `mashctl` verbs and options listed in the [mashctl reference](reference/mashctl.md#removed-in-058), and the
  options of `mashctl endpoint create` that took metadata (`TestEndpointCreateRemovedOptions`).
- The environment variables `MASH_OWNER`, `MASH_GROUP`, `MASH_NAME`, `MASH_URL` and `MASH_TLS_CA` and the config keys
  `url`, `owner` and `tls_ca`: they are ignored (`TestOldSettingNamesAreIgnored` in `internal/config`).
- `-h` for `--help` is a short flag, not a deprecated alias.
