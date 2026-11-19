# Security reread, mash 0.5.6 work contract (cli-ux) revision 11 draft

Review lane: x-ops2 (operations). Date: 2026-11-19. Advisory to the contract, not a release gate (mash-lead #879, #882). Security only; a reread after our rev 9 read (receipts/0.5.6-cli-ux/security-read-rev9.md, bogworks/mash PR #837). Revision 10 (answers R9-1, R9-2, R9-6) was read together with revision 11 (adds: adopt --from output no longer says the helper server folder can be removed; endpoint delete --force removed; in-place trust rotation deferred out of 0.5.6, replaced by `mash trust reset` plus a fresh first install).
Reviewed: the mash lead's draft contract mash-0.5.6-cli-ux, revision 11 (draft), contract@0.3.4, copy at s3://homerange-xfer/masterplanner/for-herds/mash-0.5.6/contract-0.5.6-cli-ux-rev11-draft.md (sha256 f8e2ff9fdecb059eea5d2962d31d94c8fad2a5e443b98ea1dda2f683779c0cde). Not handed over: the helper script and the skill DRAFT5 (not landed).
Reviewer: OpenAI Codex, run by kit/codex-review (x-herd#132), `codex exec --sandbox read-only`, codex-cli 0.170.0, model gpt-6-sol, Daybreak ON (codex -c daybreak=true); the attestation was stated to the reviewer in its prompt. Other family from the writer (Anthropic). The reviewer read text and ran nothing.
Handed over (sha256 list at the end): the rev 11 draft; standards from bogworks-dev-standards 68475627ac8c673d9510cdeccd675a44d8765c07 (contract/STANDARD.md 0.3.4, contract/CHECKLIST.md, standing-orders/mash.md, cli/STANDARD.md, skills/contract-critic/SKILL.md); under prior/, the rev 9 and rev 10 drafts and our rev 9 read. Nothing else. Where the content went: the pages above were sent to OpenAI through the operator's Codex subscription for this review.

---

# Security read, mash 0.5.6 cli-ux revision 11 (draft)

Reviewer attestation: this security runs on model gpt-6-sol, Daybreak ON (codex -c daybreak=true), codex-cli 0.170.0, read-only sandbox.

I read the supplied contract, prior rev 9 read, rev 10 draft, and standards. I ran no installer, helper, skill, binary, plugin, or acceptance test. Helper and skill DRAFT5 were not supplied. The named `dev-process/`, `brief/`, and ADR files were not in this workspace. The contract contains **no `NFR-002` or `NFR-003` ids**; the relevant authority and network rules are in §5, “Authority of an agent” and “Network exposure.”

## Threat model

Signing seeds, TLS keys, endpoint credentials, mail, and the binding to the intended server are the assets. An installing agent may have only a sparse instruction yet run as the user; another process with that account can read or replace owner-only files. Other host users can connect to a loopback port. The key boundaries are the person’s decision versus an agent’s action, the account’s files versus same-user processes, attested bytes versus executed bytes, and a token’s claims versus a server authenticated using a separately supplied address and CA fingerprint.

## Rev 9 disposition

“Closed” means the **contract text** gives a checkable rule; it does not mean implementation passed.

| Prior item | Rev 11 status | Closing words and runnable check |
|---|---|---|
| `R9-1` | **Closed in text** | `REQ-011(i)`, §3 requires bytes hashed through a held handle to be executed from that handle or an unreachable read-only copy. A later source-path swap “has no effect”; a script or either binary with mismatched bytes is refused. Swap each object at the named stages and inspect what ran or was installed. |
| `R9-2` | **Partly closed** | `REQ-011(ii)`, §3 now says an after-open pathname swap commits the original checked bytes and calls for destination and interruption checks. It still says “a file replaced during the copy is detected and refused.” Those words give the same swap two outcomes. See `SR11-1`. |
| `R9-6` | **Closed in text** | The token row in §5 says install reads the operator’s file in place, creates no copy, and leaves deletion to the person. An acceptance run can inspect the source and search for copies; `AT-007` should name that check explicitly. |
| Rev 9 power-loss gap | **Partly closed** | `REQ-013`, §3 names kill tests and syncs, but no longer explicitly requires syncing the *new generation directory before* publishing its pointer. See `SR11-3`. |
| Rev 9 plugin-route gap | **Closed** | `REQ-002`, §3 identifies the **repository marketplace** as unverified; `REQ-011(iii)` identifies the embedded route as verified. Test with network off and a substituted marketplace entry. |
| Rev 9 sparse-authority gap | **Closed, subject to `SR11-5`** | §5 expressly permits the pinned manifest at user scope after “set mash up,” while reserving permissions beyond it. The sparse-request test can check that boundary. The manifest’s permitted powers remain unspecified. |

## Credential and trust inventory

`INV-003`, §5 bars secret **values** in argv, examples, logs, JSON, errors, and receipts; `AT-007`, §6 calls for scans. `--key` and `--token` take paths, not values. Every 0600 or 0700 protection below excludes other OS users, **not** processes or an agent using the owner’s account.

| §5 credential row | Location, readers, agent action, and exposure |
|---|---|
| Operator signing seed, account seed, system credentials | 0600 in mashctl’s config directory; mashctl and same-user processes can read them. An agent with that account can use or copy them to mint identities and read mail. Values must never appear in argv, logs, or receipts. |
| Admin credentials | 0600 in mashctl’s config directory; mashctl and same-user processes can read and use them to manage endpoints and buckets. No value in argv, logs, or receipts. |
| CA key | 0600 in the helper directory until the person stores it offline or runs `--retire-keys`; same-user processes can mint server certificates. Rev 11 correctly forbids output suggesting removal of the live-key folder. Archive contents and diagnostics must not leak into argv, logs, or receipts. |
| Server TLS key | 0600 in the helper’s server directory; nats-server and same-user processes can read it and impersonate the server. No value in diagnostics or receipts. |
| CA certificate | 0644 in the server directory and mash config; everyone can read it. It is public, but replacing it consistently with trust state redirects authority. |
| Endpoint seed key | 0600 on the endpoint machine; mash and same-user processes can use it to act as that endpoint. `--key` may reveal its path in argv, never its value. |
| Endpoint credentials | 0600 in mash config or its selected generation; mash and same-user processes can act as the endpoint. No value in argv, logs, or receipts. |
| Token (JWT) | 0600 operator file transported by the person; install reads it in place, creates no copy, and leaves deletion to the person. Same-user processes can read it while present. `--token` reveals a path in argv; token contents must not appear there or in logs or receipts. |
| `trust.json` | 0600 in the selected generation; mash and same-user processes can read or replace it. It is not a secret; a coordinated replacement with config, CA, credentials, and pointer can redirect trust. |
| Backups and key archives | Exclusive 0600 files at person-named destinations under safe parents. Same-user processes and the person can read their contents. §5 reserves deletion and destination choices for the person; secret values remain barred from argv, logs, and receipts. |

For `REQ-013`, §3, the first token signature is checked against the account public key served by the **connected server**, whose certificate and hostname are checked using the independently supplied address and CA fingerprint. It is expressly **not** validation against only the token’s claimed issuer. This authenticates the server as the source of the key; it does not independently attest the account key. Later installs compare issuer, signature, server, and CA with the record. Exclusive creation and mode 0600 stop accidental overwrites and other users; they do not stop coordinated same-user substitution. The contract states and tests that limit accurately.

## Findings

**`SR11-1` — CARRIED `R9-2`; blocking; high certainty. `REQ-011(ii)`, §3.** A same-user process swaps a source pathname after adopt opens it. One sentence requires committing the original checked bytes and testing that result; the later “a file replaced during the copy is detected and refused” requires refusal. A reviewer cannot assert both. **Smallest change:** remove that last categorical sentence, or restrict it explicitly to a change in the held inode or bytes at the named checks. Keep the after-open pathname-swap success, destination-byte, and interruption assertions.

**`SR11-2` — NEW; blocking; high certainty. `REQ-013`, §3; `INV-004`, §5; `AT-007`, §6.** `mash trust reset` removes configuration, trust, and credentials, but its commit order, interruption behavior, and recovery state are unspecified. A kill after deleting credentials but before deselecting the generation can leave a selected, unusable generation; a kill after deselection may leave an apparently fresh machine with old secrets still present. The contract’s old-or-complete rule applies to **first install**, not reset. **Smallest change:** require reset to atomically deselect and durably sync the pointer before cleaning the old generation; state what remains after each interruption, whether cleanup is retried, and that commands refuse while reset is incomplete. Test kill before and after deselection, restart, credential usability, and recovery. Name reset explicitly in the §5 destructive-command confirmation and no-terminal tests. A terminal-driving agent can still answer its prompt; that is `S1`, not proof of human consent.

**`SR11-3` — NEW; blocking; high certainty. `REQ-013`, §3.** Rev 11 says all files are “written and synced in a new directory,” then switches the pointer and syncs “the directory.” It does not clearly require syncing the **new directory entry and contents before** publishing the pointer, as rev 10 did. After power loss, a durable pointer can refer to a generation whose directory entry was not made durable. **Smallest change:** explicitly sync each file **and the new generation directory** before the pointer switch, then sync the pointer’s parent after it. Assert that restart after a crash at each stage selects either nothing or a complete usable generation. Kill tests alone cannot verify the power-loss claim; record the durability operations and use a suitable crash test where available.

**`SR11-4` — NEW; blocking; medium certainty. `REQ-002`, §3; `REQ-011(i)`, §3; §5 “Authority of an agent.”** The agent may run the NATS helper from the skill, and the helper contains the expected nats-server and nsc hashes. The contract verifies downloaded binaries and the release install script, but gives no authenticated acquisition or byte check for the **helper script or skill** before execution. A substituted helper can change its embedded hashes or execute commands before checking them while satisfying the literal binary-verification rule. **Smallest change:** specify the trusted source and independently verified version or digest of the helper and skill, and require verification of the exact helper bytes before the agent runs them. Add a substituted-helper test. DRAFT5 was unavailable, so this is a contract-text finding, not a claim about its implementation.

**`SR11-5` — NEW; blocking; medium certainty. `REQ-011(iii)`, §3; §5 “Authority of an agent.”** A sparse “set mash up” authorizes installing every permission in the “pinned manifest,” but the contract never enumerates or bounds that baseline. A verified release could carry a manifest granting broad file, command, or network access; an agent could install it without a separate answer while obeying “any permission beyond” the manifest. Pinning proves which bytes were installed, not that their permissions match the person’s sparse request. **Smallest change:** name the allowed baseline permissions and their scope in the contract or a pinned, reviewed manifest incorporated into it; require the person’s separate answer for any broader grant, including a changed baseline across versions. Test the sparse request against the actual permission list and a broadened manifest.

### Rev 11 changes and residual boundary

Removing `endpoint delete --force` closes the flag bypass in text: `--delete-creds` still prompts, and §5 reserves deleting anything for the person. A non-terminal run must refuse under `INV-004`; an agent with a terminal can still type or auto-answer the destructive prompt and delete an endpoint or its local credentials. That is the stated `S1` residual. `adopt --from` now avoids telling anyone to remove the folder holding the CA key and signing keys; `--retire-keys` archives before removal, and its partial-archive test is named. Deferring `mash trust replace` removes the in-place rotation concurrency surface from 0.5.6. It leaves a deliberate period with no configured endpoint between reset and first install, and the reset recovery and first-install durability defects above must be fixed. There is no separate requirement for in-place rotation in this release.

## Gaps and written acceptances

- `AT-007`, §6 should explicitly inspect the caller’s token file after install and confirm that install made no copy; it should also inspect CA-key custody before and after `--retire-keys`. Those behaviors are now stated in §5 and are testable.
- `REQ-011(ii)`, §3 names an interruption test for adopt, but does not state whether interruption means kill only or also power loss. Its “commits atomically or is cleaned up” claim needs a restart-state assertion if it is meant to cover power loss.
- The contract does not specify how an agent obtains an authenticated helper or skill; `SR11-4` addresses that executable-code gap.
- `REQ-001`, §3 allows server and config paths in failures, detail, and status. These paths are not secret values under `INV-003`, but the contract does not state whether they may enter shared receipts; the no-secret scan alone will not decide that disclosure policy.

Each §7 residual is precise enough for **separate written acceptance for mash**, subject to the wording distinctions below. Randy’s homerange acceptance is precedent, not mash acceptance. The first four share same-account power, but have distinct effects; `S4` is a separate transport choice.

| §7 residual | Precision and required disposition |
|---|---|
| `S1` | Precise: typed input does not authenticate the typist. Needs Randy’s mash-specific written acceptance or a different authority mechanism. Applies to reset and destructive prompts too. |
| `S2` | Precise: same-user pathname races can redirect helper writes, archives, or removal. Needs separate written acceptance or a different filesystem isolation model. |
| Whole-generation substitution | Precise: a consistent config, CA, trust, credentials, and pointer substitution passes; `AT-007` demonstrates it. Needs separate written acceptance or protected trust storage. |
| Custody | Precise about signing seeds, readable backups, and same-user adoption-directory control. Needs separate written acceptance or a separate admin account or machine. |
| `S4` | Precise: an inherited override can send credentials and mail unencrypted without a fresh choice. Any process with loopback packet-capture rights can observe that plaintext. Needs separate written acceptance or a fresh-choice mechanism. |

On `NFR-003`’s requested network question, §5 says exactly what the default exposes: one TLS client port on `127.0.0.1`, reachable for connection attempts by **every local OS user**, with NATS credentials required for authorization; no default monitoring, cluster, gateway, or leaf-node ports. TLS protects another connection’s payload. Under the plaintext override, a process able to capture loopback traffic can read credentials and mail. On `NFR-002`’s requested authority question, §5 permits verified downloads, default loopback helper setup, named endpoints, and the pinned user-scope plugin; the person must supply the adoption directory, address and fingerprint, broader permissions, exposure changes, and deletions. `SR11-5` is the permission-widening hole in that wording.

## Summary

I find **five blocking contract defects** (`SR11-1` through `SR11-5`) and **five separately stated residual risks** requiring Randy’s written mash-specific acceptance. Rev 11 closes `R9-1` and `R9-6` in testable text; `R9-2` remains partly open. **Apart from the five residual acceptances, the five findings still block ratification in my view.** This is a text review only; I could not verify code, DRAFT5, or any runtime or acceptance result.
---

Inputs (sha256):

```
f8e2ff9fdecb059eea5d2962d31d94c8fad2a5e443b98ea1dda2f683779c0cde  ./contract-0.5.6-cli-ux-rev11-draft.md
e4952ae284364f2e012319c357245910402765b90b987cdadaf3316df5416aa7  ./prior/contract-0.5.6-cli-ux-rev10-draft.md
1510a6919cd4b7705cb0a15a196b72bd3c6bf2aa2be33586bc0b5f9af792b084  ./prior/contract-0.5.6-cli-ux-rev9-draft.md
f37e4db46c87b2eefa2df8d409e63578e4371161e89813273154c42c6f4b65ec  ./prior/security-read-rev9.md
9e8d9c48c2fde84244ca65c4ea32c331b2154a184535611d0ffe97a5a037f531  ./standards/cli/STANDARD.md
ed7b2448870e7cf899e4e8dc3b2735ff0ce8d8701844e5cb1e9b2b3152b8f73d  ./standards/contract/CHECKLIST.md
d96d969f78ff7e4eef8fc22a6b7b12db5a8ef3776b9a6a9b970e26ab45a3de85  ./standards/contract/STANDARD.md
9470bf08d732b908689ff2def78c271e852d77e6a1840ad2e5b4a9d7a02647a0  ./standards/skills/contract-critic/SKILL.md
ed1f58552eef3f041f4d37bb709557f6bcd7809f8eb00d5fb08aade797ac68cc  ./standards/standing-orders/mash.md
```
