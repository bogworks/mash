# Is mashctl co-resident with mash? Audit summary (lane ctl, 2026-11-15)

For lead #614 (Randy's hard law: mash works fully in isolation, with no homerange or upward dependency; large setups run
mashctl on one central host against a remote multi-server NATS, driven by JSON). Three read-only audits, each with
file:line evidence on `origin/main` 5e610e5, written by subagents I briefed and checked (I read their summaries and spot
checked the cited lines; each claim is marked fact or inferred in its file):

- `standalone/audit-provisioning.md`: co-residency of init, limits, permissions, provision, set-meta, suspend/resume,
  deprovision, list, add, reissue, retire, label/unlabel, unzone, audit, inbox inspect, adopt.
- `standalone/audit-policy.md`: co-residency of `policy adopt|serve|reconcile|show|explain|diff`, `issue serve`,
  `wrangler *`, `issued *`, and the endpoint's `mash policy`; the standalone "direct path".
- `standalone/audit-json.md`: JSON in and out, errors and exit codes of every command.

(All paths are under ``.)

## Verdict

1. **Randy's criticism holds in one place, not everywhere.** No mashctl command assumes the NATS server is on the same
   host. Every command that only talks to NATS (record, label, suspend, list, inbox inspect, audit, limits, provision,
   set-meta, `policy show|explain`) works from a central host with the admin creds file and nothing else.
2. **The key-and-credential commands assume the key store, the system creds and the endpoint creds files sit together
   on the one host that runs mashctl**: `add`, `reissue`, `retire`, `deprovision`, `unzone`, `adopt`, `policy serve`,
   `policy reconcile`. That is a central host holding the operator's keys, which is the model Randy describes; what it
   lacks is a way to hand the results to the other 49 hosts and to drive it without text parsing.
3. **The real co-residency assumption is per account, not per host**: one config directory, one key directory and one
   set of creds files per account, selected by environment (`MASH_CONFIG_DIR`, `MASH_KEYS_DIR`, `MASH_OWNER`,
   `MASH_URL`, `MASH_ADMIN_CREDS`). One process serves one account. A driver loops over accounts with one env set each.
4. **Nothing in the audited code names homerange, a herd or a lane.** The homerange-shaped items are the policy bucket
   name pattern `{range}` (`cmd/mashctl/policy_listset.go:78-89`), comments such as `internal/policydoc/decode.go:1`
   ("the one value per account that homerange keeps and mash applies"), and the 3-token address `agent.owner.name`.
   The policy bucket "is created by operations; ask for it before applying policy" (`internal/.../kvledger.go:116-118`):
   no mash command creates it, so a standalone user cannot start (fact, per the policy audit).

## What breaks from a central host against a remote multi-server NATS

| Break | Where | Effect |
|---|---|---|
| `add` writes the endpoint's creds (with its seed) to `<config dir>/NAME.creds` only; no `--out`, no delivery | `add.go:222,232,307` | A central host must copy the secret to the endpoint's host itself |
| `reissue` needs each endpoint's creds file back, seed included | `reissue.go:431-452` | Fails if the file is not on the central host; the new JWT needs only the public key, so a seed-free mode is possible |
| `add`, `reissue`, `retire`, `deprovision` have no `--servers` or `--sys-creds` flag, only the environment | `add.go:260`, `retire.go`, `revoke.go:76-96` | A cluster revocation works only with `MASH_SERVERS=N`; the `adopt` hint tells people to pass `--servers N`, which these verbs reject (`adopt.go:286`) |
| A cluster joined by gateways is never "complete", so every revoke, `serve` and `reconcile` refuses | `apply.go:61` | No super-cluster |
| `policy serve` dials each server at the host:port that server reports, ignoring the configured URL list | `servers.go:149-151` | A cluster advertising internal addresses fails from a central host |
| The account-JWT lock is a local file `apply.lock` | `lock.go:22-23` | Two central hosts can erase each other's revocations |
| `policy diff` checks sizes against default limits, not the real servers | `policy_listset.go:267-269` | "Fits" is unverified |
| `issue serve` reads its list set from `<config dir>/listset.json`, not the ledger | issuecmd | It can drift from what `policy serve` applied |

## JSON: what a wrapper can and cannot do today

1. No mashctl command reads stdin or prompts; all run non-interactively. The only confirmation flag is `reissue --yes`.
2. JSON out exists for: `audit` (`version: 1`), `inbox inspect` (`schema: mash-inbox-inspect@1`), `issued list|check`,
   `policy show|explain|diff`, the `policy reconcile` dry run, `policy adopt --show`, `permissions` (always JSON).
3. **Every command that changes state prints text only**: `provision`, `add`, `reissue`, `retire`, `deprovision`,
   `set-meta`, `label`, `suspend`, `resume`, `limits`, `unzone`. `list` is tab-separated text without labels, role
   or host, and no admin command returns the directory record as JSON.
4. No command returns a JSON error; errors are stderr text. The same fault exits 1, 2 or 3 depending on the command;
   `3` means "not suspended" for `suspended` and "could not complete" for `audit`; `policy diff` exits 1 for both a
   refused plan and a failure; `reissue --all` without `--yes` exits 2 like a usage error; second runs of `provision`,
   `add`, `deprovision`, `retire` exit 1 with text that means "already done".
5. Policy documents go in as a file path only (no stdin); `reconcile --yes --json` is a usage error; `wrangler list`
   has no `--json`.
6. Three help texts disagree with the code: `permissions` says `add` gets narrower permissions (both use
   `admin.Permissions`); `reconcile --yes` "refused in this version" (it is implemented); `issue serve` omits the
   required `--test-account`.

## Which of the policy machinery is the adoption model, and which is the engine

- **Adoption-only** (the outside-wrangler model): the submitter key, the adoption marker, signed documents, the
  intake, `next_submitter` rotation, `reconcile`, the `accountGuard` refusals, the issue service, the wrangler registry.
- **The engine, usable by a mashctl that holds the keys** (policy audit, section 7): the list-set model (`Validate`,
  `Compile`, `DryRun`), `policyserve.Applier.Apply` (it never reads the adoption marker), `policyapply.Apply`,
  `GroupUserIssuer.Issue`, `policyview`; `policydoc.Decode` already accepts an unsigned document.
- **The direct path** ("allow X to talk to Y" with no signature) can be assembled from existing functions: read the
  in-force list set from the ledger, edit it, compile and diff it, encode an unsigned document, take the account lock
  (`adm.LockAccount`), call `Applier.Apply`. **Missing**: a verb that builds the next list set from "allow X Y"; an
  encoder for the unsigned document; a command that creates the policy bucket; **minting list-set credentials** (`add`
  mints account-key-signed, untagged users, `mint.go:1-5`, while list-set users need group-key signing and tags;
  `GroupUserIssuer` is only the signing half); a way to **deliver** reissued credentials (`FailClosedDeliverer` always
  errors).

## Changes, smallest first (from the audits; costs are estimates)

1. Text fixes: `main.go:60` ("reissue needs no server" is wrong), `adopt.go:286`, `ctlkeys.go:31` ("run `mashctl init`"
   does not create the key), the `revoke` verb that docs name but is not dispatched, the three help texts above.
2. A central-host recipe in `docs/operations.md` (the env set per account). Docs only.
3. Flag parity: `--servers` and `--sys-creds` on `add`, `reissue`, `retire`, `deprovision`, `unzone`. Small.
4. `--creds-out FILE` (and `--creds FILE` for reissue and retire) so a driver names where the secret lands. Small.
5. `--json` on every state-changing command (name, user public key, creds path, servers acked, outcome class), a JSON
   error object, and one documented exit-code table. Medium.
6. A command that creates the policy bucket (`policy init`) so the standalone path starts. Small.
7. Mint for a public key (`add --user PUB`; a `reissue` mode that signs for the key the directory record names and
   writes a JWT, no seed): the change that removes the "seed and creds file together" assumption. Medium.
8. A NATS-based lock for the account-JWT write instead of `apply.lock`. Medium; needed only with more than one
   central host.
9. Super-cluster and JetStream domain support in `ServerSet.Complete`. Medium to large.
10. A document-input form for policy by stdin, and `wrangler list --json`. Small.

## What I recommend for the 0.5.0 decision (for lead and Randy; not a ruling)

- The co-residency problem is narrow and fixable without redesign: items 1 to 6 are small, need no new concept, and
  make mashctl drivable from one central host with JSON. Item 7 is the one structural change.
- The policy surface Randy called clunky is built on the adoption model; the engine under it does not need adoption.
  The design doc (B), re-centred as lead asked, therefore proposes human verbs over a direct path that applies list
  sets with the keys mashctl already holds, and keeps the signed-document intake as the wrangler surface.
