Generated from spec/*.json by go run ./scripts/refgen. Do not edit.

# Reference

The mash and mashctl command reference for release 0.5.9, generated from the
spec files in spec/.

Source: v0.5.8 (8e8fe56).

Regenerate: `go run ./scripts/refgen`. A test fails when these pages differ
from the spec.

## Commands

- [`mash`](mash.md): Trusted mail between agents. Speaks as one endpoint.
  - [`mash send`](mash-send.md): Send a message to an endpoint.
  - [`mash inbox`](mash-inbox.md): List the messages this endpoint has
    received.
  - [`mash read`](mash-read.md): Show one message in full.
  - [`mash who`](mash-who.md): List the endpoints you can see, and which are
    online.
  - [`mash status`](mash-status.md): Say in one line whether mail works for
    this endpoint, and whether it is online.
  - [`mash doctor`](mash-doctor.md): Check everything mail depends on for this
    endpoint, and say how to fix what is wrong. Changes nothing.
  - [`mash version`](mash-version.md): Print the version. It can also be run
    as --version.
  - [`mash join`](mash-join.md): Install an endpoint's credentials on this
    machine from a join file.
  - [`mash key`](mash-key.md): Make your own key pair, for when the seed must
    never leave this machine.
    - [`mash key create`](mash-key-create.md): Make a seed and its public key.
  - [`mash serve`](mash-serve.md): Run as an MCP server for an agent host.
    Started by the host, not by hand.
  - [`mash hook`](mash-hook.md): Print mail waiting for this session, in the
    form the agent host expects. Run by the host after each tool call and with
    each prompt.
  - [`mash prompt`](mash-prompt.md): Ask an endpoint a live question and wait
    for its answer. Not stored: if the recipient is not there, it fails.
- [`mashctl`](mashctl.md): Administers a mash mesh through a server.
  - [`mashctl init`](mashctl-init.md): Set mash up on a NATS server you
    already have, from a file.
  - [`mashctl status`](mashctl-status.md): Say in one line whether the server
    is working, and how many endpoints it has.
  - [`mashctl doctor`](mashctl-doctor.md): Check the server, mash's storage on
    it and every endpoint's records, and say how to fix what is wrong. Changes
    nothing.
  - [`mashctl limits`](mashctl-limits.md): Show or apply the storage limits.
    - [`mashctl limits show`](mashctl-limits-show.md): Print the limits in
      force.
    - [`mashctl limits apply`](mashctl-limits-apply.md): Raise existing
      inboxes to the configured limits. Never lowers one.
  - [`mashctl endpoint`](mashctl-endpoint.md): Create, change and remove
    endpoints.
    - [`mashctl endpoint create`](mashctl-endpoint-create.md): Create an
      endpoint and its credentials.
    - [`mashctl endpoint delete`](mashctl-endpoint-delete.md): Delete an
      endpoint, its inbox and its credentials. The address can never be used
      again.
    - [`mashctl endpoint list`](mashctl-endpoint-list.md): List endpoints.
    - [`mashctl endpoint show`](mashctl-endpoint-show.md): Show one endpoint.
    - [`mashctl endpoint set`](mashctl-endpoint-set.md): Set a value on an
      endpoint's record.
    - [`mashctl endpoint unset`](mashctl-endpoint-unset.md): Remove a value
      from an endpoint's record.
    - [`mashctl endpoint suspend`](mashctl-endpoint-suspend.md): Refuse new
      mail to an endpoint. Its stored mail and its credentials are untouched.
    - [`mashctl endpoint resume`](mashctl-endpoint-resume.md): Take new mail
      again, undoing suspend.
    - [`mashctl endpoint reissue`](mashctl-endpoint-reissue.md): Give an
      endpoint new credentials and cancel the old ones.
    - [`mashctl endpoint inbox`](mashctl-endpoint-inbox.md): List what is in
      an endpoint's inbox, without showing any message's text.
    - [`mashctl endpoint permissions`](mashctl-endpoint-permissions.md): Print
      the NATS permissions an endpoint's user needs, for someone who runs
      their own NATS.
  - [`mashctl version`](mashctl-version.md): Print the version. It can also be
    run as --version.

## MCP tools

- [MCP tools](mcp.md)
- [Server](mcp-server.md)
  - [`doctor`](mcp-doctor.md)
  - [`inbox`](mcp-inbox.md)
  - [`read`](mcp-read.md)
  - [`reply`](mcp-reply.md)
  - [`send`](mcp-send.md)
  - [`status`](mcp-status.md)
  - [`version`](mcp-version.md)
  - [`who`](mcp-who.md)

## Concepts

- [Glossary](glossary.md)
- [Rules](rules.md)
- [Setup scripts](setup.md)
- [Coming in 0.6.0](coming-in-0-6-0.md)
