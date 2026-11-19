# Security reread, mash 0.5.6 work contract (cli-ux) revision 19 draft

Review lane: x-ops2 (operations). Date: 2026-11-19. Advisory to the contract, not a release gate (mash-lead #957). Security only; a reread after our rev 17 read (receipts/0.5.6-cli-ux/security-read-rev17.md, bogworks/mash PR #848). Revision 18 (install asks the person to confirm the address and fingerprint before any write) was an intermediate draft read as part of revision 19. Randy's decision 264 stands: the same-OS-user risk is accepted for mash and was not raised.
Reviewed: the mash lead's draft contract mash-0.5.6-cli-ux, revision 19 (draft), contract@0.3.4 (lane/lead ef81925), copy at s3://homerange-xfer/masterplanner/for-herds/mash-0.5.6/contract-0.5.6-cli-ux-rev19-draft.md (sha256 c5447da8542aa12d8c9d41ed4acb5ed5f732f44a7e8949396586ca7527fdccc3). Not handed over: the helper script and the skill (not landed).
Reviewer: OpenAI Codex, run by kit/codex-review (x-herd#132), `codex exec --sandbox read-only`, codex-cli 0.170.0, model gpt-6-sol, Daybreak ON (codex -c daybreak=true); the attestation was stated to the reviewer in its prompt. Other family from the writer (Anthropic). The reviewer read text and ran nothing.
Handed over (sha256 list at the end): the rev 19 draft; standards from bogworks-dev-standards 68475627ac8c673d9510cdeccd675a44d8765c07 (contract/STANDARD.md 0.3.4, contract/CHECKLIST.md, standing-orders/mash.md, cli/STANDARD.md, skills/contract-critic/SKILL.md); under prior/, the rev 17 draft and our rev 17 read. Nothing else. Where the content went: the pages above were sent to OpenAI through the operator's Codex subscription for this review.

---

# Security read, mash 0.5.6 cli-ux revision 19 (draft)

Reviewer attestation: this security runs on model gpt-6-sol, Daybreak ON (codex -c daybreak=true), codex-cli 0.170.0, read-only sandbox.

I read the revision 19 contract (contract-0.5.6-cli-ux-rev19-draft.md), the revision 17 security read (prior/security-read-rev17.md), its prior contract, and the supplied standards. I ran no code or tests. The helper, install skill, release artifacts, and target-output pages are absent, so their behavior is unverified. This contract has no `NFR-002` or `NFR-003` IDs; the relevant text is §5 **“Authority of an agent”** and **“Network exposure.”**

## Threat model

The assets are signing seeds, endpoint keys and credentials, mail, and the endpoint’s choice of server. A sparse “set mash up” request gives an installing agent limited authority to download and run software. The other-machine hand-off crosses a separate trust boundary: a person supplies a server address and CA, while a token and key establish an endpoint identity. Other host users can reach the loopback listener, and a process able to capture a plaintext connection can see its contents. Randy’s decision 264, recorded in §7, accepts the same-OS-user standing risk; I do not raise it.

## Revision 17 disposition

“Closed” means the words are testable, not that implementation was verified.

| Revision 17 item | Revision 19 status |
|---|---|
| Finding 1, `R17-1` | **Partly closed.** `REQ-003`, §3 now refuses a server without required authentication and a server without a matching address/public-key directory record; it mandates no-write tests. A valid token for another account **with its own matching record** can still pass. The record also cannot prove who wrote it when the accepted server controls the directory. Findings 1–2 below. |
| Finding 2, startup backlog wake | **Closed in text.** `REQ-015`, §3 says an over-limit backlog still present ten minutes after start wakes once; `AT-008`, §6 tests it. |
| Finding 3, inherited plaintext override | **Open, nonblocking.** `REQ-010`, §3 still permits an inherited `MASH_DANGEROUSLY_SKIP_NATS_ENCRYPTION=1`, with a warning; `AT-007`, §6 tests the warning. Finding 4 below. |
| `REQ-015` count advice | **Closed in text.** `REQ-015`, §3 counts pending plus delivered-but-unacknowledged mail. Full delivery to session context, the read tool, or `mash read` acknowledges; a digest line does not. `AT-008`, §6 tests delivery without acknowledgement and acknowledgement lowering the count. |
| Owner-boundary gap | **Closed in text.** `AT-007`, §6 adds mixed owned/foreign patterns and failed caller-credential lookup, both returning `unknown` where caller credentials cannot read. |
| Unlanded-artifact gap | **Open as a verification limit.** The helper and skill are still absent. Their download, mode, permission, and output behavior cannot be verified from this workspace. |

## Findings

**1. CARRIED from `R17-1` — blocking; high certainty. `REQ-003`, §3; `AT-002` and `AT-007`, §6.** Suppose an attacker has a legitimate token and matching key for another account on the **genuine server**, and that account has a directory record for its endpoint. The attacker hands that token to Bob while the genuine server address and CA still match what Bob confirms. Install can pass TLS, operator-mode authentication, and the stated address/public-key record check, then commit credentials for the attacker’s account. `REQ-003` names the record as one “the operator’s mashctl wrote,” but the check as specified establishes a record under the **token’s claimed issuer account**, not that this is the account Bob intended. The existing wrong-account tests cover a missing record, not a matching record in the wrong account. **Smallest change:** have mashctl hand over an independently comparable operator/account public identity with the address and CA fingerprint; require install to show and confirm that identity and to match the token issuer to it before any write. Add an authenticated, other-account token **with a matching record** that must write nothing. The directory record then checks consistency within the account the person chose.

A hostile server whose address and CA the person confirms can fabricate its own directory record. Verified TLS and the record do not authenticate that server beyond the person’s address-and-fingerprint decision. `REQ-003` should call that decision the trust root and describe the record as a consistency check, rather than attributing its authorship to mashctl as something install can prove.

**2. NEW — blocking; medium-high certainty. `REQ-003`, §3; `INV-002`, §5; `AT-007`, §6.** Install reads mash’s directory **as the new endpoint, before writing local credentials**. The contract does not say what directory permission makes that read possible or confines it. An implementer could make the check work by granting endpoint tokens a broad directory read, exposing other owners’ endpoint addresses and public keys to any valid endpoint. `INV-002` says no reach across owners, but supplies no rule for this new read. **Smallest change:** specify an authenticated lookup limited to the caller’s issuer account and this endpoint’s exact address, with no foreign-account enumeration or records returned; refuse an unauthorized or failed lookup without writing. Test the successful own-record read, denied foreign read, absent record, and lookup error. This also makes the new check’s failure behavior observable.

**3. NEW — nonblocking; high certainty. `REQ-003`, §3; `AT-007`, §6.** The new no-write promises are not all carried into the named security acceptance list. A person can answer “no” at the fingerprint prompt, or install can get a directory lookup error, yet `AT-007` as written does not name those cases; it also omits its newly specified accepting no-auth and missing-record cases. `REQ-003` does demand tests for the latter two, so this is an acceptance-evidence mismatch, not silence in the requirement. **Smallest change:** add to `AT-007` no-write cases for “no,” no terminal with exit 4, accepting no-auth server, missing record, and lookup error, checking **all** target files remain absent or byte-identical. Confirm the prompt displays the server address and eight-group CA fingerprint before any write. The §5 person-run instruction and sparse-agent test correctly require the agent to hand over `mash credential install` and stop; the prompt is not proof of a human when an agent has a terminal, as decision 264 already accepts.

**4. CARRIED — nonblocking; high certainty. `REQ-010`, §3; §5 “Network exposure”; `AT-007`, §6.** A parent process can set `MASH_DANGEROUSLY_SKIP_NATS_ENCRYPTION=1`; a connecting command inherits it and, if the server accepts plaintext, sends credentials and mail unencrypted. The warning occurs before connection, but does not require a fresh choice by the person. A process with loopback packet-capture rights, or an observer on a wider plaintext route, can read that connection. This is permitted explicitly and tested as a warning, so it is an exposed choice rather than an implementation ambiguity. **Smallest change if the writer chooses to close it:** require an explicit, destination-specific choice for that run instead of treating an inherited value as consent.

## Credential and trust inventory

`INV-001` and `INV-003`, §5 prohibit secret values in argv, output, logs, JSON, errors, examples, and receipts; `AT-007` scans those channels. File **paths** may appear in argv and, where permitted, output. The table’s modes govern direct reads by other OS users; decision 264 covers same-user processes.

| §5 credential row | Where and who reads it | Agent action and exposure |
|---|---|---|
| Operator signing seed, account seed, system credentials | `mashctl` config directory, 0600; `mashctl` reads them. | Sparse-request agent may run the bounded helper and `mashctl adopt --from`, which copies needed material. Leakage can mint identities and read mail. Values must stay out of argv, logs, and receipts. |
| Admin credentials | `mashctl` config directory, 0600; `mashctl` reads them. | Agent may run local `adopt`; leakage permits endpoint and bucket administration. `mashctl status --detail` may show the **path**, not the value. |
| CA key | Helper directory, 0600; the setup user keeps it. | Agent may create it through the default local helper. Leakage permits server certificates to be minted. |
| Server TLS key | Helper server directory, 0600; that user’s `nats-server` reads it. | Agent may start the recorded local server through the helper. Leakage permits server impersonation. |
| CA certificate | Server directory and mash config, 0644; everyone can read it. | Agent may copy it. It is public; the first-install screen shows its fingerprint for comparison. |
| Endpoint seed key | Endpoint machine, 0600; mash reads it. | Agent may create a same-machine endpoint for a person-named agent. `--key` passes a path. Leakage permits acting as that endpoint. |
| Endpoint credentials | Mash config directory, 0600; mash reads them. | Agent may create local, person-named endpoints. The person runs other-machine install. Values must stay out of argv, logs, and receipts. |
| Token (JWT) | Person-carried operator file, 0600; `mash credential install` reads it in place and makes no separate token copy. | Agent hands over the install command and stops. The person deletes the source file after install. With its key, leakage permits acting as that endpoint; `--token` carries only a path. |

Under §5 **“Authority of an agent,”** the sparse request permits pinned verified downloads, the default loopback/TLS helper, `adopt`, and same-machine endpoints for named agents. Another source or server and deletion require the person’s answer; plugin installation and `credential install` are handed to the person. I found no revision 19 clause that lets the agent widen those permissions itself.

Under §5 **“Network exposure,”** the helper binds one TLS client port to `127.0.0.1`, with no monitoring, cluster, gateway, or leaf-node port. Another local user can attempt a connection but is refused without credentials; merely connecting does not reveal another TLS payload. The plaintext override exposes credentials and mail to an observer of that connection. `NFR-002` and `NFR-003` are absent, so findings cite the actual §5 headings.

## Gaps and consistency watch

- `REQ-003`, §3 and the §5 credential table do not expressly require install to reject a source token or key file that is foreign-owned, a symlink, or more permissive than 0600. `INV-003` specifies safe **writes**; `REQ-002` gives `adopt` an explicit source-folder check. State the install source checks and test refusal before connection or write.
- `REQ-015`, §3 can count a message as acknowledged when it is placed in full in session context even if no person or agent subsequently acts on it. A sender able to keep that delivery path active can keep the backlog below the wake threshold. That follows the chosen “delivery to the session” definition; state whether this is the intended meaning of “waiting.” Digest-only delivery keeps the count high and can cause one wake every ten minutes while the backlog persists, but the cooldown prevents a wake storm. Continuous restarts shorter than ten minutes can indefinitely postpone the startup wake; the contract explicitly chooses the per-start hold.
- §8 **“Readiness”** still says it is waiting for “the security read of rev 15,” although the header names the revision 17 read and revision 19 is the draft under review. Update that reference before ratification. `REQ-013` is explicitly removed; `DEC-001` explicitly refers to `REQ-014 of 0.5.5`, so neither is a dangling current requirement.

## Summary

I find **two blocking** and **two nonblocking** findings. In my view, ratification should wait for the intended account to be authenticated independently of the token and for the new directory read to have a defined owner boundary. Revision 19 closes the startup-wake and count issues in testable text, and narrows the prior install finding, but does not close it fully. This was a text-only read: I could not verify the helper, skill, server permissions, artifacts, or tests, and I ran nothing.
---

Inputs (sha256):

```
c5447da8542aa12d8c9d41ed4acb5ed5f732f44a7e8949396586ca7527fdccc3  ./contract-0.5.6-cli-ux-rev19-draft.md
106af60e6d0d0e7d7371a1edaec867c8c9024ca3c09d9117161c4fd698191629  ./prior/contract-0.5.6-cli-ux-rev17-draft.md
2a5d613d8648f9ce0e9f54e8466c1b4ebf83b33c37ce5c6d77e5a40c80973f7d  ./prior/security-read-rev17.md
9e8d9c48c2fde84244ca65c4ea32c331b2154a184535611d0ffe97a5a037f531  ./standards/cli/STANDARD.md
ed7b2448870e7cf899e4e8dc3b2735ff0ce8d8701844e5cb1e9b2b3152b8f73d  ./standards/contract/CHECKLIST.md
d96d969f78ff7e4eef8fc22a6b7b12db5a8ef3776b9a6a9b970e26ab45a3de85  ./standards/contract/STANDARD.md
9470bf08d732b908689ff2def78c271e852d77e6a1840ad2e5b4a9d7a02647a0  ./standards/skills/contract-critic/SKILL.md
ed1f58552eef3f041f4d37bb709557f6bcd7809f8eb00d5fb08aade797ac68cc  ./standards/standing-orders/mash.md
```
