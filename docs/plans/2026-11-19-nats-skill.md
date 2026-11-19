# A skill and a script to set up NATS for mash (0.5.6 design)

Status: draft for the lead and Randy, 2026-11-19. Design and draft only;
no product code. The skill and the script are drafts in this PR and are
not wired into mashctl or the plugin.

## Why

Randy, on the 0.5.5 walkthrough video (point 1): mashctl must not install
NATS. A fixed path like `/demo` at the root is "bad manners", an
environment variable for the location is "not ideal", and mashctl "should
be doing none of this". He wants "helper scripts, or preferably a markdown
skill for an agent to follow instructions and set it up per the
requirements."

Today `mashctl init --local [--start]` makes keys, a CA and server
certificate, `server.conf`, and starts `nats-server`. Only that verb does
(mash-core's facts, bogworks/mash#820, Part 2).

## The split

| Who | Does |
|---|---|
| The skill (a person or an agent follows it) | Asks the questions, installs tools with the person's consent, runs the script, checks, explains. |
| `scripts/nats-local-setup.sh` | Makes the CA and certificate, the operator, system account and mash account, the users, `server.conf`; optionally starts or stops the server. Uses `openssl`, `nsc`, `nats-server`. Touches only the directory it is given. Does not use mashctl. |
| mashctl | Takes over a server that exists: `adopt`, `init` (buckets), then endpoints. It never installs, configures or starts NATS. |

Mash stands alone: nothing here depends on homerange. Homerange can call
the same script or follow the same skill.

## (a) The skill

File: `docs/skills/mash-nats-local/SKILL.md` (draft in this PR). Content:

- What you end up with, with a diagram, and why each piece exists, in
  the plain words Randy asked for (key, seed, account, resolver).
- Ten questions, each with what the answer changes. The ones that matter:
  server directory (ask; never default to a root path; offer a directory
  under the person's own home only as a suggestion), port (ask; check it
  is free), local only or other machines (changes `--listen` and the
  certificate names, and means the CA file must be copied to clients),
  existing server or new, owner name, disk and size, started now or as a
  service.
- Tool installation: the skill shows the official install pages and the
  person's package manager and installs nothing without a yes. mashctl and
  the script install nothing.
- Steps: check tools, run the script, check it, `mashctl adopt` (dry run,
  then for real), `mashctl init`, two endpoints (Alice and Bob), a message.
- An existing-server path: the checklist, then `mashctl adopt`.
- Stop and remove, by recorded pid and exact path only.
- Do-nots: no root, no `/`, no secrets in messages, no kill by pattern.

Where it ships is open (see Questions): in the repo's docs now; the
candidates for 0.5.6 are `docs/skills/` only, or also a skill inside the
mash plugin.

## (b) The script

File: `scripts/nats-local-setup.sh` (draft in this PR). Interface:

```
nats-local-setup.sh --dir DIR [--port N] [--host NAME]... [--listen ADDR]
                    [--account NAME] [--operator NAME] [--js-disk SIZE]
                    [--start]
nats-local-setup.sh --dir DIR --check
nats-local-setup.sh --dir DIR --stop
```

Rules, each from Randy's point 1 or the standing orders:

- `--dir` is required. No default location, no environment variable for
  it. It must be absent or empty, not `/`, and have at least three path
  components.
- Everything lives under `DIR` (0700). nsc's own store is under
  `DIR/nsc`; the person's `~/.nsc` and `~/.config/nats` are untouched.
- It prints a line per step and never a secret.
- `--stop` kills only the pid it recorded, and only after `ps -p PID -o
  args=` shows `nats-server -c DIR/server.conf`. No pattern kills.
- It refuses, and changes nothing, when the port is taken.

What it writes (same shape as `init --local` today, so mashctl and
the docs keep working):

| Item | Today's `init --local` | The script |
|---|---|---|
| CA | ECDSA P-256, "mash local CA", 10 years | same, with `openssl` |
| Server certificate | SANs localhost, 127.0.0.1, ::1; 10 years | same, plus each `--host` |
| Operator, SYS, mash account | made in Go; operator "mash-local-operator", account `MASH` | made with `nsc`; operator name and account name are flags |
| Account limits | 2 GiB disk, 64 streams, 128 consumers, no memory | same |
| `server.conf` | loopback, TLS, operator, full resolver preloaded, JetStream file store | same; `--listen` changes the address |
| Users | `mashadmin` (full access in the account), `sys` | same |
| Files for mashctl | keys in mashctl's key store, `sys.creds`, `mashadmin.creds`, `config.json` | `DIR/export/` holds `operator.nk`, `account.nk`, `sys.creds`, `mashadmin.creds`; `mashctl adopt` puts them in the key store and writes `config.json` |

Test status: see "Evidence" below.

## (c) What mashctl does against that server

`mashctl adopt` already does this and is built for it (read-only checks,
then writes keys to the key store, `sys.creds`, `config.json`; optional
`--mint-admin`). Per mash-ctl (mail #3) and mash-core's boundary list:

mashctl keeps: `adopt`, `init` (the five buckets), `limits`,
`permissions`, `endpoint` create/reissue/delete/list/show, `label`,
`unlabel`, `set-meta`, `suspend`, `resume`, `audit`, `issued`,
`inbox inspect`.

mashctl drops: `init --local` and `--start` (CA, certificate,
`server.conf`, start). `internal/bootstrap` is called only from
`runInitLocal`; removing the verb removes the package, except
`bootstrap.SysCreds`, `bootstrap.AdminCreds` and `MintAdminCreds`, which
`adopt` and the sys-creds default use and which move to a small shared
place.

Follow-up changes for the implementing PR (not in this one):

- Error and usage texts that point at `init --local`
  (`ctlkeys.go:26,30`, `syscreds.go`, `revoke.go`, `ctl.go:117`,
  `docs/operations.md`) say "see the NATS setup skill" or `adopt`.
- `nats-local.md` in #817 (mash-ctl) moves its path A from `init --local`
  to the script, and links the skill.
- `internal/testenv` and any test that calls `init --local` build their
  server another way (they should not need the verb).
- Removing `init --local` departs from the decisions that introduced it;
  that PR carries its ADR.

What mashctl needs from the server (the contract the script meets):
operator mode with a full resolver preloaded with SYS and the mash
account; JetStream file store; account limits enough for `endpoints + 5`
streams; TLS on the client port with a CA the clients trust; sys creds;
admin creds; the operator seed (SO) and the account identity seed
(SA) as 0600 files.

## Randy's other points that touch this

- Point 2, 6, 8, 15: the skill uses Alice and Bob, one named host and one
  group (`demo`), and no one's real name. The owner is asked, not
  assumed; the example owner in the text is a placeholder.
- Point 4: lines in the skill and plan wrap at 80 columns where the text
  is prose; tables and commands are exempt only where wrapping breaks
  them.
- Point 3 (seed or private key): the skill says "seed" because NATS and
  mashctl say seed, and explains it once as a private key.

## Questions

1. Where the skill ships: `docs/skills/` only, or also as a skill in the
   mash plugin so an agent finds it after install? Recommendation: both,
   one source file. This needs a decision from the lead.
2. Should `mashctl adopt` print "see the NATS setup skill" when no server
   is reachable, so a newcomer finds it? Recommendation: yes, one line.
3. nsc is a new prerequisite (for the script, not for mash). The
   alternative is to generate the JWTs with `nk`/`nats` only, which is
   harder to read. Recommendation: use nsc; it is the NATS project's own
   tool and what operators already use.
4. A systemd or launchd unit for long-running use: out of scope for the
   script; the skill asks and writes one on request.

## Evidence

Run 2026-11-19 in a throwaway rootless podman container (alpine 3.20,
openssl 3.3.7, nsc 2.15.0, nats-server 2.15.1), destroyed afterwards; the
script was not run on the host. Passed: create with `--start`; no write
outside `--dir` (HOME stayed empty); `mashctl adopt --dry-run`, `adopt`,
`init`; two `endpoint create --make-key`; `mash send` design to review,
read as `[verified]`; `endpoint delete` revoked a key (sys creds work);
`--check`; `--stop` (refuses a pid that is not our nats-server); refusals
for no `--dir`, non-empty dir, `/`, shallow path, `..`, taken port.
Findings: `operator.nk` is the operator identity seed (no signing key
needed); strict signing keys left off; adopt is run with `--admin-creds`,
not `--mint-admin`; nsc ignores NSC_HOME, so the script sets XDG dirs.
Not verified: host openssl 4.x, IPv6 `--host`, non-default
`--operator/--account/--js-disk`, `--listen 0.0.0.0` with a start, strict
signing, macOS. The script is bash only (`/dev/tcp`, procps `ps -p`).
A create that fails partway leaves a non-empty dir to clear by hand.

## Risks

- A second implementation of server setup: `init --local` and the script
  disagree until the verb is removed. Mitigation: the removal and the
  script's wiring land together; this PR is held.
- The 0.5.5 CLI promised one command. The skill is more steps for the
  newcomer; the questions and the explanations are the price Randy chose.

## Update 2026-11-19 (lead #4, #5)

Skill ships in the plugin too (one source file, wiring in the implementing PR). `mashctl adopt` prints a one-line hint naming what to run next when no server is reachable. nsc is a prerequisite for 0.5.6; the skill shows how to install it. A later mashctl could create the nsc objects itself (operator, accounts, users) but not the CA, config or process; not planned. Script and skill lines are <= 80 columns; the script ends by printing the files it left and the adopt command. Second test run (podman): IPv6 --host and --listen ::1, non-default --operator/--account/--js-disk passed; macOS not run.

## Update 2026-11-19 (DRAFT4: reviews and security rev3)

The script changed shape; the interface block above is superseded.

```
nats-local-setup.sh [create] --dir DIR --owner NAME [options]
nats-local-setup.sh --dir DIR --start | --stop | --check | --upgrade
nats-local-setup.sh --dir DIR --backup DEST | --retire-keys DEST
nats-local-setup.sh --dir DIR --enable-boot [--linger]
nats-local-setup.sh --dir DIR --disable-boot | --remove
```

- Linux x86-64, Debian or Ubuntu only; other platforms stop with a message.
- It installs nats-server v2.15.1 and nsc v2.15.0 itself, into `DIR/bin`,
  from pinned URLs with digests written in the script, and runs only those
  two paths (never a copy found on PATH). The skill no longer installs them.
- `DIR` is made exclusively (0700); every path component is checked
  (real directory, yours or root's, not group- or world-writable).
  Characters that mean something in `server.conf` are refused in `--dir`.
- A listen address other than loopback needs the person to type it at a
  terminal. There is no `--yes-expose`. `--retire-keys`, `--enable-boot`,
  `--disable-boot` and `--remove` also need a typed confirmation.
- Every subprocess's output is redacted before it is shown or logged; a
  canary-secret test covers each path.
- `--retire-keys DEST` moves `ca.key`, `nsc/` and `export/` into one
  0600 archive, checks it lists back, then removes them from `DIR`. The
  server keeps `server.pem`, `server.key`, `ca.pem`.
- Lifecycle: start, stop, check, upgrade, backup, boot, remove: one
  command each, each tested in a rootless podman container (boot under
  systemd as PID 1).
- mash and mashctl are verified with `gh attestation verify --bundle
  mash.intoto.jsonl` (works with no login), not with SHA256SUMS fetched
  beside the binaries.

Open dependencies (not in this PR): v0.5.5's release has no `mashctl`
asset, and `release.yml` does not publish one yet; the guide assumes
`mashctl-linux-amd64` is built and attested in the 0.5.6 release. The
guide also assumes 0.5.6 `mashctl adopt` copies `mashadmin.creds` into
its own folder (today it records the path: "the file is not copied");
the guide's verify-then-retire block fails safe if it does not. The
script's raw URL is pinned to a commit on this branch; the pin moves to
the merge commit when this lands.

Tests: `scripts/nats-local-setup_test.sh` (the helper, 194 cases, podman)
and `scripts/mash-nats-local-guide_test.sh` (every code block of the guide
run as its own `bash -c` in a fresh Debian container).

## DRAFT5 update (2026-11-19)

Sources: lead mails #10 (junior read on DRAFT4, 8 items) and #11
(security reread rev4: S1-S10 and the custody truth); ctl mail #12/#13
(adopt target: `--ca`, `--system-credentials`, `--admin-credentials`,
`copied` lines, fingerprint typed by the person, `--dry-run` does not
prompt, `MASH_ENDPOINT`).

Guide changes:

- Custody truth stated plainly: mashctl's folder keeps the operator and
  account seeds and the system and admin credentials, because it signs
  every agent with them; modes; what a holder can do; excluded from
  backups and sync by name; a shared host or cross-machine use needs
  signing elsewhere. Only the CA key and nsc's store move off.
- Part 6 no longer backs up or restores mashctl's folder.
- One install operation in one private 0700 folder (S5): pinned `gh`,
  verify both programs against the bundle, install, verify the installed
  copies again, move them to `rejected/` on failure. `go install` and
  the plugin route are named as unverified developer routes.
- Alice and Bob use `MASH_ENDPOINT` (S6); no `MASH_TLS_CA` in typed
  commands (adopt copies the CA into config).
- adopt (real) is a person block; the agent hands the exact command out
  of its session (S1).
- No `exit` in any block; expected output or its key lines for every
  step; a table of FAIL lines with verified fixes.
- Names written where each of `--backup`, `--retire-keys` and
  `--enable-boot` writes.

Helper v5 (S2 inode recheck, S3 server.log through the diagnostics
filter, S7 refuse root, S9 compare archive to sources before
`--retire-keys`), and a closing output aligned to ctl's adopt flags, with
the CA sha256 fingerprint printed.

Known limits, stated in the guide or the PR: no mashctl release asset
exists yet and v0.5.6 attestation is untested; adopt's copy behaviour is
ctl's DRAFT10 target; the same-user race is a residual; the systemd
journal copy of `server.log` is unfiltered; reboot, Ubuntu and
`enable-linger` as an ordinary user are untested.
