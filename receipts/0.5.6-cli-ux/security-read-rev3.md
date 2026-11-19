# Security reread, mash 0.5.6 work contract (cli-ux) revision 3 draft

Review lane: x-ops2 (operations). Date: 2026-11-19. Advisory to the contract, not a release gate (mash-lead #790). Security only; a reread after our rev 2 read (bogworks/mash PR #825, receipts/0.5.6-cli-ux/security-read-rev2.md) with the helper and skill now in hand.
Reviewed: the mash lead's draft contract mash-0.5.6-cli-ux, revision 3 (draft), contract@0.3.4, copy at s3://homerange-xfer/masterplanner/for-herds/mash-0.5.6/contract-0.5.6-cli-ux-rev3-draft.md (sha256 a3751cc433df72bdcad92a44d5bab6ff3ba41b80591bd80f744232cb4c3bb753); the skill 2026-11-19-nats-local-skill-DRAFT3.md from the same bucket path (sha256 b0ae52b2ce63005d751a1b798f819bc7e0f24e5aa6c898a333336d0a777082ae); the helper scripts/nats-local-setup.sh from bogworks/mash PR #822 head 8415532c67bf661f79ee3399c837dd3bd2a50cb7 (sha256 16fc1dcc97c68e36e28f2c895b8f5c904d1841334ac16ee7172186b49b8eaa60).
Reviewer: OpenAI Codex, run by kit/codex-review (x-herd#132), `codex exec --sandbox read-only`, codex-cli 0.170.0, model gpt-6-sol, Daybreak ON (codex -c daybreak=true); the attestation was stated to the reviewer in its prompt. Other family from the writer (Anthropic). The reviewer read text and the whole helper and ran nothing.
Handed over (sha256 list at the end): the three artifacts above; standards from bogworks-dev-standards 68475627ac8c673d9510cdeccd675a44d8765c07 (contract/STANDARD.md 0.3.4, contract/CHECKLIST.md, standing-orders/mash.md, cli/STANDARD.md, skills/contract-critic/SKILL.md); under prior/, the rev 2 draft and our rev 1 and rev 2 reads. Nothing else. Where the content went: the pages above were sent to OpenAI through the operator's Codex subscription for this review.

---

# Security read, mash 0.5.6 cli-ux revision 3 (draft)

Reviewer attestation: this security runs on model gpt-6-sol, Daybreak ON (codex -c daybreak=true), codex-cli 0.170.0, read-only sandbox.

I read the supplied contract, the whole helper and skill, the prior revision 2 read, and the workspace standards. I ran no installer, binaries, or acceptance tests. The helper file’s SHA-256 matches the digest printed in the skill; that establishes which local text I reviewed, not how a future download behaves. This contract has **no `NFR-002`, `NFR-003`, or section 5 credential table**. The corresponding rules are `REQ-002`, `REQ-010`, and `INV-003` in this revision.

## Threat model

The assets are the CA and server keys, NATS signing seeds, user credentials, stored mail, and the server identity clients accept. An installing agent can choose commands and drive a shell for a user who may have said only “set it up.” Other users on the host can reach a loopback port and may affect shared path components. The main boundaries are downloaded executables and scripts, the setup directory, the hand-off token and independent CA fingerprint, the user’s mash configuration, and each decision to expose or persist the server.

## Revision 2 finding disposition

“Testable” means the contract names a check a reviewer could run. It does not mean that check passed.

| Prior finding | Revision 3 status | Closing words and testability |
|---|---|---|
| B1, agent-controlled consent | **Partly closed** | `INV-004`, §5 requires the person to type back the displayed value on a terminal and refuses a non-terminal run. `AT-007`, §6 tests no terminal and a *wrong* typed fingerprint. An agent can drive a PTY and type the displayed value; the stated test does not reject that sequence. The helper also accepts `--yes-expose`. |
| B2, unauthenticated CA | **Closed in contract text** | `REQ-003`, §3 requires `--ca-fingerprint <sha256>` from the operator separately from the token, including on first install. `AT-007`, §6 substitutes another server and CA while retaining the operator’s fingerprint and requires refusal. Implementation was unavailable. |
| B3, parent replacement | **Partly closed** | `REQ-002`, §3 requires every parent component to be owned by the user, free of symlinks and group/world write, plus a swapped-parent test in `AT-007`. The supplied helper checks the final parent’s ownership and mode and compares a resolved path, but does not enforce that rule on every component or hold them stable during creation. |
| B4, download identity | **Partly closed** | `REQ-002`, §3 requires literal reviewed SHA-256 values for pinned `nats-server` and `nsc`, and `AT-007` requires substituted downloads to fail. The skill prints digests for manual comparison; the helper has no pinned digests and executes whichever tools are on `PATH`. |
| B5, inherited plaintext override | **Partly closed** | `REQ-010`, §3 requires a warning on every connecting command and in status, including inherited variables; `AT-007` tests that warning. An inherited variable still authorizes plaintext without a per-run decision, and the test does not require a warning before connection. |
| B6, boundary acceptance | **Partly closed** | `AT-007`, §6 now names argv, output, file, selection, trust, override, parent, and artifact cases. §8 still requires run records for only `AT-001` through `AT-006`; §6’s verification mapping also omits `AT-007`. Several cases need sharper expected observations. |
| N1, custody | **Partly closed** | §5 assigns the CA key and exports to the person and says the guide will explain storage or deletion. The skill lists copies, but its instruction to back up `nsc/` and leave `ca.key` in the server directory leaves the post-adoption custody choice unresolved. A reviewer can inspect the guide, but no acceptance case checks the resulting copies. |

## Credential and trust inventory

| Material | Location and readers stated by the supplied text | Agent action and exposure |
|---|---|---|
| CA private key; server private key | `ca.key` and `server.key` in the helper’s 0700 directory, intended for its owner; the server reads its key. | The agent may run the helper, which creates them. `INV-003` bars secret values from argv, output, logs and receipts. The skill leaves `ca.key` beside the live server after adoption. |
| Operator and account seeds | In `nsc/` and `export/*.nk`; the skill says `mashctl adopt` copies them into `MASH_CONFIG_DIR/keys`. The owning user and processes they run can read them. | The agent may create, adopt and, following a person’s choice, remove exports. Copies remain in `nsc/`; neither argv nor diagnostic output may contain their values under `INV-003`. |
| System and admin credentials | `export/*.creds`; skill says copies go to `MASH_CONFIG_DIR/server/`, at 0600 under `REQ-002` and `INV-003`. | The agent may pass **paths** to `mashctl adopt`, not credential contents in argv. The skill’s sample output still says the admin file was *not* copied (§4, lines 467–479), contradicting `REQ-002` and the skill’s custody list. |
| Endpoint key and credentials | Current user’s mash configuration under `REQ-003` and `INV-003`; intended for that user and their mash processes. | An agent may create or install for that user. Secret contents are barred from argv, logs, JSON, errors and receipts; `AT-007` calls for a scan of these channels. |
| Hand-off token | A file or stdin under `INV-003`; received by the installing user. | The agent may supply its **path** to `credential install`. Its server and CA claims must be checked against the separately supplied CA fingerprint under `REQ-003`. |
| CA certificate and server URL | Helper directory and hand-off; installed in current-user config, with no system trust-store write under `REQ-003`. | They are public trust inputs. `INV-004` governs a later change, but the first install depends on the operator’s independently supplied fingerprint. |
| Logs and receipts | Temporary helper command log, optional `server.log`, CLI/MCP output and test receipts. | `REQ-002` and `INV-003` prohibit secret values. The helper filters some failure diagnostics, but writes server stdout/stderr directly to `server.log` and prints some successful tool output without filtering. |

Default `127.0.0.1` binding in `REQ-002`, §3 and the skill’s “Who can connect” section means **every local user can reach the NATS TCP listener**. The intended server configuration requires TLS and NATS credentials before account access; reachability still exposes the handshake and any unauthenticated protocol behavior to local processes. `REQ-010` concerns a different boundary: if mash connects to a plaintext server with `MASH_INSECURE_PLAINTEXT=1`, credentials and mail travel unencrypted on that connection. A local listener does not make plaintext private to one user.

## Findings

**F1 — CARRIED from B1; blocking; high certainty. `INV-004`, §5; `REQ-002`, §3; `AT-007`, §6; helper lines 19, 43–47, 149–150, 193–200.** An agent can start a PTY, read the prompt’s displayed address or fingerprint, and type that same value. For exposure it can directly pass `--listen 0.0.0.0 --yes-expose`; the helper accepts the flag without a person’s terminal confirmation. `INV-004` says a terminal prompt means “an agent driving a shell cannot supply it,” which does not follow from the mechanism. The current test only rejects a wrong answer. **Smallest change:** state that the agent must stop and hand the exact proposed change to the person, who runs or answers it outside the agent-controlled session; remove `--yes-expose` as sufficient authority. Test a PTY with the correct displayed answer supplied by the agent, and require refusal or a documented human hand-off.

**F2 — CARRIED from B3; blocking; high certainty. `REQ-002`, §3; `AT-007`, §6; helper lines 348–374.** A setup path can have a root-owned or writable sticky ancestor because the helper checks ownership and mode only for the immediate parent and expressly permits root ownership and sticky write. A process able to replace a component between resolution and `mkdir` can redirect creation. These paths satisfy the script while violating `REQ-002`’s “owned by the user,” “not group- or world-writable,” and “every component checked.” **Smallest change:** make the helper enforce the stated rule on each component and perform creation relative to verified directory handles, refusing replacement; specify in `AT-007` that the swapped path stays untouched and no files appear at the replacement destination.

**F3 — CARRIED from B4; blocking; high certainty. `REQ-002`, §3; `AT-007`, §6; helper lines 332–343; skill lines 181–194, 206–256.** A prior executable named `nsc` or `nats-server` on `PATH` runs during preflight and setup. The helper contains no literal expected digest and does no verification before execution. In the skill, `sha256sum` merely prints a digest; a following agent step can extract and run the file without an enforced comparison. The Arm instruction takes digests from the same release checksum source, contrary to the contract’s independent reviewed value. **Smallest change:** use checked, pinned executables at known paths; place the literal platform digests in the reviewed installer/skill, compare them programmatically, and stop before extraction or execution on mismatch. Test a substituted artifact and a malicious earlier `PATH` entry.

**F4 — NEW; blocking; medium certainty. `REQ-002`, §3; `INV-003`, §5; helper lines 166–191 and 460–487.** The helper allows quotation marks and backslashes in `--dir`, then interpolates that path into quoted NATS configuration fields without escaping it. An agent or another process supplying such a path can make `server.conf` parse differently or fail after secrets have been created. A syntactically valid but altered configuration could point the server at different files or settings. The contract requires shell quoting for *printed* values, but says nothing about escaping values embedded in NATS configuration. **Smallest change:** reject path characters that cannot safely be represented in NATS config or use a proven config-string encoder; add a metacharacter path case that inspects the parsed configuration, not just the printed command.

**F5 — NEW; blocking; medium certainty. `REQ-002`, §3; `INV-003`, §5; helper lines 80–99, 306–313, 342–343, 417–430, 517–529.** A tool can emit a secret in an unexpected diagnostic form or in server output. The helper’s `safe()` removes lines matching a limited set of patterns, while successful `nats-server -t` output and version output bypass it; detached server stdout/stderr goes straight to persistent `server.log`. The absolute “never write a secret to a log or to failure output” is therefore not established by this implementation. **Smallest change:** keep raw subprocess diagnostics in private temporary files and print fixed, non-secret failure messages or an allowlist of known safe fields; define how the persistent server log is checked. Inject a canary secret into each subprocess output path and require absence from stdout, stderr, `server.log`, and receipts.

**F6 — CARRIED from B5; blocking; medium certainty. `REQ-010`, §3; `AT-007`, §6.** A parent process can set `MASH_INSECURE_PLAINTEXT=1`. A later agent command inherits it, connects, and exposes credentials and mail while satisfying the rule that the variable is “in the environment of that run.” A warning may arrive after the connection unless timing is specified. **Smallest change:** require the warning before any network I/O and require an explicit choice tied to the plaintext target for that invocation; test an inherited variable with a capture server and assert no credentials leave before the warning and decision. If inherited opt-in is intentionally accepted, say so as an explicit residual risk.

**F7 — CARRIED from B6; blocking; high certainty. `AT-007`, §6; §8 “Required evidence” and “Accept if.”** A release can meet §8’s named evidence requirement with records for `AT-001` through `AT-006` and no security-boundary run, despite `AT-007` being defined. The §6 verification mapping likewise names only the earlier tests. **Smallest change:** include `AT-007` in required evidence, verification mapping and accept condition; require observed destinations, modes, unchanged hostile files, pre-execution artifact refusal, and canary scans in its record.

**F8 — NEW; blocking; high certainty. `REQ-002`, §3; §8 “Release mechanics”; skill lines 259–290.** The skill downloads mash binaries and `SHA256SUMS` from the same release, then tells the agent to compare with hashes on that same release page. A substituted release page can supply both binaries and matching hashes, after which the agent installs them. `REQ-002`’s independently reviewed literal digest rule names `nats-server` and `nsc`, but does not cover the mash binaries the same skill installs. **Smallest change:** give mash and mashctl an authenticated release provenance check or reviewed literal digests, and require the skill to verify it before installation; include a substituted binary plus checksum case.

**F9 — CARRIED from N1; nonblocking; high certainty. §5 “Custody of setup secrets”; `REQ-002`, §3; skill lines 385–428 and 495–501.** After adoption, an operator can delete `export/` yet retain the same signing seeds under `nsc/` and copies under mashctl config. The skill says to back up `nsc/` and leave the CA key in the live server directory; the contract says to explain offline storage or deletion but does not settle which disposition the guide requires. A backup or live server compromise can therefore expose signing authority long after setup. **Smallest change:** list every remaining copy and require an explicit post-adoption custody choice for `nsc/`, `ca.key`, and mashctl’s copies, with the guide and an acceptance check matching that choice.

**F10 — NEW; nonblocking; high certainty. `REQ-002`, §3; skill §4, lines 467–479.** The skill says 0.5.6 copies admin credentials, but its sample output says the file is “not copied.” A person can retain or delete `export/` based on the wrong statement. **Smallest change:** replace the stale sample with the required 0.5.6 output and have output conformance check it.

## Gaps

- `AT-007` does not test the **correct** answer supplied through an agent-driven PTY, or the helper’s `--yes-expose` route. Its current wrong-answer test cannot establish `INV-004`’s person-only boundary.
- `REQ-003` authenticates the CA fingerprint independently of the token, but does not require the operator to bind that fingerprint to the **intended server address** in the separately delivered information. A test should check a substituted URL under an otherwise matching CA.
- `REQ-002` does not state how the helper rejects a pre-existing unpinned executable on `PATH` or verifies supported tool versions before any tool invocation.
- `INV-003` calls for atomic completion of every secret file; neither contract nor `AT-007` specifies an interruption test for a partially written helper export, `adopt` copy, or credential/config pair.
- The skill permits `sudo dpkg` after a separate yes, but gives no pinned package or authenticated digest for that privileged install path. The contract should either constrain that path or exclude it.
- The contract’s §8 release routes include `go install ...@v0.5.6` and release binaries with attestations, but do not say what an installing agent must verify before executing either route.

## Summary

I find **8 blocking and 2 nonblocking findings**. B2 is closed in contract text; B1, B3, B4, B5, B6 and N1 remain partly closed, with additional helper and skill conflicts. My view is that these blocking issues prevent ratification as written. Under contract/STANDARD.md, **each blocking finding left unfixed needs the ratifier’s written acceptance of that specific risk before READY**; that applies to F1–F8. I verified text and the local helper digest only, and ran no behavioral checks.
---

Inputs (sha256):

```
a3751cc433df72bdcad92a44d5bab6ff3ba41b80591bd80f744232cb4c3bb753  ./contract-0.5.6-cli-ux-rev3-draft.md
b0ae52b2ce63005d751a1b798f819bc7e0f24e5aa6c898a333336d0a777082ae  ./helper/2026-11-19-nats-local-skill-DRAFT3.md
16fc1dcc97c68e36e28f2c895b8f5c904d1841334ac16ee7172186b49b8eaa60  ./helper/nats-local-setup.sh
2986d2b9338dfaabe7e5f281e36714557f319deb7873867871c2da75371b1392  ./prior/contract-0.5.6-cli-ux-rev2-draft.md
e8b6b142726befd7b6d1d2ed9181d51573b1bb54bf0bd3634dc976805bb4c74a  ./prior/security-read-rev1.md
36a52bafb876f418fce910ed03e277a6fae21363b745c4a967b0b23ad9a6bfab  ./prior/security-read-rev2.md
9e8d9c48c2fde84244ca65c4ea32c331b2154a184535611d0ffe97a5a037f531  ./standards/cli/STANDARD.md
ed7b2448870e7cf899e4e8dc3b2735ff0ce8d8701844e5cb1e9b2b3152b8f73d  ./standards/contract/CHECKLIST.md
d96d969f78ff7e4eef8fc22a6b7b12db5a8ef3776b9a6a9b970e26ab45a3de85  ./standards/contract/STANDARD.md
9470bf08d732b908689ff2def78c271e852d77e6a1840ad2e5b4a9d7a02647a0  ./standards/skills/contract-critic/SKILL.md
ed1f58552eef3f041f4d37bb709557f6bcd7809f8eb00d5fb08aade797ac68cc  ./standards/standing-orders/mash.md
```
