# Live proof: mash Pi host adapter in a real Pi session

Throwaway harness (never committed, removed from the repo).
Final run: `MASH_LIVE_PI=<this dir> go test -C cmd/mash -run TestZZLivePi -v` (174 s, test PASS; the scenario verdicts are in results.txt).

## Versions and knobs
- mash commit: 88ab1072a4f6f31dace84bc7f03f82bc2f454d0d (branch pr/mash/pi-live), binary built from it, extension from `mash plugin export --host pi`
- pi 1.4.0, `pi --mode rpc --provider openrouter --model deepseek/deepseek-v4.1-flash -e <export>/mash.ts --no-context-files --no-skills --no-prompt-templates --no-approve`
- Env knobs in the Pi child: **MASH_IDLE_WAKE=10s**, MASH_SETTLE_MS=20, MASH_HOST unset (the extension sets MASH_HOST=pi for serve and hooks), PI_OFFLINE=1,
  isolated PI_CODING_AGENT_DIR, HOME and XDG_STATE_HOME (temp dirs; ~/.pi untouched), a private NATS world (scratch, sender).
- Spool AckWait of the inbox consumer is 30 s (internal/store/store.go:56).

## Scenarios (final run; run1/ is the earlier run, see note at the end)
| # | Result | Evidence |
|---|---|---|
| 1 Startup | PASS | `mash who` from sender: scratch online 3.3 s after Pi start; one serve, pid 2084958, a descendant of Pi. See the S1 addendum below: Pi did NOT keep that pid. |
| 2 Prompt drain | PASS | MARK-PROMPT spooled 0.2 s after send; prompt run has a custom message customType "mash" containing `kind: task > MARK-PROMPT` and the assistant reply mentions MARK-PROMPT. |
| 3 Busy drain | PASS | Mail MARK-TOOL sent 3 s after the sleep tool started; the bash tool_execution_end content contains `> MARK-TOOL`; exactly 1 agent_start in the run and no run started in the 20 s after it. |
| 4 Idle wake | PASS | MARK-IDLE sent while idle; a run started by itself 10.1 s later (final run; 10.3 s in run1), custom mash message has MARK-IDLE. |
| 5 fyi held | PASS | MARK-FYI (`--kind fyi`): no run in 35 s; the next prompt run's custom mash message has `kind: fyi > MARK-FYI`. |
| 6 Urgent | PASS | `--urgent` MARK-URGENT: run started 0.1 s after send (0.3 s in run1); custom message `urgent: yes ... MARK-URGENT`. |
| 7 Live prompt | PASS | `mash prompt scratch "Answer with the word PONG using the reply tool ..."`: sender printed `PONG`, exit 0, in 2.6 s. The model got it as a wake-injected message, found `mcp__mash__reply` through Pi's `codemode` tool (describeTool, then `tools.mcp__mash__reply({request_id, text:"PONG"})`). |
| 8 New session | PASS | RPC `new_session` ok; MARK-NEW sent idle; a run started 10.3 s later and its custom mash message has MARK-NEW. Side effect below. |
| 9 Restart replay | PASS, with a 30 s delay | MARK-REPLAY (fyi) spooled; Pi SIGKILLed; the serve children were gone 0.35 s later (no stray; they exit on their own, nothing to kill). New Pi online. (a) A prompt 3 s after the new Pi was online did NOT show MARK-REPLAY (spool dir empty, unread=1); the model went to the inbox/read MCP tools instead. (b) The new serve re-spooled `7.json` 30.0 s after the kill (24 s after Pi online). (c) The next prompt run showed MARK-REPLAY in a custom mash message. The re-delivered marker did not appear, and is not expected: the mail was never shown before the kill. |
| 10 Ack accounting | PASS | End of run: unread (NumPending + NumAckPending) = 0, no spool leftovers, all seven MARK messages in the inbox and each appears in a customType "mash" message or hook-shown tool result in rpc.jsonl. |

## Findings (observed; no product code changed)

### S1 addendum: Pi respawns serve on new_session, and starts two
- Before `new_session`: one serve, pid 2084958. After it: pids 2091186 and 2091199, both children of the same Pi (ppid 2084857) with the same start tick; the original is gone. Timeline lines `WATCH` at 14:25:47.435 show the change 18 ms after the new_session response.
- Cause (partly established): Pi's built-in MCP extension ties server connections to the session (dist/extensions/mcp/index.js: aborts on `session_shutdown`, reconnects on `session_start`), so the stdio server is not kept across `/new`. Why two are started was not determined; the extension re-registers `mash` on each load, a likely contributor, but this was not tested.
- Impact seen: none functional. S8, S9 and S10 all passed with both serves running, and mail kept being delivered. But two serves of one endpoint ran for ~35 s, which the design note's open question ("does Pi keep the idle stdio server, or respawn it") answers: **Pi keeps it across idle and prompts (same pid S1 through S7, per the WATCH log) but respawns it on new_session.**

### S9: replay is not immediate
- The new serve at start calls `Spool.ClearWaiting` (internal/spool/spool.go) and relies on NATS redelivering unacked mail. The old serve's keepalive stopped at the kill, so redelivery waits out the 30 s AckWait. A prompt sent in that window shows nothing; at-least-once holds, with up to ~30 s of latency after a restart. Evidence: timeline.log lines 14:26:23 to 14:26:53.

### Other observations
- S8's idle-wake run took ~32 s because the model wandered (read files, ran shell commands) trying to understand a bare `MARK-NEW` message; that is the model, not mash. It still passed.
- No `hook.log` exists under XDG_STATE_HOME/mash for the Pi path (the shell-hook log is Claude's); stderr.log is empty.
- **Key hygiene incident.** In S8 and run1 the model ran `env` in its bash tool, and the child environment holds `OPENROUTER_API_KEY`, so the key was printed into that tool result in rpc.jsonl (and went back to OpenRouter, the provider it belongs to, in the model context). The harness scrubbed it to `REDACTED` at the end (7 places in rpc.jsonl, 14 in run1/rpc.jsonl) and a scan confirms it is in no file here, but the file held it between the run and the scrub. Consider rotating that key. A rerun should pass the key by a mechanism the bash tool cannot read, or tell the model not to run `env`.
- Run1 (run1/): the first run, whose harness checked the spool by raw string match on a base64 Body: S2 and S9 "spooled" checks failed falsely, and S9 and S10 failed on the 30 s replay delay (the harness did not wait for it). The harness was fixed and the whole suite rerun to produce the final results.

## Files
- timeline.log, results.txt (committed). The raw RPC log, stderr, the go test log, the throwaway harness and run1/ stayed in the run directory: the raw log holds a model-run `env` dump.
