# Security reread, mash 0.5.6 work contract (cli-ux) revision 8 draft

Review lane: x-ops2 (operations). Date: 2026-11-19. Advisory to the contract, not a release gate (mash-lead #869). Security only; a reread after our rev 7 read (receipts/0.5.6-cli-ux/security-read-rev7.md, bogworks/mash PR #835).
Reviewed: the mash lead's draft contract mash-0.5.6-cli-ux, revision 8 (draft), contract@0.3.4 (lane/lead 2aa04cb), copy at s3://homerange-xfer/masterplanner/for-herds/mash-0.5.6/contract-0.5.6-cli-ux-rev8-draft.md (sha256 ccbb1750180fc8b4f137a76db107905317bcc71a5d362d70ff3f185e25cb42a4). Not handed over: the helper script and the skill DRAFT5 (not landed).
Reviewer: OpenAI Codex, run by kit/codex-review (x-herd#132), `codex exec --sandbox read-only`, codex-cli 0.170.0, model gpt-6-sol, Daybreak ON (codex -c daybreak=true); the attestation was stated to the reviewer in its prompt. Other family from the writer (Anthropic). The reviewer read text and ran nothing. (The reviewer notes it did not see dev-process/, brief/ or ADR files: those belong to the homerange template and are not part of this review's workspace.)
Handed over (sha256 list at the end): the rev 8 draft; standards from bogworks-dev-standards 68475627ac8c673d9510cdeccd675a44d8765c07 (contract/STANDARD.md 0.3.4, contract/CHECKLIST.md, standing-orders/mash.md, cli/STANDARD.md, skills/contract-critic/SKILL.md); under prior/, the rev 7 draft and our rev 7 read. Nothing else. Where the content went: the pages above were sent to OpenAI through the operator's Codex subscription for this review.

---

# Security read, mash 0.5.6 cli-ux revision 8 (draft)

Reviewer attestation: this security runs on model gpt-6-sol, Daybreak ON (codex -c daybreak=true), codex-cli 0.170.0, read-only sandbox.

I read the revision 8 contract (contract-0.5.6-cli-ux-rev8-draft.md), the revision 7 security read (prior/security-read-rev7.md), and the supplied standards. This is a **text review**. I ran no installer, helper, skill, binary, plugin, or acceptance test. Helper and skill DRAFT5 were not supplied. The named `dev-process/`, `brief/`, and ADR files were also absent from this workspace, so I could not check their contents.

## Threat model

The assets are signing seeds, TLS keys, administrator and endpoint credentials, mail, and the binding between an endpoint and its intended server. An installing agent acts with the user’s account after a sparse request; another process under that account can read or replace its files. Other host users can attempt loopback connections. The critical boundaries are a person’s choice versus an agent’s choice, user-owned files versus same-user processes, verified downloads versus executed bytes, and a token’s claims versus an independently authenticated server.

## Revision 7 findings

“Closed” means the revision 8 **words** provide a checkable rule; it does not mean code passed.

| Prior finding | Revision 8 status | Closing words and acceptance check |
|---|---|---|
| `F7-1` | **Partly closed** | `REQ-003` removes install trust replacement. `REQ-013`, §3 puts config, CA, `trust.json`, and credentials in one generation and requires interruption tests of the selected generation and usable credentials. Its post-switch connection and rollback still allow a failed generation to be selected temporarily; see `R8-1`. |
| `F7-2` | **Partly closed** | `REQ-011(i)`, §3 drops the release-notes digest alternative and requires a repository, workflow, tag, and subject attestation before execution. A swapped-script test is named, but the verified script must also be the bytes executed; see `R8-2`. |
| `F7-3` | **Closed in text** | §5 “Authority of an agent” enumerates what a sparse “set mash up” request permits, reserves the source directory, server identity, plugin permissions, and exposure changes to the person, and names a sparse-request skill test. The missing skill prevents implementation verification. |
| `F7-4` | **Closed in text** | §5 “Network exposure” specifies one TLS client port on `127.0.0.1`, authentication on every connection, who can attempt a connection, and who can read plaintext traffic. `AT-007`, §6 names a per-port second-user reach test. The inherited plaintext risk remains separately disclosed as `S4`. |
| `F7-5` | **Partly closed** | `AT-007`, §6 now says coordinated same-user replacement and agent-prepared adoption **pass** as demonstrated residuals. §7 separates those risks and requests mash-specific written acceptance. That acceptance is still pending, and the trust residual describes fewer replaced files than `REQ-013` permits; see `R8-4`. |
| `F7-6` | **Partly closed** | `REQ-011(ii)`, §3 defines held handles, inode and byte checks, destination comparison, and atomic commit or cleanup; its swap test names expected bytes and destinations. Its categorical promise to detect *a file replaced during the copy* exceeds those checks; see `R8-3`. |
| `F7-7` | **Closed in text** | `REQ-011(iii)`, §3 pins the plugin manifest and installed digest, requires destination inspection and the person’s choice for extra permission, and names substitution tests. |
| `F7-8` | **Closed in text** | §5 “Credential inventory” and `AT-007`, §6 require exclusive 0600 archives under safe parents in named destinations. |

## Credential and trust inventory

`INV-003`, §5 bars secret **values** in argv, examples, logs, JSON, errors, and receipts; `AT-007`, §6 requires a scan of those channels. These rules apply to every secret row below. A 0600 file protects against other OS users, not processes running as its owner.

| §5 credential-table row | Location, readers, and agent authority |
|---|---|
| Operator signing seed, account seed, system credentials | 0600 in mashctl’s config directory; mashctl and same-user processes can read them. An agent with that account can operate mashctl and potentially read the files, gaining identity-minting and mail-reading power. Secret values must stay out of argv, logs, and receipts. |
| Admin credentials | 0600 in mashctl’s config directory after adoption; mashctl and same-user processes can read them. They permit endpoint and bucket administration. Values must stay out of argv, logs, and receipts. |
| CA key | The table says “moved off the machine after adoption” and “nobody on the machine” reads it. §5 “Custody of setup secrets” instead tells the person to **store or delete** it. Until that happens, the helper’s copy remains readable to the owning account and its agent. It can mint server certificates. |
| Server TLS key | 0600 in the helper’s server directory; the user’s nats-server process and same-user processes can read it. It permits server impersonation. The helper’s logging and receipt rules apply. |
| CA certificate | 0644 in the server directory and mash config; any local user may read it. Its value is public, but replacing the selected certificate changes trust; `REQ-013` requires consistency checks. |
| Endpoint seed key | 0600 on the endpoint’s machine; mash and same-user processes can read it. With the token it permits endpoint access. Its bytes cannot appear in argv, logs, or receipts. |
| Endpoint credentials | 0600 in mash config, including the selected trust generation under `REQ-013`; mash and same-user processes can read them. An agent can use the endpoint through mash. Values cannot appear in argv, logs, or receipts. |
| Token (JWT) | The table says “in transit, then removed,” while `REQ-003` accepts a token **file**. Its source file and any transfer copy may persist and be readable to their owner; the contract does not say which copy is removed. With the endpoint key, it permits endpoint access. `--token` passes a path, not the value, in argv. |
| `trust.json` | 0600 in mash config, within a generation under `REQ-013`; mash and same-user processes can read or replace it. It contains no secret value, but coordinated replacement can redirect trust. §7 accepts that as a proposed residual. |
| Backups and key archives | Exclusive 0600 files under safe parents at a destination the person names; the person and same-user processes can read their contents. An agent may create them only within the helper’s permitted actions and §5 authority limits. Secret values remain barred from logs and receipts. |

## Findings

**`R8-1` — CARRIED `F7-1`; blocking; high certainty. `REQ-013`, §3; `AT-007`, §6.** `mash trust replace` switches the pointer *before* connecting with the new credentials. A concurrent command can read that selected generation, fail to connect, or act on a server the replacement later rejects. A failed post-switch check then switches back, so the promise that a commit “never leaves unusable credentials selected” does not cover the interval. A second replacement during validation could also make an unconditional rollback select the wrong generation. **Smallest change:** validate the complete new generation before publication; serialize replacement and rollback against other replacements; define whether commands may observe a candidate during final validation. Test a concurrent command and a second replacement during both successful and failed rotations, including interruption at the pointer switch.

**`R8-2` — CARRIED `F7-2`; blocking; medium certainty. `REQ-011(i)`, §3.** The page verifies a downloaded script before running it, but the requirement does not explicitly bind the *executed* bytes to the verified subject. A same-user process could replace the script at its pathname after verification and before execution. The named “swap between verify and install” concerns installed binaries and need not catch that execution window. **Smallest change:** require execution from an exclusively held, private verified copy and compare its digest with the attested subject immediately before execution; make a verify-to-execute script swap refuse before any script code runs. Keep the equivalent verified-byte rule for both binaries.

**`R8-3` — CARRIED `F7-6`; blocking; high certainty. `REQ-011(ii)`, §3.** An attacker can replace a source pathname after `adopt --from` opens a held handle. The handle continues to yield the checked old bytes, and the destination comparison passes. That is safe with respect to the copied bytes, but it contradicts “a file replaced during the copy is detected and refused.” A reviewer could write a test expecting refusal where the stated checks cannot prove replacement. **Smallest change:** define success as committing only bytes checked through the held handles, and require refusal when an inode or checked bytes actually differ at a specified check. State the pathname-swap test’s exact timing and expected result. Keep a consistent malicious source directory as the separately accepted custody risk.

**`R8-4` — NEW; blocking pending written acceptance; high certainty. `REQ-013`, §3; §7 “Residual risks for Randy’s written acceptance.”** The residual says another process replaces `trust.json` “together with config.json.” The new generation also holds the CA file and credentials, and its pointer selects them together. A same-user process can substitute a complete, internally consistent generation and pointer; disagreement checks pass. The current §7 wording understates that attack. **Smallest change:** describe substitution of the pointer and *whole generation*, including CA and credentials, and retain the `AT-007` passing test as evidence of the limit. If this design remains, Randy must accept it **for mash in writing**. Homerange’s acceptance is precedent, not mash’s acceptance.

**`R8-5` — NEW; nonblocking; high certainty. §5 “Credential inventory” and “Custody of setup secrets”; `REQ-003`, §3.** The table presents the CA key as already moved off-machine, yet the custody rule leaves that action to the guide and person. It also says the token is removed without identifying whether that means an installer temporary copy or the operator-supplied source. An agent could delete the only source token, or leave an untracked copy while a reviewer reads the table as a cleanup guarantee. **Smallest change:** show the CA key’s initial helper location and owner-readable period, then its optional offline or deletion state; identify each token copy, its reader, and which copy installation removes. Check those states and receipt contents in `AT-007`.

## Gaps and acceptance boundary

- **Initial account identity is indirect.** `REQ-013`, §3 checks the token against the account public key served by the TLS-authenticated server, rather than against the token’s self-claimed issuer alone. The person’s independently supplied address and CA fingerprint anchor that first connection. The contract should say explicitly that this authenticates the *server as the source of the account key*; it does not independently authenticate the account key through a second channel. The existing self-signed-token, substituted-address, and issuer-mismatch tests are runnable.
- **Recovery after a refused replacement needs an observable state.** `REQ-013`, §3 says rollback “switches back and says why,” but `AT-007`, §6 should assert the old generation remains selected, its credentials connect, the rejected generation is not selected on restart, and `mash trust repair` can remove its leftover files. Those checks matter after interruption as well as a live connection failure.
- **The published plugin route is ambiguous.** `REQ-011(iii)`, §3 calls `mash plugin install` the verified route and the marketplace route an unverified developer route. §8 “Release mechanics” still lists “the plugin from the marketplace” without that qualification. State which route the user-facing release and quick start publish. Keep a test that the installer neither fetches a marketplace entry nor widens pinned permissions without the person’s separate choice.
- **The remaining residuals need separate dispositions.** `S1` (agent impersonates confirmation), `S2` (same-user pathname race), whole-generation trust substitution, and signing-seed custody including agent-prepared adoption concern related same-user power but have different consequences. `S4` (inherited plaintext) is a separate transport risk. None is accepted for mash by Randy’s homerange decision. If left unchanged, each needs his written mash-specific acceptance before READY under `contract@0.3.4`.

## Summary

I find **4 blocking findings and 1 nonblocking finding** in the revision 8 text. `F7-3`, `F7-4`, `F7-7`, and `F7-8` are closed as testable contract requirements; the other revision 7 findings are partly closed. In my view, ratification is blocked by the replacement visibility and recovery rule, verified-script execution binding, the adoption test’s overclaim, and the unaccepted whole-generation substitution residual. This is a contract reading only; I could not verify any implementation or run an acceptance test.
---

Inputs (sha256):

```
ccbb1750180fc8b4f137a76db107905317bcc71a5d362d70ff3f185e25cb42a4  ./contract-0.5.6-cli-ux-rev8-draft.md
ac8d5540574634d92d6e1b499f227e712978a674ba21bc7f08aa084d56565084  ./prior/contract-0.5.6-cli-ux-rev7-draft.md
65bad47e4ad63fc6ae97dc0f092fc4caa63d71cc32477511ec3670e39dbb6d43  ./prior/security-read-rev7.md
9e8d9c48c2fde84244ca65c4ea32c331b2154a184535611d0ffe97a5a037f531  ./standards/cli/STANDARD.md
ed7b2448870e7cf899e4e8dc3b2735ff0ce8d8701844e5cb1e9b2b3152b8f73d  ./standards/contract/CHECKLIST.md
d96d969f78ff7e4eef8fc22a6b7b12db5a8ef3776b9a6a9b970e26ab45a3de85  ./standards/contract/STANDARD.md
9470bf08d732b908689ff2def78c271e852d77e6a1840ad2e5b4a9d7a02647a0  ./standards/skills/contract-critic/SKILL.md
ed1f58552eef3f041f4d37bb709557f6bcd7809f8eb00d5fb08aade797ac68cc  ./standards/standing-orders/mash.md
```
