# Security reread, mash 0.5.6 work contract (cli-ux) revision 21 draft

Review lane: x-ops2 (operations). Date: 2026-11-19. Advisory to the contract, not a release gate (mash-lead #959). Security only; a reread after our rev 20 read (receipts/0.5.6-cli-ux/security-read-rev20.md, bogworks/mash PR #850), asked as a clean-read attempt. Randy's decision 264 stands: the same-OS-user risk is accepted for mash and was not raised.
Reviewed: the mash lead's draft contract mash-0.5.6-cli-ux, revision 21 (draft), contract@0.3.4 (lane/lead ef467d7), copy at s3://homerange-xfer/masterplanner/for-herds/mash-0.5.6/contract-0.5.6-cli-ux-rev21-draft.md (sha256 f7ae5ee630ede34c581b4e72a2d23843abca388a798a1b637ad51747dc4cdf37). Not handed over: the helper script and the skill (not landed).
Reviewer: OpenAI Codex, run by kit/codex-review (x-herd#132), `codex exec --sandbox read-only`, codex-cli 0.170.0, model gpt-6-sol, Daybreak ON (codex -c daybreak=true); the attestation was stated to the reviewer in its prompt. Other family from the writer (Anthropic). The reviewer read text and ran nothing.
Handed over (sha256 list at the end): the rev 21 draft; standards from bogworks-dev-standards 68475627ac8c673d9510cdeccd675a44d8765c07 (contract/STANDARD.md 0.3.4, contract/CHECKLIST.md, standing-orders/mash.md, cli/STANDARD.md, skills/contract-critic/SKILL.md); under prior/, the rev 20 draft and our rev 20 read. Nothing else. Where the content went: the pages above were sent to OpenAI through the operator's Codex subscription for this review.

---

# Security read, mash 0.5.6 cli-ux revision 21 (draft)

Reviewer attestation: this security runs on model gpt-6-sol, Daybreak ON (codex -c daybreak=true), codex-cli 0.170.0, read-only sandbox.

I read the workspace contract, rev 20 read and draft, and supplied standards. I ran no helper, install, code, or tests. The helper and skill have not landed, so their behavior remains unverified.

## Threat model

The assets are signing seeds, endpoint credentials, mail, and the choice of server and account an endpoint trusts. An installing agent may start from “set mash up” with little authority. Trust crosses from the operator’s separate hand-off to the person’s confirmation, then through TLS and NATS authentication to local files. Other OS users can reach a loopback port; a process able to capture plaintext traffic can read it. Randy’s decision 264 accepts the same-OS-user standing risk for mash; I do not raise it.

## Revision 20 disposition

“Closed” means the revision 21 words specify a check, not that code passed it.

| Rev 20 item | Revision 21 status and testability |
|---|---|
| Finding 1, shortened account key | **Partly closed.** `REQ-003`, §3 now requires the complete, copyable 56-character key and says the token issuer equals the operator’s separately sent full key byte for byte. `AT-007`, §6 adds a difference outside the former shortened portion. The command still has no way to receive that independent comparison value; the test can establish only what the person answers. Finding 1 below. |
| Finding 2, missing hand-off value | **Closed.** `JRN-002`, §2 and `AT-002`, §6 now name the account key; `AT-007`, §6 names it on the screen. This is testable as output and a two-home hand-off. |
| Finding 3, inherited plaintext override | **Open, nonblocking.** `REQ-010`, §3 still permits an inherited value; `AT-007`, §6 tests the warning, not a fresh choice. Finding 2 below. |
| Gap, certificate signed by another CA | **Closed.** `AT-007`, §6 explicitly requires no write. Testable. |
| Gap, interrupted states before rerun | **Closed in the acceptance text.** `AT-007`, §6 says every mash command ignores each interrupted state before rerun. Testable, although the implementation is unavailable. |
| Gap, unlanded helper and skill | **Open as a verification limit.** Their downloads, permissions, output, and actual agent behavior cannot be inspected here. |

The earlier rev 20 inventory also recorded closed source-file checks, backlog semantics, and the Readiness wording. Revision 21 retains those requirements in `REQ-003`, `REQ-015`, `AT-007`, `AT-008`, and “Readiness.”

## Findings

**1. CARRIED — blocking; high certainty. `REQ-003`, §3; `AT-007`, §6. Independent account-key equality has no specified input.** The command takes `--key`, `--token`, `--server`, and `--ca`; it does not take the operator’s separately sent account key. An implementation can display the token’s issuer, ask whether it matches, accept “yes” for a different issuer, and claim it required equality to “the full account key the person confirmed.” The person’s confirmation is expressly the first-install root of trust, but the separate value is never available to install for the stated byte-for-byte check. The new `AT-007` mismatch case can pass simply because its test person answers “no.” **Smallest change:** require the person to supply or paste the separately sent full public key, define that as the comparison input, and refuse before writing if its bytes differ from the token issuer. Keep the three-value confirmation. Test a differing issuer with an affirmative confirmation attempt.

**2. CARRIED — nonblocking; high certainty. `REQ-010`, §3; §5 “Network exposure”; `AT-007`, §6. Inherited plaintext remains sufficient.** A parent sets `MASH_DANGEROUSLY_SKIP_NATS_ENCRYPTION=1`; a child command inherits it and connects to a server that accepts plaintext. The warning does not require a fresh choice. A process with packet-capture rights on loopback, or an observer on a wider plaintext route, can read credentials and mail. The contract expressly permits inherited values and tests only their warning. **Smallest change if this exposure is to be closed:** require a destination-specific choice on that run; an inherited variable alone would not authorize plaintext. Section 7 records Randy’s treatment of the override.

**3. NEW — blocking; medium certainty. `REQ-011(i)`, §3; §5 “Authority of an agent”; `AT-007`, §6. A verified binary may be installed in a directory another OS user can write.** The sparse-request agent may download, verify, and install release binaries into “the user’s own directory.” Ownership alone does not rule out a directory writable by other users or a destination reached through a symlink. Another user can replace a verified executable after installation, and the next invocation runs their code. `AT-007` checks substituted downloads, but not the destination. **Smallest change:** require the installer to refuse a symlinked or other-user-writable destination and parent path, and test that refusal. This concerns other OS users, outside decision 264’s accepted same-user risk.

## Credential and trust inventory

`INV-001` and `INV-003`, §5 prohibit secret values in argv, stdout, stderr, JSON, logs, errors, examples, and receipts; `AT-007`, §6 scans those channels. Paths may be arguments or appear in the specified detail and failure output.

| §5 credential row | Location and readers; agent authority and exposure |
|---|---|
| Operator signing seed, account seed, system credentials | mashctl config, 0600; mashctl reads them. A sparse-request agent may run the bounded helper and local `mashctl adopt --from`, which copies needed material. A value in argv, logs, or receipts could enable identity minting and mail access. |
| Admin credentials | mashctl config, 0600; mashctl reads them. Local adopt is permitted. A leaked value permits endpoint and bucket administration; `status --detail` may name its path, not its value. |
| CA key | Helper directory, 0600; the setup user keeps it. The agent may create it through the default local helper. A leak permits minting server certificates. |
| Server TLS key | Helper server directory, 0600; that user’s `nats-server` reads it. The agent may start the recorded local server. A leak permits server impersonation. |
| CA certificate | Server directory and mash config, 0644; everyone may read it. The agent may copy it; it is public. The person compares its full fingerprint. |
| Endpoint seed key | Endpoint machine, 0600; mash reads it. The agent may create a same-machine endpoint for a person-named agent. `--key` carries a path; a leaked value permits acting as the endpoint. |
| Endpoint credentials | Mash config, 0600; mash reads them. The agent may create local, person-named endpoints. Secret values must stay out of argv, logs, and receipts. |
| Token (JWT) | Person-carried operator file, 0600; `mash credential install` reads it in place, without a separate copy. The agent hands over the command; the person runs it and deletes the source afterward. `--token` carries a path. A leaked token with its key permits acting as the endpoint. |

Section 5 “Authority of an agent,” read with `REQ-002`, `REQ-003`, and `REQ-011`, does not otherwise let a sparse-request agent widen its own authority: another source or server needs the person’s answer; plugin installation, credential install, and deletion are handed to the person. Section 5 “Network exposure” specifies one TLS client port on `127.0.0.1`, with no monitoring, cluster, gateway, or leaf-node port. Other local users can attempt connections but are refused without credentials and cannot read another TLS connection’s payload. The plaintext override exposes credentials and mail to anyone able to observe that connection. **The contract has no `NFR-002` or `NFR-003` IDs**; these §5 headings and `REQ-002`/`REQ-010` are the applicable authority and network requirements.

## Gaps and consistency watch

- `REQ-003`, §3 states the honest first-install trust root: the operator separately sends address, CA fingerprint, and full account key; the person confirms all three; TLS, authentication, and issuer checks then establish consistency with that choice. Finding 1 is the remaining missing input needed to make the stated independent issuer comparison enforceable.
- `REQ-003`, §3 calls `config.json` the commit point, while also allowing installation of another endpoint when a config already exists. `AT-007`, §6 requires every mash command to ignore *each* interrupted state. The contract should state how the existing config marks a later endpoint install as incomplete; otherwise a newly written credentials file can be visible before that install finishes.
- The helper and install skill are absent. The contract’s pinned-download, redaction, file-mode, port, and stopping rules are reviewable requirements, but their implementation and run evidence remain unverified.
- The removed trust record, generations, directory lookup, reset, executing install script, and plugin-install command appear only as removal or historical context. I found no live requirement reinstating them. Typed confirmation remains for deletion and exposure, plus the expressly required first-install comparison.

I find **two blocking and one nonblocking finding**. In my view, ratification should wait for an enforceable independent account-key input and a safe binary destination rule. The rev 20 hand-off omission and both named test gaps are closed in text; the inherited plaintext exposure remains nonblocking. This is a read-only contract review, not verification of the unlanded helper, skill, binaries, or tests.
---

Inputs (sha256):

```
f7ae5ee630ede34c581b4e72a2d23843abca388a798a1b637ad51747dc4cdf37  ./contract-0.5.6-cli-ux-rev21-draft.md
bf9b8752d5928a905805d4fae35bf4d7d798cd8352430d05607e4595a6fadefe  ./prior/contract-0.5.6-cli-ux-rev20-draft.md
54b831e0db444545223a8924b5efdad87939699bf3b0a915f2de3579e786db47  ./prior/security-read-rev20.md
9e8d9c48c2fde84244ca65c4ea32c331b2154a184535611d0ffe97a5a037f531  ./standards/cli/STANDARD.md
ed7b2448870e7cf899e4e8dc3b2735ff0ce8d8701844e5cb1e9b2b3152b8f73d  ./standards/contract/CHECKLIST.md
d96d969f78ff7e4eef8fc22a6b7b12db5a8ef3776b9a6a9b970e26ab45a3de85  ./standards/contract/STANDARD.md
9470bf08d732b908689ff2def78c271e852d77e6a1840ad2e5b4a9d7a02647a0  ./standards/skills/contract-critic/SKILL.md
ed1f58552eef3f041f4d37bb709557f6bcd7809f8eb00d5fb08aade797ac68cc  ./standards/standing-orders/mash.md
```
