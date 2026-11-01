# Stranger-matrix live tier: 2026-11-01, mash (plugin 0.2.10, commit fcd862da9dab31cdf0f13f9eeeb65499e45c679b)

mash was built from that checkout (`go build ./cmd/mash`) and put first on the child's PATH. Claude Code 2.1.316, resolved to `~/.local/share/claude/versions/2.1.316` (sha256 55cdb8e162d1), unchanged across the run. Model haiku, at most 8 turns and 5 minutes per scenario, Bash limited to `sleep`. Each scenario ran with a temp HOME, `DISABLE_AUTOUPDATER=1` and a child environment built from scratch (no keys, no `MASH_*` from the caller), against a dedicated Claude Code seat's config directory used in place as its only consumer for the run (no credentials copied). The plugin was installed from the checkout as a local marketplace and uninstalled after each scenario. Only `timeline.log` (the harness clock) and this file are kept; raw streams and transcripts are never written.

Run: `MASH_LIVE_CLAUDE=1 MASH_LIVE_CLAUDE_CONFIG_DIR=<seat dir> go test ./cmd/mash -run LiveStranger -v -p 1`

| Scenario | Check | Result | Detail |
|---|---|---|---|
| L1 plain claude, no managed settings | plain claude, no allowlist | not run | managed-settings.json present on host |
| L2 team policy: server-managed settings without mash | server-managed settings without mash | not run | no organisation account |
| L3 minimal environment (IDE-like launch) | plugin mash@mash installed from the checkout | PASS | plugin list: Installed plugins: ❯ mash@mash Version: 0.2.10 Read from: <checkout>/plugin Scope: user Status: ✔ enabled |
| L3 minimal environment (IDE-like launch) | no mash on PATH: nothing hangs (finished inside the cap, a result produced) | PASS | elapsed 5s err <nil> results 1 |
| L3 minimal environment (IDE-like launch) | no mash on PATH: MCP server state (init message) | RECORDED | plugin:mash:mash=failed, [connector list redacted] |
| L3 minimal environment (IDE-like launch) | no mash on PATH: hook.log says so | PASS | hook.log (252 bytes): mash launcher: mash: no binary found. Install: go install github.com/randybias/mash/cmd/mash@latest (historical path, now github.com/bogworks/mash) (or see docs/install.md) / mash launcher: mash: no binary found. Install: go install github.com/randybias/mash/cmd/mash@latest (or see docs/install.md) |
| L3 minimal environment (IDE-like launch) | a failed plugin MCP server is cached per config dir | RECORDED | a failed plugin MCP server is cached per config dir for ~15 min |
| L3 minimal environment (IDE-like launch) | mash reachable: finished and named the marker | PASS | elapsed 13s err <nil> turns 2 |
| L3 minimal environment (IDE-like launch) | mash reachable: MCP server state (init message) | RECORDED | plugin:mash:mash=connected, [connector list redacted] |
| L3 minimal environment (IDE-like launch) | mash reachable: the model's answer | RECORDED | MARK-L3-911338 |
| L3 minimal environment (IDE-like launch) | serve's owner record (parent pid, session id, launch token) | RECORDED | {"pid":740236,"session":"f3b55c47-6858-4320-add0-44fbd16251ee"} |
| L3 minimal environment (IDE-like launch) | MASH_HOOK_TRACE=1: the hook logged its pid chain and session ids | PASS | 1 trace lines; first: mash hook: trace pid=746078 ancestors=[746074 740236 735729 734682 734652 2823614 2823309 2783795 703] hook-session="f3b55c47-6858-4320-add0-44fbd16251ee" match=true (session id) |
| L3 minimal environment (IDE-like launch) | a traced hook matched serve's ownership (and so drained) | PASS | trace lines: mash hook: trace pid=746078 ancestors=[746074 740236 735729 734682 734652 2823614 2823309 2783795 703] hook-session="f3b55c47-6858-4320-add0-44fbd16251ee" match=true (session id) |
| L4 hooks disabled | plugin mash@mash installed from the checkout | PASS | plugin list: Installed plugins: ❯ mash@mash Version: 0.2.10 Read from: <checkout>/plugin Scope: user Status: ✔ enabled |
| L4 hooks disabled | claude finished inside the 5 minute cap | PASS | elapsed 21s err <nil> |
| L4 hooks disabled | no hook ran (disableAllHooks) | PASS | hook lifecycle events 0 |
| L4 hooks disabled | tool calls in order | RECORDED | ToolSearch, mcp__plugin_mash_mash__who, Bash, mcp__plugin_mash_mash__who, mcp__plugin_mash_mash__status (turns 6) |
| L4 hooks disabled | status says delivery: DEGRADED after two tool calls | PASS | status facts: channel allowlist: OK - local managed settings file enables channels and lists mash@mash ;; delivery: DEGRADED - no mash hook has run since 2026-11-01T10:29:26Z although this session has made tool calls. Likely cause: hooks are disabled (disableAllHooks, or allowManagedHooksOnly withou... |
| L4 hooks disabled | wake turns (at most 3) and the read-hint wake | not run | needs a managed allowlist in /etc, which is never touched |

Result: `--- SKIP: TestLiveStrangerL1PlainNoAllowlist`, `--- SKIP: TestLiveStrangerL2TeamPolicyShadowed`, `--- PASS: TestLiveStrangerL3MinimalEnvironment (22.25s)`, `--- PASS: TestLiveStrangerL4HooksDisabled (24.84s)`; `ok github.com/randybias/mash/cmd/mash 49.430s` (historical path, now github.com/bogworks/mash).

Findings:
- With no `mash` on the hook's PATH the plugin's MCP server is `failed` and the hook says so in `hook.log`; nothing hangs. Claude Code caches that failure per config directory for about 15 minutes, so a later session in the same config dir sees the server as failed without starting it. The harness clears only that cache entry between scenarios.
- With `mash` reachable, a wrapped serve and the hook agree on the session (`match=true`), and the hook drains.
- With hooks disabled, `status` reports `delivery: DEGRADED` after two mash tool calls at least 12 s apart. It depends on the model issuing those calls, so one earlier run (the model ordered the calls differently) did not reach it.
- L4 wake turns and the read-hint wake are not run: they need an allowlist in `/etc`, which the harness never touches.

Manual add-ons for the same receipt (not scripted):
- one VS Code extension session and one desktop-app session: `ps -o pid,ppid,comm` chains for serve and a hook, and whether the hook drained;
- the hook's CLAUDE_CODE_SESSION_ID before and after `/clear` and after `--resume`;
- L2 on a Claude.ai Team or Enterprise organisation: `/status` (Setting sources), `claude doctor`, and mash's `channel allowlist: SHADOWED` line.
