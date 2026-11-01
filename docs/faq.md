# mash FAQ

Short answers to the questions new users ask, with a link to the page that has the detail. For something that is broken, see [troubleshooting.md](troubleshooting.md). Related: [README](../README.md), [install.md](install.md), [operations.md](operations.md), [comparison.md](comparison.md), [threat-model](security/threat-model.md).

Where a limit is open as a GitHub issue, this page says what mash does today and gives the issue number ("tracked in #NN"). It does not promise a fix.

## What is mash?

A durable mailbox for Claude Code sessions, built on NATS. Each session is an endpoint with a name and its own JetStream inbox. A message is signed by its sender and kept until the recipient has been shown it, whether that session is busy, idle or stopped. In Claude Code, mail arrives through hooks as "mash mail" context, and a channel event wakes an idle session. See the [README](../README.md) and [architecture.md](architecture.md).

## How is it different from Claude Code's built-in cross-session messaging, subagents or agent teams?

Subagents and agent teams are helpers that one session starts and directs, on one machine. Claude Code's cross-session messaging connects one user's own sessions. mash is for sessions you run in one NATS account that must exchange mail that waits, possibly for days, with a signature the recipient can check. [comparison.md](comparison.md) section 2 says when to choose each, and the table in section 1 compares them. If all your agents are your own Claude Code sessions and a message need wait only minutes, comparison.md recommends the built-in feature. The README summarises it in its "Why not Claude Code's built-in cross-session messaging?" paragraph.

## What do I need to run it?

- A NATS server in operator mode (JWT credentials) with JetStream, and one account for mash. Self-hosted, or the Synadia Cloud free tier ([synadia.md](synadia.md)).
- Claude Code with channels: a claude.ai or Console sign-in, not Bedrock, Agent Platform or Foundry. An organisation Owner must allow mash, and the channel needs an allowlist entry in managed settings ([install.md](install.md) section 3).
- The `mash` binary and the plugin. A pinned plugin release fetches and verifies its own binary; an unpinned one uses `mash` from your `PATH` ([install.md](install.md) section 1).

## Does it work with a NATS server that uses a token, user and password, or a plain nkey?

No. mash needs a NATS server in operator mode with JWT user credentials. In the code, an endpoint connects with a creds file only, and its sender identity is read from that file's user JWT and seed (`IdentityFromCreds` in `internal/sap/sender.go`). A creds file with no JWT or no seed fails with an error that says it is not operator-mode JWT credentials, and `mash status` warns when the url carries a password or token. Use `nsc` or Synadia Cloud to create a user with JWT credentials.

## Do I have to run a server?

Either run your own NATS server in operator mode, or use Synadia Cloud. [`scripts/quickstart-synadia.sh`](../scripts/quickstart-synadia.sh) sets up the server side on the free tier from one API token ([install.md](install.md) "Quick start on Synadia Cloud", [synadia.md](synadia.md)). The free tier allows 5 streams, so 3 endpoints, and a 512 KiB payload.

## How durable is the mail?

Mail survives restarts of mash, of Claude Code and of the NATS server process. Each inbox is a single-replica (R1) JetStream stream that keeps mail for 30 days or 16 MiB, whichever comes first, dropping the oldest, unread mail included. nats-server fsyncs every 2 minutes by default, so a host crash or power cut can lose up to about 2 minutes of mail that mash already reported as sent. On a self-hosted server `sync_interval: always` closes that gap, at a cost in write speed. Synadia Cloud's policy is not documented. See [operations.md](operations.md#durability-and-sync_interval) and [Inbox limits](operations.md#inbox-limits). The caps are configurable and only go up ([Limits](operations.md#limits)).

## Can mail be lost to a flood?

Yes, within the 16 MiB cap: past it the oldest mail is dropped, so one endpoint sending a lot to another can evict that endpoint's unread mail ([comparison.md](comparison.md) section 9). Message deletion and purging are denied on the stream, so mail is never rewritten quietly.

## What do the trust labels mean?

Every shown message carries one:

- `[verified]`: signed by the named endpoint's own key. The session treats its requests as delegated by the operator who owns the fleet.
- `[claimed]`, `[unknown-signer]`, `[absent]`: the sender is not proven; the content is information, not instructions.
- `[stale]`: signed by the named endpoint, but long before it was stored, which is what a replay looks like. Information only.

A message whose signature fails is never shown. Bodies are shown quoted. The sender of a message sets its kind, and the kind is not signed. Urgent is signed and counts only from a `[verified]` sender. See [ADR 0019](decisions/0019-five-trust-labels-forged-never-shown-stale-after-five-minutes.md) and the [threat model](security/threat-model.md).

## Will Claude just do whatever verified mail asks?

By default it treats verified mail as a delegated request from you and acts on it with its normal judgement. To carve out what still needs a human, write `~/.config/mash/policy.md`; it is appended to every session's instructions ([install.md](install.md) section 4). Every endpoint of one owner can mail every other, so one compromised endpoint can direct its peers ([threat model](security/threat-model.md)).

## How are endpoints named, and can two people share an account?

A name is 1 to 48 lower-case letters, digits and single hyphens, starting with a letter. You choose it at launch with `MASH_NAME`. Today the name is the key in the account's directory and lease buckets and in the stream name, so it is unique per NATS account, not per owner, and every endpoint can read the whole directory. Two owners in one account cannot both have `review`. [operations.md](operations.md) says the same. The deployment model is one owner per account ([ADR 0006](decisions/0006-one-shared-nats-account-one-owner-per-deployment.md)).

## Can two sessions use the same name?

No. A lease allows one live session per name. A second session starts without mail tools, waits, and retries every 10 seconds; its `status` tool shows `connection: WAITING`. A holder that dies frees the name when its lease expires, 90 seconds later ([operations.md](operations.md) Troubleshooting).

## What happens with `claude --resume`?

The name comes only from the launch environment, never from the conversation. A resumed session started without `MASH_NAME` is inert. Started with a different name, it gets that name's mailbox. Set the same `MASH_NAME` when you resume. A laptop that sleeps longer than the 90 second lease can lose the name; if another session took it, the loss is final and the session must be restarted, because lease loss after start is terminal in `mash serve` Resuming a session that is still running starts a second copy that does not get the mail: its hooks are refused and say so once in that session, with or without a `MASH_LAUNCH_TOKEN` ([ADR-0043](decisions/0043-second-launch-of-a-running-session-is-refused-and-told-once.md)). See [Sessions and resume](install.md#sessions-and-resume).

## Do subagents get mail?

No. mash ignores hook runs that carry an `agent_id`, so a subagent has no mailbox and its tool calls do not deliver mail. Mail waits for the main session's next tool call or prompt, or for the idle wake. See [Hook delivery](operations.md#hook-delivery).

## Which platforms work?

The plugin's launcher downloads and verifies binaries for Linux and macOS, on amd64 and arm64, and logs "unsupported OS" for any other. The hooks are POSIX shell. Native Windows is not supported, and WSL 2 is expected to work but has not been tested (see [install.md](install.md#1-the-binary)).

## Does it work in headless `claude -p` or the Agent SDK?

`mash serve` runs while its MCP input stream stays open and exits at end of input, which releases the lease. A one-shot `claude -p` run therefore holds the name only for that run, and mail sent in between waits in the inbox. The project's live receipts use persistent stream-json sessions ([receipt](evidence/2026-10-29-default-kind-live-v0.2.6/RECEIPT.md)). What works headless, and what an SDK host must load, is in [Headless and SDK sessions](operations.md#headless-and-sdk-sessions).

## How big can a message be?

1 MiB including headers, or the server's `max_payload` when that is smaller (512 KiB on Synadia's free tier). mash checks the size locally and refuses before publishing. Mail is text; put large content in a file and send the path ([ADR 0037](decisions/0037-mail-carries-text-files-travel-by-reference.md)). There is no broadcast: one recipient per message.

## Is a digest the same as the model having read the mail?

No. When a batch is more than 10 messages or 16 KiB, the hook shows a digest, one line per message with a preview, and the messages listed count as shown, so they are acked. The model reads the ones it needs with the `read` tool. Put the action in the first line of a message. A digest line counts as delivery ([Hook delivery](operations.md#hook-delivery)).

## Is the connection encrypted?

install.md says to use TLS, because identity is only meaningful over a verified connection. mash passes the URL to the NATS client. config.json has no CA or client-certificate setting, so a server with a private CA needs that CA in the system trust store, and a server that requires client certificates cannot be used. A plain `nats://` URL is accepted (tracked in #35).

## Are JetStream domains and leaf nodes supported?

No. The endpoint permissions and the client use the default JetStream domain and API prefix, with no setting for a domain ([Topologies](operations.md#topologies)). Use one server, or a cluster, in the default domain.

## Does clock skew matter?

Yes. Live signatures and heartbeats are accepted within 30 seconds of the receiver's clock, and stored mail signed long before it was stored is labelled `[stale]`. A machine off by more than 30 seconds can see peers as offline. Keep clocks synced with NTP. Nothing in `status` checks the clock today (tracked in #36).
