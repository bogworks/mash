---
status: accepted
date: 2026-11-18
decision-makers: Randy Bias (mash maintainer)
---

# Every mash and mashctl connection requires TLS, loopback included, and one override, insecure_plaintext, allows plain text

## Context and Problem Statement

Plain `nats://` has been allowed in three places. Admin and system connections allowed it to a loopback host
([ADR 0059](0059-mashctl-checks-who-issued-the-account-jwt-carries-only-its-own-signing-keys-and-requires-tls-for-admin-connections.md),
with `insecure_loopback` to allow other plain hosts for mashctl). Endpoint connections allowed it everywhere unless
`require_tls` was set, and then still allowed it to loopback
([ADR 0108](0108-require-tls-makes-endpoint-connections-refuse-plain-text-to-a-remote-host.md)). A plain connection lets anyone
on the network path read mail and obtain signatures, and loopback is not a safe place to assume: it is where a
forwarded port, a container network or a shared host puts another party in reach.

The maintainer's ruling of 2026-11-18: "mash should refuse to work in non-TLS mode without a specific override", with no
exemption for loopback. What does mash require, and how does a person who must run plain text say so?

## Decision Drivers

* Plain text lets anyone on the network read mail and obtain signatures.
* Loopback is not exempt: it has no reliable meaning of "local" once a port is forwarded or shared.
* 0.5.5 is a hard cut, so the driver of ADR 0108, that an existing deployment must keep working, no longer applies.
* The quick start must still work with no extra step for a solo developer.
* One setting, one meaning, the same for mash and mashctl, named so that the person who sets it knows what they chose.

## Considered Options

* Keep ADR 0108: `require_tls` off by default, loopback always allowed, `insecure_loopback` for mashctl.
* Make TLS the default but keep the loopback allowance.
* Require TLS on every connection to any host, loopback included, with one override, `insecure_plaintext` (chosen).

## Decision Outcome

Chosen: TLS on every connection and one override.

* **The rule.** Every connection mash and mashctl make (endpoint, admin and system) uses `tls://` or `wss://`, to any
  host, loopback included, and verifies the server's certificate against `tls_ca` when one is set. A plain URL
  (`nats://`, `ws://`, or none of these) is refused before any connection is made. A scheme that is none of nats, tls, ws
  or wss is refused as unsupported, with or without the override.
* **The override.** One setting, `insecure_plaintext` (`MASH_INSECURE_PLAINTEXT=1`), the same for mash and mashctl. It is
  a setting, not a command-line flag. With it set a plain connection is made to any host, and each connecting command and
  `mash status` say so on standard error and in the status line.
* **The refusal.** It names the scheme and the host (never the URL's userinfo), the override, and the fix: a `tls://`
  URL and, for a private certificate authority, `tls_ca`. `mash serve` exits 2 at session start; the hook writes the
  refusal to its log on standard error and exits 0; the CLI commands exit 2; `mash status` shows `tls: FAILED`.
* **The quick start.** `mashctl init --local` makes a local certificate authority and a server certificate for loopback,
  runs its server with TLS only, and writes the `tls://` URL and `tls_ca` into the configuration it writes, so it needs
  no override.
* **Removed.** `insecure_loopback`, `require_tls` and `MASH_REQUIRE_TLS` (all are no longer read), and the
  loopback-pinned dialer for admin and system connections.

This supersedes the `require_tls` default and the loopback allowance of ADR 0108, and the loopback allowance of ADR 0059. It also supersedes the line of ADR 0059 that has `mashctl init --local` write `insecure_loopback: true` into the configuration it makes: `init --local` now makes a local certificate authority and a server certificate and writes a `tls://` URL and `tls_ca`.
The rest of both records stands.

### Consequences

* Good: no plain-text connection is made by mash or mashctl unless a person set a named override, and the warning says
  what they chose.
* Good: one rule and one setting for every connection; no loopback special case in the code.
* Bad: a plain 0.5.x deployment sees a refusal on its first connection after the upgrade, naming the override. The release
  notes list this among the breaking changes, next to the address change.
* Bad: a person who runs their own server must give it TLS (and set `tls_ca` for a private authority) or set the
  override knowingly.
* Neutral: tests that are not about TLS set the override explicitly in their fixtures.

### Confirmation

* Unit tests of `natsauth.RequireTLS`: plain with and without a scheme, loopback and remote, a list with one plain
  entry, an unsupported scheme with the override, userinfo kept out of the error, the warning's host.
* `mash` tests with a listener that records connects: a plain URL with no override makes no connection from serve, the
  hook or the CLI commands (exit codes 2, 0 and 2); with the override the connection is made and the warning is on
  standard error and in `mash status`; `tls: FAILED`, `WARN` and `OK` lines.
* An endpoint test against a TLS server: `tls://` with `tls_ca` connects with the certificate verified, and a server whose
  certificate the configured authority did not sign is refused.
* Acceptance AT-001 (the quick start over TLS with no override), AT-003 (plain URL refused, with and without the
  override) and AT-005 (TLS cluster) of the 0.5.5 contract, REQ-014.

## Pros and Cons of the Options

### Keep ADR 0108

* Good: nothing changes for an existing deployment.
* Bad: the safe behaviour is not the default, and plain text to loopback is allowed to anyone who can reach it.

### Make TLS the default but keep the loopback allowance

* Good: the quick start needs no certificates.
* Bad: a second rule for one host class, which is the case that does not hold when a port is forwarded or shared.

### Require TLS on every connection, with one override

* Good: one rule, one override, no exemption.
* Bad: a breaking change for every plain deployment, and `init --local` has to make a certificate authority.

## More Information

* Contract: `contracts/0.5.5-endpoint-model.md`, REQ-014.
* Related: [ADR 0145](0145-an-endpoint-address-is-owner-group-instance-and-0-5-5-is-a-hard-cut-with-no-migration-in-mash.md)
  (the hard cut).
