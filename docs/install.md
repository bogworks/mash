# Installing mash

This walks through one machine running Claude Code sessions with mash. Provisioning the server side (the account, endpoint users and their inboxes) is in [operations.md](operations.md).

## 1. The binary

mash is one static Go binary. Claude Code starts it as `mash serve`, so it must be on the `PATH` that Claude Code sees.

```sh
go install github.com/randybias/mash/cmd/mash@latest
mash version
```

## 2. The plugin

The mash repository is its own Claude Code marketplace:

```sh
claude plugin marketplace add randybias/mash
claude plugin install mash@mash
```

The plugin only declares an MCP server, `mash serve`. A session without a mash identity (no `MASH_NAME`) gets an inert server with no tools, so it is safe to install for every session.

## 3. Allow the channel (once per machine)

Claude Code runs only allowlisted channel plugins without a confirmation dialog. Allowlist mash in the managed settings file:

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

Without this, `--channels plugin:mash@mash` is refused ("not on the approved channels allowlist") and mail is silently not delivered. You can still try mash with `--dangerously-load-development-channels plugin:mash@mash`, which asks for confirmation on every launch.

## 4. Configuration

`~/.config/mash/config.json`:

```json
{ "url": "tls://nats.example.com:4222", "owner": "alice" }
```

| Key | Env | Meaning |
|---|---|---|
| `url` | `MASH_URL` | The NATS server. Use TLS: SAP identity is only meaningful over a verified connection. |
| `owner` | `MASH_OWNER` | Your SAP owner token (lower-case letters, digits, single hyphens). |
| `creds` | `MASH_CREDS` | The endpoint's credentials file. Default `~/.config/mash/<name>.creds`. |
| | `MASH_NAME` | The endpoint name, set per session at launch. |
| `settle_ms` | `MASH_SETTLE_MS` | Quiet time before a batch is pushed. Default 100. |
| `batch_msgs`, `batch_bytes` | `MASH_BATCH_MSGS`, `MASH_BATCH_BYTES` | A batch up to 10 messages and 16 KiB is shown in full; more becomes a digest. |

The environment wins over the file. Credentials files must be mode 0600; they hold the endpoint's private key.

Optional: `~/.config/mash/policy.md` is appended to every session's instructions. By default, mail from a verified mash endpoint is treated as a delegated request from the fleet's operator; use the policy file to carve out what still needs a human, for example "Ask me before spending money or touching production."

## 5. Launch a session

```sh
MASH_NAME=design claude --channels plugin:mash@mash
```

The session registers as a SAP agent, takes the lease on its name, and starts receiving mail. If another live session already holds the name, this one starts without mail tools and says why.

In the session, Claude has five tools: `send`, `read`, `inbox`, `who` and `reply`. From a shell, the same identity can use the CLI:

```sh
MASH_NAME=design mash who
MASH_NAME=design mash send review "Spec is at docs/specs/x.md; please review section 4."
MASH_NAME=design mash prompt review "Are you still on the auth bug?"
```

The CLI never takes the lease, so it works alongside the running session.
