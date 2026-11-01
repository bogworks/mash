# Hook delivery live proof (the maintainer's condition 5): 2026-10-28 00:29-00:36Z, mash
Claude Code 2.1.312, `claude -p` stream-json, model haiku. The installed mash plugin (serve and channel) ran with this
branch's mash first on PATH (pr/mash/hook-urgent, #23 = #17+#21+#22+#23). The hooks were plugin/hooks/hooks.json,
passed as --settings. The endpoint was a scratch endpoint on mash's testenv NATS, with the spool under a temp
XDG_STATE_HOME. Here: timeline.log (the harness's clock), and excerpts.md (every channel event and hook context, from the
session transcript). The raw stream and transcript are not committed, because they carry the session's whole system context.

| case | sent | result | turns |
|---|---|---|---|
| mid-turn | MARK-MIDTURN, 8 s into a `sleep 20` Bash call | shown by the PostToolUse hook in the SAME turn ("two MARK-MIDTURN") | 0 extra |
| idle | MARK-IDLE, right after turn 2 | no turn for 4 min 30 s; one wake at +5 min 04 s; the channel event was the summary alone ("1 message from sender"); UserPromptSubmit showed the body | 1 (the idle wake) |
| urgent | MARK-URGENT --urgent, idle session | a wake 3.9 s later ("urgent, 1 message from sender"); UserPromptSubmit showed the body; no turn in the next 20 s | 1 (the urgent wake) |

Total: 5 turns = 3 prompts + 1 idle wake + 1 urgent wake. The model's own account: every MARK- token was seen in hook
context, none in a channel event, prompt or tool result. Every hook batch was headed by "mash mail for scratch,
delivered by mash's hook ...".

Not the release gate for mash v0.2.0, which ships with downstream consumer release 0.2.3. That is an interactive session under a terminal multiplexer with 2 h of no lost mail, which live runs once
A downstream consumer pins this mash release (maintainer, 2026-10-28 00:36Z).
