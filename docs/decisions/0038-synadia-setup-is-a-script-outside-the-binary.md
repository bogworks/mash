---
status: accepted
date: 2026-10-31
decision-makers: Randy Bias
---

# Synadia setup is a script outside the binary

## Context and Problem Statement

`mash init synadia` ([ADR 0035](0035-init-synadia-provisions-a-hosted-account.md)) set mash up on Synadia Cloud from one API token: it called Synadia's control-plane REST API to discover the account, create a temporary admin NATS user and one NATS user per endpoint, then provisioned the endpoints. That put a client for an undocumented, beta API (`/api/core/beta`), about 2,900 lines of client, flow, guards and tests, and the ability to mint NATS users into the mash binary, which [ADR 0008](0008-mash-never-mints-users-or-holds-seeds.md) says must not create NATS users. How should the Synadia quickstart ship?

## Decision Drivers

* [ADR 0008](0008-mash-never-mints-users-or-holds-seeds.md): mash does not create NATS users; user creation belongs to the operator's tooling.
* The Synadia API has no public spec and no stability promise. Its shapes were found by probing a live account, so it can break without a mash release.
* The binary should stay small and its security surface (an API token in the process, user creation) should stay out of it.
* A newcomer still needs a one-command path onto the free tier.
* The flow must stay testable without the network.

## Considered Options

* Keep `mash init synadia` in the binary.
* A quickstart script outside the binary (`scripts/quickstart-synadia.sh`), plus a docs page.
* Docs only: a manual console walkthrough.

## Decision Outcome

Chosen option: "a quickstart script outside the binary", because it keeps the one-command path while removing the API client and user creation from mash, in line with [ADR 0008](0008-mash-never-mints-users-or-holds-seeds.md).

The script needs only bash, curl, jq and a `mash` binary. It reads the token from `SYNADIA_API_TOKEN`, stdin or a prompt (never argv, never stored), creates one temporary admin user and the endpoint users with the permissions `mash admin permissions` prints, writes `config.json`, runs `mash admin init` and `mash admin provision`, sends a test message, and always deletes the admin user (EXIT trap). `docs/synadia.md` documents it. `mash init`, `internal/initsynadia` and `internal/synadia` are deleted from the binary in 0.3.0.

### Consequences

* Good, because the binary no longer holds a Synadia API client, the API token or any user-creating code, and `mash admin` stays the only admin surface.
* Good, because an API change breaks a script a user can read and edit, not a released binary.
* Good, because the script is tested offline against a fake API and an in-process NATS server (`scripts/quickstart_synadia_test.go`).
* Bad, because the script drops guards the Go code had: the `config.json` file lock, a check of streams already in use (only the plan against the limit is checked), reuse of same-name users and records with permission verification, and cleanup of orphaned admin users from crashed runs. Those are documented, and the script refuses rather than reuses.
* Bad, because it needs curl and jq, and does not run on Windows without a bash.
* Bad, because the Go-level unit tests of the old client and flow are gone.

### Confirmation

* `scripts/quickstart_synadia_test.go` runs the script offline against a fake Synadia API and an in-process NATS server: endpoints created and provisioned, the admin user always deleted (also on failure), dry run creating nothing, refusals (plan over the stream limit, existing config, existing creds file, restricted API URL), and the default `mash` found on `PATH`.
* `internal/release` and `scripts/releasecheck` tests confirm the release carries the script and lists it in `SHA256SUMS`.
* `docs/evidence/2026-10-31-quickstart-synadia-live/RECEIPT.md` records a live run against the real Synadia Cloud API.

## Pros and Cons of the Options

### Keep `mash init synadia` in the binary

* Good, because one tool, typed code, full guards and tests.
* Bad, because it contradicts [ADR 0008](0008-mash-never-mints-users-or-holds-seeds.md) and ships a client for an unstable, undocumented API in every mash release.
* Bad, because every user carries the code and the token-handling path even if they run their own NATS.

### A quickstart script outside the binary

* Good, because it keeps a one-command path and takes the unstable API out of the release.
* Good, because users can read exactly what it does with their token.
* Bad, because of the dropped guards and the curl/jq dependency (see Consequences).

### Docs only

* Good, because there is nothing to maintain or to break.
* Bad, because the manual path (console clicks, copying keys, running `mash admin provision` by hand) is long and error-prone, and the first-run experience on the free tier suffers.

## More Information

* supersedes: [0035](0035-init-synadia-provisions-a-hosted-account.md) (`mash init synadia` in the binary).
* Relies on [ADR 0008](0008-mash-never-mints-users-or-holds-seeds.md) (mash does not create NATS users).
* The live run that verified the REST shapes is recorded in `docs/evidence/2026-10-31-init-synadia-live/`; the script reuses those shapes but has not itself been run against the live API.
