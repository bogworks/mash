---
status: accepted
date: 2026-11-12
decision-makers: Randy Bias (mash maintainer)
---

# MASH_REQUIRE_TLS makes endpoint connections refuse plain text to a remote host

## Context and Problem Statement

Admin and system connections already refuse a plain `nats://` or `ws://` URL to a host that is not loopback. Endpoint
connections (`mash serve`, `mash hook` and the CLI commands `send`, `read`, `inbox`, `who`, `prompt` and `status`) do
not: a plain URL to a remote host connects, and `mash status` prints a warning, only for an entry that carries a scheme.
An entry with no scheme (`host:4222`, which the client reads as `nats://`) was skipped, so the warning was silent for it
and for a list holding it. A deployment that wants endpoints to refuse plain text has no setting for it. What does mash
expose?

## Decision Drivers

* An existing deployment that uses plain text to a remote host must keep working after an upgrade.
* A deployment that wants the refusal must be able to say so with one concrete setting.
* The refusal must come before any connection, and must not print credentials.
* Plain text to the local machine is not a network exposure and must stay possible.
* Which settings a kind of deployment uses is decided by the layer above mash, not by mash.

## Considered Options

* A concrete setting, `require_tls` (`MASH_REQUIRE_TLS`), default off, loopback always allowed (chosen).
* Make the refusal the default now.
* A named profile that mash reads and expands into settings.

## Decision Outcome

Chosen: a concrete boolean setting, `require_tls` in `config.json` or `MASH_REQUIRE_TLS` in the environment, read like
the other boolean settings (`1`/`true`, `0`/`false`, anything else an error, the environment beats the file). It is off
by default, and with it off nothing changes.

With it on, every endpoint connection goes through one check, before the first connection is made: a URL, or any entry
of a comma-separated list, that is plain `nats://` or `ws://` (an entry with no scheme counts as `nats://`) to a host
that is not `localhost` or a loopback IP literal is refused. The error names the host, the setting and the fix (use
`tls://` with the server's certificate, `tls_ca` for a private CA), and never carries the URL's userinfo. Plain text to
a loopback host is always allowed, with no `insecure_loopback`. A `tls://` or `wss://` entry makes the connection
require TLS, so a server that does not offer it fails, and servers a server advertises are not added to the pool. The
check shares the host and scheme parsing of `natsauth.RequireTLS`, whose behaviour and callers are unchanged.

`mash serve` reports the refusal as its start error on stderr and in the notice, and does not retry it: a configuration
refusal is permanent. `mash hook` connects to nothing, so it writes the refusal to stderr (the hook log) and exits 0. A
CLI command prints it and exits non-zero.

`mash status`, in the command and in the session's tool, prints `tls: WARN` for a plain nats or ws URL to a non-loopback
host in every spelling (with or without a scheme, anywhere in a list), naming the host and both fixes (`tls://`, and
`MASH_REQUIRE_TLS=1` to make it a refusal). With the setting on it prints `tls: OK (required)` when the URL is
acceptable and `tls: FAILED` with the refusal when it is not.

### Consequences

* Good, because a deployment can require TLS for endpoints with one setting, and the refusal comes before any connection.
* Good, because existing deployments are unchanged, and the silent warning for a scheme-less entry is fixed.
* Good, because mash holds no notion of a profile: what turns the setting on is decided above it.
* Bad, because the default still allows plain text to a remote host; a deployment must set it.
* Bad, because a list that mixes a loopback plain entry with a `tls://` entry makes the connection require TLS for every
  entry, so the plain loopback entry fails.

### Confirmation

Tests cover both states (off connects to a plain remote host and status warns naming it; on refuses with no connection
made, shown by a listener that records connects), loopback in both states for `127.0.0.1`, `localhost` and `[::1]`, a
list with one plain remote entry, `tls://` accepted and enforced against a plain server, the userinfo kept out of the
error, the hook's stderr line and exit 0, serve's start error without a retry, the CLI commands' refusal, the setting's
parsing (environment, file, invalid value), and the status line in the command and the tool, including the scheme-less
case that was silent.

## Pros and Cons of the Options

### A concrete setting, default off, loopback always allowed (chosen)

* Good, because it changes nothing for a deployment that does not set it.
* Good, because it is one setting with one meaning, which a layer above mash can set.
* Bad, because the safe behaviour is not the default.

### Make the refusal the default now

* Good, because every endpoint would refuse plain text to a remote host.
* Bad, because it breaks existing deployments on upgrade; a profile above mash decides when the default changes.

### A named profile that mash reads

* Good, because one name would set several things.
* Bad, because mash would own the meaning of the profile and its future settings; the layer above mash owns profiles,
  and mash exposes only concrete settings.

## More Information

* Related: [ADR 0093](0093-the-lease-bucket-does-not-allow-rollup-writes.md) (the decision record format);
  `docs/install.md` settings table; `docs/operations.md`.
