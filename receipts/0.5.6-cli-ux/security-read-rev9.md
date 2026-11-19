# Security reread, mash 0.5.6 work contract (cli-ux) revision 9 draft

Review lane: x-ops2 (operations). Date: 2026-11-19. Advisory to the contract, not a release gate (mash-lead #875). Security only; a reread after our rev 8 read (receipts/0.5.6-cli-ux/security-read-rev8.md, bogworks/mash PR #836).
Reviewed: the mash lead's draft contract mash-0.5.6-cli-ux, revision 9 (draft), contract@0.3.4, copy at s3://homerange-xfer/masterplanner/for-herds/mash-0.5.6/contract-0.5.6-cli-ux-rev9-draft.md (sha256 99016573c74d07b30975167f190622ae23d574d6e5cea2e015563041f36a277f). Not handed over: the helper script and the skill DRAFT5 (not landed).
Reviewer: OpenAI Codex, run by kit/codex-review (x-herd#132), `codex exec --sandbox read-only`, codex-cli 0.170.0, model gpt-6-sol, Daybreak ON (codex -c daybreak=true); the attestation was stated to the reviewer in its prompt. Other family from the writer (Anthropic). The reviewer read text and ran nothing. (The dev-process/, brief/ and ADR files it mentions as absent belong to the homerange template and are not part of this workspace.)
Handed over (sha256 list at the end): the rev 9 draft; standards from bogworks-dev-standards 68475627ac8c673d9510cdeccd675a44d8765c07 (contract/STANDARD.md 0.3.4, contract/CHECKLIST.md, standing-orders/mash.md, cli/STANDARD.md, skills/contract-critic/SKILL.md); under prior/, the rev 8 draft and our rev 8 read. Nothing else. Where the content went: the pages above were sent to OpenAI through the operator's Codex subscription for this review.

---

# Security read, mash 0.5.6 cli-ux revision 9 (draft)

Reviewer attestation: this security runs on model gpt-6-sol, Daybreak ON (codex -c daybreak=true), codex-cli 0.170.0, read-only sandbox.

I read the revision 9 contract (contract-0.5.6-cli-ux-rev9-draft.md), the revision 8 security read (prior/security-read-rev8.md), and the supplied standards. This is a **text review**. I ran no installer, helper, skill, binary, plugin, or acceptance test. Helper and skill DRAFT5 were not supplied. The named `dev-process/`, `brief/`, and ADR files are absent from this workspace, so I could not check their contents.

## Threat model

The assets are signing seeds, TLS keys, administrator and endpoint credentials, mail, and the binding between an endpoint and its intended server. An installing agent may act on a sparse request with the person’s OS account. Other processes under that account can read or replace its files; other host users can reach a loopback port. The boundaries that matter are person versus agent authority, owner-only files versus same-user processes, verified artifacts versus bytes actually run, and a token’s claims versus the server authenticated through the separately supplied address and CA fingerprint.

## Revision 8 disposition

“Closed” here means the **contract words** state a checkable rule, not that code passed a test.

| Prior item | Revision 9 status | Closing words and testability |
|---|---|---|
| `R8-1` | **Closed in text** | `REQ-013`, §3 says the complete generation “connects with it” **before** the pointer switch, forbids commands seeing an unvalidated candidate, serializes replace and repair, and names concurrent-command, second-replacement, interruption, restart, connection, and repair checks. Those are runnable acceptance scenarios. |
| `R8-2` | **Partly closed** | `REQ-011(i)`, §3 binds the attested subject to a private copy and calls for a digest check immediately before running. Its requirement that *any* “swap between verify and run refuses” still conflicts with safely executing an already verified private copy; it also does not state how that copy is protected between its final digest check and execution. See finding `R9-1`. |
| `R8-3` | **Partly closed** | `REQ-011(ii)`, §3 now defines success by bytes checked through held handles. It retains the categorical sentence “a file replaced during the copy is detected and refused,” although a source pathname swap after opening need not change the held file. The named swap test has no explicit expected result for that case. See `R9-2`. |
| `R8-4` | **Closed in text; acceptance pending** | §7 now names the pointer and the **whole** generation, including config, CA, `trust.json`, and credentials. `AT-007`, §6 expects a coordinated replacement to pass, making the limit testable. Randy still must accept this risk for **mash** in writing. |
| `R8-5` | **Partly closed** | §5 locates the CA key in the helper directory before optional retirement and distinguishes the installer’s token copy from the source. The latter copy’s creation and deletion are not defined in `REQ-003` or checked specifically in `AT-007`. See the gaps below. |

The four other revision 8 gaps are substantially answered: `REQ-013`, §3 explicitly calls first use authentication of **the server as source of the account key**, not independent authentication of that key; it specifies the refused-replacement recovery state and tests; §8 publishes only the attested script and embedded-plugin routes, naming `go install` and the repository marketplace as unverified developer routes; and §7 requests separate mash-specific acceptance for each residual. The first-use check is testable with the named self-signed-token, substituted-address, and issuer-mismatch cases. It does **not** protect against a malicious server that has a certificate valid under the independently supplied CA.

## Credential and trust inventory

`INV-003`, §5 bars secret **values** in argv, examples, logs, JSON, errors, and receipts; `AT-007`, §6 calls for scans of those channels. The `--key` and `--token` arguments in `REQ-003` are file paths, not secret values. Mode 0600 limits other OS users, but does not exclude processes or an agent running as the owner.

| §5 row | Location and readers; agent authority and exposure |
|---|---|
| Operator signing seed, account seed, system credentials | 0600 in mashctl’s config directory. Mashctl and same-user processes can read them; an agent with that account can use mashctl and potentially read the files, mint identities, and read mail. Values must stay out of argv, logs, and receipts. |
| Admin credentials | 0600 in mashctl’s config directory after adoption. Same-user processes can read them and administer endpoints and buckets. Values must stay out of argv, logs, and receipts. |
| CA key | 0600 in the helper directory during setup, readable to the owner and same-user agent until the person stores it offline or retires it. It can mint server certificates. The contract does not make retirement automatic. No value belongs in argv, logs, or receipts. |
| Server TLS key | 0600 in the helper’s server directory, read by nats-server and same-user processes; leakage permits server impersonation. Helper diagnostics and receipts must not reveal it. |
| CA certificate | 0644 in the server directory and mash config; all local users can read it. It is public, but substitution changes trust if paired with a consistent generation and pointer. |
| Endpoint seed key | 0600 on the endpoint machine, read by mash and same-user processes. With the token, it permits endpoint access. No value belongs in argv, logs, or receipts. |
| Endpoint credentials | 0600 in mash config and the selected generation, read by mash and same-user processes. An agent can act as that endpoint. No value belongs in argv, logs, or receipts. |
| Token (JWT) | An operator source file is copied to the endpoint machine; the row says install deletes its own temporary copy while the person deletes the source. Same-user processes may read surviving copies. With the endpoint key, the token permits endpoint access. `--token` exposes its path in argv, not its value. The copy lifecycle needs a specific acceptance check. |
| `trust.json` | 0600 in the selected mash generation. Same-user processes can read or replace it; a consistent replacement with the generation and pointer can redirect trust despite permission mode and disagreement checks. Its contents are not secret. |
| Backups and key archives | Exclusive 0600 files at person-named destinations under safe parents. The person and same-user processes can read whatever secrets they contain. An agent may create them only within §5 “Authority of an agent”; values remain barred from argv, logs, and receipts. |

## Findings

**`R9-1` — CARRIED `R8-2`; blocking; medium certainty. `REQ-011(i)`, §3; `REQ-002`, §3.** A same-user process swaps the *download pathname* after verification while the exclusively held private copy remains intact. Running the verified copy is safe, yet “a swap between verify and run refuses before any of its code runs” demands refusal. Conversely, if the final digest check is on a mutable private pathname, a same-user process can replace its bytes after the check and before execution. The contract does not say which object is executed or what prevents that last change. **Smallest change:** define the executable object and its protection through execution; require refusal when the bytes to be executed differ from the attested subject. State that swapping an unused source pathname after a private copy is secured either has no effect or is detected by an explicit source recheck. Give the script and both binaries separate expected results for those timed swaps. The attestation’s repository, workflow, tag, and subject rule is otherwise concrete; a README or release-notes digest is not an accepted substitute for the script. `go install` remains explicitly unverified.

**`R9-2` — CARRIED `R8-3`; blocking; high certainty. `REQ-011(ii)`, §3.** `adopt --from` opens and checks a source file, then another same-user process replaces that file’s pathname before commit. The held handle still yields the checked bytes; the destination comparison succeeds. That meets the new safe-success rule, but violates “a file replaced during the copy is detected and refused.” A reviewer cannot implement one unambiguous expected result for the specified after-open swap test. **Smallest change:** remove the categorical refusal sentence. Require refusal when the held inode or checked bytes differ at a named check; for a pathname-only swap after open, explicitly expect either commitment of the original checked bytes or refusal after a specified pathname recheck. Test the destination bytes and the state after interruption. The signature chain, TLS hostname check, safe-parent checks, and refusal to replace an existing trust record remain stated and testable. A fully consistent agent-prepared source directory remains a custody residual, not a failed signature check.

**`R9-3` — CARRIED `R8-4`; blocking pending written acceptance; high certainty. `REQ-013`, §3; `AT-007`, §6; §7 “Residual risks for Randy’s written acceptance.”** A same-user process replaces the pointer and a whole internally consistent generation, including credentials and CA. Agreement checks pass, and the attacker can redirect trust or substitute endpoint authority. “Created exclusively, 0600” stops an accidental overwrite and other OS users; it does not stop a process with the owner’s account from replacing these files. Revision 9 describes this accurately and gives the coordinated-replacement test a passing expected result. **Smallest disposition:** retain the wording and obtain Randy’s separate written acceptance for **mash**, or change the trust-storage model. His homerange acceptance is precedent, not this decision.

**`R9-4` — CARRIED residuals; blocking pending written acceptance; high certainty. `INV-004`, §5; §5 “Authority of an agent”; §7.** An agent that controls an interactive terminal can type the address, fingerprint, interface, or deletion confirmation itself. The prompt proves only that text was entered, as `S1` says. The sparse-request skill rule and no-terminal refusal constrain a cooperative agent; they do not authenticate the typist. Separately, a same-user process can race the helper’s pathname checks (`S2`), or read the mashctl signing seeds and an agent-prepared adoption directory (custody). These have different effects and need individual dispositions even though all involve same-account power. **Smallest disposition:** keep each separately named and tested, and obtain Randy’s written mash-specific acceptance for `S1`, `S2`, and custody, or change the respective authority or isolation model.

**`R9-5` — CARRIED residual; blocking pending written acceptance; high certainty. `REQ-010`, §3; §5 “Network exposure”; §7 `S4`.** A parent process sets `MASH_INSECURE_PLAINTEXT=1`; a later command inherits it and sends credentials and mail without TLS. Every local user can attempt the 127.0.0.1 client port but, without NATS credentials, is refused; TLS normally hides another connection’s payload. With plaintext, a process able to capture loopback traffic can read those credentials and messages. The per-command warning and inherited-variable test make the limit observable, but cannot establish a fresh human choice. **Smallest disposition:** obtain separate written mash-specific acceptance for `S4`, or require a fresh choice that an inherited environment cannot supply.

**`R9-6` — NEW; nonblocking; high certainty. `REQ-002`, §3; `REQ-003`, §3; §5 “Credential inventory”; `AT-007`, §6.** The token row says install deletes “its own temporary copy,” but `credential install --token <file>` does not require creation of such a copy or say whether an operator-supplied file is left untouched. An implementer could delete the only supplied token or retain an extra transfer copy while satisfying a generic no-secret-output scan. **Smallest change:** name each token file the install command owns, explicitly leave caller-supplied source files to the person, and test the surviving and removed paths. Add a check of the CA key’s initial and post-retirement states and receipt contents.

## Gaps and acceptance boundary

- `REQ-013`, §3 says an interruption leaves old or new trust selected, but does not distinguish process interruption from host power loss or require durable writes before the pointer switch. State the fault model for the interruption tests. The refused live replacement path is otherwise concrete: old generation selected and connecting across restart, rejected files removable by `mash trust repair`.
- `REQ-002`, §3 still calls “the plugin route” unverified while `REQ-011(iii)`, §3 and §8 call the embedded `mash plugin install` route verified. Qualify the former as the **repository marketplace** route. The embedded route’s no-network, user-scope, pinned manifest and digest, destination inspection, and separate-choice rules are testable; the referenced DRAFT5 skill was unavailable to inspect.
- §5 “Authority of an agent” reserves **any plugin permission** and plugin scope for the person. `REQ-011(iii)`, §3 expressly requires a separate choice only for permission *beyond* the pinned manifest. State whether a sparse “set mash up” request permits installing the pinned manifest at user scope without a further answer. The sparse-request skill test should assert that exact behavior.
- §7’s “same class” framing is fair as a description of shared same-user power, but it must not collapse decisions. `S1`, `S2`, whole-generation substitution, and custody require distinct mash-specific written acceptances; `S4` is a separate plaintext-transport decision. Their current wording is precise enough for Randy to accept individually. None is accepted for mash by his homerange decision.

## Summary

I find **2 blocking contract defects** (`R9-1`, `R9-2`), **1 nonblocking defect** (`R9-6`), and **5 separately stated residual risks pending Randy’s written mash-specific acceptance** (`S1`, `S2`, whole-generation substitution, custody, `S4`). `R8-1` is closed in testable text; `R8-4` is precisely described but awaits acceptance; `R8-2` and `R8-3` remain partly open. **Apart from the residual acceptances, `R9-1` and `R9-2` still block ratification in my view.**
---

Inputs (sha256):

```
99016573c74d07b30975167f190622ae23d574d6e5cea2e015563041f36a277f  ./contract-0.5.6-cli-ux-rev9-draft.md
ccbb1750180fc8b4f137a76db107905317bcc71a5d362d70ff3f185e25cb42a4  ./prior/contract-0.5.6-cli-ux-rev8-draft.md
d84443885df8baac01100b4bd09ab94514b35db92d008344c9cd0acb3ab64a3f  ./prior/security-read-rev8.md
9e8d9c48c2fde84244ca65c4ea32c331b2154a184535611d0ffe97a5a037f531  ./standards/cli/STANDARD.md
ed7b2448870e7cf899e4e8dc3b2735ff0ce8d8701844e5cb1e9b2b3152b8f73d  ./standards/contract/CHECKLIST.md
d96d969f78ff7e4eef8fc22a6b7b12db5a8ef3776b9a6a9b970e26ab45a3de85  ./standards/contract/STANDARD.md
9470bf08d732b908689ff2def78c271e852d77e6a1840ad2e5b4a9d7a02647a0  ./standards/skills/contract-critic/SKILL.md
ed1f58552eef3f041f4d37bb709557f6bcd7809f8eb00d5fb08aade797ac68cc  ./standards/standing-orders/mash.md
```
