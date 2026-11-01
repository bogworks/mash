# Two live Claude Code processes on one session id, mash 0.3.0, 2026-11-01

Test: `TestLiveDualResumeD1` (`cmd/mash/live_dual_resume_test.go`), run with `MASH_LIVE_CLAUDE=1` and a dedicated seat's
config directory in place, one consumer. Claude Code 2.1.316, model haiku, mash built from this checkout (commit
30bd955 plus the test). Process A starts a fresh session; process B starts with `claude --resume <A's session id>` while A
is still running. Both use the same `MASH_NAME`, credentials and state directory, the plugin installed, hooks on,
`MASH_HOOK_TRACE=1`. Both are `-p --input-format stream-json` processes held open on stdin, driven turn by turn.
No launcher was involved, so `MASH_LAUNCH_TOKEN` was empty for both (see caveats). Only `timeline.log` and this file are kept.

## Results

| Question | Result |
|---|---|
| (a) Who holds the lease | **A**, the first to start. B's MCP server shows `connected` in its init message and B's stderr has nothing. B is mail-dead for the lease only: its `status` says `connection: WAITING - the lease for endpoint a is held by another session (host, pid, since ...). mash retries on its own`, and its `who` says `mash is waiting for endpoint a ...; mail is off until it is free`. **The error is visible only if B's user or model calls `status` or `who`;** nothing is printed at start, in the hook, or in the transcript. |
| (b) Where mail goes | **To exactly one process per message, never both, and not lost.** Both processes' hooks pass gate 2 (same session id), so B, which does not hold the lease, drains the holder's spool. Round 1 (both asked at once, mail M1): A saw it, B did not. Round 2 (only B asked, M2): B drained it from A's spool; A never saw it. Round 3 (only A asked, M3): A saw M3, not M2. Whichever process makes the next tool call gets the mail; the other process's context lacks it, silently. (B also showed the mail A had drained before B started, because `--resume` replays the shared transcript.) |
| (c) Transcript | **One shared file, no byte-level corruption.** Both appended to `<session id>.jsonl`: 151 lines, 0 unparsable, one `sessionId`, no second `.jsonl` created. But 2 message uuids are the parent of 2 or more lines: the conversation chain forked where the two processes appended independently. A later `--resume` follows one branch; the other process's turns sit on a dead branch. |
| (d) Holder exits | A killed with SIGKILL. B (alive, answering) became the holder within the 90 s window: a later spool owner record shows B's pid, `who` from another endpoint shows the name online, and mail sent afterwards (M4) reached B. B's `status` read immediately after the kill (5 s) still said it did not hold the lease; it was taken on B's next retry. B's end of the stream closed cleanly (exit code 0). |

## Findings

1. **A resumed copy steals mail from the lease holder without any signal** (b). It is the accepted risk of ADR 0040
   (rule 1, session id). Filed as an issue with a proposed narrowing (refuse a hook whose session id matches but whose
   non-empty launch token differs from a live holder's).
2. **The non-holder's "mail is off" notice is pull-only** (a): nothing reaches the transcript or stderr by itself.
3. **A shared transcript forks** (c): not a mash defect, but a user of `--resume` on a running session should know.

## Caveats

- No launcher, so launch tokens were empty; the proposed token rule in finding 1 was not exercised.
- `-p` stream-json processes, not interactive TUIs; no channel allowlist wake was possible, so mail arrived by hook only.
- One run, haiku: counts such as the number of transcript forks vary with how many tool calls each side made.

## After the fix (ADR 0043): `TestLiveDualResumeD2`, distinct launch tokens

Same flow, but A runs with `MASH_LAUNCH_TOKEN=live-token-a` and B (the `--resume`) with `live-token-b`
(`timeline-d2.log`; the fixed mash built from the same checkout). All checks pass:

- B's first hook output carried "this copy of the session is not receiving mail", once (`.refused` and `.told` appear in the spool).
- Round 1 (both asked): A named the new mail, B did not. Round 2 (only B asked): B did not see it. Round 3 (only A asked): A named both
  the round 2 and round 3 mail, so mail stays with the holder.
- A killed: B took the lease (owner record shows B's token); mail sent in the meantime waited in the spool for B's next hook, now that B is the owner.
