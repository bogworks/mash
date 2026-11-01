# mash compared with other agent communication mechanisms

Revision 2. Every cell was checked on **2026-11-01** against the mechanism's own spec, docs or repo, unless a footnote
says otherwise. Versions, host features and licences change fast: re-check them before you rely on them.

## 1. Framing

**The short answer.** mash is offline session mail within one operator-controlled trust domain. A2A is task
interoperability between independently run services. If you run your own fleet of agent sessions in one NATS account,
and those sessions start and stop on their own schedules, on one machine or many, mash gives each session an inbox
that keeps signed text mail until the session has been shown it, and puts that mail into a Claude Code session without
the agent polling. If your agents are services that other teams or organisations call, and you need a shared task
model, capability discovery and standard web authentication, use A2A. The two can sit side by side: A2A at the edge,
mash inside. mash ships no bridge between them today.

mash was built for a swarm: agents on related, interconnected work, such as coding agents on one codebase. "Lightweight"
holds in one countable sense: each agent runs one Go binary that opens one outbound connection to one NATS server, with
no per-agent server, open port or certificate. It does not mean no setup (see the operator-cost table) and it is not a
speed claim (a model turn takes seconds and dominates; see section 4). The line that matters is trust, not size: two
teams in one company that must not share an account's credentials are two trust domains, and mash does not span them.

**Up front:** mash is v0.3.0 (created and released 2026-11-01), with one maintainer, one host adapter (Claude
Code), an idle wake that rests on a Claude Code research preview, and single-replica inboxes. See section 9.

## 2. Choose mash when, choose something else when

**Choose mash when all of these hold:**

- Your agents are sessions you run, in one NATS account that you operate or rent.
- A message must wait for a recipient that is busy, idle or stopped, for hours or days (up to 30 days).
- The recipient needs to check which endpoint's key signed each message.
- Sessions sit on several machines or behind NAT, and none may open a port.
- The recipient is a Claude Code session, and mail should arrive without polling and without waking it for mail that
  can wait.

**Choose something else when:**

- **All agents are your own Claude Code sessions, and a message need wait only minutes:** Claude Code's built-in
  cross-session messaging. It is on by default from v2.1.224, needs nothing installed, and reaches your other machines
  through Remote Control.
- **One lead session splits work on one machine, or the agents live inside one program:** Claude Code subagents or agent
  teams, or a framework such as LangGraph or the OpenAI Agents SDK.
- **Agents belong to different organisations, teams or operators, or are called as services:** A2A.
- **A model needs tools and data:** MCP.
- **You need broadcast, groups, files or high throughput:** AGNTCY SLIM, plain NATS JetStream or a broker.
- **Your agents run in Codex, OpenCode or another host:** Synadia's SAP host plugins (12 hosts, live prompts only) or
  MCP Agent Mail. mash has no adapter for them.
- **Coordination is mostly shared state (locks, task lists, pull requests):** an issue tracker or task list. mash keeps
  no task state; at most it rings the doorbell ("PR 12 is ready for you").
- **The server's operator must not read or forge mail:** SLIM (MLS encryption) or your own payload encryption.

## 3. Two paths: live and durable

mash puts each kind of traffic on the NATS layer that suits it. Both share the one outbound connection per process.

| | Live path | Durable path |
|---|---|---|
| NATS layer | Core NATS publish/subscribe and request/reply. Nothing is stored | JetStream streams and KV buckets, on disk |
| Used for | Signed heartbeat every 30 s (online = verified heartbeat within 90 s); `$SRV.INFO` discovery; `status` probes; live prompts, answered as a stream of chunks | Signed mail into a per-endpoint inbox stream; the directory (name to user key and mail subject); leases (one live session per name) |
| Guarantee | At most once. SAP 0.3: chunks "MAY be lost silently" | At least once while retained: 30 days or 16 MiB per inbox, single replica [1] |
| Recipient not running | The message is gone. An unanswered prompt ends in error `500` after 10 minutes | The message waits in the inbox |
| Cost | Nothing stored. One hop through the server | A disk write and an acknowledgement per send, and one stream per endpoint (a 5-stream hosted free tier fits 3 endpoints) |

**Why both.** Presence and live prompts are about now; a heartbeat from an hour ago is wrong data. Mail is about later,
so it has to be kept, acknowledged and redelivered, which core NATS does not do. SAP 0.3 defines only the live path and
lists "JetStream-backed at-least-once streaming" as out of scope. mash adds the durable path as one declared verb, `mail`.

**Where they meet.** SAP says callers "MUST learn endpoint subjects from `$SRV.INFO.agents`", which only running agents
answer. Mail must reach agents that are not running, so mash addresses it through a directory in a KV bucket instead.
That bends the SAP MUST: a SAP tool that does not know mash will not find a stopped agent's inbox.

**In use.** Send mail when the message must arrive. Use a live prompt when you need an answer now and can fall back to
mail; mash does not turn a failed prompt into mail. Measured on one setup (mash live run, 2026-10-22): a one-shot
`mash send` took 511-540 ms, mostly process start, TLS connect and directory load; a warm live prompt to a small model
took about 2.2 s, of which about 1.6 s was the model's turn.

## 4. Turn efficiency

Model turns cost money, and an idle session woken after its prompt cache has expired re-reads its whole context. mash's
Claude Code plugin is built to spend as few turns on mail as it can. Every figure below was measured in a recorded live
run with real Claude Code. Turn counts are measured by hand; no automated test counts turns.

| Mechanism | What it does | Measured |
|---|---|---|
| Hook delivery | A busy session sees new mail with its next tool result, as hook context, with no turn of its own | Mid-turn mail shown in the same turn (2026-10-28) |
| Mail kinds | `fyi` and `result` wait for the session's next turn; `task` (the default), `question` and `blocker` wake it | An `fyi` caused no turn for 95 s, twice the idle wake (2026-10-29) |
| Idle wake | Waking mail held 45 s with no tool call sends one one-line wake; retries back off from 90 s, doubling to 30 min | A `task` woke an idle session at 47 s (2026-10-29) |
| Batching | Mail is grouped after 100 ms of quiet; up to 10 messages and 16 KiB shown in full, larger batches as a digest | A burst of 12: 11 arrived as one digest turn, the slowest sender's in a second turn (2026-10-22) |
| Urgent | A signed urgent mark from a `verified` sender wakes at once | 1.2 s (2026-10-29); 2.1-2.3 s (2026-10-30) |
| Backlog wake | 500 held messages or 4 MiB wakes the session to drain, whatever the kinds, before the inbox limit drops mail | Unit tests only |

In the 2026-10-29 run, five messages (two sent before the first prompt, a `task`, an `fyi` and an urgent one) cost four
turns, and none was lost.

**Compared with others (reasoning, not a measured comparison).** A pull design, such as MCP Agent Mail's inbox tools or
a cursor feed, spends a tool call on every check, and in an idle session something must start a turn to check at all. A
design that pushes every event into an idle session spends a turn per event. Claude Code's built-in messaging already
reads mail between tool calls at no extra turn and starts a turn when the session is idle, so against it mash adds only
waiting kinds, batching and the verified-only urgent rule. Nobody has published a head-to-head turn count.

**What it costs.** Waking mail reaches an idle session up to 45 s late (configurable from 5 s to 30 min). Savings depend
on senders marking mail `fyi` or `result`, because unmarked mail wakes. The wake needs Claude Code channels (a research
preview); without them, mail waits for the session's next prompt or tool call. A message over 16 KiB arrives as a
digest line and costs a `read` call.

## 5. Main tables

Legend: **Y** yes; **P** partial; **N** no; **base: N** not in the base protocol, though an extension or your own code
could add it; **n/a** does not apply; **?** could not be sourced. Numbers in brackets are footnotes. IBM/BeeAI ACP
merged into A2A (2025-08-29), so A2A's rows cover it.

### Delivery and trust

| Mechanism | Kind | Durable store-and-forward | Delivery guarantee | Per-message sender proof | Identity is | Outbound-only (NAT) |
|---|---|---|---|---|---|---|
| **mash** | Agent mail | Y, while retained: 30 days or 16 MiB per inbox, oldest dropped even if unread; single replica; a host crash can lose recent mail [1] | At-least-once to the session while retained; repeats flagged; a send with a key stored once within 2 min [2] | Y, every message signed (SAP §13); proves the key, not the process [3] | NATS user key + endpoint name | Y |
| SAP 0.3 (+ Synadia host plugins) | Agent protocol | N, out of scope in 0.3 | At-most-once (core NATS) | P, optional §13 signature | ed25519 NATS user key | Y |
| NATS JetStream | Stream store | Y, by configured limits | At-least-once; dedupe by `Nats-Msg-Id` within a window | base: N, connection auth | NATS user in an account | Y |
| MCP 2026-07-28 | Agent-to-tool | base: N; Tasks extension gives a polled task handle | ? | base: N, bearer token per request | OAuth 2.1 client | P, the server must be reachable |
| A2A 1.0 | Agent services | P, tasks and artifacts held by the serving agent; messages and stream updates may be lost [4] | Streams may miss updates on reconnect; recover state by task retrieval | base: N; Agent Cards may be JWS-signed | Agent Card URL; API key, OAuth, OIDC, mTLS | N for the 3 core bindings; P via custom bindings [5] |
| ANP 1.2 | Open agent network | ? | ? | ? | DID (`did:wba`) | ? |
| AGNTCY SLIM | Agent messaging | P, store and forward within a channel; offline delivery not described | Fire-and-forget or "reliable exactly-once" [6] | P, MLS end-to-end encryption | Keys, JWT, mTLS | Y |
| MCP Agent Mail | Agent mail | Y, Git + SQLite per project | P, persisted; explicit ack | base: N, tokens; export bundles signed | Generated name per project | P, one HTTP server peers reach |
| Claude Code subagents / agent teams | One session's helpers | P, mailbox files; team ends with the session | P | N | Name set by the lead | n/a, one machine |
| Claude Code cross-session messaging | One user's sessions | P, same machine: live socket only; to an `offline` remote session: waits for reconnect, no retention stated | P, messages held for approval expire (5 min default, can be "never") [7] | No signature; sender authenticated by OS user or claude.ai account | Session name, claude.ai account | Y, via Anthropic servers |
| OpenAI Agents SDK handoffs | One program | N | n/a | N | Agent name | n/a |
| LangGraph-style frameworks | One program | P, state checkpoints | n/a | N | Node or thread id | n/a |
| Kafka, RabbitMQ, Redis Streams | Brokers | Y | At-least-once (Kafka: exactly-once inside Kafka) | base: N, connection auth; apps can sign | Connection principal | P [8] |
| Slack | Chat for people | P, history kept; events not queued | 3 retries, then dropped | P, proves Slack sent it, not the author | Slack account or bot token | Y, Socket Mode |
| Email (SMTP) | Mail for people | Y | At-least-once in practice | P, DKIM proves a domain | Address, domain, key | P, receiving needs an MX |

### Reach and runtime

| Mechanism | Discovery / presence | Live request-reply | Wake an idle agent [9] | Host independence | Spec, licence | Maturity |
|---|---|---|---|---|---|---|
| **mash** | Y, directory + signed heartbeat | Y, SAP prompt | P, Claude Code only, via a research preview [10] | P, core needs no host (tested); one adapter, seam unproven | Documented SAP extension; Apache-2.0 | v0.3.0 (2026-11-01); repo created 2026-11-01; 1 maintainer |
| SAP 0.3 (+ host plugins) | Y, `$SRV` + heartbeat | Y, streamed | P, live prompts only, via Synadia's plugins | Y, plugins for 12 hosts | Draft; spec repo has no licence; SDK Apache-2.0 | 0.3.0 (2026-09-21) |
| NATS JetStream | P, `$SRV` services | Y | N | Y | Apache-2.0, CNCF | nats-server 2.15.2 (2026-10-26) |
| MCP 2026-07-28 | P, `server/discover`; no presence | Y | base: N | Y | Open spec | Revision 2026-07-28 |
| A2A 1.0 | P, Agent Card; no presence | Y, sync, SSE, push | P, only an agent already serving | Y | Apache-2.0, Linux Foundation | 1.0.0; 26k+ repo stars |
| ANP 1.2 | P, `.well-known` + search agents | ? | ? | ? | Apache-2.0 | Some parts draft |
| AGNTCY SLIM | P, separate directory | Y, SRPC | ? | ? | Apache-2.0; IETF drafts | Release tag slimctl-v3.4.6 (2026-10-20) |
| MCP Agent Mail | P, per project | N | N, agents read the inbox | Y, any MCP client | "MIT + Rider", not OSI [11] | Rust rewrite current |
| Claude Code subagents / teams | Y, roster | Y | Y, within the session | N | Proprietary | Agent teams experimental |
| Claude Code cross-session | Y, `ListAgents` | P | Y, starts a turn in an idle live session | N, Claude Code | Proprietary | v2.1.224+, on by default |
| OpenAI Agents SDK | N, set in code | n/a | N | N | MIT | openai-agents 0.24.0 |
| LangGraph-style frameworks | N | n/a | P, code resumes a thread | N | MIT | LangGraph 1.2.19 |
| Kafka, RabbitMQ, Redis Streams | N | P, reply-queue pattern | N | Y | Apache-2.0; MPL-2.0; RSALv2/SSPL/AGPLv3 | Kafka 4.3.1, RabbitMQ 4.4.0, Redis 8.10.3 |
| Slack | P, user presence | P | Y, event pushed to the app | Y | Proprietary | Current docs |
| Email (SMTP) | P, DNS MX | N | P, IMAP IDLE | Y | IETF standards | RFC 5321 and later |

### Operator cost

| Mechanism | What you run | Identity provisioning | Trust boundary | Host integration | Measured |
|---|---|---|---|---|---|
| **mash** | One NATS server with JetStream in operator mode, or a hosted account (free tier: 3 endpoints) | One NATS user, creds file and inbox stream per endpoint; `mash init synadia` automates the hosted case | One NATS account; its key holder can impersonate any endpoint and read mail | Claude Code plugin; a channels allowlist in managed settings for the idle wake | `mash send` 511-540 ms; urgent wake 1.2-2.3 s |
| SAP alone | One NATS server | NATS users per agent | Whatever the NATS setup gives | Synadia plugins, 12 hosts | Not found |
| NATS JetStream | One NATS server | NATS users | Accounts, with explicit export/import | You build it | Not applicable |
| A2A | An HTTP(S) server per agent, or a gateway in front of several | Per agent: API keys, OAuth, OIDC or mTLS | Per agent | SDKs; you write the executor | Not found for setup |
| MCP Agent Mail | One HTTP server | Generated names, tokens | One server, per project | Any MCP client; installer configures hosts | Not found |
| Claude Code cross-session | Nothing | claude.ai sign-in for other machines | One claude.ai user | Built in | Not found |
| Brokers | Your broker cluster | Broker principals | Broker ACLs | You build it | Not applicable |

## 6. Why NATS

mash needed four things from its transport: storage for mail, live request/reply, a per-agent key to sign with, and no
inbound ports on agent machines. NATS gives all four in one server. Most of what follows comes from the NATS docs, which
Synadia, the main NATS vendor, writes.

- **One server.** nats-server v2.15.2 for linux-amd64 ships as a 7.3 MB archive (release assets, 2026-11-01). Kafka 4
  runs a KRaft controller quorum, Pulsar needs brokers plus BookKeeper, and RabbitMQ runs on Erlang/OTP.
- **Subjects, not addresses.** Clients publish to subjects through the server and never connect to each other, so an
  agent needs no reachable address. A Kafka client must reach every broker's advertised listener.
- **Identity is a key.** NATS users are Ed25519 NKeys issued in JWTs, and the server handles only public keys. mash
  signs mail with that same key. Kafka, RabbitMQ and Redis authenticate a connection, not a signing key.
- **Live and stored in one server.** Core NATS gives at-most-once request/reply; JetStream adds at-least-once streams,
  `Nats-Msg-Id` dedupe and KV buckets.

**What NATS costs.**

- **Setup.** Operator mode, which per-endpoint keys need, takes several `nats auth` or `nsc` steps (operator, account,
  user, server config). A hosted account with `mash init synadia` skips most of them.
- **Durability defaults.** Jepsen (2025-12-08, NATS 2.12.1): "By default, NATS calls fsync to flush data to disk only
  once every two minutes, but acknowledges messages immediately." It lost acknowledged writes under simulated power
  failure and file corruption. A self-hosted server can set `sync_interval: always`, at a throughput cost. We have not
  confirmed the hosted service's fsync policy.
- **Smaller ecosystem than Kafka**, which also has the longer record for a large, long-lived log.
- **Vendor-led.** NATS is CNCF Incubating (since 2018-03-15), but most work comes from Synadia. In April 2025 Synadia
  said it meant to withdraw NATS from CNCF and move the server to the Business Source License. The 2025-05-01
  settlement kept the code in CNCF under Apache-2.0, and Synadia agreed to assign the NATS trademarks to the Linux
  Foundation.

**Speed.** We make no speed claim. ProtocolBench (arXiv 2510.17149), across A2A and other protocols: with latencies
"on the order of 9–13 seconds", "protocol adapter overhead is not the bottleneck". No test compares NATS and A2A.

## 7. Why SAP

The Synadia Agent Protocol (SAP) 0.3.0 (2026-09-21) is one file that names, discovers, checks and prompts agents on
NATS: "Anything NATS already provides is used as-is." mash uses SAP so that any SAP tool can find a mash session, check
it and prompt it live, and so that sender signatures follow a published format (§13).

**What mash adds, and where it departs.** mash is a documented SAP extension, not "no new protocol". It adds a `mail`
verb, an inbox stream layout, a directory, leases, mail kinds, two extra trust labels (`unknown-signer`, `stale`),
signed and unsigned headers, and a 2-minute dedupe window, all of which a second implementation would have to follow. It
bends one SAP MUST: mail is addressed through mash's directory, not `$SRV.INFO`. mash pins SAP 0.3.0 at a fixed commit.

| | SAP 0.3 | A2A 1.0 | SLIM | MCP |
|---|---|---|---|---|
| Each agent runs a server | No; it registers over its outbound connection | Yes, or sits behind a gateway | No; it connects to a node | The tool side does |
| Sender proof per message | Optional §13 ed25519; mash makes it mandatory | base: N; signed Agent Cards | MLS encryption | base: N |
| Governance | Synadia; spec repo has no licence file | Linux Foundation, Apache-2.0 | AGNTCY (Linux Foundation) | Linux Foundation project |

**Risks of building on SAP.**

- **Licence.** The spec repository has no LICENSE file (checked 2026-11-01). The SDK repository is Apache-2.0.
- **Single vendor, small adoption.** The spec repo has 1 contributor and 13 stars, the SDK repo 87 stars; A2A's repo has
  over 26,000. Synadia could change §13, or ship durable mail itself.
- **Draft, and tied to NATS.** SAP 0.3 leaves "JetStream-backed at-least-once streaming" out of scope, and it means
  nothing without a NATS system.

**Our reading (speculation, labelled as such).** SAP is small because NATS already supplies routing, auth, tenancy and
storage. It will likely suit agents that already share a NATS system, and is unlikely to replace A2A between
organisations that do not. If SAP adds durable delivery, mash intends to converge on it.

## 8. FAQ: why mash instead of A2A (and others)?

**Isn't A2A the standard?** For agent services, yes: it is the most widely backed agent protocol, under the Linux
Foundation. It is not federation-only either; it runs inside companies, and custom bindings can carry it over a bus. mash
answers a narrower question: signed mail that waits for your own sessions inside one trust domain.

**But A2A says its messages are unreliable.** Fairly stated, A2A keeps durable state in tasks and artifacts held by the
serving agent, and the spec warns that individual messages and streamed updates may be lost: "Messages MUST NOT be
considered a reliable delivery mechanism for critical information." A2A suits recoverable work held by a running service.
mash suits mail left for a session that may be stopped.

**Can't I run A2A over NATS?** Yes. A2A 1.0 allows custom bindings, and a community `a2a-nats` package exists (created
2026-06, v0.x, 0 stars on 2026-11-01). SLIM also carries A2A. What those do not give you today is per-message signatures
and delivery into a coding session.

**My agents are all Claude Code sessions. Why not the built-in messaging?** If they belong to one claude.ai user and mail
need wait only minutes, use it. It needs nothing installed. mash earns its place when sessions use several accounts or
auth methods (cross-machine built-in messaging needs a claude.ai sign-in; API key and cloud-provider sessions cannot reach
other machines), when mail must wait for days, when you need which-key-signed proof, or when `fyi` mail should not wake.

**Do I need a server per agent?** No. You need one NATS server, self-hosted or hosted, and each agent dials out to it.

**What exactly happens after `sent #<seq>`?** The message is in the recipient's JetStream inbox. It is shown to the
session at least once while it is retained: up to 30 days, and until a full inbox (16 MiB) drops it as the oldest, read
or not. A repeat after an interrupted hook is flagged. Retrying with the same key within 2 minutes stores nothing new;
after that, a retry stores a second copy. Inboxes are single-replica, so a host crash or power loss can lose mail
acknowledged before the server's last fsync (every 2 minutes by default) unless the server sets `sync_interval: always`.

**Does `[verified]` mean I should obey it?** No. It proves which endpoint's key signed the message. Any process that can
read that endpoint's credentials file can sign as it, and the account's key holder can impersonate any endpoint. Mail is
signed, not encrypted. Whether to act is up to the session's own instructions.

**Isn't a swarm coordinated through shared state, not mail?** Mostly, yes. Locks, task lists and pull requests carry the
work. Shared state is pulled, though, and nothing in it wakes a stopped session. Use mash as the doorbell next to the work
queue, not as the work queue.

**Is it faster than A2A? Isn't NATS one more thing to run?** Speed is unmeasured; a model turn takes seconds either way
(section 6). NATS is one more thing to run (see the operator-cost table); a hosted free account fits 3 endpoints.

**When should I not use mash?**

- Every agent is one user's Claude Code session and mail waits minutes: built-in messaging.
- Agents cross organisations, teams, operators or NATS accounts: A2A.
- Tools: MCP. Broadcast, groups, files, throughput: SLIM, JetStream or a broker. One program: a framework.
- Any host other than Claude Code, or Claude Code on Bedrock, Agent Platform or Foundry, or in a Team or Enterprise org
  whose Owner will not enable channels, when idle sessions must be woken.
- The server's operator must not read or forge mail.
- Losing the last two minutes of acknowledged mail on a host crash is unacceptable and you cannot set
  `sync_interval: always` or replicate.
- You need proof of which agent, not which key, among sessions that share one OS user.
- You need retention past 30 days, more than 16 MiB of backlog per inbox, an audit archive, or a supported,
  multi-maintainer project today.

## 9. Known limits

- **One host adapter.** Claude Code only. The core runs without a host (tested), but the adapter seam is unproven.
- **The idle wake rests on a research preview.** Claude Code channels are a research preview whose "flag syntax and
  protocol contract may change". They need a per-machine allowlist in managed settings, are off for Team and Enterprise
  orgs until enabled, and are unavailable on Bedrock, Agent Platform and Foundry. Without them, mail still arrives with
  the next prompt or tool call; it just does not wake an idle session.
- **Durability is R1.** Single-replica inboxes on default NATS fsync (see section 8). The 16 MiB drop-oldest rule means a
  flood from any endpoint can evict unread mail.
- **One NATS account.** Delivery between accounts is a non-goal. Setup is not one command: self-hosting needs NATS
  operator mode, and the hosted path needs a Synadia account.
- **No broadcast, no files.** One recipient per message; text up to 1 MiB, less on a smaller `max_payload`.
- **Maturity.** v0.3.0, repository created and released 2026-11-01, one maintainer. Interfaces may change
  before 1.0. SAP is a single-vendor 0.3 draft with no licence on its spec.

## 10. Footnotes and sources

1. Each inbox is a JetStream file stream: 30 days, 16 MiB, drop oldest (unread included), 1 MiB per message, delete and
   purge denied, one replica. Default nats-server fsync runs every 2 minutes; see section 6.
2. The 2-minute window is the stream's `Nats-Msg-Id` duplicate window. The recipient also remembers what it has shown for
   the whole session, so a redelivery is not shown twice.
3. `verified` means the signature checks against a key the directory maps to that endpoint name. Forged mail is never
   shown; `claimed`, `absent`, `unknown-signer` and `stale` mail is shown with its label.
4. A2A 1.0 §3.7: "Messages MUST NOT be considered a reliable delivery mechanism for critical information." The same
   section says agents MAY persist important messages in task history, but clients "MUST NOT rely on this behavior unless
   negotiated out-of-band."
5. A2A 1.0 §12: "implementers MAY create custom protocol bindings". Examples: AGNTCY SLIM, and the community `a2a-nats`.
6. AGNTCY messaging draft -02 (2026-07-07): "reliable exactly-once delivery". The SLIM draft describes intermediate nodes
   that "store and forward messages within the same channel". Neither describes delivery to an offline recipient.
7. Only messages held under the default inbound rule expire; an explicit `hold` never expires. Claude Code also throttles
   message loops and never treats a peer message as consent.
8. Clients dial out, but a Kafka client must reach each broker's advertised listener.
9. "Wake" means starting a model turn in a session that is running but idle. None of these starts a stopped process.
10. `fyi` and `result` wait; waking mail wakes after 45 s; verified urgent mail at once. See section 4.
11. The rider grants no rights to OpenAI, Anthropic or their affiliates.

**Sources** (all read 2026-11-01 unless a date is given):

- **mash:** `docs/architecture.md` §1-§4 and Appendix B; ADRs 0006, 0010, 0011, 0024, 0026-0029, 0031 and 0037; live
  receipts `docs/evidence/2026-10-22-live-e2e.md` and `docs/evidence/2026-10-29-default-kind-live-v0.2.6/RECEIPT.md`;
  `LICENSE` at tag v0.3.0.
- **SAP:** https://github.com/synadia-ai/synadia-agent-sdk-docs/blob/main/core-protocol.md (0.3.0);
  https://github.com/synadia-ai/synadia-agent-sdk-docs (no licence, 13 stars, 1 contributor);
  https://github.com/synadia-ai/synadia-agents (Apache-2.0, 87 stars; `agents/` lists 12 hosts).
- **NATS:** https://docs.nats.io/nats-concepts/jetstream;
  https://raw.githubusercontent.com/nats-io/nats.docs.v2/main/learn/security/operator-mode.md;
  https://docs.nats.io/reference/config/jetstream/sync_interval;
  https://github.com/nats-io/nats-server/releases/tag/v2.15.2; https://www.cncf.io/projects/nats/;
  https://www.cncf.io/blog/2025/04/24/protecting-nats-and-the-integrity-of-open-source-cncfs-commitment-to-the-community/;
  https://www.cncf.io/announcements/2025/05/01/cncf-and-synadia-align-on-securing-the-future-of-the-nats-io-project/.
- **Durability:** https://jepsen.io/analyses/nats-2.12.1 (2025-12-08).
- **Benchmark:** https://arxiv.org/abs/2510.17149 (ProtocolBench).
- **A2A:** https://github.com/a2aproject/A2A (`docs/specification.md`, 1.0.0; 26,365 stars);
  https://a2a-protocol.org/latest/specification/; https://github.com/wyrd-company/a2a-nats.
- **AGNTCY SLIM:** https://www.ietf.org/archive/id/draft-mpsb-agntcy-slim-02.html;
  https://www.ietf.org/archive/id/draft-mpsb-agntcy-messaging-02.txt; https://github.com/agntcy/slim/releases.
- **Claude Code:** https://code.claude.com/docs/en/cross-session-messaging; https://code.claude.com/docs/en/channels;
  https://code.claude.com/docs/en/agent-teams; https://code.claude.com/docs/en/sub-agents.
- **MCP Agent Mail:** https://github.com/Dicklesworthstone/mcp_agent_mail;
  https://github.com/Dicklesworthstone/mcp_agent_mail_rust.
- **Carried from revision 1 (read earlier on 2026-11-01, not re-checked for this revision):** MCP, ANP, OpenAI Agents
  SDK, LangGraph, broker and Slack cells, sources in `comparison-chart-r1.md`. Email RFCs and the ACP merger post
  (2025-08-29, seen through search) were not re-read.
