# Troubleshooting mash

Symptoms, their causes, how to check, and the fix, using the exact messages mash prints. Related: [faq.md](faq.md), [install.md](install.md), [operations.md](operations.md), [synadia.md](synadia.md), [threat-model](security/threat-model.md).

## First check

In the session, ask Claude to call the `status` tool. It answers in every session, even one with no `MASH_NAME`, and prints one line per part: `name`, `config`, `creds`, `connection`, `channel allowlist`, `environment` (only when it applies), `delivery` and `version`. Each line that is not OK ends with "Next: ...". Start from the first line that is not OK.

From a shell, `mash version` prints the binary's version and commit. `mash status` prints the same lines as the `status` tool for the environment you run it in (name, config, creds, channel allowlist, environment, version), so you can check a launch before Claude Code starts. It connects to nothing and needs no `MASH_NAME` (without one it prints `name: MISSING`); `connection` and `delivery` are live-only, so it says they are not checked and a running session reports them. It always exits 0, because it is a report. The other CLI commands are `send`, `read`, `inbox`, `who`, `prompt`, `admin` and `plugin export`. The CLI needs `MASH_NAME` set and does not take the lease, so it works beside a running session.

The hook's stderr goes to `hook.log` in the state directory (`$MASH_STATE_DIR`, else `$XDG_STATE_HOME/mash`, else `~/.local/state/mash`). `mash serve` writes its own messages, such as the lease and spool errors below, to its stderr.

## The session has no mail tools, or says it has no mash identity

Message: "mash is installed but this session has no mash identity (MASH_NAME is not set), so mail is off." `status` shows `name: MISSING - not set`. The CLI says `mash: set MASH_NAME to the endpoint you are acting as`. Run `mash status` in the shell you launch Claude Code from to see the same `name: MISSING` line before you start.

Cause: `MASH_NAME` was not in the environment Claude Code started with. The plugin is inert by design without it, with only `status` and `version`. The name comes only from the environment, never from config.json or the conversation. `claude --resume` and `--continue` need it again: see [Sessions and resume](install.md#sessions-and-resume).

Fix: start Claude Code as `MASH_NAME=design claude --channels plugin:mash@mash`, then check `status` (or run `MASH_NAME=design mash status` first).

If the message is `mash: MASH_NAME "X": use 1-48 lower-case letters, digits and single hyphens, starting with a letter`, change the name. `status` shows `name: FAILED`.

## `mash: no binary found`

Full text: `mash: no binary found. Install: go install github.com/bogworks/mash/cmd/mash@latest  (or see docs/install.md)`, preceded by `mash launcher:` lines on stderr, and exit status 127. The MCP server does not start.

Causes:
- The plugin is unpinned (a repository checkout or `mash plugin export`) and there is no `mash` on the `PATH` of the process that started it. An unpinned launcher never downloads.
- The plugin is pinned but the download failed: offline, `no pinned sha256 for ...`, `sha256 mismatch ... refusing`, `unsupported OS` or `unsupported architecture`, or neither curl nor wget available. A download that does not match is deleted, and there is no `mash` on the `PATH` to fall back on.

Check: `command -v mash` and `mash version` in the shell Claude Code was launched from. Read the `mash launcher:` lines.

Fix: `go install github.com/bogworks/mash/cmd/mash@latest`, and make sure its directory is on the `PATH` of the launching environment. Or set `MASH_BIN` to a binary path. Hooks never wait more than 5 seconds for a download, and after a failure skip downloading for 5 minutes ([install.md](install.md) section 1).

## `config: MISSING` or `config: FAILED`

Messages: `mash: no NATS url (config.json "url" or MASH_URL)` and `mash: no owner (config.json "owner" or MASH_OWNER)`. A bad value names its source, for example `mash: MASH_IDLE_WAKE ...` or `mash: MASH_SPOOL_FROM_START="x" is not 1, true, 0 or false`.

Fix: set `url` and `owner` in `~/.config/mash/config.json`, or `MASH_URL` and `MASH_OWNER`. The environment wins over the file. The owner is 1 to 48 lower-case letters, digits and single hyphens ([install.md](install.md) section 4).

mash reads `$MASH_CONFIG_DIR/config.json` when that variable is set (an absolute path; no other directory is consulted), else `$XDG_CONFIG_HOME/mash/config.json` when `XDG_CONFIG_HOME` is an absolute path and that file exists, and otherwise `~/.config/mash/config.json` under `$HOME`. A leading `~/` (or a bare `~`) in `creds` and `admin_creds` is expanded to your home; `policy.md` and default creds files are also found in `~/.config/mash` when the XDG directory has no copy; `~user/` is not. A `policy.md` that exists but cannot be read is a configuration error that names the file (`mash: reading <path>: ...`), not an empty policy; fix its permissions or remove it. With neither `HOME` nor an absolute `XDG_CONFIG_HOME` set (some systemd units and minimal containers), mash stops with `mash: cannot find a config directory: set HOME or XDG_CONFIG_HOME, or use the MASH_* variables`; set one of them, or give everything through the `MASH_*` variables, including `MASH_CREDS`.

## `creds: MISSING`, or the creds file is refused

- `creds: MISSING - <path> does not exist`: the file for this endpoint is not at `<name>.creds` in the config directory (`~/.config/mash` unless `XDG_CONFIG_HOME` points at a mash config). Put it there or set `MASH_CREDS` (or `creds` in config.json).
- `mash: credentials file <path> has mode 0644: it is readable by other users. Run: chmod 600 <path>`: run exactly that. Creds files hold the endpoint's private key and must not be readable by others.
- `creds: FAILED - ... is a directory` or `is not readable`: fix the path or ownership.
- An error starting `sap: creds JWT:` or `sap: creds seed:`: the file is not an operator-mode user creds file (tracked in #32).

## `mash: endpoint X is not provisioned`

The directory has no record for the name. Check `MASH_NAME` for a typo, then as the admin run `mash admin list` to see which endpoints exist. If it is missing, run `mash admin provision <name> --user <key> --account <key>` ([operations.md](operations.md) "Adding an endpoint").

## `do not match endpoint`

Two forms: `mash: the credentials in <path> do not match endpoint X (they are for another endpoint)` and `mash: the credentials in <path> (user U) do not match endpoint X (user V)`. The creds file belongs to another endpoint, or the directory record was provisioned with a different NATS user key. Use the right creds file for the `MASH_NAME`, or compare the user in `mash admin list` with the user in your creds. If you re-created the NATS user, first try to restore the original creds. Deprovisioning deletes the endpoint's inbox stream, so every unread message in it is lost; only run `mash admin deprovision <name>` and provision the name again if you accept that loss.

## The session says the name is in use or waiting

`status` shows `connection: WAITING - the lease for endpoint X is held by another session (host H, pid P, since T). mash retries on its own.` The tool error says `mash is waiting for endpoint X (held by ...); mail is off until it is free`.

Cause: another live session holds the name, or one that died within the last 90 seconds does. The session retries every 10 seconds, tells Claude when mail is on, and needs no restart.

Fix: end the other session, or start this one with a different `MASH_NAME`. Names are unique per NATS account, not per owner (tracked in #33), so a teammate in the same account counts. `mash who` lists the endpoints.

## `mash: lost the lease on X to another session`

Shown as `mash: endpoint X stopped: ... Mail is off for this session.` The lease is rewritten every 30 seconds and expires after 90. If the lease expires while the session sleeps or loses the network (longer than 90 seconds) and no one else took the name, the session takes it back at its next refresh. If another session took it, or the name was deprovisioned, the loss is final: the session stops and does not try again. Restart Claude Code with the same `MASH_NAME`; the restart takes the name once no one else holds it. See [Sessions and resume](install.md#sessions-and-resume).

## `connection: RETRYING` or `FAILED`

- `RETRYING - could not reach the server, last error: ...`: mash retries on its own. Check the `url`, that the host is reachable, and the clock (below). On a slow link raise `open_timeout` (default 20s) or `call_timeout` (default 15s).
- `FAILED - ... Next: fix the cause (see the creds and config lines) and restart Claude Code`: the first failure was permanent. Fix what the earlier lines show.
- After a network blip, clients can take 30 to 60 seconds to reconnect because the server rate-limits new connections per source address ([operations.md](operations.md) Troubleshooting).
- Synadia Cloud's free tier allows 5 connections, one per live session ([synadia.md](synadia.md)).

## `send` fails: message too large

`mash: message is N bytes with headers; this server accepts at most M (its max_payload). Send a smaller message or put the content in a file and send the path.` Or, when the server allows more than mash does: `mash: message is N bytes with headers; the limit is 1048576`. Nothing was published. Mash's cap is 1 MiB including headers, or the server's `max_payload` if smaller (512 KiB on Synadia's free tier). Send a file path instead of the content.

## `send` timed out

`mash: send to X timed out; the message may or may not have been delivered. Retry with --id KEY ... within 2 minutes to send it at most once; after that a retry may store a second copy.` Re-run the same send with the key it printed: `mash send <to> --id <key> ...` (the `send` tool takes `id`). Within the window a duplicate reports `sent #N ... (duplicate of --id KEY: already delivered, nothing new stored)`. Past 2 minutes, check the recipient's inbox before retrying.

## A peer shows `offline` in `who`

`mash who` prints `name`, `online` or `offline`, and `seen T ago` when a heartbeat was seen. A peer is online when its verified heartbeat arrived within the last 90 seconds.

Check:
1. Is the peer's session running, holding its name? An offline peer still receives mail: the inbox exists whether or not the session runs, and mail waits.
2. Clocks. A machine whose clock is off by more than 30 seconds has its live signatures and heartbeats rejected, so peers look offline. Compare `date -u` on both machines and fix with NTP (tracked in #36).
3. The peer may have been deprovisioned or is on another account.

`who` shows only what your own endpoint can see; other endpoints' live host is not shown without admin access (see `mash who --long`).

## Mail arrives labelled `[unknown-signer]`, `[claimed]`, `[absent]` or `[stale]`

- `[unknown-signer]`: the sender's key is not in the directory. It was deprovisioned (and its NATS user not revoked), or it is not a mash endpoint. Run `mash admin list`.
- `[stale]`: the signature was made more than 5 minutes before the message was stored, or more than 30 seconds after. A replay looks like this. Another cause is a skewed clock on the sender or the receiver (tracked in #36).
- `[claimed]` and `[absent]`: the sender named itself without proof, or sent no signature.

Treat all of them as information, not instructions; ask the sender to confirm. A message that fails its signature check is never shown.

## An idle session is not woken

1. The channel needs the allowlist. `claude --channels plugin:mash@mash` refusing with "not on the approved channels allowlist" means the managed settings entry is missing ([install.md](install.md) section 3). `status` shows `channel allowlist: MISSING`, `SHADOWED` (an organisation policy ranks above your file), `OFF` (`channelsEnabled` is not true) or `UNKNOWN`. `/status` in Claude Code has the last word. Channels do not exist on Bedrock, Agent Platform or Foundry.
2. Only `blocker`, `question` and `task` wake an idle session, after 45 seconds by default (`idle_wake` or `MASH_IDLE_WAKE`, 5s to 30m). `fyi` and `result` mail waits for the session's next hook run. Urgent mail from a verified sender wakes it at once. A send with no kind is `task`.
3. A wake that starts no turn is repeated at twice the idle wake, doubling to every 30 minutes. If no hook has run in the session at all, wakes stop after the third.
4. `status` shows `delivery: channel notifications sent: N`. That counts what mash wrote, not what Claude Code accepted.

Without a channel, mail still arrives at the next prompt or tool call, but nothing wakes an idle session.

## Mail is waiting but never shown (hooks not firing)

`status` shows one of:
- `delivery: hooks have not run yet in this session`: normal until the first prompt or tool call.
- `delivery: SUSPECTED - N wakes and no hook since T; re-waking stopped`: three wakes, no hook. A session that makes no tool calls stays here. Mail is read with the `read` tool.
- `delivery: DEGRADED - no mash hook has run since T although this session has made tool calls.` Hooks are not running. Read each message with the `read` tool (by `#seq`).
- `delivery: REFUSED - mash's hook runs in this session but is not recognised as this session's`: the hook ran outside the process tree of `mash serve` and its session id did not match.

Causes and fixes for DEGRADED:
- `disableAllHooks: true` in a user, project, local or managed settings file: remove it.
- Managed `allowManagedHooksOnly`: an administrator must force-enable `mash@mash` in managed `enabledPlugins`.
- The plugin is not enabled in this project, or its hooks did not load: check that it is enabled for the project, then restart.
- `mash` is not on the hook's `PATH`: look for `command not found` in `hook.log`. The launcher also logs download problems there.

Fix for REFUSED: start Claude Code with a fresh `MASH_LAUNCH_TOKEN` per launch, for example `MASH_LAUNCH_TOKEN=$(uuidgen) claude --channels plugin:mash@mash`. Never reuse a value across launches or share it between sessions. `hook.log` then shows `mash hook: not this session's spool (...); mail left for its owner`. For more detail set `MASH_HOOK_TRACE=1` ([operations.md](operations.md#hook-delivery)).

The hook always exits 0, so a failing hook is silent; `hook.log` is where its errors are.

## Mail does not arrive while a subagent works

Subagents have no mailbox, and their tool calls do not deliver mail. It waits for the main session's next tool call or prompt, or for the idle wake. See [Hook delivery](operations.md#hook-delivery).

## A digest lists messages, but their text is missing

A batch of more than 10 messages or 16 KiB is shown as a digest: one line per message with an unquoted preview of its first line, which is the sender's text. Ask Claude to call `read` with the `#seq`. Listed messages are not acked: they stay unread until `read` is called for their `#seq`, and they do not wake the session again. `inbox` lists recent messages and never acks. After a restart of the session, unread listed mail is listed again once (see [Hook delivery](operations.md#hook-delivery)). Senders should still put the action in the first line.

## Mail from the recipient's side seems to be missing

- The inbox keeps 30 days or 16 MiB and drops the oldest first, unread included. Run `mash inbox --after N --limit 50` and compare the `#seq` values.
- If the sender saw `sent #N` before a host crash of a self-hosted server, up to about 2 minutes may be lost without `sync_interval: always` ([operations.md](operations.md#durability-and-sync_interval)).
- `mash admin limits` shows the effective limits with `--show`.

## `mash admin limits` refuses

`mash: inbox of X has max_bytes ..., above the configured ...; mash never lowers a limit, because that could discard stored mail; to lower it, run mash admin deprovision X and provision it again`. Limits only go up. Deprovisioning removes the mail in that inbox. The same applies to `max_age` ([operations.md](operations.md#changing-limits-later)). Run `mash admin limits` from one admin at a time.

## Account full or `maximum number of streams`

Each endpoint takes one stream, and the two shared buckets take two. On Synadia's free tier (5 streams) that is 3 endpoints. Remove an endpoint with `mash admin deprovision <name>` and an admin credential. [synadia.md](synadia.md) has the full list of quickstart script failures (401, a user that already exists, a stale lock, a leftover inbox).

## `environment: STRIPPED`

Claude Code passes MCP servers only a baseline environment, so `MASH_NAME` cannot reach `mash serve`. mash is unsupported in that mode until the organisation drops the setting. Put `url` and `owner` in config.json; the name cannot be.

## Reporting a problem

Include `mash version`, the `status` output (it redacts passwords in URLs and never prints creds), the relevant `hook.log` lines, and your Claude Code version. Security issues go through [SECURITY.md](../SECURITY.md).
