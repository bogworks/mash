---
status: accepted
date: 2026-11-02
decision-makers: Randy Bias (mash maintainer)
---

# mashctl checks who issued the account JWT, carries only its own signing keys, and requires TLS for admin and system connections

## Context and Problem Statement

`mashctl policy apply` reads the MASH account's JWT from the resolver, changes the zone signing keys, signs the result
with the operator key and pushes it ([ADR 0052](0052-mashctl-mints-nats-users-and-holds-the-account-signing-key.md)).
`mashctl adopt` reads the same JWT. Three properties were not required of that read: that the JWT is signed by a key the
operator accepts, that the signing keys apply re-signs are ones mashctl created, and that the connections carrying admin
and system credentials are encrypted.

## Decision Drivers

* Whatever apply preserves from the fetched JWT is re-signed with the operator key, so it must come from a trusted issuer.
* Apply should add or remove only mash's own zone keys.
* Admin and system credentials should not travel in plain text to a host that is not the local machine.
* A local development server made by `init --local` has no TLS and must keep working, by an explicit opt-in.

## Considered Options

* Keep the checks as they were (adopt warned on a foreign issuer; apply carried every signing key over).
* Require a valid signature, the right subject and an accepted issuer; refuse signing keys mashctl did not create; require TLS unless a loopback opt-in is set.

## Decision Outcome

Chosen option: the second.

Issuer. `apply.VerifyAccountJWT` requires that the account JWT decodes with a valid signature by its stated issuer, that its subject is the account identity key mashctl holds, and that its issuer is an accepted one. The accepted issuers are the operator key in the key store and, when `<key dir>/operator.jwt` exists, the identity and signing keys of that operator JWT, which must itself carry a valid signature and must list the operator key mashctl holds. `init --local` records `operator.jwt` in the key directory (0600, never replacing a file); `mashctl adopt` installs none, and without one the accepted issuer is the operator key it was given. `apply`, `diff`, `--dry-run`, `repair`, `drift` and the read-back after a push use the check, and `adopt` refuses (exit 1, nothing written) where it formerly warned.

Signing keys. Apply knows the account identity key and every zone key file in the key store. Any other signing key in the fetched JWT, scoped or plain, is refused (it would otherwise be carried into a JWT mashctl signs) by `apply`, `repair` and `--dry-run`, which name it by public key and role; `diff` and `drift` report it as a finding. The message says that mashctl will not carry signing keys it did not create and that the operator must remove them with the tool that added them before apply can run. `--keep-zone` and `--remove-zone` are unchanged for a zone key that is in the key store. The refusal exists so that mashctl never carries a signing key it did not create; it does not stop an explicit instruction that removes one. `--replace-zone-key ZONE` and `--remove-zone ZONE` drop the named zone's scoped key, and only a scoped signer whose role is exactly a zone of the file (or, for `--remove-zone`, a zone the file lacks) can be dropped; apply prints the public key and role being dropped and says that credentials signed by it, by any tool, stop working. A plain signing key, or a scoped key whose role differs in case or spelling, stays refused even when a flag names another zone.

Transport. Every connection mashctl makes with admin or system credentials (`natsauth.RequireTLS`) requires TLS: the connection fails when the server does not offer it, and a plain `nats://` or `ws://` URL to a host that is not `localhost` or a loopback IP literal is refused before any connection is made. The one opt-in for plain text is the setting `insecure_loopback` (`MASH_INSECURE_LOOPBACK=1`), a setting and not a command-line flag, honoured only when every host of the URL is loopback and refused otherwise; it may be renamed when the command-line settings are redesigned. `init --local` writes it into the `config.json` it creates. `init --local --start` connects only to the loopback server that same run generated and started, which has no TLS, and needs no opt-in; `bootstrap.Start` takes that as an internal parameter and refuses a URL that is not loopback. The client ignores server addresses the server advertises (`nats.IgnoreDiscoveredServers`) in both modes, and with `insecure_loopback` it dials through a dialer that refuses any address that is not `localhost` or a loopback IP, at connect and at every reconnect. Endpoint connections are unchanged.

### Consequences

* Good: what apply signs comes from a verified issuer and only mash's own keys.
* Good: a local server keeps working with one explicit setting.
* Bad: a deployment whose account JWT is issued by an operator signing key needs the operator JWT as `operator.jwt` in the key directory.
* Bad: an account that carries signing keys from other tools must have them removed with the tool that added them before apply can run.
* Bad: a server reached over plain `nats://` on a non-loopback address no longer works for admin verbs; use `tls://`.

### Confirmation

* `internal/apply` tests with a resolver that returns a JWT with another issuer, another subject, a damaged signature, an
  operator JWT that lacks the operator key, and unknown signing keys; `cmd/mashctl` tests on an in-process server for
  the same refusals through the commands, for `adopt`, and for TLS with and without the setting.
* `natsauth` tests of `RequireTLS` for loopback and non-loopback hosts.

## Pros and Cons of the Options

### Keep the checks as they were

* Good, because nothing changes for existing setups.
* Bad, because apply re-signs, with the operator key, content it did not verify, and plain-text admin connections are allowed anywhere.

### Verify the issuer, carry only mash's own keys, require TLS

* Good, because each requirement is one check with a named refusal and a named refusal.
* Bad, because setups with an operator signing key, other tools' signing keys or a plain remote server need extra work.

## More Information

* extends: [0052](0052-mashctl-mints-nats-users-and-holds-the-account-signing-key.md).
* Sources: nats-io/jwt v2 2.8.2 (`Decode` verifies the signature against the claimed issuer).
