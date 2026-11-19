# Security read (final), mash 0.5.6 work contract (cli-ux) revision 26 draft

Review lane: x-ops2 (operations). Date: 2026-11-19. Advisory to the contract, not a release gate (mash-lead #970); the planner opens Randy's ratification decision on this read. Security only; a reread after our rev 25 read (receipts/0.5.6-cli-ux/security-read-rev25.md, bogworks/mash PR #856), asked as the final clean read. Randy's decision 264 stands: the same-OS-user standing risk is accepted for mash and the plaintext override is his recorded choice; neither was raised as a blocker.
Reviewed: the mash lead's draft contract mash-0.5.6-cli-ux, revision 26 (draft), contract@0.3.4 (lane/lead eda2434), copy at s3://homerange-xfer/masterplanner/for-herds/mash-0.5.6/contract-0.5.6-cli-ux-rev26-draft.md (sha256 9c63c120685324c6e90618db53b35362e89dbf179e7f36eb3b88e9ef2aca19fa). Not handed over: the helper script and the skill (not landed): this is a read of the contract text only.
Reviewer: OpenAI Codex, run by kit/codex-review (x-herd#132), `codex exec --sandbox read-only`, codex-cli 0.170.0, model gpt-6-sol, Daybreak ON (codex -c daybreak=true); the attestation was stated to the reviewer in its prompt. Other family from the writer (Anthropic). The reviewer read text and ran nothing.
Handed over (sha256 list at the end): the rev 26 draft; standards from bogworks-dev-standards 68475627ac8c673d9510cdeccd675a44d8765c07 (contract/STANDARD.md 0.3.4, contract/CHECKLIST.md, standing-orders/mash.md, cli/STANDARD.md, skills/contract-critic/SKILL.md); under prior/, the rev 25 draft, our rev 25 read and the findings-by-origin sort. Nothing else. Where the content went: the pages above were sent to OpenAI through the operator's Codex subscription for this review.

---

# Security read, mash 0.5.6 cli-ux revision 26 (draft)

Reviewer attestation: this security runs on model gpt-6-sol, Daybreak ON (codex -c daybreak=true), codex-cli 0.170.0, read-only sandbox.

I read the revision 26 contract (contract-0.5.6-cli-ux-rev26-draft.md), the revision 25 security read (prior/security-read-rev25.md), the by-origin sort, and the supplied workspace standards. I read text and ran nothing. The helper, install skill, binaries, target outputs and tests were not available to verify.

## Threat model

The assets are signing seeds, endpoint keys and credentials, mail, and the server identity an endpoint trusts. An installing agent may receive only “set mash up”; that request does not authorize it to choose a new trust source, install an unverified plugin, handle a person’s credential install, or delete access. Other processes and users on the host, downloaded artifacts, files and receipts, and the loopback network each cross a separate trust boundary.

## Revision 25 disposition

| Revision 25 item | Revision 26 status |
|---|---|
| Finding 1 — `REQ-003` §3; `AT-007` §6, temporary-file recovery and concurrent installs | **Closed.** `REQ-003` now says the check and create “are not atomic and are not promised to be” and that the refusal “is a courtesy, not a lock.” It defines exit 1, `error: another mash credential install is running`, and the wait-or-remove fix. `AT-007` ages A’s temporary past 10 minutes before B installs; B inside 10 minutes refuses. A future mtime is stale. The requirement and test now agree on the supported recovery cases. |
| Finding 2 — `REQ-010` §3; “Network exposure” §5; `AT-007` §6, inherited plaintext override | **Open as Randy’s accepted, nonblocking choice.** The inherited value warns before connection and in status; §7 records the choice. It does not block ratification for security. |

The revision 25 gap about separate later `--server` and `--ca` mismatch tests is closed: `REQ-003` §3 requires each supplied value to match before connecting, and `AT-007` §6 names each test separately.

## Findings

**No new or carried actionable security findings. Blocking: 0. Nonblocking: 0.** The inherited plaintext override and same-OS-user risks are recorded accepted choices, not findings reopened by this read.

There are no `NFR-002` or `NFR-003` IDs in this contract. The applicable text is §5 “Authority of an agent” and “Network exposure.” The agent’s sparse-request authority is bounded to verified official downloads, the default local TLS helper, `adopt --from` on its folder, and endpoints for agents the person named. Another source, another server, and deletion require the person’s answer; the person runs plugin installation and `mash credential install`. I found no requirement letting an agent widen its own permissions. `REQ-011(i)` §3 requires attestation checks before the agent installs binaries; `REQ-002` §3 pins and checks nats-server and nsc before execution. `go install` requires the person’s answer, and `REQ-011(iii)` leaves the unverified plugin installation to the person.

The helper’s single client port is TLS-only on `127.0.0.1`, with no monitoring, cluster, gateway or leaf-node listener. Another local user can reach that port but must be refused without credentials; TLS protects another connection’s payload. Under `REQ-010`’s plaintext override, a process able to observe loopback traffic, including one with packet-capture rights, can read credentials and mail. §7 records Randy’s choice; `AT-007` tests the second-user reach and refusal and the inherited-override warning.

## Credential and trust inventory

`INV-001` and `INV-003` §5 prohibit seeds, JWTs and private keys in argv, output, JSON, logs, errors and receipts; secret files must be exclusive, regular, 0600 from creation, and written atomically. `AT-007` §6 requires channel scans. The public `--account` key may appear in argv; `--key` and `--token` take paths.

| §5 “Credential inventory” row | Location and reader | Agent authority and exposure |
|---|---|---|
| Operator signing seed, account seed, system credentials | mashctl config directory, 0600; mashctl and processes of the same OS user | The agent may run the bounded helper and local `adopt --from`. Disclosure can mint identities and read all mail; no values in argv, logs or receipts. |
| Admin credentials | mashctl config directory, 0600; mashctl and same-user processes | Local adopt is allowed. Disclosure enables endpoint and bucket administration; no values in argv, logs or receipts. |
| CA key | Helper directory, 0600; setup user and same-user processes | Default local helper use is allowed. Disclosure can mint server certificates; no value in argv, logs or receipts. |
| Server TLS key | Helper server directory, 0600; the user’s nats-server process and same-user processes | Starting the recorded local server is allowed. Disclosure permits impersonation; no value in argv, logs or receipts. |
| CA certificate | Server directory and mash config, 0644; everyone | Public trust material. A person compares its full fingerprint on first credential install; printing the fingerprint is intended. |
| Endpoint seed key | Endpoint machine, 0600; mash and same-user processes | An agent may create a local endpoint only for an agent the person named. Disclosure permits acting as that endpoint; no value in argv, logs or receipts. |
| Endpoint credentials | Mash config directory, 0600; mash and same-user processes | Same bounded local creation authority. Disclosure permits acting as that endpoint; temporary files follow `REQ-003`’s recovery rules and cannot appear in argv, logs or receipts. |
| Token (JWT) | Person-carried operator file, 0600; `mash credential install` reads it in place | The agent hands the command to the person and stops. The person deletes the source afterward. With its key, the token permits endpoint access; the source contents must not enter argv, logs or receipts. |

`REQ-003` §3 keeps first-install server, CA and full account-key comparison with the person before any credential-bearing connection or write. A later install must match the configured server and CA. It preserves the 0.5.5 refusal to change an existing different server, CA or credentials file. I found no dangling authority through a trust record, generations, reset, directory lookup, executing install script, agent-run plugin installation, or extra typed confirmation.

## Gaps and verification limits

I found no remaining security-relevant silence in the contract text requiring a new requirement for ratification. The helper, skill, binaries, target outputs and tests are absent from this workspace. Their actual file modes, download verification, redaction, listener configuration, credential writes and acceptance results remain unverified. §8 “Required evidence” and “Readiness” leave that implementation evidence and the output reviews pending; this read cannot count them as completed.

Security verdict: **CLEAN** as a contract-text read, with **ZERO blocking findings** and no actionable nonblocking findings. In my view, revision 26 does not block ratification for security. Randy’s record should retain the accepted same-OS-user standing risk and inherited plaintext override, plus the explicit limit that this review read text and ran nothing.
---

Inputs (sha256):

```
9c63c120685324c6e90618db53b35362e89dbf179e7f36eb3b88e9ef2aca19fa  ./contract-0.5.6-cli-ux-rev26-draft.md
877de328b2117c03c69f670d85cc4c08483eb35a58d207d27b570293c0400783  ./prior/contract-0.5.6-cli-ux-rev25-draft.md
35297b31b088b768ecad795656ebe9af2eff74641be7ad5834cc5127583297e5  ./prior/security-findings-by-origin.md
d2b3570b93688f102215ae1e5c1aa09cccc51949762092e469ca5af5890ad5c7  ./prior/security-read-rev25.md
9e8d9c48c2fde84244ca65c4ea32c331b2154a184535611d0ffe97a5a037f531  ./standards/cli/STANDARD.md
ed7b2448870e7cf899e4e8dc3b2735ff0ce8d8701844e5cb1e9b2b3152b8f73d  ./standards/contract/CHECKLIST.md
d96d969f78ff7e4eef8fc22a6b7b12db5a8ef3776b9a6a9b970e26ab45a3de85  ./standards/contract/STANDARD.md
9470bf08d732b908689ff2def78c271e852d77e6a1840ad2e5b4a9d7a02647a0  ./standards/skills/contract-critic/SKILL.md
ed1f58552eef3f041f4d37bb709557f6bcd7809f8eb00d5fb08aade797ac68cc  ./standards/standing-orders/mash.md
```
