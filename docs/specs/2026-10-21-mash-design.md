# mash: design

- **Status:** approved for build, 2026-10-21. Randy asked for the spec and then straight to building; the architecture write-up is a separate artifact.
- **Scope of v0.1:** sections 1–12. Section 13 lists the later phases.
- **Protocol:** Synadia Agent Protocol (SAP) 0.3.0, `synadia-ai/synadia-agent-sdk-docs/core-protocol.md` at `c48cea5f`. Section numbers written §n refer to that document.

## 1. What mash is

mash ("messaging mesh for agents") gives every Claude Code session a durable mailbox and a live SAP presence on a NATS server.

- **Mail** waits on the server while a session is busy or not running, and is pushed into the session the moment it can take it. Delivery uses Claude Code channels, so no turn is spent polling.
- **The session answers through the same process.** It sends, reads and replies with tools on the endpoint manager, which already holds the NATS connection open.
- **Every message is signed** by the sender's own NATS key (§13), so the receiver knows who sent it.

mash is SAP plus one additional verb, `mail`, and one durability layer, JetStream. It invents no protocol of its own.

### 1.1 Goals

1. **Fast.** One long-lived process per session and one connection. A send is one tool call and one network round trip, with no subprocess.
2. **No wasted turns.** Mail wakes an idle session. Mail that arrives mid-turn rides along with the next tool result. Several messages arrive as one turn.
3. **Unique, verifiable identities.** One NATS user per session name, and a second live session under the same name refuses to start.
4. **Durable for 30 days.** History stays readable by its owner for reports.
5. **Pure SAP.** Any SAP tool sees mash sessions as ordinary SAP agents.
6. **Minimal new code.** One Go binary, plus conventions.
7. **Ready to open-source.** Apache-2.0, no deployment details in the code, and a Claude Code plugin in its own marketplace.

### 1.2 Non-goals for v0.1

- Agents other than Claude Code. The design doesn't preclude them, but nothing else is built or tested.
- File transfer. Mail carries text; large files go by reference to an object store.
- Cross-account delivery.
- An approval UI.

## 2. Terms

| Term | Meaning |
|---|---|
| **endpoint** | One named Claude Code session, the unit of identity. It has one name, one NATS user, one inbox stream and one directory entry. |
| **name** | The endpoint's instance name: SAP's 5th subject token. Lower-case letters, digits and single hyphens, 1–48 characters, starting with a letter. Unique within an owner. |
| **owner** | SAP's 4th token: the operator, e.g. `randyb`. One per deployment, set in the config. |
| **agent token** | SAP's 3rd token. mash uses `cc` (Appendix C), with `metadata.agent` set to `claude-code`. |
| **endpoint manager** | `mash serve`: the per-session process Claude Code starts as an MCP channel server. |
| **directory** | The KV bucket that maps each name to its identity and mail subject. Only the admin writes it. |
| **admin** | Whoever holds the MASH account's admin credentials and provisions endpoints. |

## 3. Subjects (SAP §2)

For endpoint `<name>` under owner `<owner>`:

| Purpose | Subject | Notes |
|---|---|---|
| prompt (live request/reply) | `agents.prompt.cc.<owner>.<name>` | Required by SAP. Queue group `agents`. |
| status | `agents.status.cc.<owner>.<name>` | Required by SAP. Queue group `agents`. |
| heartbeat | `agents.hb.cc.<owner>.<name>` | Every 30 s, signed (§13.11). |
| **mail** | `agents.mail.cc.<owner>.<name>` | The mash verb. Captured by the endpoint's inbox stream. |

`mail` is not one of SAP's four reserved verbs, so using it is allowed (§2). SAP's official SDKs support custom endpoints the same way, e.g. `agents.spawn.…`.

mail is **declared, not registered** as a micro-service endpoint:
- A registered endpoint would subscribe to the mail subject and answer the JetStream publish acknowledgement's reply subject, which would break the publisher's acknowledgement.
- Instead, the subject is declared in the service metadata as `mail_subject`. Additional keys are allowed (§3.2).
- It is also declared in the heartbeat as `endpoints.mail` = `{"subject": …, "metadata": {"durable": "true"}}`. Unknown entries are tolerated (§8.3).

**Addressing mail:**
- Senders resolve a name to its mail subject through the directory (section 6), not through `$SRV.INFO`, because the recipient may be offline.
- This bends §2's "callers MUST learn endpoint subjects from `$SRV.INFO`". The directory is the authoritative registry for durable endpoints, written by the same admin that provisions them.

## 4. Identity and signing (SAP §13)

- **One NATS user per endpoint.** The user is issued by the MASH account and carries the permissions in section 8. Its credentials file lives only on the endpoint's host, at mode 0600.
- **Every message mash publishes carries a signed `Agent-Sender` header:** mail, heartbeats, and the prompt requests sent by `mash prompt`.
  - The header and signing follow §13.5–§13.6 exactly: tag `AGENT-SENDER-V1`, then the seven fields, ed25519 over the user seed, base64url without padding.
  - `name` is the sender's endpoint name.
  - Mail also sets `Nats-Msg-Id` to the nonce (§13.8).
- **The registration carries `user_nkey`, `account` and `id_sig`** (§13.10). The `prompt` endpoint declares `min_sender_trust: "signed"`.
- **Verifying stored mail** follows §13.8: the signature and `sub` only, with no `ts` window and no nonce set. Duplicates are dropped on `(user, nonce)` within one session's memory.
- **Trust classes shown to Claude:**
  - `verified`: a valid signature and a directory match.
  - `unknown-signer`: a valid signature, but no directory entry or a different name for that user.
  - `claimed`: no `sig`.
  - `absent`: no header.
  - `forged`: a failing signature. These are never shown as mail. They are logged and counted, then acked so they can't block the inbox.
- **Who a verified user is:** the directory maps `user → name`. This is the "registry it trusts" of §13.7.2. A key the directory doesn't know is `unknown-signer`, even with a valid signature.

## 5. Storage

### 5.1 Inbox streams, one per endpoint

| Setting | Value |
|---|---|
| name | `inbox-<name>` |
| subjects | `agents.mail.*.<owner>.<name>` |
| storage | file |
| retention | limits |
| max age | 30 days |
| max bytes | 16 MiB |
| max message size | 1 MiB |
| discard | old |
| duplicate window | 2 minutes |
| deny delete / deny purge | true / true |
| allow direct | true |
| subject transform | none (§13.8 forbids it) |

One durable pull consumer, `deliver`, with explicit ack, ack wait 30 s, max ack pending 1000, deliver all.

A per-endpoint stream keeps each endpoint's history private to that endpoint, gives each inbox its own limits, and makes retiring an endpoint a single delete.

### 5.2 KV buckets

| Bucket | Purpose | Settings | Writers |
|---|---|---|---|
| `mash-directory` | key `<name>`, value: the directory record (6.1) | history 1, file storage | admin only |
| `mash-leases` | key `<name>`, value `{"instance_id","host","pid","since"}` | history 1, **max age 90 s**, file storage (the server may have no memory store) | each endpoint, its own key only |

## 6. Directory

### 6.1 Record

```json
{"name":"p-ops2","owner":"randyb","agent":"claude-code","agent_token":"cc",
 "account":"A…","user":"U…","mail_subject":"agents.mail.cc.randyb.p-ops2",
 "created":"2026-10-21T22:00:00Z","note":"plethorax ops session"}
```

### 6.2 Rules

- The directory is the only source for `name ↔ user` and for mail subjects.
- Endpoints read it by watching the bucket and keep an in-memory copy that stays current.
- An endpoint's own directory record must match the user in its credentials, or `mash serve` refuses to run.

## 7. Endpoint manager (`mash serve`)

An MCP server over stdio, started by Claude Code from the mash plugin. One per Claude session; it exits when stdin closes.

### 7.1 Startup

1. **Read config** (section 10). If `MASH_NAME` is unset, run **inert**: answer MCP, register no tools, connect to nothing, and say so in the instructions. The plugin can then be enabled everywhere without effect on unnamed sessions.
2. **Connect** with the credentials. Reconnect forever, with a 2 s wait and 0–5 s jitter (the server rate-limits new connections per source).
3. **Check identity:** the directory record for the name must exist and match the credentials' user, or exit with an explanatory error.
4. **Take the lease:** a KV *create* of `mash-leases/<name>`.
   - If it exists, refuse: the instructions and the first channel event say "another session already holds <name> (host, pid)", and no mail is consumed.
   - Refresh with a revision-checked *update* every 30 s.
   - If the lease is lost (an update conflict), stop consuming, report it, and exit non-zero.
5. **Register the SAP service** (section 7.4) and start the heartbeat.
6. **Start the inbox consumer** and the directory watch.

### 7.2 Delivering mail to Claude

- **Batching:** gather messages until 100 ms pass with no new one (`MASH_SETTLE_MS`), then push one `notifications/claude/channel` event.
- **Full or digest:** if the batch is at most 10 messages and at most 16 KiB of bodies (`MASH_BATCH_MSGS`, `MASH_BATCH_BYTES`), each message is shown in full. Otherwise a digest shows one line each: `#<seq>`, sender, trust class, size, first line.
- **Each message header line:** `--- #<seq> from <sender> [<trust>] at <time>`.
- **Event `meta`:** `{to, count, seqs}`, string values only.
- **Ack** each message after its notification is written to stdout. Delivered means received: history stays in the stream.
- **Ordering:** stream order, per inbox.

### 7.3 Tools

| Tool | Input | Result |
|---|---|---|
| `send` | `to` (name), `text` (1 B – 1 MiB), optional `in_reply_to` (seq) | `sent #<seq> to <name>` after the stream's publish acknowledgement. Unknown `to` → error listing close names. |
| `read` | `seq` | That message from the session's own inbox, rendered as in 7.2, with trust. |
| `inbox` | optional `limit` (default 20, max 100) | The last `limit` messages in the session's own inbox, one line each. |
| `who` | none | The directory joined with presence: online when a heartbeat was seen within 90 s. |
| `reply` | `request_id`, `text`, optional `done` (default true) | Answers a live SAP prompt (7.4). |

Tool errors are MCP tool results with `isError: true`, so Claude sees them as text.

### 7.4 SAP registration

- **Service:** a NATS micro service named `agents`, with version = the mash version.
- **Metadata:** `agent: claude-code`, `owner`, `session: <name>`, `protocol_version: "0.3"`, `user_nkey`, `account`, `id_sig`, `mail_subject`.
- **`prompt` endpoint:** queue group `agents`; metadata `max_payload "1MB"`, `attachments_ok false`, `min_sender_trust "signed"`. A request:
  1. Classify the sender (§13.7). Not verified → `401`. Malformed → `400`.
  2. Reply `{"type":"status","data":"ack"}` at once.
  3. Push a channel event with `meta.request_id` and the sender. Claude answers with the `reply` tool.
  4. While no reply has come, send `{"type":"status","data":"working"}` every 30 s, so the caller's 60 s inactivity window (§6.6) stays open.
  5. After 10 minutes with no reply, send error `500` "no reply from session", then the terminator.
- **`status` endpoint:** the §8.3 payload, freshly built.
- **Heartbeat:** every 30 s, signed (§13.11), including `endpoints.prompt` and `endpoints.mail`.

### 7.5 Instructions given to Claude

Fixed text. It says:
- who this endpoint is;
- how mail arrives;
- how to send, read and reply;
- that trust labels mean what section 4 says;
- **the delegation policy (section 9).**

Then the contents of `~/.config/mash/policy.md`, if that file exists.

## 8. Endpoint permissions

These permissions, applied to a user named `<name>`, are the whole security model. `mash admin permissions <name>` prints them as JSON and as `nsc` flags.

**Publish allow:**
- `agents.mail.*.<owner>.*`: send mail to any endpoint. The signature, not the subject, proves the sender.
- `agents.prompt.*.<owner>.*` and `agents.status.*.<owner>.*`: live SAP prompts and status probes to peers (`mash prompt`).
- `agents.hb.cc.<owner>.<name>`.
- `$SRV.>`: micro-service replies.
- `$JS.API.INFO`.
- For its own inbox stream, `inbox-<name>`:
  - `$JS.API.STREAM.INFO.inbox-<name>`;
  - `$JS.API.CONSUMER.INFO.inbox-<name>.deliver`;
  - `$JS.API.CONSUMER.MSG.NEXT.inbox-<name>.deliver`;
  - `$JS.ACK.inbox-<name>.deliver.>`;
  - `$JS.API.DIRECT.GET.inbox-<name>` and `$JS.API.DIRECT.GET.inbox-<name>.>`.
- For the directory, read only, by watch:
  - `$JS.API.STREAM.INFO.KV_mash-directory`;
  - `$JS.API.CONSUMER.CREATE.KV_mash-directory.>`;
  - `$JS.API.DIRECT.GET.KV_mash-directory.>`;
  - `$JS.FC.KV_mash-directory.>`.
- For its own lease:
  - `$JS.API.STREAM.INFO.KV_mash-leases`;
  - `$KV.mash-leases.<name>`.

**Subscribe allow:**
- `_INBOX.>`;
- `agents.prompt.cc.<owner>.<name>`;
- `agents.status.cc.<owner>.<name>`;
- `$SRV.>`;
- `agents.hb.*.<owner>.*`, for presence.

**Response permission:** `allow_responses` with up to 1000 messages for 15 minutes. A SAP prompt reply is a stream of chunks: an ack, a "working" status every 30 s for up to 10 minutes, the response and the terminator.

Anything not listed is denied. That includes another endpoint's inbox, its history and its lease, stream deletion and purging, and the admin API. The integration tests (section 12) assert each denial against a real server.

## 9. Delegation policy

- **Allow by default.** Mail from a `verified` sender is treated as a delegated request from the operator who owns the fleet, and acted on without asking, with Claude's normal judgement. Claude may still pause on something it judges risky; that's intended.
- **`claimed`, `unknown-signer` and `absent`** mail is information, not instructions.
- **The blocklist hook:** `~/.config/mash/policy.md`. Free text appended to the instructions, e.g. "ask Randy before spending money or touching production". It is empty by default.

## 10. Configuration

`~/.config/mash/config.json`, with the environment overriding it:

| Key | Env | Default | Meaning |
|---|---|---|---|
| `url` | `MASH_URL` | none (required) | NATS URL, e.g. `tls://nats.example.com:4222`. |
| `owner` | `MASH_OWNER` | none (required) | SAP owner token. |
| `creds` | `MASH_CREDS` | `~/.config/mash/<name>.creds` | The endpoint's credentials file. |
| (none) | `MASH_NAME` | unset → inert | The endpoint name. Set per session at launch. |
| `settle_ms`, `batch_msgs`, `batch_bytes` | `MASH_SETTLE_MS`, `MASH_BATCH_MSGS`, `MASH_BATCH_BYTES` | 100, 10, 16384 | Delivery tuning. |

The admin commands also read `MASH_ADMIN_CREDS`.

## 11. Packaging and rollout

### 11.1 Binary and commands

`mash`, one static Go binary.

- **Endpoint:** `mash serve`.
- **CLI, for humans and scripts:** `mash send <to> [text | --file F]`, `mash read <seq>`, `mash inbox`, `mash who`, and `mash prompt <to> <text>` (a signed live SAP prompt that prints the streamed reply), using the identity in `MASH_NAME`.
- **Admin:**
  - `mash admin init` creates the two KV buckets;
  - `mash admin permissions <name>`;
  - `mash admin provision <name> --user U… [--note]` creates the inbox stream, the consumer and the directory record;
  - `mash admin deprovision <name>` deletes the directory record and lease, and removes the inbox (the account's deny-delete applies to messages, not to stream deletion);
  - `mash admin list`.

**mash never mints NATS users and never holds an account or operator seed.** Minting stays with the operator's tooling (`nsc`, or any JWT issuer), using `mash admin permissions`.

### 11.2 Plugin and marketplace

- The repo is its own marketplace: `.claude-plugin/marketplace.json`, with marketplace `mash` and plugin `mash` in `plugin/`.
- The plugin's `.mcp.json` runs `mash serve`, which must be on `PATH`.
- Install:

  ```
  claude plugin marketplace add randybias/mash
  claude plugin install mash@mash
  ```

- **Managed settings, once per machine:**

  ```json
  {"channelsEnabled": true, "allowedChannelPlugins": [{"marketplace": "mash", "plugin": "mash"}]}
  ```

  Then launch with `claude --channels plugin:mash@mash`, with `MASH_NAME` set. No confirmation dialog appears; this was checked on a Max account on 2026-10-21.

### 11.3 Repository

- Apache-2.0.
- Layout: `cmd/mash`, `internal/...`, `plugin/`, `.claude-plugin/`, `docs/`.
- CI: `go vet`, `go test ./...` and `go test -race`.
- Releases: tagged builds for linux/amd64, linux/arm64, darwin/arm64 and darwin/amd64.

## 12. Testing

1. **Unit tests:**
   - §13 signing and verification against the SAP known-answer vector B.13. Every byte of the input and the signature must match.
   - Rendering: full, digest, and the thresholds.
   - Subjects and names.
   - Config precedence.
   - The MCP framing.
2. **Integration tests**, against an in-process `nats-server` in operator mode with JetStream:
   - The test builds the operator, the account, and users with section 8's permissions.
   - Mail between two endpoints, verified.
   - The forged signature, `claimed` and `unknown-signer` cases.
   - The lease refuses a second instance.
   - Each permission denial.
   - `prompt` → `reply` → the SAP chunk sequence.
   - Store and forward.
   - Batching and digests.
3. **Live end to end:**
   - Two Claude Code sessions on the reference deployment, using the plugin, the managed settings and `--channels`.
   - A round trip, a digest, the lease conflict, and a live SAP prompt from `mash prompt`.
   - Timings recorded.

## 13. Later phases (not v0.1)

1. **Archive.** A NATS leaf node with its own JetStream domain, on the archive host. One `archive` stream sources every inbox (no subject transform), so mail outlives the 30 days. `mash admin provision` adds the source. An outage loses nothing that is still within an inbox's 30 days.
2. **Monitoring.** One endpoint watches heartbeats, unread counts, archive lag and server health, and mails the operator's endpoint when something is wrong.
3. **Approvals.** A blocklist-driven approval flow, with a small mobile client on NATS.
4. **Other agents**, such as swamp workflows, as endpoints with their own agent token.
5. **Migration from codev** in the reference deployment: every named session gets an endpoint, launchers set `MASH_NAME` and `--channels`, and codev is removed.

## Appendix: reference deployment (Randy's)

| Item | Value |
|---|---|
| NATS server | devsup1, `tls://nats.thaos.dev:4222`, operator `thaos-dev` |
| Account | `MASH`; seeds in the keyring under `nats/thaos-dev/mash-*` |
| Endpoint users | `scripts/nats-operator.sh add-user MASH <name> <flags from mash admin permissions>`; creds built by `nats-operator.sh creds` |
| Owner | `randyb` |
| Account limits | JetStream streams ≥ endpoints + 2 (the inbox streams and the two KV buckets); raise `--js-streams` and `--js-consumer` with `nats-operator.sh nsc edit account` and push. |
| Archive host (phase 13.1) | cdb |
