# Where mash differs from plain SAP, and why

OUTLINE (draft). One page, in one place, as the specification asks. Each row
gets two or three plain sentences. Source for the SAP side: the core protocol
in the synadia-ai/synadia-agent-sdk-docs repository. Source for the mash
side: `spec/mash.json` and the code.

- Sender identity. SAP: an optional signature. mash: required; every
  message is signed by its sender's endpoint key, and unverified mail is
  refused on receipt by default. Why: trust runs both ways.
- Durable mail. SAP: live prompts. mash: a stored inbox per endpoint, kept
  while the endpoint is offline. Why: agents are not always on.
- Directory. SAP: discovery of live agents. mash: a directory of endpoints,
  online or not.
- Addresses. SAP: agent names. mash: `owner.group.name`. Why: policy by
  owner and group.
- Permissions. mash: one NATS user per endpoint, confined to its own inbox
  and its owner's subjects. Why: least privilege.
- Live prompts. SAP: the core. mash: later (`mash sap`, after 0.5.6).

To fill in: the exact wording from the spec, one example per row, a link to
each decision record.
