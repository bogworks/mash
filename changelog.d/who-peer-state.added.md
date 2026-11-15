`mash who --long`, `mash who --json` and the `who` tool show a `state` column for online endpoints: what the peer
reports it is doing over SAP status (`working`, `blocked(question)` and so on). `-` means not reported, never idle; `?`
means no answer within the 2 s budget. The state is the peer's own unsigned report. The default `mash who` line is
unchanged ([ADR 0132](docs/decisions/0132-mash-asks-a-peers-reported-state-over-sap-status-and-shows-it-only-where-who-already-adds-fields.md)).
