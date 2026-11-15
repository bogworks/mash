# Co-residency audit of mashctl provisioning commands

Repo: the mash repository, detached at origin/main 5e610e5. Read-only. `go build ./cmd/mashctl` passes.
Tags: [F] = fact, read in code (file:line). [I] = inferred from code, not run.
Paths are relative to the repo root.

## Verdict in four lines

1. Randy's criticism holds, but only for the key-and-credential verbs. Every verb that only talks to NATS (record, label, suspend, list, inspect, audit, limits) is already central-ready. [F]
2. Four things must sit on the one host that runs the key-and-credential verbs: the account key store, the system creds, the admin creds, and a copy of every endpoint's creds file with its seed. [F]
3. `add` makes the endpoint's user seed on the central host and writes the creds file into mashctl's own config dir. There is no flag for the output path and no delivery step. `reissue` then needs that same file back. [F]
4. Cluster revocation works from one host only when `MASH_SERVERS=N` is set and the servers form one cluster without gateways. Retire and deprovision have no flag for it. [F]

## Summary table

| command | central-host ready? | local files it needs | breaks for a central host or remote cluster | evidence |
|---|---|---|---|---|
| init | yes | admin creds | none; buckets get 1 replica | ctl.go:270-277, admin.go:154-185, store.go:134 |
| init --local | no, by design (small setup only) | writes server dir, key dir, config.json | loopback, no TLS, may spawn local nats-server | local.go:30,102,125, start.go:66-78 |
| limits [name...] | yes | admin creds, config.json limits | none | ctl.go:327-339 |
| limits --show | yes (offline) | config only | none | ctl.go:252-258 |
| permissions | yes (offline) | config only (owner) | none | ctl.go:237-251 |
| provision | yes | admin creds | none; 3-token form needs agent_token | ctl.go:278-326, newform.go:474 |
| set-meta | yes | admin creds | none | ctl.go:340-362 |
| suspend / resume / suspended | yes | admin creds | none | ctl.go:363-389 |
| deprovision (mashctl) | yes, with local files | admin creds, key dir (operator.nk, account.nk), sys creds | needs MASH_SERVERS in a cluster; refuses on gateways; no flags for sys creds or servers | main.go:69-75, revoke.go:28-61, apply.go:61 |
| deprovision (`mash admin`) | yes, but no revoke | admin creds | prints advice only | ctl.go:28-31,467-468 |
| list | yes | admin creds | text TSV only, no JSON | ctl.go:558-565 |
| add | partly | admin creds, key dir (account.nk), sys creds, writable config dir | creds file lands on the central disk, no output flag, no delivery; sys creds forced by zone guard | add.go:222,232,260,307, zoneguard.go:22-46 |
| reissue (NAME, --all, --changed) | partly | admin creds, key dir, sys creds, every endpoint's creds file with seed, config dir | fails if creds file is not on this host; one admin and one sys connection per name; no --servers | reissue.go:431-452,480, revoke.go:76-96 |
| retire | yes, with local files | admin creds, key dir, sys creds; creds file optional | needs MASH_SERVERS in a cluster; gateways refused; renames only the local copy | retire.go:70,138,154,200-232 |
| revoke | not a command | n/a | `mashctl revoke` is "unknown command"; only internal functions | main.go:35-65, revoke.go:23,64 |
| label / unlabel | yes | admin creds | none | label.go:94-110,161 |
| unzone | yes, with local files | admin creds, key dir (read), sys creds | needs sys creds even if nothing revokes; help text says run reissue "where its creds file is" | unzone.go:12,61,79, zoneguard.go:22-46 |
| audit | yes | admin creds; reads local *.creds | orphan-creds check sees only the local config dir or one --creds-dir | audit.go:669-693 |
| inbox inspect | yes | admin creds | none | inbox_inspect.go:222 |
| adopt | yes (it is the import step) | input seeds and sys creds as files; writes key dir, `<config>/server/sys.creds`, config.json | one config dir per account; cluster hint names a flag that retire/reissue do not have | adopt.go:159,169,344-349,357,418,286 |

## Facts that apply to every command

**C1. How the target is chosen.** [F]
- `mashctl` has no flag for server, owner or admin creds (docs/operations.md:59).
- Config comes from `config.json` in the config dir, and `MASH_*` variables override it (config.go:410-428).
- Config dir: absolute `MASH_CONFIG_DIR`, else `$XDG_CONFIG_HOME/mash`, else `$HOME/.config/mash` (config.go:206-231).
- A relative `MASH_CONFIG_DIR` is an error (config.go:195-203).
- With no home and no `MASH_CONFIG_DIR`, `ErrNoConfigDir` is returned (config.go:189-191).
- A driver on a central host should set `MASH_CONFIG_DIR` and not rely on `HOME`. [I]

**C2. The connection.** [F]
- `OpenAdmin` reads and mode-checks the admin creds (ctl.go:182-189; credsmode.go:25-26 refuses group/other bits).
- It calls `natsauth.RequireTLS`, then `nats.Connect(cfg.URL, ...)` (ctl.go:191-195).
- `RequireTLS` splits `url` on commas, so `MASH_URL=tls://a:4222,tls://b:4222,tls://c:4222` is accepted (natsauth.go:181).
- Plain `nats://` to a non-loopback host is refused (natsauth.go:187-188). `insecure_loopback` is refused for a non-loopback host (natsauth.go:185-186).
- `IgnoreDiscoveredServers` is set (natsauth.go:193,195). So every server must be listed in `MASH_URL`; none is learned from the cluster.
- TLS options are one set per config: `tls_ca`, `tls_cert`, `tls_key` (ctl.go:195).
- JetStream is `jetstream.New(nc)` with no domain or API prefix (admin.go:139). No `Domain` or `APIPrefix` exists anywhere in non-test code. A leafnode or domain layout is therefore unreachable. [F]

**C3. The directory, inboxes, leases and locks all go through NATS.** [F]
- The directory is a JetStream KV bucket (admin.go:246, 296).
- The account lock is a KV key (`accountlock.go:19,50`), taken over the admin connection, and held across the whole command with a background refresh (accountlock.go:19-23).
- The name locks are KV keys too (admin.go:302-320).
- No file lock guards these. Two central hosts are safe against each other here. [I]

**C4. The one local lock.** [F]
- `<keydir>/apply.lock` is an flock that serializes writes to the account JWT (lock.go:14-26, apply/revoke.go:75).
- Its own comment says it "does nothing for copies of the keys on other hosts" (lock.go:22-23).
- docs/operations.md:298 says two operators with separate key dirs must not revoke at the same moment, or one push can erase the other.
- So the central host must be a single writer of the account JWT. [F]

**C5. The key store.** [F]
- Dir: absolute `MASH_KEYS_DIR`, else `<config dir>/keys` (ctlkeys.go:62-73).
- Files: `account.nk`, `operator.nk`, `system.nk`, `groups/` (ctlkeys.go:35-38), `operator.jwt` (ctlkeys.go:616, optional: absent gives `""`, ctlkeys.go:649-656), `apply.lock` (lock.go:14), `wranglers.json` (wranglerreg.go:32).
- The dir must be a real dir, not a symlink, mode 0700, owned by the effective uid (ctlkeys.go:92-113).
- So a key dir on a symlinked or group-readable secrets mount is refused. [I] for the mount case, [F] for the check.
- `account.nk` has two creators only: `init --local` (bootstrap.go:139,145) and `adopt` (adopt.go:344,349).
- Plain `init` never makes keys (ctl.go:270-277), yet the error for a missing account key says "run `mashctl init`" (ctlkeys.go:31). That message is wrong. [F]

**C6. System creds (`$SYS`).** [F]
- Path: `--sys-creds` flag, else `MASH_SYS_CREDS`, else `sys.creds` beside the admin creds, else `<config dir>/server/sys.creds` (syscreds.go:23-44).
- Only `unzone`, `issued check`, `issue serve` and `policy *` take `--sys-creds`. `add`, `reissue`, `retire` and `deprovision` pass `""`, so env or default path only (zoneguard.go:24, revoke.go:37,40; docs/operations.md:298 says "there are no flags for these two").
- The sys connection uses `MaxReconnects(0)`, one server, a 5 s connect timeout (syscreds.go:66, 18-19). A 25 s deadline bounds each apply (`applyDeadline`, syscreds.go:19).
- The system creds are used for: the account JWT lookup, the server ping, the claims push, and the zone-policy check.

**C7. The server count for a cluster.** [F]
- `MASH_SERVERS` is the only way for `add`, `reissue`, `retire`, `deprovision`, `unzone` (revoke.go:99-110). `--servers N` exists only on `issued check`, `issue serve` and `policy *`.
- The revoke path asks every server: the ping is `$SYS.REQ.SERVER.PING` (sys.go:135-171), the lookup is `$SYS.REQ.ACCOUNT.<a>.CLAIMS.LOOKUP` collected from all replies (sys.go:83-120), the push is `$SYS.REQ.CLAIMS.UPDATE` with acks counted (sys.go:176+).
- It works through one connection to one server. The cluster replies come back on that connection. [F] code, [I] live behaviour (a live test exists: apply/lookupall_live_test.go).
- The set is "complete" only if: no gateways reported, one cluster name, at least one reply, and replies >= max(routes reported, `MASH_SERVERS`). A named cluster with no `MASH_SERVERS` is refused (apply.go:56-67, 70-80).
- Any server reporting gateways makes it never complete (apply.go:61). So a super-cluster over five ranges refuses every revoke. [F]

**C8. Replicas.** [F]
- Inbox streams, the issue bucket and the adoption bucket are made with `Replicas: 1` (store.go:134, issued.go:160, adoption.go:88). No setting changes it. The lease bucket copies `cfg.Replicas` (lease_harden.go:96). The directory bucket sets none (store.DirectoryConfigFor).
- On a 3-server cluster the inbox is not replicated. This is not a central-host problem, but it is a cluster problem. [I] for the effect.

**C9. Accounts, owners and processes.** [F]
- ADR 0006: one NATS account for mash, one owner per deployment. `Config.Owner` is a single value, required by every verb (ctl.go:234-236, ctl.go:176).
- A mint is confined to `agents.mail.*.<owner>.*` (permissions.go:31-33).
- One mashctl process acts on one config: one URL, one owner, one admin creds, one key dir, one TLS set. It keeps no state in memory between runs and has no daemon. `StateDir` and spool are unused by mashctl (grep: none in cmd/mashctl, ctl, admin, ctlkeys, apply, mint).
- State per account on disk: the config dir (`config.json`, `NAME.creds`, `NAME.creds.next`, `NAME.creds.retired-<ts>`, `server/`), the key dir, and the creds files for admin and sys.
- To drive many accounts, run one process per call with a different env set. Nothing in the code stops that. [I]

**C10. Environment variables that change meaning on a central host.**
- `MASH_URL`: server list (comma). Central: one per account. [F config.go:410]
- `MASH_OWNER`: the owner token. Central: one per account; sets the subject scope of every minted user. `add` and `reissue` have no `--owner`; only `provision` and `deprovision` do (newform.go:456-459). [F]
- `MASH_ADMIN_CREDS`, `MASH_TLS_CA`, `MASH_TLS_CERT`, `MASH_TLS_KEY`: per account. [F]
- `MASH_KEYS_DIR`, `MASH_CONFIG_DIR`, `MASH_SYS_CREDS`, `MASH_SERVERS`: per account. [F]
- `MASH_NAME`: the endpoint's own name. If set, `config.Load` enforces a valid name, URL and owner (config.go:534-575), and `announceTarget` warns that the shell "looks like it is inside a mash session" (target.go:85-87). Leave it unset on a central host. [F]
- `MASH_AGENT_TOKEN`: if set, every verb except 3-token `provision` and `deprovision` refuses (loadcfg.go:23-25). Leave it unset. [F]
- `MASH_INSECURE_LOOPBACK`: refused for non-loopback hosts. Leave it unset. [F]
- `MASH_LAUNCH_TOKEN`, `MASH_HOST`, spool and idle settings: unused by mashctl. [F]
- `announceTarget` words its warning for a shell inside an endpoint session: "These MASH_* variables belong to the endpoint session you may be inside" (target.go:80-82). That is co-residency in the text. [F]

**C11. Output form for a driver.** [F]
- `--json` exists only on `audit`, `inbox inspect`, `issued list|check`, and `policy` verbs (ctl.go:111-112,134-135).
- `list` prints tab-separated text (ctl.go:564). `add` prints `added NAME creds PATH` (add.go:312). `reissue`, `retire`, `provision`, `suspend` print sentences.
- Exit codes: 0 ok, 1 fail, 2 usage, 3 for "not suspended" (ctl.go:216-217,386). Audit and inspect use 3 for "incomplete".

**C12. The `mash admin` twin.** [F]
- `mash admin` calls `ctl.Run("mash admin", ...)` (cmd/mash/admin.go:21). It shares: init, init --local, limits, permissions, provision, set-meta, suspend, resume, suspended, deprovision, list (ctl.go:269-565).
- It does not carry audit, add, reissue, retire, label, unzone, adopt, issued, policy (ctl.go:110-120 gate on `prog`; main.go dispatch).
- `ctl.RevokeUsers` stays nil in `mash admin` ("which has neither" key store nor sys creds, ctl.go:28-31), so deprovision removes and says "revoke its NATS user with your operator tooling" (ctl.go:467-468).
- `mash admin` loads config through the endpoint's `config.Load`, so the same C10 env rules apply.

## Per command

### init
- a. [F] Reads admin creds (ctl.go:182, 775-781). Creates directory, lease, retired, issue-record and adoption buckets over NATS (admin.go:154-185). No key store, no sys creds, no local write. Env: URL, OWNER, ADMIN_CREDS, TLS, KEYS not used.
- b. Yes. Needs only the admin creds file on the central host. Buckets are made with 1 replica (C8).
- c. One account per call (C9).
- d. None.

### init --local
- a. [F] Writes `server.conf`, `operator.jwt`, `mashadmin.creds`, `sys.creds` under `<config dir>/server` or `--dir` (local.go:54-59, 84-94). Makes `operator.nk`, `system.nk`, `account.nk` in the key dir (local.go:76-80, bootstrap.go:139-145). Writes `config.json` if none (local.go:106-111). Listens on `127.0.0.1:P`, no TLS (local.go:30, 102). `--start` runs `nats-server` from PATH and polls the port, local only (start.go:66-78). It prints "never copy them off this machine" (local.go:105).
- b. No, by design. It is the small-setup verb. A central host would use `adopt` against a server it did not start.
- c. One account.
- d. "for one developer" (ctl.go:95).

### limits [name...] and limits --show
- a. [F] `--show` is offline: prints `cfg.Limits` (ctl.go:252-258). The other form raises inbox streams and bucket caps over NATS (ctl.go:333). Limits come from `config.json` "limits" (config/limits.go).
- b. Yes. Limits live in the config dir of the account, so there is one `limits` block per config. [I]
- c. One account per call.
- d. None.

### permissions
- a. [F] Offline. Needs only the owner (ctl.go:234-251). Prints JSON or `--nsc` flags. No NATS, no files.
- b. Yes.
- c. Owner by env.
- d. A stderr note about "hand-made legacy users" (ctl.go:664-667).

### provision
- a. [F] NATS only. Takes `--user U --account A` as public keys (ctl.go:305-322). No key store, no creds file. With labels it takes the account lock and refuses an adopted account (ctl.go:294-299, 245-255). The 3-token form (`--agent A [--owner O] [--alias OWNER/NAME]`, or `agent.owner.name`) needs `agent_token` (newform.go:474-476); that is the one verb pair allowed with `MASH_AGENT_TOKEN` set (main.go:61, loadcfg.go:23-25).
- b. Yes. It is the one record verb that does not assume where the user key was made.
- c. One account per call; `--owner` lets one call name another owner in the 3-token form (newform.go:456-459).
- d. 3-token identity `agent.owner.name` (newform.go:336-349). The plan doc says mash code must not call these range, herd and lane (docs/plans/2026-11-10-naming-grammar-in-mash.md:120). Code follows that.
- Gap: no mashctl verb mints a JWT for a public key that the endpoint host made. `issue serve` does, for test accounts only (ctl.go:139). So `provision` has no partner for the central model. [F]

### set-meta, suspend, resume, suspended, list
- a. [F] NATS only (ctl.go:340-389, 558-565). `set-meta` with labels takes the account lock (ctl.go:348-354). No key store, no sys creds, no local file.
- b. Yes.
- c. One account per call.
- d. None.

### deprovision
- a. [F] `mashctl deprovision` sets `ctl.RevokeUsers` (main.go:69-75) and revokes inside the name lock (ctl.go:430-447). The revoke opens the key store (operator.nk, account.nk, optional operator.jwt), connects with sys creds (default path or `MASH_SYS_CREDS`), uses `MASH_SERVERS`, takes `apply.lock`, signs the account JWT with the operator key, pushes to all servers, reads back (revoke.go:28-61; apply/revoke.go:63-140). Then it marks the issue record revoked over NATS (ctl.go:444). It deletes no creds file. `--no-revoke` skips all of it (ctl.go:431).
- b. Yes, with local files on the central host: key dir, sys creds. Breaks: no `--servers` or `--sys-creds` flag (revoke.go:37,40; docs/operations.md:298); gateways refuse (apply.go:61); a named cluster without `MASH_SERVERS` refuses (apply.go:62-64).
- c. One account per call.
- d. None.

### add
- a. [F]
  - Config: needs a config dir that can hold `NAME.creds` (add.go:134-141, 232).
  - Keys: `account.nk` must exist (add.go:218-224).
  - Sys creds, forced: `zoneGuard` runs on every add (add.go:260). It needs the key store, sys creds, `MASH_SERVERS`, and a sys connection (zoneguard.go:22-46). So `add` with no sys creds fails even though it revokes nothing.
  - Account lock over NATS (add.go:206, guard.go:22).
  - Mint: a new user key pair is made on the central host and signed by `account.nk` (mint.go:55-74). The seed goes into the creds text (mint.go:78).
  - Order: issue record, directory record and inbox over NATS, then the creds file (add.go:274-308). A file failure rolls the record back (add.go:292-297, 305-309).
  - Output file: `<config dir>/NAME.creds`, 0600, never overwritten (add.go:134-141, 307; writeCredsNew add.go:147-181). Output text: `added NAME creds PATH` (add.go:312).
  - Delivery to the endpoint host: none in mashctl. The caller must copy the file. [F] none exists; [I] that this is the gap.
- b. Needs local files on the central host: key dir (account.nk), sys creds, admin creds, writable config dir. Breaks for a central model: (1) the output path is fixed to the config dir (add.go:232); (2) the seed is made on the central host and has to travel; (3) the central host then holds a copy of every seed, because `reissue` needs it (see reissue).
- c. One account and one owner per call. The config dir of each account is also where its endpoints' creds land, so 200 endpoints give 200 files in one dir. [I]
- d. None by name. `add` makes only legacy-form (single-name) endpoints; it has no 3-token form (add.go:197-209, 266).

### reissue (NAME..., --all, --changed)
- a. [F]
  - Reads each endpoint's creds file from the config dir, including the seed (reissue.go:431-452). If the file is absent: "no creds file for NAME: retire and add it again" (reissue.go:433). `--changed` skips it as "no creds file here" (reissue.go:109). `--all` lists it as "(it will fail)" (reissue.go:218-220).
  - Writes the new creds into the same config dir, through a temp file and rename (reissue.go:748-780), staged as `NAME.creds.next` for a rotation (reissue.go:676-679).
  - Key dir: `account.nk` (reissue.go:80, 475). Admin creds and the server: needed for the record check (reissue.go:480-483). The header comment in main.go says "needs the key directory and creds files, no server" (main.go:60). That is wrong; the code needs the server.
  - Sys creds: the zone guard once per run (reissue.go:168-177) and a revoked-key check per name (reissue.go:130, 561, revoke.go:70-96), plus the revocation on a rotation (reissue.go:638).
  - `MASH_SERVERS`: parsed per name (reissue.go:553-556) and once for `--changed` (reissue.go:99).
  - `--all` and `--changed` read the directory and keep only legacy-form records of this owner; 3-token and other-owner records are skipped (reissue.go:785-805).
  - Locks: account lock held for the whole run (reissue.go:75-79); name lock per name (reissue.go:488); `apply.lock` on a rotation.
- b. Needs local files on the central host: key dir, sys creds, admin creds, and every endpoint's creds file with its seed. If the creds files were delivered and removed, `reissue` cannot run (reissue.go:433). Cost for 200 names: it opens one admin connection per name (reissue.go:480) and one sys connection per revoked-key check (revoke.go:89, called at reissue.go:130 and 561), each with the 25 s deadline (C6). The ping and lookup settle waits add about 1 to 3 s each (sys.go:48-58). [I] for the total time. No `--servers` flag (C7).
- c. One account per call. State per account is the set of creds files in its config dir.
- d. The help text for `unzone` tells the operator to run reissue "where its creds file is" (unzone.go:12, 79). That is the co-residency assumption in plain words.
- Note: the new JWT needs only the user public key. The seed is read only to derive the key and to write it back into the creds text (mint.go:34-78, reissue.go:438-452, 568). [F] The seed is not needed to sign. [I] that a "re-sign for a recorded public key" mode is therefore possible without the seed.

### retire
- a. [F]
  - NATS: list the record, revoke inside the name lock, remove record and inbox (retire.go:101-193). Account lock (retire.go:88).
  - Key dir and sys creds and `MASH_SERVERS`, as `deprovision` (retire.go:70, 154; revoke.go:28-61).
  - The creds file is optional: it is read only to refuse a future-dated JWT (retire.go:138-149). If the file is missing, `credsIssuedClaims` returns nil, nil (revoke.go:133-135) and the issue-record check still runs (retire.go:134).
  - Then it renames the local creds file to `NAME.creds.retired-<ts>` or deletes it with `--delete-creds` (retire.go:200-232). It touches only the copy on this host.
- b. Yes, with the key dir and sys creds on the central host. Breaks: as deprovision (C7). The endpoint host's own copy of the creds is not removed; the revocation in the account JWT is what stops it. [I]
- c. One account per call.
- d. None.

### revoke
- a. [F] Not reachable: `mashctl revoke x` prints `unknown command "revoke"`; main.go has no case for it. `revoke.go` holds `revokeUserKeys`, `revokedInAccount`, `parseServers` and helpers used by deprovision, retire and reissue. docs/operations.md:173,298 still name a `revoke` verb.
- b to d. See deprovision and C4, C6, C7.

### label, unlabel
- a. [F] NATS only: list, compute, `SetMeta` (label.go:94-161). Account lock through a guard that opens its own admin connection (label.go:99, guard.go:22, ctl.go:262-279). No key store, no sys creds, no creds file. `--dry-run` writes nothing (label.go:151-154).
- b. Yes.
- c. One account per call; two connections per call.
- d. None.

### unzone
- a. [F] Takes no account lock (no `accountGuard` in unzone.go). Runs the zone guard first: key store (read), sys creds, `MASH_SERVERS` (unzone.go:61, zoneguard.go:22-46). Then `adm.Unzone` over NATS (unzone.go:65-70). It writes no key and no creds file. `--sys-creds` is accepted (unzone.go:26-30).
- b. Yes, with the key dir and sys creds on the central host. It always needs them, "there is no way to skip it" (zoneguard.go:9-10).
- c. One account per call.
- d. Its help says "run mashctl reissue NAME where its creds file is" (unzone.go:12, 79).

### audit
- a. [F] Read only. Admin creds; NATS for records, streams, leases, retired bucket (audit.go:656-662). Scans local `*.creds` and `*.creds.next` in `cfg.Dir` and the legacy dir, or in one `--creds-dir` (audit.go:669-678, 547-585). Skips the admin and sys users using `cfg.AdminCreds` and `MASH_SYS_CREDS` (audit.go:684-691). Reads the account key from the admin creds JWT (audit.go:693). No key store, no sys creds needed.
- b. Yes. A record with no local creds file is not reported; only a local creds file with no record is an orphan (audit.go:535-539). So on a central host that has delivered all creds, the orphan-creds list is empty and says nothing about endpoint hosts. [I]
- c. One account per call. `--creds-dir` takes a single dir (audit.go:610-622). `--json` and `--compare` give a driver form.
- d. The record fields `identity.agent/owner/name` and `form legacy|new` (audit.go:96-105).

### inbox inspect
- a. [F] Read only; admin creds; NATS (inbox_inspect.go:222-248). Never creates or acks a consumer. Tolerates a set `agent_token` (inbox_inspect.go:215-219). `--json` supported.
- b. Yes.
- c. One account per call.
- d. None.

### adopt (first-time takeover)
- a. [F]
  - Inputs as files: `--operator-key`, `--account-key` (seeds), `--sys-creds`, optional `--admin-creds` or `--mint-admin`, and `--server URL` (adopt.go:64-69, 110-140). Seeds are read from files only and must be 0600 (docs/operations.md:45).
  - Config: reads the loaded config for TLS and the owner; refuses a URL with userinfo (adopt.go:127-130). The URL may be a comma list; `RequireTLS` splits it (natsauth.go:181; adopt.go:217).
  - Server checks, no write: an unauthenticated probe must fail (adopt.go:221-224); a sys connection with `MaxReconnects(0)` (adopt.go:225-226); a ping of all servers; a lookup of the account JWT and its signature check (adopt.go:232-258).
  - Writes on the central host: `operator.nk` and `account.nk` into the key dir (adopt.go:159, 344-349); `<config dir>/server/sys.creds` (adopt.go:169, 357); `<config dir>/server/mashadmin.creds` for `--mint-admin` (adopt.go:170); `config.json` only if none exists (adopt.go:418).
  - Server writes: none, except `--mint-admin` records the admin user in the issue record (docs/operations.md:49).
  - The local keys are the whole point: it moves the account and operator seeds onto the host that runs mashctl. That host becomes the signing authority. [F] It does not install `operator.jwt` (no call in adopt.go); that file stays optional (ctlkeys.go:649).
- b. Yes, from a central host against a remote cluster. The cluster hint is wrong: it says "pass --servers N to `mashctl retire` and `reissue`" (adopt.go:286), but those verbs parse no such flag and reject it as unknown (retire.go:56-58, reissue.go:54-56); only `MASH_SERVERS` works. [F]
- c. One account per config dir. It writes `config.json` only when none exists, so a second account on the same host needs its own `MASH_CONFIG_DIR` and `MASH_KEYS_DIR`; otherwise a different key is refused (adopt.go:379).
- d. "the identity seed of the account mash uses" and "take over an operator-mode server you already run" (adopt.go:110-122). No herd or range wording.

## What a central host needs on disk and in the environment (minimal, per account)

On disk:
1. A config dir, absolute path, owner-only writable, set by `MASH_CONFIG_DIR`. [F] It holds `NAME.creds` for every endpoint that `add` made, plus `.next` and `.retired-*` files.
2. The admin creds file, mode 0600 (`MASH_ADMIN_CREDS`). [F] Every NATS verb needs it.
3. A key dir, mode 0700, owned by the user that runs mashctl, a real dir not a symlink (`MASH_KEYS_DIR`). [F] Needs `account.nk` for add and reissue. Needs `operator.nk` and `account.nk` for every revoking verb. `operator.jwt` is optional. `adopt` fills it.
4. The system creds file, mode 0600 (`MASH_SYS_CREDS`). [F] Needed by add, reissue, unzone, retire, deprovision (mashctl), `issued check`.
5. For reissue only: each endpoint's creds file with its seed, in the config dir. [F]
6. TLS CA file if the cluster uses a private CA (`MASH_TLS_CA`; plus cert and key for client TLS). [F]
7. Not needed: state dir, spool dir, a running local process, a local nats-server (except `init --local --start`). [F]

In the environment:
- `MASH_URL` (all servers, comma list, `tls://`), `MASH_OWNER`, `MASH_ADMIN_CREDS`, `MASH_SYS_CREDS`, `MASH_KEYS_DIR`, `MASH_CONFIG_DIR`, `MASH_TLS_CA`. [F]
- `MASH_SERVERS=N` for any cluster; required for a named cluster. [F]
- Unset: `MASH_NAME`, `MASH_AGENT_TOKEN`, `MASH_LAUNCH_TOKEN`, `MASH_INSECURE_LOOPBACK`. [F]
- One env set per account, applied per invocation. [I]
- One central host only. A second host with copies of the keys can erase a revocation (C4). [F]

## What must change for the central model (smallest first)

1. **Text fixes, no code risk.** [F] they are wrong today.
   - main.go:60: the comment "no server" for reissue is wrong (reissue.go:480-483).
   - adopt.go:286: say `MASH_SERVERS=N` or add the flag (item 3).
   - ctlkeys.go:31: "run `mashctl init`" does not create the key; point to `adopt` or `init --local`.
   - ctl.go:111-112 and docs/operations.md:173,298 name a `revoke` verb that is not dispatched.
2. **Write one recipe in docs/operations.md for a central host**: the env set above, one set per account. Cost: small. Needs no code.
3. **Flag parity.** Add `--servers N` and `--sys-creds FILE` to `add`, `reissue`, `retire`, `deprovision`, `unzone` (unzone has the second). The code path already takes the values (`connectSys(cfg, flagVal, env)`, `parseServers`); the verbs pass `""` or read env only. Cost: small.
4. **Explicit output path for creds.** Add `--creds-out FILE` (and a matching `--creds FILE` for reissue and retire) so a driver names where the secret lands, for example a tmpfs path, and removes it after delivery. Today the path is fixed by `credsFileFor` (add.go:134-141). Cost: small.
5. **JSON out for the write verbs.** `add`, `reissue`, `retire`, `provision`, `set-meta`, `suspend`, `resume`, `deprovision`, `list` need `--json` with fields such as name, user public key, creds path, servers acked, and exit code. Today they print sentences (C11). Cost: small to medium, mostly per-verb output code.
6. **Make bulk reissue reuse connections.** One admin connection and one sys connection per run, not per name (reissue.go:480, revoke.go:89). Cost: small to medium. Matters at 200 names.
7. **Mint for a public key (no seed on the central host).**
   - `add --user PUB` and a `reissue` mode that re-signs the JWT for the key the directory record names, writing a JWT and no seed. The signer needs only the public key (mint.go:34-78).
   - This removes the seed escrow and the need to ship a secret. It also lets `reissue` run with no creds file on the central host (reissue.go:431-452 is the only place that needs it).
   - Cost: medium. A rotation still needs the staged-file logic (reissue.go:604-666), now keyed by public key.
   - It is the change that answers Randy's criticism directly. Without it, `add` and `reissue` require the key store, the seed and the creds file to be together.
8. **Cross-host safety for the account JWT write.** Replace or back the local `apply.lock` with a NATS KV lock, like `accountlock.go`, around the read-modify-write of the account JWT (lock.go:22-23; docs/operations.md:298). Cost: medium. Needed only if more than one operator host will ever run revokes.
9. **Super-cluster and domains.** Let `ServerSet.Complete` accept an explicit server list per cluster instead of refusing on gateways (apply.go:61), and add a JetStream domain or prefix option (admin.go:139). Cost: medium to large.
10. **Account selection and multi-account.** Add `--config-dir`, `--owner` on `add` and `reissue`, or a single `--account-profile`. Env already works for one account per process (C9). One process for many accounts is large and not needed if the driver loops. Cost: large; low value.
11. **Replicas.** A setting for the replica count of inbox streams and buckets (C8). Cost: small to medium; a cluster concern, not a co-residency one.
12. **Audit with several creds dirs.** `--creds-dir` repeatable, and a note that a record with no local creds is not a finding (audit.go:535-539, 610-622). Cost: small.

## Homerange-shaped items found (quotes)

- `mashctl policy ... --range RANGE`, and the bucket pattern: "the policy bucket is named by the MASH_POLICY_BUCKET setting, a bucket name that may contain {range}" (cmd/mashctl/policy.go:23; policy_listset.go:78-89). Outside the audited verbs. [F]
- docs/operations.md:269: "the range's policy bucket (`homerange_<range>_policy`)". [F]
- internal/policydoc/decode.go:1: "the one value per account that homerange keeps and mash applies". [F]
- 3-token identity `agent.owner.name` (newform.go:336-349); the plan doc maps the tokens to range, herd and lane outside mash (docs/plans/2026-11-10-naming-grammar-in-mash.md:120-124). [F]
- The model "one owner per deployment, one account" (ADR 0006) puts the herd-level scope in `owner`, and every mint is confined to its owner (permissions.go:31-33). With 200+ herds as owners in one account, the `owner` is set per process by `MASH_OWNER`, and `add` has no `--owner`. [I] for the herd mapping; [F] for the single owner per call.
- No file in cmd/mashctl, internal/ctl, internal/admin, internal/ctlkeys, internal/apply or internal/mint names homerange, herd or range outside the policy items above (grep, non-test code). [F]
