`mashctl` records every user JWT it issues (`add`, `reissue`, and the admin user at `init`) in a new `mash-issued` bucket that only
the admin connection writes and no endpoint can read or write, and `mashctl issued list` and `mashctl issued check` read it. The check
compares the user keys connected to every server with the record and reports any key mash did not issue (exit 1), and refuses an
incomplete server set (exit 2). The threat model gains "Who holds which key"
([ADR 0100](docs/decisions/0100-every-issued-user-jwt-is-recorded-in-a-bucket-only-the-admin-connection-writes.md)).
