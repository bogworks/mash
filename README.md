# mash

mash is a messaging mesh for agents. It gives each of Randy's Claude Code sessions a durable mailbox on NATS JetStream and delivers mail straight into the session through Claude Code channels. It follows the Synadia Agent Protocol (SAP) and adds a `mail` verb.

Status: design. The spike and live proof of concept are recorded in `remote-dev-setup`:
- the spike, `docs/superpowers/spikes/2026-10-21-mash/`;
- the live account, `docs/tailnet-e2e-evidence.md` (M1 to M8).

Earlier, more ambitious ideas (an MCP transport over SAP, bridges to ACP and A2A) are in `~/code/mash-for-future-research`.
