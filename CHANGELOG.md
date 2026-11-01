# Changelog

All notable user-facing changes to mash are recorded here. The format follows [Keep a Changelog](https://keepachangelog.com/en/1.1.0/), and mash follows [Semantic Versioning](https://semver.org/) (pre-1.0: minor releases may change behaviour).

## [0.3.1] - unreleased

### Added

- `mash status` prints what a session would see (name, config, creds, channel allowlist, environment, version) from a shell, before Claude Code is launched. It shares the `status` tool's text, connects to nothing, works with `MASH_NAME` unset (printing `name: MISSING`), redacts the same secrets, and says the connection and delivery lines are only known inside a running session. It always exits 0.
- Inbox, bucket and consumer limits are configurable in the `limits` section of config.json, with the defaults unchanged. `mash admin limits [name...]` raises existing inboxes in place and `--show` prints the effective limits. mash never lowers a limit, because that could discard mail; a lower value is refused, naming the endpoint and field, and an unlimited stream is never given a finite cap. `mash admin init` raises a bucket cap that is below the configured one (see "Limits" in [docs/operations.md](docs/operations.md)).
- Build provenance: the release binaries carry a GitHub attestation, and the publish gate verifies it for all four binaries before a release is published. Check one with `gh attestation verify mash-linux-amd64 -R bogworks/mash` ([docs/release.md](docs/release.md)).
- Docs: [docs/faq.md](docs/faq.md) and [docs/troubleshooting.md](docs/troubleshooting.md).

### Changed

- A `claude --resume` of a still-running session (same session id, new `MASH_LAUNCH_TOKEN`) no longer drains the lease holder's spool: its hooks are refused, and it is told once in its own session that it is not receiving mail ([ADR-0043](docs/decisions/0043-second-launch-of-a-running-session-is-refused-and-told-once.md)).
- `mash admin provision` refuses to lower the caps of an inbox stream that already exists (a retried provision no longer shrinks an inbox the limits command had raised).

## [0.3.0] - 2026-11-01

Changes since 0.2.10.

### Changed

- Mail is delivered by hook. When mash's hooks are installed, the mail arrives as "mash mail" context after a tool call or with a prompt, and the channel event only wakes an idle session with a one-line summary.
- A backlog of `fyi` or `result` mail wakes the session to drain it, so waking mail queued behind it still arrives. A fresh session wakes at once for mail that arrived before its first hook, under spool-from-start.
- The sender of a message is shown by its endpoint name.
- A creds file that other users can read is refused. The file must be mode 0600.
- The directory records an endpoint's role, host and free-form labels (`--label key=value`, up to 8, repeatable on `mash admin provision` and `mash admin set-meta`). `mash admin list` is unchanged.
- Hosted NATS servers are supported better: every KV bucket carries a size cap (an older uncapped bucket is bounded in place by `mash admin init`), the send size cap follows the server's `max_payload`, and `open_timeout` and `call_timeout` suit slow links.
- Each inbox keeps mail for 30 days or 16 MiB, and a consumer is limited in outstanding messages; a backlog of half the consumer's limit wakes the session to drain (see "Inbox limits" in [docs/operations.md](docs/operations.md)).
- `mash serve` starts in 7 round trips, and CLI commands use under 6 NATS round trips, flat in the size of the fleet.
- A message's shown stream sequences are remembered for the session, and only pruned below the consumer's confirmed ack floor, so a redelivery cannot show twice.
- `mash prompt` replies come back on unguessable inboxes, so a spoofed answer is refused.
- mash refuses to sign a connect nonce that did not come from the server (signature oracle).
- The MCP server answers `initialize` with a protocol revision mash supports and never echoes the client's.
- Docs: a host crash or power cut can lose up to about 2 minutes of acknowledged mail unless the NATS server sets `sync_interval: always`; process restarts are safe. See [docs/operations.md](docs/operations.md#durability-and-sync_interval).

### Added

- `mash send --id KEY` makes a send at-most-once across retries. A timed-out send prints the key to retry with.
- The lease record carries `waking_pending` and `waking_pending_at`.
- MCP `version` and `status` tools show the binary, the plugin and the endpoint a session is talking to, and what is missing in a mis-configured mode.
- A Synadia Cloud free-tier quickstart: [docs/synadia.md](docs/synadia.md), with `scripts/quickstart-synadia.sh` (ADR 0038).
- Docs for outside readers and contributors: [SECURITY.md](SECURITY.md) (private vulnerability reporting), [CONTRIBUTING.md](CONTRIBUTING.md), [CODE_OF_CONDUCT.md](CODE_OF_CONDUCT.md), and the architecture decisions as ADRs in `docs/decisions/`. Issue and pull request templates.
- Release process: a publish check and a release gate that refuses to tag while an earlier release is unpublished, and a mail-latency gate against the previous release.

### Removed

- The `--herd` directory field and the legacy herd read. Use `--label key=value` instead.
