# Troubleshooting mash

Symptoms, their causes, how to check, and the fix, using the exact messages mash prints. Related: [faq.md](faq.md), [install.md](install.md), [operations.md](operations.md), [synadia.md](synadia.md), [threat-model](security/threat-model.md).

## First check

In the session, ask Claude to call the `status` tool. It answers in every session, even one with no `MASH_ENDPOINT`, and says one line: `<endpoint>: ready, offline`, `<endpoint>: ready, online` (online means an agent session holds the endpoint open now) or `<endpoint>: not ready: <reason>`. The reasons are `the server refused its credentials`, `the server did not answer: <address>`, `the creds file is missing: <path>`, `the creds file can be read by other users: <path>`, `the server's certificate is not trusted: <address>`, `the server did not offer TLS: <address>`, `mail is held: its folder cannot be written` (MASH_SPOOL_REQUIRED is set and the folder that stores arriving mail cannot be written, so mail waits in the inbox; read it with the `inbox` and `read` tools; `doctor` names the folder) and `no server is set`. A session with no endpoint named says `No endpoint is chosen for this session.` instead. When it is not ready, call the `doctor` tool.

`doctor` checks each part in turn and prints a row for each: `config`, `creds`, `server`, `sign-in`, `delivery` and `refused`, with a state (`ok`, `warn` or `FAIL`) and a detail. Then a summary (`No problems.` or `1 problem, 1 warning.`), and the fix for the first failure as `hint: <fix>`. Every row is always printed: a row that cannot be run because an earlier one failed shows `skip` and what it needs (`sign-in   skip  needs the server`), and a skipped row is neither a problem nor a warning. The `delivery` row says whether mail that arrives can be stored on this machine. It is checked only inside a session: at a shell it is `delivery  skip  needs a session`, and it also waits on the server and the sign-in. When it fails (`delivery  FAIL  mail is held: its folder cannot be written:`) the folder is on the next line, and it counts as a problem. In the JSON, `checks` always lists all six rows, each with a state of `ok`, `warn`, `fail` or `skip`. Start from the first row that is not `ok`. It never prints the contents of the creds file.

From a shell, the same two checks are `mash status` and `mash doctor`, for the environment you run them in, so you can check a launch before Claude Code starts. In a shell the `delivery` row says `skip  checked only inside a Claude Code session`: that is not a problem; run the `doctor` tool in the session to see it. `mash status` prints the one line, and when the endpoint is not ready it prints `hint: mash doctor` on stderr and exits 1. `mash doctor` exits 1 if any row is `FAIL` and 0 for warnings only. Both take `--json`. With several endpoints installed and none chosen, they stop with `error: more than one endpoint is installed here: a, b` and `hint: mash status --endpoint a` (exit 2; the hint names the first installed address in alphabetical order). `mash version` prints the binary's version and commit, and `mash inbox` shows mail. The other CLI commands are `send`, `read`, `who` and `prompt`. The CLI needs an endpoint (`MASH_ENDPOINT`, or the one installed endpoint) and does not take the lease, so it works beside a running session.

The hook's stderr goes to `hook.log` in the state directory (`$MASH_STATE_DIR`, else `$XDG_STATE_HOME/mash`, else `~/.local/state/mash`). `mash serve` writes its own messages, such as the lease and spool errors below, to its stderr.

## Claude Code or plugin version differs from the binary

- The `claude` row of `mash doctor` checks the Claude Code on PATH (`claude --version`) against the versions mash was tested with ([compatibility](compatibility.md)):
  - `FAIL` with `2.1.311 is below the tested 2.1.312` (your version in place of 2.1.311): 2.1.312 is the oldest version mash has a passing live receipt on. mash still runs, but channels, hook context or hook fields may behave differently, and `mash doctor` exits 1. Update Claude Code.
  - `warn` with `2.2.0 is above the tested 2.1`: mash was tested with the 2.1 series only. It may work, and this row alone does not make `mash doctor` fail. If mail stops arriving, say which version in a bug report.
  - `warn` with `the version of claude could not be read`: `claude --version` failed, took more than 3 seconds, or printed no version. Run it yourself to see why.
  - `skip` with `no claude command found`: there is no `claude` on PATH in the shell you ran `mash doctor` from. That is fine for a shell that only sends mail.
- Plugin and binary from different releases (different major or minor): the plugin (hooks and MCP command) and the `mash` binary should be the same release. Update both: `claude plugin update mash@mash`, and reinstall the binary with the install line in [install.md](install.md#1-the-binary), pinning the release with `MASH_VERSION=<tag>` (to build from source instead, `go install github.com/bogworks/mash/cmd/mash@<tag>`, not `@latest`). The plugin launcher downloads a pinned binary, so this is mostly a hand-installed binary on PATH that is out of step. `mash version` prints the binary's version.

## The session has no mail tools, or says it has no mash identity

Message: "mash is installed but this session has no mash identity (MASH_ENDPOINT is not set), so mail is off. Start Claude Code with MASH_ENDPOINT=<owner.group.name> ..." The `status` and `doctor` tools answer `No endpoint is chosen for this session.` and, on the next line, `hint: start the session with MASH_ENDPOINT=<address>`. In a shell with no endpoint installed, `mash status` and `mash doctor` say `error: no endpoint is installed here (set mash up with the setup skill: see the quick start)` with no hint line (exit 1), and so does every other command. With several installed and none chosen they say `error: more than one endpoint is installed here: <addresses>` and `hint: mash <command> --endpoint <the first address>` (exit 2). Run `mash status` in the shell you launch Claude Code from to see this before you start.

Cause: `MASH_ENDPOINT` was not in the environment Claude Code started with, and no single endpoint's creds file was installed. The plugin is inert by design without it, with only `version`, `status` and `doctor`. The name comes from the environment (or the one installed creds file), never from the conversation. `claude --resume` and `--continue` need it again: see [Sessions and resume](install.md#sessions-and-resume).

Fix: start Claude Code as `MASH_ENDPOINT=alice.dev.design claude`, then check `status` (or run `MASH_ENDPOINT=alice.dev.design mash status` first).

If several endpoints are installed and none is named, mash stops with `error: more than one endpoint is installed here: a, b` and `hint: mash status --endpoint a` (the first installed address in alphabetical order): name one with `MASH_ENDPOINT` or `--endpoint`. If it says to fix `MASH_ENDPOINT`, the value is not three dotted parts `owner.group.name`, each 1-32 lower-case letters, digits and single hyphens, starting with a letter.

## `warning: gh ...: checked checksums only, not verified` from `install.sh`

Message, on stderr: `warning: gh not found: checked checksums only, not verified`, or the same with `gh did not run`, `gh version unreadable` or `gh is below 2.102.0` in place of `gh not found`. It is not an error: the install went on. It means the installer checked each binary against `SHA256SUMS` but could not check that `SHA256SUMS` itself came from the release's own workflow, so the download was not verified.

Fix, if you want the full check: install or update `gh` ([cli.github.com](https://cli.github.com/)) and run the install line again, or check by hand as [Verify what you download](install.md#verify-what-you-download) shows.

## `the release failed verification`

`install.sh` stopped with `error: the release failed verification; nothing was installed`. Nothing was installed. Do not install from that download. Run the manual check in [Verify what you download](install.md#verify-what-you-download) for the tag you meant, and if it fails there too, report it through [SECURITY.md](../SECURITY.md).

## `is writable by its group or other users: not installing there` from `install.sh`

`install.sh` refuses a folder that its group or other users can write to, and it checks every folder above the install folder too. Nothing was installed. Claude Code's installer can leave `~/.local` and `~/.local/bin` writable by their group, so this is common when Claude Code was installed first.

Fix: the message names every such folder, nearest first, and prints one `chmod go-w '<folder>' '<folder>'` command that fixes them all (each folder is in single quotes, so a path with a space pastes safely). Run that command and run the install line again. Or set `MASH_INSTALL_DIR` to a folder only you can write to.

## `mash is not installed`

Full text: `mash is not installed: the mash plugin found no mash program. Install it: curl -fsSL https://github.com/bogworks/mash/releases/latest/download/install.sh | bash`. (That is the plugin's own text; do not pipe it. Download `install.sh` and `SHA256SUMS`, check, then `bash install.sh`, as the [install guide](install.md) shows.) The plugin's session-start hook prints it as one line on stdout, so the agent sees it and can tell you; the MCP launcher prints it on stderr after `mash launcher:` lines and exits 127, and the MCP server does not start. The line always names the project's own latest release, even when `MASH_RELEASE_BASE` is set.

Causes:
- The plugin is unpinned (a repository checkout) and there is no `mash` on the `PATH` of the process that started it. An unpinned launcher never downloads.
- The plugin is pinned but the download failed: offline, `no pinned sha256 for ...`, `sha256 mismatch ... refusing`, `unsupported OS` or `unsupported architecture`, or neither curl nor wget available. A download that does not match is deleted, and there is no `mash` on the `PATH` to fall back on.

Check: `command -v mash` and `mash version` in the shell Claude Code was launched from. Read the `mash launcher:` lines.

Fix: run that line (it installs the attested release and says where), and make sure its directory is on the `PATH` of the launching environment. Or set `MASH_BIN` to a binary path. Hooks never wait more than 5 seconds for a download, and after a failure skip downloading for 5 minutes ([install.md](install.md) section 1).

## The `config` row fails, or `no server is set`

`mash doctor` shows `config  FAIL` and a hint. Messages: `mash: no server: set MASH_SERVER, --server, or "servers" in config.json`. A bad value names its source, for example `mash: MASH_IDLE_WAKE ...` or `mash: MASH_SPOOL_FROM_START="x" is not 1, true, 0 or false`.

Fix: set `servers` in `~/.config/mash/config.json`, or `MASH_SERVER`, and set `MASH_ENDPOINT`. A flag wins over the environment, and the environment over the file. The owner (the first part of the endpoint) is 1 to 32 lower-case letters, digits and single hyphens ([install.md](install.md) section 4).

mash reads `$MASH_CONFIG_DIR/config.json` when that variable is set (an absolute path; no other directory is consulted), else `$XDG_CONFIG_HOME/mash/config.json` when `XDG_CONFIG_HOME` is an absolute path and that file exists, and otherwise `~/.config/mash/config.json` under `$HOME`. A leading `~/` (or a bare `~`) in `creds` and `admin_creds` is expanded to your home; `policy.md` and default creds files are also found in `~/.config/mash` when the XDG directory has no copy; `~user/` is not. A `policy.md` that exists but cannot be read is a configuration error that names the file (`mash: reading <path>: ...`), not an empty policy; fix its permissions or remove it. With neither `HOME` nor an absolute `XDG_CONFIG_HOME` set (some systemd units and minimal containers), mash stops with `mash: cannot find a config directory: set HOME or XDG_CONFIG_HOME, or use the MASH_* variables`; set one of them, or give everything through the `MASH_*` variables, including `MASH_CREDS`.

## The creds file is missing or refused

- `the creds file is missing: <path>` (`status`), or `creds  FAIL` in `doctor`: the file for this endpoint is not at `<owner>.<group>.<name>.creds` in the config directory (`~/.config/mash` unless `XDG_CONFIG_HOME` points at a mash config). Put it there or set `MASH_CREDS` (or `creds` in config.json).
- `the creds file can be read by other users: <path>` (`status`), `creds  FAIL` in `doctor` with the hint `chmod 600 <path>`, or `mash: credentials file <path> has mode 0644: it is readable by other users. Run: chmod 600 <path>`: run exactly that. Creds files hold the endpoint's private key and must not be readable by others.
- A creds path that is a directory or is not readable: fix the path or ownership.
- `mash: <path> is not operator-mode JWT credentials ...`: the file has no user JWT or no seed (a bare token, a `user:password` line, a plain nkey seed, an empty file, or a JWT without its seed). mash supports only JWT user credentials from an operator-mode server ([requirements](nats-server.md#requirements)). Next: make the endpoint again with `mashctl endpoint create` and `mash join` ([operations.md](operations.md#adding-an-endpoint)), or ask whoever runs the server for a creds file, and set `creds` (or `admin_creds`) to it.
- A server url that carries a password or token: mash ignores it. Remove it from the url and use a creds file.

## `mash: endpoint X is not provisioned`

The directory has no record for the address. Check `MASH_ENDPOINT` for a typo, then as the admin run `mashctl endpoint list` to see which endpoints exist. If it is missing, make it with `mashctl endpoint create alice.dev.design` (on the `mashctl` host), or with `--out alice.dev.design.join` and `mash join` on the endpoint's machine ([operations.md](operations.md#adding-an-endpoint)). The address in `endpoint list` may differ from the one you started: an endpoint that was deleted keeps its address burned, so it cannot be made again.

## `do not match endpoint`

Two forms: `mash: the credentials in <path> do not match endpoint X (they are for another endpoint)` and `mash: the credentials in <path> (user U) do not match endpoint X (user V)`. The creds file belongs to another endpoint, or the directory record names a different NATS user key. Use the right creds file for the address, or compare the user in `mashctl endpoint show alice.dev.design` with the user in your creds. To give the endpoint a new key, run `mashctl endpoint reissue alice.dev.design` (the unread mail stays), or with `--out alice.dev.design.join --public-key design.pub` for a key made on the endpoint's machine. Do not delete the endpoint to fix this: `mashctl endpoint delete` removes the inbox stream, so every unread message in it is lost, and the address is never reused.

## After `/login` the old serve is still listed

A new `mash serve` of the same session takes the lease from the earlier one at once, so mail is on in the new one. The earlier serve says it was superseded and exits when its client closes it. Nothing to fix.

## The session says the address is in use or waiting

The session's tools say `mash is waiting for endpoint X (held by ...); mail is off until it is free`.

Cause: another live session holds the address, or one that died within the last 90 seconds does. The session retries every 10 seconds, tells Claude when mail is on, and needs no restart.

Fix: end the other session, or start this one with a different `MASH_ENDPOINT`. Addresses are unique per NATS account, so a teammate who uses the same owner, group and name counts. `mash who` lists the endpoints.

## `mash: lost the lease on X to another session`

Shown as `mash: endpoint X stopped: ... Mail is off for this session.` The lease is rewritten every 30 seconds and expires after 90. If the lease expires while the session sleeps or loses the network (longer than 90 seconds) and no one else took the name, the session takes it back at its next refresh. If another session took it, or the endpoint was deleted, the loss is final: the session stops and does not try again. Restart Claude Code with the same `MASH_ENDPOINT`; the restart takes the name once no one else holds it. See [Sessions and resume](install.md#sessions-and-resume).

## The server does not answer, or refuses the credentials

- `the server did not answer: <address>` (`status`), or `server  FAIL` in `doctor`: mash could not reach the server. Check `servers` and that the host is reachable, and the clock (below). A running session retries on its own. On a slow link raise `open_timeout` (default 20s) or `call_timeout` (default 15s).
- `the server's certificate is not trusted: <address>` (`status`), or `server  FAIL` in `doctor`: the server answered with a certificate this machine does not trust, such as a self-signed one. Set `ca` (or `MASH_CA`) to the PEM file of the CA that signed it.
- `the server did not offer TLS: <address>` (`status`), or `server  FAIL` in `doctor`: the address is `tls://` but what answered speaks plain text. Use the server's `tls://` address, or its plain address with `MASH_DANGEROUSLY_SKIP_NATS_ENCRYPTION=1` set.
- `the server refused its credentials` (`status`), or `sign-in  FAIL  the server refused these credentials` in `doctor`: the endpoint was deleted or its key reissued, so the old credentials no longer work, or the operator requires signing keys (`strict_signing_key_usage`) and `signing_key` was not set when `mashctl` was set up (`init` does not check this). Ask whoever runs mashctl for a new credentials file (`mashctl endpoint reissue <address>`; `doctor` prints the exact command as its hint).
- After a network blip, clients can take 30 to 60 seconds to reconnect because the server rate-limits new connections per source address. Clients retry with a short random delay; whoever runs the server can raise the limit for trusted sources if this matters.
- `x509: certificate signed by unknown authority` (or another `x509:` error) on connect: the server's certificate is not trusted. Set `ca` (`MASH_CA`) to the PEM file of the CA that signed it. The files are read at connect time.
- `remote error: tls: bad certificate` (or `certificate required`) on connect: the server requires a client certificate, or refused the one given. Set both `tls_cert` and `tls_key` (`MASH_TLS_CERT`, `MASH_TLS_KEY`) to a certificate the server's CA signed.
- `doctor` warns on the `server` row (`is plain text: MASH_DANGEROUSLY_SKIP_NATS_ENCRYPTION is set`): use `tls://` with the server's certificate and unset the variable. It fails on a plain url when the variable is not set: mash refused it before connecting, loopback included.
- Synadia Cloud is not supported: see [synadia.md](synadia.md).

## An administrator's check: `mashctl doctor`

On the machine that runs `mashctl`, `mashctl doctor` checks the server, mash's storage on it and every endpoint's records in one run, changes nothing, and prints the command that fixes the first thing that is wrong (`hint:`). It exits 1 on a problem. See [doctor.md](doctor.md) for the checks.

## `send` fails: message too large

`mash: message is N bytes with headers; this server accepts at most M (its max_payload). Send a smaller message or put the content in a file and send the path.` Or, when the server allows more than mash does: `mash: message is N bytes with headers; the limit is 1048576`. Nothing was published. Mash's cap is 1 MiB including headers, or the server's `max_payload` if smaller. Send a file path instead of the content.

## `send` refused by policy

`refused by policy: you may not mail X`. The server refused the publish because the sender's NATS permissions do not include X's mail subject. It fails at once (not after a timeout) when only one send to X is in flight; with several overlapping sends to X a violation cannot be attributed, so they time out instead and the timeout says so, nothing was stored, and retrying with the same credentials will not help. The message ends with the send's key (`If you retry, use --id KEY`): in the rare case the refusal was a violation delayed from an earlier timed-out send and the message was in fact delivered, a retry with that key stores it at most once. The default permission set lets an endpoint mail every endpoint of its owner, so this means the sender was issued a narrower set (a hand-edited allow list). If X is an endpoint of the sender's owner, the credentials may be older than the permissions mash issues now, or the permissions were narrowed: ask the admin to check them. Otherwise ask the admin to issue a set that includes X, or mail a recipient you are allowed to. See "Refused sends" in [operations.md](operations.md).

## `name rule` refused, or mash will not start with a name-rule file

`mash: name rule file X: ...` at start means the file named by `MASH_NAME_RULE_FILE` (or `name_rule_file`) cannot be read or does not follow the format; the text after the path says which condition. `a name rule is required ... but no rule file is set` means `MASH_NAME_RULE_REQUIRED` is `true` and no file is named. `name rule "ID" refuses ...` is a refusal by the rule `ID`, with the rule's own message: the identity is not accepted in this deployment. See "The name rule" in [operations.md](operations.md) and [name-rule.md](name-rule.md).

## `send` timed out

`mash: send to X timed out; the message may or may not have been delivered. Retry with --id KEY ... within 2 minutes to send it at most once; after that a retry may store a second copy.` Re-run the same send with the key it printed: `mash send <to> --id <key> ...` (the `send` tool takes `id`). Within the window a duplicate reports `Mail already sent; nothing new stored`. Past 2 minutes, check the recipient's inbox before retrying.

## A peer shows `offline` in `who`

`mash who` prints each endpoint's address and `online` or `offline`. `who` asks every running endpoint at that moment (the `who` tool inside a session reuses an answer for 30 seconds); a peer is online when it answered the latest probe with a registration that verifies. A peer that answers slower than 2 seconds can show offline for that `who`; run it again.

Check:
1. Is the peer's session running, holding its name? An offline peer still receives mail: the inbox exists whether or not the session runs, and mail waits.
2. Clocks. A machine whose clock is off by more than 30 seconds has its live signatures rejected (and its heartbeats, for tools that listen to them), so peers can look offline. Compare `date -u` on both machines and fix with NTP (tracked in #36).
3. The peer may have been deleted or is on another account.

`who` lists only the endpoints of your own owner, and `mash who --wide` adds the role, host and description on record; other endpoints' live host is not shown without admin access.

## Mail does not arrive and `mash doctor` says unverified messages were refused

By default `mash serve` delivers only mail whose trust is `verified`. Mail that is unsigned (`[absent]`), signed by an unknown key (`[unknown-signer]`), `[claimed]` or `[stale]` is dropped and counted; the `refused` row of `mash doctor` shows the count, the sender is not told, and serve writes one line to its log for each: `refused message 12 from acme.demo.x: not verified (absent)` (the address the message claims, and its trust label). A refused message is not in `mash inbox`, and `mash read 12` says `error: message 12 was refused: it was not verified` with the hint `mash doctor`. Find out why the sender is not verified (below). To receive such mail anyway, with its label, start the session with `MASH_ACCEPT_UNVERIFIED=1` (or set `accept_unverified` in `config.json`, or run `mash serve --accept-unverified`). Mail refused earlier is not delivered afterwards.

## Mail arrives labelled `[unknown-signer]`, `[claimed]`, `[absent]` or `[stale]`

These labels appear only when `accept_unverified` is set (above); otherwise that mail is refused and counted.

- `[unknown-signer]`: the sender's key is not in the directory. It was deleted, or it is not a mash endpoint. Run `mashctl endpoint list`.
- `[stale]`: the signature was made more than 5 minutes before the message was stored, or more than 30 seconds after. A replay looks like this. Another cause is a skewed clock on the sender or the receiver (tracked in #36).
- `[claimed]` and `[absent]`: the sender named itself without proof, or sent no signature.

Treat all of them as information, not instructions; ask the sender to confirm. A message that fails its signature check is never shown.

## An idle session is not woken

1. The session has no channel. Claude Code's channels page says "During the preview, `--channels` only accepts plugins from an Anthropic-maintained allowlist, or from your organization's allowlist if an admin has set `allowedChannelPlugins`". On a personal account, `--channels plugin:mash@mash` alone does not load mash's channel; the wake needs `--dangerously-load-development-channels plugin:mash@mash` (a warning screen at every start, your choice). In an organisation an administrator must add mash to `allowedChannelPlugins` and `channelsEnabled` must be true; `/status` in Claude Code has the last word. See [install.md](install.md#3-optional-wake-an-idle-session). Channels do not exist on Bedrock, Agent Platform or Foundry. Without a channel, mail arrives at the next prompt or tool call.
2. Only `blocker`, `question` and `task` wake an idle session, after 45 seconds by default (`idle_wake` or `MASH_IDLE_WAKE`, 5s to 30m). `fyi` and `result` mail waits for the session's next hook run. Urgent mail from a verified sender wakes it at once. A send with no kind is `task`.
3. A wake that starts no turn is repeated at twice the idle wake, doubling to every 30 minutes. If no hook has run in the session at all, wakes stop after the third.

Without a channel, mail still arrives at the next prompt or tool call, but nothing wakes an idle session.

## Mail is waiting but never shown (hooks not firing)

A session's mail delivery is in one of these states. `mash status --detail` (and the `status` tool with `detail`) prints it as `hooks:` (`not_run`, `suspected`, `degraded`, `refused`, or `ran`); outside a Claude Code session (a plain shell) it shows `session: none` instead. And `mash doctor` shows it on the `delivery` row: `warn  hooks have not run in this session yet; they run with the first prompt or tool call` (no hint: no command fixes it), or `FAIL  hooks are not delivering mail to this session; restart the session after reinstalling` for the last three, with the hint `claude plugin install mash@mash`. A session that has just started warns until its first prompt or tool call, so `mash doctor --strict` exits 1 until then: a program that waits for a session to be ready repeats the check. A host that has no hooks warns for as long as it runs (known issue #993). `hook.log` shows the symptoms:
- Hooks have not run yet in this session: normal until the first prompt or tool call.
- Suspected: three wakes and no hook; re-waking stopped. A session that makes no tool calls stays here. Mail is read with the `read` tool.
- Degraded: no mash hook has run although this session has made tool calls. Hooks are not running. Read each message with the `read` tool (by its number).
- Refused: mash's hook runs in this session but is not recognised as this session's: the hook ran outside the process tree of `mash serve` and its session id did not match.

Causes and fixes for degraded:
- `disableAllHooks: true` in a user, project, local or managed settings file: remove it.
- Managed `allowManagedHooksOnly`: an administrator must force-enable `mash@mash` in managed `enabledPlugins`.
- The plugin is not enabled in this project, or its hooks did not load: check that it is enabled for the project, then restart.
- `mash` is not on the hook's `PATH`: look for `command not found` in `hook.log`. The launcher also logs download problems there.

Fix for refused: start Claude Code with a fresh `MASH_LAUNCH_TOKEN` per launch, for example `MASH_LAUNCH_TOKEN=$(uuidgen) claude`. Never reuse a value across launches or share it between sessions. `hook.log` then shows `mash hook: not this session's spool (...); mail left for its owner`. For more detail set `MASH_HOOK_TRACE=1` ([operations.md](operations.md#hook-delivery)).

The hook always exits 0, so a failing hook is silent; `hook.log` is where its errors are.

## Mail does not arrive while a subagent works

Subagents have no mailbox, and their tool calls do not deliver mail. It waits for the main session's next tool call or prompt, or for the idle wake. See [Hook delivery](operations.md#hook-delivery).

## A digest lists messages, but their text is missing

A batch of more than 10 messages or 16 KiB is shown as a digest: one line per message with an unquoted preview of its first line, which is the sender's text. Ask Claude to call `read` with the message number. Listed messages are not acked: they stay unread until `read` is called for their number, and they do not wake the session again. `inbox` lists the messages the endpoint holds, with their state (`new` until the session has been told of it), and never acks. After a restart of the session, unread listed mail is listed again once (see [Hook delivery](operations.md#hook-delivery)). Senders should still put the action in the first line.

## Mail from the recipient's side seems to be missing

- The inbox keeps 30 days or 16 MiB and drops the oldest first, unread included. Run `mash inbox --after N --limit 50` and compare the NUMBER column.
- If the sender saw `Mail sent (number N in <recipient>'s inbox)` before a host crash of a self-hosted server, up to about 2 minutes may be lost without `sync_interval: always` ([operations.md](operations.md#durability-and-sync_interval)).
- `mashctl limits show` prints the limits in force.

## `mashctl limits apply` refuses

`mash: inbox of X has max_bytes ..., above the configured ...; mash never lowers a limit, because that could discard stored mail; to lower it, run mashctl endpoint delete X and create an endpoint under a new name (a deleted address cannot be used again)`. Limits only go up. Deleting the endpoint removes the mail in that inbox, and its address cannot be used again. The same applies to `max_age` ([operations.md](operations.md#changing-limits-later)). Run `mashctl limits apply` from one admin at a time.

## Account full or `maximum number of streams`

Each endpoint takes one stream, and mash's shared buckets take more (see [Sizing](nats-server.md#sizing)). Remove an endpoint with `mashctl endpoint delete <owner.group.name>` and an admin credential.

## `jetstream not enabled` (code 503, err_code 10076) or `the server could not enable JetStream for the account`

Symptom: `mashctl init --config` or any JetStream call fails with `nats: API error: code=503 err_code=10076 description=jetstream not enabled` (or `no responders available for request`), and mash adds "the server could not enable JetStream for the account: it could not reserve the account's disk limit (2 GiB). nats-server gives JetStream 75% of the free disk by default, so the free disk under the server's store directory is probably under about 2.7 GiB".

Cause: the account's disk limit is more than the server's JetStream budget (75% of the free disk by default; see [Requirements](nats-server.md#requirements)), so the server silently leaves JetStream off for the account. The account JWT looks fine, because the limit is only a request: `mashctl init --config` asks the server for the account's JetStream info and is the check that catches it. What the account needs is on [Sizing](nats-server.md#sizing).

Fix, either one: free disk under the server's store directory (or move the store to a larger disk), then restart the server; or lower the account's limit, for example `nsc edit account NAME --js-disk-storage 1G`, then push the account.

## The environment is stripped

Claude Code passes MCP servers only a baseline environment, so `MASH_ENDPOINT` cannot reach `mash serve`. mash is unsupported in that mode until the organisation drops the setting. Put `servers` and `endpoint` in config.json.

## Reporting a problem

Include `mash version`, the `mash status` and `mash doctor` output (neither prints creds), the relevant `hook.log` lines, and your Claude Code version. Security issues go through [SECURITY.md](../SECURITY.md).
