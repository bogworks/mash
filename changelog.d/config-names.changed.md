The settings that name the endpoint and the server have new names, and the old ones are no longer read:
`MASH_ENDPOINT` (the full address, owner.group.name) replaces `MASH_OWNER`, `MASH_GROUP` and `MASH_NAME`; `MASH_SERVER`
(a comma list) replaces `MASH_URL`; `MASH_CA` replaces `MASH_TLS_CA`. In `config.json` the keys are `endpoint`,
`servers` and `ca`; `url` and `tls_ca` are gone, and `owner` is read only by `mashctl`. Each can also be given as a
flag (`--endpoint`, `--server`, `--ca`). With one endpoint's credentials installed in the config folder, `mash` uses
that endpoint without being told ([ADR 0147](docs/decisions/0147-the-0-5-6-command-line-and-mcp-surface-is-built-from-a-specification-that-supersedes-contract-revision-26.md)).
