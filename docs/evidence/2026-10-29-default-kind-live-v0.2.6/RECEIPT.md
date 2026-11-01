# Default-kind and early-mail live proof on the v0.2.6 tag: 2026-10-29 18:40-18:44Z, mash
The tag was v0.2.6 = e08f4f285e722468bdba98859cfeb62ee4600f6a, checked out detached. mash was built from it and put
first on PATH. The run used Claude Code 2.1.312 with `claude -p` stream-json, model haiku, and the plugin's hooks as
--settings. MASH_SPOOL_FROM_START=1 was set in claude's environment, as the v0.2.6 plugin/.mcp.json sets it for serve.
MASH_IDLE_WAKE was the 45 s default. The endpoint was a scratch endpoint on mash's testenv NATS, with the spool under
a temp XDG_STATE_HOME. No provider key was in the session's environment: the harness drops MASH_* and CLAUDE_CODE_*,
and the session's Bash was limited to `sleep`. Only timeline.log (the harness's clock) is committed here. The raw
stream and the transcript are not committed, because they hold the session's whole system context.

| Check | Result |
|---|---|
| #16: mail sent before the first prompt is held, not lost | 2 sent before the prompt, unread=2; prompt 1 named both (hook context) |
| A send with no kind (now a task) wakes an idle session | woke at 47 s (idle wake 45 s) |
| An explicit fyi does not wake | no turn for 95 s (2x the idle wake) |
| An urgent send wakes at once | woke in 1.2 s; that turn also took the pending fyi |
| Nothing is lost | the final prompt named all 5 markers; unread=0; exactly 4 turns |

Result: `--- PASS: TestZZLive026 (198.73s)`.
