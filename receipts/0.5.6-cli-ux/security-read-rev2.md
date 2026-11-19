# Security reread, mash 0.5.6 work contract (cli-ux) revision 2 draft

Review lane: x-ops2 (operations). Date: 2026-11-19. Advisory to the contract, not a release gate (mash-lead #786). Security only; a reread of the draft after our read of revision 1 (bogworks/mash PR #824, receipts/0.5.6-cli-ux/security-read-rev1.md).
Reviewed: the mash lead's draft contract mash-0.5.6-cli-ux, revision 2 (draft), contract@0.3.4, copy at s3://homerange-xfer/masterplanner/for-herds/mash-0.5.6/contract-0.5.6-cli-ux-rev2-draft.md (sha256 2986d2b9338dfaabe7e5f281e36714557f319deb7873867871c2da75371b1392).
Not handed over: the NATS helper script and the skill text (policy's replacement, draft 3, has not landed in #822); the reviewer judged the contract's requirements on them only. Rerun the script read when it lands.
Reviewer: OpenAI Codex, run by kit/codex-review (x-herd#132), `codex exec --sandbox read-only`, codex-cli 0.170.0, model gpt-6-sol, Daybreak ON (codex -c daybreak=true); the attestation was stated to the reviewer in its prompt. Other family from the writer (Anthropic). The reviewer read text and ran nothing.
Handed over (sha256 list at the end): the revision 2 draft; standards from bogworks-dev-standards 68475627ac8c673d9510cdeccd675a44d8765c07 (contract/STANDARD.md 0.3.4, contract/CHECKLIST.md, standing-orders/mash.md, cli/STANDARD.md, skills/contract-critic/SKILL.md); under prior/, the revision 1 draft and our revision 1 read. Nothing else. Where the content went: the pages above were sent to OpenAI through the operator's Codex subscription for this review.

---

# Security read, mash 0.5.6 cli-ux revision 2 (draft)

Reviewer attestation: this security runs on model gpt-6-sol, Daybreak ON (codex -c daybreak=true), codex-cli 0.170.0, read-only sandbox.

Revision 2 adds substantive rules, but **six blocking findings remain**. This is a read of the supplied text. I did not run the helper, skill, binaries, or acceptance tests; the replacement helper and skill were not in this workspace. This contract has no `NFR-002`, `NFR-003`, or section 5 credential table. I reviewed the corresponding boundaries in `REQ-002`, `REQ-003`, `REQ-010`, and `INV-003`.

## Threat model

The assets are endpoint credentials and private keys, operator and account seeds, admin credentials, the CA signing key, and the server and CA identity clients trust. An installing agent can act with its user’s filesystem and process authority, including downloading tools and answering prompts. Other local users and processes can reach a loopback listener and may influence paths in shared directories. The trust boundaries are downloaded code, generated authority material, the hand-off token and CA, per-user configuration, and any decision to expose a listener beyond the host.

## Revision 1 finding disposition

“Test” means revision 2 specifies a check a reviewer could run, not that I ran it.

| Rev 1 item | Status | Closing words in revision 2; testability |
|---|---|---|
| 1. Token in argv | **Partly closed** | `INV-003`, §5: “tokens, seeds and keys come from a file or stdin, never argv,” with no secret in examples, logs, JSON, errors or receipts. `REQ-003`, §3, uses `--token <file>`. The rule is testable with process arguments and captured outputs; `AT-002` does not require those checks. |
| 2. Trust replacement | **Partly closed** | `REQ-003`, §3, confines writes to the current user’s mash config, displays old and new servers and CA fingerprints, requires `--replace` and “an explicit yes,” and states precedence. A fingerprint supplied by the same untrusted hand-off does not authenticate the new CA; an agent can supply `--yes`. `AT-002` tests neither case. |
| 3. `--creds-out` creation | **Partly closed** | `REQ-003`, §3, applies `INV-003`: exclusive new regular file, owned directory, no symlink following, 0600 from creation, atomic completion, and collision refusal. These are testable, but `AT-001` and `AT-002` specify no existing-file or hostile-link case. |
| 4. Endpoint auto-pick | **Partly closed** | `REQ-009`, §3, scopes discovery to the selected current-user config, requires owned regular 0600 files, sets precedence, and refuses zero or several valid endpoints. `AT-001` exercises the one-endpoint path; `AT-004` is output conformance, with no specified zero, multiple, or hostile-file test. |
| 5. Helper directory race | **Partly closed** | `REQ-002`, §3: “create the directory exclusively, mode 0700 from creation … not a symlink” and create files exclusively without following links. It does not secure every parent component against replacement or specify a race test. |
| 6. Secret-bearing diagnostics | **Closed in text** | `REQ-002`, §3, forbids secrets in logs and failure output, requires redacted `nsc` and `nats-server` diagnostics, and explicitly requires a failure-path test at supported versions. The helper and test were unavailable to verify implementation. |
| 7. Agent widens network exposure | **Partly closed** | `REQ-002`, §3, defaults to `127.0.0.1`, identifies other same-host users, and requires a “separate explicit yes” for other interfaces, firewall changes, and a startup unit. It does not say the person, rather than the installing agent, must give each yes for a specified address or scope. |
| 8. Unpinned installation | **Partly closed** | `REQ-002`, §3, pins `nats-server` and `nsc`, requires a published digest check and advance display of source, version and command, and forbids pipe-to-shell. It does not anchor the digest to an authenticated source or prescribe a verification test. |
| 9. Unsafe printed command | **Closed in text** | `REQ-002`, §3, requires shell quoting or refusal and explicitly tests spaces and metacharacters. The helper was unavailable to verify the result. |
| 10. Plain-text override | **Partly closed** | `REQ-010`, §3, removes the config key, allows only `MASH_INSECURE_PLAINTEXT=1` “in the environment of that run,” warns of unencrypted credentials and mail, and tells agents not to set it themselves. An inherited environment can still enable it without a decision for this connection; `AT-004` specifies no transport check. |
| 11. Authority lifecycle | **Partly closed** | `REQ-002`, §3, requires the guide to say who keeps the CA key and exports and how to store or remove them; `adopt` copies admin credentials into mashctl config. The contract leaves custody and post-adoption disposition to the unwritten guide, with no acceptance check. |

## Credential and trust inventory

The contract states no complete credential table. “Reader” below identifies the intended boundary; it is not a claim that permissions were verified.

| Artifact | Stated location and reader | Agent action; exposure boundary |
|---|---|---|
| CA private key and server private key | Helper directory, created under `REQ-002`/`INV-003`; intended for its owning user. | Agent may run the helper. The contract does not decide who retains the CA key after adoption. Secret values are barred from argv, logs and receipts by `INV-003`. |
| CA certificate and server address | Helper output; CA path saved by `adopt` under `REQ-007`; pair installed in current-user config by `REQ-003`. | Agent can transfer or install them. Displaying a fingerprint records the offered identity but does not establish its origin. |
| Operator and account seeds; exported admin credentials | Helper export directory; intended for its owning user. `adopt` copies admin credentials into mashctl’s config directory at 0600. | Agent may adopt and later remove exports. The contract does not specify when removal is required or how copied credentials are checked after a partial failure. |
| Endpoint private key and `.creds` | `mash key create` and `--creds-out`; current-user files governed by `INV-003`. | Agent may create them for that user. Values must not appear in argv, examples, logs, JSON, errors or receipts; `AT-001`/`AT-002` do not explicitly verify those channels. |
| Hand-off token | File or stdin under `INV-003`; received by the installing user. | Agent may pass its path to `credential install`. Token contents can propose a server and CA under `REQ-003`, so the token is also a trust input. |
| Config, logs and receipts | Current-user mash config for installed server and CA; helper directory for helper output. | Config writes are to be atomic under `REQ-003`. `REQ-002` and `INV-003` prohibit secret values in logs and receipts, but the supplied acceptance cases do not inspect all such output. |

## Findings

**B1 — CARRIED, blocking, high certainty. Agent-controlled consent can authorize a trust change or wider exposure.** Affected: `REQ-002` and `REQ-003`, §3; `AT-001` and `AT-002`, §6. An agent given “set up mash” can accept a hand-off token, calculate its new CA fingerprint, run `credential install --replace --yes`, then answer yes to an external bind, firewall rule, or startup unit. The contract requires an “explicit yes” but does not reserve that decision to the person or require the exact new address, CA identity, interface, firewall scope, and persistence action to be approved separately by them. **Smallest change:** require the skill to obtain the person’s explicit decision on each exact trust or authority change; forbid an agent from generating or supplying the confirming yes from the generic setup request. Add refusal cases to `AT-001` and `AT-002`.

**B2 — CARRIED, blocking, high certainty. The new CA fingerprint has no authenticated source.** Affected: `REQ-003`, §3; `JRN-002`, §2; `AT-002`, §6. A substituted hand-off token can supply both an attacker server and its CA. Showing that CA’s fingerprint and asking for `--replace` still lets the recipient approve the attacker’s self-described identity. The contract says “verified pair” without defining what verifies the CA against the operator Bob intended. **Smallest change:** require the recipient to compare the new CA fingerprint and server address with an operator-provided value received independently of the token, before first install or replacement. Have `AT-002` substitute a token and require refusal.

**B3 — CARRIED, blocking, medium certainty. Helper path protection stops at the final directory.** Affected: `REQ-002`, §3; `INV-003`, §5. In a writable parent path, another process can replace a parent component while the helper creates its final directory and files. The contract requires an exclusively created, owned final directory and no-follow file creation, but says nothing about trusted parent components or detecting their replacement. **Smallest change:** require owned, non-symlink trusted parent components and refuse a path whose components change during setup; exercise a swapped-parent case and ownership/type checks in the helper test.

**B4 — CARRIED, blocking, high certainty. A published digest alone does not establish the downloaded tool’s identity.** Affected: `REQ-002`, §3; §8 “Release mechanics.” An agent can obtain a binary and its matching digest from a substituted download page and execute it while satisfying “verify each download against a published digest.” **Smallest change:** name the trusted release source and require the version and digest or signature to be authenticated independently of the downloaded binary; test a mismatched or substituted artifact before execution.

**B5 — CARRIED, blocking, medium certainty. An inherited environment can silently permit plain-text transport.** Affected: `REQ-010`, §3; `JRN-007`, §2; `AT-004`, §6. A parent process sets `MASH_INSECURE_PLAINTEXT=1`; a later agent invocation reaches a plain-text server without making a decision for that connection. “In the environment of that run” includes inherited values. The refusal warning is never shown once the variable is set. On that connection, the contract itself says credentials and mail cross unencrypted. Default loopback binding limits remote reach but is reachable by other same-host users; it is not a per-user boundary. **Smallest change:** define how a deliberate per-run choice is evidenced and require a conspicuous plain-text status or warning whenever the override is active. Add an inherited-variable and actual plain-transport case to acceptance.

**B6 — CARRIED, blocking, high certainty. Acceptance does not verify several new security rules.** Affected: `INV-003`, §5; `REQ-003`, `REQ-009`, `REQ-010`, §3; `AT-001`, `AT-002`, `AT-004`, §6. An implementation can pass the named journeys and output goldens while putting a token in process arguments, writing through a symlink, silently picking a planted credential, or using the inherited plain-text override. The requirement words reject most of these, but §6 assigns no corresponding verification; `AT-004` checks output conformance. **Smallest change:** add focused acceptance cases for argv and every output channel, existing files and links, zero/multiple/foreign-owned credentials, trust replacement, and override transport. Record expected refusal and observed file modes and destinations.

**N1 — CARRIED, nonblocking, high certainty. Authority material still lacks a contract-level disposition.** Affected: `REQ-002`, §3; `REQ-007`, §3; `AT-001`, §6. After adoption, the helper’s CA key and exported seeds or admin credentials can remain in a long-lived or backed-up directory while the copied admin credential is also live. The contract tells the future guide to “say who keeps” them but does not decide who, what must be removed, or what protected storage is acceptable. **Smallest change:** state the required post-adoption custody and cleanup rule in the contract and verify the guide and adoption result against it.

## Gaps

- `REQ-002` gives no concrete limit for what same-host users can learn from or do at the default loopback port. It should distinguish reachability from authenticated NATS access and state what the helper’s default TLS and authentication require.
- `REQ-002` does not define the exact interface address, firewall scope, or startup unit details that must be presented for each human decision.
- `REQ-003` does not specify how the recipient authenticates the first installed server and CA when there is no old config to compare.
- §6 has no stated failure and cleanup check for an interrupted `adopt` after copying admin credentials, or for an interrupted atomic credential/config write.

## Summary

Of the 11 revision 1 findings, **2 are closed in contract text, 9 are partly closed, and none is fully verified in implementation**. I find **6 blocking and 1 nonblocking** residual findings; no separate new finding is needed beyond the new routes described in B1, B2, and B5. My view is that ratification should wait for contract changes. If any blocking finding is left unfixed, contract@0.3.4 requires the ratifier’s **written acceptance of that specific residual risk** before READY. N1 should also receive an explicit custody decision rather than remain delegated to an unwritten guide.
---

Inputs (sha256):

```
2986d2b9338dfaabe7e5f281e36714557f319deb7873867871c2da75371b1392  ./contract-0.5.6-cli-ux-rev2-draft.md
6989d803bff9d15e27951ef8dc54236809fd2bf561112dda40a5358c9b86975b  ./prior/contract-0.5.6-cli-ux-rev1-draft.md
e8b6b142726befd7b6d1d2ed9181d51573b1bb54bf0bd3634dc976805bb4c74a  ./prior/security-read-rev1.md
9e8d9c48c2fde84244ca65c4ea32c331b2154a184535611d0ffe97a5a037f531  ./standards/cli/STANDARD.md
ed7b2448870e7cf899e4e8dc3b2735ff0ce8d8701844e5cb1e9b2b3152b8f73d  ./standards/contract/CHECKLIST.md
d96d969f78ff7e4eef8fc22a6b7b12db5a8ef3776b9a6a9b970e26ab45a3de85  ./standards/contract/STANDARD.md
9470bf08d732b908689ff2def78c271e852d77e6a1840ad2e5b4a9d7a02647a0  ./standards/skills/contract-critic/SKILL.md
ed1f58552eef3f041f4d37bb709557f6bcd7809f8eb00d5fb08aade797ac68cc  ./standards/standing-orders/mash.md
```
