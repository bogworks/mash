# mash against the SAP Go SDK

This page names each mash package and API against its equivalent in the SAP Go SDK (`github.com/bogworks/synadia-agents.go`, v0.1.1 is the version checked here), says what mash does about it, and says where mash differs from plain SAP and why. It implements REQ-002 of the SDK adoption contract and is checked by inspection.

Every row's status is one of four forms: `adopted`, `kept in mash (why)`, `kept in mash: SAP departure` (mash differs from the SDK on purpose, and the row names its exit) or `waits (on what)`.

## Reading this page

The SDK is a dependency of `mash` and `mashctl` from v0.5.10: both build addresses and subjects through it (`internal/sap`, stage S3). Nothing else on this page is in a released mash. Where a row says `adopted`, the change lands in the stage named in the note and is switched off until stage W (wiring and views). The stage-4 and stage-5 code is guarded out of 0.5.x binaries. Do not read an `adopted` row as a behaviour a released mash has today.

| Stage | What it is |
| --- | --- |
| S1 | The SDK pin in `go.mod`. On `main`, first as a test-only import; in releases from v0.5.10. |
| S2 | The compatibility gate. Tests only. |
| S3 | Constants and subjects built through the SDK's subject constructor. |
| S4 | The identity adapter in `internal/sapsdk`: signing, id signatures, stored and live verification, sender classification, the nonce set. |
| C | `release-check --compat`. |
| S5 | Under amendment 5 of the SDK adoption contract, REQ-016's checks and the count of answers that fail them (TO-12) in mash's own discovery, presence and prompt sender. The verify step is `internal/sapsdk/discovery`, guarded out of 0.5.x binaries; in 0.5.x discovery is main's own, and the shipped `mash` turns the checks on at startup (`cmd/mash/s5wire.go`). No SDK caller is built, and the reply subject stays mash's own. The four rows marked `kept in mash: SAP departure` below say what would have to change for the SDK caller to replace them. |
| W | Wiring and views: the refused-malformed record, the scan, and the inbox, read, status, doctor, mashctl and MCP views. |
| S6 | The SDK host. Waits on Synadia. mash will not wrap, patch or copy it. |
| M | Mash-only. No stage; there is nothing to adopt. |

A symbol or behaviour that could not be confirmed in the source is written `unverified`, not guessed.

## Identity and signing

| mash package or API | SDK equivalent | status | note |
| --- | --- | --- | --- |
| `internal/sap` key generation and seed handling for the endpoint key | `identity.SignerFromSeed` (package `identity`) | adopted | S4. `internal/sapsdk` wraps the SDK signer; the seed file format and the key's place on disk are unchanged. Switched off until W. |
| `sap.Sign`: sign the `Agent-Sender` header on a live prompt (`service.go`, prompt path) | `identity.SignHeader` | adopted | S4, through `sapsdk.Sign`. Switched off until W. |
| `sap.IDSig` / the `id_sig` in the directory record and the heartbeat (`service.go`) | `identity.SignAgentID` | adopted | S4, through `sapsdk.IDSig`. Switched off until W. |
| `sap.VerifyIDSig` (directory load, `directory.go`) | `identity.VerifyAgentID` | adopted | S4, through `sapsdk.VerifyIDSig`. Switched off until W. |
| Heartbeat signature (`service.go`) | `identity.SignHeader` over the heartbeat subject | adopted | S4. Same adapter as the prompt signature. Switched off until W. |
| Mail verification: the stored `Agent-Sender` header (`sap.VerifyStored`, ADR 0017) and the signed `Mash-*` marks (`Mash-Sender-Instance`, `Mash-Urgent`, `Mash-In-Reply-To`, `Mash-Via`; `sender.go`) | `Agent-Sender` verification through the SDK (S4); the `Mash-*` marks: none | `Agent-Sender`: adopted (S4, switched off until W); `Mash-*` marks: kept in mash (mail is not a SAP verb) | See the mail model below. The signing key is the same endpoint key; only the signed bytes differ. |
| `internal/natsauth` credentials, nonce-oracle guard | none; the SDK's `connect` package builds a connection, it does not carry mash's account and permission model | kept in mash (the server side of the auth callout is mash's, not the protocol's) | The SDK signer does not refuse a server-supplied nonce: it signs what it is given (`identity/signer.go`, v0.1.1), so a guard moved onto it would be lost. |

## Sender header: parse and classify

| mash package or API | SDK equivalent | status | note |
| --- | --- | --- | --- |
| `sap.ParseSender`, the old lenient `Agent-Sender` parse (`sender.go`) | the SDK's strict `Agent-Sender` parser, reached through `identity` | adopted | S4, per ADR 0173: the strict parser is adopted outright. Ten shapes mash used to read now class as malformed; X1 to X3 below are the three accepted differences. Switched off until W. |
| `sap.Trust` and the label set verified / claimed / unknown-signer / absent / stale | `identity.Trust` and `Classify` (verified, claimed, unverified-class names as the SDK spells them; the exact SDK strings are frozen by ADR 0173 and the contract) | adopted for the SDK classes; kept in mash for `unknown-signer` and `stale` | S4 adopts `sapsdk.Classify`. The two extra labels are mash's: see the mail model. |
| `Endpoint.classify` (`service.go`) | `Classify` | adopted | S4 then W. The endpoint calls the adapter instead of its own logic. Switched off until W. |
| `refused-malformed.seqs` record of live prompts refused as malformed | none | kept in mash (the SDK refuses; it does not keep a record) | Written and read at W. |
| Frozen class strings | the SDK's class strings | adopted | The strings are frozen so that views and tests do not move when the SDK is bumped. |

## Live verify and nonces

| mash package or API | SDK equivalent | status | note |
| --- | --- | --- | --- |
| `Endpoint.verifyLive` and the policy views (`service.go`, `policyview.go`) | `identity.Gate` with `AdmitPrompt` and `PreAccept` | adopted | S4, as `sapsdk.VerifyLive(values, subject, payload, now, nonces, listed)`, returning an outcome, an HTTP-style status and a reason. Switched off until W. |
| Live reply statuses | the SDK gate's outcomes | adopted | 400 for a malformed header, 401 for the nonce cap (with its own text), 403 for a sender not listed. |
| Nonce store (mash's own set) | the SDK's nonce set | adopted, behind a mash interface | S4. mash keeps its own `NonceSet` interface (fresh, seen, full) so that the store is mash's and the SDK's caps apply: 100,000 entries overall and 10,000 per sender. |
| Stored-mail verification (`client.go`, `mail.go`, `admin/inbox_inspect.go`) | `VerifyStored` | adopted | S4, through `sapsdk.VerifyStored`. The stale label is applied by mash after the SDK verifies: see the mail model. Switched off until W. |
| `release-check --compat` | the SDK's `conformance` package and `cmd/sapconform` | adopted | Stage C runs the compatibility checks against a candidate. mash does not import `conformance` or `agentstest` into a shipped binary. |

## Addresses and subjects

| mash package or API | SDK equivalent | status | note |
| --- | --- | --- | --- |
| `internal/sap/subjects.go` subject builders for `agents.<verb>.<owner>.<group>.<instance>` | `NewAgentSubject("claude-code", group, instance, WithSubjectToken(owner))` | adopted | S3. mash's three tokens fill the SDK's owner and name slots, so the SDK's owner argument is mash's group and `WithSubjectToken` carries mash's owner. See the address section below. |
| SAP constants (protocol name, verbs, metadata keys) | the root `agents` package constants | adopted | S3. |
| Token rule in `internal/sap/address.go` | `ValidateToken` (the SDK's, in `tokens.go`) | kept in mash (stricter) | mash rejects more than the SDK does. Every mash-valid token is SDK-valid; the reverse is false. |
| Flat key `owner_group_instance` | none | kept in mash (storage key) | ADR 0074. The key names the JetStream and KV entries. The SDK has no storage key. |
| Metadata `agent=claude-code` | the SDK's agent-type field | adopted | S3. |

## Directory and records

| mash package or API | SDK equivalent | status | note |
| --- | --- | --- | --- |
| `internal/endpoint/directory.go` directory KV and its `id_sig` check | the SDK's `AgentID` record and `VerifyAgentID`; the SDK has no directory KV | adopted for the signature; kept in mash for the KV (the SDK discovers by `$SRV` probe, not a stored directory) | Signature check lands in S4. The directory bucket, its poll and its jitter (ADR 0065) stay in mash. |
| `internal/store` records (`store.Record`) | none | kept in mash (the inbox is mash's feature) | |
| Presence by `$SRV.INFO` probe (`presence.go`, ADR 0066) | the SDK's discovery probe in `caller` | kept in mash: SAP departure | mash probes on demand, to answer `who`, and, once the S5 switch is on (W), checks each answer in `applyPresence` (REQ-016: the `id_sig` verifies, and the user key, account and owner are the directory's for the address in the prompt subject), counting each answer that fails. In 0.5.x it is main's check (the `id_sig` verifies and the user key is the directory's), with nothing counted. The SDK's probe pieces are unexported; the public route is `ListAgents`, which starts a heartbeat tracker (a standing subscription until the client closes) on `agents.hb.*.*.*`, a subject mash does not grant (see the heartbeat scope row). Exit: the heartbeat scope row. |
| Endpoint metadata fields `instance`, `session`, heartbeat `instance_id` | the SDK's `AgentInfo` | kept in mash (mash's names) | See the endpoint fields note below. |

## Caller and prompt

| mash package or API | SDK equivalent | status | note |
| --- | --- | --- | --- |
| `internal/endpoint/client.go` live prompt caller (discovery in `service.go`, the prompt sender) | `caller` (package `caller`: discover, prompt) | kept in mash: SAP departure | Amendment 5 (A): discovery, presence and the prompt sender stay in mash. S5 adds REQ-016's checks and the TO-12 count to them; no second caller is built. The four rows below are the gaps that keep the SDK caller out, each with its exit. |
| Verified-identity requirement on the target (`Verify` in `internal/sapsdk/discovery`, called through the S5 switch in `internal/endpoint/s5hook.go`) | `caller.WithRequireVerifiedIdentityFunc` or `caller.TrustPolicy.Accept` | kept in mash (one verify step, called by presence and by the recipient discovery of `prompt`, behind the 0.5.x guard) | The `id_sig` verifies, and the prompt subject's address has a directory record, the user key is that address's, and the account and `metadata.owner` are the record's. An answer that fails is counted once (`Endpoint.AnswersNotVerified`) and never attributed to the endpoint it names. Nothing is printed until W. In 0.5.x the step is not linked: presence accepts an answer whose `id_sig` verifies and whose user key is the directory's (as on main), `prompt` accepts the first answer that names the subject, and nothing is counted. |
| Reply inbox `_INBOX.<owner>.<flat>.<token>` (`secretInbox`, `service.go`) | `caller.WithInboxPrefix` (`_INBOX.<owner>.<flat>.<client-id>.<route>`) | kept in mash (no change) | One token of 128 bits (16 bytes, 32 hex characters) from `crypto/rand`, fresh for each request, under the owner-scoped prefix the permissions allow. The presence probe uses it too once the S5 switch is on (in 0.5.x it uses the client's `NewInbox`, as on main). The SDK layout is not adopted, so the reply-subject change of the contract fell away. |
| The owner slot: mash puts the owner in the third subject token | `CheckSubjectConsistency`, which drops a record whose fourth token is not `metadata.owner` | kept in mash: SAP departure | Every mash registration would be dropped by the SDK caller. SAP 0.3 says the token should match the owner (line 58) and also that it matches the fourth token (line 165). Exit: Synadia's answer on whether a caller may drop a record whose subject tokens differ from its metadata. |
| The heartbeat scope: mash grants an endpoint its own owner's heartbeats only | the SDK's discovery subscribes to `agents.hb.*.*.*` | kept in mash: SAP departure | The `admin/permissions` golden cannot change here. Exit: an SDK way to subscribe to a narrower heartbeat subject. |
| The prompt body: mash sends the bare text | the SDK sends `{"prompt":"..."}` | kept in mash: SAP departure | Both are valid SAP (0.3 section 5.1 allows the text as shorthand), and mash's bytes stay as they are. Exit: plain text or JSON is a wire choice for the maintainers. |
| The 32768-byte prompt boundary (`MaxPromptBytes`) | the SDK counts the headers toward `max_payload`, as SAP does (section 5.4) | kept in mash: SAP departure | The SDK refuses a prompt of exactly 32768 bytes that mash accepts (#1536). Exit: the boundary, listed as a difference in AT-008. |
| `reply` MCP tool and the 10-minute prompt timeout, 30-second keepalive (`service.go`) | the SDK caller's prompt stream | kept in mash (the caller side stays mash's own under amendment 5; the endpoint side is the host) | The endpoint side is the host: see below. |

## Host and serve

| mash package or API | SDK equivalent | status | note |
| --- | --- | --- | --- |
| `mash serve`, `internal/endpoint/service.go` (the micro service, heartbeat, prompt handling) | `host` package | waits (S6, on Synadia) | mash does not wrap, patch or copy the SDK host. It serves through `nats.go/micro` itself. |
| `MaxPromptBytes` and the channel notice limit | none | kept in mash | A longer prompt is refused before it is acknowledged; longer text goes as mail. |
| Heartbeat timer (30 s) | the host's heartbeat | waits (S6) | |
| Endpoint version string rule (`0.0.0-dev` default) | none | kept in mash | `micro.AddService` refuses non-SemVer, so mash's unstamped build reports a valid one. |

## Anything with no SDK equivalent

| mash package or API | SDK equivalent | status | note |
| --- | --- | --- | --- |
| The `mail` verb and the JetStream inbox (`mail.go`, `internal/store`) | none | kept in mash (this is what mash is) | See the mail model below. |
| Inbox views: `inbox`, `read`, `status`, `doctor`, `who`, `send`, `version` | none | kept in mash | MCP tools in `spec/mash-mcp.json`. W changes how they show sender trust; the tools stay. |
| `mashctl` administration | none | kept in mash | Standalone, no NATS client in the display path. |
| `internal/natsauth` and the permission templates (`admin/permissions.go`, ADR 0102) | none | kept in mash | The SDK does not model accounts or permissions. |
| Policy packages (`internal/policy*`) | none | kept in mash | The 0.6.0 policy engine. Switched off, and a release candidate that links one is refused by `releasecheck`. |
| SDK `tools`, `connect`, `agentstest`, `conformance` | n/a | not used by shipped mash | `conformance` and `agentstest` serve stage C and the tests only. `tools` and `connect` are not used (S5 builds no SDK caller). |

## Where mash differs from plain SAP, and why

### The address layout

A SAP agent subject is `agents.<verb>.<owner>.<agent-type>.<name>` in the SDK's spelling, with the agent type in the metadata as well. mash uses three tokens, `owner.group.instance`, and puts them where SAP puts its owner and name tokens: the wire subject is `agents.<verb>.<owner>.<group>.<instance>`. The metadata field `agent` carries `claude-code`. This follows ADR 0145 and ADR 0074. The reason is that mash names a person's session by who owns it, which fleet it is in and which session it is, and SAP's single name slot cannot hold that. At S3 mash builds the subject with `NewAgentSubject("claude-code", group, instance, WithSubjectToken(owner))`, so the SDK's owner argument holds mash's group and the subject-token option carries mash's owner. This is an adapter detail, not a protocol change.

### The token rule and the flat key

mash accepts a narrower token than the SDK's `ValidateToken`. Every mash token passes the SDK's check, so the SDK builds any subject mash does. mash keys storage on the flat form `owner_group_instance` (ADR 0074), which is why a token may not contain the underscore ambiguity that would make two addresses collide.

### Endpoint fields

What mash emits is: the metadata `instance` (the third address token), `session` (the flat key), and, in the heartbeat, `instance_id` (the micro service id).

### The `Agent-Sender` header

mash signs `Agent-Sender` on live prompts as SAP requires, with the endpoint's own key. mash adds nothing to the header's meaning. The differences are in what mash does with the result: mash maps the verification outcome to its label set (verified, claimed, unknown-signer, absent, stale) and shows that label to the model with the message. The `unknown-signer` label is for a sender that signs correctly but whom this endpoint does not know. The `stale` label is for a message signed by the named endpoint long before it was stored, which is what a replay looks like. Neither exists in the SDK; both are applied by mash after the SDK has verified. The SDK's strictness replaces mash's old lenient parse (the ten shapes below).

### The inbox and mail model

SAP has live prompts: the caller sends, the agent answers or does not. mash adds a `mail` verb. A message is stored in the receiver's JetStream inbox, so it survives the receiver being offline, and the receiver reads it by sequence number. Mail carries `Mash-*` headers signed by the sender's endpoint key, and the receiving side verifies them when the mail is stored and again when it is read (`VerifyStored`). The directory is a KV bucket that holds each endpoint's record with its `id_sig`; the SDK discovers by probe and has no stored directory. Presence is answered by a `$SRV.INFO` probe made when someone asks `who` (ADR 0066), not by listening to every heartbeat. mash's trust label is shown quoted on every message, and the model is told that only `[verified]` is delegated.

### The reply inbox prefix and the nonce oracle

mash's reply inbox prefix is `_INBOX.<owner>.<flat>`, followed by a 128-bit random token from the system's random source, so that account permissions can limit what each endpoint may subscribe to. The layout does not change at S5. `natsauth` guards against the nonce oracle, a way to get an endpoint to sign a server-chosen nonce. That guard stays in mash.

### The ten refused shapes and X1 to X3

ADR 0173 adopts the SDK's strict `Agent-Sender` parser outright. Ten shapes that mash read before are malformed from S4, switched off until W. The ten are listed in ADR 0173 (`docs/decisions/0173-mash-adopts-the-sap-sdks-strict-agent-sender-parser-outright-and-ten-shapes-it-read-before-are-now-malformed.md`, on branch `pr/core/sdk-identity-s4` until it merges); this page does not restate them, so the ADR is the one place they are kept. Three differences go the other way, in that the SDK accepts or classifies something differently from mash:

- X1: a `v` spelled `1.0` or `1e0` now verifies. mash refused it.
- X2: invalid UTF-8, or a bad `sub`, is classed malformed. mash classed it by how far parsing got.
- X3: an unsigned claim with a bad `ts`, `nonce` or `sub` is classed malformed. mash called it a claim.

A live reply gives 400 for a malformed header, 401 for the nonce cap with its own text, and 403 for a sender that is not listed.

### What stays out

The SDK host (S6) is not adopted: mash serves itself through `nats.go/micro` until Synadia ships what mash needs, and mash will not wrap, patch or copy it. The 0.6.0 policy engine is mash's, has no SDK equivalent, lands as code that is switched off, and is not reachable in a 0.5.x release.
