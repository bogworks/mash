The `send` tool's receipt adds one line with the recipient's self-reported state for kinds that wake it
(`blocker`, `question`, `task`, or urgent), for example `recipient: blocked (question); your ask will wait until it is
answered there`. The line is advisory and unsigned, and it never delays the receipt by more than 1 s. `not reporting`
does not mean idle. The CLI `mash send` output is unchanged
([ADR 0132](docs/decisions/0132-mash-asks-a-peers-reported-state-over-sap-status-and-shows-it-only-where-who-already-adds-fields.md)).
