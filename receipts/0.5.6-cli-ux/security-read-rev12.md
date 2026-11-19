# Security reread, mash 0.5.6 work contract (cli-ux) revision 12 draft

Review lane: x-ops2 (operations). Date: 2026-11-19. Advisory to the contract, not a release gate (mash-lead #886). Security only; a reread after our rev 11 read (receipts/0.5.6-cli-ux/security-read-rev11.md, bogworks/mash PR #838).
Reviewed: the mash lead's draft contract mash-0.5.6-cli-ux, revision 12 (draft), contract@0.3.4 (lane/lead 7d4f6ae), copy at s3://homerange-xfer/masterplanner/for-herds/mash-0.5.6/contract-0.5.6-cli-ux-rev12-draft.md (sha256 1714553c1651e18e2cb309b4fccd8b94ed3946c809147c89d49ffaadd86953e7). Not handed over: the helper script and the skill DRAFT5 (not landed).
Reviewer: OpenAI Codex, run by kit/codex-review (x-herd#132), `codex exec --sandbox read-only`, codex-cli 0.170.0, model gpt-6-sol, Daybreak ON (codex -c daybreak=true); the attestation was stated to the reviewer in its prompt. Other family from the writer (Anthropic). The reviewer read text and ran nothing.
Handed over (sha256 list at the end): the rev 12 draft; standards from bogworks-dev-standards 68475627ac8c673d9510cdeccd675a44d8765c07 (contract/STANDARD.md 0.3.4, contract/CHECKLIST.md, standing-orders/mash.md, cli/STANDARD.md, skills/contract-critic/SKILL.md); under prior/, the rev 11 draft and our rev 11 read. Nothing else. Where the content went: the pages above were sent to OpenAI through the operator's Codex subscription for this review.

---

# Security read, mash 0.5.6 cli-ux revision 12 (draft)

Reviewer attestation: this security runs on model gpt-6-sol, Daybreak ON (codex -c daybreak=true), codex-cli 0.170.0, read-only sandbox.

I read the revision 12 contract (contract-0.5.6-cli-ux-rev12-draft.md), the revision 11 security read (prior/security-read-rev11.md), and the standards present in this workspace. I ran nothing. The helper, skill DRAFT5, code, and acceptance evidence were unavailable. The prompt’s `dev-process/`, `brief/`, ADR, and `standing-orders/homerange.md` files are also absent here. This contract has no `NFR-002` or `NFR-003` ids; their authority and network subjects appear under those names in §5.

## Threat model

The assets are signing seeds, TLS keys, endpoint credentials, mail, and the binding to the intended server. An installing agent runs with the user’s account and may have only “set mash up” as authority. Other processes of that account can read and replace its files; other host users can attempt loopback connections. The decisive boundaries are the person’s choice versus an agent’s action, verified download bytes versus executed bytes, owner-only files versus same-user processes, and a token’s claims versus a server identified through a separately supplied address and CA fingerprint.

## Revision 11 disposition

“Closed” describes testable contract words, not an implementation result.

| Prior finding | Revision 12 status | Closing words and check |
|---|---|---|
| `SR11-1` | **Closed** | `REQ-011(ii)`, §3 defines “replaced during the copy” as a change to the held inode or bytes at named checks. An after-open pathname swap commits the original checked bytes. The specified swap and destination-byte test can distinguish the outcomes. |
| `SR11-2` | **Closed for the stated kill model** | `REQ-013`, §3 orders pointer deselection and parent sync before removal, restricts commands while old files remain, and makes reset retryable. `INV-004`, §5 and `AT-007`, §6 include reset in typed-confirmation and kill tests. Kill before and after deselection and inspect selection, remaining files, refusal, and retry. |
| `SR11-3` | **Closed in text** | `REQ-013`, §3 orders each file sync, new generation directory sync, pointer switch, then parent sync, for first and later installs. `AT-007` records calls in order; §3 calls for a crash test where available. Runtime durability remains unverified. |
| `SR11-4` | **Closed for downloaded-byte substitution** | `REQ-011(i)` and `(i-b)`, §3 put helper and skill digests in the attested script, require checks before installation, and direct the agent to the printed installed helper path. Substituted downloads must install nothing. Attestation of the script is the trust anchor; its digests do not independently review the release workflow. Later same-user replacement is expressly `S2`. |
| `SR11-5` | **Closed in text** | §5 bounds the baseline to one `mash serve` stdio MCP server, three named hooks running the plugin launcher, skills as text, and no permission allow-list entries. `AT-007` inspects the installed list and tests a broadened manifest. A changed upgrade baseline requires the person’s answer. |

The other revision 11 gaps are addressed: `AT-007`, §6 inspects the caller’s token file and CA-key custody, confines failure paths in receipts to scratch homes, and expressly calls adopt’s interruption test **kill only**. `REQ-011(ii)` relies on the stated sync order for its power-loss claim; no power-loss result was available to verify.

## Findings

**`SR12-1` — NEW; blocking; high certainty. `REQ-003` and `REQ-013`, §3; `INV-004`, §5; `AT-007`, §6.** The first-install CA rule admits incompatible implementations. `REQ-003` says the fingerprint is typed “whenever an install writes or changes a CA” and “with no CA involved it asks nothing”; `--ca <file>` is optional. `REQ-013` instead requires the person to type **both** server address and CA fingerprint before the first write and includes a CA file in every new generation. An implementer could treat omitted `--ca` as “no CA involved,” skip the independent fingerprint, and still claim to follow `REQ-003`. That weakens first-use server binding precisely where there is no trust record. **Smallest change:** say whether first install requires `--ca`. If it does, refuse its omission; if another CA source is allowed, name that source and require the same independently supplied fingerprint and certificate/hostname check. Add an `AT-007` first-install case with `--ca` omitted and an explicit expected result. This needs a contract change, or Randy’s written acceptance of the weaker first-use rule.

**`SR12-2` — NEW; blocking; medium-high certainty. `REQ-013`, §3; `AT-007`, §6.** Generation writes and reset have an interruption rule but no rule for **concurrent** mutations. Two installs can each validate the same selected generation, publish different complete successors, and both report success although only the last selection survives. More seriously, reset can deselect while an install publishes a new generation, leaving credentials present but unselected, or causing one operation to report a state the other has undone. “Every command reads one generation” and atomic pointer switches do not serialize these decisions. **Smallest change:** require installs and reset to serialize from validation through publication or deselection and cleanup, with the second operation rechecking the current selection before acting. Test concurrent install/install and install/reset attempts, including an interrupted lock holder. This is a contract change; accepting it instead would need Randy’s written acceptance.

### Credential and trust inventory

`INV-003`, §5 bars secret **values** from argv, examples, logs, JSON, errors, and receipts; `AT-007`, §6 scans those channels. Paths supplied to `--key` and `--token` can appear in argv. Modes 0600 and 0700 exclude other OS users, not a process or agent running as the owner.

| §5 row | Location and readers; agent action and exposure |
|---|---|
| Operator signing seed, account seed, system credentials | mashctl config, 0600; mashctl and same-user processes can read them. An agent with that account can mint identities or read mail. Values must stay out of argv, logs, and receipts. |
| Admin credentials | mashctl config, 0600; same-user processes can manage endpoints and buckets. No credential value in argv, logs, or receipts. |
| CA key | Helper directory, 0600, until the person archives or retires it; same-user processes can mint server certificates. Archive contents and diagnostics must not disclose it. |
| Server TLS key | Helper server directory, 0600; nats-server and same-user processes can read it and impersonate the server. No value in output or receipts. |
| CA certificate | Server directory and mash config, 0644; publicly readable. Consistent replacement with trust state can redirect authority. |
| Endpoint seed key | Endpoint machine, 0600; mash and same-user processes can act as the endpoint. `--key` exposes a path in argv, not the seed value. |
| Endpoint credentials | Mash config generation, 0600; mash and same-user processes can act as the endpoint. No value in argv, logs, or receipts. |
| Token (JWT) | Operator’s 0600 file carried by the person; install reads it in place and leaves deletion to the person. Same-user processes can read it while present. `--token` exposes its path in argv; its value must not enter logs or receipts. |
| `trust.json` | Selected mash config generation, 0600; same-user processes can replace it. A coordinated replacement with config, CA, credentials, and pointer can redirect trust. |
| Backups and key archives | Person-named destinations, exclusive 0600 under safe parents; the person and same-user processes can read their contents. The agent must obtain the destination choice; contents remain barred from argv, logs, and receipts. |

`REQ-013`, §3 verifies a first token against the account key **served by the connected server**, not merely the token’s claimed issuer. The independently conveyed address and CA fingerprint authenticate that server; they do not separately attest its account key. Subsequent installs compare issuer, signature, server, and CA against the record. Exclusive 0600 creation and an atomic rename protect against accidental overwrite and other OS users; they do not prevent a same-user process from replacing a whole consistent generation. The contract states this limit and tests that the coordinated replacement passes.

`mash trust replace` is expressly deferred by `REQ-013`; reset followed by a first install is the 0.5.6 rotation path. Reset’s post-kill recovery is specified and testable as above. `adopt --from`, `REQ-011(ii)`, names parent, ownership, symlink, held-handle, signature-chain, certificate, hostname, destination-byte, and kill checks. Its local directory is explicitly the origin of the address, so a consistent directory prepared by the same user remains the stated custody residual.

## Gaps and residual acceptances

The install script’s attestation is checked against the named repository, workflow, tag, and subject before execution. Its embedded digests then bind the binaries, helper, and skill; a README digest or a `SHA256SUMS` beside an artifact is not the script’s trust anchor. `go install` and the marketplace plugin remain identified as unverified developer routes. The embedded `mash plugin install` route is user scope, from the verified binary, with no network fetch. These are checkable requirements, but the absent release artifacts and DRAFT5 prevent a claim that they work.

`--server` removal makes the person type the first address; `REQ-013` supplies the trust check. §5 assigns exit 4 to server credential refusal in both tools. Tests can check both changes. The §5 network rule is also concrete: default exposure is one TLS client port on `127.0.0.1`, reachable for attempts by every local user, with NATS credentials required; no other listed NATS port opens by default. TLS protects another connection’s payload. Under `MASH_INSECURE_PLAINTEXT=1`, a process with loopback packet-capture rights can read credentials and mail. The inherited-variable case remains a residual.

The contract is silent on concurrent install/reset arbitration (`SR12-2`). It is also silent on a first install without `--ca` in a way that reconciles `REQ-003` and `REQ-013` (`SR12-1`). Both need observable outcomes before ratification.

Section 7 correctly calls Randy’s homerange acceptance **precedent, not acceptance for mash**. Each of these five wordings is precise enough for a separate written mash acceptance:

| §7 residual | Precision and disposition |
|---|---|
| `S1` | Precise: a typed answer does not authenticate the person, including for reset and destructive commands. Accept separately or change the authority mechanism. |
| `S2` | Precise: same-user path swaps can redirect helper writes, archive, or removal; later replacement can substitute the installed helper. Accept separately or change filesystem isolation. |
| Whole-generation substitution | Precise: consistent trust/config/CA/credentials/pointer replacement can pass. Accept separately or protect trust from that account. |
| Custody | Precise: same-user access to signing material and backups, plus a same-user prepared adoption directory. Accept separately or move signing custody to another account or machine. |
| `S4` | Precise: an inherited plaintext override can expose credentials and mail without a fresh choice. Accept separately or require a fresh choice. |

The first four share the **same-account authority class**, but have distinct effects and warrant individually recorded acceptance. `S4` is a separate transport risk. None is covered by the homerange decision.

## Summary

I find **two new blocking contract findings** and **zero carried revision 11 findings** still open in text. Revision 12 closes `SR11-1` through `SR11-5` with checkable requirements. **Apart from the five residuals requiring Randy’s separate written mash acceptance, `SR12-1` and `SR12-2` still block ratification in my view.** I verified wording only; I ran no code or tests and could not inspect the helper, skill DRAFT5, release artifacts, or runtime evidence.
---

Inputs (sha256):

```
1714553c1651e18e2cb309b4fccd8b94ed3946c809147c89d49ffaadd86953e7  ./contract-0.5.6-cli-ux-rev12-draft.md
f8e2ff9fdecb059eea5d2962d31d94c8fad2a5e443b98ea1dda2f683779c0cde  ./prior/contract-0.5.6-cli-ux-rev11-draft.md
64a8eedf06e6c55688f64599705cc9913782a0194e8a952ae6e6cb291bac2008  ./prior/security-read-rev11.md
9e8d9c48c2fde84244ca65c4ea32c331b2154a184535611d0ffe97a5a037f531  ./standards/cli/STANDARD.md
ed7b2448870e7cf899e4e8dc3b2735ff0ce8d8701844e5cb1e9b2b3152b8f73d  ./standards/contract/CHECKLIST.md
d96d969f78ff7e4eef8fc22a6b7b12db5a8ef3776b9a6a9b970e26ab45a3de85  ./standards/contract/STANDARD.md
9470bf08d732b908689ff2def78c271e852d77e6a1840ad2e5b4a9d7a02647a0  ./standards/skills/contract-critic/SKILL.md
ed1f58552eef3f041f4d37bb709557f6bcd7809f8eb00d5fb08aade797ac68cc  ./standards/standing-orders/mash.md
```
