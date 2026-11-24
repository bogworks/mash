# mash and the SAP Go SDK

This page maps mash's code onto the SAP Go SDK, `github.com/bogworks/synadia-agents.go`, tag v0.1.0 (SAP 0.3). Each row says one of three things:

- **adopted**: the SDK call will replace mash's code, with the same bytes on the wire. The stage that does it is named.
- **kept in mash**: the piece has no SDK equivalent, or mash's rule is stricter than SAP's. The row says why.
- **waits**: the piece cannot move yet. The row says on what.

The move is planned in stages. None of them changes what mash sends or accepts until the 0.6.0 security policy release turns the new paths on.

| Stage | What it does |
|---|---|
| S1 | pins the SDK in `go.mod`, imported by one test only; no shipped binary links it |
| S2 | a compatibility gate: mash's old code and the SDK give the same verdicts on one corpus |
| S3 | the SAP constants and subjects come from the SDK's builders |
| S4 | sender identity goes through the SDK's `identity` package, in a new package that nothing ships yet |
| S5 | the caller side (discovery and prompts) goes through the SDK's `caller` package |
| W | the receive and send paths are wired onto S4 and S5, at the 0.6.0 switch-on |

## Where mash differs from plain SAP, and why

[mash and SAP](sap.md) has the full story. The differences that shape this mapping:

- **The subject slots.** SAP's subject is `agents.<verb>.<agent>.<owner>.<name>`. mash's is `agents.<verb>.<owner>.<group>.<instance>` ([ADR 0145](decisions/0145-an-endpoint-address-is-owner-group-instance-and-0-5-5-is-a-hard-cut-with-no-migration-in-mash.md)). The five tokens and the root are the same, so the wire is compatible. The SDK can build mash's bytes: `NewAgentSubject("claude-code", group, instance, WithSubjectToken(owner))` puts the owner in the agent slot and the group in the owner slot. A spike run found it byte-identical for 610 of 610 addresses.
- **The `mail` verb.** SAP has `prompt`, `status` and `heartbeat`. mash adds `mail`, a durable inbox. The SDK refuses a `mail` verb by design.
- **Signed extra headers.** mash signs the instance, urgency, `in_reply_to` and `via` of a message. SAP signs a fixed set of fields.
- **Trust labels.** SAP has verified, claimed and absent. mash also shows unknown-signer and stale.
- **The token rule.** mash allows lower-case a-z, 0-9 and single dashes, starting with a letter, at most 32 characters. SAP's rule is looser (#1296).
- **Registration metadata.** mash's registration advertises `owner=<owner>`. The SDK host writes `owner` from the subject's owner slot, which in mash holds the group. This is why mash keeps its own registration.

## `internal/sap`

| mash | SDK v0.1.0 | State |
|---|---|---|
| `ServiceName`, `QueueGroup`, `ProtocolVersion` (`subjects.go`) | `agents.ServiceName`, `QueueGroup`, `ProtocolVersion` | adopted in S3; the same literals, pinned by goldens |
| `AgentName = "claude-code"` | none: the harness value is the caller's | kept in mash: it names mash's harness |
| `Address.Subjects()`: prompt, status, heartbeat (`address.go`) | `NewAgentSubject(...)` with `WithSubjectToken(owner)` | adopted in S3; byte-identical, and an invalid address fails with mash's own message, never SDK error text |
| `Subjects.Mail` | none: the SDK refuses a `mail` verb | kept in mash: `mail` is mash's verb |
| `checkToken`, `MaxTokenLen`, `MaxIdentityLen` | `ValidateToken`, which is looser | kept in mash: mash checks its stricter rule first, then the SDK's |
| `AddressFromSubject`, `ParseAddress`, `Flat`, `KeyOf`, `InboxStream`, `InboxPrefix`, `MailFilter` | none | kept in mash: the SDK's `ParseSubject` would read a mash subject with the owner as the agent and the group as the owner |
| `HeaderName` (`Agent-Sender`), the header encoder (`sender.go`) | `identity.SenderHeader`, `Encode`, `Set` | adopted in S4, live at W |
| `Identity.Sign` and its signed input | `identity.SignHeader`, `SignedInput` | adopted in S4, live at W; the signed input is the same |
| `Identity.IDSig`, `VerifyIDSig` | `identity.SignAgentID`, `VerifyAgentID` | adopted in S4, live at W; the same `AGENT-ID-V1` input |
| the header parser, `VerifyStored` | `identity.ParseHeader`, `VerifyStored` | adopted in S4, live at W. The SDK's parser is stricter: ten header shapes that mash accepts today are refused as malformed, and each refusal is counted and shown |
| `VerifyLive`, `LiveSkew` (30 s) | `identity.VerifyLive` (30 s window), with `AdmitPrompt` and `PreAccept` | adopted in S4, live at W |
| `NonceSet`, `NewNonceSet` | `identity.NewNonceSet`: 100,000 in all, 10,000 per sender, fails closed | adopted in S4 behind mash's own nonce-set interface, which can say "refused: full"; live at W |
| trust `verified`, `claimed`, `absent`, `forged`, `malformed` | `identity.Trust` (verified, claimed, absent) and `*identity.Error` (400, 401) | adopted in S4: forged and malformed come from the SDK's error codes |
| `TrustUnknownSigner`, `TrustStale` | none: the SDK's trust type is closed | kept in mash: mash derives them from its directory and the message's age |
| `SignInstance`, `SignUrgent`, `SignInReplyTo`, `SignVia` and their verifiers | none: the SDK signs a fixed set of fields | kept in mash: these are mash's extra signed headers |
| `IdentityFromCreds` | `identity.SignerFromCreds`, `IdentityFromJWT` | adopted in S4, with mash's credential rules on top |

## `internal/endpoint`

| mash | SDK v0.1.0 | State |
|---|---|---|
| `StartService`: the micro service and its registration metadata (`service.go`) | `host.New` | waits on Synadia: the SDK host would advertise the group as `owner`, and extra metadata cannot override it |
| the `prompt` endpoint: chunks, terminator, error headers | `host.Responder`, `Reject` and the chunk types | waits on the host, above |
| `MaxPromptBytes` and the advertised `max_payload` | `WithMaxPayload`, which the host enforces | waits on #1536: mash advertises 1MB and refuses over 32 KiB |
| the heartbeat, with `endpoints.mail` | the host's heartbeat with `WithHeartbeatExtras` | waits on the host, above |
| the `mail` endpoint in the registration | `WithExtraMetadata` or `host.WithEndpoint` | kept in mash: mail is mash's verb, and an extra endpoint is outside the SDK's gate, so mash verifies it itself, as today |
| `Discover`, `DiscoverRecipient` | `caller.Discover`, `Find`, `WithInboxPrefix` | adopted in S5, live at W. The caller accepts only a verified registration of the right endpoint |
| `Prompt` and its reply inbox | `caller.Agent.Prompt`, `Stream.Events` | adopted in S5, live at W. The reply subject takes the SDK's layout, `<prefix>.<client-id>.<route-token>`, with a route token of at least 128 bits |
| presence: the `$SRV.INFO.agents` probe and the `id_sig` check | `caller.ListAgents` with `WithRequireVerifiedIdentityFunc` | adopted in S5, live at W |
| `mail.go`: the `Mash-*` headers, classification, the send nonce | `caller.PublishSigned` with `WithNonce` and `WithHeaders` | kept in mash: the SDK does not sign extra headers; mash signs them as today |
| the directory, leases, hooks, refusals, relay, spool | none | kept in mash: no SAP equivalent |

## Other packages

| mash | SDK v0.1.0 | State |
|---|---|---|
| `internal/issue`, `internal/policyview`: their own micro services | `identity` only | kept in mash, except the live check: `policyview` takes S4's nonce-set interface at W |
| `internal/admin/permissions.go`, `internal/listset` | none | kept in mash: they are pinned to mash's subject grammar by goldens |
| `internal/store`: the inbox stream, the mail filter, the stored record | none | kept in mash: no SAP equivalent |
| `internal/natsauth`, `internal/spool`, `internal/mcp`, `internal/host` | none (`connect` exists, but mash has its own credential rules) | kept in mash |

## What the SDK is not changed for

mash-only pieces stay in mash. The SDK is not changed to carry the `mail` verb, mash's signed headers, its trust labels or its token rule. A bug found in the SDK is fixed in the SDK, by an issue, a reviewed change and a tag, and mash then moves its pin by a deliberate bump. mash does not work round an SDK bug.
