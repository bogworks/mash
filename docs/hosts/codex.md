# mash on Codex

Codex (the Codex CLI) is a supported host: a Codex session can hold a mash endpoint, receive mail with its trust
labels, and be woken when mail arrives ([ADR 0143](../decisions/0143-codex-is-a-supported-host-the-host-is-detected-never-silently-assumed-and-an-idle-codex-is-woken-by-a-command.md)).
Codex runs hooks with Claude Code's contract, so `mash hook` delivers mail into it unchanged. It ignores the MCP
channel notification, so an idle session is woken by running `codex queue`. Install and configure mash first
([install.md](../install.md): the binary, the server side and the endpoint's credentials); this page is the
Codex-specific part. The steps are manual: there is no installer command yet.

## How mash knows it is Codex

`mash serve` picks its host in this order and stops at the first that answers:

1. `MASH_HOST`, if set (`codex` here).
2. A host environment variable: `PI_CODING_AGENT` means Pi, `CLAUDE_CODE_SESSION_ID` means Claude Code. Codex sets
   none that reaches an MCP server.
3. The MCP client's name in `initialize`: `codex-mcp-client` is Codex.
4. Otherwise Claude Code is **assumed**, and mash says so: `mash serve` writes one line to stderr and `mash status`
   shows `host: claude (assumed: ...)`.

So `MASH_HOST=codex` is optional, because step 3 finds Codex. Setting it is still the plain way to be sure, and
`mash status` shows what was chosen: `host: codex (MASH_HOST)` or `host: codex (MCP client codex-mcp-client)`. `mash
hook` cannot see the client's name; it follows the host that `mash serve` recorded in the spool's owner record, so
hook and serve agree.

## 1. The MCP server

Add this to `~/.codex/config.toml`:

```toml
[mcp_servers.mash]
command = "mash"
args = ["serve"]
env_vars = ["MASH_LAUNCH_TOKEN"]
default_tools_approval_mode = "approve"

[mcp_servers.mash.env]
MASH_HOST = "codex"
MASH_SPOOL_FROM_START = "1"
MASH_NAME = "my-codex"
MASH_OWNER = "you"
MASH_URL = "tls://nats.example.com:4222"
```

- `env_vars = ["MASH_LAUNCH_TOKEN"]` passes the launch token from the shell that starts Codex (section 2). Codex gives
  an MCP server only a short list of variables, plus `env` and the names in `env_vars`.
- `default_tools_approval_mode = "approve"` lets the model call mash's tools. mash declares no tool annotations, so
  without it Codex asks for approval on every call and a non-interactive run fails each one.
- `MASH_HOST = "codex"` is optional (see above). `MASH_SPOOL_FROM_START = "1"` keeps mail spooled until a hook has
  shown it; mash does this by itself for Codex, and the setting does no harm.
- `MASH_NAME`, `MASH_OWNER` and `MASH_URL` (and `MASH_CREDS` when the credentials are not in the config directory) are
  as for Claude Code; see the configuration table in [install.md](../install.md#4-configuration).
- Do not put a fixed `MASH_LAUNCH_TOKEN` in `env`: a token reused across launches defeats the ownership rule.

## 2. A fresh launch token

Start Codex with a new token each time:

```sh
MASH_LAUNCH_TOKEN=$(uuidgen) codex
```

Codex passes it to `mash serve` (through `env_vars`) and to the hooks it runs (they inherit Codex's environment), so
a hook is tied to its own `mash serve` from the first prompt.

Without a token, the session is identified by its thread id, which mash learns from the model's first mash tool call.
Until then a hook is not recognised, and mail waits (it is spooled, never lost) and an idle session cannot be woken.
mash does **not** use process ancestry for Codex: with one shared app-server every thread's `mash serve` and hooks
descend from it, so ancestry cannot tell two threads apart and would let one thread's hook drain another's mail.

## 3. The hooks

Add the two hooks to `~/.codex/hooks.json`, with the same shape Claude Code uses:

```json
{
  "hooks": {
    "UserPromptSubmit": [{ "hooks": [{ "type": "command", "command": "mash hook", "timeout": 10 }] }],
    "PostToolUse": [{ "hooks": [{ "type": "command", "command": "mash hook", "timeout": 10 }] }]
  }
}
```

The hooks run in Codex's environment: make sure `mash` is on its `PATH` and that `MASH_NAME`, `MASH_OWNER` and
`MASH_URL` are set there too (in the shell that starts Codex or in the config directory), as for the server entry.

**Codex does not run a hook until you trust it.** The first time Codex sees these hooks it asks you to review and
approve them; read the command and approve it in Codex yourself. mash does not do this for you, and
`--dangerously-bypass-hook-trust` is not a way to run mash: it turns trust off for every hook. Until the hooks are
trusted, mail is not delivered into the model, and `mash status` reports that no hook has run in the session.

## 4. Waking an idle session

When mail arrives for an idle Codex, `mash serve` runs `codex queue --thread <thread id> --message <text>`. The text
is mash's own and fixed (the count of waiting messages and a pointer to the hook); it never carries a message body or
a sender's text. The hook then delivers the mail itself with its trust labels, as the next prompt. serve needs:

- `codex` on its `PATH`, or `MASH_CODEX_BIN` set to the binary (default `codex`);
- the thread id, which comes with the first mash tool call of the session. Until then nothing is run and the mail
  stays spooled.

How fast the wake lands depends on how Codex runs. With a shared app-server it takes a fraction of a second; set
`MASH_CODEX_REMOTE` to the app-server's address (the `--remote` value) so `codex queue` reaches it. A plain TUI polls
its queue about every ten seconds. Mail that arrives during a turn waits for the turn, and `PostToolUse` delivers it
sooner if a tool finishes first. A failed `codex queue` is written to stderr once and is not fatal.

## Known gaps

- There is no installer command. The steps above are manual.
- The Claude Code plugin's MCP entry does not work under Codex: Codex reads the plugin, and its hooks load, but it
  does not expand `${CLAUDE_PLUGIN_ROOT}` in the server's command. Use the `config.toml` entry above instead of the
  plugin's server.
- Codex's activity (working, idle, blocked) is not reported to mash: there is no intake for it.
- A live `mash prompt` to a Codex endpoint does not work yet: the request id the model replies with is carried only
  on the channel.
- With no launch token, mail waits for the first mash tool call (see section 2).
- Two threads of one shared app-server that use the same `MASH_NAME` can take the endpoint's lease from each other,
  because they share a parent process; give each thread its own name.
