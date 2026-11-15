# Co-residency audit: policy, issue, wrangler, issued commands

Repo: the mash repository at origin/main 5e610e5. Read only; nothing run except help.
Marks: [F] read in code, [I] inferred from code, not run.
Paths are relative to the repo. `cmd/mashctl/` is abbreviated `c/`, `internal/` is `i/`.

## 0. Short answer

[F] No command in this set assumes the NATS server is on the same host (the one exception is the loopback opt-in, which is off by default). Every command assumes that the host where mashctl runs holds, on local disk, one account's config dir, key dir and creds files. That is a co-residency assumption of a different kind: mashctl is assumed to be co-resident with ONE ACCOUNT'S KEY STORE, not with the server.

[F] The commands that write or apply do work from a central host against a remote cluster, if the central host holds the key store for that account. There is one account per config dir and key dir, selected by environment (section 5).

[F] Real central-host breakers: (1) `policy serve` dials every server at the host:port the server itself reports (i/policyserve/servers.go:149-151), so a server that advertises an address the central host cannot reach fails the apply. (2) A cluster joined by gateways is never "complete", so `policy serve` and `reconcile` refuse it (i/apply/apply.go:61, 77-78). (3) No command creates the policy bucket (i/policyapply/kvledger.go:116-118). (4) No command writes a list set, so the only way to get one applied is a signed document written to the bucket by something else. (5) JSON-in is a file path only (no stdin).

## 1. Summary table

| command | central-host ready? | local files it needs | what breaks for a central host or remote cluster | evidence |
|---|---|---|---|---|
| `policy adopt` | yes, local files only on the central host | config dir (config.json), `keys/account.nk`, admin creds, sys creds file | none found. Writes the marker through the admin connection, so remote is fine. Reads sys creds only to get the sys user's public key. | c/policy_adopt.go:85-160 |
| `policy adopt --show` | yes | config dir, admin creds | none | c/policy_adopt.go:197-235 |
| `policy serve` | needs local files only on the central host, and one process per (account, range) | config dir, key dir with `operator.nk`, `operator.jwt` (optional), `account.nk`, `groups/*.nk`, admin creds, sys creds | server-reported addresses must be dialable from the central host. Super-cluster refused. The host is a single point of failure for applying and for `mash policy show/explain`. | c/policy_serve.go:67-205; i/policyserve/servers.go:132-164 |
| `policy reconcile` (dry run and `--yes`) | yes, local files only | config dir, key dir (`account.nk`, `groups/*.nk`; for `--yes` also `operator.nk`), admin creds, sys creds, optional `--listset FILE` | needs a complete server set (`--servers N`). Reads CONNZ over $SYS from every server through one sys connection, so no per-server dialing. | c/policy_reconcile.go:118-310, 539-580; c/issued.go:249-290 |
| `policy show` | yes | config dir, admin creds; key dir only if `--account` is not given | none. Not offline (comment in c/main.go:35 says it is; it is not). | c/policy_listset.go:122-166, 216-236 |
| `policy explain` | yes | same as show | none | c/policy_listset.go:239-265 |
| `policy diff FILE` | yes, but its size check is blind to the real servers | same as show, plus FILE, plus key dir if the document has `next_submitter` | size limits are the defaults, not the cluster's (`listSetServerLimits()` returns nil) | c/policy_listset.go:267-320, 326-350 |
| `issue serve` | needs local files only on the central host, one process per range | config dir `listset.json`, key dir `wranglers.json`, `account.nk`, `operator.nk`, `groups/*.nk`, admin creds, sys creds | reads its list set from a local file, not the ledger, so it can drift from what `policy serve` applied. Test accounts only. | c/issuecmd.go:116-216 |
| `wrangler init/add/list/remove` | yes, local file only, no server | key dir `wranglers.json` and `wranglers.json.lock`, `account.nk` | none. It is one file on one host. The file is read by `issue serve` on the same host. | c/issuecmd.go:219-346; i/wranglerreg/wranglerreg.go |
| `issued list` | yes | config dir, admin creds | none | c/issued.go:80-160 |
| `issued check` | yes | config dir, key dir (`account.nk` for the public key), admin creds, sys creds | needs `--servers N` or reports incomplete | c/issued.go:344-424 |
| `mash policy show/explain` (endpoint) | yes, runs on the endpoint's own host | the endpoint's own config and creds only | needs a running `policy serve` for its range: otherwise "no responder" | cmd/mash/policy.go:93-130 |

## 2. Per command

### 2.1 `mashctl policy adopt` and `adopt --show`

a. Host assumptions.
- [F] Config dir and env: `loadCtlConfig(env, lookup, true)` (c/policy_adopt.go:85) calls `config.Load`, which reads `MASH_CONFIG_DIR`, `MASH_URL`, `MASH_OWNER`, `MASH_ADMIN_CREDS`, `MASH_TLS_*`, `MASH_INSECURE_LOOPBACK` (i/config/config.go:90-92, 410-428). It refuses if `MASH_AGENT_TOKEN` is set (c/loadcfg.go:7, 20-21).
- [F] Key store: `openKeys` reads `$MASH_KEYS_DIR` (absolute) else `<config dir>/keys` (i/ctlkeys/ctlkeys.go:62-73). `keys.HeldKeys()` (c/policy_adopt.go:113) reads `account.nk` (required) and `operator.nk`, `system.nk` if present (i/ctlkeys/held.go:13-37). Adopt refuses a submitter equal to any held key (c/policy_adopt.go:127).
- [F] Admin creds: must belong to the same account as `account.nk` (c/policy_adopt.go:121-125).
- [F] Sys creds: `--sys-creds`, else `MASH_SYS_CREDS`, else `sys.creds` beside the admin creds or in `<config dir>/server/` (c/syscreds.go:25-45). Adopt only reads the sys user's public key from the file (c/policy_adopt.go:141); it does not connect with it.
- [F] The marker is the key `account` in the JetStream KV bucket `mash-adoption` of the account, written over the admin connection (i/store/adoption.go:15-17, 41-47; i/admin/adoption.go:56-76). It takes the account lock `_policy-account` (i/admin/accountlock.go:34, 45).
- [F] No loopback or $SYS use. No long-running service.

b. Central host to remote cluster: yes. The only host-local things are the config dir, `account.nk` and the creds files. [F] The refusal that the admin creds match `account.nk` means the central host must hold `account.nk` of the account, not just the public key (c/policy_adopt.go:113). A central host that holds only public keys cannot run adopt (inferred from that code path; `--account` is not an adopt flag).

c. One adopt acts on one account: the one in the config dir. Per-account state: the marker (bucket `mash-adoption`), the account lock. [F]

d. Adoption-only. See section 4.

e. [F] help text: "ADOPTING A REAL ACCOUNT IS A LIVE-ACCOUNT STEP: do it only on an explicit go from the maintainers" (c/policy_adopt.go:24). "outside system" appears in the usage and in `adoptedNote` (c/policy_adopt.go:20, 186-187).

### 2.2 `mashctl policy serve --range R --servers N`

a. Host assumptions.
- [F] Flags: `--range` required (c/policy_serve.go:73), `--servers` or `MASH_SERVERS` required (c/policy_serve.go:77-88), `--sys-creds`.
- [F] Bucket name from `MASH_POLICY_BUCKET`, with `{range}` replaced (c/policy_serve.go:90; i/policyapply/seam.go:20-39).
- [F] Key store: `account.nk` (c/policy_serve.go:108), and `apply.LoadTrust(keys)` reads `operator.nk` and `operator.jwt` (c/policy_serve.go:125; i/apply/trust.go:50-61). Without `operator.nk` it fails with "it exists only for a server `mashctl init --local` stood up" (i/ctlkeys/ctlkeys.go:27). Account JWTs are signed with the OPERATOR key (i/policyserve/signer.go:81-91). `groups/<range>.<owner>.nk` are read, and CREATED for a new owner group (i/policyserve/applier.go:303, `GroupKey(tpl.Group, true)`).
- [F] Admin creds must belong to the same account as `account.nk` (c/policy_serve.go:116-120). Admin creds are also used on a new connection per server (c/policy_serve.go:182-186).
- [F] Sys creds: one connection, `connectSys` (c/policy_serve.go:130; c/syscreds.go:48-71). It uses `cfg.URL`, the same comma list as the admin connection.
- [F] Over NATS: adoption marker (c/policy_serve.go:145), policy bucket and ledger (`NewKVLedger`, c/policy_serve.go:158), directory bucket `mash-directory` (c/policy_serve.go:166), issue record, inbox checks. The policy bucket must already exist (i/policyapply/kvledger.go:33-34, 116-118: "is created by operations; ask for it before applying policy").
- [F] Long-running: `intake.Run(ctx)` until SIGINT or SIGTERM (c/policy_serve.go:43, 205). It also mounts the policy-view responder on `agents.policy.<range>` in the queue group `mash-policy` (c/policy_serve.go:197; i/policyview/policyview.go:147-148).
- [F] What pins it to a host: the key dir (`operator.nk`, `account.nk`, `groups/*.nk`), the admin creds file, the sys creds file, the config dir. Nothing else is on disk. The ledger, the lease (`DefaultLeaseTTL` 2 minutes, i/policyapply/kvledger.go:24), the pending JWT and the marker are in NATS.
- [F] One process serves one account and one range (`Applier.Range`, `Intake.Range`, i/policyserve/applier.go:184-186; c/policy_serve.go:179-195). A document naming another range or account is refused (i/policyintake/intake.go:498, StageRange).
- [F] Refuses to start unless the account is adopted (c/policy_serve.go:146-151).

b. Central host to a remote cluster.
- [F] How it reaches every server for the push and the JWT read: through ONE sys connection. `$SYS.REQ.SERVER.PING` finds the servers (i/apply/sys.go:46, 267-345), `$SYS.REQ.CLAIMS.UPDATE` pushes to all (i/apply/sys.go:45), `$SYS.REQ.SERVER.PING.ACCOUNTZ` reads the JWT every server holds (i/policyapply/natscluster.go:19, 61-113), `$SYS.REQ.SERVER.PING.VARZ` reads each server's limits and client address (i/policyserve/servers.go:30, 61-115). No per-server URL is needed for these.
- [F] How it finds every server's address: `ReadServers` takes the host and port each server reports in VARZ (i/policyserve/servers.go:168-206). `ResolveURLs` then uses that host:port if it is not a wildcard, ignoring the configured URL list; if the server reports a wildcard or no host, it takes the configured URL list entry at the server's sorted position, only when the list has exactly N entries and N servers answered; otherwise the apply is refused (i/policyserve/servers.go:132-164). The scheme is taken from the first configured URL.
- [F] What it connects to those per-server URLs for: probes, witnesses, and one admin-credential "loader" connection per server (i/policyserve/applier.go:344, 360, 438-440). Each URL is vetted (TLS required unless loopback) before any connection (c/policy_serve.go:223-231; i/policyserve/applier.go:268).
- [F] Credentials for those connections: the admin creds (loaders), minted probe users signed by group keys (probes), and the senders' own probe creds (witnesses). All come from the central host's key store; none come from a host in the range.
- Breaks for a central host: [I] if the servers advertise an internal address (for example a private IP) that the central host cannot reach, the loaders, probes or witnesses fail after the sys read succeeds, and the apply is refused with "open a connection to server ...; nothing was pushed" (i/policyserve/applier.go:344-346). The configured URL list does not override a concrete reported host. Fix needs a per-server address override (section 6).
- [F] `--servers N` is one number for the whole deployment. For a super-cluster (any server reports gateways) `ServerSet.Complete` is false and the apply is refused: "a super-cluster's membership cannot be verified from here" (i/apply/apply.go:60-67, 77-78). [I] So 5 separate clusters joined by gateways cannot be one deployment for this command; 5 separate clusters not joined by gateways each need their own config dir and process.
- [F] A single cluster with N routed servers is supported and tested (c/policy_serve_cluster_test.go:153, `TestPolicyServeAppliesOnTwoRoutedServers`).

c. Scale.
- [F] One process = one account + one range. [F] An account may hold several ranges: the Issuer keeps other ranges' group keys (i/policyserve/signer.go:34-37; i/policyapply/gate.go:31-33 `KeptSigners`), the bucket is per range via `{range}` (i/policyapply/seam.go:10-14), and the account lock is account-wide (i/admin/accountlock.go:34), so several range processes of one account take turns on it.
- [F] Selection of the account: config dir (`MASH_CONFIG_DIR`), key dir (`MASH_KEYS_DIR`), `MASH_URL`, `MASH_OWNER`, `MASH_ADMIN_CREDS`, `MASH_SYS_CREDS`. No flag selects an account for `serve`.
- [I] 5 ranges with 5 accounts = 5 processes, 5 config dirs, 5 key dirs, 5 admin creds. 200 herds: herds are not a unit here (grep for "herd" in non-test Go finds one comment, i/listset/compile.go:11); the unit is the owner group `range.owner` and the sender address. One serve handles all senders of its range.
- [F] Per-account state: marker (`mash-adoption`), account lock (`_policy-account`, in the lock bucket), ledger `status.<account>`, `.pending`, `.lease` in the range's policy bucket (i/policyapply/seam.go:54-60), the list set key `listset.<account>` (i/policyintake/intake.go:42), the issue record (`mash-issued`). Host-local: key dir and `apply.lock` (i/ctlkeys/lock.go:14, advisory flock, host-local: "does nothing for copies of the keys on other hosts").

When the host dies [F/I]: new list sets are not applied (the intake is the only watcher). `mash policy show/explain` fail with "policy view unavailable: no responder" (cmd/mash/policy.go:117-119). The applied policy stays in the servers' account JWT [I: standard NATS behaviour]. A restart resumes a killed apply (c/policy_serve_restart_test.go:221, 227, 251) [F: tests exist]. A second host can run the same `serve` if it has a copy of the key dir; the lease and account lock serialize the two (i/policyintake/intake.go:6-12) [I]. Failover is not automatic.

d. Adoption-only: the intake, the marker check, the signature check. The apply core is not adoption-only (section 4).

e. Homerange-shaped:
- [F] i/policyapply/seam.go:10-12: "The seam with the range manager that writes list sets. Operations creates one KV bucket per range, shared with that manager, which writes listset.<account public key> there".
- [F] i/policyapply/seam.go:14: "The bucket name is configuration ... because the seam (decision D211) is pending. The proposed pattern is "<manager>_{range}_policy"".
- [F] i/policyapply/kvledger.go:33-34 and 117: "Operations creates it; mash never does" and "is created by operations; ask for it before applying policy".
- [F] i/policyintake/intake.go:40-41: "The outside system writes only keys of this family".
- [F] c/policy_serve.go:272-275 (usage): "one process serves the one account the key store and the admin credentials belong to; it refuses to start unless that account is adopted".

### 2.3 `mashctl policy reconcile`

a. Host assumptions.
- [F] Same config dir, `account.nk` (or `--account KEY`, then no seed read, c/policy_reconcile.go:163-171), group publics (`keys.GroupPublics()`, c/policy_reconcile.go:172), admin creds (public key and issuer, c/policy_reconcile.go:176-183), sys creds (`connectSys`, c/policy_reconcile.go:225). Optional `--listset FILE` read from local disk (c/policy_reconcile.go:186).
- [F] Policy bucket: `--bucket`, or `MASH_POLICY_BUCKET` with `--range` (c/policy_reconcile.go:487-513, 515-527).
- [F] `--yes --plan DIGEST` additionally uses `operator.nk` and `operator.jwt` (`apply.LoadTrust`, c/policy_reconcile.go:370) and signs and pushes an account JWT (`policyserve.Issuer`, c/policy_reconcile.go:374-380). It takes the account lock and the ledger lease. The dry run takes no lease and writes nothing.
- [F] Reads the marker (must be adopted, must record exempt keys, c/policy_reconcile.go:207-220). Reads the issue record, the directory and `readInForce` from the ledger.

b. Central host to remote cluster: yes. Reads connected keys from every server with one sys connection: CONNZ ping then a direct per-server CONNZ over `$SYS` (i/apply/connz.go:20-21; c/issued.go:249-290). No per-server dial. A scan that is not complete is reported as incomplete (c/issued.go:277-289) and the exempt-issuer check refuses on an incomplete scan (c/policy_reconcile.go:349-351).

c. One account per config dir; `--account` here only chooses which key the ledger is read for; the admin connection is still bound to the config dir's account [I].

d. Adoption-only: the whole command (its input is the adoption marker's exempt keys).

e. None found beyond the adoption wording.

### 2.4 `mashctl policy show`, `explain`, `diff`

a. Host assumptions.
- [F] All three open an ADMIN connection (`ctl.OpenAdmin`, c/policy_listset.go:151) and read the ledger of the account in the range's bucket (c/policy_listset.go:160). So they are not offline. [F] The comment `case "policy": // offline: no server, no admin credentials` (c/main.go:35) is wrong.
- [F] Account: `--account KEY`, else `keys.Account()` from `account.nk` (c/policy_listset.go:139-150). With `--account KEY` no key store is opened.
- [F] Bucket: `--bucket` or `MASH_POLICY_BUCKET` (+ `--range`) (c/policy_listset.go:80-99). A literal bucket needs no `--range`; `diff` needs `--range` when nothing is in force.
- [F] `diff FILE` reads FILE from local disk with `os.ReadFile` (c/policy_listset.go:282). No `-` for stdin. The signature is checked only with `--submitter` (c/policy_listset.go:287-291).
- [F] `diff` reads the adoption marker over the admin connection (c/policy_listset.go:328) and, only for a document with `next_submitter`, the key store and admin creds (c/policy_listset.go:340-345).
- [F] `diff` runs `policyserve.FailClosed` with the admin connection, so it reads inboxes (c/policy_listset.go:301-304).
- [F] `diff` size check uses default server limits because "mashctl has no way to learn a server's limits" (c/policy_listset.go:267-269). It does not use sys creds and has no `--servers`.

b. Central host to remote cluster: yes, over NATS only. `diff` result can say "fits" for a list set that `serve` refuses for size (inferred from the two code paths: i/policyserve/applier.go:273 uses `Limits(servers)`, diff uses nil).

c. One account per config dir.

d. `show`, `explain` and the list set model are not adoption-only. `diff` carries rotation and signature logic that is adoption-only (`RotationContext`, c/policy_listset.go:326-350); the plan and refusal logic is not.

e. None found.

### 2.5 `mashctl issue serve`

a. Host assumptions.
- [F] Requires `--test-account` (c/issuecmd.go:131-133).
- [F] Local files: registry `<key dir>/wranglers.json` (or `--registry`, c/issuecmd.go:166-175); list set `<config dir>/listset.json` (or `--list-set`, c/issuecmd.go:177-184), read by `issue.FileListSet` which demands a regular file owned by the user with mode `& 0022 == 0` and a strict parent directory (i/issue/listsetfile.go:25-31, 60-70). `account.nk` (c/issuecmd.go:156), `operator.nk` and `operator.jwt` (`LoadTrust`, c/issuecmd.go:188), `groups/*.nk`, never created (c/issuecmd.go:100-103), admin creds, sys creds.
- [F] The list set is a JSON file in the shape of `listset.ListSet` (`{"Range","Switches","Senders"}`), not the ledger, and `Generation` is 0 (i/issue/listsetfile.go:21-29: "The generation is 0 until the policy gate lands"). [I] Two sources of truth for one account: the ledger (policy serve) and this file.
- [F] Subject: `agents.issue.<range>.*`, taken from the list set file's `Range` (c/issuecmd.go:198; i/issue/service.go:36-50). One range per process.
- [F] Registers a NATS micro service named `mash-issue` on the admin connection in the queue group `mash-issue` (i/issue/service.go:50-62). [I] Two hosts may run it; the nonce set is in memory per process (i/issue/nonces.go), so replay protection is per process.
- [F] Runs until SIGINT or SIGTERM (c/issuecmd.go:42-45, 207-215).

b. Central host to remote cluster: yes by the code (admin and sys connections over `cfg.URL`). The wranglers (on other hosts) must be able to publish on `agents.issue.<range>.<owner>`, which is "account setup" the code does not do (i/issue/service.go:21-23). The account JWT is read through the sys connection with `--servers N` (c/issuecmd.go:136-148, 190).

c. One account, one range, per process. State: registry file and its lock (host-local), list set file (host-local), issue record (NATS).

d. Whole command is outside-wrangler-only. See section 4.

e. Homerange-shaped:
- [F] c/issuecmd.go:131-132: "the static wrangler registry is a test-account facility and real wrangler registration is not decided yet".
- [F] i/issue/service.go:20-23: "A wrangler's permission to publish on it, and to receive the reply, belongs to account setup."
- [F] i/issue/request.go:50: `Host string "the name the wrangler is registered under"`.

### 2.6 `mashctl wrangler init|add|list|remove`

a. [F] Only local files: `<key dir>/wranglers.json` and `wranglers.json.lock` (c/issuecmd.go:277; i/wranglerreg/wranglerreg.go:32, 55, 167-181). `account.nk` is opened to learn the account public key (c/issuecmd.go:268-272). No server, no creds. The registry binds a wrangler public key to a (host, owner group) pair.
b. [F] Not a remote operation at all. On a central host with a key dir per account, there is one `wranglers.json` per account, and `issue serve` must run on the same host (or with `--registry`).
c. One file per key dir.
d. Whole command is outside-wrangler-only.
e. [F] i/wranglerreg/wranglerreg.go:38: "the static registry is for test accounts only; real wrangler registration is not decided yet". Output: `list` prints tab-separated text, no `--json` (c/issuecmd.go:318).

### 2.7 `mashctl issued list|check`

a. [F] `list`: config dir and admin creds; reads the issue record `mash-issued` over NATS (c/issued.go:131-142). `check`: plus `account.nk` for the account public key (c/issued.go:353-358), sys creds, `--servers N` or `MASH_SERVERS` (c/issued.go:104-118). It compares CONNZ of every server with the record.
b. [F] Yes for both. `check` is one sys connection to the cluster.
c. One account per config dir.
d. Not adoption-only: the issue record is written by `mashctl add` and `reissue` as well (c/add.go:206-230 region, `RecordIssued`). Independent of the wrangler model.
e. None found. [F] Exit codes 0, 1, 2 are defined and `--json` exists (c/issued.go:25-32, 261-264).

### 2.8 Endpoint side: `mash policy show|explain` (cmd/mash/policy.go)

a. [F] Runs as the endpoint: needs `MASH_NAME` (cmd/mash/policy.go:62), the endpoint's own creds. It signs a request and sends it to `agents.policy.<range>` (cmd/mash/policy.go:96-107). Only a list-set credential (signed by a scoped key) may ask; any other gets a local "owner line only" answer (cmd/mash/policy.go:21, 87-94). It reads no key store and no admin creds.
b. [F] Works from any host that reaches NATS. [F] It needs a `policy serve` responder for the range (cmd/mash/policy.go:117-119). The responder resolves the caller's key to an address through the directory bucket (i/policyview/resolve.go:11-30).
c. One responder per range; two `serve` processes share the queue group `mash-policy` (i/policyview/policyview.go:147-148).
d. [I] The view is not adoption-only, but its only server today is `policy serve`, which refuses an unadopted account. So today the endpoint view needs adoption.
e. [F] cmd/mash/policy.go:23 text: "this credential is not covered by a list set: it has the owner line only".

## 3. What a central host needs on disk and in the environment

Per account (one set per account; 5 accounts = 5 sets) [F unless marked]:

Files:
- Config dir with `config.json` (or the env): `url` (a comma list of all servers, `tls://`), `owner`, `admin_creds`, `tls_ca`. Path chosen by `MASH_CONFIG_DIR`. (i/config/config.go:90-92, 195-215)
- Key dir at `$MASH_KEYS_DIR` or `<config dir>/keys`, mode 0700, owned by the user, not a symlink (i/ctlkeys/ctlkeys.go:62-73, 92-118), holding:
  - `account.nk` (adopt, serve, reconcile, issue serve, issued check; show/explain/diff only if `--account` is not given)
  - `operator.nk` and `operator.jwt` (serve, reconcile `--yes`, issue serve). These sign every account JWT. The operator key is the root key of the NATS deployment. [I] With one operator and several accounts, each key dir needs its own copy of `operator.nk`.
  - `system.nk` (read only if present, for the held-keys list)
  - `groups/<range>.<owner>.nk` (created by `serve` for new owner groups)
  - `wranglers.json` (+ `.lock`) for `issue serve`
  - `apply.lock` (advisory, host-local)
- Admin creds file (0600): the account's admin user.
- Sys creds file (0600): `--sys-creds` or `MASH_SYS_CREDS`, else `sys.creds` beside the admin creds or in `<config dir>/server/`. Used by serve, reconcile, issue serve, issued check.
- For `issue serve` only: `<config dir>/listset.json`.
- For `diff` and `reconcile --listset`: the document as a file.
- TLS CA (and optional client cert and key) files.

Environment (all read by the commands in scope):
- `MASH_CONFIG_DIR`, `MASH_KEYS_DIR`, `MASH_URL`, `MASH_OWNER`, `MASH_ADMIN_CREDS`, `MASH_SYS_CREDS`, `MASH_SERVERS`, `MASH_POLICY_BUCKET`, `MASH_TLS_CA`, `MASH_TLS_CERT`, `MASH_TLS_KEY`, `MASH_INSECURE_LOOPBACK`.
- `MASH_AGENT_TOKEN` must be unset or every verb refuses (c/loadcfg.go:7, 20-21).
- [F] `announceTarget` warns when `MASH_URL` is set and `MASH_NAME` or `MASH_LAUNCH_TOKEN` is also set (c/target.go:85-86 region): a central host that is also an agent host will see this warning and `MASH_URL` of the agent session will override config.json. [I] Use `env -u` or a clean environment per account.

Network:
- The URL list: the central host must reach the servers' client port with TLS (plain only to loopback with `MASH_INSECURE_LOOPBACK`, i/natsauth/natsauth.go:180-196).
- For `policy serve` also: the central host must reach every server at the host:port the server reports in VARZ (i/policyserve/servers.go:149-151).

## 4. Which pieces exist only because of the adoption model

| piece | exists because of | what is under it that a direct path can use |
|---|---|---|
| submitter key (`--submitter`, `Marker.Submitter`) | an outside system signs list sets (i/store/adoption.go:41-47) | nothing; it is a verification key |
| adoption marker (`mash-adoption`), `policy adopt`, `adopt --show` | tells every other command the account is owned by the outside system (ADR 0111) | the account lock `_policy-account` and `ctl.GuardAccountFor` are separate and stay useful (i/admin/accountlock.go, i/ctl/adoption.go:108) |
| `accountGuard` refusal of add, reissue, retire, label, unlabel, set-meta, provision (c/guard.go:20; c/policy_adopt.go:186) | an adopted account cannot change policy by hand | none; this is the inverse of the direct model |
| signed list-set document (JWS), `policydoc.DecodeOptions.Submitter`, `ErrUnsigned`, `ErrBadSignature` (i/policydoc/decode.go:43-55, 102-120) | verify the submitter | `policydoc.Decode(raw, DecodeOptions{})` already accepts raw canonical JSON with no signature and returns `Verified=false` (i/policydoc/decode.go:106-112, 116-118) [F] |
| `policyintake.Intake` (watch `listset.<account>`, marker check, signature, rotation, refusal reports) (i/policyintake/intake.go) | the intake of the outside system's writes | nothing the apply needs; `Applier.Apply` takes only `policyintake.Doc{Raw}` and a `Guard` (i/policyserve/applier.go:183) |
| `next_submitter`, planned rotation, `RotationStep`, recovery adopt | submitter key rotation (ADR 0112, 0119) | none |
| exempt keys, `policy reconcile` | account holds users and signing keys made before the outside system took over (ADR 0121, 0124) | CONNZ scan and issue-record reading (`scanConnections`, `heldAccountClaims`) are generic |
| issue service, wrangler registry, countersignature (`i/issue`, `i/wranglerreg`, ADR 0104) | the sender's private key never leaves its host, a wrangler asks for the credential | `issue.Issuer.issue` after the registry and proof steps: list set lookup, scope check, group-key signing, checked record write (i/issue/issuer.go:~150-240) |
| `policy serve` bucket `listset.<account>` and the policy bucket "created by operations" | the range manager writes there | `KVLedger` (status, lease, pending, in force) is the ledger of the apply; it needs the bucket to exist |
| `mash policy show/explain` responder | a sender looks at its own reach | `policyview.Mount` works on any `Store` |

Engine pieces under it, and what a mashctl that holds the keys can drive directly with no signature step [F, function names read in code]:

1. List set model: `listset.ListSet`, `Validate`, `Compile` (i/listset/validate.go:81, i/listset/compile.go:64), `Explain`, `Show`, `DryRun`, `SizeCheck`. Pure functions. No keys.
2. Document form: `policydoc.Document`/`Decode` (unsigned JSON accepted). Needs `schema`, `account`, `range`, `generation` = `base_generation`+1, `issued_at`, `groups`, `senders` (i/policydoc/decode.go:158-200).
3. Compile to templates: `Compiled.Templates` (one per owner group `range.owner`) and `Compiled.Users` (tags per sender). Group keys: `Keys.GroupKey(group, true)` creates a scoped signing key in the key store (i/policyserve/applier.go:303).
4. Apply gate: `policyapply.Apply(ctx, ApplyRequest, ApplyDeps)` (i/policyapply/apply.go, called at i/policyserve/applier.go:434). It checks base generation, compiles, signs and pushes the account JWT to every server, probes, witnesses and commits to the ledger. It does not read the marker or the signature. The adoption marker is only used by the intake and by `Issuer.ExemptIssuers`/`canDrop` (i/policyserve/signer.go:107-112), which drops non-mash signing keys only at generation 1 and only when `AdminIssuer` and `ExemptIssuers` are set.
5. Credential reissue: `policyapply.Rotate`, `GroupUserIssuer.Issue(address, userPub, name, tags)` (i/policyapply/rotate.go:44-70), `RemoveSender`, `RotationsAfterPush`. Rotation requires a `Deliverer`; the only one is `FailClosedDeliverer` which always errors "not decided" (i/policyserve/failclosed.go:172-181). A change that narrows a sender's tags is refused before the push (NarrowingDetail, i/policyserve/failclosed.go:24-27, applier.go:291).

## 5. Selecting an account and scale (summary)

- [F] An account is selected only by the environment (`MASH_CONFIG_DIR`, `MASH_KEYS_DIR`, `MASH_URL`, `MASH_OWNER`, `MASH_ADMIN_CREDS`, `MASH_SYS_CREDS`). `--account KEY` exists on show, explain, diff and reconcile and selects only which account's key the ledger is read for.
- [F] One process handles one account; `serve` and `issue serve` also one range.
- [I] 5 accounts on one central host = 5 environments, 5 key dirs, and one `serve` per (account, range) plus one `issue serve` per (account, range) if the wrangler path is used. A process supervisor on the central host would have to hold these.
- [F] Per-account state in NATS: marker, account lock, ledger and lease per range, issue record, directory. Per-account state on the host: key dir, config dir, creds, `wranglers.json`, `listset.json`.

## 6. What must change for the central model (small, ordered by effort)

1. [Doc, minutes] Remove the false comment `// offline: no server, no admin credentials` for `policy` in c/main.go:35. State in `docs/mashctl.md` that every verb in this set connects with admin creds, and which also need sys creds and the operator key.
2. [Small] `policy diff` size check: read the real limits through the sys connection (use `policyserve.ReadServers`) when `--sys-creds` and `--servers` are given; otherwise say in the output that sizes were checked against default limits. Today it says nothing (c/policy_listset.go:267-269).
3. [Small] `ResolveURLs`: let the operator say the configured URL list wins over the reported host (a flag or `MASH_SERVER_ADDRS`, one entry per server name). Without it a server that advertises an internal address blocks apply from a central host (i/policyserve/servers.go:149-151).
4. [Small] `--json` and stdin: accept `-` for FILE in `diff` and `reconcile --listset`; add `--json` to `policy adopt` (write path), `reconcile --yes`, and `wrangler list`. Today `reconcile --yes --json` is a usage error (c/policy_reconcile.go:144-145) and `wrangler list` is tab-separated (c/issuecmd.go:318).
5. [Small] Create the policy bucket when missing: a `mashctl policy init` (or in `init`) that creates it. Today only "operations" does (i/policyapply/kvledger.go:116-118), and a standalone setup has no operations.
6. [Small] Remove the need for `account.nk` where only the public key is used (`adopt`, `serve`, `issue serve`, `wrangler`): take `--account KEY` as `show` already does (c/policy_listset.go:139-150). Reduces the secrets that sit on the central host to those that sign.
7. [Medium] Make `issue serve` read the list set from the ledger (`KVLedger.InForceView`) instead of `<config dir>/listset.json`, or retire it from the standalone path (c/issuecmd.go:177-184).
8. [Medium] One process per account is a scaling limit for 5 accounts only if processes are managed by hand; a `policy serve` that takes a list of (config dir, range) pairs is not needed to meet the requirement. Systemd units or a supervisor per account are enough.
9. [Medium] Failover: nothing prevents a second `serve` on another host holding a copy of the key dir; document it and test it (lease 2 minutes). [I]
10. [Large, only if super-clusters are in scope] `ServerSet.Complete` refuses gateways (i/apply/apply.go:61). A deployment of several clusters joined by gateways needs a declared per-cluster server list. Not needed for one cluster of N servers.

## 7. The direct path

Goal: "a human says: allow agent X to talk to agent Y" to an applied list set, with mashctl holding the keys, no adoption, no submitter, no signature.

Existing functions, in order [F: all read in code]:

1. Load config and keys: `loadCtlConfig` (c/add.go:103), `openKeys` (c/add.go:111) -> `ctlkeys.Authority` (needs `account.nk`, `operator.nk`).
2. Read the list set in force: `policyapply.NewKVLedger(ctx, js, account, KVLedgerOptions{Bucket})` (i/policyapply/kvledger.go:113) then `InForceView()` returns `(listset.Record, listset.ListSet, ok, err)` (used at c/policy_reconcile.go:255 `readInForce`). Gives the current generation and `ListSet`.
3. Edit the list set in memory: append to `ListSet.Senders[X].Allow` the address Y (types in i/listset/listset.go:57-100: `ListSet{Range, Base, Switches, Senders}`, `SenderLists{Sender, Allow, Deny}`). A new sender X and a switch entry per verb for its owner group are needed for a first list set (the validator requires all four verbs per group).
4. Check: `listset.Validate`, `listset.Compile` (compile and self-check), `listset.DryRun(inForce, next, servers)` (i/listset/dryrun.go:131), `policydoc.Diff` for the plan with the fail-closed checks (c/policy_listset.go:301). These are what `policy diff` runs.
5. Make a document: marshal `policydoc` wire form (schema, account, range, generation = in-force generation + 1, base_generation, groups, senders, issued_at) as canonical JSON. `policydoc.Decode(raw, DecodeOptions{})` accepts it unsigned (i/policydoc/decode.go:106-112).
6. Take the account lock: `adm.LockAccount(ctx)` (i/admin/accountlock.go:45), which gives `Held(ctx)`. A `policyintake.Guard` is `Before(ctx, what) error`; a one-method adapter over `lock.Before` would do (`ctl.Guard.Before` has the same behaviour without a ctx, i/ctl/adoption.go:48-55).
7. Apply: build `policyserve.Applier` exactly as c/policy_serve.go:179-188 does (Account, Range, Ledger, Sys, Keys, Trust, Record, KV, Servers, URL, Vet, Options, Inboxes, Load, plus `AdminUser`, `AdminIssuer`, `ExemptIssuers`), then call `app.Apply(ctx, policyintake.Doc{Raw: raw, ...}, guard)` (i/policyserve/applier.go:183). That ends at `policyapply.Apply`. Status and ledger are written by it. [I] This works without the marker: `Applier.Apply` does not read it. With `ExemptIssuers` nil and `AdminIssuer` empty, `Issuer.Sign` refuses an account JWT that carries a signing key that is not an owner-group key of the key store (i/policyserve/signer.go:37-47, 107-112); [I] so on a fresh `init --local` account whose JWT holds the account's own signing key set this may need checking.
8. Result: `Result.StatusWritten`, or the error; the ledger holds the generation; `mashctl policy show` reads it.

What is missing for the direct path [F: grep found no such function]:

a. A command, or a function, that builds the next list set from "allow X Y" and the in-force list set. No `policy allow`, `put` or `apply` exists in mashctl (the `policy` verbs are adopt, serve, reconcile, show, explain, diff).
b. A function that builds the unsigned document bytes from a `ListSet` and the in-force record. Only the decoder exists (i/policydoc/decode.go); the encoder exists only in tests, if at all [I].
c. A guard that does not need the marker: `policyintake.guard.check` requires the marker (i/policyintake/intake.go:615-630). A direct caller supplies its own `Guard` (item 6).
d. The policy bucket: no command creates it (section 6 item 5).
e. A way to mint a list-set credential for X. `mashctl add` mints a user signed by the ACCOUNT key with owner-line permissions and no tags (i/mint/mint.go:1-5, 54-62: "There is no per-zone scoped signing key and no tag"). A list-set user must be signed by the owner group's scoped key and carry the sender's tags (i/issue/issuer.go:200-215; i/listset/compile.go:56-62). The signing half exists: `policyapply.GroupUserIssuer.Issue(address, userPub, name, tags)` (i/policyapply/rotate.go:44-70). Missing around it: generate the user key, take tags from `Compiled.Users`, write the record (`adm.RecordIssued`/`RecordIssuedChecked`), the directory record and inbox (`adm.Provision`), and write the creds file. That is what `mashctl add` does for the other credential type (c/add.go:206-330).
f. A "new address" and a "narrowed sender" are refused by `FailClosed` (i/policyserve/failclosed.go:183-191, applier.go:291): the address must already have its inbox, and a tag change on an existing sender needs a credential reissue that `FailClosedDeliverer` does not do. On a single host, with the key and the creds file together, a direct `Deliver` (write the new creds file) is possible; no function does it yet.
g. Reconcile, issue service and the wrangler registry are not on the direct path.

## 8. Facts that affect the plan, listed once

- [F] `mashctl add` credentials and list-set credentials are two different credential kinds (account-key-signed with owner-line permissions; scoped-key-signed with tags). An account whose users came from `add` has no list-set reach until its users are reissued as list-set users. `policy reconcile` exists to plan the removal of such users on an adopted account (c/policy_reconcile.go:32-43).
- [F] The listset type comment says "ListSet is the switches and lists for one account" and the package doc says "for one NATS account (one range)" (i/listset/listset.go:5, 52). A multi-range account is also supported by `KeptSigners` and per-range buckets. The two statements are not reconciled in the code. [I]
- [F] Sender addresses are three-part `range.owner.name`; `--range` and `{range}` are part of every bucket and command here. A solo setup still has a range name.
