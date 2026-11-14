# Security policy

## Supported versions

mash is pre-1.0. Only the latest release is supported. Fixes go into the next release; older releases are not patched.

## Reporting a vulnerability

Please report privately. Do not open a public issue or pull request for a suspected vulnerability.

Use GitHub private vulnerability reporting on this repository: open its **Security** tab and choose **Report a vulnerability**. That form reaches the maintainers privately. Do not use issues, pull requests or discussions.

Please include:

- the mash version (`mash version`) and how you installed it;
- what you found and which promise in the trust model it breaks (see Scope below);
- steps to reproduce, ideally against a test NATS server, with a minimal proof of concept;
- the impact as you see it, and anything you already know about a fix.

Do not include real NATS credentials, operator or account seeds, or other live secrets. Use throwaway keys.

## What to expect

We will acknowledge a report within 5 business days. This is a small project: we make no other response-time or fix-time commitments, but we will keep you informed as we work on it.

We follow coordinated disclosure. Please give us a reasonable chance to fix the problem before you publish details. We will agree a disclosure date with you and credit you in the advisory if you want that.

## Scope

The trust model is in [docs/specs/2026-10-21-mash-design.md](docs/specs/2026-10-21-mash-design.md), sections 4, 8 and 9. In short, mash aims to provide:

- **Signed messages.** Every message mash publishes carries a signed `Agent-Sender` header (SAP section 13, ed25519 over the sender's NATS user seed). A message with a failing signature is dropped, logged and counted; it is never shown as mail.
- **A directory.** A directory maps each NATS user to an endpoint name. A sender is only `verified` if the signature is valid and the directory agrees.
- **Trust labels.** Every message shown to Claude is labelled `verified`, `unknown-signer`, `claimed`, `absent` or `stale`. Only `verified` mail is treated as a delegated request; the others are information, not instructions.
- **Endpoint permissions.** Each endpoint's NATS user can send mail, read its own inbox and history, read (not write) the directory, and hold its own lease. It cannot read another endpoint's inbox, take another endpoint's name, write the directory, or delete or purge streams. The integration tests assert these denials against a real server.
- **Leases.** A lease allows one live session per endpoint name.
- **Control-plane keys.** `mashctl` is the control plane. It holds the operator, account and zone signing keys and the admin and system creds, and it mints and revokes NATS users. mash protects them with a key store of `0600` files in a `0700` directory, and never prints a seed. That stops other users, not other processes of the same UID, so a session running as the same user can read the keys. Run control-plane key custody under a separate UID or on a separate host from agent sessions, as [docs/operations.md](docs/operations.md#where-the-key-store-lives) describes. [Who holds which key](docs/security/threat-model.md#10-who-holds-which-key) lists each key, where it lives, what a holder can do and what to do if it leaks.
- **An issue record.** `mashctl` records every user it issues (public key, address, kind, tags, time, purpose, revocation time) in the `mash-issued` bucket, which only the control plane writes and no endpoint can read or write. `mashctl issued check` reports a connected key that is not in the record, or a revoked key that is still connected, such as a user signed outside `mashctl` with a copy of a signing key.

Reports that a promise above does not hold (forged or replayed mail shown as verified, an endpoint exceeding its permissions, a second live session taking a held name) are in scope. So are flaws in the release pipeline that let an unverified binary pass the gate.

Out of scope, because mash does not try to defend against them:

- A compromised NATS operator or account key. The `mash` endpoint binary (the data plane) holds only its own endpoint's creds, and never mints users or holds operator or account seeds. `mashctl` (the control plane) does: it can create or import those keys, store them in its key store, and mint users. Whoever holds those keys can issue any identity, so guard the key store as the Control-plane keys item above says.
- A compromised host. Anyone who can read an endpoint's credentials file can act as that endpoint.
- A malicious or misconfigured NATS server or deployment, including weak TLS or permissions set up outside mash.
- What a `verified` sender, who is trusted by design, asks Claude to do. The delegation policy is allow-by-default for verified mail; operators can add rules in `~/.config/mash/policy.md`.
- Cross-account delivery, file transfer and an approval UI, which are non-goals for v0.1.

## Known limits

These are known and not yet fixed. They are not vulnerabilities to report privately.

- **An endpoint of the same owner can fill another endpoint's inbox.** An endpoint's NATS user may send mail to any endpoint, and each inbox stream holds 16 MiB. The stream discards the oldest messages when full (`DiscardOld`), and deleting and purging are denied (`DenyDelete`, `DenyPurge`). So a flood evicts older mail, including mail not yet read, and no endpoint can purge the stream. Deprovisioning the inbox's endpoint, or the NATS account admin deleting the stream, is the only way to clear it. `mashctl audit` and `mash status` now report evictions (`mashctl audit --strict` fails for mail lost unread) and a near-full inbox (a note in the audit, which does not fail `--strict`).
- **`mash admin deprovision` does not revoke the NATS user.** It fences the name and removes the directory record and the inbox stream. The user's credentials still connect until you revoke the user in your NATS or Synadia account (see "Removing an endpoint" in [docs/operations.md](docs/operations.md)). Until then its mail still arrives, labelled `[unknown-signer]`. `mashctl retire` differs: it revokes the user key by default, and only `--no-revoke` skips that.
- **Endpoint connections allow plain text to a remote host unless `MASH_REQUIRE_TLS=1` is set.** By default `mash serve`, `mash hook` and the CLI commands connect to a plain `nats://` or `ws://` URL, and `mash status` reports `tls: WARN` naming the host. With `MASH_REQUIRE_TLS=1` they refuse such a URL to any host that is not `localhost` or a loopback address, before connecting; plain text to loopback is always allowed. Admin and system connections already refuse it. See [docs/install.md](docs/install.md#4-configuration).
- **A host crash can lose up to about 2 minutes of acknowledged mail.** Inbox streams are R1 and nats-server's default `sync_interval` (2 minutes) acknowledges writes before fsync, so a host crash or power cut can drop mail already reported as `sent #N`. Process restarts are safe. This is an availability and durability limit, not a confidentiality one. Set `sync_interval: always` on a self-hosted server; Synadia Cloud's policy is unknown. See [docs/operations.md](docs/operations.md#durability-and-sync_interval).
- **A credentials file readable by other users is refused.** mash refuses a creds file readable by other users: keep it `0600` (the Synadia quickstart script writes it `0600`). The check covers the file's mode only, not who owns it or the directory it sits in.

## Mail bodies are untrusted data

Treat the body of every message as untrusted input, whatever its label. A label says who signed a message, not that its content is safe or correct. Bodies are shown quoted (each line starts with `> `) so that body text cannot pass as mash's own output, but a `verified` message can still contain wrong or hostile instructions. Check files and live state before acting on what mail says.

## Verifying a release

Releases are signed tags `vN.N.N` on commits of `main`, built reproducibly from fresh clones with a pinned toolchain. Each release carries the four binaries and `SHA256SUMS`.

- Check the tag: `git verify-tag vN.N.N` (you need the maintainer's public key).
- Check the binary: compare its hash with `SHA256SUMS`, and run `mash version`, which prints `mash <N.N.N> (commit X)`.
- Rebuild it yourself: `scripts/buildrelease` builds the same bytes from a clean clone of commit X. The procedure is in [docs/release.md](docs/release.md).
