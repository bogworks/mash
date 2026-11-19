# Security reread, mash 0.5.6 work contract (cli-ux) revision 20 draft

Review lane: x-ops2 (operations). Date: 2026-11-19. Advisory to the contract, not a release gate (mash-lead #958). Security only; a reread after our rev 19 read (receipts/0.5.6-cli-ux/security-read-rev19.md, bogworks/mash PR #849). Randy's decision 264 stands: the same-OS-user risk is accepted for mash and was not raised.
Reviewed: the mash lead's draft contract mash-0.5.6-cli-ux, revision 20 (draft), contract@0.3.4 (lane/lead b22cc3d), copy at s3://homerange-xfer/masterplanner/for-herds/mash-0.5.6/contract-0.5.6-cli-ux-rev20-draft.md (sha256 bf9b8752d5928a905805d4fae35bf4d7d798cd8352430d05607e4595a6fadefe). Not handed over: the helper script and the skill (not landed).
Reviewer: OpenAI Codex, run by kit/codex-review (x-herd#132), `codex exec --sandbox read-only`, codex-cli 0.170.0, model gpt-6-sol, Daybreak ON (codex -c daybreak=true); the attestation was stated to the reviewer in its prompt. Other family from the writer (Anthropic). The reviewer read text and ran nothing.
Handed over (sha256 list at the end): the rev 20 draft; standards from bogworks-dev-standards 68475627ac8c673d9510cdeccd675a44d8765c07 (contract/STANDARD.md 0.3.4, contract/CHECKLIST.md, standing-orders/mash.md, cli/STANDARD.md, skills/contract-critic/SKILL.md); under prior/, the rev 19 draft and our rev 19 read. Nothing else. Where the content went: the pages above were sent to OpenAI through the operator's Codex subscription for this review.

---

# Security read, mash 0.5.6 cli-ux revision 20 (draft)

Reviewer attestation: this security runs on model gpt-6-sol, Daybreak ON (codex -c daybreak=true), codex-cli 0.170.0, read-only sandbox.

I read the supplied workspace text and ran no code or tests. The helper and install skill are not present, so their behavior remains unverified.

## Threat model

The assets are signing seeds, endpoint keys and credentials, mail, and the endpoint’s choice of server and account. An installing agent starts with limited authority from a sparse request; the other-machine install belongs to the person. Trust crosses from the operator’s hand-over to the person’s confirmation, then through TLS and NATS authentication to local files. Other OS users can reach the loopback port, and a network observer can see plaintext connections. Section 7 records Randy’s decision 264 accepting the same-OS-user standing risk; I do not raise it.

## Revision 19 disposition

“Closed” below means testable in the contract text, not verified in code.

| Prior item | Revision 20 status |
|---|---|
| Finding 1, `R19-1` | **Partly closed.** `REQ-003`, §3 adds the operator’s account public key to the separate hand-over and requires the token issuer to equal the confirmed account before any write. `AT-007`, §6 tests an authenticated other-account token. The displayed key is “shortened,” leaving the exact comparison underspecified. Finding 1 below. |
| Finding 2, `R19-2` | **Closed.** `REQ-003`, §3 says install “reads nothing else from the server (no directory lookup).” The proposed endpoint directory permission and its cross-owner read no longer exist. |
| Finding 3, no-write acceptance list | **Closed in substance.** `AT-007`, §6 now names “no,” no terminal, refused or unreachable server, no-auth server, issuer mismatch, and refused source files, with every target absent or byte-identical. It also tests interruption after each write and rerun. The account display still needs the correction in Finding 1. |
| Finding 4, inherited plaintext override | **Open, nonblocking.** `REQ-010`, §3 still permits an inherited `MASH_DANGEROUSLY_SKIP_NATS_ENCRYPTION=1`; `AT-007`, §6 requires a warning. Finding 3 below. |
| Gap: source key and token file checks | **Closed in text.** `REQ-003`, §3 rejects symlink, foreign-owned, and broader-than-0600 files **before connecting**; `AT-007`, §6 names all three no-write cases. |
| Gap: meaning of “waiting” | **Closed in text.** `REQ-015`, §3 says full delivery to session context or a read acknowledges; a digest does not. It states that mail seen but not acted on is no longer waiting. `AT-008`, §6 covers count, wake, cooldown, restart, and acknowledgement. |
| Gap: stale Readiness line | **Closed.** Readiness now waits for “a clean security read.” |
| Gap: unlanded helper and skill | **Open as a verification limit.** Their actual downloads, permissions, logging, and output cannot be checked here. |

## Findings

**1. CARRIED from `R19-1` — blocking; high certainty. `REQ-003`, §3; `AT-007`, §6.** The install screen shows “the account’s public key, shortened the same way” as the CA fingerprint, while `AT-007` requires the *full* fingerprint but only “the account.” A person can confirm a matching abbreviation while the full token issuer differs from the operator’s account key. An implementation could then compare the issuer to the full value it extracted from that same token and claim it matches “the account the person confirmed.” The text never requires a full, independent account-key comparison; it also applies an eight-group hex display description to a public key without defining its actual display encoding. **Smallest change:** require mashctl and install to show the complete account public key in the same unambiguous, copyable format, identify the operator’s separately conveyed full key as the comparison value, and require byte-for-byte equality between that confirmed value and the token issuer before writing. Make `AT-007` test a token whose issuer differs outside the presently displayed abbreviation. The person’s comparison remains the stated trust root.

**2. NEW — nonblocking; high certainty. `JRN-002`, §2; `REQ-003`, §3; `AT-002` and `AT-007`, §6.** The journey still says the operator sends “the credential, server address and CA” and Bob compares only address and fingerprint. `AT-002` likewise says “the token, server address and CA come back,” with no account key. Someone following that journey or acceptance scenario could omit the third hand-over value, making the `REQ-003` confirmation impossible even though the detailed security test expects an account on the prompt. **Smallest change:** add the separately sent account public key and its comparison to `JRN-002` and `AT-002`; update the earlier `AT-007` line that says install shows only address and fingerprint.

**3. CARRIED — nonblocking; high certainty. `REQ-010`, §3; §5 “Network exposure”; `AT-007`, §6.** A parent process can set `MASH_DANGEROUSLY_SKIP_NATS_ENCRYPTION=1`. A child connecting command inherits it and, if a server accepts plaintext, sends credentials and mail unencrypted. The required warning precedes connection, but no fresh choice is required. A process with packet-capture rights on loopback, or an observer on a wider plaintext route, can read that traffic. **Smallest change if this exposure is to be closed:** require an explicit destination-specific choice for that run; an inherited variable alone would not authorize plaintext. Section 7 already records Randy’s treatment of the override.

## Credential and trust inventory

`INV-001` and `INV-003`, §5 keep secret values out of argv, stdout, stderr, JSON, logs, errors, examples, and receipts; `AT-007`, §6 scans those channels. File paths may be command arguments. The stated modes limit reads by other OS users; decision 264 covers same-user access.

| §5 credential row | Location and permitted reader | Agent action and exposure |
|---|---|---|
| Operator signing seed, account seed, system credentials | mashctl config directory, 0600; mashctl reads them. | A sparse-request agent may run the bounded helper and `mashctl adopt --from`, which copies needed material. Leaked values can mint identities and read mail; values must stay out of argv, logs, and receipts. |
| Admin credentials | mashctl config directory, 0600; mashctl reads them. | The agent may run local `adopt`. Leakage permits endpoint and bucket administration; `mashctl status --detail` may show the path, not the value. |
| CA key | Helper directory, 0600; the setup user keeps it. | The agent may create it through the default local helper. Leakage permits server certificates to be minted. |
| Server TLS key | Helper server directory, 0600; that user’s `nats-server` reads it. | The agent may start the recorded local server. Leakage permits server impersonation. |
| CA certificate | Server directory and mash config, 0644; everyone may read it. | The agent may copy it. It is public; install shows its full fingerprint for comparison. |
| Endpoint seed key | Endpoint machine, 0600; mash reads it. | The agent may create a same-machine endpoint for a person-named agent. `--key` passes a path. Leakage permits acting as the endpoint. |
| Endpoint credentials | Mash config directory, 0600; mash reads them. | The agent may create local, person-named endpoints. The person runs other-machine install. Values must stay out of argv, logs, and receipts. |
| Token (JWT) | Person-carried operator file, 0600; `mash credential install` reads it in place and makes no separate token copy. | The agent hands over the install command and stops. The person deletes the source after install. `--token` carries a path; a leaked token together with its key permits acting as the endpoint. |

Under §5 **“Authority of an agent,”** a sparse request permits pinned, verified downloads; the default loopback/TLS helper; local `adopt`; and same-machine endpoints for agents the person named. Another source or server and deletion require the person’s answer. Plugin installation and `credential install` are handed to the person. I found no revision 20 clause that lets the agent widen its own authority.

Under §5 **“Network exposure,”** the helper binds one TLS client port to `127.0.0.1` and exposes no monitoring, cluster, gateway, or leaf-node port. Other local users can attempt to connect; NATS credentials are required and TLS protects another connection’s payload. The plaintext override exposes credentials and mail to anyone able to observe that connection. This contract has no `NFR-002` or `NFR-003` IDs; these are the applicable §5 headings.

The confirmed address and CA fingerprint identify the TLS destination the person chose. Operator-mode authentication and an accepted connection show that this server accepts the supplied key and token; they do not independently prove who operated a server whose address and CA the person confirmed. The server does not supply an account key for install to compare: `REQ-003` expressly drops that lookup. If a token’s issuer differs from the **full confirmed** account key, install must refuse before writing, regardless of whether the server accepts it. Subject to Finding 1, `REQ-003` honestly places the root of trust in the person’s confirmation; the remaining checks test properties and consistency with that choice.

## Gaps and consistency watch

- `AT-007`, §6 does not explicitly name a certificate signed by a different CA as a no-write case. `REQ-003`, §3 requires CA and host-name verification before writing, and the test already names host-name mismatch; add the wrong-CA case to make both halves observable.
- `REQ-003`, §3 specifies preflight of every destination, atomic CA then credentials then `config.json`, with `config.json` as commit point. `AT-007`, §6 tests a kill after each file and rerun, but does not explicitly assert that mash ignores each intermediate state before rerun. Add that assertion to the existing interruption test.
- `REQ-015`, §3 and `AT-008`, §6 now define and test backlog behavior, including owner-boundary cases in `AT-007`. Continuous restarts under ten minutes can still defer a startup wake indefinitely; the specified per-start cooldown deliberately permits that outcome.

## Summary

I find **one blocking and two nonblocking findings**. In my view, ratification should wait for the full, independent account-key comparison in Finding 1; the directory-read issue is closed, and the no-write, source-file, backlog, and Readiness changes are testable in text. This was a read-only review of the contract and supplied standards: I ran nothing and could not verify the unlanded helper, skill, binaries, or tests.
---

Inputs (sha256):

```
bf9b8752d5928a905805d4fae35bf4d7d798cd8352430d05607e4595a6fadefe  ./contract-0.5.6-cli-ux-rev20-draft.md
c5447da8542aa12d8c9d41ed4acb5ed5f732f44a7e8949396586ca7527fdccc3  ./prior/contract-0.5.6-cli-ux-rev19-draft.md
9aeb5240ffd0f59ed3fa8db589a4c27b56b09dd7ca93f2c1818ee154508906ff  ./prior/security-read-rev19.md
9e8d9c48c2fde84244ca65c4ea32c331b2154a184535611d0ffe97a5a037f531  ./standards/cli/STANDARD.md
ed7b2448870e7cf899e4e8dc3b2735ff0ce8d8701844e5cb1e9b2b3152b8f73d  ./standards/contract/CHECKLIST.md
d96d969f78ff7e4eef8fc22a6b7b12db5a8ef3776b9a6a9b970e26ab45a3de85  ./standards/contract/STANDARD.md
9470bf08d732b908689ff2def78c271e852d77e6a1840ad2e5b4a9d7a02647a0  ./standards/skills/contract-critic/SKILL.md
ed1f58552eef3f041f4d37bb709557f6bcd7809f8eb00d5fb08aade797ac68cc  ./standards/standing-orders/mash.md
```
