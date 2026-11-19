# Security reread, mash 0.5.6 work contract (cli-ux) revision 17 draft

Review lane: x-ops2 (operations). Date: 2026-11-19. Advisory to the contract, not a release gate (mash-lead #953). Security only; a reread after our rev 16 read (receipts/0.5.6-cli-ux/security-read-rev16.md, bogworks/mash PR #847). Randy's decision 264 stands: the same-OS-user risk is accepted for mash and was not raised.
Reviewed: the mash lead's draft contract mash-0.5.6-cli-ux, revision 17 (draft), contract@0.3.4 (lane/lead 0f3bbc4), copy at s3://homerange-xfer/masterplanner/for-herds/mash-0.5.6/contract-0.5.6-cli-ux-rev17-draft.md (sha256 106af60e6d0d0e7d7371a1edaec867c8c9024ca3c09d9117161c4fd698191629). Not handed over: the helper script and the skill (not landed). The lead says REQ-015's count definition may still change to "not yet delivered"; the reviewer was asked to judge that change in advance.
Reviewer: OpenAI Codex, run by kit/codex-review (x-herd#132), `codex exec --sandbox read-only`, codex-cli 0.170.0, model gpt-6-sol, Daybreak ON (codex -c daybreak=true); the attestation was stated to the reviewer in its prompt. Other family from the writer (Anthropic). The reviewer read text and ran nothing.
Handed over (sha256 list at the end): the rev 17 draft; standards from bogworks-dev-standards 68475627ac8c673d9510cdeccd675a44d8765c07 (contract/STANDARD.md 0.3.4, contract/CHECKLIST.md, standing-orders/mash.md, cli/STANDARD.md, skills/contract-critic/SKILL.md); under prior/, the rev 16 draft and our rev 16 read. Nothing else. Where the content went: the pages above were sent to OpenAI through the operator's Codex subscription for this review.

---

# Security read, mash 0.5.6 cli-ux revision 17 (draft)

Reviewer attestation: this security runs on model gpt-6-sol, Daybreak ON (codex -c daybreak=true), codex-cli 0.170.0, read-only sandbox.

I read the revision 17 contract (contract-0.5.6-cli-ux-rev17-draft.md), the revision 16 read (prior/security-read-rev16.md), and the supplied standards. I ran no code or tests. The helper, skill, release artifacts, and referenced target-output pages are not in this workspace. This contract has no `NFR-002` or `NFR-003` IDs: their subjects are §5 **“Authority of an agent”** and **“Network exposure.”**

## Threat model

The assets are signing seeds, TLS keys, endpoint credentials, mail, and the choice of server an endpoint trusts. A sparse “set mash up” request crosses an authority boundary when an agent downloads and runs code, creates a local server, or handles credentials. Other host users can reach the loopback listener; anyone able to observe a plaintext connection can read its payload. An operator-provided address and CA cross a separate trust boundary at `credential install`. Randy’s decision 264, recorded in §7, accepts the standing risk from processes acting as the **same OS user**. I do not raise it as a finding.

## Revision 16 disposition

“Closed” here means closed by testable contract words, not verified in code.

| Prior finding | Rev 17 status |
|---|---|
| `R16-1` | **Closed in text.** `REQ-003` calls `credential install` a person’s command; §5 “Authority of an agent” and `AT-007` require a sparse-request agent to hand over the command and stop. `REQ-001` explicitly permits the two comparison screens. The test can observe whether the agent invokes the command, though text alone cannot enforce who operates a terminal. |
| `R16-2` | **Partly closed.** `REQ-003` makes a refused or unreachable connection and any lookup error write nothing; `AT-007` tests refusal and unreachability. The accepted-connection argument is conditional on **operator mode**, while the contract does not require that mode or define an account lookup. See finding 1. |
| `R16-3` | **Closed in text.** `REQ-011(iii)`, §5, and `AT-007` say the person runs the two plugin commands and a sparse-request agent does not install it. The manifest check is separately testable. |
| `R16-4` | **Open, nonblocking.** `REQ-010` still permits an inherited plaintext override, with an explicit warning and exposure statement. See finding 3. |
| `R16-5` | **Closed for restart bursts; first-wake behavior remains unclear.** `REQ-015` and `AT-008` suppress a wake for ten minutes after **each** start, so a rapid crash loop cannot wake once per start. A running process can wake after that interval. See finding 2. |

## Findings

**1. CARRIED `R16-2` — blocking; high certainty. `REQ-003`, §3; `AT-007`, §6.** A server with a CA and hostname the person has accepted could allow the connection without the operator-mode rule described in `REQ-003`, or could accept a valid token from a different account it holds. Install would then treat acceptance as proof of the intended account and commit the files. The contract says what acceptance proves **“in operator mode”** but neither requires operator mode nor identifies the intended account to check. Its “any lookup error” rule is testable as a no-write result, but no lookup is specified; the three `AT-007` cases do not test an accepting non-operator server or an accepting wrong-account server. **Smallest change:** require an operator-mode server for this install path and define how install identifies and verifies the intended account before any write; add accepting non-operator and wrong-account cases that must write nothing. Verified TLS proves possession of the supplied server key and protects that connection. It does not make a hostile server trustworthy if the person was given and accepted that server’s address and CA.

**2. NEW — nonblocking; high certainty. `REQ-015`, §3; `AT-008`, §6.** If a mailbox is already over the limit when `mash serve` starts, the text both says it **“wakes the session at once”** and prohibits a wake until ten minutes after start. An implementer can suppress the first wake for ten minutes while satisfying the restart test, leaving an idle session with an existing backlog asleep. This is a reliability failure, not a demonstrated way for a sender alone to trigger repeated wakes: the sender is not given control of restarts. **Smallest change:** state explicitly whether an over-limit backlog at startup receives a wake after the ten-minute hold, and test that first eligible wake. The current per-start bound is suitable for stopping rapid restart bursts.

**3. CARRIED `R16-4` — nonblocking; high certainty. `REQ-010`, §3; §5 “Network exposure”; `AT-007`, §6.** A parent process can supply `MASH_DANGEROUSLY_SKIP_NATS_ENCRYPTION=1`. If the selected server accepts plaintext, a process able to capture that connection can read credentials and mail despite the warning. The contract explicitly permits inherited values and specifies this consequence; it is therefore an exposed choice, not an ambiguity in implementation. **Smallest change if the writer elects to close it:** require a fresh, destination-specific plaintext choice. `AT-007` currently tests the inherited-value warning.

## Credential and trust inventory

`INV-001` and `INV-003`, §5, prohibit secret **values** in argv, output, errors, logs, JSON, examples, and receipts; `AT-007` scans those channels. Commands may carry file **paths** in argv. Modes below restrict direct reads by other OS users; the accepted same-user risk is in decision 264.

| §5 credential row | Location, reader, permitted agent action, exposure |
|---|---|
| Operator signing seed, account seed, system credentials | `mashctl` config directory, 0600; `mashctl` reads them. On a sparse request the agent may run the bounded helper and `mashctl adopt --from`, which copies required material. Leakage permits minting identities and reading mail. Values must not enter argv, logs, or receipts. |
| Admin credentials | `mashctl` config directory, 0600; `mashctl` reads them to administer endpoints and buckets. The agent may run `adopt` for the local setup. Values must not enter argv, logs, or receipts; `mashctl status --detail` may show the path. |
| CA key | Helper directory, 0600; the owner keeps it. The agent may create it through the default local helper, with no wider-server or backup authority. Leakage permits minting server certificates. Its value must not enter diagnostics or receipts. |
| Server TLS key | Helper server directory, 0600; the owner’s `nats-server` reads it. The agent may start that server through the helper. Leakage permits server impersonation; its value must not enter output or logs. |
| CA certificate | Server directory and mash config, 0644; everyone may read this public material. The agent may copy it during setup. Its path can appear in a command; `REQ-003` puts its fingerprint and the server address on the person’s first-install screen. |
| Endpoint seed key | Endpoint machine, 0600; mash reads it. The agent may create an endpoint **on this machine for an agent the person named**. `--key` carries a path, never the key value. Leakage permits acting as the endpoint. |
| Endpoint credentials | Mash config directory, 0600; mash reads them. The agent may create local, person-named endpoints; the person runs `credential install` on the other-machine path. Values must not enter argv, logs, or receipts. |
| Token (JWT) | Person-carried operator file, 0600; `credential install` reads it in place and makes no separate copy. The agent hands over the exact install command and stops; the person runs it and deletes the token file afterward. With the endpoint key, leakage permits acting as that endpoint. The token value must not enter argv, logs, or receipts. |

Under §5 “Authority of an agent,” a sparse request permits verified official downloads, the default loopback/TLS helper, `adopt`, and local endpoints for agents the person named. Another release source, another server, and deletion require the person’s own answer; plugin installation and `credential install` are person-run commands. There is no remaining clause that lets the agent grant itself a wider permission. The sparse test can check the commands the agent actually issues and where it stops. The contract’s person-run rule is an instruction and test obligation, not a claim that the CLI can distinguish a person from an agent driving that person’s terminal; decision 264 covers that standing risk.

Under §5 “Network exposure,” the helper has one TLS client port on `127.0.0.1`, with no monitoring, cluster, gateway, or leaf-node port. Other local users can connect to that port but must authenticate; they cannot read another TLS connection merely by connecting. A wider server belongs to the operator’s separate path. The plaintext override exposes credentials and mail to an observer of that connection, including a process with loopback packet-capture rights.

## Gaps and change watch

- `REQ-015`’s present count is **pending plus delivered-but-unacknowledged**. Changing it to “not yet delivered” would let a sender fill mail already delivered to a consumer but never acknowledged without increasing the reported backlog or causing the threshold wake. The opposite failure—counts that never fall—arises if acknowledgement is omitted or ineffective. Keep the current definition and test both delivery without acknowledgement and acknowledgement lowering the count. `REQ-015` says the count is mash’s own, so a hostile remote endpoint should not supply it.
- `AT-007` now tests another owner’s explicit `mash endpoint status` address returning `unknown`. It does not test a pattern or listing that mixes owned and foreign endpoints, or a failed caller-credential lookup. Add those cases to exercise the full `REQ-015` owner boundary.
- `REQ-011(i)` and `REQ-002` now distinguish installing binaries into the user’s directory from the helper’s rule to write only inside its own directory. §5 now unambiguously excludes wider helper ports. Those rev 16 gaps are closed in text.
- The referenced status-view targets and the unlanded helper and skill cannot be checked here for contradictory output, permissions, download handling, or file modes. `AT-001`’s live journey should record the person-run plugin step consistently with `REQ-011(iii)` and §5; `AT-007` covers the sparse agent stop.

## Summary

I find **one blocking** and **two nonblocking** findings. In my view, the undefined account assurance in `REQ-003` blocks ratification until clarified or expressly accepted by the ratifier; this read is a finding, not a ratification decision. Decision 264’s same-OS-user risk remains accepted. This was a text-only review: I could not verify the helper, skill, plugin bytes, server behavior, or acceptance tests, and I ran nothing.
---

Inputs (sha256):

```
106af60e6d0d0e7d7371a1edaec867c8c9024ca3c09d9117161c4fd698191629  ./contract-0.5.6-cli-ux-rev17-draft.md
7bf582f0c0f91f01d7568611942944cace42fd2a467bbfa37c4e6d9da7828b2a  ./prior/contract-0.5.6-cli-ux-rev16-draft.md
3615b9a341c34254f5be9747e7b14496e99414c2e555f677c945299abd6bf076  ./prior/security-read-rev16.md
9e8d9c48c2fde84244ca65c4ea32c331b2154a184535611d0ffe97a5a037f531  ./standards/cli/STANDARD.md
ed7b2448870e7cf899e4e8dc3b2735ff0ce8d8701844e5cb1e9b2b3152b8f73d  ./standards/contract/CHECKLIST.md
d96d969f78ff7e4eef8fc22a6b7b12db5a8ef3776b9a6a9b970e26ab45a3de85  ./standards/contract/STANDARD.md
9470bf08d732b908689ff2def78c271e852d77e6a1840ad2e5b4a9d7a02647a0  ./standards/skills/contract-critic/SKILL.md
ed1f58552eef3f041f4d37bb709557f6bcd7809f8eb00d5fb08aade797ac68cc  ./standards/standing-orders/mash.md
```
