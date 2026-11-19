# Security reread, mash 0.5.6 work contract (cli-ux) revision 25 draft

Review lane: x-ops2 (operations). Date: 2026-11-19. Advisory to the contract, not a release gate (mash-lead #968). Security only; a reread after our rev 24 read (receipts/0.5.6-cli-ux/security-read-rev24.md, bogworks/mash PR #855), asked as a clean-read attempt. Randy's decision 264 stands: the same-OS-user risk is accepted for mash and the plaintext override is his recorded choice; neither was raised as a blocker.
Reviewed: the mash lead's draft contract mash-0.5.6-cli-ux, revision 25 (draft), contract@0.3.4 (lane/lead c86cdae), copy at s3://homerange-xfer/masterplanner/for-herds/mash-0.5.6/contract-0.5.6-cli-ux-rev25-draft.md (sha256 877de328b2117c03c69f670d85cc4c08483eb35a58d207d27b570293c0400783). Not handed over: the helper script and the skill (not landed).
Reviewer: OpenAI Codex, run by kit/codex-review (x-herd#132), `codex exec --sandbox read-only`, codex-cli 0.170.0, model gpt-6-sol, Daybreak ON (codex -c daybreak=true); the attestation was stated to the reviewer in its prompt. Other family from the writer (Anthropic). The reviewer read text and ran nothing.
Handed over (sha256 list at the end): the rev 25 draft; standards from bogworks-dev-standards 68475627ac8c673d9510cdeccd675a44d8765c07 (contract/STANDARD.md 0.3.4, contract/CHECKLIST.md, standing-orders/mash.md, cli/STANDARD.md, skills/contract-critic/SKILL.md); under prior/, the rev 24 draft and our rev 24 read. Nothing else. Where the content went: the pages above were sent to OpenAI through the operator's Codex subscription for this review.

---

# Security read, mash 0.5.6 cli-ux revision 25 (draft)

Reviewer attestation: this security runs on model gpt-6-sol, Daybreak ON (codex -c daybreak=true), codex-cli 0.170.0, read-only sandbox.

I read revision 25 (contract-0.5.6-cli-ux-rev25-draft.md), the revision 24 security read (prior/security-read-rev24.md), and the supplied workspace standards. I read text and ran nothing. The helper, install skill, binaries, target outputs and tests were not supplied.

## Threat model

The assets are signing seeds, endpoint keys and credentials, mail, and the server identity an endpoint trusts. An installing agent may have only “set mash up” as authority. Trust boundaries sit between that request and the agent’s actions; between downloaded material and executed code; between an operator’s hand-off and `credential install`; and between private files, process output, receipts and the network. Other OS users can reach a loopback listener but must authenticate. Randy’s decision 264 accepts the same-OS-user standing risk for mash; this read does not raise it.

## Revision 24 disposition

| Prior item | Revision 25 status |
|---|---|
| Finding 1, concurrent installs: `REQ-003` §3, `INV-003` §5, `AT-007` §6 | **Partly closed; blocking issue remains.** `REQ-003` now says an install refuses while a temporary is “younger than 10 minutes,” and `AT-007` calls for both interleavings. That conflicts with its A-interrupted-then-B-installed test and does not make the check and temporary creation atomic. Finding 1 below. |
| Finding 2, inherited plaintext override: `REQ-010` §3, “Network exposure” §5, `AT-007` §6 | **Open, nonblocking by Randy’s recorded choice.** Inheritance remains expressly allowed, with warnings and a test. |
| Finding 3, terminal ambiguity: `REQ-003` §3, `AT-007` §6 | **Closed and testable.** A first install requires a terminal; a later one runs without it. `AT-007` tests both. |
| Address grammar gap: `REQ-003` §3, `AT-007` §6 | **Closed in text and testable.** Three parts of 1–63 characters plus two dots give the stated 191-character maximum. The lowercase rule matches the contract’s `acme.demo.bob` example and prevents case-fold collisions. `REQ-009` says the address comes from the token; code-level parsing cannot be checked here. |
| Later server/CA comparison gap: `REQ-003` §3 | **Closed in text.** Supplied values must be byte-identical to the configured URL and CA file, with refusal before connection. `AT-007` should explicitly exercise each mismatch. |
| Pending implementation and target-output gap: §3 “Target output,” §8, “Readiness” | **Open as a verification limit.** The contract marks these pending; their actual behavior cannot be verified from this workspace. |

## Findings

**1. CARRIED — blocking; high certainty. `REQ-003` §3; `AT-007` §6. The temporary-file rules cannot satisfy both stated outcomes.** A later install for A creates `.<address>.creds.tmp` and is killed. If B starts immediately, `REQ-003` requires it to refuse because A’s temporary is younger than 10 minutes. Yet `AT-007` requires “endpoint A interrupted before its rename, then endpoint B installed, leaves no temporary of A.” B cannot both refuse and install in that sequence. There is also a race: A and B can each check before either creates its distinct temporary; A can then die, while B commits and leaves A’s young temporary. “Concurrent installs … are not supported” does not define an atomic exclusion step, while `AT-007` demands clean interleavings. **Smallest change:** specify one atomic, config-directory-wide exclusion held through temporary cleanup and commit; define crash recovery and align the A-then-B test with the recovery delay. Test the check-before-create interleaving. State the refusal’s exit code, message and recovery action.

**2. CARRIED — nonblocking; high certainty; accepted choice. `REQ-010` §3; “Network exposure” §5; `AT-007` §6.** A parent process can set `MASH_DANGEROUSLY_SKIP_NATS_ENCRYPTION=1`; a child inherits it and connects to a plaintext-capable server. A process able to capture that connection can read credentials and mail. `REQ-010` permits inheritance and requires a warning before connecting; §7 records Randy’s treatment. **Smallest change if that choice changes:** require a fresh destination-specific choice each run. I do not count this accepted exposure as a ratification blocker.

## Credential and trust inventory

`INV-001` and `INV-003`, §5 require secret files at 0600, prohibit secrets in argv, output, JSON, logs, errors and receipts, and require exclusive, atomic creation. `AT-007`, §6 calls for channel scans. `--key` and `--token` take file paths; `--account` carries a public key.

| §5 credential row | Location and permitted reader | Agent authority and disclosure consequence |
|---|---|---|
| Operator signing seed, account seed, system credentials | mashctl config directory, 0600; mashctl | The agent may run the bounded local helper and `mashctl adopt --from`. Exposure can mint identities and read mail; values must not enter argv, logs or receipts. |
| Admin credentials | mashctl config directory, 0600; mashctl | Local adopt is allowed. Exposure enables endpoint and bucket administration. |
| CA key | Helper directory, 0600; setup user | Default local helper use is allowed. Exposure enables server-certificate minting. |
| Server TLS key | Helper’s server directory, 0600; that user’s `nats-server` | Starting the recorded local server is allowed. Exposure enables server impersonation. |
| CA certificate | Server directory and mash config, 0644; everyone | Public material; the person compares its full fingerprint on first install. |
| Endpoint seed key | Endpoint machine, 0600; mash | An agent may create a local endpoint only for an agent the person named. Exposure allows acting as that endpoint. |
| Endpoint credentials | Mash config directory, 0600; mash | Same local authority. The interrupted temporary and concurrency rules are finding 1. |
| Token (JWT) | Person-carried operator file, 0600; `mash credential install` reads it in place | The agent hands the command to the person and stops. The person deletes the source after install. With its key, the token permits endpoint access. |

There are **no `NFR-002` or `NFR-003` IDs** in revision 25. Their applicable text is §5 “Authority of an agent” and “Network exposure.” On a sparse request, the agent may use pinned, verified official downloads, run the default loopback/TLS helper, adopt its folder, and create local endpoints only for named agents. Another source, another server and deletion need the person’s answer; plugin installation and `credential install` are handed to the person. I found no permission for the agent to widen its own authority. `REQ-011(i)` requires an attested binary route for that agent; `go install` needs the person’s answer, and `REQ-011(iii)` leaves the marketplace plugin’s bytes unverified and its installation to the person.

The helper’s server has one TLS-only client port on `127.0.0.1` and no monitoring, cluster, gateway or leaf-node port. Other local users can attempt connections but must fail authentication; TLS protects connection payloads. The accepted plaintext override exposes credentials and mail to anyone able to observe that connection, including a process with loopback packet-capture rights.

## Gaps and limits

- `REQ-003` §3 does not define recovery for a killed install whose temporary is younger than 10 minutes, or an mtime in the future after a clock rollback. The former blocks an honest retry for up to 10 minutes; the latter can keep it blocked indefinitely under a literal age check. Define the refusal message, exit code and bounded recovery rule. This is part of finding 1’s repair.
- `AT-007` §6 does not explicitly test later `--server` and `--ca` mismatches separately, although `REQ-003` §3 requires byte-identical comparison before connecting.
- The helper, skill, binaries, target outputs and tests are absent. I cannot verify their permissions, token-address parsing, download attestation, output redaction, file modes or network behavior. §8 and “Readiness” already leave review and ratification pending.

**Summary:** One blocking finding and one nonblocking finding, the latter an accepted plaintext choice. In my view, the contradictory temporary-file recovery and concurrent-install rules block ratification until `REQ-003` and `AT-007` agree. All conclusions are from contract text; I ran no implementation or tests.
---

Inputs (sha256):

```
877de328b2117c03c69f670d85cc4c08483eb35a58d207d27b570293c0400783  ./contract-0.5.6-cli-ux-rev25-draft.md
4faa423fa60d82f213b65ef3939e9fa6b75cf8f07d5ac6f4ede59fea684d19ee  ./prior/contract-0.5.6-cli-ux-rev24-draft.md
6b0247e4e960d458b8f5cf0687003c2efe3548c2cffe363f5231fc02471e63f7  ./prior/security-read-rev24.md
9e8d9c48c2fde84244ca65c4ea32c331b2154a184535611d0ffe97a5a037f531  ./standards/cli/STANDARD.md
ed7b2448870e7cf899e4e8dc3b2735ff0ce8d8701844e5cb1e9b2b3152b8f73d  ./standards/contract/CHECKLIST.md
d96d969f78ff7e4eef8fc22a6b7b12db5a8ef3776b9a6a9b970e26ab45a3de85  ./standards/contract/STANDARD.md
9470bf08d732b908689ff2def78c271e852d77e6a1840ad2e5b4a9d7a02647a0  ./standards/skills/contract-critic/SKILL.md
ed1f58552eef3f041f4d37bb709557f6bcd7809f8eb00d5fb08aade797ac68cc  ./standards/standing-orders/mash.md
```
