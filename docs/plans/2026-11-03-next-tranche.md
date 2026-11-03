# mash: next tranche (2026-11-03)

**Why:** the mashctl plan (init, apply, drift, adopt, policy file, zones, signed marks, directory polling, on-demand
presence) has merged. What is left is hardening from the security reviews, three follow-ups with design weight, test
coverage that was never exercised against a real cluster, and a few small features. This lists the rows that need no
maintainer ruling. Each is S (under a day, one pull request) or M (a day or two, one or two pull requests).

**Not in this tranche (Randy's calls):** F8 (do prompt and status follow the zone layout), F9 (the CLI redesign; the
mashctl freeze stands until it is ruled), #216 (emergency revoke with silent servers; needs F9 for the flag shape),
#218 (the endpoint publish grant on `_INBOX.>` and `$SRV.>`; needs a ruling and an ADR before code).

**Triage (open issues, 2026-11-03):** #253 fixed by #255 and closed. #179 (user JWT budget) is addressed by #241 (in
review); it closes with that pull request. #177 is split into rows 1 to 4 below. #220 is row 5. #235 is row 6. #199 and
#130 need a design ruling and are under "New direction". #216 and #218 are Randy's.

## Rows

Order is the order to take them. Owners are suggestions; the coordinator assigns.

| # | Size | Owner | Row | Issue |
|---|------|-------|-----|-------|
| 1 | S | mash | Key store custody: open seeds and the lock file with `O_NOFOLLOW`, then `fstat` the descriptor for type, mode and owner equal to the effective uid; check ancestors of the key directory; `CheckCredsMode` on the open descriptor; `bytes.TrimSpace` so no unwiped seed string copy is left. Tests with a symlinked seed, lock and directory. | #177 |
| 2 | S | mash | Creds writers: `writeCredsNew` and `replaceCreds` reuse `ctlkeys.InstallFile` (0600 temp, `O_EXCL`, fsync, link, directory sync); `reissue` refuses a seed that does not match the endpoint's recorded user public key. After #241 merges (same files). | #177 |
| 3 | S | mash3 | Output hygiene: quote server-derived strings (`%q`) in `list`, `suspended`, `drift`, ack and server-name lines so a hostile server cannot inject terminal escapes; cap `sys.go` lookup replies at `policy.AccountJWTLimit` and cap the server-name count. | #177 |
| 4 | S | mash3 | `--server` URLs with userinfo are redacted or refused in output, connect errors and `config.json`; `config.json` owner and mode are checked and relative `admin_creds`/`tls_*` paths are resolved against the config file, not the working directory. | #177 |
| 5 | S | mash2 | Docs: the key store needs its own UID or mount and a host's agents get only their own creds (the same-UID finding); `docs/policy-file.md` tag budget corrected to what fits at the default control line (after #241). | #177, #179 |
| 6 | M | mash3 | `add` and `reissue` verify the zone key is a `UserScope` signer in the account JWT before issuing: fetch the account JWT with the admin creds they already hold (system account only if no other copy exists), build the `ScopeVerifier`, refuse otherwise. Flag: if no copy is reachable with admin creds alone, this needs a design note first. | #220 |
| 7 | S | mash3 | Lift the 8-label cap per endpoint to 64, keep the key and value grammar and length limits. The cap is in the directory record validation. Needs a short ADR (it changes the record limit) and a compatibility note: an older endpoint reading a record with more than 8 labels. | #235 |
| 8 | S | mash4 | Diagnose the three undiagnosed flakes (`TestHookRedeliveryOfADrainedMessageIsAcked` second shape, `TestHookIdleWake`, `TestConfiguredTimeoutsApply`): reproduce under load, fix the test or the code, or record why not. | notes in memory |
| 9 | M | mash4 | A real three-node NATS cluster test for `policy apply`, `diff` and `drift` (today only a fake resolver covers the multi-server rules: every known server answers, complete set before push, read-back on all). Reuse the `scripts/dirbench` scratch cluster; skipped under `-short`. | none (coverage gap) |
| 10 | S | mash4 | A real TLS server test for `mashctl adopt --server` (verify chain, refuse plain, loopback opt-in), not only the loopback config path. | none (coverage gap) |
| 11 | S | mash | JetStream `sync_interval` doctor warning (a non-default `sync_interval` with `sync always` unset risks acknowledged writes lost on power loss); a check in the existing doctor output, no new verb. | none |
| 12 | S | security | Threat model and ADR 0042 text on self-hosted runners (the CI runner trust assumptions). | none |

## New direction (flagged; needs a ruling before code)

* **#199 One account-JWT lock across operators.** A key in the lease bucket held from read to read-back, taken by apply,
  repair and `RevokeUsers`. It makes the lease bucket a security primitive and needs admin or system creds to take the
  lock. Needs an ADR and a decision on who may write that key. Interim is documented (one operator at a time).
* **#130 Suspend and resume an endpoint's inbound mail** (`mash admin suspend`/`resume`, readable state). A new verb
  while the CLI freeze holds, so it waits for F9; JetStream's sealed stream setting is the likely mechanism and its
  interaction with mash's stream limits and permissions is unchecked. Needs an ADR.
* **#158 Attestations, `trust: ask`, default endpoint name `<user>-<host>`, CLI instance label `cli`.** Each changes
  behaviour that other tools see (trust labels, names); none is specified well enough to row. They need a spec or ADR
  from Randy's side first.
* **G312 fault-injection network test (faultnet).** New test infrastructure with its own maintenance cost; unscoped.

## Not mine to schedule

* PR 6, the live-account cutover (re-sign endpoints under zone keys, deliver, revoke old JWTs): nats-ops and Randy. It
  needs apply `--servers N` if that NATS is clustered.
* The Pi adapter drafts (#21 to #24) wait for the Pi-adapter design talk and an ADR.
* A release dry run on `main` with a real Fulcio certificate exercises the generator-commit check (#163): the coordinator's call.
