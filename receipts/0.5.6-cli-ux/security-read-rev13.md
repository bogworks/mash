# Security reread, mash 0.5.6 work contract (cli-ux) revision 13 draft

Review lane: x-ops2 (operations). Date: 2026-11-19. Advisory to the contract, not a release gate (mash-lead #891, #896). Security only; a reread after our rev 12 read (receipts/0.5.6-cli-ux/security-read-rev12.md, bogworks/mash PR #840). Revision 14 (one change on top: same-machine same-user `endpoint create --make-key --creds-out` writes the first generation without a typed fingerprint) was NOT read: held at mash-lead's word (#895) while the scope question goes to Randy.
Reviewed: the mash lead's draft contract mash-0.5.6-cli-ux, revision 13 (draft), contract@0.3.4 (7badb28), copy at s3://homerange-xfer/masterplanner/for-herds/mash-0.5.6/contract-0.5.6-cli-ux-rev13-draft.md (sha256 a773bcff385b74bab1f8033821575bc83c76937115687ae6365f29426bc7c2a5). Not handed over: the helper script and the skill DRAFT5 (not landed).
Reviewer: OpenAI Codex, run by kit/codex-review (x-herd#132), `codex exec --sandbox read-only`, codex-cli 0.170.0, model gpt-6-sol, Daybreak ON (codex -c daybreak=true); the attestation was stated to the reviewer in its prompt. Other family from the writer (Anthropic). The reviewer read text and ran nothing.
Handed over (sha256 list at the end): the rev 13 draft; standards from bogworks-dev-standards 68475627ac8c673d9510cdeccd675a44d8765c07 (contract/STANDARD.md 0.3.4, contract/CHECKLIST.md, standing-orders/mash.md, cli/STANDARD.md, skills/contract-critic/SKILL.md); under prior/, the rev 12 draft and our rev 12 read. Nothing else. Where the content went: the pages above were sent to OpenAI through the operator's Codex subscription for this review.

---

# Security read, mash 0.5.6 cli-ux revision 13 (draft)

Reviewer attestation: this security runs on model gpt-6-sol, Daybreak ON (codex -c daybreak=true), codex-cli 0.170.0, read-only sandbox.

I read the revision 13 contract (contract-0.5.6-cli-ux-rev13-draft.md), the revision 12 read (prior/security-read-rev12.md), the prior contract, and the standards present in this workspace. I ran nothing. The helper, skill DRAFT5, release artifacts, implementation, and acceptance evidence were unavailable. The prompt’s `dev-process/`, `brief/`, ADRs, and `standing-orders/homerange.md` are absent. This draft has no `NFR-002` or `NFR-003` ids: their subjects appear under **“Authority of an agent”** and **“Network exposure”** in §5.

## Threat model

The assets are signing seeds, TLS keys, endpoint credentials, mail, and the binding to the intended server. An installing agent acts with the user’s account after a sparse request. Other processes of that account can read and replace its files; other host users can attempt connections to loopback. The important boundaries are the person’s decision versus an agent’s action, verified download bytes versus executed bytes, owner-only files versus same-user processes, and a token’s claims versus a server identified through a separately supplied address and CA fingerprint.

## Revision 12 disposition

“Closed” here means the contract gives an observable rule, not that code has passed it.

| Prior finding | Revision 13 status | Closing words and acceptance check |
|---|---|---|
| `SR12-1` | **Closed in text** | `REQ-003`, §3 now says first-install `--ca` “is required,” omission “is refused (exit 2, naming `--ca`) before anything is read or written,” and “there is no other CA source.” `AT-007`, §6 tests omission and no writes. A reviewer can run that case, then check first-use fingerprint, certificate, and hostname refusal. “Before anything is read” needs the narrow clarification below because deciding whether this is a first install requires inspecting selection state. |
| `SR12-2` | **Closed for cooperative local mutations** | `REQ-013`, §3 requires one exclusive, kernel-released lock before validation through publication or reset cleanup; the second command waits, re-reads and revalidates, or refuses with exit 1. `AT-007`, §6 names install/install, install/reset, and killed-holder cases. A reviewer can interleave those commands and check the selected generation and each result. The lock-path and filesystem assumptions below remain unspecified. |

## Findings

**`SR13-1` — CARRIED; blocking; high certainty. `REQ-003`, §3; `INV-004`, §5; `AT-007`, §6.** `REQ-003` says existing credentials are “never overwritten” and install refuses, naming the file to move aside. `INV-004` instead includes “an install over existing credentials” among destructive operations that may proceed after typed confirmation. An implementer can treat that confirmation as permission to replace an installed credential, while another must refuse it. With an agent-driven terminal, the former can replace the user’s working identity after typing the answer itself (`S1`). **Smallest change:** state in `INV-004` that install over existing credentials always refuses and that confirmation does not authorize overwrite; test the existing-credential case with a correct typed answer and require unchanged bytes and selection. If replacement is intended, specify that transaction and its recovery instead.

**`SR13-2` — CARRIED; blocking; medium-high certainty. `REQ-002` and `REQ-011(i)`, §3; §8 “Release mechanics.”** `REQ-002` still permits mash and mashctl downloads to be verified by “digests written in the reviewed guide” instead of attestations. `REQ-011(i)` makes the attested script and its embedded digests the quick-start route, but does not define who controls the guide digest or bind the guide revision independently of the release being downloaded. An actor able to change the published guide and binary can supply matching malicious bytes and digest; the helper route could call that “verified” without the script’s attestation. **Smallest change:** make the attested script’s embedded digests the sole installation authority for release binaries in all user setup routes, or identify an independently pinned, reviewed guide revision and test its provenance before accepting its digest. A `SHA256SUMS` fetched beside the binary is already excluded. `go install` and the marketplace plugin are expressly unverified developer routes, not agent-authorized substitutes for the quick start.

**`SR13-3` — NEW; blocking; medium certainty. `REQ-013`, §3; `AT-007`, §6.** The new lock is named only as “a lock file beside the pointer” held by an advisory lock. The contract gives no mode, safe-parent, no-symlink, stable-inode, or local-filesystem rule for that file. If the parent permits replacement, one process can lock an open inode, another can replace the pathname and lock a new inode, and two installs can again validate and publish concurrently. An exotic or network filesystem may also fail to provide the assumed `flock`/`fcntl` semantics. **Smallest change:** require a regular, non-symlink lock file in an owner-only safe parent, opened without following links and held by file handle; define the supported locking primitive and filesystem semantics, refusing before mutation when they cannot be guaranteed. Add a replaced-lock-path test as well as the killed-holder test. A same-user adversary can still deny service by holding the lock indefinitely, replacing owner-controlled paths, or exhausting disk space; this lock does not establish a security boundary against that account. Its bounded wait and exit 1 are appropriate for a cooperative holder, but the bound should be stated so the test can assert it. The contract should also say whether readers take a shared lock or pin a generation and retry or refuse if reset removes it.

**`SR13-4` — NEW; nonblocking; high certainty. `REQ-003`, §3; `AT-007`, §6.** The rule that missing first-install `--ca` refuses “before anything is read” is broader than the operation can literally guarantee while allowing a later install without `--ca`: the command must inspect at least selection metadata to distinguish the two. This does not create a CA bypass, but it makes the claimed read prohibition ambiguous and hard to test. **Smallest change:** say “before reading any supplied key, token, or CA file, making a network connection, or writing anything; inspection of the selected trust state to determine first versus later install is allowed.” On a later install, identify the selected generation’s CA file as the source: `trust.json` itself records its fingerprint, not certificate bytes. A supplied `--ca` that differs must refuse with the old generation intact. After a reset, the person can recover by obtaining the required token, key, CA, address, and fingerprint and running first install again; the contract should make that dependency explicit in reset’s warning, since §5 tells the person to delete the original token file.

**`SR13-5` — CARRIED; nonblocking; high certainty. `REQ-010`, §3; §5 “Authority of an agent” and “Network exposure.”** The authority rule permits a sparse-request agent to run the default helper and pinned user-scope plugin, but reserves any other source, `adopt --from` directory, server address and fingerprint, broader plugin permission, broader exposure, startup unit, and deletion for the person. `INV-004` prevents a flag or environment variable from replacing typed confirmation, yet expressly concedes that an agent controlling a terminal can type it. The concrete failure is an agent choosing a hostile server or opening an interface and typing the displayed answer itself. The smallest stronger contract change would require a confirmation channel outside the agent’s control. The draft instead identifies this exact limitation as `S1` for written acceptance; it must not describe the prompt alone as proof of a person’s decision. The baseline plugin manifest is testable: one `mash serve` stdio MCP server, the three named hooks running its launcher, skills as text, and no permission allow-list entries. Any expansion requires the person’s separate answer.

## Credential and trust inventory

`INV-003`, §5 and `AT-007`, §6 bar **secret values** from argv, stdout, stderr, JSON, logs, errors, examples, and receipts. File paths in `--key`, `--token`, `--ca`, and person-chosen archive arguments can still appear in argv; failure-path receipts are limited to scratch-home paths. Modes 0600 and 0700 exclude other OS users, not the owner’s other processes or an agent running as the owner.

| §5 credential row | Location, readers, agent action, and exposure |
|---|---|
| Operator signing seed, account seed, system credentials | Mashctl config, 0600; mashctl and same-user processes can read them. They can mint identities and read all mail. A sparse-request agent has no stated authority to export or archive them; values must stay out of argv, logs, and receipts. |
| Admin credentials | Mashctl config, 0600; same-user processes can manage endpoints and buckets. No credential value in argv, logs, or receipts. |
| CA key | Helper directory, 0600, until the person archives or retires it; same-user processes can mint server certificates. Archive destination needs the person’s choice; contents and diagnostics must not disclose it. |
| Server TLS key | Helper server directory, 0600; nats-server and same-user processes can read it and impersonate the server. No value in output or receipts. |
| CA certificate | Server directory and mash config, 0644; publicly readable. It is not a secret, but coordinated replacement with trust state can redirect authority. Its path may be in `--ca`; the fingerprint must come from the operator’s separate channel on first install. |
| Endpoint seed key | Endpoint machine, 0600; mash and same-user processes can act as that endpoint. `--key` reveals a path in argv, not the seed value. |
| Endpoint credentials | Selected mash config generation, 0600; mash and same-user processes can act as that endpoint. No value in argv, logs, or receipts. |
| Token (JWT) | Person-carried operator file, 0600; install reads it in place and leaves deletion to the person. Same-user processes can read it while present. `--token` reveals its path in argv; its value must not enter logs or receipts. |
| `trust.json` | Selected mash config generation, 0600; mash and same-user processes can read or replace it. A coordinated replacement with config, CA, credentials, and pointer can redirect trust. |
| Backups and key archives | Person-named destinations, exclusive 0600 under safe parents; the person and same-user processes can read their contents. The agent must obtain the destination choice. Contents remain barred from argv, logs, and receipts. |

`REQ-013`, §3 checks a first token’s signature against the account key **served by the connected server**, not the token’s own claimed issuer alone. The separately supplied address and CA fingerprint authenticate that server and its hostname; they do not independently attest its account key. Later installs compare issuer, signature, server, and CA against the selected record and refuse mismatches without touching files. Exclusive 0600 creation and atomic publication prevent accidental partial records and access by other OS users. They do not stop a same-user process replacing a whole consistent generation; the contract deliberately tests that substitution **passes**.

`mash trust replace` is expressly deferred by `REQ-013`, §3. Reset followed by first install is the rotation path. Reset deselects and syncs before cleanup, refuses ordinary commands while old files remain, and is retryable after a kill. `AT-007`, §6 names before/after deselection and reinstall tests. `adopt --from` in `REQ-011(ii)`, §3 names parent ownership, mode and symlink checks; held handles; signature chain, certificate and hostname checks; destination-byte comparison; pathname-swap behavior; and kill recovery. These are testable for the stated kill model. A fully consistent directory prepared by another same-user process remains the stated custody risk.

The install script in `REQ-011(i)` is attested against repository, workflow, tag, and subject **before execution**; the checked bytes are executed from the held handle or inaccessible private copy. Its embedded digests bind binaries, helper, and skill downloads before installation; installed bytes are rehashed. `mash plugin install` uses the verified binary’s embedded bytes, no network fetch, user scope, and the pinned manifest. Those rules are testable, but no release bytes were available to verify. The script’s attestation and, through it, the release workflow are the stated digest trust root; a compromised workflow is outside what those checks establish.

The §5 network rule exposes one TLS NATS client port on `127.0.0.1` by default. Every local user can attempt a connection; NATS credentials are required, and TLS protects another connection’s payload. No monitoring, cluster, gateway, or leaf-node port opens by default. With `MASH_INSECURE_PLAINTEXT=1`, a process able to capture loopback packets can read credentials and mail. `REQ-010`, §3 warns before connecting, but an inherited variable requires no fresh choice; that is `S4`.

## Gaps and residual decisions

The contract should state the lock file’s creation and identity rules, supported filesystem semantics, reader behavior during reset, and a measurable wait bound (`SR13-3`). It should reconcile the guide-digest route with the attested-script route (`SR13-2`), resolve the credential-overwrite conflict (`SR13-1`), and tell the person before reset which inputs a fresh install will require. These are contract wording gaps, not implementation results.

Section 7 correctly treats Randy’s homerange acceptance as **precedent, not mash acceptance**. The first four items are one same-account authority class with distinct consequences; `S4` is a separate plaintext transport risk. Each wording is precise enough for its own written mash acceptance:

| §7 residual | Precision and needed disposition |
|---|---|
| `S1` | Precise: typed confirmation cannot prove a person typed it, including for reset and destructive commands. Written acceptance or a separate confirmation channel. |
| `S2` | Precise for helper path swaps and later installed-helper replacement. Written acceptance or stronger filesystem isolation. Extend it to name same-user lock-path replacement and denial of service if that remains permitted. |
| Whole-generation substitution | Precise: consistent config, CA, trust, credentials, and pointer replacement passes. Written acceptance or trust held outside that account. |
| Custody | Precise: same-user access to signing material, backups, and a prepared adoption directory. Written acceptance or separate signing custody. |
| `S4` | Precise: inherited plaintext override exposes credentials and mail without a fresh choice. Written acceptance or a fresh confirmation rule. |

## Summary

Revision 13 closes both revision 12 findings **in text** for their stated tests. I find **three blocking findings** (`SR13-1` through `SR13-3`) and **two nonblocking clarifications**. **Apart from the five residuals needing Randy’s separate written mash acceptance, the three blocking contract findings still block ratification in my view.** I verified wording only; I ran no code or tests and could not inspect the helper, skill DRAFT5, release artifacts, or runtime behavior.
---

Inputs (sha256):

```
a773bcff385b74bab1f8033821575bc83c76937115687ae6365f29426bc7c2a5  ./contract-0.5.6-cli-ux-rev13-draft.md
1714553c1651e18e2cb309b4fccd8b94ed3946c809147c89d49ffaadd86953e7  ./prior/contract-0.5.6-cli-ux-rev12-draft.md
e870a2aca32aaa63a12f8b191227df705239de75b3af058a6f7608479419f94b  ./prior/security-read-rev12.md
9e8d9c48c2fde84244ca65c4ea32c331b2154a184535611d0ffe97a5a037f531  ./standards/cli/STANDARD.md
ed7b2448870e7cf899e4e8dc3b2735ff0ce8d8701844e5cb1e9b2b3152b8f73d  ./standards/contract/CHECKLIST.md
d96d969f78ff7e4eef8fc22a6b7b12db5a8ef3776b9a6a9b970e26ab45a3de85  ./standards/contract/STANDARD.md
9470bf08d732b908689ff2def78c271e852d77e6a1840ad2e5b4a9d7a02647a0  ./standards/skills/contract-critic/SKILL.md
ed1f58552eef3f041f4d37bb709557f6bcd7809f8eb00d5fb08aade797ac68cc  ./standards/standing-orders/mash.md
```
