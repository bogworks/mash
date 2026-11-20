Breaking: mash serve now refuses unverified mail. Only mail whose trust is `verified` reaches the session, `mash inbox`
and `mash read`. Mail that is unsigned, signed by an unknown key, claimed but unproven, or stale is dropped and counted,
`mash doctor` shows the count in its `refused` row, and the sender is not told. To receive it anyway, labelled as before,
set `MASH_ACCEPT_UNVERIFIED=1` (or `accept_unverified` in `config.json`) or start `mash serve --accept-unverified`.
Mail refused earlier is not delivered afterwards
([ADR 0147](docs/decisions/0147-the-0-5-6-command-line-and-mcp-surface-is-built-from-a-specification-that-supersedes-contract-revision-26.md)).
