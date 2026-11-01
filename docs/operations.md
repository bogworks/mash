# Operating mash

mash needs a NATS server in operator mode with JetStream, and one account for mash. mash itself never mints NATS users and never holds an operator or account seed: you issue endpoint users with your own tooling (`nsc`, or any JWT issuer), using the permissions `mash admin permissions` prints, and mash creates the streams and directory entries behind them.

## The account

Create an account with JetStream limits sized for your endpoints: one stream per endpoint plus two KV buckets, and one consumer per endpoint. The account name is yours to choose; `MASH` is only the default in these examples (set `ACCOUNT` to use another). The limits default to 2G of disk, 64 streams and 128 consumers; see [Limits](#limits) for the sizing rule.

```sh
ACCOUNT=${ACCOUNT:-MASH}
JS_DISK=${JS_DISK:-2G} JS_STREAMS=${JS_STREAMS:-64} JS_CONSUMERS=${JS_CONSUMERS:-128}
nsc add account "$ACCOUNT"
nsc edit account "$ACCOUNT" --sk generate --js-disk-storage "$JS_DISK" --js-streams "$JS_STREAMS" --js-consumer "$JS_CONSUMERS"
nsc push --account "$ACCOUNT"
```

mash stores everything on file storage; it needs no memory store. A server with no memory store configured refuses an account that asks for one, so leave `--js-mem-storage` unset.

Add an admin user for mash's admin commands (full access to the account), and keep its credentials only where you run `mash admin`:

```sh
nsc add user --account "$ACCOUNT" mashadmin
nsc generate creds --account "$ACCOUNT" --name mashadmin > ~/.config/mash/admin.creds
chmod 600 ~/.config/mash/admin.creds
```

Then create the directory and lease buckets once:

```sh
export MASH_URL=tls://nats.example.com:4222 MASH_OWNER=alice MASH_ADMIN_CREDS=~/.config/mash/admin.creds
mash admin init
```

Every bucket carries a size cap, because hosted NATS accounts (Synadia Cloud's free tier, for one) refuse a stream with no max bytes. By default the directory bucket gets 16 MiB and the lease bucket 4 MiB; both hold tiny records, so that is generous (see [Limits](#limits) to change them). Running `mash admin init` again is safe: a bucket made by an older mash with no cap is bounded in place with a stream update, keeping every key; a bucket whose cap is smaller than the configured one is raised to it; a bucket with a larger cap keeps it. Init never lowers a cap and never purges anything.

## Adding an endpoint

An endpoint is one named Claude Code session. Names are lower-case letters, digits and single hyphens, up to 48 characters, unique per NATS account (see [ADR 0006](decisions/0006-one-shared-nats-account-one-owner-per-deployment.md)).

```sh
(
set -eu
name=design
perms=$(mash admin permissions "$name" --nsc)
[ -n "$perms" ] || { echo "no permissions printed; refusing to add the user" >&2; exit 1; }
set --
while IFS= read -r arg; do set -- "$@" "$arg"; done <<EOF
$perms
EOF
nsc add user --account MASH "$name" "$@"
user=$(nsc describe user --account MASH --name "$name" --json | jq -r .sub)
account=$(nsc describe account MASH --json | jq -r .sub)
mash admin provision "$name" --user "$user" --account "$account" --note "design session on laptop"
nsc generate creds --account "$ACCOUNT" --name "$name" > ~/.config/mash/$name.creds
chmod 600 ~/.config/mash/$name.creds
)
```

This runs in a subshell under `set -eu`, so any failing step stops it, and an empty permission list never reaches `nsc add user` (a user created with no `--allow-*` flags would carry none of the restrictions above). It is plain POSIX `sh`, so it also works pasted into zsh or macOS bash 3.2.

Deliver the credentials file to the machine that runs the session, and nowhere else.

`mash admin permissions <name>` prints the permission set as JSON; `--nsc` prints the same set as `nsc add user` flags, one argument per line. The set lets an endpoint:

- send mail to any endpoint of the owner, and send live SAP prompts and status probes to them;
- publish its own heartbeat and answer SAP discovery;
- read and acknowledge its own inbox, and read its own history;
- read (never write) the directory;
- hold its own lease and nothing else.

It cannot read another endpoint's inbox, history or replies (each endpoint receives only under its own `_INBOX.<name>` prefix), take another endpoint's name, write the directory, create or delete consumers, or delete or purge any stream. It holds no response permission, because NATS grants that for any reply subject an endpoint receives; replies may only go to `_INBOX.>` subjects. The integration tests assert each of these denials against a real server.

### Directory metadata

An endpoint's directory record can carry optional fields: `--role` (what it is for), `--host` (the machine it is meant to run on) and up to 8 free-form labels, `--label key=value` (repeatable), for grouping or tagging endpoints, for example `team=acme` or `project=billing`. They are the endpoint's identity on record, set by the admin; they are not live state. A role, host or label value is up to 64 printable characters with no tab or newline. A label key is a lowercase letter followed by up to 31 of `a-z`, `0-9`, `_`, `.` and `-`.

```sh
mash admin provision "$name" --user "$user" --account "$account" --role reviewer --host laptop --label team=acme --label tier=1
mash admin set-meta "$name" --role builder --host - --label tier=- --label env=prod   # change role, clear host, clear one label, set another; everything else stays
```

`set-meta` needs at least one flag, changes only the fields given, and writes with a compare-and-set, re-reading and retrying if another write lands first. A value of `-` clears a field, and `--label k=-` clears that one label while the other labels stay. The 8-label cap is checked after the merge. Fields a newer mash wrote to the record are kept. Older mash versions ignore the labels, so mixed versions are safe. `mash admin list` is unchanged.

`mash who` prints the same lines as before, so tools that parse it keep working. `mash who --long` (and the `who` tool a session's model sees) appends `role R` and `host H` for the fields that are set, then one `label.K=V` column per label sorted by key, each as its own tab-separated column. For the endpoint you run it as, it also appends the live lease's `lease host` and `lease version`, and the note `lease host X differs from directory host Y` when both hosts are set and differ. Other endpoints' leases are not readable with an endpoint's permissions, so their live host is not shown. The lease is the live view; the directory metadata is only the record of intent.

## Removing an endpoint

```sh
mash admin deprovision design
nsc revocations add-user --account "$ACCOUNT" --name design
nsc push --account "$ACCOUNT"
```

Deprovisioning fences the name, then removes the directory record and the inbox stream. The fence is a tombstone in the endpoint's lease key: a session still running under the name stops at its next lease refresh (within 30 seconds), and one starting up is refused, instead of either taking the name back. The tombstone expires with the lease TTL (90 seconds), and provisioning the name again clears it at once. Revoking the user stops its credentials from connecting at all. Until you revoke, mail it sends still arrives, but labelled `[unknown-signer]`, because its key is no longer in the directory.

## Topologies

mash supports one NATS server, or a cluster, in the default JetStream domain, with the default API prefix. Inboxes are single-replica ([Durability](#durability-and-sync_interval)), so on a cluster an inbox is unavailable while its node is down.

JetStream domains, leaf-node inboxes and subject mappings are not supported. The endpoint permissions that `mash admin permissions` generates (`$JS.API.*`, `$JS.ACK.<stream>.<consumer>.>`) and the client (`jetstream.New` with no domain or API prefix) assume the default domain. On a leaf node with its own domain, or behind subject mappings, the API and ACK subjects differ: acks can be denied (endless redelivery), the inbox may be unreachable, and signatures over mapped subjects may not verify. A `js_domain` setting is not implemented.

## Inbox limits

By default each inbox keeps mail for 30 days or 16 MiB, whichever comes first, dropping the oldest (both are configurable: see [Limits](#limits)). Single messages are limited to 1 MiB including headers, or to the server's `max_payload` when that is smaller: mash reads it from the server at connect time and refuses an oversize send locally with a clear error before anything is published. Synadia Cloud's free tier caps a payload at 512 KiB, so there the limit is 512 KiB. Message deletion and purging are denied on the stream itself, so even the account admin cannot quietly rewrite history; deleting the whole stream (deprovisioning) is the only way to remove it.

## Limits

The caps on inboxes and buckets are configurable. The defaults are what mash has always used:

| Key (`limits` in config.json) | Default | Applies to |
|---|---|---|
| `inbox_max_bytes` | `16MiB` | each inbox stream; the oldest mail is dropped past it (minimum 2 MiB) |
| `inbox_max_age` | `30d` | each inbox stream; older mail is dropped (minimum `1h`) |
| `directory_max_bytes` | `16MiB` | the directory bucket (minimum 1 MiB) |
| `lease_max_bytes` | `4MiB` | the lease bucket (minimum 1 MiB) |
| `inbox_max_consumers` | `-1` | consumers per inbox stream (-1 is unlimited, else at least 1) |

An optional `limits` object in `~/.config/mash/config.json` changes them; leave out a key to keep its default. Sizes are integers (bytes) or strings with a `B`, `KiB`, `MiB` or `GiB` suffix; ages are Go durations (`720h`) or whole days (`30d`). An unknown key or an invalid value is an error naming it. There are no environment overrides.

```json
{
  "url": "tls://nats.example.com:4222",
  "owner": "alice",
  "limits": {
    "inbox_max_bytes": "16MiB",
    "inbox_max_age": "30d",
    "directory_max_bytes": "16MiB",
    "lease_max_bytes": "4MiB",
    "inbox_max_consumers": -1
  }
}
```

`mash admin limits --show` prints the effective limits, one `key value` per line.

### Changing limits later

Limits only go up. Edit config.json, then apply it:

```sh
mash admin init              # raises the two buckets (a larger existing cap is kept)
mash admin limits            # raises every inbox's max_bytes and max_age, and sets max_consumers
mash admin limits design     # or only the named inboxes
```

New endpoints (`mash admin provision`) get the configured limits at once. `mash admin limits` updates each existing stream in place, keeping its mail, and prints a line per endpoint saying what changed. It refuses to lower `max_bytes` or `max_age` on an endpoint, with an error naming the endpoint and field, and changes nothing for it, because a lower cap could discard stored mail. To lower an inbox's limits, run `mash admin deprovision <name>` (which removes the mail in it) and provision it again. `mash admin init` likewise never lowers a bucket. The check that runs on an existing inbox still reports any stream that differs from the configured limits. Run `mash admin limits` from one admin at a time: JetStream has no compare-and-set on a stream's configuration, so two admins applying different limits to the same inbox at the same moment can leave the lower of the two caps (and a lower cap can discard mail).

### Account limits

The NATS account has limits of its own: streams, consumers and disk. Size them from the endpoint count: one stream per endpoint plus two for the buckets, and one consumer per endpoint. The disk must hold the sum of the stream caps (`inbox_max_bytes` per endpoint plus the two bucket caps), so raising the inbox cap raises the disk you need.

```sh
ENDPOINTS=20
JS_STREAMS=$((ENDPOINTS + 2))
JS_CONSUMERS=$ENDPOINTS
JS_DISK_MIB=$((ENDPOINTS * 16 + 16 + 4))   # inbox_max_bytes in MiB per endpoint, plus the two buckets
```

The examples above default to `JS_DISK=2G`, `JS_STREAMS=64` and `JS_CONSUMERS=128`, which leave room to grow; change the values and re-run `nsc edit account "$ACCOUNT" ... --js-disk-storage "$JS_DISK" --js-streams "$JS_STREAMS" --js-consumer "$JS_CONSUMERS"` and `nsc push`. Leave `--js-mem-storage` unset.

## Durability and sync_interval

Every mash inbox is a JetStream stream with one replica (R1). nats-server's default `sync_interval` is 2 minutes: the server acknowledges a write before it has fsynced it. So a host crash or power cut can lose up to about 2 minutes of mail that mash already reported as `sent #N`. Restarting or killing mash, or the nats-server process, is fine: the data is in the operating system's page cache and survives. See [Jepsen's analysis of NATS 2.12.1](https://jepsen.io/analyses/nats-2.12.1) and [Synadia's response](https://synadia.com/blog/jepsen-nats-2-12-1), which recommends `sync_interval: always` for a single server.

- **Self-hosted server:** set it in `nats-server.conf`, then restart the server:

  ```
  jetstream { sync_interval: always }
  ```

  I could not confirm the block from the NATS configuration pages, so check the documentation for your server version. Replicated streams (R3) can survive the loss of one server when the replicas sit on independent hosts, but replication alone does not guarantee that acknowledged writes survive a crash (see the NATS JetStream documentation); mash does not provision replicated streams today and Synadia's free tier does not offer them.
- **Cost:** `always` fsyncs on every write, so write latency and throughput suffer. A mail mesh sends little, so this is usually acceptable.
- **Synadia Cloud:** its policy for this is not documented and unknown to us (we have asked). Treat a host crash there as able to lose recent mail.
- **Visibility:** mash cannot read the server's setting today, so `mash status` does not warn about it.

## Hook delivery

The plugin ships two Claude Code hooks, PostToolUse and UserPromptSubmit, which run `mash hook` (`plugin/hooks/hooks.json`).

The hook command always exits 0 (a mash too old to have `hook` would otherwise exit 2, which Claude Code treats as blocking) and appends the hook's stderr to `hook.log` in the state directory (`$MASH_STATE_DIR`, else `$XDG_STATE_HOME/mash`, else `~/.local/state/mash`), beside the spool. That is where a failing or too-old hook is found; a diagnostic tool can surface it.

**Subagents have no mailbox.** Claude Code runs hooks for a subagent's tool calls too, and mash ignores them (a hook input carrying `agent_id`): they show no mail and drain nothing. Mail waits for the main session's next tool call or prompt, or for the idle wake. A session busy in a long subagent run therefore sees no mail until then. Such a hook only counts as proof that hooks run (see "Hooks that never run").

**Which hooks may drain the spool.** `mash serve` records its owner in `<spool>/.owner` as JSON: its parent pid, its `CLAUDE_CODE_SESSION_ID` and its `MASH_LAUNCH_TOKEN`. A serve that waits for the lease records itself in `<spool>/.waiting.<pid>` (its parent Claude pid and session id) and removes the record when it binds or exits. A hook that descends from such a waiting serve and not from the owner is refused as another launch, with no launch token needed (broken process layouts fall back to the rules below). Otherwise a hook drains the spool if any one of these holds: its session id (the `session_id` on its stdin, else `CLAUDE_CODE_SESSION_ID` from its environment) equals serve's (unless both sides have a non-empty `MASH_LAUNCH_TOKEN` and they differ: that is another launch of the same session, such as a `claude --resume` of a running one, [ADR-0043](decisions/0043-second-launch-of-a-running-session-is-refused-and-told-once.md)); both sides have the same non-empty `MASH_LAUNCH_TOKEN` (this survives `/clear`); or serve's parent pid is among the hook's ancestors. A hook that matches none is refused: it prints nothing, leaves the mail spooled, writes one line to `hook.log` (`mash hook: not this session's spool (...); mail left for its owner`) and records `<spool>/.refused` (at most once a minute). For the other-launch case the hook also prints a one-time notice into that session's context. This keeps a second session with the same `MASH_NAME` from draining this session's mail. `<spool>/.session` keeps the legacy text `pid:N`, so a hook from an older mash still enforces the process-ancestry rule (and nothing else) when serve is newer; a damaged `.owner` makes every hook refuse. Keep serve and hook on the same mash version for the session-id and launch-token rules. `MASH_HOOK_TRACE=1` makes every hook run log its pid chain, both session ids and the rule that matched.

**`delivery: REFUSED` in `status`** means the plugin's hook runs in this session but was not recognised as this session's, so mail stays spooled, and serve wakes the session a few times (then stops: see below). It happens where hooks run outside the process tree of serve's parent (serve started through a wrapper that does not `exec`, a PID namespace or sandbox, some IDE or desktop hosts, a chain deeper than 10) and the session id does not match either. The fix: start Claude Code with `MASH_LAUNCH_TOKEN` set to a fresh value for each launch (for example `MASH_LAUNCH_TOKEN=$(uuidgen) claude ...`); serve and its hooks inherit it. A value reused across launches, or one shared by two sessions with the same name, defeats the launch-token rule: a waiting session would then drain the holder's mail (serve warns on stderr when it sees this). If it still refuses, report your setup with the `hook.log` line and `MASH_HOOK_TRACE=1` output.

**Hooks that never run** (ADR-0041). Mail waits in the spool for a hook, and only a hook drains it. If no mash hook runs in the session, waking it again would cost a turn each time and show nothing, so mash watches for that. Both states hold only while no main-session hook, and no hook of the session's own subagents, has run since `serve` bound the spool; a hook run ends them.

* **Suspected:** three wakes (the first, at once, counts) and still no hook. Re-waking stops. A new waking message still rings once. Nothing is acked, and a message the spool will not take is held unacked instead of written to the channel: a channel write is not delivery when wakes are not landing (issue #40). `status` shows `delivery: SUSPECTED - the last <n> wakes started no turn (no hook since <time>); channel writes are not counted as delivery; re-waking stopped`. A session that makes no tool calls stays here: no turn is wasted, and the mail is read if the model uses the `read` tool.
* **Proven:** two mash tool calls at least 12 seconds apart (above the hook's 10 second timeout) with no hook between them. A PostToolUse hook runs after every successful tool call, so the first call's hook would have run by the second. Then one wake, and each later wake, ends "mash's hooks are not running in this session; read each message with the read tool (#seq)." The `read` tool marks the message it returns as seen once its result has been written to the client, so it is acked; the `inbox` tool never acks. The one notice in the proven state is sent for held mail of any kind, result and fyi included. `status` shows `delivery: DEGRADED - no mash hook has run since <time> although this session has made tool calls. Likely cause: ... Mail now: read each message with the read tool. Next: ...`. A hook refused as another session's (above) counts as no hook, and its refusal is named as the cause.

mash does not read Claude Code's settings files, so `status` lists the causes that leave no trace: `disableAllHooks`, `allowManagedHooksOnly`, hooks not loaded, `mash` not on the hook's PATH. The fixes: remove `disableAllHooks: true` from the user, project, local or managed settings; under `allowManagedHooksOnly` an administrator force-enables `mash@mash` in managed `enabledPlugins` (hooks of plugins force-enabled there are exempt); check that the plugin is enabled in this project; read `hook.log` for `command not found` or a refusal. Mash tool calls a subagent makes count as the session's tool calls, and a session whose only running hooks are its subagents' is never proven.

**Under the plugin** (`MASH_SPOOL_FROM_START=1`), mail is spooled from the first message and shown by the hooks; the channel event is only a one-line summary that wakes an idle session. Without that setting, `mash serve` pushes mail to the channel in full until the first hook has run in the session.

Spooled delivery works like this (without `MASH_SPOOL_FROM_START`, it starts at the first hook run):
- It writes each message to `spool/<owner>/<name>/<seq>.json` in the state directory (`$MASH_STATE_DIR`, else `$XDG_STATE_HOME/mash`, else `~/.local/state/mash`) (directories 0700, files 0600).
- It holds the message unacked and sends the channel nothing: a hook runs after every tool call and with every prompt, so a busy session sees the mail without a turn of its own.
- If spooled mail that wakes waits 45 seconds by default with no hook draining it (`MASH_IDLE_WAKE` or `idle_wake` in config.json, 5s to 30m) (an idle session), serve sends one channel event, the summary line alone (`1 message from lead (question)`). That starts a turn, and its UserPromptSubmit hook shows the mail. If that wake starts no turn, serve wakes it again while the mail is still held: twice the idle wake later (90 seconds by default), then doubling each time, to every 30 minutes. A hook drain resets the backoff. If no hook has run in the session at all, the wakes stop after the third (see "Hooks that never run"). Under `MASH_SPOOL_FROM_START` (the Claude plugin sets it), mail that wakes arriving before the first hook has run in the session wakes it at once, with no idle wait, each message once, and that ring does not start the backoff, so the idle wake still follows if it starts no turn; result and fyi still wait, and once a hook has run the idle wake applies as above.
- Which mail wakes is its kind (`mash send --kind K`, or the send tool's `kind`; the `Mash-Kind` header): `blocker`, `question` and `task` wake an idle session; `result` and `fyi` (opt-in; a send with no kind is `task`) never do (mail with no `Mash-Kind` header, from older mash, or with an unknown kind is `task`, and wakes), so they wait for the session's next hook run and cost no turn, except that a backlog of half the consumer's limit (500 messages) or a quarter of the inbox's 16 MiB byte limit wakes the session at once to drain, so waking mail behind it can still arrive. While only result or fyi mail is held there is no idle wake and no re-wake; once a waking message is held, the wake's summary covers all held mail and the drain shows it all. The kind is the sender's claim and is not signed. A reply (`in_reply_to`) sent as `question` with no `?` and at most 280 characters goes as `fyi`, and the send tool says so; a `task` or `blocker` is never downgraded. Every message shows its `kind:`.
- Urgent mail (`mash send --urgent`, or the send tool's `urgent`) wakes the session at once, of any kind, with a summary led by `urgent,`. Each urgent message wakes it once. The mark is the sender's signature over the message (the `Mash-Urgent` header) and counts only for a [verified] sender; anyone else's urgent mail is normal mail.
- The next hook run shows the session the spooled mail as hook context, framed with the trust notes, at most `MASH_BATCH_MSGS` messages per run.
- A large batch is shown as a digest: one line per message with a preview of its first line, when the bodies of the messages in a run (at most `MASH_BATCH_MSGS`, 10 by default) total more than `MASH_BATCH_BYTES` (16384 by default), or when the full text would pass the 9,500-unit cap mash keeps under Claude Code's 10,000-character hook limit. Messages that do not fit even as a digest wait for the next run and are not marked. **A digest line counts as delivery.** The hook marks every message it listed as drained, and serve acks it, even if the model never calls `read` for its body. Senders should put the action in the first line.
- The hook marks each message `drained/<seq>`. serve then acks it (confirmed by the server) and removes the tombstone. Until the ack is confirmed the tombstone stays, so a redelivery is acked rather than shown again.
- Delivery is **at-least-once**. The hook records `shown/<seq>`, prints the batch, then marks it drained. A hook killed between the print and the mark (a timeout, a crash) shows the batch again on the next run, each message flagged `re-delivered after an interrupted hook` with its #seq, so the model does not act on it twice. No message is lost.
- If the spool cannot be written (disk full, permissions), a message is retried 3 times and then, once a hook has run in the session, delivered by the channel instead (before that, held unacked in the inbox, readable with the inbox and read tools); the failure is logged once. A held message whose spool file goes missing or is damaged is rewritten from memory.

What this means for operators:
- **Unread means not yet shown by a hook.** The inbox consumer's unread count (pending plus ack-pending) counts a spooled message until a hook has printed it and serve has acked it. It is not proof the model read it: a digest prints one line per message, and a hook's output may still be on its way into the session.
- **The spool is bounded.** The consumer's MaxAckPending (1000) caps the count, and `SpoolMaxBytes` (32 MiB) caps the bodies held. Past either cap, mail stays in NATS, still unread. The byte cap is tested; the count cap rests on NATS's MaxAckPending and is not exercised by a test.
- **A restart re-spools.** Unacked messages are redelivered and spooled again, except one a hook already showed: its tombstone gets it acked instead. A Claude restart starts a new serve, which takes over the spool and delivers what the old session had not shown.
- **A hook without serve does nothing.** It never creates a spool, so it is silent until serve has made one.
- **Test failpoints.** `MASH_HOOK_FAILPOINT` (`before-print`, `after-print`, `after-first-drained`) makes `mash hook` print `mash hook: failpoint <name>` on stderr and block, and `MASH_SERVE_FAILPOINT=before-ack` makes `mash serve` print `mash serve: failpoint before-ack #<seq>` and block just before it acks a message a hook has drained (it never acks it), so a test can SIGKILL the process there. Both are for the crash tests only; never set them outside those.

## Headless and SDK sessions

mash's live receipts use persistent `claude -p` stream-json sessions. Other headless shapes behave differently.

- **Persistent stream-json works.** A `claude -p` process that stays open between turns, with the plugin's hooks, delivers mail as in an interactive session. The receipt is [2026-10-29-default-kind-live-v0.2.6](evidence/2026-10-29-default-kind-live-v0.2.6/RECEIPT.md) (Claude Code 2.1.312, hooks injected with `--settings`).
- **A one-shot run holds the name only for its turn.** `mash serve` exits when its MCP input stream ends (`internal/mcp/server.go`, `Run`), which ends the session's lease. Mail that arrives while the run is in its turn is shown by the hooks at its next tool call. Mail sent between runs waits in the inbox, and the next run's hooks show it once its serve has received it. This one-shot behaviour follows from the code and is not covered by a live receipt.
- **The development flag does nothing here, so the allowlist is required.** With `-p` or the Agent SDK, Claude Code ignores `--dangerously-load-development-channels` and the channel does not register ([Claude Code channels reference](https://code.claude.com/docs/en/channels-reference#test-during-the-research-preview)). Allowlist mash in managed settings as in [install.md](install.md#3-allow-the-channel-once-per-machine) for any channel wake; hooks deliver mail without a channel, but nothing wakes an idle session. A headless run that is already in a turn needs no wake.
- **An SDK host must load the plugin itself.** `claude -p` loads plugins you already installed, but an Agent SDK host takes plugins through the SDK's plugin option ([Load plugins in the Agent SDK](https://code.claude.com/docs/en/agent-sdk/plugins)). Mail needs the plugin's MCP server (`mash serve`) and both hooks, PostToolUse and UserPromptSubmit (`plugin/hooks/hooks.json`); a host that loads MCP servers or hooks its own way and omits either gets no delivery, and `status` reports `delivery:` accordingly. Also set `MASH_NAME`. mash has not been run under an SDK host.

## Fixed protocol constants

These timings are not configurable: peers and the server must agree on them, so one operator's value would break another's session or weaken a check.

| Name | Value | Where | Why fixed |
|---|---|---|---|
| Heartbeat interval | 30 s | `endpoint.HeartbeatInterval` (`internal/endpoint/service.go`) | Endpoints publish presence at this rate; the online window below assumes it. |
| Online window | 90 s (3 heartbeats) | `endpoint.OnlineWindow` (`directory.go`) | How recently a heartbeat must be seen for `who` to show an endpoint online; every reader must apply the same rule. |
| Lease TTL | 90 s | `store.LeaseTTL` (`internal/store/store.go`) | The lease bucket's expiry: how long a crashed holder blocks its name. It is set on the bucket, so every session shares it. |
| Lease refresh | 30 s | `endpoint.LeaseRefresh` (`endpoint.go`) | A third of the TTL, so two missed refreshes do not lose the lease. |
| Live request signature skew | 30 s | `sap.LiveSkew` (`internal/sap/sender.go`) | How far a signed live request's time may differ from the receiver's clock; `mash status` warns at 10 s. |
| Signed message age | 5 min | `endpoint.MaxSignedAge` (`mail.go`) | A message signed longer than this before it was stored is labelled `[stale]`: the replay defence. |
| MCP ping | every 30 s, 10 s to answer | `mcpPingEvery`, `mcpPingWait` (`cmd/mash/serve.go`) | Serve's liveness check of its MCP client, recorded in the lease. |
| Channel grace and fallback | 500 ms, 3 s | `mcp.ChannelGrace`, `mcp.ChannelFallback` (`internal/mcp/server.go`) | Claude Code drops channel events sent before it registers the channel; these bound that wait. |

## Troubleshooting

| Symptom | Likely cause |
|---|---|
| The session says it has no mash identity | `MASH_NAME` was not set when Claude Code started. |
| The session says the name is already in use | Another live session holds the lease (it expires 90 s after that session dies). The session keeps retrying every 10 s and tells Claude when mail is on; no restart is needed. |
| `--channels` says "not on the approved channels allowlist" | The managed settings allowlist is missing (see install.md, step 3). |
| Mail arrives labelled `[unknown-signer]` | The sender's key is not in the directory: it was deprovisioned, or it is not a mash endpoint. |
| `mash: endpoint X is not provisioned` | Run `mash admin provision`, or check `MASH_NAME` for a typo. |
| `x509: certificate signed by unknown authority` (or another `x509:` error) on connect | The server's certificate is not trusted. Set `tls_ca` (`MASH_TLS_CA`) to the PEM file of the CA that signed it. The files are read at connect time. |
| `remote error: tls: bad certificate` (or `certificate required`) on connect | The server requires a client certificate, or refused the one given. Set both `tls_cert` and `tls_key` (`MASH_TLS_CERT`, `MASH_TLS_KEY`) to a certificate the server's CA signed. |
| `status` says `tls: WARN` | The url is plain `nats://` to a non-loopback host. Use `tls://` with the server's certificate. |
| `do not match endpoint` | The credentials file belongs to a different endpoint than `MASH_NAME`. |
| Peers look offline, or mail arrives labelled `[stale]` | Clock skew. Keep every machine's clock in sync (NTP, including laptops, containers and VMs after suspend): more than 30 s of skew (`sap.LiveSkew`) makes live requests and heartbeats fail and stored mail `[stale]`. `status` prints a `clock:` line and warns past 10 s. |
| Clients take 30-60 s to reconnect after a network blip | The server rate-limits new connections per source address. Clients retry with jitter; raise the limit for trusted sources if this matters. |
