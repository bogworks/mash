# Hook delivery live proof on the v0.2.1 tag: 2026-10-28 15:42-15:48Z, mash
The tag was v0.2.1 = c1f212ce45ed5915792cf9b31e233c4a7461bfef, checked out detached; mash was built from it and put
first on PATH. The run used Claude Code 2.1.312 with `claude -p` stream-json, model haiku. The hooks were
plugin/hooks/hooks.json from the tag, passed as --settings. The endpoint was a scratch endpoint on mash's testenv
NATS, with the spool under a temp XDG_STATE_HOME. The harness was the same as the v0.2.0 proof's
(docs/evidence/2026-10-28-hook-delivery-live), with a new session id. Here: timeline.log (the harness's clock), and excerpts.md
(every channel event and hook context, from the session transcript). The raw stream and transcript are not committed,
because they carry the session's whole system context.

| case | sent | result | turns |
|---|---|---|---|
| mid-turn | MARK-MIDTURN, 8 s into a `sleep 20` Bash call | shown by the PostToolUse hook in the same turn ("two MARK-MIDTURN") | 0 extra |
| idle | MARK-IDLE, right after turn 2 | no turn for 4 min 30 s; a wake at +5 min 02 s whose channel event was the summary alone ("1 message from sender"); UserPromptSubmit showed the body | 1 |
| urgent | MARK-URGENT --urgent, idle session | a wake 2.1 s later ("urgent, 1 message from sender"); UserPromptSubmit showed the body; no turn in the next 20 s | 1 |

The run took 5 turns: 3 prompts, 1 idle wake and 1 urgent wake. The transcript check confirms:
- Both channel events carry the summary line only, with no body and no MARK- token.
- Every MARK- token reached the model in hook context headed "mash mail for scratch, delivered by mash's hook".

The model's own account agrees.

Not exercised live: re-wake backoff (the first wake landed a turn), at-least-once re-delivery, spool fallback, self-heal.
Those are covered by #47 and #49 tests.
