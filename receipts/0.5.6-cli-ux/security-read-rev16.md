# Security reread, mash 0.5.6 work contract (cli-ux) revision 16 draft

Review lane: x-ops2 (operations). Date: 2026-11-19. Advisory to the contract, not a release gate (mash-lead #947). Security only; a reread after our rev 15 read (receipts/0.5.6-cli-ux/security-read-rev15.md, bogworks/mash PR #844). Randy's decision 264 stands: the same-OS-user risk is accepted for mash and was not raised.
Reviewed: the mash lead's draft contract mash-0.5.6-cli-ux, revision 16 (draft), contract@0.3.4 (lane/lead d59218e), copy at s3://homerange-xfer/masterplanner/for-herds/mash-0.5.6/contract-0.5.6-cli-ux-rev16-draft.md (sha256 7bf582f0c0f91f01d7568611942944cace42fd2a467bbfa37c4e6d9da7828b2). Not handed over: the helper script and the skill (not landed).
Reviewer: OpenAI Codex, run by kit/codex-review (x-herd#132), `codex exec --sandbox read-only`, codex-cli 0.170.0, model gpt-6-sol, Daybreak ON (codex -c daybreak=true); the attestation was stated to the reviewer in its prompt. Other family from the writer (Anthropic). The reviewer read text and ran nothing.
Handed over (sha256 list at the end): the rev 16 draft; standards from bogworks-dev-standards 68475627ac8c673d9510cdeccd675a44d8765c07 (contract/STANDARD.md 0.3.4, contract/CHECKLIST.md, standing-orders/mash.md, cli/STANDARD.md, skills/contract-critic/SKILL.md); under prior/, the rev 15 draft and our rev 15 read. Nothing else. Where the content went: the pages above were sent to OpenAI through the operator's Codex subscription for this review.

---

# Security read, mash 0.5.6 cli-ux revision 16 (draft)

Reviewer attestation: this security runs on model gpt-6-sol, Daybreak ON (codex -c daybreak=true), codex-cli 0.170.0, read-only sandbox.

I read the workspace contract, the revision 15 contract and security read, and the supplied standards. I ran nothing. The helper, install skill, release artifacts, and implementation are not available here. This draft has no `NFR-002` or `NFR-003` IDs; their subjects appear in §5 as **“Authority of an agent”** and **“Network exposure.”**

## Threat model

The assets are signing seeds, TLS keys, endpoint credentials, mail, and the choice of server an endpoint trusts. An installing agent may receive only “set mash up.” Trust crosses from that request to downloaded binaries and plugin code, from an operator’s separate message to a server and CA, and from owner-only files to network connections and diagnostic output. Other host users can reach a loopback port; a process with packet-capture rights can observe an unencrypted loopback connection. Randy’s decision 264 accepts the standing risk posed by processes acting as the **same OS user**. I do not reopen it.

## Revision 15 finding disposition

“Closed” means the revision 16 words address the finding; it does not mean an implementation passed a test.

| Prior finding | Revision 16 status and testability |
|---|---|
| `F15-1` | **Partly closed.** `REQ-003` says the skill hands the full address and fingerprint to the person and stops without an answer. It does not define an affirmative answer or require `credential install` to wait for one. `AT-007` tests display, not the stop. |
| `F15-2` | **Partly closed.** `REQ-003` requires a signature check against an account key served by the authenticated server before writing; `AT-007` names another account’s valid token. The key lookup, account selection, and failure behavior when no suitable key is served remain undefined. |
| `F15-3` | **Closed in text.** `REQ-011(i)` pins repository, workflow, tag, subject, and installed-byte digest; its substitution tests are concrete. A release-asset attacker can supply both a binary and a bundle, but cannot satisfy those stated identity and digest checks with an unrelated valid attestation. The sparse agent route excludes `go install` without the person’s answer. This has not been run. |
| `F15-4` | **Partly closed.** `REQ-011(iii)` pins `bogworks/mash#v0.5.6`, bounds manifest entries, and tests an added permission. It leaves execution of the plugin’s unverified launcher and the content of its skills authorized by a sparse request. |
| `F15-5` | **Closed for threshold crossings; restart exposure remains.** `REQ-015` specifies a per-endpoint ten-minute cooldown from the last wake; `AT-008` tests drain and refill. The text expressly resets the cooldown on restart, so repeated restarts can produce repeated wakes. |
| `F15-6` | **Closed in text.** `REQ-003` preflights every destination, writes CA and credentials before `config.json`, ignores an uncommitted directory, and requires kill-and-rerun tests. An identical rerun leaves existing files unchanged and completes missing ones. |
| `F15-7` | **Open, nonblocking.** `REQ-010` still permits an inherited `MASH_DANGEROUSLY_SKIP_NATS_ENCRYPTION=1`; its warning and the transport consequence are explicit. |
| `F15-8` | **Closed in text.** `REQ-015` specifies the caller’s credentials, the caller’s owner scope, no admin query, and `unknown` outside that scope. The exact authorization test is not in `AT-008`. |
| `F15-9` | **Closed.** `REQ-003` identifies what a missing flag must precede, and §5 now says install writes “config, CA and credentials.” |

## Findings

**R16-1 — CARRIED from `F15-1`; blocking; high certainty. `REQ-003` and `REQ-001`, §3; `AT-007`, §6.** A skill can display the address and fingerprint, receive a vague response such as “continue,” and run `credential install`; the command itself has no specified comparison or authorization input. The agent could also invoke the command directly. The contract says “an agent with no answer stops,” but defines neither what answer permits continuation nor a test in which the agent gets no answer. There is also a direct conflict: `REQ-003` requires the first install to **SHOW** the address, while `REQ-001` prohibits the server address in success output except `status` or `--detail`. An implementer could satisfy the output lint by hiding the value needed for comparison. **Smallest change:** give `REQ-003` an explicit exception to `REQ-001`; specify the person’s affirmative answer to the displayed full address and fingerprint, where the skill checks it, and a sparse-request acceptance test proving no answer means no install. State whether the CLI intentionally remains callable without that skill step.

**R16-2 — CARRIED from `F15-2`; blocking; medium-high certainty. `REQ-003`, §3; `AT-007`, §6.** The new words require the token signature to verify against “the account public key that authenticated server serves for that account.” They do not specify which server response supplies that key, how the install obtains it before credentials exist, or how it selects the account when several are served. An implementation could fall back to the issuer key named by the JWT when the lookup fails and still appear to perform a signature check; another could write a CA file before discovering an ambiguous or absent key. **Smallest change:** name the authenticated lookup and account identifier, require exactly one matching server-supplied key, and refuse absent, ambiguous, or mismatching results before **any** destination write. Add those three cases to `AT-007`. The token still names no server: first-install trust therefore depends on the person’s separate address and fingerprint comparison. Until R16-1 is resolved, that trust story is partly open.

**R16-3 — CARRIED from `F15-4`; blocking; high certainty. `REQ-011(iii)`, §3; §5 “Authority of an agent”; `AT-007`, §6.** An attacker able to alter plugin contents at the source the agent installs can leave the listed manifest entries unchanged while changing the permitted launcher or skill text. The agent is authorized by “set mash up” to install the plugin, and its three hooks then run that launcher. The immutable-tag claim and added-permission test constrain the manifest and tag movement; the contract itself says the plugin bytes are “not otherwise verified.” **Smallest change:** either bind the launcher, manifest, and skill bytes to independently verified release provenance, or require the person’s own answer before the agent installs this unverified plugin. Test a changed launcher or skill with an unchanged allowed manifest.

**R16-4 — CARRIED from `F15-7`; nonblocking; high certainty. `REQ-010`, §3; §5 “Network exposure”; `AT-007`, §6.** A parent process can pass `MASH_DANGEROUSLY_SKIP_NATS_ENCRYPTION=1` into a mash command. Against a server accepting plaintext, a process with packet-capture rights on loopback can read the credentials and mail on that connection. The contract explicitly permits the inherited value and requires a warning before connecting; the warning does not prevent the connection. **Smallest change if the writer wants to close this:** require a fresh destination-specific choice for plaintext. The present wording makes the consequence visible and testable.

**R16-5 — NEW; nonblocking; medium certainty. `REQ-015`, §3; `AT-008`, §6.** A crash loop or repeated restart of `mash serve` while a mailbox remains over its limit can emit a backlog wake on every start, because the contract explicitly says the cooldown “does not survive a restart.” A sender alone is not shown to control restarts, so this is narrower than the original threshold-crossing finding. **Smallest change:** persist the last wake per endpoint, or state a bounded restart behavior and test repeated restarts with a full inbox. The specified drain-and-refill test does close the original crossing case.

## Credential and trust inventory

`INV-001` and `INV-003`, §5, bar secret **values** from argv, output, logs, JSON, errors, examples, and receipts; `AT-007` scans those channels. File **paths** can still appear in argv. Owner-only modes exclude other OS users from direct file reads; decision 264 governs processes acting as that owner.

| §5 credential row | Location and readers; authorized agent use; exposure boundary |
|---|---|
| Operator signing seed, account seed, system credentials | `mashctl` config directory, 0600; `mashctl` and the owner can read them. The sparse journey permits `adopt` to copy needed material into that directory, not to disclose it. Leakage permits minting identities and reading all mail. Values must stay out of argv, logs, and receipts. |
| Admin credentials | `mashctl` config directory, 0600; `mashctl` and the owner can read them to manage endpoints and buckets. An agent may use `adopt` in the authorized local setup. Values must stay out of argv, logs, and receipts; `mashctl status --detail` may show the file path. |
| CA key | Helper directory, 0600; the owner can read it and mint server certificates. The agent may run the bounded helper; no backup, export, or key-retirement mode is authorized. The key value must not enter diagnostics or receipts. |
| Server TLS key | Helper server directory, 0600; the owner’s `nats-server` process uses it. The agent may create and start that local server through the helper. Leakage permits server impersonation; the key value must not enter output or logs. |
| CA certificate | Server directory and mash config, 0644; everyone can read this public trust material. The agent may copy it during setup. Its path may be in argv; its full fingerprint and server address need the separate person comparison described above. |
| Endpoint seed key | Endpoint machine, 0600; mash and the owner can read it and act as that endpoint. An agent may create one for a person-named endpoint; `--key` carries a path, never the seed value. |
| Endpoint credentials | Mash config directory, 0600; mash and the owner can read them and act as that endpoint. The agent may create or install them within the authorized journey. `--creds-out` carries a destination path; values must stay out of argv, logs, and receipts. |
| Token (JWT) | Person-carried operator file, 0600; `credential install` reads it in place and creates no token copy. The agent may pass its **path** to install; the person deletes the original afterward. The JWT value must stay out of argv, logs, and receipts. Paired with the endpoint key, it permits acting as that endpoint. |

Under §5 “Network exposure,” the helper defaults to one TLS client listener on `127.0.0.1`. Any host user can attempt a TCP connection, but the specified credential check refuses unauthenticated clients; TLS protects the connection payload from another connection. The helper opens no monitoring, cluster, gateway, or leaf-node port by default. The plaintext override exposes credentials and mail to an observer of that connection, including a loopback packet-capture process. The one-port guarantee applies to the helper, not an operator-provided server.

## Gaps to state

- `AT-007` should assert that the person-comparison step stops an installing agent with no answer. It currently asserts only that `credential install` shows the values.
- `REQ-003` should state what a failed or unavailable account-key lookup returns and verify that **no** CA, credential, or config file is written in each case.
- `REQ-015` should test the caller’s own credentials and owner boundary for `mash endpoint status`, including an unauthorized endpoint returning `unknown`. Its current `AT-008` exercises wakes and `mash status`, not that boundary.
- §5 “Network exposure” says other NATS ports may be opened “unless the person asks,” while `REQ-002` says the 0.5.6 helper offers no other interface. State that any wider server setup belongs to the operator’s separate server path.
- `REQ-002` says the helper writes nothing outside its directory while also directing verification of mash downloads under `REQ-011(i)`. State that binary installation into `~/.local/bin` is a separate install step, so an agent cannot read the helper rule as authority to write there.

## Summary

I find **three blocking** and **two nonblocking** findings in revision 16. In my view, R16-1 through R16-3 block ratification until resolved or expressly accepted by the ratifier; that is a review view, not a decision. Decision 264 already accepts the same-OS-user standing risk. This is a text-only read: I could not verify the unlanded helper or skill, attestation command, plugin bytes, or runtime behavior, and I ran no tests.
---

Inputs (sha256):

```
7bf582f0c0f91f01d7568611942944cace42fd2a467bbfa37c4e6d9da7828b2a  ./contract-0.5.6-cli-ux-rev16-draft.md
acea0f4bcc2534300a76f61261d5dc6e3aa9c6fb8d3b72831c36cf4111d455c8  ./prior/contract-0.5.6-cli-ux-rev15-draft.md
9c127a6db867cacb0a5e92275b76b0c5c284ec0b03d73ff61b21b533bcf315ef  ./prior/security-read-rev15.md
9e8d9c48c2fde84244ca65c4ea32c331b2154a184535611d0ffe97a5a037f531  ./standards/cli/STANDARD.md
ed7b2448870e7cf899e4e8dc3b2735ff0ce8d8701844e5cb1e9b2b3152b8f73d  ./standards/contract/CHECKLIST.md
d96d969f78ff7e4eef8fc22a6b7b12db5a8ef3776b9a6a9b970e26ab45a3de85  ./standards/contract/STANDARD.md
9470bf08d732b908689ff2def78c271e852d77e6a1840ad2e5b4a9d7a02647a0  ./standards/skills/contract-critic/SKILL.md
ed1f58552eef3f041f4d37bb709557f6bcd7809f8eb00d5fb08aade797ac68cc  ./standards/standing-orders/mash.md
```
