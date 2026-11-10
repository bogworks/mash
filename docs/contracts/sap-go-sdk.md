# Work contract: the Synadia Agents Protocol Go SDK

| | |
|---|---|
| Standard | contract@0.1.0 |
| Contract id and revision | sap-go-sdk, revision draft-7 (draft-2 to draft-5 answered four critic passes and mash-lead's review; draft-6 recorded Randy's answers; draft-7 renames the repository and prepares the upstream landing on his draft-6 comments; Appendix A) |
| Status | DRAFT, for Randy to ratify. Not READY: no work starts under it until it is. |
| Team | The mash team's SDK workstream, endpoint `mash-sdk`, builds it (Randy, board note on card #129, 2026-11-09) |
| Board card | #129 mash-sap-go-sdk (p1); related #13 mash-sap-conformance (parked), #103 mash-sap-security-overlay (backlog) |
| Writer | mash-sdk, delegated by mash-lead, who writes the team's contracts; model family Anthropic |
| Critic | OpenAI (Codex CLI 0.166.0, gpt-6-sol, daybreak on); findings and dispositions in Appendix A |
| Ratified by | not yet |
| Charters and seam pages cited | none exist for the SDK, and none are needed: the SDK's only boundary is the SAP specification |
| Date | 2026-11-10 |
| Sources | Card #129 (`cards/mash-sap-go-sdk.md`, its log of 2026-11-10); SAP 0.3.0 `core-protocol.md` in `synadia-ai/synadia-agent-sdk-docs` at `c48cea5f07965cc8fd4054e120459f1833b3ed2b` (cited `SAP:Lnnn`); `synadia-ai/synadia-agents` at `ed9aefb` (the TypeScript and Python SDKs, `test-fixtures/`, `docs/protocol-mapping.md`); `henomis/phero` v2.3.0 `nats/`; `mepuka/effect-nats-jetstream` `docs/conformance/protocol-requirements.json`; `nats-io/nats.go` `CONTRIBUTING.md`; `synadia-io/orbit.go` `CONTRIBUTING.md`; bogworks/mash `internal/sap/` and ADRs 0003, 0004, 0005, 0017 (evidence only) |

## 1. Intent

### Problem

SAP, the Synadia Agents Protocol, has official SDKs in TypeScript and Python only. No Go SDK exists. mash speaks SAP
through its own `internal/sap` code, mixed with mash's extensions, so it is neither reusable nor checked against the
protocol as a whole. Randy: "before final mash release, we need to build the SAP Go SDK with robust testing, PR it to
Synadia, and rebuild mash around it." (card #129, 2026-11-08)

### Desired outcome

A public Go SDK for SAP 0.3 that implements the protocol and nothing else, modeled on Synadia's TypeScript and Python
SDKs. It has a caller side and a host side, the optional sender identity extension (SAP §13), and a conformance suite
that ties every requirement of the spec to a test and that others can run. It is offered to Synadia, and mash can
consume it. Randy, on the scope: "Existing Synadia Agent Protocol SDKs that it can work from as a model." "But not Go."
(card #129 log, 2026-11-10)

### Motivation

It gates the final mash release (card #129: "a gate on the final mash release"). It is also what mash shows Synadia
(card mash-show-synadia), and it gives mash the SAP conformance evidence card #13 asked for.

### Success measures

- **Go programs can speak SAP.** A Go program can discover SAP agents, prompt them and read their streamed replies,
  and can host an SAP agent that the official TypeScript and Python callers can use. The same holds the other way
  round.
- **The conformance catalog is complete.** Every MUST in SAP 0.3 maps to a passing Go test or to a stated reason it
  does not apply, and CI fails when a MUST has neither.
- **The identity vectors pass.** The Go SDK passes Synadia's language-neutral identity vectors.
- **The offer to Synadia is made.** An issue, and then a pull request, offering the SDK to Synadia are open.
- **mash can build on it.** mash could be rebuilt on the SDK's public API without forking it (AT-010).

### Non-goals

- A mash SDK. Nothing of mash goes in it: no `mail` verb, no zones, no mash signatures (`SignInstance`, `SignUrgent`,
  `SignInReplyTo`, `SignVia`), no mash token rule, no JetStream inboxes.
- Rebuilding mash on the SDK. That is a follow-on contract, and card #129's done-when needs it: "mash's main branch
  uses the SDK". This contract only requires that mash can (REQ-016). The follow-on is tracked as Q11.
- Changing SAP. The SDK implements 0.3 as written; spec defects go to Synadia as issues.
- The deferred parts of SAP 0.3: end-to-end encryption, the `attachments` upload endpoint, and JetStream streaming
  (SAP:L17-L22).
- LLM tool wrappers like the TypeScript `AgentTools`, and the Agent Fabric layer.
- Policy. The SDK offers admission hooks and decides no one's access (card #103 is mash's).

## 2. Actors and journeys

### Actors

- **ACT-001, Randy.** Decision owner and ratifier. He owns the relationship with Synadia and signs what needs his
  legal name.
- **ACT-002, the mash team (`mash-lead`, with `mash-sdk` doing the work).** Writes and tests the SDK under this
  contract.
- **ACT-003, Synadia's SAP maintainers.** `CODEOWNERS` of `synadia-ai/synadia-agents` names
  `@synadia-io/nats-ai-sdk-maintainers`. They decide whether to take the SDK.
- **ACT-004, Go developers.** They write SAP callers or agents in Go.
- **ACT-005, mash.** A later consumer, through the SDK's public API only.

### Primary journeys

- **JRN-001, caller.** A Go program connects to NATS and discovers agents. It prompts one with text and attachments,
  reads ack, response and query chunks until the terminator, answers a query, and gets typed errors.
- **JRN-002, host.** A Go program registers an agent with its agent, owner and name tokens. It serves prompts and
  status, publishes heartbeats, and is found and prompted by the TypeScript and Python callers.
- **JRN-003, conformance.** A developer of an SAP agent runs the SDK's conformance runner against it over NATS. They
  get a report listing every host obligation as checked, needing configuration, or not checkable, with the reason
  (REQ-013).
- **JRN-004, upstream.** Randy opens an issue on `synadia-ai/synadia-agents` offering the SDK. Then a pull request
  follows, in whatever form Synadia asks for.

### Exceptional journeys

- **JRN-005, a malformed peer.** The peer sends a malformed envelope, bad base64, an oversize payload, a missing
  terminator or an unknown chunk type. The SDK answers as the spec says: 400, the error stream, ignore, or time out.
  It never hangs and never crashes.
- **JRN-006, a bad signature.** A signed request fails §13 verification. The host refuses it with 401 or 403 as the
  spec says, and an admission hook can refuse a verified sender.
- **JRN-007, Synadia declines or asks for changes.** The SDK stays public under bogworks. Changes Synadia asks for are
  made in the pull request. A decline is a deviation that Randy accepts or not (REQ-017), and the contract is met only
  if he accepts it.

## 3. Behavioral requirements

Source classes: Confirmed (Randy ruled it), Derived (from a cited document), Proposed (the writer's; open to the critic
and to Randy).

**How wire rules are cited.** The spec is the authority for wire behaviour. A requirement below cites the spec's section
and lines and names the SDK's outcome. It does not restate the rule, and it never strengthens a SAP SHOULD or MAY into a
MUST. Where the SDK makes a choice the spec leaves open, the requirement marks it as **SDK policy (Proposed)**. Every
spec obligation is enumerated atomically in the conformance catalog (REQ-010), not in this page.

### REQ-001: The protocol only, nothing of mash
- Source: Confirmed. "it should just be building that not a mash SDK" (Randy, card #129 log, 2026-11-10).
- MUST: the SDK implements SAP 0.3 and its optional §13 extension, and no other protocol.
- MUST NOT: mash's `mail` verb, zone token, signature variants, token rule, directory or JetStream inboxes appear in
  the SDK's code, API or docs.
- Acceptance: AT-009.

### REQ-002: One pinned protocol version
- Source: Derived. SAP §11 (SAP:L637-L659). Callers MUST compare only the MAJOR.MINOR prefix (SAP:L643), and "callers
  SHOULD pin to an exact MAJOR.MINOR until 1.0" (SAP:L651).
- MUST: the SDK targets SAP 0.3.0 at `c48cea5f`, and the host declares `protocol_version` `0.3`. The caller compares
  only the MAJOR.MINOR prefix (spec MUST).
- SDK policy (Proposed), following the SHOULD at SAP:L651: by default, the caller refuses an agent whose MAJOR.MINOR is
  not exactly `0.3`, with a typed error. The program can relax this to §11.2's same-MAJOR rule, and from 1.0 the
  default becomes same-MAJOR.
- MUST: the spec revision is pinned by commit and by the SHA-256 of `core-protocol.md`.
- MUST: moving to a later spec revision is a deliberate change that re-runs the catalog extraction (REQ-010).
- Acceptance: AT-001, AT-002.

### REQ-003: Caller
- Source: Derived. SAP §4 to §7 and §9; the TypeScript `Agents` and `Agent` API (`client-sdk/typescript/src/index.ts`).
- **Discovery.** The caller MUST discover agents through `$SRV.PING.agents` and `$SRV.INFO.agents` (§4).
- **Prompt subject.** It MUST publish a prompt to the subject from the discovery record and MUST NOT construct one
  from identity (SAP:L76).
- **Payload.** It MUST send plain text or the JSON envelope with attachments (§5).
- **Local checks.** It MUST enforce `max_payload` and `attachments_ok` locally (§5.4).
- **Reply stream.** It MUST stream reply chunks in order (`response`, `status`, `query`), ignore unknown chunk types
  and status values, and end at the zero-byte terminator (§6). It MUST accept both the string and object forms of a
  `response` chunk's `data` (SAP:L364).
- **Queries.** It MUST let the program answer a mid-stream query exactly once (§7).
- **Timeout.** It MUST apply a per-stream inactivity timeout (SAP:L402). The default of 60 s is the spec's
  recommendation, adopted as SDK policy (Proposed). It MUST support cancellation by
  dropping the subscription (SAP:L413).
- **Errors.** It MUST inspect error headers on every message before the body (§9). When the error body is empty or
  not valid JSON, it MUST fall back to the `Nats-Service-Error` header as the description (SAP:L593).
- **Context.** Every blocking call takes a `context.Context`.
- Acceptance: AT-001, AT-003, AT-004.

### REQ-004: Host
- Source: Derived. SAP §3, §5, §6, §8.7 and §9; the TypeScript `AgentService` (`agent-sdk/typescript/README.md`).
- **Registration.** The host MUST register a NATS micro service named `agents` with `version`, `description` and the
  metadata of §3.2 (`agent`, `owner`, `protocol_version`, `session` when session-aware). It MUST register the `prompt`
  and `status` endpoints, both in queue group `agents`, set explicitly (SAP:L175, L547). It MUST support the endpoint
  metadata `max_payload` and `attachments_ok` (§2.1).
- **Reply stream.** On a prompt it MUST send exactly one `ack` status chunk first (SAP:L380), then the program's
  chunks, then the zero-byte, header-less terminator (SAP:L388). It sends nothing after the terminator.
- **Request errors.** It MUST answer malformed requests with 400 as §5 and §9 list them. It MUST tolerate and preserve
  unknown envelope fields (SAP:L307). Refusing an oversized payload on the host is a MAY (SAP:L293). The SDK does so
  by default with 400 (SDK policy, Proposed), and that is tested apart from the caller's local MUST (§5.4).
- **Status.** It MUST answer `status` with a freshly built heartbeat-shaped payload, or 500 if building it fails
  (§8.7). It MUST ignore the reserved request body: a non-empty body is not an error (SAP:L548).
- **Error codes.** It MUST use only the error codes in SAP §9's table (SAP:L607).
- **Extra endpoints.** It MUST let the program add extra micro endpoints without the SDK knowing what they are. This
  is the TypeScript `extraEndpoints` equivalent (REQ-016).
- Acceptance: AT-001, AT-003, AT-004.

### REQ-005: Heartbeat
- Source: Derived. SAP §8 (SAP:L462-L537).
- **Spec obligations (cited, enumerated in the catalog).** The host publishes heartbeats on the fixed subject with
  the §8.3 required fields (SAP:L484-L519). Optional fields
  (`protocol_version`, `endpoints`), when declared, follow §8.3. A receiver ignores a malformed optional field and keeps
  the heartbeat (SAP:L517).
- **SDK policy (Proposed), from the spec's recommendations, not its MUSTs:**
  - The default interval is 30 s (SAP:L478).
  - An interval under 1 s is allowed only with an explicit opt-in. SAP says SHOULD NOT on shared infrastructure; it
    does not forbid it.
  - The host starts heartbeats after registration (a SHOULD, SAP:L482).
  - The host declares `protocol_version` and `endpoints` by default.
  - The tracker's offline threshold defaults to 3 × `interval_s` (a recommendation, SAP:L480), and the program can
    configure it.
  - The tracker subscribes to the heartbeat wildcard before its first PING (a SHOULD, §8.5, SAP:L533).
- MUST: the SDK provides a heartbeat tracker.
- Acceptance: AT-001, AT-003.

### REQ-006: Attachments
- Source: Derived. SAP §5.2 (SAP:L263-L274).
- MUST: attachments are `{filename, content}`, with content in standard-alphabet padded base64 and no whitespace.
- MUST: the SDK encodes attachments that way and refuses anything else as 400 on the host side and as a typed error on
  the caller side.
- Acceptance: AT-001, AT-004.

### REQ-007: Errors
- Source: Derived. SAP §9 (SAP:L566-L613); the typed errors of the TypeScript SDK.
- MUST: errors are typed Go values, usable with `errors.Is` and `errors.As`, carrying the code, the header text and the
  optional JSON body (`error`, `message` and extra fields).
- MUST: a caller can tell a local refusal (payload too large, attachments not supported, empty prompt, query already
  answered, version mismatch) from a remote error.
- Acceptance: AT-001, AT-004.

### REQ-008: Sender identity (§13), optional in SAP and in scope here
- Source: Derived from SAP §13 (SAP:L717-L1072); in scope: Confirmed, "Signing is in scope. We need it." (Randy,
  2026-11-10; Q3).
- **Signing.** When given a seed, the SDK MUST sign heartbeats, and both `prompt` and `status` requests, with
  `Agent-Sender` (§13.5; caller checklist SAP:L709). It MUST NOT sign a reply, `status` replies included (SAP:L1052).
- **Payload limit.** The caller MUST count the `Agent-Sender` header bytes in its local `max_payload` check
  (SAP:L887).
- **Verifying.** It MUST verify live messages (§13.7, SAP:L927-L971: 30 s window, nonce replay set). It MUST verify
  stored messages for authorship only (§13.8, SAP:L973-L980: signature and `sub`, no time window and no nonce set,
  redelivery accepted, deduplication on `(user, nonce)` left to the consumer).
- **Own identity (§13.4, SAP:L832-L858).** When the connection's identity is unknown (no reply to
  `$SYS.REQ.USER.INFO`, or a permissions error), the SDK MUST NOT guess and MUST NOT keep the failure for the
  connection's lifetime: it retries after a short interval (SAP:L851). A connection with no or unknown identity sends
  no `Agent-Sender` header, and fails locally against a `signed` endpoint.
  It MUST classify senders as verified, claimed or absent.
- **Declaring the requirement (§13.9, SAP:L982-L1003).** A host with §13 configured MUST declare `min_sender_trust`
  in its `prompt` endpoint metadata and nothing on `status`. A caller MUST read the declaration from discovery. It MUST
  fail locally, without publishing, when the endpoint requires `signed` and the caller cannot sign. It MUST treat an
  unknown value as `signed` (SAP:L1001).
- **Registration (§13.10, SAP:L1005-L1037).** The host MUST publish `user_nkey`, `account` and `id_sig` in
  registration.
- **Status is always answerable.** The host MUST classify a `status` request's sender and MUST NOT refuse it on
  identity grounds: a failed classification is logged and the reply is sent (SAP:L1003; agent checklist SAP:L688).
- **Admission hook.** It MUST offer an admission hook that can refuse a verified sender with 403. It decides no policy
  itself.
- **Interop.** A peer without §13 MUST interoperate unchanged (SAP:L1061-L1070).
- **Keys.** The program supplies the signing key as an NKEY seed or a NATS `.creds` file. The SDK neither reads key
  material from fixed paths nor writes it anywhere. To rotate a key, the program restarts the instance with the new
  key, which the SDK documents. Re-registration publishes the new `user_nkey` and `id_sig` (§13.10). There is no live
  rotation in 0.x (SDK policy, Proposed).
- **Replay set.** The live-verification replay set is bounded. It keeps each `(user, nonce)` only for the 30 s window
  plus clock skew, and it has a configurable maximum size. When the set is full it refuses new messages as replays
  (fail closed) and does not drop old entries, and it logs that it did so (SDK policy, Proposed).
- **Nothing extra.** It MUST carry no signature form beyond §13.
- Acceptance: AT-005.

### REQ-009: Tokens as SAP defines them
- Source: Derived. SAP §2 (SAP:L45-L134), §2.2 (SAP:L94-L102) and §3.2.
- MUST: the program sets the agent, owner and name tokens of the subject per service instance (SAP §2, §3.2:
  `metadata.agent`, `metadata.owner`, `name` in the subject). The SDK enforces SAP's MUSTs on them: NATS subject rules,
  and no leading `$` (SAP:L98).
- MUST: the SDK reports SAP's SHOULDs (characters `a-z 0-9 - _`, 1 to 63 characters, subject under 256) as warnings
  or an opt-in strict mode, not as refusals.
- MUST NOT: it carries any stricter token rule of its own beyond SAP §2.2.
- Acceptance: AT-001.

### REQ-010: A conformance catalog that ties every requirement to a test
- Source: Proposed, from the method of `mepuka/effect-nats-jetstream` `docs/conformance/protocol-requirements.json`
  (169 requirements, each with section, line and tests) and card #13's "SAP v0.3 conformance table".
- MUST: a machine-readable catalog lists every MUST, MUST NOT, SHOULD, SHOULD NOT and MAY in `core-protocol.md` at the
  pinned revision, split into **atomic obligations**: one testable behaviour each. A sentence with two obligations is
  two entries. Each entry has its id, section, line, a short paraphrase (not the spec's text), role (caller, host or
  both), strength, and either the tests that cover it or a reason it does not apply.
- MUST: each covering test names the obligation ids it asserts (for example in a test comment or table), and the check
  verifies that link both ways. A test that names no obligation does not count, and a single test that claims more
  than a small fixed number of obligations is flagged for review.
- MUST: every atomic MUST and MUST NOT obligation has its own targeted fault check, recorded in the catalog. A fault
  check is an injected fault or mutation of the SDK that breaks exactly that obligation, and it is shown to make the
  obligation's linked tests fail. The catalog records the fault's id and the result at each release. An obligation
  whose fault leaves its tests green is hollow. Where no fault can be injected, a reviewed assertion note may stand
  in, naming the assertion that checks the obligation.
- MUST: every "not applicable" reason, and every change to an existing entry, is reviewed in the pull request that
  makes it. The adversarial review of that pull request covers the catalog diff.
- MUST: a CI check fails when a MUST or MUST NOT has neither a test nor a reviewed reason, when a named test does not
  exist or does not reference the obligation, or when the pinned spec's hash changes without the catalog being
  regenerated.
- MUST: the catalog cites the spec by URL, commit and line. It does not copy the spec's text, because the spec
  repository states no licence (Q6).
- MUST: no test expectation is taken from mash's behaviour. Expected values come from the spec, Synadia's published
  vectors, or the observed behaviour of the official SDKs. mash's `internal/sap` is evidence of one implementation,
  never an oracle.
- Acceptance: AT-002.

### REQ-011: Interoperability with the official SDKs
- Source: Derived. The `synadia-agents` interop pattern (`client-sdk/python/tests/test_interop_e2e.py`), which runs the
  TypeScript `ReferenceAgent` against another language's caller.
- MUST: CI runs the Go caller against both official reference hosts: the TypeScript `ReferenceAgent` and the Python
  reference agent (`agent-sdk/python/examples/_reference_agent.py`). It also runs the official TypeScript and Python
  callers against a Go host. Every combination runs on a pinned `nats-server`; Synadia's CI pins 2.12.7.
- MUST: the runs cover discovery, a text prompt, an envelope with attachments, a mid-stream query, an error stream,
  status and heartbeat, and a signed request in both directions.
- Acceptance: AT-003.

### REQ-012: Synadia's identity vectors pass
- Source: Derived. `synadia-agents/test-fixtures/identity/` (`sender-vectors.json`, `id-sig-vectors.json`,
  `agent-id-fixtures.json`, `keys.json`): "Known-answer vectors ... verified by every SDK".
- MUST: the Go SDK passes every vector in those files, pinned by commit.
- Acceptance: AT-005.

### REQ-013: A conformance runner others can use
- Source: Confirmed. "yes sapconform" (Randy, 2026-11-10; Q12).
- MUST: a command-line runner, `sapconform` or similar, tests an SAP agent (host) over NATS as a black box.
- MUST: it lists every host-role catalog obligation in one of three groups:
  - **checkable:** checked on every run;
  - **needs configuration:** checkable only when the operator supplies inputs through a documented configuration
    file, such as a seed for §13, session-aware settings or the account and server layout;
  - **not checkable:** cannot be observed over NATS, with the reason.

  That group is the obligation's capability. It is fixed in the catalog, not chosen at run time. Each run then gives
  every checkable or needs-configuration obligation one result: pass, fail, or configuration missing. Each result
  cites the obligation id and spec line. An obligation whose effect a NATS client can
  observe (a message published, a reply, a header, a registration record, a heartbeat or its cadence) is never "not
  checkable". It is checked, or it needs configuration. Every "not checkable" entry is reviewed like a catalog
  exclusion (REQ-010).

  The report is complete: every host obligation appears in exactly one group.
- Scope decided here: testing callers needs a driver for the caller under test, so it is out of this contract and
  listed as a follow-on. The runner's report states that it covers host obligations only, so it never claims caller
  conformance.
- MUST: the runner needs nothing of the system under test beyond NATS access.
- MUST: the runner's checks are the catalog's checks, not a second list.
- Acceptance: AT-006.

### REQ-014: Ready for Synadia to take, from day one
- Source: Confirmed in scope: "we should be prepping to submit it upstream" (Randy, 2026-11-10, on draft-6). The items
  below follow what Synadia's repositories and NATS's Go repositories expect. Evidence: `nats-io/nats.go` `CONTRIBUTING.md` (`Signed-off-by` in commits and PR, CNCF Code of Conduct, an
  issue first, no non-essential dependencies); `synadia-io/orbit.go` `CONTRIBUTING.md` (per-directory modules, commit
  subjects prefixed with the module); the Apache-2.0 licence of `synadia-ai/synadia-agents`.
- **Licence.** Apache-2.0, with a `LICENSE`, a `NOTICE` if needed, and an SPDX header in each file.
- **Dependencies.** Only `github.com/nats-io/nats.go` (including its `micro` package) and `github.com/nats-io/nkeys`
  at run time. A further dependency needs a recorded reason.
- **Repository files.** A `CONTRIBUTING.md`, a code of conduct and a security policy, written so Synadia could adopt
  them unchanged.
- **Mapping and examples.** A `protocol-mapping.md` that maps every public call to its spec section, the way the
  TypeScript SDK does. Runnable examples for a caller and a host.
- **Go versions.** It supports the two most recent Go releases.
- **Commits.** Commits carry whatever sign-off Synadia requires (Q5).
- **Shaped to land as a folder (REQ-017).** The repository root is written to become `go/` in
  `synadia-ai/synadia-agents` unchanged:
  - nothing in the code or tests depends on its location in the repository;
  - the Apache-2.0 `LICENSE` sits beside `go.mod`, as each upstream SDK directory carries its own;
  - Synadia's identity vectors are vendored under `testdata/` with their source commit, so that after the move they can
    instead be read from upstream's `test-fixtures/`;
  - CI is a single workflow that upstream can adopt as a path-filtered `go-sdk.yml` beside theirs.
- Acceptance: AT-007.

### REQ-015: A new standalone public repository, named for SAP, never inside mash
- Source: Confirmed: "Put it in a new repo." and "public is fine." (Randy, 2026-11-10). The name must read as part of
  SAP: "'agents' is too generic" (Randy, on draft-6). The fork question and the name are answered in Q1, and the module
  path in Q2.
- MUST: the SDK lives in the new public repository `github.com/bogworks/synadia-agents.go`, not a fork.
- MUST: the module path is `github.com/bogworks/synadia-agents.go` (Q2). One module sits at the root; the caller, host
  and identity code are subpackages. Releases are tagged `v0.x.y` at the root. From v2 on, the path takes a `/v2`
  suffix (Go's major-version rule, as `github.com/nats-io/jwt/v2` does). The package name is `agents`, as `nats.go`'s
  is `nats`.
- MUST: until Synadia accepts it, the README says it is an independent implementation of SAP, written to be offered
  upstream, and not published or endorsed by Synadia.
- MUST: interop tests that need dependencies beyond nats.go and nkeys live in a separate test module (`test/go.mod`),
  as `synadia-io/orbit.go` does with its `<module>/test` modules. This keeps the runtime dependencies to REQ-014's.
- MUST: it does not depend on mash and does not import any bogworks package.
- Acceptance: AT-007, AT-009.

### REQ-016: A consumer can build on it without forking it
- Source: Confirmed that a later consumer builds on it: "rebuild mash around it" (card #129). The rebuild itself is
  out of scope (non-goals; Q11).
- MUST: the public API offers generic, protocol-level extension points, each grounded in the spec:
  - per-instance agent, owner and name tokens (SAP §2, §3.2; REQ-009);
  - extra micro endpoints the SDK does not interpret (SAP §3 registers services through NATS micro; REQ-004);
  - an admission hook for verified senders (SAP §13.7, the 403 for a refused verified sender; REQ-008);
  - read access to the headers of received messages (SAP §9 has callers inspect headers on every message, and §13
    carries `Agent-Sender` as a header).
- SDK policy (Proposed): a hook to add application headers to outgoing requests. SAP neither requires nor forbids it.
  Randy may cut it at ratification (Q12).
- MUST: no extension point is named for, or shaped around, one consumer.
- MUST: a written mapping from mash's `internal/sap` to the SDK's API shows which SDK call replaces each SAP piece,
  and lists each mash-only piece (the `mail` verb, zones, the mash signature variants, the stricter token rule) as
  staying in mash for the follow-on rebuild, with the extension point it would use, if any. That
  is the evidence a later consumer can build on it. The mapping is context for the follow-on contract, not a
  requirement on the SDK's API.
- Acceptance: AT-010.

### REQ-017: Offered to Synadia
- Source: Confirmed. "PR it to Synadia" (card #129).
- MUST: an issue on `synadia-ai/synadia-agents` offers the SDK. The recommended landing is a single pull request that
  adds the repository's tree as the folder `go/`. Their monorepo keeps one directory per language SDK
  (`client-sdk/typescript`, `agent-sdk/python`), and a Go module carries caller and host together. In that pull request:
  - **Module path.** It becomes `github.com/synadia-ai/synadia-agents/go`. Releases are tagged `go/vX.Y.Z`, the
    per-directory tag pattern of `synadia-io/orbit.go` and of upstream's own per-package tags (`python-v0.8.1`).
  - **What changes for users.** They change their import path once, with no API change. This happens while the SDK is
    0.x, before any compatibility promise. The final bogworks release marks its module `// Deprecated: moved to
    github.com/synadia-ai/synadia-agents/go`, and the bogworks repository is archived with a pointer.
  - **CI.** Upstream gains a path-filtered `go-sdk.yml` workflow modeled on theirs.
- MAY: if Synadia prefers a standalone Go repository, the fallback is a transfer to `synadia-ai/synadia-agents.go`.
  That keeps the history and the import path's shape; GitHub redirects the old URL, but Go users still change the
  path.
- MUST: a pull request follows that contains the SDK, or the concrete upstream integration Synadia asks for, in the
  form they name. A link-only pull request does not meet this.
- MUST: if Synadia declines, whether at the issue (no pull request wanted) or on the pull request, the decline is
  recorded with Synadia's reply as evidence. It is a deviation for Randy to accept, not completion by default. If Randy
  accepts an issue-stage decline, no pull request is needed.
- MUST: Synadia is not asked first. The SDK is built, then offered: "Do not ask Synadia." (Randy, 2026-11-10; Q4).
  The issue and pull request come after the 0.x release that meets AT-001 to AT-007, AT-009 and AT-010.
- MUST: Randy, or someone he names, opens both. Any sign-off is in the name Randy Bias (Q5).
- MUST: the contract is met when the issue and pull request are open and answered or waiting, or when a decline is
  accepted by Randy. Synadia does not have to merge (section 8).
- Acceptance: AT-008.

### REQ-018: Versions
- Source: Proposed.
- MUST: the SDK's own version is semver and starts at 0.x. Its protocol version is a separate constant (REQ-002).
- MUST: each release states the SAP revision and the conformance catalog result it was built against.
- Acceptance: AT-007.

### REQ-019: Concurrency
- Source: Proposed (mash-lead's review, 2026-11-10).
- MUST: a caller value is safe for concurrent use. Many prompts may run at once on one connection, each with its own
  reply subscription and stream.
- MUST: a host serves concurrent prompts. Its handler runs once per request, with a documented concurrency limit that
  the program can set.
- MUST: shared state is safe under the race detector: the heartbeat tracker and the replay set. Tests run with `-race`.
- Acceptance: AT-001.

### REQ-020: Go version, logging and documentation
- Source: Proposed (mash-lead's review, 2026-11-10; the `nats-io/nats.go` norm of supporting recent Go releases).
- **Go version.** The `go` directive is the older of the two most recent Go releases at the first tag, and CI builds
  and tests on both.
- **Logging.** The SDK never writes to stdout, stderr or a global logger. It accepts an optional `*slog.Logger`, and
  with none it logs nothing. It never logs seeds, credentials or message payloads (NFR-001).
- **Documentation.**
  - Every exported identifier has a godoc comment that cites its spec section.
  - The package overview shows a caller and a host in a few lines.
  - The README states the supported SAP revision, the Go version, a quick start, the conformance status and how to
    run `sapconform`.
- Acceptance: AT-007.

## 4. System boundaries

### BND-001: SAP is Synadia's
- Owned by Synadia (`synadia-ai/synadia-agent-sdk-docs`).
- Prohibited: changing the wire, adding verbs, reinterpreting a requirement.
- A spec ambiguity is decided by the official SDKs' behaviour where they agree, and recorded in the catalog. Where they
  disagree it goes to Synadia as an issue.

### BND-002: The SDK knows nothing of mash
- Prohibited: any mash concept or dependency (REQ-001, REQ-015).
- mash's extensions live in mash, on the SDK's extension points (REQ-016).

### BND-003: mash is unchanged by this contract
- No mash code changes under it. The rebuild, and any spike of mash on the SDK, belong to the follow-on contract
  (Q11).

### Existing behaviour to preserve

- Not applicable: this is new code. Interop with the official SDKs is the behaviour to match.

### Evidence from existing systems (not requirements)

- **mash `internal/sap/`.** It has the only Go implementation of §13 (`sender.go`). It also has mash-only signatures, a
  `mail` verb, zones (`subjects.go`) and a stricter token rule (`identity.go`). It has no generic caller and no
  conformance table, and it pins the spec at `c48cea5f`.
- **`henomis/phero` `nats/`** (Go, v2.3.0). It has a caller and a host and shows Go idioms for `micro`, error mapping
  and the heartbeat tracker. It lacks §13, its client cannot answer a query, and it is coupled to Phero's types.
- **The official TypeScript and Python SDKs.**
  - TypeScript: `@synadia-ai/agents` and `@synadia-ai/agent-service` 0.6.0 (2026-10-02).
  - Python: `synadia-ai-agents` 0.8.1 and `synadia-ai-agent-service` 0.5.0 (2026-10-14).
  - Both have the caller and host split, full coverage including §13, and a `ReferenceAgent` test counterparty.
  - `docs/protocol-mapping.md` is stale in two rows (L69 and L81). The code and the spec are authoritative.
- **Conformance material.**
  - Synadia publishes language-neutral fixtures only for §13 (`test-fixtures/identity/`). There are no core wire
    fixtures.
  - The spec's Appendix B examples (SAP:L1103-L1305) are prose, usable as test vectors once transcribed.
- **SDK contract.** SAP:L30 cites an `sdk-contract.md` for SDKs, which is not public.

## 5. Invariants and constraints

### INV-001: Wire-compatible with the official SDKs
Anything the Go SDK sends, the official TypeScript and Python SDKs accept. Anything they send, it accepts, within SAP
0.3.

### INV-002: No mash in the SDK
REQ-001, REQ-015 and BND-002 hold for every release.

### INV-003: The development-process standard is followed
dev-process@0.1.0 applies:
- tests before implementation;
- a pre-merge review with `ocr`;
- an adversarial review on another model family, with Daybreak for security-relevant changes (§13, parsing, error
  handling);
- Conventional Commits;
- no secrets.

The review records are completion evidence.

### Security, reliability, compatibility, performance

- **NFR-001 (security).** Seeds and credentials:
  - are never logged, never in errors and never in test output;
  - signature verification uses the nkeys library's verify;
  - the replay nonce set is bounded in memory.

  Parsers of untrusted input (envelopes, headers, chunks, `Agent-Sender`) are fuzzed.
- **NFR-002 (reliability).**
  - No goroutine outlives its context or its close.
  - Every subscription the SDK makes is drained on close.
  - A peer that never sends a terminator costs the caller one inactivity timeout, not a leak.
  - Leak checks run in the tests.
- **NFR-003 (compatibility).** The public API is documented and stable within a minor version from 0.1. Breaking
  changes are listed in a changelog.
- **NFR-004 (performance).** Proposed budget: on a local `nats-server`, the median round-trip of a short prompt
  through a Go caller and a Go host is no more than twice that of the official TypeScript SDK pair, measured by the
  same benchmark. A release that misses the budget records why (Q9 confirms the figure).

## 6. Acceptance contract

### AT-001: Unit and component tests (runnable check)
- Every REQ-002 to REQ-009 behaviour has tests that run against an embedded or spawned `nats-server` at the pinned
  version.
- They include, for every 400 case in §5 and §9 (the host's oversize refusal tested as SDK policy, apart from the
  caller's local check), every chunk type, and unknown-field preservation:
  - the ack-first and terminator rules;
  - the inactivity timeout;
  - the heartbeat interval floor and offline threshold;
  - version mismatch;
  - base64 strictness.
- Fuzz tests cover the envelope, chunk, header and `Agent-Sender` parsers.
- Leak checks pass.

### AT-002: The catalog is complete (runnable check)
- The catalog lists every RFC 2119 keyword occurrence in `core-protocol.md` at the pinned commit. This is checked
  against a regenerated extraction, so a missing keyword fails.
- Every MUST and MUST NOT has a test that exists and passes, or a reason reviewed in the pull request.
- Mutations fail the check:
  - removing a test the catalog names;
  - changing the spec hash;
  - adding a MUST without coverage.
- Behavioural mutations of the SDK make the tests linked to the matching obligation fail. A mutation that leaves
  them green shows the link is hollow, and that fails AT-002. At least these are run, one at a time, at each release:
  - omit the ack;
  - send a chunk after the terminator;
  - accept padded-less or URL-safe base64;
  - build the prompt subject from identity instead of discovery;
  - drop an unknown envelope field;
  - accept a replayed nonce;
  - sign a reply;
  - refuse a `status` request on identity grounds;
  - stop publishing heartbeats.
- Beyond that fixed list, every MUST and MUST NOT obligation's targeted fault check (REQ-010) runs at each release,
  and the catalog records the result. One fault check that leaves its tests green, or an obligation with neither a
  fault check nor a reviewed assertion note, fails AT-002.

### AT-003: Interop (runnable check)
- In CI (bogworks self-hosted runners; GitHub-hosted runners are excluded by standing order):
  - the Go caller runs against the TypeScript `ReferenceAgent` and the Python reference agent;
  - the official TypeScript and Python callers run against a Go host.
- The pinned versions of `nats-server`, Node or Bun, Python and the official SDK packages are stated in the
  repository. The test runs on any CI that has those runtimes, so it does not depend on a particular host.
- Each run covers REQ-011's list and records its messages, as Synadia's evidence recorder does.
- A run that skips a scenario fails.

### AT-004: Malformed-peer behaviour (runnable check)
- A test peer sends each malformed input of JRN-005, and these too:
  - a non-empty `status` request body, which is answered, not refused;
  - a `response` chunk in each `data` form;
  - an error with an empty body, and one with a non-JSON body, where the header text is used.
- The SDK answers as the spec says.
- No call blocks past its context or its inactivity timeout.

### AT-005: Sender identity (runnable check)
- Synadia's identity vectors pass, pinned by commit.
- Live verification is tested for every 400, 401 and 403 case of §13.7 (SAP:L927-L971).
- Stored verification (§13.8) is tested separately for authorship only:
  - a valid record older than 30 s verifies;
  - a redelivered record verifies again;
  - a bad signature or `sub` mismatch fails.
- An unknown own identity (a timed-out `$SYS.REQ.USER.INFO`) is retried and recovers, and sends no `Agent-Sender`
  header meanwhile.
- A replayed nonce is refused.
- A non-§13 peer interoperates.
- A `status` request is signed by the caller, and a host answers it after a failed classification (logged, not
  refused).
- No reply is signed.
- The caller counts header bytes against `max_payload`.
- An unknown `min_sender_trust` is treated as `signed`.
- A §13 host declares `min_sender_trust` on `prompt` and nothing on `status`.
- A caller that cannot sign fails locally, without publishing, against a `signed` endpoint.

### AT-006: The runner (demonstrated state)
- `sapconform` runs against the TypeScript `ReferenceAgent`, against a Go host, and against a deliberately broken host
  that sends no ack.
- It reports the first two as passing and the third as failing the ack-first requirement, citing its line.
- Its report states that it covers host obligations only, and gives every host obligation's capability group and
  its run result.
- A run without the configuration file reports the needs-configuration obligations as configuration missing. A run
  with it reports them as pass or fail.
- A run against a host that publishes no heartbeat reports the heartbeat obligations as failed, not as not
  checkable.

### AT-007: Ready to offer (inspection, plus a runnable check)
- A clean external Go module, outside the repository, runs `go get` on the public tag and then builds and runs a
  caller and a host against it.
- The repository has the REQ-014 files and only the allowed dependencies.
- `protocol-mapping.md` covers every exported identifier.
- The REQ-020 documentation exists. The Go version and logging rules hold, as `go vet` and a godoc coverage check
  show.
- The examples run.
- A tagged 0.x release states its spec revision and catalog result.

### AT-008: Offered (demonstrated state)
- The REQ-017 issue and pull request exist, with their URLs and Synadia's response or the absence of one recorded.
- The pull request's diff contains the SDK, or the integration Synadia asked for, as their issue reply shows. A
  link-only change fails.
- If Synadia declined, at the issue or on the pull request, their reply and Randy's written acceptance of that
  deviation are recorded. An accepted issue-stage decline stands in for the pull request.

### AT-009: No mash (runnable check, plus a review)
- A CI check fails on any import of a bogworks module, and on the identifiers or subjects named in REQ-001 (`mail`
  verb, zone, `SignInstance`, `SignUrgent`, `SignInReplyTo`, `SignVia`) in code or docs.
- The adversarial review of each release inspects the exported API (`go doc -all` output) for any concept not in SAP
  0.3, under any name. A finding blocks the release.

### AT-010: Building on it without forking it (demonstrated state)
- An example program in the SDK's own examples uses every REQ-016 extension point and builds only on the public API.
  It must be unrelated to any one consumer.
- The REQ-016 mapping document exists. It names the SDK call that replaces each SAP piece of mash's `internal/sap`,
  and assigns each mash-only piece to the follow-on rebuild.
- A spike that rebuilds mash on the SDK is not part of this contract. It belongs to the follow-on rebuild contract
  (Q11).

### Verification (was the contract met?)

| Requirements | Checked by |
|---|---|
| REQ-002 to REQ-009 | AT-001, AT-004 |
| REQ-008, REQ-012 | AT-005 |
| REQ-010 | AT-002 |
| REQ-011 | AT-003 |
| REQ-013 | AT-006 |
| REQ-014, REQ-015, REQ-018 | AT-007 |
| REQ-017 | AT-008 |
| REQ-001, REQ-015, INV-002 | AT-009 |
| REQ-016 | AT-010 |
| REQ-019 | AT-001 |
| REQ-020 | AT-007 |
| INV-001 | AT-003 |
| INV-003 | the review records |

### Validation (does it meet the need?)

- Randy reads the conformance report and can tell, for any SAP requirement, whether the Go SDK meets it.
- A Synadia maintainer can run `sapconform` and the interop CI without asking us anything.

## 7. Assumptions and open decisions

### Confirmed assumptions

- No SAP Go SDK exists. Synadia ships TypeScript and Python only (Randy, card #129 log, 2026-11-10; prior-art report).
- The SDK is for the protocol, not for mash (Randy, 2026-11-10).
- It is offered to Synadia, and mash is rebuilt on it later (Randy, card #129, 2026-11-08).

### Open questions for Randy

1. **Q1, where the repository lives, and its name: answered.**
   - Randy: "Put it in a new repo. Presumably a fork of upstream?" and, on draft-6, "'agents' is too generic ... we
     should be prepping to submit it upstream. public is fine."
   - **Not a fork.** A new standalone public repository. A fork of `synadia-ai/synadia-agents` makes the eventual
     pull request a branch away. That is its one advantage, and it is outweighed by four costs:
     - **The fork is a TypeScript and Python monorepo.** The Go SDK would be a `go.mod` in a subdirectory, with a
       path such as `github.com/bogworks/synadia-agents/go` and tags like `go/v0.1.0`.
     - **The path changes anyway.** It changes again on an upstream merge.
     - **Upstream's tooling comes with the fork.** Upstream's CI, `CODEOWNERS`, release workflows and dependabot arrive
       in our repository. Their workflows assume GitHub-hosted runners, which our standing order forbids.
     - **A standalone repository can still be offered as a fork would be.** It is shaped to drop into upstream as
       `go/` in one pull request (REQ-014, REQ-017).
   - **The name: `synadia-agents.go`.** Three candidates, each in NATS's `<name>.go` form:

     | Candidate | Module path now | After the recommended folder landing | After a transfer (fallback) | Reading |
     |---|---|---|---|---|
     | **`synadia-agents.go` (chosen)** | `github.com/bogworks/synadia-agents.go` | `github.com/synadia-ai/synadia-agents/go` | `github.com/synadia-ai/synadia-agents.go` | Names it after the repository it is meant to join. A Go developer reads it as the Go member of `synadia-agents`, beside `@synadia-ai/agents` and `synadia-ai-agents`, the way `nats.go` sits beside `nats.js` and `nats.py`. |
     | `sap.go` | `github.com/bogworks/sap.go` | as above | `github.com/synadia-ai/sap.go` | Short, but "SAP" is not in any Synadia package or repository name, and in search and in Go import lists it collides with SAP SE's many Go libraries. |
     | `agent-protocol.go` | `github.com/bogworks/agent-protocol.go` | as above | `github.com/synadia-ai/agent-protocol.go` | Says "protocol", but drops Synadia and is as generic as `agents`, which is Randy's objection. |

     `synadia-agents.go` puts Synadia's name on a bogworks repository before Synadia has accepted it. The README says
     plainly that it is independent until accepted (REQ-015).
   - Randy confirms the name at ratification.
2. **Q2, the module path: answered from NATS's conventions.**
   - The NATS conventions:
     - **Repository and module path.** A Go client library's repository is named `<name>.go` and its module path is
       the repository path (`github.com/nats-io/nats.go`). Libraries that aren't clients use a plain name
       (`github.com/nats-io/nkeys`).
     - **One module, subpackages.** One module carries its subpackages (`nats.go/micro`, `nats.go/jetstream`).
     - **Per-directory modules.** These appear only in a collection of independent extensions (`orbit.go`, tags
       `<dir>/vX.Y.Z`).
     - **Major versions.** From v2 on, a major version takes a path suffix (`github.com/nats-io/jwt/v2`). v0 and v1
       have none (`nats.go` is v1.55.0, and `nkeys` v0.4.17).
   - The derived path is **`github.com/bogworks/synadia-agents.go`** (the name is from Q1). It is one module, with
     package `agents` at the root (shared types; the package name is SAP's service name, SAP:L142, as `nats.go`'s is
     `nats`). The subpackages are `caller`, `host`, `identity` and `cmd/sapconform`. Tags are `v0.x.y` at the root,
     with `/v2` from v2.
   - After the recommended upstream landing, the path becomes `github.com/synadia-ai/synadia-agents/go`, with tags
     `go/vX.Y.Z` (REQ-017).
3. **Q3, is §13 in scope: answered.** Yes. "Signing is in scope. We need it." (Randy, 2026-11-10.)
4. **Q4, ask Synadia first: answered.** No. "Do not ask Synadia." Build, then offer upstream (REQ-017).
5. **Q5, sign-off: answered.** If Synadia requires sign-off, it is in the name Randy Bias (Randy, 2026-11-10). An agent
   does not sign the Developer Certificate of Origin. Randy, or the person he names, signs off the commits offered
   upstream.
6. **Q6, the spec's licence [not blocking].**
   - `synadia-agent-sdk-docs` has no licence file.
   - The catalog cites the spec by line and does not copy its text (REQ-010).
   - Should Randy ask Synadia to license the spec, so that Appendix B's examples can be vendored as fixtures?
7. **Q7, the runtimes for interop CI [not blocking for READY; blocking for AT-003 to run].**
   - AT-003 states its pinned runtimes and runs on any CI that has them. The bogworks self-hosted runners need Node or
     Bun, and Python, installed at those versions, beside Go and `nats-server`.
   - Owner: x-lead for the hosts, Randy for any new spend.
8. **Q8, done when Synadia has not answered [not blocking].**
   - The writer proposes that the contract is met when the offer is made and the SDK meets AT-001 to AT-007, AT-009
     and AT-010. Synadia's merge is not required (section 8).
9. **Q9, a performance budget [not blocking].**
   - None is set. Should one be set, for example a prompt round-trip on a local `nats-server` within a factor of the
     TypeScript SDK?
10. **Q10, the scope of the runner: decided in REQ-013.**
    - `sapconform` tests agents (hosts) only, and testing callers is a follow-on. Randy may widen this at ratification.
11. **Q11, the mash rebuild contract [not blocking for this contract; blocking for card #129's done-when].**
    - The mash team writes the follow-on contract, "rebuild mash on the SAP Go SDK", once this one is READY. Its first
      acceptance step is a spike of mash on the SDK.
    - Card #129 is done only when that one is too.
    - Owner: mash-lead.
12. **Q12, ratify the Proposed scope explicitly [blocking].**
    - Decided: the `sapconform` runner is in ("yes sapconform", Randy, 2026-11-10; REQ-013). The upstream-ready files
      and dependency limits (REQ-014) are in, because Randy is "prepping to submit it upstream" (on draft-6).
    - Still Proposed, each for Randy to keep or cut at ratification:
      - concurrency, Go version, logging and documentation (REQ-019, REQ-020);
      - the outgoing-header hook (REQ-016);
      - the NFR-004 performance budget. The planner recommends dropping it.

### Delegated to the team

- The internal design, package layout within Q2's answer, test structure, catalog file format and runner command name.
- The default behaviour of SHOULD-level token checks: warnings, or an opt-in strict mode (REQ-009).
- Which Appendix B examples become fixtures, transcribed and cited by line, not copied, until Q6 is answered.

### Changes that need approval

- Any change to SAP's wire behaviour, or adding a verb to the SDK (BND-001; Randy, and Synadia upstream).
- A runtime dependency beyond nats.go and nkeys (REQ-014; Randy).
- The repository's location and visibility (Q1; Randy).
- Anything said to Synadia, and the upstream pull request (Q4, REQ-017; Randy).
- New spend for CI runners (Q7; Randy).

## 8. Completion contract

### Required evidence

- The repository, its tagged 0.x release and its CI runs: unit, catalog, interop, identity vectors and the no-mash
  check.
- The conformance catalog and its report at the release.
- The `sapconform` runs of AT-006.
- The REQ-017 issue and pull request, with their state.
- The REQ-016 mapping document and the AT-010 example.
- The review records under INV-003.
- Deviations and residual risks, including spec ambiguities sent to Synadia.

### Accept if

- AT-001 to AT-007, AT-009 and AT-010 pass at the release.
- AT-008 shows the offer made.

### Reject if

- A MUST has no test and no reason.
- Interop with either official SDK fails.
- Anything of mash is in the SDK.
- The SDK depends on mash or bogworks code.
- §13 is in scope and a Synadia identity vector fails.
- Evidence is a plan rather than a result.

### Deviations

- A deviation that still passes this contract's tests is the owning lead's to approve and record (as in the
  naming-grammar contract).
- Any other deviation goes to the planner as a contract gap.

## Readiness

- Result: DRAFT. It goes to mash-lead, then the planner, then Randy. Randy's comments on draft-6, verbatim: Randy, 2026-11-10, on draft-6, via the planner: "I don't think that makes it clear it's meant as part of SAP. 'agents' is too generic. I guess we could move it later, but we should be prepping to submit it upstream. public is fine."
- Randy's answers to Q1 to Q5 and to the runner in Q12, verbatim: Randy, 2026-11-10, via the planner: "Do not ask Synadia. Put it in a new repo. Presumably a fork of upstream? Signing is in scope. We need it. yes sapconform. Randy Bias. USe go module path standards that conform to what NATS already does."
- **Answered by Randy (2026-11-10):**
  - Q1: a new repository, public. The writer answered his fork question (standalone) and chose the name
    `synadia-agents.go` after he rejected `agents.go`.
  - Q2: the path follows NATS's conventions.
  - Q3, Q4 and Q5.
  - In Q12: the runner, and the upstream-ready files.
- **Blocking before READY:**
  - Randy's confirmation of the name `synadia-agents.go`, of the standalone answer and of the folder landing
    (REQ-017);
  - the rest of Q12;
  - the critic's blocking findings. Four passes ran, with 9 blocking findings on draft-1, 4 on draft-2, 5 on draft-3
    and 2 on draft-4, all accepted and applied (Appendix A). The critic has not read draft-5.
- **Not blocking:** Q6 to Q9 and Q11. Q10 is decided in REQ-013.

## What the standard got wrong

Filled by the team when the work closes.

## Appendix A: critic findings and dispositions

The critic ran on draft-1: OpenAI Codex CLI 0.166.0, `codex exec -m gpt-6-sol -c daybreak=true`, read-only. Its inputs
were the contract, the intent card, `core-protocol.md` at `c48cea5f`, and the writer's prior-art report as a claim to
verify. The critic has not read draft-2.

| Finding | Severity | Disposition |
|---|---|---|
| F-01: REQ-002 made the SHOULD to pin an exact MAJOR.MINOR into a MUST. | blocking | Accepted. The caller compares MAJOR.MINOR (spec MUST, SAP:L643). Exact `0.3` pinning is marked SDK policy (Proposed) under the SHOULD at SAP:L651, and the program can relax it. |
| F-02: REQ-005 presented recommended timings and optional fields as spec obligations. | blocking | Accepted. Spec obligations are cited and listed in the catalog. The 30 s default, the opt-in below 1 s, the default 3 × threshold and the default declaration of optional fields are SDK policy (Proposed). |
| F-03: identity MUSTs were missing: sign `status` requests, count headers in `max_payload`, treat an unknown `min_sender_trust` as `signed`, always answer `status`, never sign replies. | blocking | Accepted. Each was checked against the spec (SAP:L709, L887, L1001, L1003, L688, L1052), added to REQ-008 and given AT-005 cases. |
| F-04: core MUSTs were missing: both `response` data forms, ignoring the `status` request body, and the error-header fallback. | blocking | Accepted. Each was checked against the spec (SAP:L364, L548, L593), added to REQ-003 and REQ-004 and given AT-004 cases. |
| F-05: keyword extraction proves indexing, not testing. One smoke test could cover every MUST. | blocking | Accepted. REQ-010 now asks for atomic obligations, test-to-obligation links checked both ways, a flag on tests that claim many obligations, and review of every exclusion and entry change. |
| F-06: REQ-013 promised a caller runner while Q10 left it open. | blocking | Accepted. REQ-013 is narrowed to hosts, the report states it, and caller testing is a follow-on. Q10 is decided. Randy may widen it. |
| F-07: interop tested the Go caller against the TypeScript host only. | blocking | Accepted. REQ-011 adds the Python reference agent as a host for the Go caller. |
| F-08: per-endpoint tokens and mash-shaped extension points came from mash, and AT-009 matched strings only. | blocking | Accepted in part. Per-instance agent, owner and name tokens are SAP's own model (§2, §3.2), now cited as such. REQ-016 is reworded to generic, protocol-level extension points, none shaped around one consumer. AT-009 adds a review of the exported API for non-SAP concepts. AT-010 adds an unrelated example that uses every extension point. |
| F-09: a link-only pull request could satisfy AT-008. | blocking | Accepted. The pull request must contain the SDK or the integration Synadia asks for. A decline is a deviation for Randy, not completion. |
| F-10: scope beyond Randy's words was labelled Derived or Confirmed. The card's final gate (mash on the SDK) needs a tracked follow-on. | nonblocking | Accepted. REQ-014 and REQ-015 are relabelled Proposed. The mash rebuild is tracked as Q11. The governance files stay in, as Proposed: mash-lead asked for an upstream-ready structure, and Randy can cut them. |
| F-11: the contract copied wire rules instead of citing them. | nonblocking | Accepted. A citation rule now opens section 3: cite the section and lines, never strengthen a SHOULD, mark SDK policy. Atomic obligations live in the catalog, not this page. REQ-003 to REQ-009 keep short summaries with citations, so the contract reads on its own. |
| M-1: AT-007 passes while the tagged module can't be fetched or built externally. | mutation row | Accepted. AT-007 adds a clean external consumer module built from the public tag. |
| M-2: AT-010's one-endpoint spike passes while other mash paths need private SDK changes. | mutation row | Accepted. The spike exercises every extension point the mapping names, and anything it can't do is a failure or a recorded narrowing. Superseded in draft-3: the spike moved to the follow-on contract (Q11). |

Disputed: none.

### Second pass, on draft-2

Same critic and command. The critic judged its draft-1 findings resolved (F-01, F-03, F-04, F-10, M-1, M-2) or partly
resolved (F-02, F-05 to F-09, F-11). Each partial finding is answered by the new findings below, applied in draft-3.

| Finding | Severity | Disposition |
|---|---|---|
| G-01: AT-003 omitted the Go caller against the Python host, though REQ-011 required it. | blocking | Accepted. AT-003 adds the combination and states its pinned runtimes. |
| G-02: a host could skip declaring `min_sender_trust`, and a caller could send unsigned to a `signed` endpoint. The citation was §13.7 and §13.8 instead of §13.9 and §13.10. | blocking | Accepted. REQ-008 now requires the declaration (`prompt` only) and the caller's local refusal, citing §13.9 (SAP:L982-L1003) and §13.10 (SAP:L1005-L1037). AT-005 adds both cases. |
| G-03: a black-box runner cannot check every host obligation without configuration, yet claimed to. | blocking | Accepted. REQ-013 sorts every host obligation into checked, needs configuration (through a documented configuration file) or not checkable, with reasons, in a complete report. AT-006 adds a run that uses the configuration file. |
| G-04: a linked test need not assert its obligation. | blocking | Accepted. AT-002 adds behavioural mutations of the SDK, one per key obligation; a mutation that leaves its tests green fails AT-002. |
| G-05: subscribe-before-PING was stated as a spec obligation, but SAP §8.5 is a SHOULD. | nonblocking | Accepted. It moves to SDK policy, citing SAP:L531. |
| G-06: refusing an oversized payload on the host is a MAY (SAP:L293), and only the caller's local check is a MUST. | nonblocking | Accepted. REQ-004 marks the host refusal as SDK policy, and AT-001 tests it apart from the caller's MUST. |
| G-07: BND-003's "no mash code changes" conflicted with the mash spike. | nonblocking | Resolved by mash-lead's review: the spike moved to the follow-on contract (Q11), and BND-003 says so. |
| G-08: JRN-007 and REQ-017 disagreed on a decline, and AT-008 inspected neither the PR's contents nor Randy's acceptance. | nonblocking | Accepted. JRN-007 now matches REQ-017. AT-008 checks the diff's contents and records Randy's acceptance of any decline. |
| G-09: Q5 rested on nats.go's sign-off rule, while `synadia-agents` states none. | nonblocking | Accepted. Q5 is conditional on what Synadia requires, as the answer to Q4 shows. |

The critic has not read draft-3.

### Third pass, on draft-3

Same critic and command. It judged these resolved: G-01, G-02, G-06, G-07, G-09, F-02, F-06 and F-07. It judged these
partly resolved, each answered by a new finding below: G-03, G-04, G-05, G-08, F-05, F-08, F-09 and F-11.

| Finding | Severity | Disposition |
|---|---|---|
| H-01: the catalog and the runner could overclaim coverage. "Not checkable" needed only a reason, and only eight behaviours were mutated. | blocking | Accepted. An obligation a NATS client can observe is never "not checkable": the classification is fixed in the catalog and reviewed (REQ-013). AT-002 adds a heartbeat mutation and one mutation per catalog section that has a MUST. AT-006 adds the no-heartbeat host. |
| H-02: the §13.4 rule to retry an unknown own identity (SAP:L851) was missing. | blocking | Accepted. It is in REQ-008, with the no-header and local-refusal behaviour, and tested in AT-005. |
| H-03: AT-005 mixed live and stored verification. Stored verification skips the window and the nonce set (SAP:L977-L980). | blocking | Accepted. REQ-008 and AT-005 split them. Stored records are tested for authorship only, including an old record and a redelivered one. |
| H-04: the mash mapping asked for an SDK call for every piece, including mash-only pieces the SDK must not have. | blocking | Accepted. The mapping covers SAP pieces only, and assigns mash-only pieces to the follow-on rebuild. |
| H-05: a decline at the issue stage had no completion path. | blocking | Accepted. A decline at either stage is recorded with Synadia's reply, as a deviation for Randy. An accepted issue-stage decline stands in for the pull request. |
| H-06: the citations needed fixing (§13.8 not §13.6; SAP:L533 not L531), and the recommended 60 s timeout was not marked as policy. | nonblocking | Accepted and corrected. The G-05 row above cites L531 as it was written for draft-2; L533 is the line. |

The critic has not read draft-4.

### Fourth pass, on draft-4

Same critic and command, now told to raise blocking only for defects that would accept a non-conformant SDK or make
the contract unsatisfiable. It judged these resolved: H-02 to H-06, F-08, F-09, F-11, G-05 and G-08. It judged
H-01, F-05, G-04 and G-03 partly resolved, answered below. It found no misstated citation in the requirements.

| Finding | Severity | Disposition |
|---|---|---|
| I-01: one mutation per section left the other MUSTs in a section with possibly hollow tests. | blocking | Accepted. Every atomic MUST and MUST NOT has its own targeted fault check, recorded in the catalog with its result at each release. A reviewed assertion note stands in only where no fault can be injected (REQ-010, AT-002). |
| I-02: Q1(c) and Q3(b) conflicted with REQ-015 and the §13 requirements, so choosing them would make the contract unsatisfiable. | blocking | Accepted. Each option now says that choosing it needs a revised contract before READY. |
| I-03: the runner's classification meant two things, a fixed group and a run result. | nonblocking | Accepted. Each obligation has a capability group, fixed in the catalog (checkable, needs configuration, not checkable). Each run gives a separate result (pass, fail, configuration missing) (REQ-013, AT-006). |

The critic has not read draft-5.

### mash-lead's review of draft-2 (2026-11-10), applied in draft-3

1. **The mash spike was in acceptance.** It moved to the follow-on rebuild contract (Q11). AT-010 keeps the generic
   example and the written mapping. REQ-016's extension points are tied to SAP §2, §3, §9 and §13, and the outgoing
   header hook is marked Proposed.
2. **Tokens.** REQ-009 cites SAP §2 alone. The naming-grammar contract and seam page are no longer cited, and
   BND-004 is removed.
3. **Questions.** Q4 now blocks READY, since it gates Q1 and Q2. AT-003 states its pinned runtimes and runs on any CI
   that has them. Q7 blocks AT-003 running, not READY.
4. **Test oracles.** No test expectation is taken from mash's behaviour (REQ-010).
5. **Missing pieces:**
   - concurrency (REQ-019);
   - Go version, logging (an optional `slog` logger, otherwise silent) and documentation (REQ-020);
   - §13 key sourcing, restart-to-rotate and a bounded, fail-closed replay set (REQ-008);
   - an NFR-004 budget proposal.
6. **The runner (REQ-013)** stays Proposed and is listed for Randy to ratify explicitly (Q12), together with the other
   Proposed scope.
