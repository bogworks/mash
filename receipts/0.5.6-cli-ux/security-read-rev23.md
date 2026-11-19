# Security reread, mash 0.5.6 work contract (cli-ux) revision 23 draft

Review lane: x-ops2 (operations). Date: 2026-11-19. Advisory to the contract, not a release gate (mash-lead #966). Security only; a reread after our rev 22 read (receipts/0.5.6-cli-ux/security-read-rev22.md, bogworks/mash PR #853), asked as a clean-read attempt. Randy's decision 264 stands: the same-OS-user risk is accepted for mash and was not raised.
Reviewed: the mash lead's draft contract mash-0.5.6-cli-ux, revision 23 (draft), contract@0.3.4 (lane/lead 1dbe688), copy at s3://homerange-xfer/masterplanner/for-herds/mash-0.5.6/contract-0.5.6-cli-ux-rev23-draft.md (sha256 31f89534fc72bcab5902c1e575dbd4c7665c05cc19c69df509d0d8d13c8ba8e9). Not handed over: the helper script and the skill (not landed).
Reviewer: OpenAI Codex, run by kit/codex-review (x-herd#132), `codex exec --sandbox read-only`, codex-cli 0.170.0, model gpt-6-sol, Daybreak ON (codex -c daybreak=true); the attestation was stated to the reviewer in its prompt. Other family from the writer (Anthropic). The reviewer read text and ran nothing.
Handed over (sha256 list at the end): the rev 23 draft; standards from bogworks-dev-standards 68475627ac8c673d9510cdeccd675a44d8765c07 (contract/STANDARD.md 0.3.4, contract/CHECKLIST.md, standing-orders/mash.md, cli/STANDARD.md, skills/contract-critic/SKILL.md); under prior/, the rev 22 draft and our rev 22 read. Nothing else. Where the content went: the pages above were sent to OpenAI through the operator's Codex subscription for this review.

---

# Security read, mash 0.5.6 cli-ux revision 23 (draft)

Reviewer attestation: this security runs on model gpt-6-sol, Daybreak ON (codex -c daybreak=true), codex-cli 0.170.0, read-only sandbox.

I started with §5’s credential inventory, “Authority of an agent,” and “Network exposure,” then read the rev 22 findings and the full rev 23 contract against the supplied standards. I read text and ran nothing. The helper, skill, binaries, and tests were not supplied.

## Threat model

The assets are signing seeds, endpoint keys and credentials, mail, and the server and account an endpoint trusts. An installing agent may receive only “set mash up”; a person’s separate operator message is the trust input for `credential install`. Other OS users can reach a loopback listener, while downloaded tools and plugin bytes cross a supply-chain boundary before execution. Files, argv, output, logs, and receipts are disclosure paths. Randy’s decision 264 accepts mash’s same-OS-user standing risk; this review does not raise it.

## Rev 22 disposition

| Rev 22 item | Rev 23 status |
|---|---|
| Finding 1: authentication before confirmation | **Closed in text; testable.** `REQ-003`, §3 now orders local preflight and issuer comparison, then three-value confirmation, then any credential-bearing connection. `AT-007`, §6 requires a “no” case in which the server observes **“NO authentication attempt.”** |
| Finding 2: temporary credentials left by an interrupted later install | **Partly closed.** `REQ-003`, §3 fixes the name, directory, exclusive 0600 creation, and same-attempt rerun cleanup; `AT-007`, §6 tests a kill before rename. A successful install for a *different* address can still leave the earlier temporary copy. |
| Finding 3: inherited plaintext override | **Open, nonblocking.** `REQ-010`, §3 still allows an inherited value and tests its warning in `AT-007`, §6. Section 7 records Randy’s treatment. |
| Missing and later-install `--account` cases | **Closed in text; testable.** `REQ-003`, §3 requires a complete valid key on **“EVERY install”** and says it is never ignored. `AT-007`, §6 covers missing, invalid, and differing-issuer cases, including a “yes” answer. |
| Binary destination cases | **Closed in text; testable.** `REQ-011(i)`, §3 names group/world writability, sticky `/tmp`, symlinks, and an absent destination. |
| Temporary-file shape | **Closed for the same address; testable.** `REQ-003`, §3 specifies `.<address>.creds.tmp` in the destination directory on the same filesystem. |

## Findings

**1. NEW residual of rev 22 finding 2 — blocking; high certainty. `REQ-003`, §3; `INV-003`, §5; `AT-007`, §6 — a different later install can leave a secret temporary file.** Install endpoint A, kill immediately before its rename, then successfully install endpoint B. `REQ-003` removes a stale temporary **“of that exact name”** on rerun and bars success only while **“one”** remains, permitting A’s 0600 JWT-bearing temporary to persist after B succeeds. `AT-007` tests a rerun of the interrupted install, not this sequence. **Smallest change:** before any later-install success, require inspection and safe removal of stale credential temporaries for all addresses in that config directory, refusing unexpected file types; test A interrupted followed by B installed and inspect remaining files.

**2. CARRIED — nonblocking; high certainty. `REQ-010`, §3; §5 “Network exposure”; `AT-007`, §6 — inherited plaintext remains sufficient to enable an unencrypted connection.** A parent sets `MASH_DANGEROUSLY_SKIP_NATS_ENCRYPTION=1`; the child inherits it and connects to a plaintext-capable server. The warning precedes the connection, but no fresh choice is required. A process with loopback packet-capture rights can read credentials and mail; on a wider plaintext route, an observer there can do the same. The contract expressly permits inherited values, and §7 records Randy’s treatment. **Smallest change if the writer intends to close this exposure:** require an explicit choice for that destination on the run; inheritance alone would not enable plaintext.

**3. NEW — nonblocking; medium certainty. `REQ-003`, §3; `AT-007`, §6 — the later-install confirmation sequence is ambiguous.** `REQ-003` says **“The first install SHOWS”** the three values, but its new order requires **“the three-value confirmation”** before *any* credential-bearing connection. For a later install, one implementer could prompt again and another could omit the prompt and connect using the configured server and CA. The latter follows the first-install wording but not the stated order; `AT-007` does not settle it. **Smallest change:** state explicitly whether every install prompts. If later installs do not, scope the ordering sentence to first installs and state that the local `--account` comparison still precedes their authenticated connection; test the chosen flow.

## Credential and trust inventory

`INV-001` and `INV-003`, §5 prohibit secret values in argv, output, JSON, logs, errors, examples, and receipts; `AT-007`, §6 requires channel scans. `--key` and `--token` carry paths. The complete public `--account` key is an argv value, but is not a secret.

| §5 row | Location and readers | Permitted agent action; exposure if leaked |
|---|---|---|
| Operator signing seed, account seed, system credentials | mashctl config, 0600; mashctl reads them | The agent may run the bounded helper and `mashctl adopt --from`. Values in argv, logs, or receipts enable identity minting and mail access. |
| Admin credentials | mashctl config, 0600; mashctl reads them | Local adopt is permitted. Disclosure enables endpoint and bucket administration. |
| CA key | Helper directory, 0600; setup user reads it | The agent may create it through the default helper. Disclosure enables server-certificate minting. |
| Server TLS key | Helper server directory, 0600; that user’s `nats-server` reads it | The agent may start the recorded local server. Disclosure enables server impersonation. |
| CA certificate | Server directory and mash config, 0644; everyone can read it | The agent may copy it. It is public; the person compares its full fingerprint. |
| Endpoint seed key | Endpoint machine, 0600; mash reads it | The agent may create a same-machine endpoint for a person-named agent. Its value must stay out of argv and the scanned channels. |
| Endpoint credentials | Mash config, 0600; mash reads them | The agent may create local, person-named endpoints. The interrupted-install extra copy is finding 1. |
| Token (JWT) | Person-carried operator file, 0600; install reads it in place | The agent hands over the install command and stops. The person deletes the source after install. The token together with its key enables endpoint access. |

The contract has **no `NFR-002` or `NFR-003` IDs**. The applicable §5 paragraphs are “Authority of an agent” and “Network exposure.” The former permits an agent on a sparse request to verify pinned releases, run the default local helper, adopt its folder, and create endpoints for agents the person named. It requires the person’s own answer for another source, another server, or deletion, and hands plugin installation and `mash credential install` to the person. I found no requirement allowing the agent to widen those permissions itself.

The helper’s TLS-only listener binds one client port on `127.0.0.1`, with no monitoring, cluster, gateway, or leaf-node port. Other local users can attempt connections but must fail authentication; TLS protects another connection’s payload. With the plaintext override, an observer able to capture that loopback traffic can read credentials and mail.

## Gaps and consistency checks

- **Local issuer comparison:** `REQ-003`, §3 requires the comparison before a credential-bearing connection. Reading the token locally is necessary and allowed by that order. The contract should name the JWT `iss` claim, require its local parse without network lookup, and test the order.
- **Early TLS and post-confirmation failure:** The text allows an unauthenticated TLS handshake or CA/host check before confirmation because it forbids only a connection *carrying the key or token*. Such a handshake sends neither secret. The existing “before ANY write” refusal covers TLS verification failure after confirmation; `AT-007` should explicitly exercise that ordering and verify no files were written.
- **Temporary-name input and stale-file refusal:** `REQ-003`, §3 derives a filename from the token’s address without specifying a filename-safe address grammar or treatment of separators, dots, excessive length, and case-folding collisions. It also does not say how cleanup handles a pre-existing symlink or directory at that name. Require a canonical safe component and refusal of unexpected stale-file types without following links; test those inputs.
- **Section 7 and readiness:** Decision 264’s accepted same-user risk remains cited, not reopened. The plaintext residual is recorded. §3’s target-output files, §8’s review round, and Readiness’s status-views page remain pending; their content and the helper and skill cannot be verified from this workspace.

**Summary:** One blocking finding and two nonblocking findings. In my view, ratification should wait for the cross-address temporary-file cleanup rule and test. Rev 23 closes the prior pre-confirmation authentication finding in testable contract text; implementation behavior remains unverified.
---

Inputs (sha256):

```
31f89534fc72bcab5902c1e575dbd4c7665c05cc19c69df509d0d8d13c8ba8e9  ./contract-0.5.6-cli-ux-rev23-draft.md
6e707fec5fae62b3d7f77e01b2d558b61923bce100284293948d5d57cee4566b  ./prior/contract-0.5.6-cli-ux-rev22-draft.md
58670b9b1a4c6d731792946d7f1ac3f9fb0d7c2344a07d3dd2af5dd75cfa7d5a  ./prior/security-read-rev22.md
9e8d9c48c2fde84244ca65c4ea32c331b2154a184535611d0ffe97a5a037f531  ./standards/cli/STANDARD.md
ed7b2448870e7cf899e4e8dc3b2735ff0ce8d8701844e5cb1e9b2b3152b8f73d  ./standards/contract/CHECKLIST.md
d96d969f78ff7e4eef8fc22a6b7b12db5a8ef3776b9a6a9b970e26ab45a3de85  ./standards/contract/STANDARD.md
9470bf08d732b908689ff2def78c271e852d77e6a1840ad2e5b4a9d7a02647a0  ./standards/skills/contract-critic/SKILL.md
ed1f58552eef3f041f4d37bb709557f6bcd7809f8eb00d5fb08aade797ac68cc  ./standards/standing-orders/mash.md
```
