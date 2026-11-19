# Security reread, mash 0.5.6 work contract (cli-ux) revision 22 draft

Review lane: x-ops2 (operations). Date: 2026-11-19. Advisory to the contract, not a release gate (mash-lead #965). Security only; a reread after our rev 21 read (receipts/0.5.6-cli-ux/security-read-rev21.md, bogworks/mash PR #852), asked as a clean-read attempt. Randy's decision 264 stands: the same-OS-user risk is accepted for mash and was not raised.
Reviewed: the mash lead's draft contract mash-0.5.6-cli-ux, revision 22 (draft), contract@0.3.4 (lane/lead 94a20d1), copy at s3://homerange-xfer/masterplanner/for-herds/mash-0.5.6/contract-0.5.6-cli-ux-rev22-draft.md (sha256 6e707fec5fae62b3d7f77e01b2d558b61923bce100284293948d5d57cee4566b). Not handed over: the helper script and the skill (not landed).
Reviewer: OpenAI Codex, run by kit/codex-review (x-herd#132), `codex exec --sandbox read-only`, codex-cli 0.170.0, model gpt-6-sol, Daybreak ON (codex -c daybreak=true); the attestation was stated to the reviewer in its prompt. Other family from the writer (Anthropic). The reviewer read text and ran nothing.
Handed over (sha256 list at the end): the rev 22 draft; standards from bogworks-dev-standards 68475627ac8c673d9510cdeccd675a44d8765c07 (contract/STANDARD.md 0.3.4, contract/CHECKLIST.md, standing-orders/mash.md, cli/STANDARD.md, skills/contract-critic/SKILL.md); under prior/, the rev 21 draft and our rev 21 read. Nothing else. Where the content went: the pages above were sent to OpenAI through the operator's Codex subscription for this review.

---

# Security read, mash 0.5.6 cli-ux revision 22 (draft)

Reviewer attestation: this security runs on model gpt-6-sol, Daybreak ON (codex -c daybreak=true), codex-cli 0.170.0, read-only sandbox.

I read the supplied workspace text and ran nothing. The helper, install skill, binaries, and tests are not present, so this review assesses requirements, not implementation.

## Threat model

The assets are signing seeds, endpoint keys and credentials, mail, and the server and account an endpoint trusts. An installing agent may act on a sparse request; the person’s separate operator hand-off is the first-install trust boundary. Other OS users can reach a loopback listener but should fail authentication. Downloaded binaries and plugin bytes cross a supply-chain boundary before execution. Files, process arguments, output, logs, and receipts are possible disclosure paths. Randy’s decision 264 accepts the same-OS-user standing risk for mash; I do not raise it.

## Revision 21 disposition

| Prior item | Revision 22 status |
|---|---|
| Finding 1, no independent account-key input | **Closed in text.** `REQ-003`, §3 requires `--account <key>` from the operator’s separate message and a byte-for-byte issuer check before writing. The affirmative-answer mismatch test is specified in `REQ-003`, §3. `AT-007`, §6 should name that affirmative answer explicitly. Behavior remains unverified. |
| Finding 2, inherited plaintext override | **Open, nonblocking.** `REQ-010`, §3 still permits an inherited value and requires a warning. `AT-007`, §6 tests that warning. |
| Finding 3, writable or symlinked binary destination | **Closed in text.** `REQ-011(i)`, §3 requires refusal before copying for a symlinked destination or parent, or one writable by another user, and calls for a refusal test. Behavior remains unverified. |
| `config.json` gap, later-install commit point | **Partly closed.** `REQ-003`, §3 adds a temporary credentials file and final rename that mash must ignore until commit. `AT-007`, §6 does not expressly test a kill between those steps or disposal of the temporary file. |

## Findings

**1. NEW — blocking; medium certainty. `REQ-003`, §3; `AT-007`, §6 — credentials can reach a server before the person confirms it.** A person receives a substituted server address and CA file alongside a genuine token and key. Install can verify TLS against that CA and authenticate to that server, then show the comparison screen. The person answers “no,” but the server has already received the JWT. `REQ-003` requires confirmation before *writing*, while it places the connection among checks done “before writing”; it does not require confirmation before authenticated network use. `INV-001`, §5 treats a JWT as a secret. **Smallest change:** require the three-value confirmation and the `--account` issuer comparison before any credential-bearing connection; add an `AT-007` refusal test in which the person answers “no” and the server observes no authentication attempt.

**2. NEW — blocking; high certainty. `REQ-003`, §3; `INV-003`, §5; `AT-007`, §6 — an interrupted later install can leave an extra credentials copy.** With `config.json` already present, kill install after it writes the temporary credentials file but before rename. On rerun, an implementation can create a second temporary file, rename that one, and leave the first behind. Mash may correctly ignore both temporary names, yet a JWT-bearing 0600 file remains after success. The new commit-point words do not define temporary-file cleanup; `AT-007`’s “no copy … but the credentials file” and interrupted-state checks do not expressly cover this later-install sequence. **Smallest change:** require a temporary file in the destination directory, define which temporary names mash ignores, and require a rerun to remove or safely reuse its stale temporary file before reporting success. Test a kill immediately before rename, rerun, and inspect both mash behavior and remaining files.

**3. CARRIED — nonblocking; high certainty. `REQ-010`, §3; §5 “Network exposure”; `AT-007`, §6 — inherited plaintext remains sufficient authorization.** A parent process sets `MASH_DANGEROUSLY_SKIP_NATS_ENCRYPTION=1`; a child command inherits it and connects to a plaintext-capable server. Its warning does not demand a fresh choice. A process with loopback packet-capture rights can then read credentials and mail; on a wider plaintext route, an observer there can do the same. The contract explicitly permits inherited values. **Smallest change, if the writer intends to remove this exposure:** require an explicit choice for that destination on the run; an inherited variable alone would not enable plaintext. Section 7 records Randy’s treatment of the override.

## Credential and trust table

`INV-001` and `INV-003`, §5 ban secret values from argv, output, JSON, logs, errors, examples, and receipts; `AT-007`, §6 calls for scans of those channels. Public paths may appear where the contract allows them. The public `--account` value is an argument, not a secret.

| §5 credential row | Location and readers | Permitted agent action and exposure |
|---|---|---|
| Operator signing seed, account seed, system credentials | mashctl config, 0600; mashctl reads them | The agent may run the bounded helper and local `mashctl adopt --from`. Values in argv, logs, or receipts would permit identity minting and mail access. |
| Admin credentials | mashctl config, 0600; mashctl reads them | Local adopt is permitted. A leaked value permits endpoint and bucket administration. |
| CA key | Helper directory, 0600; setup user reads it | The agent may create it through the default helper. A leak permits server-certificate minting. |
| Server TLS key | Helper server directory, 0600; that user’s `nats-server` reads it | The agent may start the recorded local server. A leak permits server impersonation. |
| CA certificate | Server directory and mash config, 0644; everyone may read it | The agent may copy it. It is public; its full fingerprint is shown for the person’s comparison. |
| Endpoint seed key | Endpoint machine, 0600; mash reads it | The agent may create a same-machine endpoint for a person-named agent. `--key` is a path; the value must not enter argv, output, logs, or receipts. |
| Endpoint credentials | Mash config, 0600; mash reads them | The agent may create local, person-named endpoints. Secret values must stay out of the listed channels. The later-install temporary copy is finding 2. |
| Token (JWT) | Person-carried operator file, 0600; install reads it in place | The agent hands the person the install command and stops. `--token` is a path. The person deletes the source after install; disclosure with its key permits acting as the endpoint. |

The contract has **no `NFR-002` or `NFR-003` IDs**. Its applicable authority and network rules are §5 “Authority of an agent” and “Network exposure,” together with `REQ-002`, `REQ-010`, and `REQ-011`. The authority paragraph does not license a sparse-request agent to install the plugin, run `mash credential install`, choose another server or release source, or delete without the person’s answer. The helper’s TLS listener is limited to one client port on `127.0.0.1`, with no monitoring, cluster, gateway, or leaf-node port. Other local users can attempt a connection but should be refused without credentials; TLS protects another connection’s payload. Plaintext exposes credentials and mail to a process able to observe that connection.

## Gaps and consistency checks

- **Missing `--account` test:** `REQ-003`, §3 says it is required on first install, but `AT-007`, §6 names the early exit-2/no-read/no-connect test only for missing `--server` or `--ca`. Add `--account` to that case, require a complete valid account public key, and expressly test a differing issuer when the person answers “yes.” The separate operator message supplies the value without a server or token lookup.
- **Later-install account behavior:** `REQ-003`, §3 does not say whether `--account` is required, optional but checked, or rejected when `config.json` already exists. State one rule and test it. A supplied value should not be silently ignored.
- **Destination cases:** `REQ-011(i)`, §3’s “writable by another user” covers group- and world-writable components, including a sticky shared parent such as `/tmp`; a root-owned, non-writable parent can be traversed. The test should name those cases, a symlinked parent, and a destination that does not yet exist. The required check occurs before copying.
- **Temporary-file shape:** `REQ-003`, §3 does not specify a temporary-name pattern or expressly place it in the destination directory on the same filesystem. Those words would make the rename commit point and mash’s ignore rule directly testable.
- **External references:** §3 “Target output” lists pending files and §8 “Readiness” says the status-views page and review round 2 are pending. Those outputs, the helper, and the skill cannot be checked from this workspace. Section 7’s accepted same-user risk remains cited, not reopened.

I count **two blocking and one nonblocking finding**. In my view, ratification should wait for the pre-confirmation network boundary and later-install temporary-file cleanup to be specified and tested. The revision 21 account-input and binary-destination findings are closed in contract text, while the inherited plaintext finding remains open.
---

Inputs (sha256):

```
6e707fec5fae62b3d7f77e01b2d558b61923bce100284293948d5d57cee4566b  ./contract-0.5.6-cli-ux-rev22-draft.md
b03a3c366531308b690d82be1746be681d83f9030826fef01a90b89393de3663  ./prior/contract-0.5.6-cli-ux-rev21-draft.md
86c45095071b2a6b90ceb92b25d15bb893b3a012fef23d02d84fdb60fdea4fb5  ./prior/security-read-rev21.md
9e8d9c48c2fde84244ca65c4ea32c331b2154a184535611d0ffe97a5a037f531  ./standards/cli/STANDARD.md
ed7b2448870e7cf899e4e8dc3b2735ff0ce8d8701844e5cb1e9b2b3152b8f73d  ./standards/contract/CHECKLIST.md
d96d969f78ff7e4eef8fc22a6b7b12db5a8ef3776b9a6a9b970e26ab45a3de85  ./standards/contract/STANDARD.md
9470bf08d732b908689ff2def78c271e852d77e6a1840ad2e5b4a9d7a02647a0  ./standards/skills/contract-critic/SKILL.md
ed1f58552eef3f041f4d37bb709557f6bcd7809f8eb00d5fb08aade797ac68cc  ./standards/standing-orders/mash.md
```
