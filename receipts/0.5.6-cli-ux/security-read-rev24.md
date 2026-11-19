# Security reread, mash 0.5.6 work contract (cli-ux) revision 24 draft

Review lane: x-ops2 (operations). Date: 2026-11-19. Advisory to the contract, not a release gate (mash-lead #967). Security only; a reread after our rev 23 read (receipts/0.5.6-cli-ux/security-read-rev23.md, bogworks/mash PR #854), asked as a clean-read attempt. Randy's decision 264 stands: the same-OS-user risk is accepted for mash and was not raised.
Reviewed: the mash lead's draft contract mash-0.5.6-cli-ux, revision 24 (draft), contract@0.3.4 (lane/lead 1bca3e8), copy at s3://homerange-xfer/masterplanner/for-herds/mash-0.5.6/contract-0.5.6-cli-ux-rev24-draft.md (sha256 4faa423fa60d82f213b65ef3939e9fa6b75cf8f07d5ac6f4ede59fea684d19ee). Not handed over: the helper script and the skill (not landed).
Reviewer: OpenAI Codex, run by kit/codex-review (x-herd#132), `codex exec --sandbox read-only`, codex-cli 0.170.0, model gpt-6-sol, Daybreak ON (codex -c daybreak=true); the attestation was stated to the reviewer in its prompt. Other family from the writer (Anthropic). The reviewer read text and ran nothing.
Handed over (sha256 list at the end): the rev 24 draft; standards from bogworks-dev-standards 68475627ac8c673d9510cdeccd675a44d8765c07 (contract/STANDARD.md 0.3.4, contract/CHECKLIST.md, standing-orders/mash.md, cli/STANDARD.md, skills/contract-critic/SKILL.md); under prior/, the rev 23 draft and our rev 23 read. Nothing else. Where the content went: the pages above were sent to OpenAI through the operator's Codex subscription for this review.

---

# Security read, mash 0.5.6 cli-ux revision 24 (draft)

Reviewer attestation: this security runs on model gpt-6-sol, Daybreak ON (codex -c daybreak=true), codex-cli 0.170.0, read-only sandbox.

I started with §5’s credential inventory, agent authority, and network exposure, then compared revision 23’s read (prior/security-read-rev23.md) with the full revision 24 contract (contract-0.5.6-cli-ux-rev24-draft.md). I read text and ran nothing. The helper, install skill, binaries, and tests were not supplied.

## Threat model

Signing seeds, endpoint keys, credentials, mail, and the server an endpoint trusts are the assets. An installing agent may have only a sparse “set mash up” request. The person’s separately supplied server, CA, and account values cross a trust boundary at `credential install`; downloaded binaries and plugin material cross a supply-chain boundary. Other OS users can reach a loopback port, while files, argv, output, logs, and receipts can disclose secrets. Randy’s decision 264 accepts the same-OS-user standing risk for mash; I do not raise it as a finding.

## Revision 23 disposition

| Revision 23 item | Revision 24 status |
|---|---|
| Finding 1: cross-address stale credential temporary | **Partly closed.** `REQ-003`, §3 now requires removal for “any address” and refusal of unexpected types without following them. `AT-007`, §6 tests A interrupted, then B installed. Neither covers two honest installs running concurrently; finding 1 below remains. |
| Finding 2: inherited plaintext override | **Open, nonblocking.** `REQ-010`, §3 expressly permits inheritance; `AT-007`, §6 tests its warning. §7 records Randy’s treatment. |
| Finding 3: later-install prompt ambiguity | **Closed in the requirement.** `REQ-003`, §3 says a later install “does not prompt again” and compares `--account` locally before connecting. `AT-007`, §6 tests the comparison. Its unqualified no-terminal test still needs alignment, as finding 3 below explains. |
| Gap: local issuer comparison | **Closed and testable in text.** `REQ-003`, §3 names the token’s `iss` claim, local parsing, and “no lookup”; `AT-007`, §6 covers issuer mismatch before connection. |
| Gap: early TLS handshake and failure | **Closed and testable in text.** `REQ-003`, §3 permits an unauthenticated handshake carrying neither key nor token; `AT-007`, §6 requires no writes after a post-confirmation TLS failure. |
| Gap: temporary-name input and unexpected types | **Partly closed.** `REQ-003`, §3 gives the character shape and refuses unexpected temporary types; `AT-007`, §6 tests symlink and directory refusal. The numeric length and exact grammar remain unspecified. |
| Gap: pending targets and implementation | **Open as a verification limit.** §8 and Readiness still await target-output reviews and ratification. The helper and skill are absent here. |

## Findings

**1. CARRIED — blocking; high certainty. `REQ-003`, §3; `INV-003`, §5; `AT-007`, §6 — concurrent installs defeat the temporary-file guarantee.** Two people can start later installs for endpoints A and B in the same config directory. B can scan and remove stale files, then A creates `.<address>.creds.tmp` and is interrupted. B can commit and report success while A’s secret-bearing temporary remains. In the opposite order, B can remove A’s *live* temporary and make A fail at rename. The contract says cleanup occurs “before any later install reports success” but never distinguishes stale from live files or serializes installs. The A-then-B test is sequential, so it cannot catch either interleaving. **Smallest change:** serialize `credential install` per config directory from temporary creation through cleanup and commit, or require a second install to refuse before writing while another is active. Test both interleavings.

**2. CARRIED — nonblocking; high certainty. `REQ-010`, §3; §5 “Network exposure”; `AT-007`, §6 — an inherited override can send credentials and mail in plaintext.** A parent process sets `MASH_DANGEROUSLY_SKIP_NATS_ENCRYPTION=1`; a child inherits it and connects to a plaintext-capable server. Its warning does not require a fresh choice. A process with packet-capture rights on loopback, or an observer on a wider plaintext route, can read that connection’s credentials and mail. `REQ-010` explicitly permits inherited values, and §7 records Randy’s treatment. **Smallest change if this exposure is to be closed:** require an explicit choice for that destination on each run; inheritance alone would not enable plaintext.

**3. NEW — nonblocking; high certainty. `REQ-003`, §3; `AT-007`, §6 — the later-install no-terminal case is inconsistent.** `REQ-003` says a later install “does not prompt again,” while `AT-007` says, without limiting the case, “there is no terminal (exit 4).” An implementation could accept a later install without a terminal because it has no prompt, while the test could reject it. This matters because §3 calls `credential install` “a person’s command” and §5 says an agent never runs it. **Smallest change:** state whether every invocation requires a terminal independently of prompting, and test first and later installs separately. This does not reopen decision 264’s accepted risk of an agent driving a terminal.

## Credential and trust inventory

`INV-001` and `INV-003`, §5 bar seeds, JWTs, and private keys from argv, output, JSON, logs, errors, examples, and receipts; `AT-007`, §6 requires channel scans. `--key` and `--token` take file paths. `--account` takes a complete *public* key in argv.

| §5 credential row | Location and readers | Agent authority and disclosure path |
|---|---|---|
| Operator signing seed, account seed, system credentials | mashctl config, 0600; mashctl reads them | The agent may use the bounded helper and `mashctl adopt --from`. Leaked values can mint identities or expose mail; they must stay out of argv, logs, and receipts. |
| Admin credentials | mashctl config, 0600; mashctl reads them | Local adopt is allowed. Leakage enables endpoint and bucket administration. |
| CA key | Helper directory, 0600; setup user reads it | Default local helper use is allowed. Leakage enables server-certificate minting. |
| Server TLS key | Helper server directory, 0600; that user’s `nats-server` reads it | Starting the recorded local server is allowed. Leakage enables server impersonation. |
| CA certificate | Server directory and mash config, 0644; everyone can read it | Copying it is allowed; it is public. The person compares its full fingerprint on first install. |
| Endpoint seed key | Endpoint machine, 0600; mash reads it | A local endpoint for a person-named agent may be created. The key must stay out of argv, logs, and receipts. |
| Endpoint credentials | Mash config, 0600; mash reads them | Local endpoint creation is allowed. The temporary-copy race is finding 1. |
| Token (JWT) | Person-carried operator file, 0600; install reads it in place | The agent hands the install command to the person and stops. The person deletes the source after install. With its key, the token enables endpoint access. |

The revision 24 contract has **no `NFR-002` or `NFR-003` IDs**. The applicable §5 paragraphs are “Authority of an agent” and “Network exposure.” On a sparse request, the former allows an agent to verify pinned official releases, run the default loopback/TLS helper, adopt its folder, and create local endpoints only for agents the person named. Another source, another server, deletion, plugin installation, and `credential install` require the person’s action or answer. I found no clause letting the agent widen that authority itself. `REQ-011(i)`, §3 requires attested mash binaries for that agent path; `go install` is an unverified developer route requiring the person’s answer. `REQ-011(iii)`, §3 leaves marketplace plugin bytes unverified and makes installation the person’s command.

The helper’s listener is TLS-only on `127.0.0.1`, with one client port and no monitoring, cluster, gateway, or leaf-node port. Other local users can attempt connections but must fail authentication; TLS protects other connections’ payloads. The plaintext override exposes credentials and mail to an observer able to capture that connection, including a process with loopback packet-capture rights.

## Gaps and limits

- `REQ-003`, §3 calls the address grammar “bounded length” without a numeric maximum or a rule for case-folding collisions on filesystems that fold case. Specify the maximum per component and in total, the accepted hyphen positions, and collision handling; test invalid and colliding addresses before filename use.
- `AT-007`, §6 tests sequential A-then-B cleanup but is silent on concurrent installs. Finding 1 gives the missing test and rule.
- `REQ-003`, §3 binds later installs to the configured server and CA: omitted values use them, and supplied values “must match them.” It should say whether match means exact configured URL and CA bytes or a normalized equivalence, so an implementer cannot silently select a different endpoint of trust. A mismatch must be refused before connection.
- The target-output files, status-views page, helper, skill, and code are not in this workspace. Their behavior, permission manifest, tests, and the contract’s external references could not be verified. §8 and Readiness already mark review and ratification pending.

**Summary:** One blocking and two nonblocking findings. In my view, ratification should wait for a concurrency rule and test for `REQ-003`; the sequential cleanup change closes the revision 23 example but not its guarantee under simultaneous installs. All conclusions are about contract text; I ran no implementation or tests.
---

Inputs (sha256):

```
4faa423fa60d82f213b65ef3939e9fa6b75cf8f07d5ac6f4ede59fea684d19ee  ./contract-0.5.6-cli-ux-rev24-draft.md
31f89534fc72bcab5902c1e575dbd4c7665c05cc19c69df509d0d8d13c8ba8e9  ./prior/contract-0.5.6-cli-ux-rev23-draft.md
0e045aec318b70f5970855119cac4e561dcf3f351b0624692f1651adbd90b542  ./prior/security-read-rev23.md
9e8d9c48c2fde84244ca65c4ea32c331b2154a184535611d0ffe97a5a037f531  ./standards/cli/STANDARD.md
ed7b2448870e7cf899e4e8dc3b2735ff0ce8d8701844e5cb1e9b2b3152b8f73d  ./standards/contract/CHECKLIST.md
d96d969f78ff7e4eef8fc22a6b7b12db5a8ef3776b9a6a9b970e26ab45a3de85  ./standards/contract/STANDARD.md
9470bf08d732b908689ff2def78c271e852d77e6a1840ad2e5b4a9d7a02647a0  ./standards/skills/contract-critic/SKILL.md
ed1f58552eef3f041f4d37bb709557f6bcd7809f8eb00d5fb08aade797ac68cc  ./standards/standing-orders/mash.md
```
