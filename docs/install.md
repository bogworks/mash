# Installing mash

This walks through one machine running Claude Code sessions with mash. Provisioning the server side (the account, endpoint users and their inboxes) is in [operations.md](operations.md).

**Requirement:** mash needs a NATS server in operator mode with JWT user credentials. Token, user/password and plain nkey authentication are not supported, because an endpoint's SAP signing identity is its NATS user key (the key in its creds file's user JWT; see the [design](specs/2026-10-21-mash-design.md)).

## Requirements

- Claude Code 2.1.312 or newer (the oldest version a live receipt passed on; mash does not refuse older ones, `status` warns). The plugin and the `mash` binary should be the same release (major and minor); `status` warns when they are not.

## 1. The binary

Supported platforms: Linux and macOS (amd64, arm64), the release targets (`mash-{linux,darwin}-{amd64,arm64}`, see [release.md](release.md)). Native Windows is not supported: the spool lock and the hooks need Unix file locking and a POSIX shell. WSL 2 with the Linux binary is expected to work but has not been tested.

mash is one static Go binary. **Install it first (below) until your plugin release is pinned**: the plugin fetches and verifies its own copy only once it carries a `mash.pins` file from a release (the post-release commit described next), and an unpinned plugin, which is what a checkout of the repository or `mash plugin export` gives you, uses the `mash` on your `PATH` and does not start without one. With a pinned plugin there is nothing to install by hand: no `go install`, no `PATH` edit, no root.

- A `SessionStart` hook prefetches the binary into the plugin's data directory (`${CLAUDE_PLUGIN_DATA}/bin`) on first use, so the MCP server starts without a download. The server launcher downloads it as a fallback.
- Once pinned after a release (a post-release commit of `plugin/scripts/mash.pins`), the plugin carries a `mash.pins` file beside its launcher with the release version and the sha256 of each platform binary (linux and darwin, amd64 and arm64); without it (including the plugin written by `mash plugin export`) the launcher is unpinned and uses `mash` from PATH. A download that does not match is refused and deleted, and the binary must report the pinned version before it is used. Pinning also bumps the plugin's version to `<version>-pin.1`, so installed plugins update and receive the pin. Updating the plugin updates the binary.
- Resolution order: `$MASH_BIN` (an explicit override), the downloaded copy for the pinned version, then (pinned) a fresh download of it, falling back to `mash` on the `PATH` only if that download fails, so an older install on the `PATH` never overrides the pin. Hooks never wait more than a few seconds for a download (a 5 s budget, then `PATH`, and no retry for 5 minutes after a failure). An unpinned plugin uses `mash` on the `PATH` only and never downloads.
- Verification (provisional): the environment variable `MASH_PLUGIN_REQUIRE_VERIFIED` decides whether the launcher may ever run a binary it has not verified against `mash.pins`. It is off by default, which is today's behaviour: a failed pinned download falls back to `mash` on the `PATH` with a warning naming what failed, an unpinned plugin uses `mash` on the `PATH` with a warning naming the pins file, and a cached downloaded binary is used as it is. When it is on (`1`, `true`, `yes` or `on`; `0`, `false`, `no` and `off` are off), the launcher exits 127, with what failed and how to turn the setting off on stderr, when the plugin is unpinned (no `mash.pins`, or one that is empty, unreadable or has no valid `version=` line), when the cached downloaded binary's sha256 does not match `mash.pins` (it is checked on every run, which costs a hash of the binary, and it is neither re-fetched nor replaced by `mash` on the `PATH`), and when the pinned binary cannot be fetched or fails verification (a failed download, a sha256 mismatch, no pinned sha256 for your platform, an unsupported OS or architecture, no hashing tool, a downloaded binary that does not run or reports the wrong version, or a hook skipping a download that failed recently). It never runs a `mash` from the `PATH`. When it is on, a hook therefore keeps refusing for 300 s (5 minutes) after a failed download; that is by design. Any other value, including an empty one, is treated as on, with a note naming it. mash only reads this concrete setting: which deployment profile maps to which settings is decided by the layer above mash. The variable's name, its source and the default are provisional and may change; the launcher reads the setting in one place. `$MASH_BIN` is an explicit operator choice, trusted as given and never verified, whether the setting is on or off, and a successful verified download and the prefetch are not affected.
- The MCP server runs `${CLAUDE_PLUGIN_ROOT}/scripts/mash` directly, so the launcher needs its exec bit: git-based marketplace installs preserve the mode and `mash plugin export` sets it. The hooks run it as `sh ${CLAUDE_PLUGIN_ROOT}/scripts/mash`, which needs no exec bit.
- Offline, or on an unsupported platform, the plugin uses a `mash` already on the `PATH` or a copy it downloaded earlier; if there is neither, it says so on stderr and the server does not start.

To install the binary yourself (required for an unpinned plugin; also for the CLI in section 5, or an air-gapped machine):

```sh
go install github.com/bogworks/mash/cmd/mash@latest
go install github.com/bogworks/mash/cmd/mashctl@latest   # the admin tool
mash version
```

`mashctl` is built from source for now; it is not yet in the release assets.

## 2. The plugin

The mash repository is its own Claude Code marketplace:

```sh
claude plugin marketplace add bogworks/mash
claude plugin install mash@mash
```

The plugin declares an MCP server, `mash serve` (started through the plugin's launcher script, with `MASH_SPOOL_FROM_START=1`), a SessionStart hook that prefetches the pinned binary, and two hooks, PostToolUse and UserPromptSubmit, which run `mash hook`. Mail reaches the session through those hooks, as "mash mail" context; see [Hook delivery](operations.md). A session without a mash identity (no `MASH_NAME`) gets an inert server with only the `status` and `version` tools, so it is safe to install for every session.

**Install scope.** `claude plugin install` installs at user scope by default (`--scope user`), which records the plugin in `enabledPlugins` in `~/.claude/settings.json` so every session on the machine has mash; a session without `MASH_NAME` stays inert. Project scope (`.claude/settings.json`) and local scope (`.claude/settings.local.json`) enable it in one repository only, so the mash server and hooks are absent in every other project. A narrower scope overrides a wider one, so a project can turn off a user-scope install. An organisation can force-enable `mash@mash` in managed `enabledPlugins`, which Claude Code lists at Managed scope and users cannot disable in `/plugin`; that is also what keeps the hooks under `allowManagedHooksOnly` (below). The `status` tool says which parts are missing in a session. See [Choose an install scope](https://code.claude.com/docs/en/plugins/install#choose-an-install-scope) in the Claude Code docs.

Mail needs those hooks to run. They do not run with `disableAllHooks: true` in any Claude Code settings file, or under a managed `allowManagedHooksOnly` unless an administrator force-enables `mash@mash` in managed `enabledPlugins`. Then mash stops waking the session after a few tries and `status` reports `delivery: DEGRADED`; see [Hook delivery](operations.md).

## 3. Allow the channel (once per machine)

Mail itself is delivered by the hooks (section 2); the channel carries a one-line summary of waiting mail, which wakes an idle session so its next prompt runs the hook. Claude Code runs only allowlisted channel plugins without a confirmation dialog. Allowlist mash in the managed settings file:

| OS | Path |
|---|---|
| macOS | `/Library/Application Support/ClaudeCode/managed-settings.json` |
| Linux | `/etc/claude-code/managed-settings.json` |

```json
{
  "channelsEnabled": true,
  "allowedChannelPlugins": [{ "marketplace": "mash", "plugin": "mash" }]
}
```

Managed settings are written by an administrator (`sudo`). If the file already exists, merge these keys into it. Note that `allowedChannelPlugins` replaces the default allowlist; list any other channel plugins you use as well.

> **If your organisation manages Claude Code.** Claude Code uses the highest-ranked managed source that delivers at least one policy key and ignores the rest ([how sources combine](https://code.claude.com/docs/en/managed-settings#how-claude-code-combines-managed-sources)). Server-managed settings, pushed from the Claude.ai admin console, rank first, so when your organisation uses them the local file above is ignored. An organisation Owner must add `{ "marketplace": "mash", "plugin": "mash" }` to `allowedChannelPlugins` there and enable Channels (Organization settings > Claude Code > Channels). Bedrock, Agent Platform and Foundry have no channels. Without a channel, hooks still deliver mail at the next prompt or tool call, but the idle wake and live prompts are lost. If the policy sets `allowManagedHooksOnly`, the hooks need mash in the managed `enabledPlugins` as well (see [Hook delivery](operations.md)). `strictKnownMarketplaces` can also refuse `claude plugin marketplace add bogworks/mash`. Confirm what applies with `/status` ("Setting sources") and `claude doctor` ("Managed settings (remote)"); the mash `status` tool shows its own reading as the `channel allowlist:` line, and `/status` has the last word ([ADR 0039](decisions/0039-mash-reads-claude-codes-managed-settings-read-only-for-diagnosis.md)). The development flag below skips the allowlist but not `channelsEnabled`.

Without this, `--channels plugin:mash@mash` is refused ("not on the approved channels allowlist") and the channel wake that rouses an idle session does not arrive. You can still try mash with `--dangerously-load-development-channels plugin:mash@mash`, which asks for confirmation on every launch.

## 4. Configuration

On one machine with no NATS server yet, `mashctl init --local --owner <name> --start` sets up a local operator-mode server with JetStream and writes this configuration for you (see [Local setup](operations.md#local-setup-with-mashctl-init---local)); the rest of this section is for an existing server.

`~/.config/mash/config.json`, or `$XDG_CONFIG_HOME/mash/config.json` when `XDG_CONFIG_HOME` is an absolute path and that file exists (an existing `~/.config/mash` install keeps working). With no `HOME` and no absolute `XDG_CONFIG_HOME`, mash stops with an error unless everything comes from the `MASH_*` variables. A leading `~/` in `creds` and `admin_creds` (and in `MASH_CREDS` and `MASH_ADMIN_CREDS`) is expanded to your home; `~user/` is not. `policy.md` and the default creds files live in the same directory; when the XDG directory has no copy of one, mash also finds it in `~/.config/mash` (the XDG copy wins when both exist). A relative `admin_creds`, `tls_ca`, `tls_cert` or `tls_key` in `config.json` is resolved against the config directory, not the working directory. `config.json` that is writable by every user is refused; run `chmod go-w` on it.

```json
{ "url": "tls://nats.example.com:4222", "owner": "alice" }
```

| Key | Env | Meaning |
|---|---|---|
| `url` | `MASH_URL` | The NATS server. Use `tls://`: SAP identity is only meaningful over a verified connection. A plain `nats://` url to a non-loopback host makes `status` print `tls: WARN` and serve warn once on stderr. For a private CA or a server that requires client certificates, set `tls_ca`, `tls_cert` and `tls_key` below. |
| `tls_ca` | `MASH_TLS_CA` | PEM file of the CA that signed the server's certificate, for a private CA. Without it the system trust store is used. |
| `tls_cert`, `tls_key` | `MASH_TLS_CERT`, `MASH_TLS_KEY` | PEM client certificate and its private key, for a server that requires client certificates (mutual TLS). Set both or neither. |
| `owner` | `MASH_OWNER` | Your SAP owner token (lower-case letters, digits, single hyphens). |
| `creds` | `MASH_CREDS` | The endpoint's credentials file. Default `<name>.creds` in the config directory (`~/.config/mash/<name>.creds` by default). |
| | `MASH_CONFIG_DIR` | The config directory (`config.json`, `policy.md`, default creds). An absolute path (a relative one is an error); it wins over `XDG_CONFIG_HOME` and the legacy `~/.config/mash`, which is not consulted. |
| | `MASH_STATE_DIR` | The directory that holds `spool/` and `hook.log`. An absolute path (a relative one is an error). Default `$XDG_STATE_HOME/mash` if `XDG_STATE_HOME` is absolute, else `~/.local/state/mash`. |
| | `MASH_NAME` | The endpoint name, set per session at launch. Apps started from a GUI or IDE usually do not inherit your shell's variables, so set it where Claude Code is launched (the IDE's own environment setting, or the launch command). `mash status` shows what a session would see, before you launch. |
| | `MASH_RELEASE_BASE` | Plugin launcher only: the base URL the pinned binary is downloaded from, default `https://github.com/bogworks/mash/releases/download`; the launcher fetches `<base>/v<version>/mash-<os>-<arch>`. It changes only where the bytes come from: the sha256 pin still applies, and a download that does not match is refused and deleted. Plain `http` is not refused, so use an `https` base. |
| | `MASH_PLUGIN_ROOT` | Set by the plugin's `.mcp.json` to the plugin's own root (`${CLAUDE_PLUGIN_ROOT}`). `mash serve` and `mash status` read the plugin's version from `<root>/.claude-plugin/plugin.json` to report whether the plugin and the binary versions have drifted (a major or minor difference). You normally never set it. |
| | `MASH_MANAGED_SETTINGS_DIR` | Absolute path: the directory where `status` looks for Claude Code's managed settings, default the OS location; for non-standard installs and tests. A relative value is ignored. |
| | `MASH_LAUNCH_TOKEN` | Optional. Names this launch of the agent (up to 128 of `A-Za-z0-9._-`); written into the endpoint's lease as `launch_token` so a supervisor can tell which launch holds it. It also ties a session's hooks to its `mash serve`: a hook that carries the same non-empty token as serve is recognised as this session's even where its process chain cannot be followed (wrappers, sandboxes, IDE hosts). Use a fresh value for each launch (for example `MASH_LAUNCH_TOKEN=$(uuidgen) claude ...`); a value reused across launches defeats the hook-ownership rule. |
| `settle_ms` | `MASH_SETTLE_MS` | Quiet time before a batch is pushed. Default 100. |
| `batch_msgs`, `batch_bytes` | `MASH_BATCH_MSGS`, `MASH_BATCH_BYTES` | A batch up to 10 messages and 16 KiB is shown in full; more becomes a digest. |
| `spool_from_start` | `MASH_SPOOL_FROM_START` | `1`/`true` (or `0`/`false`; anything else is an error). Spool mail from the first message, to be shown by the hooks, instead of delivering it by channel until a hook has run: Claude Code drops channel events that arrive before its first prompt. The plugin sets it. Mail that wakes (not fyi or result) wakes the session at once until the first hook has run. Without a usable spool, mail is held unacked in the inbox (read it with the inbox and read tools). |
| `spool_required` | `MASH_SPOOL_REQUIRED` | `1`/`true` (or `0`/`false`; anything else is an error). Default off. Fail closed: mail is never delivered by the channel. It is spooled from the first message and acked only after a hook has drained it; mail the spool cannot take (no spool, or a failing one) is held unacked in the inbox, and the failure is reported on stderr, in a channel notice and in the `status` tool's `spool: FAILING` line, which clears when the spool works again. A spool that cannot be made at start is retried every 5 seconds. Held mail is read with the inbox and read tools, and is delivered once the spool recovers. |
| `insecure_loopback` | `MASH_INSECURE_LOOPBACK` | `1`/`true` (or `0`/`false`; anything else is an error). Allows plain `nats://` for the admin and system connections of `mashctl` (and `mash admin`) to a loopback server (`localhost`, `127.0.0.0/8`, `::1`); a URL with any other host is refused. `init --local` writes it into the `config.json` it creates. It is the one minimal escape and may be renamed when the command-line settings are redesigned. Endpoint connections are not affected. |
| `idle_wake` | `MASH_IDLE_WAKE` | Hook mode: how long spooled mail waits for a hook before serve wakes the idle session. A Go duration, 5s to 30m. Default `45s`; re-wakes double from there, to 30 minutes. |
| `relay_window` | `MASH_RELAY_WINDOW` | serve: how soon after mail from one sender a send to another endpoint is traced as a relay candidate (see "Relay tracing" in operations.md). Nothing is blocked. A Go duration, 1s to 1h. Default `60s`. |
| `open_timeout` | `MASH_OPEN_TIMEOUT` | How long a command or serve may spend opening: connecting to its data and checking its identity. A Go duration above zero (for example `"45s"`), for slow links. Default `20s`. |
| `call_timeout` | `MASH_CALL_TIMEOUT` | How long one command, or one tool call in serve, may spend on its requests once open. A Go duration above zero. Default `15s`. |
| `lease_retry` | `MASH_LEASE_RETRY` | serve: the wait between attempts to take an endpoint's lease while another session holds it. A Go duration above zero. Default `10s`. |
| `transient_budget` | `MASH_TRANSIENT_BUDGET` | serve: how long transient failures (timeouts, no responders, a dropped connection) may repeat while waiting for the lease before the wait ends. A Go duration above zero. Default `15m`. |
| `transient_max` | `MASH_TRANSIENT_MAX` | serve: the retry backoff after transient failures doubles from `lease_retry` up to this. A Go duration above zero. Default `2m`. |
| `cli_timeout` | `MASH_CLI_TIMEOUT` | The total deadline of one command (`send`, `inbox`, `who` and the rest), open included. A Go duration above zero. Default `15m`. |
| `admin_timeout` | `MASH_ADMIN_TIMEOUT` | The total deadline of one `mash admin` command. A Go duration above zero, for a large fleet or a slow link. Default `30s`. |

The environment wins over the file. Credentials files must be mode 0600; they hold the endpoint's private key, and mash refuses to use a credentials file that other users can read (fix it with `chmod 600`). `mashctl` applies the same check to the admin credentials and to the system account credentials (`--sys-creds`, `MASH_SYS_CREDS`, or `sys.creds` in the server directory). A symlink is followed and the file it points to is checked.

Optional: the session instruction file `~/.config/mash/policy.md` (not the enforced `policy.yaml`, which `mashctl` compiles into NATS permissions) is appended to every session's instructions. By default, mail from a verified mash endpoint is treated as a delegated request from the fleet's operator; use `policy.md` to carve out what still needs a human, for example "Ask me before spending money or touching production."

## 5. Launch a session

```sh
MASH_NAME=design claude --channels plugin:mash@mash
```

The session registers as a SAP agent, takes the lease on its name, and starts receiving mail. If another live session already holds the name, this one starts without mail tools and says why.

Keep this machine's clock in sync (NTP): more than 30 s of skew from the server makes peers look offline and mail `[stale]`. `status` shows a `clock:` line (see [Troubleshooting](operations.md)).

In the session, Claude has seven tools: `send`, `read`, `inbox`, `who`, `reply`, `version` and `status`. `status` says what works and what is missing, with the next step for each: the name, config, creds, connection and delivery. It answers in every session, even one with no `MASH_NAME` or a start that failed, where it and `version` are the only tools. `version` shows the mash binary's version and commit, the plugin's version and this endpoint's name and owner. `mash status` from a shell prints the same lines for the environment it runs in, without connecting, so a missing name or creds shows before you launch (the connection and delivery lines are live-only and say so). From a shell, the same identity can use the CLI:

```sh
MASH_NAME=design mash who
MASH_NAME=design mash send review "Spec is at docs/specs/x.md; please review section 4."
MASH_NAME=design mash send review --id spec-review-1 "Spec is at docs/specs/x.md."   # safe to re-run: stored once
MASH_NAME=design mash prompt review "Are you still on the auth bug?"
MASH_NAME=design mash inbox --after 120 --limit 50   # the 50 messages after #120, oldest first
```

A live prompt is at most 32768 bytes; a longer one is refused with an error, so send the longer text as mail.

### Sessions and resume

The endpoint name belongs to the launch, not to the conversation. It comes only from `MASH_NAME` in the environment Claude Code starts with ([ADR 0012](decisions/0012-endpoint-chosen-per-launch-by-mash-name.md)).

- `claude --resume` or `--continue` without `MASH_NAME` gives an inert session. With a different name, the conversation gets that name's mailbox, and reusing a name in an unrelated conversation hands it that endpoint's backlog. Resume with the same `MASH_NAME`.
- One live session holds a name. A session that finds its name held starts without mail tools and retries. Do not resume a conversation while its original process is still running: a second process in the same Claude session can drain the holder's spooled mail without any signal ([receipt](evidence/2026-11-01-dual-resume/RECEIPT.md)).
- The lease expires 90 seconds after its last refresh (`store.LeaseTTL`), and a running session refreshes it every 30 seconds (`endpoint.LeaseRefresh`). A laptop that sleeps longer than 90 seconds loses it. When the session wakes, if nobody else took the name, `mash serve` takes it back. If another session took it, the loss is final: lease loss after a start is terminal, and the session must be restarted. Run `status` to see whether the name is still held, and restart with the same `MASH_NAME`; the restart takes the name back once the other holder is gone.

### Lease fields a supervisor can read

The endpoint's lease (key `<name>` in the `mash-leases` KV bucket, rewritten every 30 s and expiring after `store.LeaseTTL`, 90 s) is a JSON object. Besides the holder's identity (`instance_id`, `host`, `pid`, `since`, `version`, ...) and `mcp_seen`, it carries the waking-mail count:

- `waking_pending` (integer, omitted when 0): unread mail that should wake the session: kind `blocker`, `question` or `task` (mail with no or an unknown kind counts as `task`), plus any verified urgent mail whatever its kind. `fyi` and `result` mail does not count. "Unread" is mail the session has not been shown: held for a hook or unacked in the inbox; mail already shown on the channel or by a hook is gone from the count. Reading a message with the `read` tool does not remove it until it is shown.
- `waking_pending_at` (RFC3339 UTC): when the count was computed. It is set whenever the count is known, including 0, so `waking_pending` absent with a fresh `waking_pending_at` means none, and no `waking_pending_at` means unknown (an older mash, or serve has not yet accounted for the mail already waiting at its start). After a start the count stays unknown until that startup backlog has been received, or the server shows nothing outstanding beyond what serve holds; mail a previous session left unacked is redelivered only after the 30 s ack wait, so the count can be unknown that long. It is never guessed from elapsed time.

A reader treats the count as fresh when `waking_pending_at` is within 90 s (the lease TTL). It is rewritten on every refresh and when the count changes (at most one extra write per 5 s, a burst of mail being one write). Readers must ignore fields they do not know. Limits are in `internal/endpoint/waking.go`.

The CLI never takes the lease, so it works alongside the running session.

`send --id KEY` (the send tool's `id`) is an idempotency key, 1 to 128 printable ASCII characters. Re-running a send with the same key to the same recipient from the same endpoint, within the inbox stream's two-minute duplicate window, stores nothing new: it succeeds, reports the original `#seq` and says it was a duplicate. If a send times out waiting for the stream's acknowledgement, the message may or may not have landed; the error prints a key to retry with (`mash send <to> --id <key> ...`), which delivers it at most once, but only within that two-minute window: after it a retry may store a second copy, which the recipient shows as a new message. Without `--id` every send is a new message.

## Quick start on Synadia Cloud

If you use [Synadia Cloud](https://cloud.synadia.com), [`scripts/quickstart-synadia.sh`](../scripts/quickstart-synadia.sh) does the server side (operations.md) for you from one API token. It is a script outside the binary (it needs curl and jq; [ADR 0038](decisions/0038-synadia-setup-is-a-script-outside-the-binary.md)). Install the binary and the plugin (sections 1 to 3) first.

1. In the Synadia Cloud console, create an API token for your team.
2. Give it to the script through the environment (or `--token-stdin`, or the prompt in a terminal; never on the command line), and look at the plan first:

   ```sh
   export SYNADIA_API_TOKEN=...
   scripts/quickstart-synadia.sh --dry-run --endpoint design --endpoint review
   scripts/quickstart-synadia.sh --endpoint design --endpoint review
   ```

   With no `--endpoint`, it makes one named `<user>-<host>`. If the token sees several teams or accounts, name one with `--team` or `--account`. `--owner` defaults to your user name.
3. It creates a temporary admin user (named `mash-quickstart-admin-` and 8 random hex characters), a NATS user and a creds file in `~/.config/mash/<name>.creds` (mode 0600) for each endpoint, and writes `~/.config/mash/config.json` (it refuses to change a different `url` or `owner` without `--force`). Then it runs `mash admin init` and `mash admin provision` for each endpoint, sends a test message from the first endpoint to the second, and deletes the admin user, also when it fails. It refuses to reuse a NATS user that already exists; delete it in the console first. The API token is not stored, and no admin credential is left behind. [docs/synadia.md](synadia.md) lists what it does and does not check.
4. Start two sessions, each as an endpoint (section 5):

   ```sh
   MASH_NAME=design claude --channels plugin:mash@mash
   MASH_NAME=review claude --channels plugin:mash@mash
   ```

Notes:

- The free tier allows 5 streams. mash takes 2 for its shared buckets and 1 per endpoint, so it fits **3 endpoints**; the script checks this first and, if the plan does not fit, creates nothing and says how many would.
- Synadia's control-plane API (`/api/core/beta/`) is not documented or stable. If its shape changes, the script stops with an error rather than guessing.
- The account's message limit is 512 KiB, half of mash's own 1 MiB cap.
