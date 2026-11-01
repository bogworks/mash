---
status: accepted
date: 2026-10-30
decision-makers: Randy Bias (mash maintainer)
recorded: 2026-10-31 (backfilled)
---

# `mash init synadia` provisions a hosted account from the core binary

## Context and Problem Statement

Running mash needs a NATS server with JetStream, an account, the shared KV buckets (NATS's key-value store), and one
NATS user, credentials file and inbox stream per endpoint, all created by someone holding an admin credential
(ADR-0008). That is a lot to ask of someone who only wants to try mash. Synadia Cloud, a hosted NATS service, has a
free tier and a control-plane API that can create NATS users. The operator asked for a way to "setup, verify, and
show how to use" mash "quickly with Synadia Cloud using their free tier".

## Decision Drivers

* A first-time user should reach working mail in one command.
* The API token is a powerful secret: it must never appear on a command line or be stored.
* No standing admin credential may be left behind.
* The free tier's limits (5 streams, 5 connections, 512 KiB payload) must be checked before anything is created.

## Considered Options (reconstructed 2026-10-31; not recorded at the time)

* Document the manual steps in the Synadia console
* A separate setup script or tool outside the mash binary
* A subcommand in the core `mash` binary

## Decision Outcome

Chosen option: "a subcommand in the core binary", because the operator asked for a quick hosted setup (maintainer
design discussion, 2026-10-30, not public) and the README presents it as "the fastest way to try mash"
(reconstructed). Why it sits in the core binary rather than a separate tool was not recorded.

As built (mash layer, using Synadia's control-plane API and NATS), `internal/initsynadia`, `internal/synadia`:

* Token: read from `SYNADIA_API_TOKEN`, from stdin with `--token-stdin`, or from a prompt; never from argv, never
  stored.
* Discovery of team, system and account over the API; several candidates require `--team` or `--account`.
* Capacity check first: shared buckets plus one stream per endpoint against the account's stream limit, failing before
  anything is created.
* A temporary admin user per run, named `mash-init-admin-` plus random hex, held in memory and deleted at the end,
  also on failure or cancellation. Leftovers from a crashed run are warned about and deleted only with
  `--clean-admins`.
* Per endpoint: a NATS user (created through Synadia's API), creds written 0600, the inbox stream and directory
  record; then `config.json`, and a test mail checked for the `verified` label. Re-running is safe.
* `--dry-run` creates nothing.

### Consequences

* Good, because a new user gets working, verified mail from one token, with the hosted free tier.
* Good, because no admin credential or token outlives the run.
* Bad, because it depends on an API that "is not documented or stable" (`docs/install.md`, `/api/core/beta/`); if
  its shape changes, init stops with an error.
* Bad, because the core binary carries code for one vendor's service.
* Neutral, because removing an endpoint still needs an admin credential the user makes in the console
  (`docs/synadia.md`).

### Confirmation

* `cmd/mash/init_test.go` (against a fake Synadia API that mints real creds and an in-process NATS server):
  `TestInitSynadiaHappyPath`, `TestInitSynadiaRerunIsIdempotent`, `TestInitSynadiaDryRunCreatesNothing`,
  `TestInitSynadiaCapacityFailsBeforeAnythingIsCreated`, `TestInitSynadiaTheTokenNeverLeaks`,
  `TestInitSynadiaTokenSources`, `TestInitSynadiaDeletesTheAdminUserWhenProvisioningFailsHalfway`,
  `TestInitSynadiaDeletesTheAdminUserWhenTheContextIsCancelled`, `TestInitSynadiaEachRunsAdminUserHasAUniqueName`.
* `internal/initsynadia/names_test.go`: `TestCheckCapacity`; `internal/synadia/client_test.go`.
* A live run on the real free tier: `docs/evidence/2026-10-31-init-synadia-live/RECEIPT.md` (PR #173).

## Pros and Cons of the Options

### Manual console steps

* Good, because mash ships no vendor code.
* Bad, because a first-time user faces many error-prone steps.

### A separate script or tool

* Good, because the core binary stays vendor-neutral.
* Bad, because it is a second thing to install, version and test.

### A core subcommand

* Good, because it reuses mash's own provisioning code and ships with every binary.
* Bad, because the core binary takes on an unstable vendor API.

## More Information

* PRs #127 (bounded KV buckets, which Synadia requires; 2026-10-30), #128 (Synadia API client, 2026-10-30), #130
  (`mash init synadia`, 2026-10-31), #173 (live receipt, 2026-10-31).
* `README.md` (quickstart), `docs/synadia.md`, `docs/install.md`.
* On Synadia Cloud the hosted service holds the account's signing keys and creates the NATS users through its API;
  mash still holds no operator or account seed (ADR-0008).
* Related: ADR-0006 (one account), ADR-0007 (per-endpoint users and permissions), ADR-0008 (no minting of users from
  seeds), ADR-0010 (inbox limits).
