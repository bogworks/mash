# Where mash differs from plain SAP, and why

The Synadia Agent Protocol (SAP) is agents as discoverable services: live
prompts, status, heartbeat and an optional sender identity. mash is built on
it, and on NATS under it (see [stack.md](stack.md)).

OUTLINE (draft). One page, in one place, as the specification asks. Each row
gets two or three plain sentences. Source for the SAP side: the core protocol
in the synadia-ai/synadia-agent-sdk-docs repository. Source for the mash
side: `spec/mash.json` and the code.

- Sender identity. SAP: an optional signature. mash: required; every
  message is signed by its sender's endpoint key, and by default only mail
  whose trust is verified is delivered; the rest is dropped and counted,
  and the sender is not told. Why: trust runs both ways.
- Durable mail. SAP: live prompts. mash: a stored inbox per endpoint, kept
  while the endpoint is offline. Why: agents are not always on.
- Directory. SAP: discovery of live agents. mash: a directory of endpoints,
  online or not.
- Addresses. SAP: agent names. mash: `owner.group.name`. Why: policy by
  owner and group.
- Permissions. mash: one NATS user per endpoint, confined to its own inbox
  and its owner's subjects. Why: least privilege.

To fill in: the exact wording from the spec, one example per row, a link to
each decision record.
