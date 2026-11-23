# mash and the Synadia Agent Protocol

mash registers each endpoint as a Synadia Agent Protocol (SAP) 0.3 agent. This page states where mash departs from SAP and what that means for finding mash endpoints. For a shorter version, see [mash and SAP: the differences](guide/mash-vs-sap.md). The decision is [ADR 0145](decisions/0145-an-endpoint-address-is-owner-group-instance-and-0-5-5-is-a-hard-cut-with-no-migration-in-mash.md).

## The subject shape

Every endpoint has four subjects:

```text
agents.<verb>.<owner>.<group>.<instance>
```

The verbs are `prompt`, `status`, `hb` and `mail`. `prompt`, `status` and `hb` are SAP's. `mail` is mash's addition: it carries durable messages, which SAP does not define.

The three slots after the verb are the endpoint's address:

- `owner`: who the endpoint belongs to.
- `group`: a set of endpoints under one owner. There is no default group.
- `instance`: one endpoint in the group. SAP calls this slot `instance`, and so do the subject and the service metadata below. Everywhere else mash's documents call the same token the endpoint's `name`, and an address is `owner.group.name`.

For `alice.dev.design`, the mail subject is `agents.mail.alice.dev.design`.

Each of the three tokens follows one rule:

- Lower-case letters, digits and hyphens.
- Starts with a letter.
- Does not end with a hyphen and has no `--`.
- 1 to 32 characters.

The whole address, `owner.group.name` with its two dots, is at most 64 characters.

The flat key joins the tokens with underscores: `alice_dev_design`. Tokens never contain an underscore, so the flat key maps back to one address. These are keyed by the flat key:

- the directory record
- the lease
- the issue record
- the inbox stream, named `inbox-alice_dev_design`
- the retired-key ledger

The creds file keeps the dots: `alice.dev.design.creds`.

## The departure

SAP says the first slot after the verb SHOULD name the harness. mash does not follow that. It puts the owner there. The harness is still reported, in the service metadata `agent`, whose value is `claude-code`.

Consequence for discovery:

- A wildcard on the first slot, such as `agents.mail.claude-code.>`, finds no mash endpoint.
- Discovery by the SAP service name does. mash registers the service name `agents`, so `$SRV.INFO.agents` lists mash endpoints.
- The service metadata carries `owner`, `group` and `instance` (the name), so a caller reads who each endpoint is from the reply.
- mash finds an endpoint by its prompt subject alone, `T3.T4.T5` as the subject carries them. It does not filter on the metadata `owner`, so an endpoint hosted by another SAP SDK (`harness.owner.session`) is found by that address. Metadata is never used for routing or trust.
- The T3.T4.T5 space is shared; NATS subscribe permissions, not discovery, decide who answers a prompt.

This is a documented change in what one slot means. The subject keeps its shape and its five tokens. SAP's protocol and the SAP Go SDK are not changed.

### Security

A credential in the same account can answer the info request and make X look reachable for prompt when it is not; it cannot receive X's prompts and cannot make X show as verified in `mash who`.

## Service metadata

mash registers these keys on the `agents` service:

| Key | Holds |
|---|---|
| `agent` | The harness: `claude-code`. |
| `owner` | The owner token, `alice`. |
| `group` | The group token, `dev`. |
| `instance` | The instance token, `design`. |
| `session` | The endpoint's flat key, `alice_dev_design`. |
| `protocol_version` | The SAP version, `0.3`. |
| `user_nkey` | The public key of the endpoint's NATS user. |
| `account` | The public key of the NATS account the endpoint belongs to. |
| `id_sig` | A signature over the user key, account, agent, owner and prompt subject, which readers use to check that the record is the endpoint's own. |
| `mail_subject` | The endpoint's mail subject. |

The `prompt` endpoint adds its own metadata: `max_payload`, `attachments_ok` and `min_sender_trust`.

## The reply-inbox prefix

An endpoint's reply-inbox prefix is `_INBOX.<owner>.<flat key>`. For `alice.dev.design` it is `_INBOX.alice.alice_dev_design`.

The endpoint's permissions are:

- It subscribes only to `_INBOX.alice.alice_dev_design.>`. Replies meant for it stay private to it.
- It publishes replies only to `_INBOX.alice.>`. An endpoint of another owner can neither answer nor forge into an inbox of `alice`.

Inside one owner, endpoints can still publish to each other's reply subjects. ADR 0145 records this until a later release adds a policy.

The prefix is where mash's use of NATS request and reply differs from a stock client. A stock client uses `_INBOX.` followed by a random token. mash's connections use the prefix above.

## What did not change

The SAP verbs `prompt`, `status` and `hb`, and the service name `agents`, are as in SAP 0.3.
