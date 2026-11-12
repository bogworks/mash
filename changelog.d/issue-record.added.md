`mashctl` records every user JWT it issues (`add`, `reissue`, and the admin user at `init`) in a new `mash-issued` bucket that only
the admin connection writes and no endpoint can read or write, and `mashctl issued list` and `mashctl issued check` read it. The check
compares the user keys connected to every server with the record and reports any key mash did not issue (exit 1), reports a connected key the record holds as revoked (exit 1), and refuses an
incomplete server set (exit 2). Each entry carries a `purpose` (`issue`, `rotation`, or `probe`, reserved for the policy gate's probe credentials) and a
`revoked_at` time that `retire`, `deprovision` and a rotating `reissue` set after the revocation succeeds (a failure to
note it is a warning, never a failed revocation). The threat model gains "Who holds which key"
([ADR 0101](docs/decisions/0101-every-issued-user-jwt-is-recorded-in-a-bucket-only-the-admin-connection-writes.md)).
`mashctl adopt --mint-admin` now records the admin user it mints, and the check exempts no key. The record keeps the last 64 revisions of a key, and the check scans a server again when its connection pages do not agree with its own count, and reports the scan as incomplete when they still do not.
