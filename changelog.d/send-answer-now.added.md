The `send` tool takes `answer: "now"` (and `answer_within`, default 10m, at most 24h). The first verified reply that cites the ask
through its signed in-reply-to, from the recipient, then wakes you, even when the replier sent it as `fyi`. The ask is kept by
`mash serve` across a restart, and at most 20 are open. Nothing changes on the wire or for the recipient. The CLI `mash send` is unchanged
([ADR 0135](docs/decisions/0135-an-ask-marked-answer-now-is-woken-by-its-verified-reply-whatever-the-replys-kind.md)).
