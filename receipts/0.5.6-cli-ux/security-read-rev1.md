# Security read, mash 0.5.6 work contract (cli-ux) revision 1 draft

Review lane: x-ops2 (operations). Date: 2026-11-19. Advisory to the contract, not a release gate (x-lead #777, mash-lead #2211). Security only; the first read of this draft, so every finding is new.
Reviewed: the mash lead's draft contract mash-0.5.6-cli-ux, revision 1 (draft), contract@0.3.4, copy at s3://homerange-xfer/masterplanner/for-herds/mash-0.5.6/contract-0.5.6-cli-ux-rev1-draft.md (sha256 6989d803bff9d15e27951ef8dc54236809fd2bf561112dda40a5358c9b86975b; the lane commit a4431c3 named in the request is not a commit of bogworks/mash, so the bucket copy is the pin), together with the policy lane's open PR bogworks/mash#822 at d1158e24f3bef50e75d03bd76ed20f6806f49d85 (scripts/nats-local-setup.sh, docs/skills/mash-nats-local/SKILL.md, docs/plans/2026-11-19-nats-skill.md).
Focus given to the reviewer: the NATS helper script that makes keys and certificates; `mash credential install` writing config and trust; `--creds-out`; the single MASH_ENDPOINT setting and its auto-pick when exactly one endpoint is installed.
Reviewer: OpenAI Codex, run by kit/codex-review (x-herd#132), `codex exec --sandbox read-only`, codex-cli 0.170.0, model gpt-6-sol, Daybreak ON (codex -c daybreak=true); the attestation was stated to the reviewer in its prompt. Other family from the writer (Anthropic). The reviewer read text and the whole helper script and ran nothing.
Handed over (the workspace; sha256 list at the end): the draft contract; standards from bogworks-dev-standards 68475627ac8c673d9510cdeccd675a44d8765c07 (contract/STANDARD.md 0.3.4, contract/CHECKLIST.md, standing-orders/mash.md, cli/STANDARD.md, skills/contract-critic/SKILL.md); and the three PR #822 files named above. Nothing else. Where the content went: the pages above were sent to OpenAI through the operator's Codex subscription for this review.

---


Reviewer attestation: this security runs on model gpt-6-sol, Daybreak ON (codex -c daybreak=true), codex-cli 0.170.0, read-only sandbox.

**Scope note:** This draft has no `NFR-002`, `NFR-003`, or section 5 credential table. Those references in the supplied template belong to a different contract. I reviewed the corresponding security boundaries in this draft and the cited PR. I read the text and the whole helper script; I did not run the script, install tools, or test NATS behavior.

## Threat model

The assets are operator and account seeds, user credentials, the private CA and server key, endpoint identity, stored mail, and the client’s server and CA configuration. An installing agent acts with the user’s filesystem and process authority, while other local users and processes may reach a loopback port or influence paths in shared directories. Remote clients cross a further network boundary when the server binds beyond loopback. Downloaded tools and a copied CA cross supply chain and trust boundaries before they are used to issue credentials or authenticate a server.

## Credential and trust inventory

The draft does not specify all of these artifacts’ handling. “Expected reader” describes the apparent intended boundary, not a verified guarantee.

| Artifact | Location in the supplied text | Expected reader; agent action | Exposure or missing rule |
|---|---|---|---|
| CA private key, server private key | Helper `DIR/ca.key`, `DIR/server.key`, mode 0600 after creation ([script lines 282–296](/home/randyb/scratch/m056/ws/pr822/scripts/nats-local-setup.sh:282)) | Directory owner; agent may create for the user | No owner or symlink check on `DIR`; no rule for retention, backup, or failure cleanup |
| CA and server certificates | `DIR/ca.pem`, `DIR/server.pem`; CA copied to clients | Server and chosen clients; agent may distribute CA | `REQ-003` does not limit CA installation to mash’s config or define how its origin is authenticated |
| `nsc` seeds and output | `DIR/nsc/`, `DIR/nsc/nsc.log` | Directory owner; agent invokes `nsc` | Tool output is logged and failure tails reach stderr ([lines 303–314](/home/randyb/scratch/m056/ws/pr822/scripts/nats-local-setup.sh:303)); secret content of `nsc` output was not verified |
| Operator and account seeds | `DIR/export/operator.nk`, `account.nk`, mode 0600 after write | Operator and `mashctl adopt`; agent passes file paths | Export is a second live copy; overwrite, link, and cleanup rules are absent |
| System and admin credentials | `DIR/export/sys.creds`, `mashadmin.creds`, mode 0600 after write | Operator and `mashctl adopt`; agent passes file paths | Full-privilege credentials remain in `export/` until moved or deleted; the skill leaves that later action to the user |
| Endpoint private key and credential | `mash key create`; `mashctl endpoint create --make-key --creds-out <path>` | Endpoint owner; agent may create on that user’s behalf | `REQ-003` specifies 0600 but no exclusive creation, ownership, overwrite, or symlink rule |
| Hand-off token | Passed to `mash credential install --token` | Recipient and installer | A literal token argument can enter shell history and process arguments |
| Client server/CA config | `credential install` writes `config.json` and credentials | Recipient’s mash process; agent may install | `--replace` can change the server or CA; destination, scope, and atomicity are unspecified |
| Output, receipts, logs | File names printed; `nsc.log` and `server.log` written | User, agent, anyone given captured output | `INV-001` excludes seeds, JWTs, and private keys from command output, but does not explicitly cover tokens, `.creds` contents, errors, logs, argv, or receipts |

## Findings

All findings are **NEW**.

1. **Blocking; high certainty — a hand-off token is exposed through the prescribed command.** Affected: `REQ-003`, §3; `INV-001`, §5; `AT-002`, §6. An agent can run the required `mash credential install --key --token …` with the token as an argument. The token may then be retained in shell history or visible in process arguments, even if the command prints no secret. The contract requires the flag and only forbids printing a seed, JWT, or private key. **Smallest change:** accept a token file or stdin as the documented hand-off path; forbid secret values in argv, command examples, logs, JSON, errors, and receipts. Test that the command reports paths only.

2. **Blocking; high certainty — client trust and server replacement are underspecified.** Affected: `REQ-003`, §3; `JRN-002`, §2; `AT-002`, §6. A confused agent could use `--replace` to overwrite a recipient’s existing server and CA with files supplied alongside a token, redirecting future connections to an attacker-controlled server. “Different one is refused … unless `--replace`” permits that sequence, and “writes … config.json (server, CA)” does not define the trust destination or ownership. **Smallest change:** say that installation is confined to the selected user’s mash config and never modifies a system trust store; show the old and new server address and CA fingerprint before replacement, require an explicit user choice for a trust change, and write the verified pair atomically without following links. Define precedence between `--server`, token contents, environment, and existing config.

3. **Blocking; high certainty — `--creds-out` has a mode rule but no safe creation rule.** Affected: `REQ-003`, §3; `JRN-003`, §2; `INV-001`, §5. If the chosen path already exists or is a symlink, an implementation can truncate another file or replace another endpoint’s credential and still satisfy “files 0600.” A post-write chmod also does not protect bytes already written through a link. **Smallest change:** require exclusive creation of a new regular file under an owned directory, no symlink traversal, mode 0600 from creation, atomic completion, refusal of an existing different file, and no credential bytes on stdout, stderr, or receipts.

4. **Blocking; high certainty — endpoint auto-pick lacks an identity and precedence boundary.** Affected: `REQ-009`, §3; `AT-001` and `AT-004`, §6. A process able to add credentials in a scanned location could cause a client with no setting to select a hostile endpoint; a second installation could silently change a previously working client’s identity or make its behavior implementation-dependent. “Exactly one endpoint’s credentials … on the machine” does not say whose credentials count, which directory is scanned, what happens with two, or whether the environment or config wins. **Smallest change:** scope discovery to the current user’s selected mash config, require owned regular credential files, fail with an explicit choice when zero or multiple valid endpoints exist, and state environment/config precedence and the selected endpoint in `status`.

5. **Blocking; high certainty — the helper trusts a directory path it does not secure against replacement.** Affected: `REQ-002`, §3; `INV-001`, §5. The helper checks that `DIR` is absent or empty, then uses `mkdir -p`, `chmod`, and ordinary redirections ([lines 256–268](/home/randyb/scratch/m056/ws/pr822/scripts/nats-local-setup.sh:256), [282–295](/home/randyb/scratch/m056/ws/pr822/scripts/nats-local-setup.sh:282), [383–389](/home/randyb/scratch/m056/ws/pr822/scripts/nats-local-setup.sh:383)). In a shared, writable parent, another process can swap a path component or final directory between these operations; the script can write secrets into a different directory or truncate a file the invoking user could write. `umask 077` protects newly created file modes but does not close this race. **Smallest change:** require an owned, non-symlink directory under trusted parent components, create it exclusively with mode 0700, refuse pre-existing directories for creation, and use exclusive, no-follow file creation for keys and exports. Verify ownership and file type in `--check`.

6. **Blocking; medium certainty — the helper’s secret-output claim is stronger than its logging controls.** Affected: `REQ-002`, §3; `INV-001`, §5; `AT-001`, §6. The helper redirects full `nsc` output to `nsc.log` and prints its last 15 lines on failure ([lines 307–314](/home/randyb/scratch/m056/ws/pr822/scripts/nats-local-setup.sh:307)); `nats-server -t` errors can likewise be tailed ([lines 368–370](/home/randyb/scratch/m056/ws/pr822/scripts/nats-local-setup.sh:368)). I could not verify from the supplied text whether any `nsc` version emits credential material in those paths. The contract nevertheless accepts the skill’s “never prints a secret” claim without covering dependency errors or the log. **Smallest change:** make the contract forbid secrets in persistent logs and failure output, require redacted diagnostics from credential-generating commands, and include a failure-path check using the supported tool versions.

7. **Blocking; high certainty — network exposure can be widened by an agent from a brief answer.** Affected: `REQ-002`, §3; `BND-003`, §4; `AT-001`, §6. The skill converts “other machines too” into `--listen 0.0.0.0` and instructs opening the firewall ([skill lines 87–92](/home/randyb/scratch/m056/ws/pr822/docs/skills/mash-nats-local/SKILL.md:87)); it also tells an agent to write a service unit for reboot persistence ([lines 103–107](/home/randyb/scratch/m056/ws/pr822/docs/skills/mash-nats-local/SKILL.md:103)). The contract says only that the helper asks questions, stays under `DIR`, and records starts. A vague answer could therefore lead an agent to expose the NATS port to every interface or create persistent startup outside `DIR`. Local-only mode is also reachable by **other users on the same host**: TLS protects transport and server authentication, but loopback is not a per-user access boundary. **Smallest change:** specify the default bind address and this local-user exposure; require the person to choose the exact external bind address and approve the firewall scope and service installation separately. State that an agent must not widen exposure or create persistence from the generic setup request alone.

8. **Blocking; high certainty — the documented installation path has an unpinned code-execution option.** Affected: `REQ-002`, §3; §8 “Release mechanics.” The skill offers `go install github.com/nats-io/nsc/v2@latest` ([skill lines 63–71](/home/randyb/scratch/m056/ws/pr822/docs/skills/mash-nats-local/SKILL.md:63)). A user who agrees to “install the missing tool” can have an agent fetch and execute whatever is latest at that moment. The draft also lists release binaries and checksums without requiring the installer to verify an authenticated provenance before execution. No literal `curl | sh` appears in the helper or skill; the open-ended “let them pick how” instruction does not rule it out. **Smallest change:** pin supported tool versions, require verification against a trusted release identity or digest before running downloaded code, and have the agent show the exact source, version, and install command for the person’s choice. Disallow pipe-to-shell in the guide.

9. **Blocking; high certainty — the printed adoption command is unsafe for valid directory names.** Affected: `REQ-001`, `REQ-002`, §3. `--dir` accepts spaces and shell metacharacters, while the helper prints unquoted `cd $EXP` and `export MASH_TLS_CA=$DIR/ca.pem` ([script lines 442–453](/home/randyb/scratch/m056/ws/pr822/scripts/nats-local-setup.sh:442)). A user or agent pasting output for a directory containing `;` or `$()` can run an unintended command. The contract requires commands to be pasteable but does not require shell-safe rendering. **Smallest change:** shell-quote every printed path and value, or reject names that cannot be safely rendered; test a path containing spaces and shell metacharacters.

10. **Blocking; high certainty — the plain-URL override can defeat the transport invariant without a bounded authority rule.** Affected: `JRN-007`, §2; `BND-003` and “Existing behavior to preserve,” §4. The draft promises refusal “with the override named” and preserves TLS “unless overridden.” An installing agent diagnosing a connection error can apply the advertised override and send credentials over an unencrypted remote connection. Nothing limits the override to a deliberate, per-invocation user decision or prevents it being saved into config. **Smallest change:** define the override’s scope, prohibit its persistence and automatic use by an agent, and require a clear statement that credentials and mail will cross the selected connection without TLS before a person elects it.

11. **Nonblocking; high certainty — generated authority material has no lifecycle rule.** Affected: `REQ-002`, §3; `INV-001`, §5. The helper creates a ten-year CA and server certificate in the same `DIR` as the running server ([script lines 270–299](/home/randyb/scratch/m056/ws/pr822/scripts/nats-local-setup.sh:270)). Its exported seeds and admin credentials remain there after `adopt`; the skill suggests moving or deleting `export/` later. A backup or long-lived directory copy can retain signing authority and full admin access after a user thinks setup is complete. **Smallest change:** state who retains the CA key and exported credentials, whether they are needed after adoption, and the required post-adoption cleanup or protected storage instruction.

## Gaps the contract should state

- `INV-001` should define “secret” to include `.creds` files, hand-off tokens, CA private keys, and all generated seeds, and cover argv, shell history, stderr, logs, JSON, and receipts.
- `REQ-003` should name the per-user config and credential destinations, ownership, file and parent-directory modes, atomic-write behavior, and collision behavior for every write.
- `REQ-009` should define endpoint discovery scope, validation, precedence, and the zero/two-endpoint result.
- `REQ-002` should state what an agent may install, bind, start persistently, or change in a firewall, with separate user decisions for each authority increase.
- `AT-001` should exercise an external bind choice and a same-host unprivileged client, while `AT-002` should exercise existing config, hostile links, and token handling. The current acceptance text does not reject those failures.

**Summary:** 10 blocking and 1 nonblocking findings. My view is that the draft should not go to ratification until the blocking boundaries are specified or expressly accepted by the ratifier under the contract standard. This is a review of the supplied text, not a claim that the current binaries exhibit every permitted failure.

---

## Input hashes (sha256, workspace-relative)

```
bf3e2c8bb110b4f1526c40f670f0d52d292ec06ba8d9e0856ebfba7b08365c71  ._contract-0.5.6-cli-ux-rev1-draft.md
bf3e2c8bb110b4f1526c40f670f0d52d292ec06ba8d9e0856ebfba7b08365c71  ._pr822
bf3e2c8bb110b4f1526c40f670f0d52d292ec06ba8d9e0856ebfba7b08365c71  ._standards
6989d803bff9d15e27951ef8dc54236809fd2bf561112dda40a5358c9b86975b  contract-0.5.6-cli-ux-rev1-draft.md
bf3e2c8bb110b4f1526c40f670f0d52d292ec06ba8d9e0856ebfba7b08365c71  pr822/._docs
bf3e2c8bb110b4f1526c40f670f0d52d292ec06ba8d9e0856ebfba7b08365c71  pr822/._scripts
bf3e2c8bb110b4f1526c40f670f0d52d292ec06ba8d9e0856ebfba7b08365c71  pr822/docs/._plans
bf3e2c8bb110b4f1526c40f670f0d52d292ec06ba8d9e0856ebfba7b08365c71  pr822/docs/._skills
bf3e2c8bb110b4f1526c40f670f0d52d292ec06ba8d9e0856ebfba7b08365c71  pr822/docs/plans/._2026-11-19-nats-skill.md
bde9f2e77c316c9a33e2a6d3a029328f5cf4c56d7a020ac9e38edb565a4ac6ec  pr822/docs/plans/2026-11-19-nats-skill.md
bf3e2c8bb110b4f1526c40f670f0d52d292ec06ba8d9e0856ebfba7b08365c71  pr822/docs/skills/._mash-nats-local
bf3e2c8bb110b4f1526c40f670f0d52d292ec06ba8d9e0856ebfba7b08365c71  pr822/docs/skills/mash-nats-local/._SKILL.md
6c4a405a566b784b12b7faf0fcd541d2d9cdde9050bd7128b67b4f1a15fc15df  pr822/docs/skills/mash-nats-local/SKILL.md
bf3e2c8bb110b4f1526c40f670f0d52d292ec06ba8d9e0856ebfba7b08365c71  pr822/scripts/._nats-local-setup.sh
3e7e6107e9f89db170ba34e720e0c45001661d6c8a4dd8eb35c923738a4d36a7  pr822/scripts/nats-local-setup.sh
bf3e2c8bb110b4f1526c40f670f0d52d292ec06ba8d9e0856ebfba7b08365c71  standards/._cli
bf3e2c8bb110b4f1526c40f670f0d52d292ec06ba8d9e0856ebfba7b08365c71  standards/._contract
bf3e2c8bb110b4f1526c40f670f0d52d292ec06ba8d9e0856ebfba7b08365c71  standards/._skills
bf3e2c8bb110b4f1526c40f670f0d52d292ec06ba8d9e0856ebfba7b08365c71  standards/._standing-orders
bf3e2c8bb110b4f1526c40f670f0d52d292ec06ba8d9e0856ebfba7b08365c71  standards/cli/._STANDARD.md
9e8d9c48c2fde84244ca65c4ea32c331b2154a184535611d0ffe97a5a037f531  standards/cli/STANDARD.md
bf3e2c8bb110b4f1526c40f670f0d52d292ec06ba8d9e0856ebfba7b08365c71  standards/contract/._CHECKLIST.md
bf3e2c8bb110b4f1526c40f670f0d52d292ec06ba8d9e0856ebfba7b08365c71  standards/contract/._STANDARD.md
ed7b2448870e7cf899e4e8dc3b2735ff0ce8d8701844e5cb1e9b2b3152b8f73d  standards/contract/CHECKLIST.md
d96d969f78ff7e4eef8fc22a6b7b12db5a8ef3776b9a6a9b970e26ab45a3de85  standards/contract/STANDARD.md
bf3e2c8bb110b4f1526c40f670f0d52d292ec06ba8d9e0856ebfba7b08365c71  standards/skills/._contract-critic
bf3e2c8bb110b4f1526c40f670f0d52d292ec06ba8d9e0856ebfba7b08365c71  standards/skills/contract-critic/._SKILL.md
9470bf08d732b908689ff2def78c271e852d77e6a1840ad2e5b4a9d7a02647a0  standards/skills/contract-critic/SKILL.md
bf3e2c8bb110b4f1526c40f670f0d52d292ec06ba8d9e0856ebfba7b08365c71  standards/standing-orders/._mash.md
ed1f58552eef3f041f4d37bb709557f6bcd7809f8eb00d5fb08aade797ac68cc  standards/standing-orders/mash.md
```
