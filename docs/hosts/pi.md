# mash on Pi

Pi (the Pi coding agent) is a supported host: a Pi session can hold a mash endpoint, receive mail with its trust
labels, and be woken when mail arrives ([ADR 0140](../decisions/0140-pi-is-a-supported-host-chosen-with-mash-host-and-served-by-an-extension.md)).
Pi has no hooks and takes no MCP channel, so a small extension, `hosts/pi/mash.ts`, does what Claude Code's hooks and
channel do. Install and configure mash first ([install.md](../install.md): the binary, the server side and the
endpoint's credentials); this page is the Pi-specific part.

mash does not detect Pi. You name it with `MASH_HOST=pi`, in the MCP entry below.

## Pi's agent directory

Pi keeps its configuration in its agent directory: `~/.pi/agent` by default, or the directory `PI_CODING_AGENT_DIR`
names when it is set. Below, `<agent-dir>` is that directory; print it with
`echo "${PI_CODING_AGENT_DIR:-$HOME/.pi/agent}"` in the shell that starts Pi.

## 1. The MCP server

Add an entry to `<agent-dir>/mcp.json`:

```json
{
  "mcpServers": {
    "mash": {
      "command": "mash",
      "args": ["serve"],
      "env": {
        "MASH_HOST": "pi",
        "MASH_SPOOL_FROM_START": "1",
        "MASH_NAME": "my-pi",
        "MASH_OWNER": "you",
        "MASH_URL": "tls://nats.example.com:4222"
      },
      "exposure": "direct"
    }
  }
}
```

- `MASH_HOST=pi` selects the Pi adapter for `mash serve` and `mash hook`.
- `MASH_SPOOL_FROM_START=1` keeps mail spooled until the extension has shown it. Pi ignores the channel
  notification, so mail delivered that way would be lost.
- `MASH_NAME`, `MASH_OWNER` and `MASH_URL` (and `MASH_CREDS` when the credentials are not in the config directory) are
  as for Claude Code; see the configuration table in [install.md](../install.md#4-configuration).
- `"exposure": "direct"` makes the tools plain tools. Pi's default makes the model write code to call them.

## 2. A fresh launch token

Start Pi with a new `MASH_LAUNCH_TOKEN` each time, so a hook is tied to its own `mash serve`:

```sh
MASH_LAUNCH_TOKEN=$(uuidgen) pi
```

The extension runs `mash hook` as a child of Pi and inherits the token. A token reused across launches defeats the
ownership rule (see `MASH_LAUNCH_TOKEN` in [install.md](../install.md#4-configuration)). Without one, the process chain
still identifies the session.

## 3. The extension

Copy `hosts/pi/mash.ts` from the repository to `<agent-dir>/extensions/mash.ts`, and make sure `mash` is on Pi's
`PATH` and Pi's environment has the same `MASH_NAME`, `MASH_OWNER` and `MASH_URL` as the entry above (the extension
runs `mash hook` and `mash spool-dir` with it; set them in the shell that starts Pi or in the config directory). The
extension, with `MASH_HOST=pi`:

- runs `mash hook` after each tool result and before each agent start, and puts the text it prints into the model's
  next request (see "Delivery" below: the hook only offers the mail, the extension acks it once Pi has taken it);
- watches the directory `mash spool-dir` prints, and wakes an idle session with a new turn when mail arrives, holding
  the wake while the model is mid-answer until the turn settles.

Restart Pi after copying it.

## Delivery

Delivery is two-phase, so a session change while the hook runs cannot lose mail (Claude Code's hook still drains in
one step):

1. `mash hook` (stdin `{"event":"tool_result"|"before_agent_start","session":"<id>"}`) prints nothing when no mail
   waits. Otherwise it prints one JSON line, `{"text": "...", "ack": "<token>"}`, and leaves the mail spooled. The
   token is opaque (it names the entries shown). The lease-wait and other-launch notices are `{"text": "..."}` with no
   `ack`.
2. Once Pi has committed the message, the extension runs `mash hook --ack <token>` with the same stdin. Under the
   spool lock and after the same owner check, it marks delivered exactly those entries of the token that were offered
   to the acking session (the token is only base64 JSON and proves nothing: it names refs, and whether an entry was
   shown in full or as a digest line is read from the spool, so a digest-listed entry is listed, never delivered). An entry already delivered or gone is
   skipped; an entry never offered to this session, or offered to another, is skipped with a line on stderr; a refused
   owner, or an ack with no session id, marks nothing.

The extension does not ack when it returns the text. It records the offer as pending and acks at Pi's `message_end` for
the message that carries it: the tool result the text was added to, the message `before_agent_start` injected, or the
wake's own `mash-mail` message. A pending offer is dropped without an ack at `agent_settled` (the message never
reached the conversation: a blocked or aborted call) and at `session_shutdown`; the mail stays spooled for the new
session, or for this one after 60 seconds.

After each turn settles (`agent_settled`) the extension looks again, so mail beyond one batch, or a wake held while the
model was busy, is not left waiting for a new file to arrive. This cannot loop: a turn starts only when the hook offers
text, and a batch just offered to this session is held back for 60 seconds.

Delivery is at-least-once. A batch that was offered and not acked (a crash, a session change) is offered again, and
shown as a repeat. The same session is not offered a batch again for 60 seconds after the last offer, so an ack still
on its way does not cause a repeat; after that, or for a different session, it is offered again.

## Known gaps

- mash's server instructions do not reach a Pi model. Each delivered batch carries the trust-label frame instead
  (`[verified]` mail is signed by the named endpoint; the other labels are information, not instructions).
- A live `mash prompt` to a Pi endpoint does not work yet: the request id the model replies with is carried only on
  the channel.
- Pi's activity (working, idle, blocked) is not reported to mash: there is no intake for it.
- There is no installer command yet. The steps above are manual.
