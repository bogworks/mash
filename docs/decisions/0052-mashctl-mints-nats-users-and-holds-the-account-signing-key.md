---
status: accepted
date: 2026-11-02
decision-makers: Randy Bias (mash maintainer)
---

# mashctl mints NATS users under one scoped signing key per zone and holds the account signing key; it supersedes 0008

## Context and Problem Statement

[ADR 0008](0008-mash-never-mints-users-or-holds-seeds.md) kept every seed that can forge an identity out of mash:
`mash admin permissions` printed a template and the operator minted users with `nsc` by hand
(`docs/operations.md`, "Adding an endpoint": six shell steps per endpoint). That made adding an endpoint a NATS task,
and it left the permission set a pure function of owner and name (`internal/admin/permissions.go`), so no per-endpoint
policy could ever be expressed. The maintainer decided (2026-11-01) that mash grows an admin tool, `mashctl`, that
provisions NATS the mash way, mints identities and compiles a policy into their permissions. Holding the account
signing key is the price of one tool.

## Decision Drivers

* A solo developer adds an agent or a tool in one command, without learning NATS.
* Who may mail whom becomes a policy mash can compile ([ADR 0053](0053-who-may-mail-whom-is-a-policy-file-compiled-by-mashctl-into-nats-permissions.md)),
  which needs mash to issue the permissions.
* Authority stays with whoever holds the account key; there is no permission system inside mash itself.
* The layout must scale to thousands of endpoints: the account JWT has a hard 1 MiB ceiling (the JWT library's
  `MaxTokenSize`, which no server setting lifts) and a user JWT must fit the connect line (4 KiB by default). A
  ceiling under 100 endpoints is not acceptable (maintainer, 2026-11-02).
* A rule change should not re-issue or re-deliver credentials except for the endpoints it directly touches.

## Considered Options

* Keep 0008: mash prints flags, the operator mints with `nsc`.
* `mashctl` mints users with their permissions in their own user JWT.
* `mashctl` mints users under one scoped signing key per endpoint, the key carrying that endpoint's whole permission set.
* `mashctl` mints users under one scoped signing key per zone, where the zone is the one label carried in the mail
  subject; the key carries the zone's rules as wildcards and every endpoint's fixed lines as templates; exceptions are
  tags on the user.
* Per-zone accounts with exports and imports.

## Decision Outcome

Chosen option: one scoped signing key per zone, exceptions as user tags. `mashctl` holds the account signing key (and,
for a local server it stood up, the operator and system-account keys) in the operator's keyring or a 0600 file it
names, never in the repository, another tool's file, argv or an endpoint's environment. For each zone it creates one
account signing key whose permission set is the zone's compiled rules ([ADR 0053](0053-who-may-mail-whom-is-a-policy-file-compiled-by-mashctl-into-nats-permissions.md))
plus the per-endpoint fixed lines as `{{name()}}` templates and a `{{tag(allow)}}` template for exceptions. It signs
each endpoint's user JWT with its zone's key, carrying the name and the endpoint's exception tags, and writes the
credentials file (JWT plus the user's own seed) 0600 on the endpoint's host, as 0008 already required.

What changes when: a zone rule changes, one account push, and the server reconnects that zone's endpoints with the same
credentials (measured 2026-11-01: about 0.5 s, no mail lost); an endpoint is added, one user minted, one credentials
file delivered; an endpoint's exceptions change, that one user re-signed and its file re-delivered; an endpoint changes
zone, its mail subject, stream filter, directory record and user JWT change together, for that one endpoint.

Measured 2026-11-02 (nats-server 2.15.2, jwt/v2 2.8.2): the per-endpoint-key option crosses 1 MiB at 87 endpoints
all-to-all (134 MB at 1,000); user-JWT lists are 134 KB per user at 1,000 endpoints and connect only with
`max_control_line` raised to 1 MB; the zone layout is 5.9 KB, 21 KB and 102 KB at 5, 20 and 100 zones with sparse
rules, independent of the number of endpoints, and a user JWT with five exception tags is 864 bytes. Per-zone accounts
(1.4 KB each at 20 zones; a JetStream publish and its ack crossed the account boundary through a service import) are
the later step for hard isolation between organisations, not taken now.

This record supersedes 0008. It is landed with the first `mashctl` change, which moves the existing admin verbs under
`mashctl`; the minting arrives with later changes.

### Consequences

* Good: one command per endpoint; a rule change touches only the zone it names; size does not grow with endpoints.
* Good: `mash admin` folds into `mashctl`; `mash` is the endpoint's own tool. `mash admin` stays as a deprecated alias
  for now.
* Bad: mash now holds the secret that can forge any identity in the account. Mitigation: it lives only where `mashctl`
  runs, which in an enterprise is the operator's machine; developers hold endpoint credentials only.
* Bad: the mail subject gains a zone token (ADR 0053), which SAP tools would not expect; mash already resolves
  subjects through its directory.
* Bad: the account JWT grows with the square of the zones that mail each other, reaching 1 MiB at about 93 zones all
  mailing all; `mashctl` measures it and refuses one that will not push. Tens of zones are far inside.
* Bad: tags are lower-cased by the JWT library, so zone and endpoint names used in exceptions must be lower case, and
  whoever signs a user JWT under a zone key can grant exceptions: the zone key's holder is the policy authority.
* Existing deployments minted with `nsc` need a cutover: re-sign the same user keys under their zone keys (identity
  and directory record unchanged except the mail subject; only the JWT half of each credentials file changes),
  deliver, confirm every endpoint has reconnected, then revoke the old JWTs by timestamp. No rule is enforced before
  the revocation.
* Not yet true in code: minting (`init`, `adopt`, `add`, `retire`), zones and the scoped keys. Only the move of the
  existing admin verbs under `mashctl` has landed.

### Confirmation

* `cmd/mashctl` and `internal/ctl` tests run every moved verb (init, limits, permissions, provision, set-meta,
  deprovision, list) through `mashctl` against an in-process NATS server; `cmd/mash` tests confirm `mash admin` still
  works and says it is deprecated.
* The later changes confirm the rest: a user minted under a zone key connects with exactly its zone's permissions and
  its own fixed lines and no other endpoint's; a zone-rule change reaches connected endpoints without new credentials,
  including during a consume; `mashctl` measures and refuses an account or user JWT that will not push or connect.

## Pros and Cons of the Options

### Keep 0008

* Good, because mash holds no secret that can forge an identity.
* Bad, because adding an endpoint stays a six-step NATS task, and no per-endpoint rule can be compiled or enforced.

### Permissions in each user JWT

* Good, because the account JWT stays tiny.
* Bad, because a join re-issues the credentials of every endpoint that may mail it, and the list outgrows the default
  4 KiB connect line at about 24 recipients.

### One scoped key per endpoint

* Good, because a rule change is one account push and no credentials file changes.
* Bad, because the account JWT is quadratic in endpoints and cannot be pushed past 86 of them.

### One scoped key per zone, exceptions as user tags

* Good, because the account JWT is independent of the number of endpoints, and adding an endpoint changes no account.
* Bad, because the zone becomes part of the address and exceptions are tags (lower-case, one signer authority).

### Per-zone accounts

* Good, because each account is small and isolation is hard.
* Bad, because it is operationally heavier (an account, JetStream limits and imports per zone) and the denial is
  silent; it is the later step for isolation between organisations.

## More Information

* supersedes: [0008](0008-mash-never-mints-users-or-holds-seeds.md).
* Sources: the maintainer's rulings (2026-11-01, "Yes I reversed that rule"; 2026-11-02, a ceiling under 100 endpoints
  is not acceptable); the research document "Who Can Talk to Whom on Mash" and its decision table; the apply spike and
  the scale spike (nats-server 2.15.2, jwt/v2 2.8.2); NATS documentation on scoped signing keys and templates.
