# Security policy

## Supported versions

mash is pre-1.0. Only the newest version is supported, currently v0.5.10. Fixes go into the next release; older releases are not patched.

## Reporting a vulnerability

Please report privately. Do not open a public issue or pull request for a suspected vulnerability.

Use GitHub private vulnerability reporting on this repository: open its **Security** tab and choose **Report a vulnerability**. That form reaches the maintainers privately. Do not use issues, pull requests or discussions.

Please include:

- the mash version (`mash version`) and how you installed it;
- what you found and which promise under Scope below it breaks;
- steps to reproduce, ideally against a test NATS server, with a minimal proof of concept;
- the impact as you see it, and anything you already know about a fix.

Do not include real NATS credentials, operator or account seeds, or other live secrets. Use throwaway keys.

## What to expect

We will acknowledge a report within 5 business days. This is a small project: we make no other response-time or fix-time commitments, but we will keep you informed as we work on it.

We follow coordinated disclosure. Please give us a reasonable chance to fix the problem before you publish details. We will agree a disclosure date with you and credit you in the advisory if you want that.

## Scope

In short, mash aims to provide:

- **Signed messages.** Every message mash publishes carries a signed `Agent-Sender` header (ed25519, made with the sender's NATS user key). A message with a failing signature is dropped and counted; it is never shown as mail.
- **A directory.** A directory maps each NATS user to an endpoint name. A sender is only `verified` if the signature is valid and the directory agrees.
- **Trust labels.** Every message is labelled `verified`, `unknown-signer`, `claimed`, `absent` or `stale`. Only `verified` mail is treated as a delegated request; the others are information, not instructions.
- **Only verified mail is delivered by default.** Mail with any other label is dropped and counted, and `mash doctor` shows the count. Setting `accept_unverified` delivers it with its label instead. See [Configuration](docs/install.md#4-configuration).
- **Endpoint permissions.** Each endpoint's NATS user can send mail to endpoints of its own owner, read its own inbox, read (not write) the directory, and hold its own lease. It cannot read another endpoint's inbox, take another endpoint's name, write the directory, or delete or purge streams. The tests assert these denials against a real server.
- **Leases.** A lease allows one live session per endpoint name.
- **Control-plane keys.** `mashctl` is the control plane. It holds the operator, account and system signing keys and the admin and system creds, and it mints and revokes NATS users. mash keeps them in a key store of `0600` files in a `0700` directory, and does not put seed text in errors or logs. That stops other users, not other processes of the same UID, so a session running as the same user can read the keys. Keep the key store under a separate UID or on a separate host from agent sessions, as [docs/operations.md](docs/operations.md#where-the-key-store-lives) describes.
- **An issue record.** `mashctl` records every user it issues in the `mash-issued` bucket, which only the control plane writes and no endpoint can read or write. `mashctl doctor` compares it with the keys connected to your servers and reports a connected key that is not in the record, or a revoked key that is still connected. See [docs/doctor.md](docs/doctor.md).

Reports that a promise above does not hold (forged or replayed mail shown as verified, an endpoint exceeding its permissions, a second live session taking a held name) are in scope. So are flaws in the release pipeline that let an unverified binary pass the gate.

Out of scope, because mash does not try to defend against them:

- A compromised NATS operator or account key. The `mash` endpoint binary holds only its own endpoint's creds and never holds operator or account seeds. `mashctl` does, and whoever holds those keys can issue any identity. Guard the key store as the Control-plane keys item above says.
- A compromised host. Anyone who can read an endpoint's credentials file can act as that endpoint.
- A malicious or misconfigured NATS server or deployment, including weak TLS or permissions set up outside mash.
- What a `verified` sender, who is trusted by design, asks Claude to do. The delegation policy is allow-by-default for verified mail; operators can add rules in `~/.config/mash/policy.md`.

## Known limits

These are known and not yet fixed. They are not vulnerabilities to report privately. The full list for this release is the GitHub issues labelled [known-0.5.10](https://github.com/bogworks/mash/issues?q=label%3Aknown-0.5.10). The main ones:

- **An endpoint of the same owner can fill another endpoint's inbox.** An endpoint's NATS user may send mail to any endpoint of its owner, and each inbox stream holds 16 MiB. The stream discards the oldest messages when full, and deleting and purging are denied. So a flood evicts older mail, including mail not yet read, and no endpoint can purge the stream. `mashctl doctor` reports an inbox that lost mail unread as a problem and a nearly full inbox (90% or more) as a warning. `mashctl endpoint suspend` stops new mail to an inbox. `mashctl endpoint delete` removes the inbox and the name for good.
- **One administrator at a time is mash's stated limit.** `mashctl` does not coordinate two administrators (two operators, or two machines holding the keys) who write the account or reissue one endpoint at the same moment, and there is no lock. Known under it: [#199](https://github.com/bogworks/mash/issues/199), [#627](https://github.com/bogworks/mash/issues/627), [#767](https://github.com/bogworks/mash/issues/767) and [#872](https://github.com/bogworks/mash/issues/872). See [docs/operations.md](docs/operations.md#one-administrator-at-a-time).
- **Any endpoint can publish to `_INBOX.>` and `$SRV.>`, outside the mail policy.** Every endpoint's NATS user may publish on `_INBOX.>` and `$SRV.>` so it can send replies. Those subjects carry replies only and the mail policy does not govern them, so an endpoint allowed no mail can still publish bytes to a reply prefix that another endpoint subscribes to. Forging a reply to a live request means guessing a 128-bit token. Kept on purpose: [ADR 0183](docs/decisions/0183-the-reply-grant-on-inbox-and-srv-stays-and-is-stated-as-outside-the-mail-policy.md).
- **A plain-text connection is possible only with an explicit override.** Every mash and mashctl connection requires TLS (`tls://` or `wss://`), loopback included, and refuses a plain URL before connecting. The environment variable `MASH_DANGEROUSLY_SKIP_NATS_ENCRYPTION=1` (the only way: no flag, no `config.json` key) allows plain text. Then every command prints `warning: no encryption to the server (MASH_DANGEROUSLY_SKIP_NATS_ENCRYPTION)` on standard error, `mash doctor` shows the `server` row as `warn`, and anyone on the network can read mail and obtain signatures. The setting covers mash's own connections only. TLS between your NATS servers is set in nats-server, as [docs/tls-and-clusters.md](docs/tls-and-clusters.md) describes. See [Turning encryption off](docs/install.md#turning-encryption-off).
- **A host crash can lose up to about 2 minutes of acknowledged mail.** Inbox streams are R1 and nats-server's default `sync_interval` (2 minutes) acknowledges writes before fsync, so a host crash or power cut can drop mail already reported as `sent #N`. Process restarts are safe. This is an availability and durability limit, not a confidentiality one. Set `sync_interval: always` on a self-hosted server. See [docs/operations.md](docs/operations.md#durability-and-sync_interval).
- **The 0.5.5 migration command in the docs downloads a script and runs it without checking it first.** Use it only if you trust the network you are on. Fixed after 0.5.9 ([#1458](https://github.com/bogworks/mash/issues/1458)).
- **Deleting an endpoint can, in a rare race, remove a creds file that was replaced a moment earlier.** The endpoint's key is already cancelled by then, so the replacement belongs to a deleted endpoint and the exposure is small. Fixed after 0.5.9 ([#1471](https://github.com/bogworks/mash/issues/1471)).
- **A pinned plugin runs its own downloaded copy of mash, and falls back to the mash on your PATH if that download fails; `mash doctor` cannot tell which one runs.** By default the launcher does not verify what it runs: it uses an existing managed copy without rehashing it and takes the PATH fallback silently. Set `MASH_PLUGIN_REQUIRE_VERIFIED=1` to make the plugin refuse the fallback and any binary it cannot verify ([#1290](https://github.com/bogworks/mash/issues/1290)). If a host needs a different mash, set `MASH_BIN` or use the unpinned plugin ([#1497](https://github.com/bogworks/mash/issues/1497), [#1535](https://github.com/bogworks/mash/issues/1535)).
- **A credentials file readable by other users is refused.** mash refuses a creds file readable by other users: keep it `0600`. The check covers the file's mode only, not who owns it or the directory it sits in.

## Mail bodies are untrusted data

Treat the body of every message as untrusted input, whatever its label. A label says who signed a message, not that its content is safe or correct. Bodies are shown quoted (each line starts with `> `) so that body text cannot pass as mash's own output, but a `verified` message can still contain wrong or hostile instructions. Check files and live state before acting on what mail says.

## Verifying a release

Release tags are annotated but not signed. A release is checked by its files instead:

- `SHA256SUMS` lists every binary, script and SBOM in the release.
- A GitHub build provenance attestation, made by this repository's release workflow at the release tag, covers the binaries, the three scripts and `SHA256SUMS`.
- Each binary has a CycloneDX SBOM.
- You can rebuild a binary from the tagged commit and compare it byte for byte.

The steps are in [Verify your download](docs/security/verify-your-download.md). `install.sh` does the checksum check itself, and verifies the attestation of `SHA256SUMS` when the GitHub CLI (`gh`) is installed. The attestation check needs gh 2.102.0 or newer ([what you need](docs/security/verify-your-download.md#what-you-need)).
