# Changelog

All notable user-facing changes to mash are recorded here. The format follows [Keep a Changelog](https://keepachangelog.com/en/1.1.0/), and mash follows [Semantic Versioning](https://semver.org/) (pre-1.0: minor releases may change behaviour).

## [Unreleased]

## [0.5.6] - 2026-11-21

### Summary

**mash 0.5.6 is a breaking change for everyone who used 0.5.5: the settings that name your agent and your server have new names, the commands you typed to set mash up are different, and mail that is not proven to come from its sender is now refused by default. Read Upgrading before you install it.** Nothing on your server needs to change, and a small script moves your 0.5.5 configuration folder across. If you have never used mash, this is the release to start with: the quick start below takes you from nothing to two endpoints that mail each other from a shell, and shows how to start two Claude Code sessions on them. Every download has a build attestation; verify it.

mash lets Claude Code sessions send each other mail. Each session is an *agent* with an address of three parts, `owner.group.name` (for example `alice.dev.design`); mash calls that agent's place on the server an *endpoint*. `mash` is the command each session runs. `mashctl` is the command whoever runs your mesh (the set of agents that can reach each other) uses to set it up.

### Why we changed so much

0.5.5 fixed how agents are named, and then we looked honestly at what it felt like to use. Setting mash up took commands that overlapped (`adopt`, `init --local`, `provision` and `endpoint create` all made something); the same setting had three names (`MASH_OWNER`, `MASH_GROUP`, `MASH_NAME`); the same failure exited with a different number from each command; and output was written for the program that made it, not the person reading it. None of that was a deliberate design. It grew. 0.5.6 gives every command one job and one clear way to say it, written down in a specification that the commands are now built from ([ADR 0147](https://github.com/bogworks/mash/blob/v0.5.6/docs/decisions/0147-the-0-5-6-command-line-and-mcp-surface-is-built-from-a-specification-that-supersedes-contract-revision-26.md)). We chose to cut the old forms rather than carry them: there are no aliases and no deprecation period, because keeping both would have meant two ways of doing everything for good.

Refusing unverified mail is the other change of heart. A message is *verified* when it is signed by the key of the endpoint it names. Until now mash delivered everything and labelled it. A label is easy to miss, and an agent may act on what it reads, so the safe default is now to deliver only mail that is proven. You can turn the old behaviour back on (see Upgrading).

0.5.5 stays the version to use for running deployments until you have moved them. 0.5.0 reads nothing 0.5.6 makes; if you are still on 0.5.0, start a fresh deployment.

### Upgrading

**If you have never used mash:** nothing to move. Follow the quick start.

**If you used 0.5.5:** the order matters. Nothing on the server needs migrating: 0.5.6 reuses the stores and endpoint records 0.5.5 made, and 0.5.5 and 0.5.6 endpoints can mail each other on one server, as long as each keeps its own configuration folder.

1. **Install 0.5.6.** Use the install line in the quick start. Install `mash` and `mashctl` from the same release.
2. **Move your configuration folder.** A folder that 0.5.5 left (its `config.json` has a `url` or `tls_ca` key) is refused by every `mash` and `mashctl` command that reads the configuration (`--help` and `version` answer before it loads) with `error: <folder> holds a mash 0.5.5 configuration` and a hint to a script. Run it once: `curl -fsSL https://github.com/bogworks/mash/releases/latest/download/mash-migrate-0.5.5.sh | bash`. It renames `url` to `servers` and `tls_ca` to `ca`, keeps your old file beside it as `config.json.0.5.5`, and touches nothing on the server. mash does not read the old keys itself. **0.5.5 tools cannot use the migrated folder** (they stop with `no NATS url`), so a 0.5.5 and a 0.5.6 endpoint on one server need separate configuration folders; to go back to 0.5.5, put `config.json.0.5.5` back as `config.json`.
3. **Rename your settings.** Use the table below. In short: `MASH_ENDPOINT=alice.dev.design` replaces `MASH_OWNER`, `MASH_GROUP` and `MASH_NAME`; `MASH_SERVER` replaces `MASH_URL`; `MASH_CA` replaces `MASH_TLS_CA`. The old names are no longer read.
4. **Start each session with `MASH_ENDPOINT`.** A session is an agent only when `MASH_ENDPOINT`, `--endpoint` or `config.json` names it. `mash serve` and `mash hook` no longer pick the one installed endpoint for themselves; a session with none named is inert and `mash status` says so. The commands you type at a shell still use the one installed endpoint when there is only one.
5. **Update scripts that call `mashctl`.** Setup is `mashctl init --config FILE`; endpoints are `mashctl endpoint ...`. The old top-level commands are gone (table below). `mashctl` no longer installs or starts NATS: if you want a server on this machine, run `mash-local-nats-setup.sh` from the release, which writes the file `init --config` reads.
6. **Update scripts that read mash output or exit codes.** Exit codes are 0 (it worked), 1 (it failed) and 2 (the command line was wrong). `--verbose` and `-v` are now `--detail`. `mash who --json` is `{"endpoints": [...]}`. `mash status` is one line. `mash inbox` and `mash read` print new tables and labelled lines, and the MCP `read` tool's argument is `number`, not `seq`. The MCP `send` tool's parameters are `address`, `message`, `kind`, `urgent`, `reply_to`, `relay` and `id`.
7. **Decide about unverified mail.** It is now refused, and the sender is not told. If you depend on receiving it, set `MASH_ACCEPT_UNVERIFIED=1` (or `accept_unverified` in `config.json`), or start `mash serve --accept-unverified`.
8. **Turn encryption off only on purpose.** If you set `MASH_INSECURE_PLAINTEXT` or `insecure_plaintext`, mash now refuses it and names the new variable, `MASH_DANGEROUSLY_SKIP_NATS_ENCRYPTION=1`. It is an environment variable only, and every command warns while it is set.
9. **Make each machine's endpoints with `mash join`.** `mash credential create` is gone. `mashctl endpoint create ... --out FILE.join` now writes the file that `mash join` installs; see the quick start.

**If you used 0.5.0:** 0.5.0 state is not read, and 0.5.5 and 0.5.6 do not convert it. Start a new deployment with the quick start, name every agent in three parts (`owner.group.name`; there is no default group), and create each agent again. Read any mail you still need in the old inboxes first: it is not carried over. Do not install 0.5.6 over a running 0.5.0 endpoint, including with `go install ...@latest`.

#### What changed, old to new

Every removed or renamed command, setting and form. A removed command prints a message that names its replacement, or is an unknown command (exit 2); none is an alias.

| Old | New |
| --- | --- |
| `MASH_OWNER`, `MASH_GROUP`, `MASH_NAME` | `MASH_ENDPOINT`, the full address `owner.group.name` (also `--endpoint`, and `endpoint` in `config.json`) |
| `MASH_URL`, `MASH_SERVERS` | `MASH_SERVER`, a comma list (also `--server`) |
| `MASH_TLS_CA` | `MASH_CA` (also `--ca`) |
| `config.json` keys `url`, `tls_ca` | `servers` (a list), `ca` |
| `config.json` keys `owner`, `group`, `name` | `endpoint` (the full address). `owner` remains only as a `mashctl` setting |
| `MASH_INSECURE_PLAINTEXT`, `insecure_plaintext` | `MASH_DANGEROUSLY_SKIP_NATS_ENCRYPTION=1`: environment only, no flag and no config key, with a warning on stderr from every command while it is set |
| `mashctl adopt`, `mashctl init --local`, `mashctl init --start` | `mashctl init --config FILE` (the setup file; `mash-local-nats-setup.sh` writes one for a server on this machine) |
| `mashctl provision`, `deprovision`, `list` | `mashctl endpoint create`, `endpoint delete`, `endpoint list` |
| `mashctl add`, `reissue`, `retire` (top level) | `mashctl endpoint create`, `endpoint reissue`, `endpoint delete`; each prints the replacement |
| `mashctl set-meta`, `label`, `unlabel` | `mashctl endpoint set`, `endpoint unset` |
| `mashctl suspend`, `resume`, `permissions` (top level), `suspended` | `mashctl endpoint suspend`, `endpoint resume`, `endpoint permissions`; `endpoint show` says whether an endpoint is suspended |
| `mashctl audit`, `issued list`, `issued check` | `mashctl doctor`; one endpoint's records are in `mashctl endpoint show` |
| `mashctl inbox inspect` | `mashctl endpoint inbox` |
| `mash credential create` | `mash join FILE.join` (`--key bob.seed` when the endpoint made its own key) |
| `mash admin` (11 commands) | `mashctl` (they need an administrator's credentials) |
| `mash plugin export` | the plugin's marketplace: `claude plugin marketplace add bogworks/mash`, then `claude plugin install mash@mash` |
| `acme_demo_bob` and `acme/demo/bob` address forms | `acme.demo.bob` only, in `mash send`, `mash prompt`, `mash who` and `mashctl endpoint ...` |
| Exit codes: failures exited 2, 3 or 6, depending on the command | 0 (it worked), 1 (it failed) and 2 (the command line was wrong) only |
| `--verbose`, `-v` | `--detail` (an unknown option, exit 2, if you still pass the old one) |
| `mash who --match`, `--name`, `--owner`, `--cap`, `--long`, `--names`, `--format` | a prefix (`mash who acme.demo`), `--role`, `--capability`, `--label`, `--wide`, `--json` |
| `mash send TO TEXT...` with several words, MCP `send` parameters `to`, `text`, `in_reply_to`, `via` | one quoted message (or `--file`, or standard input); `address`, `message`, `reply_to`, `relay` |
| MCP `read` argument `seq` | `number` |
| The old `mash status` rows (config, creds, connection, channel) | `mash status` (one line) and `mash doctor` (one row per check) |
| Unverified mail delivered with a label | refused by default; `--accept-unverified` or `MASH_ACCEPT_UNVERIFIED=1` restores it |
| `mash serve` run by hand in a terminal | refused: `mash serve is started by an agent host, not by hand` (exit 2) |

### Quick start: your first agent

On one machine. You need Claude Code, `openssl`, and `nats-server` (2.10.7 or newer) with the `nats` command; `mash-local-nats-setup.sh` names anything that is missing and says where to get it.

```sh
curl -fsSL https://github.com/bogworks/mash/releases/latest/download/install.sh | bash
curl -fsSL https://github.com/bogworks/mash/releases/latest/download/mash-local-nats-setup.sh | bash
mashctl init --config ~/mash-nats/mash-setup.yaml     # the last line the setup script printed, with the path it used
```

`install.sh` installs `mash` and `mashctl` from the same release into `~/.local/bin` (or the folder `MASH_INSTALL_DIR` names) and says which release it fetched: `Installed mash v0.5.6 in ~/.local/bin`. The setup script makes a NATS server that only this machine can reach, with encryption on, and asks one question, the owner's name (here, `alice`). It does not start at boot; run it again with `--start` after a reboot.

Make two agents, `alice.dev.design` and `alice.dev.review`. The group (`dev`) has no default: you always name it.

```sh
mashctl endpoint create alice.dev.design
mashctl endpoint create alice.dev.review
```

With no option, `mashctl endpoint create` makes the agent's key on this machine and installs it here, ready to use. To give an agent to another machine, write a join file and install it there with `mash join`:

```sh
mashctl endpoint create alice.dev.bob --out alice.dev.bob.join      # on the mashctl machine
mash join alice.dev.bob.join                                        # on the other machine: prints "Joined as alice.dev.bob."
```

If the private key must never leave the agent's machine, the agent makes its own first, and only the public half travels:

```sh
mash key create --out bob.seed                                                        # writes bob.seed and bob.seed.pub
mashctl endpoint create alice.dev.bob --out alice.dev.bob.join --public-key bob.seed.pub
mash join alice.dev.bob.join --key bob.seed
```

Install the plugin and start two sessions. What was proven live for this release is two endpoints mailing each other from the shell (the next block); the Claude Code session step here was checked in the plugin's structure and configuration, not in a live two-session run:

```sh
claude plugin marketplace add bogworks/mash
claude plugin install mash@mash
MASH_ENDPOINT=alice.dev.design claude --dangerously-load-development-channels plugin:mash@mash   # terminal 1
MASH_ENDPOINT=alice.dev.review claude --dangerously-load-development-channels plugin:mash@mash   # terminal 2
```


Send mail from a shell and read it:

```sh
MASH_ENDPOINT=alice.dev.design mash send alice.dev.review "hello"     # prints: Mail sent (number 1).
MASH_ENDPOINT=alice.dev.review mash inbox
MASH_ENDPOINT=alice.dev.review mash read 1
MASH_ENDPOINT=alice.dev.review mash status                            # alice.dev.review: ready, offline
```

The number in `Mail sent (number N).` is the message's place in the recipient's inbox. The recipient's `mash read` takes it, and an answer arrives marked `reply to: N`. `mash status` says `ready, online` while an agent session holds the endpoint. `mash doctor` lists each check and says how to fix what failed.

### Not in 0.5.6

These were looked at and left out, so you do not wait for them. None has a date.

- **Wake on reply, deadline wake and peer state.** A session is woken by a backlog (see Added), not yet when a reply arrives, when a deadline passes or when a peer comes or goes ([#871](https://github.com/bogworks/mash/issues/871)).
- **A trust record, generations and `mash trust reset` and `replace`** ([#862](https://github.com/bogworks/mash/issues/862)).
- **`mash plugin install`,** and verification of the marketplace plugin's bytes ([#863](https://github.com/bogworks/mash/issues/863)).
- **Stand-alone NATS setup guides.** The local script is in this release; guides for a local server and for Synadia Cloud come in 0.6.0 ([#1037](https://github.com/bogworks/mash/issues/1037)).
- **Synadia Cloud and hosted NATS without the operator key** are outside `mashctl` ([#876](https://github.com/bogworks/mash/issues/876)).
- **A structured list from `mash who` and `endpoint list` in the MCP tools,** until MCP revision 2026-07-28 ([#877](https://github.com/bogworks/mash/issues/877)).

### Planned for 0.6.0

Breaking changes we already know about, so you can plan. None has a date, and none is in 0.5.6.

- **`mash prompt` and the `reply` tool move to `mash sap`.** A live prompt is part of the Synadia Agent Protocol, not everyday mail. 0.5.6 leaves `mash prompt` as it is ([#881](https://github.com/bogworks/mash/issues/881)).
- **The endpoint directory and NATS service subjects become owner-scoped.** Today any endpoint can read every owner's directory entries ([#1035](https://github.com/bogworks/mash/issues/1035)) and use the NATS service subjects ([#1012](https://github.com/bogworks/mash/issues/1012)). Scoping them to their owner may stop something that works today across owners.
- **A mesh holds many owners.** `mashctl owner create`, `list` and `delete` arrive, and mail between owners becomes a designed case.
- **Policy: who may send to whom.** mash ships open; narrowing will be a choice.
- **Asking to join.** An endpoint will be able to ask to join and wait for approval, by an administrator or by policy.
- **How a session is woken.** We will look at the MCP Events extension. Channels stay until then.
- **Telling a suspended agent from a deleted one.** Today a send to either says `is suspended or was deleted`.

### Known issues

Each has an issue; the issue is the place for detail and any workaround. Listed by what you would notice.

Mail and trust:

- [#874](https://github.com/bogworks/mash/issues/874): refused unverified mail: the sender is not told, and the mail is still stored.
- [#935](https://github.com/bogworks/mash/issues/935): an administrator cannot see which messages an endpoint refused.
- [#958](https://github.com/bogworks/mash/issues/958): `mash inbox` may count unverified mail that `serve` has not yet refused.
- [#990](https://github.com/bogworks/mash/issues/990): `mash read` at a shell can say "refused" for mail `serve` delivered under `--accept-unverified`.
- [#960](https://github.com/bogworks/mash/issues/960): a signed message replayed within 5 minutes shows as verified a second time (no workaround).
- [#883](https://github.com/bogworks/mash/issues/883): `Mash-Kind` is unsigned.
- [#866](https://github.com/bogworks/mash/issues/866): `mash read` from a shell with no session does not tell the server until the next session starts.
- [#955](https://github.com/bogworks/mash/issues/955): `mash inbox` counts urgent mail among only the newest 200 messages.
- [#1070](https://github.com/bogworks/mash/issues/1070): `mash inbox` gets slower with the span of the mailbox's messages, not the size of the page.
- [#1069](https://github.com/bogworks/mash/issues/1069): `mash doctor`'s refused-mail count can stay one low after `serve` is killed mid-refusal.
- [#1054](https://github.com/bogworks/mash/issues/1054): `mash inbox` and the wake still count older waiting mail from a deleted sender that the list leaves out.
- [#918](https://github.com/bogworks/mash/issues/918): `mashctl`'s online (holds the lease) and `mash who`'s online (heartbeat) can disagree for up to 90 seconds.
- [#1039](https://github.com/bogworks/mash/issues/1039): mash cannot discover endpoints served by a SAP SDK host; the fix is planned on mash's side after 0.5.6.

Setting up and installing:

- [#878](https://github.com/bogworks/mash/issues/878): one config folder serves one mesh; a second mesh needs `--config-dir`.
- [#965](https://github.com/bogworks/mash/issues/965): `mashctl init --config` refuses a folder set up for a different server.
- [#1065](https://github.com/bogworks/mash/issues/1065): `mashctl init` prints an unknown setup key in its error.
- [#865](https://github.com/bogworks/mash/issues/865): the NATS helper has no boot start, upgrade, backup, other-machine listening or key retirement.
- [#995](https://github.com/bogworks/mash/issues/995): `mash-local-nats-setup.sh --remove` refuses when a later instance took a stopped folder's recorded port.
- [#1093](https://github.com/bogworks/mash/issues/1093): `mash-local-nats-setup.sh`: after a run was killed and left its lock behind, two runs started at the same moment can both take the lock, and both then start, stop or remove the same local server. Someone who runs two of `--start`, `--stop` and `--remove` at once, just after a killed run, meets this; both runs are by the same user. Workaround: run one lifecycle command at a time; after a killed run, run `--stop`, then `--start`, one after the other.
- [#1094](https://github.com/bogworks/mash/issues/1094): `mash-local-nats-setup.sh --start` says "already running" whenever the recorded server process is alive, without checking that the server answers, so a server that is up but not serving (one stuck while starting, for example) is reported as running. Someone whose local server process is up but not serving, who runs `--start` to bring it back, meets this. Workaround: run `mashctl status`; if it is not ok, run the script with `--stop`; if that says it did not stop, run `kill -KILL` on the process number in `<dir>/server.pid`, then `--stop`, then `--start`, then `mashctl status` again (it says ok).
- [#1099](https://github.com/bogworks/mash/issues/1099): a failing `mash doctor` ends `1 problem.` with no fix, though its help says it tells you how to fix what is wrong, and some hints point at a command that gives the same answer: `mash status` hints `mash doctor`, and `mashctl doctor` on a down server hints `mashctl status`, which only checks. Anyone who follows the hints after a failure meets this, and an agent that follows every hint can loop. No workaround to run; read the failing row itself, which names the fault. For a local server that is down, the fix is `mash-local-nats-setup.sh --start`, which neither command prints.
- [#959](https://github.com/bogworks/mash/issues/959): `install.sh` checks checksums only when `gh` is missing or too old; the one-line commands run a script before you can read it.
- [#1071](https://github.com/bogworks/mash/issues/1071): `install.sh` killed between its two moves leaves `mash` and `mashctl` at different versions; run it again.
- [#1072](https://github.com/bogworks/mash/issues/1072): `install.sh` does not pin the source commit (`--source-digest`) when it verifies attestations.
- [#875](https://github.com/bogworks/mash/issues/875): `mash doctor` has no idle-wake (channel) or plugin check.
- [#993](https://github.com/bogworks/mash/issues/993): on a host without Claude Code hooks, `doctor`'s delivery row warns for as long as it runs.
- [#1008](https://github.com/bogworks/mash/issues/1008): a first `mash join` interrupted partway leaves a folder that neither a retry nor `--replace` finishes.
- [#1013](https://github.com/bogworks/mash/issues/1013): a join file of another account on the same servers can join a folder that already holds an endpoint.
- [#1097](https://github.com/bogworks/mash/issues/1097): `mashctl status` prints nothing and exits 0 when no server is configured. Someone running it before `mashctl init` meets this, and a script or skill that reads silence as "ok" is misled. Workaround: run `mashctl doctor`, whose config row fails: `config     FAIL  no servers in the configuration`, then `1 problem.` and `hint: mashctl init --config <file>`, exit 1.
- [#1062](https://github.com/bogworks/mash/issues/1062): `mash join` adds a certificate authority to a folder that has servers but no endpoint.
- [#1066](https://github.com/bogworks/mash/issues/1066): `mash join`'s rollback can remove a replaced creds file.
- [#1063](https://github.com/bogworks/mash/issues/1063): the migration script can write outside the mash folder if a parent path is replaced.

Endpoints and credentials:

- [#879](https://github.com/bogworks/mash/issues/879): the join file can carry a seed in clear text, with no expiry or single use.
- [#867](https://github.com/bogworks/mash/issues/867): `MASH_DANGEROUSLY_SKIP_NATS_ENCRYPTION` is honoured when inherited, with no fresh choice.
- [#880](https://github.com/bogworks/mash/issues/880): with several servers, revocation must reach every one; there are no failover rules.
- [#912](https://github.com/bogworks/mash/issues/912): `endpoint delete` has no drained-delete guard; mail arriving between a drain and a delete is lost.
- [#872](https://github.com/bogworks/mash/issues/872), [#767](https://github.com/bogworks/mash/issues/767): two machines reissuing one endpoint at once can still interleave. The reissue lock covers one machine only.
- [#765](https://github.com/bogworks/mash/issues/765): a killed same-host `endpoint create` leaves an unused issue record.
- [#1081](https://github.com/bogworks/mash/issues/1081): if `mashctl endpoint delete` fails after the key was cancelled, its error is long, internal and writes the address with underscores; run the same command again, as it says (revoking again is safe).
- [#1055](https://github.com/bogworks/mash/issues/1055): the everyday commands say "no credentials here" for an endpoint deleted on this machine; `mash doctor` says it was deleted.
- [#1056](https://github.com/bogworks/mash/issues/1056): `doctor` and `status` trust a local `<creds>.deleted` file to say an endpoint was deleted (a same-user risk).
- [#1064](https://github.com/bogworks/mash/issues/1064): retiring an endpoint keeps the revoked seed on disk as `.creds.deleted`.
- [#869](https://github.com/bogworks/mash/issues/869): standing risk: anything running as the same OS user can do what that user can (accepted for mash).

One more thing you may see: after a `mash send` that timed out, the server's log can show a filtered `_MASH.settle.*` line. It is expected and harmless.

Some tests are flaky in CI ([#842](https://github.com/bogworks/mash/issues/842), [#861](https://github.com/bogworks/mash/issues/861), [#899](https://github.com/bogworks/mash/issues/899)); they do not affect the product.

### Security

This release refuses unverified mail by default (see Added and Upgrading). It also makes turning encryption off a deliberate, noisy act: one environment variable with a long name, no flag, no config key, and a warning on every command while it is set. Every download has a build attestation; verify it, as the install guide describes ([docs/install.md](https://github.com/bogworks/mash/blob/v0.5.6/docs/install.md#verify-what-you-download)).

### Details

#### Added

- **`mash join FILE`** installs an endpoint on this machine from the join file whoever runs your mesh gave you. It copies the server addresses and the certificate authority's certificate into the configuration folder, writes the endpoint's creds file there (mode 0600), and prints `Joined as acme.demo.bob.` (`--detail` adds the `servers:` and `creds:` lines). When the endpoint made its own key, `--key bob.seed` supplies it. An endpoint already installed is refused unless you pass `--replace`; a join file for another mesh is refused, because one configuration folder serves one mesh (`--config-dir` names another). `--dry-run` writes nothing. It never prints a seed.
- **`mash doctor`** (and an MCP `doctor` tool) checks everything mail depends on: config, creds, server, sign-in and refused mail, one row each, then a count and, for the first problem with a fix, a hint. It exits 1 when it finds a problem; `--strict` makes a warning exit 1 too. Its delivery row also reports hooks: a warning until they have run in the session, a failure when they are not delivering mail. `mashctl doctor` does the same for the server and its records.
- **`mash status --detail`** says whether mail will reach the session: whether it holds the endpoint, whether its hooks ran, and whether storage works. With `--json` it adds a `reason_code`, a fixed word beside the reason, and the session.
- **`--detail` on every command** of `mash` and `mashctl`: the usual answer plus `label: value` lines, or with `--json` a fuller object that contains the usual one. A command with nothing more to say prints the same.
- **`install.sh` and `mash-local-nats-setup.sh`**, with the release. `install.sh` installs `mash` and `mashctl` from one release, checks each against the release's checksums (and the attestation too when `gh` is installed), asks nothing and needs no `sudo` ([ADR 0150](https://github.com/bogworks/mash/blob/v0.5.6/docs/decisions/0150-releases-carry-install-and-local-nats-setup-scripts-as-attested-assets.md)). The local setup script makes a NATS server only this machine can reach and writes the file `mashctl init --config` reads.
- **A `mash-setup` plugin** with a setup skill and a day-2 skill: install it from the marketplace first (`claude plugin install mash-setup@mash`), then ask your agent to set mash up. It walks you through installing mash and a local NATS server.
- **`mash who` filters by what an endpoint does**: `--role TEXT`, `--capability NAME` and `--label NAME[=VALUE]`, the last two repeatable (all must match); the MCP tool takes the same.
- **A wake for a backlog.** `mash serve` wakes the session when more than 15 messages are waiting, or the oldest has waited longer than an hour, at most once every 10 minutes and not in the first 10 minutes after it starts. Change the limits with `MASH_WAKE_COUNT` / `wake_count` and `MASH_WAKE_AGE` / `wake_age` (for example `30m` or `2d`), or `mash serve --wake-count N --wake-age DURATION`; `0` turns a limit off. `mash inbox` says on its second line when a limit has passed.
- **`mash send` prints the message's number**: `Mail sent (number 42).`, in the command and the tool. Urgent and retagged mail say so too (`Urgent question mail sent (number 43).`; `Mail sent as fyi (number 44): a reply marked question that asks nothing.`). `--json` prints the same object, including `retagged`.
- **The plugin tells you when mash is missing.** When mash is not installed, the plugin's session-start hook prints one line with the install command, so the agent can tell you. `MASH_RELEASE_BASE` moves the versioned downloads to a fork or mirror; the install line the plugin prints always names the project's own release address ([#996](https://github.com/bogworks/mash/pull/996)).

#### Changed

- **Unverified mail is refused by default.** Only mail whose trust is `verified` reaches the session, `mash inbox` and `mash read`. Mail that is unsigned, signed by an unknown key, claimed but unproven, or stale is dropped and counted; `mash doctor` shows the count in its `refused` row, and the sender is not told. `mash read` on the number of a refused message says it was refused. `MASH_ACCEPT_UNVERIFIED=1` (or `accept_unverified` in `config.json`, or `mash serve --accept-unverified`) delivers it, labelled. Mail refused earlier is not delivered afterwards.
- **One name for each setting, and three ways to give it.** `MASH_ENDPOINT`, `MASH_SERVER` (a comma list) and `MASH_CA` each also exist as `--endpoint`, `--server`, `--ca` and as `endpoint`, `servers`, `ca` in `config.json`. With one endpoint's credentials in the config folder, the commands you type use it without being told.
- **A session is an agent only when it is told which.** `mash serve` and `mash hook` do not pick the one installed endpoint for themselves.
- **Turning encryption off has one name.** `MASH_DANGEROUSLY_SKIP_NATS_ENCRYPTION=1`, environment only, with a warning from every command; `MASH_INSECURE_PLAINTEXT` and `insecure_plaintext` are refused with an error that names it.
- **Addresses are written `owner.group.name` and nothing else.** `acme_demo_bob` and `acme/demo/bob` stop with exit 2 and `error: acme_demo_bob is not an address; write it as acme.demo.bob`.
- **`mash status` is one line**: `acme.demo.bob: ready, offline` (or `ready, online`), or `acme.demo.bob: not ready: <why>` with `hint: mash doctor` and exit 1. It connects and signs in, so it says whether mail works. Asked through a session's tools, `status` and `doctor` report on that session itself.
- **`mash who` prints a headed table** of `ADDRESS` and `STATE` (`--wide` adds `ROLE`, `HOST` and `DESCRIPTION`). `mash who acme` lists everything under `acme`, `mash who acme.demo` that group, `mash who acme.demo.bob` that one endpoint, never `acme.demo.bobby`. `--online` and `--offline` cannot be combined. `--json` is `{"endpoints": [...]}`, never a bare array.
- **`mash inbox` and `mash read` have a new form**, the same at a shell and in the MCP tools. `mash inbox` prints a summary line and a table (number, sender, age, `new` or `read`, urgency, kind, first line); `--new`, `--after NUMBER` and `--wide` narrow or widen it, and `--limit` is at most 100. `mash read NUMBER` prints labelled lines, then the whole text with `> ` before every line, control characters escaped, so nothing a sender writes can pass for mash's own words. `--json` carries the text unquoted in `body`.
- **`mash send` takes the message as one argument** (quote it), or `--file`, or standard input, and has `--reply-to`, `--relay` and `--id`. `mash version` prints one line, `mash v0.5.6 (` and the short commit `)`. `mash <verb> --json` on a verb with no JSON form is a usage error.
- **A missing credentials file, a suspended or deleted recipient and a deleted endpoint are named plainly.** When the creds file is not where the configuration says, `send`, `inbox`, `read` and `who` print `error: acme.demo.bob has no credentials here: <path> is missing` and `hint: mash doctor`, never the operating system's words. A `send` to an agent that takes no mail prints `error: acme.demo.alice is suspended or was deleted; nothing was sent` and `hint: mash who`, never the server's. On the machine that deleted an endpoint, `mash status` says `this endpoint was deleted; its address cannot be used again`, and `mash doctor` says so in its creds row; from another machine, `doctor` says it in its sign-in row and points to a new join file.
- **`mash inbox` counts what it lists.** Mail from a sender that has since been deleted is not shown, so it is no longer counted in the total or the new count (one limit remains: see Known issues).
- **Errors look alike.** An error before a command runs (a bad option, an unknown command, a configuration or endpoint that cannot be used) is one `error:` line with a `hint:` line on stderr; with `--json` it is `{"error","hint"}` on stdout. Exit codes are 0, 1 and 2.
- **`mash key create` follows the specification.** `--out` defaults to `mash.seed`, the public key goes beside it as `bob.seed.pub`, and it prints `Created bob.seed and bob.seed.pub.`, or `error: bob.seed already exists; nothing was written` (exit 1). `--pub-out` and `--dry-run` are gone.
- **A 0.5.5 configuration folder is named for what it is.** Every command that reads the configuration stops with `error: <folder> holds a mash 0.5.5 configuration` and a hint to `mash-migrate-0.5.5.sh`; nothing is read or changed.
- **Setup is `mashctl init --config FILE`,** and `mashctl` does not install or start NATS.

#### Removed

- **Everything in the table above.** Each removed command or setting has a replacement named there; none is kept as an alias.
- **The old `mash who` output modes and filters** (`--match`, `--name`, `--owner`, `--cap`, `--long`, `--names`, `--format`, the tab-separated lines and the last-seen column).
- **The one-line `#seq` inbox listing and the `--- #5 from ...` form of `mash read`.** The hook still delivers mail in its own form.

[Full changes](https://github.com/bogworks/mash/compare/v0.5.5...v0.5.6)

## [0.5.5] - 2026-11-18

### Summary

**mash 0.5.5 changes how every agent is named, and it does not work with anything made by 0.5.0. Read Upgrading before you
install it.** An agent's address is now three parts, owner, group and instance (for example `alice.dev.design`), and every
agent must name its group. Credentials files, directory records, inboxes and addresses from 0.5.0 are not read or converted,
and mail still unread in old inboxes when you switch is not carried over. Start with a fresh deployment. If you have never
used mash, this is the release to start with: the quick start below takes you from nothing to two Claude Code sessions
that mail each other. Every download has a build attestation; verify it.

**mash 0.5.5 also requires TLS on every connection, loopback included. Every `mash` and `mashctl` connection now needs a `tls://` (or `wss://`) URL, and a plain `nats://` URL is refused unless you set `insecure_plaintext` (`MASH_INSECURE_PLAINTEXT=1`).** A 0.5.x deployment that connects in plain text sees that refusal on its first connection after the upgrade. `mashctl init --local` makes its own certificates, so the quick start below needs no override.

0.5.0 remains the version running deployments should keep using until they are moved. 0.5.5 reads no 0.5.0 state, so do not install it
over a running endpoint, including by `go install ...@latest`.

### Why a hard cut

0.5.0 had two address forms: agents made with `mashctl add` carried a fixed `cc` token and had no group, and their
credentials were made on the machine that runs `mashctl`, so a private key had to be copied by hand to any other machine.
Keeping both forms working would have meant carrying the old one around indefinitely, for no one's benefit, so 0.5.5 has
one form and nothing else. That also means the old way of doing things is gone rather than deprecated.

### Upgrading

If you have never used mash: nothing to move. Follow the quick start.

If you used 0.5.0: the order matters, and none of this is automatic.

1. **Start a new deployment.** Run `mashctl init --local` for a local server, or set up a new account on your own servers.
   Leave the old one untouched for as long as you want a way back to 0.5.0.
2. **Choose a name for every agent in three parts.** Write each as `owner/group/instance`, for example `alice/dev/design`.
   Each part is lower-case letters, digits and hyphens, starts with a letter, is at most 32 characters, with no `--` and no
   trailing hyphen. There is no default group: a command or session without one is refused, and nothing guesses it. A group
   cannot be changed later. To move an agent to another group, create a new one and delete the old one.
3. **Create each agent again** with the quick start below. Old credentials files, directory records and inboxes are not
   read.
4. **Read mail you still need first.** Mail unread in an old inbox when you switch is not carried over.
5. **Update scripts and settings.** `mashctl add`, the old `reissue` and `retire` are gone and print a message that names
   the replacement (`mashctl endpoint create`, `endpoint reissue`, `endpoint delete`). `unzone` is gone too and is now an
   unknown command (exit 2). `agent_token` and `MASH_AGENT_TOKEN` are
   gone. Start each session with `MASH_GROUP` (and `MASH_OWNER` and `MASH_NAME` if they are not in your config).
6. **Check where mash looks for its config.** If `XDG_CONFIG_HOME` is set, mash now reads `$XDG_CONFIG_HOME/mash` only; it no
   longer falls back to `~/.config/mash`. To keep using that directory, set `MASH_CONFIG_DIR=$HOME/.config/mash` (an
   absolute path), or move the files. (Checked against the code on main: `MASH_CONFIG_DIR`, then an absolute
   `XDG_CONFIG_HOME/mash`, then `~/.config/mash`; a relative `XDG_CONFIG_HOME` is ignored.)
7. **Update anything that reads mash output.** The audit report (`mashctl audit --json`) is version 2 with new field names,
   and `--compare` refuses a version 1 file. `mash who --json` now reports `name` as the flat key `owner_group_instance`
   and adds `identity` (`owner.group.instance`), `owner`, `group`, `instance` and `agent`. Spool folders and the owner shown
   in hook and spool messages use the new address. (Checked against the code on main: `audit` records carry `address`, `owner`, `group`, `instance`; `who --json` carries `identity`, `owner`, `group`, `instance`, `agent`.)
8. **Use TLS on every connection, or set the override knowingly.** A plain `nats://` URL (or one with no scheme, or `ws://`)
   is refused, to any host and loopback included, with a message that names the setting and the fix, unless
   `insecure_plaintext` (`MASH_INSECURE_PLAINTEXT=1`) is set; with it set, mash connects and prints a warning on standard error
   (and `mash status` shows it). Do not set it on a network you do not control: plain text lets anyone on the network read
   mail and obtain signatures.
9. **`mashctl init --local` now makes a local certificate authority and certificates,** and runs its server with TLS only; the
   configuration it writes has the `tls://` URL and `tls_ca`, so nothing else is needed. If you run your own server, give it
   TLS and set `tls_ca` (or `MASH_TLS_CA`) to the PEM file of the authority that signed its certificate, or set the override.
10. **Remove `insecure_loopback` and `require_tls` from your settings.** `insecure_loopback` (`MASH_INSECURE_LOOPBACK`) and
    `require_tls` (`MASH_REQUIRE_TLS`) are gone and are not read: `insecure_plaintext` replaces both
    ([ADR 0146](https://github.com/bogworks/mash/blob/v0.5.5/docs/decisions/0146-every-mash-and-mashctl-connection-requires-tls-loopback-included-and-one-override-insecure-plaintext-allows-plain-text.md),
    which replaces the default and the loopback allowance of ADR 0108).

Moving your existing agents is a one-time job. mash does not do it: it is done outside mash, once, by whoever runs the
deployment, onto a fresh deployment, with a written map from each old name to its owner, group and instance.

### Quick start: your first agent

On one machine, with `mash`, `mashctl` and `nats-server` installed:

```
mashctl init --local --owner alice --start
mash key create --out design.seed
mashctl endpoint create alice/dev/design --public-key design.pub
mash credential create --seed design.seed --jwt alice.dev.design.jwt
```

Install the plugin, then start the Claude Code session with `MASH_OWNER=alice MASH_GROUP=dev MASH_NAME=design`. Repeat for a
second agent (`review`) and the two sessions can mail each other.

Why it works this way: each agent makes its own key on its own machine (`mash key create`), and only the public key goes to
`mashctl`. `mashctl endpoint create` signs it and writes a JWT file; `mash credential create` joins the seed and the JWT into
the credentials file, on the agent's machine. The private key is never on the `mashctl` machine and no secret travels. An
agent on another machine uses the same four steps; only the public key file and the JWT file move. If an agent runs on the
same machine as `mashctl`, `--make-key` makes the key there and writes the credentials file, and is only for that case.

`mashctl endpoint reissue` gives an agent a new key and revokes the old one, and `mashctl endpoint delete` removes it. Either
way the session using the old key is disconnected and cannot reconnect. `endpoint list` and `endpoint show` change nothing.
A deleted address is burned: it cannot be created again. An agent's credentials reach only the agents of its own owner.

### One departure from the Synadia Agent Protocol

The first subject slot now carries the owner, not the name of the harness. The protocol says that slot SHOULD name the
harness; mash does not follow it. Finding mash agents with a wildcard on the harness slot therefore does not work. Finding
them by the service name does, and the service metadata still says `claude-code` and adds `owner`, `group` and `instance`.

### Security

This release closes replies across owners: an endpoint of another owner can no longer answer, forge or replay a reply into your
endpoints. Inside one owner, endpoints can still answer each other's reply subjects; that is the endpoint model's "wide open under
the owner" until the policy release. An endpoint's reply inbox now lives under its owner (`_INBOX.<owner>.<flat key>`), and it can
publish replies only to its own owner's inboxes. A credential made before this release is refused by this release's mash: reissue
it (the hard cut already means starting fresh).

### What was tested, and what was not

- Tested on one machine, with one local server, from a fresh home, over TLS with no override: the quick start's steps, mail
  both ways, reissue and delete, and every refusal listed here. What the test did, exactly: it built `mash` and `mashctl` from
  the commit and ran the steps of the quick start with those binaries; it ran each session through the plugin's own launcher
  (`plugin/scripts/mash serve`) and delivered mail through the plugin's hook commands, with the test itself playing the roles
  of Claude Code's MCP client and hook caller. No Claude Code session ran, and the install steps of the README (a downloaded
  binary, `go install`) were not part of it: the maintainer's own walk through the README, from a clean home to two mailing Claude Code
  sessions (JRN-001), is done after the release; AT-001, the automated run, stands in for it until then. Run records are in the repository (`docs/evidence/2026-11-17-0-5-5-live-runs/`):
  the quick start's steps, mail, the permission set and its isolation, revocation and every refusal passed. All of them ran on the code commit `e26332c` (AT-001 to AT-003 and AT-005 by the core worker; AT-004, the command contract, by the CLI team), and the records name it.
- Across separate nodes: three NATS servers in one cluster over TLS, in three isolated containers the test builds and destroys
  itself on one developer machine, with agents on nodes other than the server they connect to. Reissue and delete were checked
  against every server, and a plain `nats://` URL is refused unless the override is set. This was a run by hand on one machine, not on three real hosts, and it passed on `e26332c`.
- mash writes no server configuration beyond what `mashctl init --local` makes; TLS and a cluster are set up by the operator, as `docs/tls-and-clusters.md` describes, and the test builds the same configuration by hand.
- Two `mashctl` machines share key material copied by hand (the three-server procedure in the operations guide); the run
  above used two. A machine with its own signing key is not supported.
- Load: the directory-polling and heartbeat-fan-out measurements of 2026-11-02 (`docs/evidence/2026-11-02-directory-polling/`
  and `docs/evidence/2026-11-02-heartbeat-fanout/`) were made before the three-part address and before owner-scoped reply
  subjects. No fresh measurement was made for 0.5.5; read those numbers as of 2026-11-02.
- Not tested, and not offered: upgrading in place, moving old agents, carrying mail across, changing a group, and clock differences between machines beyond the few seconds the operations guide states (a unit test covers two
  admins whose clocks differ by up to 45 seconds: the second create is refused, never both succeed).

### Known issues

- `mashctl endpoint reissue --changed` selects endpoints by the permission set in their local credentials file and by staged or
  revoked keys; it does not check that a local credentials file belongs to the endpoint's current key. After a reissue on another
  machine it can report nothing to reissue while the local file still holds the revoked old key. Workaround:
  `reissue ADDRESS --make-key` for that endpoint (#764). The fix is planned for 0.5.6.
- A same-host `endpoint create` that is killed part way leaves an unused entry in the issue record; nothing can connect with it
  (#765). A create with the endpoint's own key (the default) is finished by running it again.
- `endpoint create` prints no line saying the servers took an account update (#766); it makes none (nothing is revoked), and
  `reissue` and `delete` do print how many servers acknowledged the revocation.
- **`endpoint reissue` locks on one host only (#767).** A reissue takes a file lock (`<creds file>.lock`) that the operating
  system drops when the run ends or is killed, so a killed run needs nothing removed. The lock works on one machine and a
  local filesystem. It does not coordinate two machines, and it does nothing on a network filesystem that ignores file locks,
  so do not run `endpoint reissue`, `create` or `delete` for one endpoint from two machines at once. A lock directory left by an
  earlier mash is reported by the refusal and removed once by hand. Owner: the mashctl commands.
- The advisory macOS test job fails in the Linux-only podman test harness's unit tests; follow-up #774.
- The test `TestCrowdedBacklogOnAFullStreamWakesAtOnceAfterADrain` failed twice in CI before the release; the cause was in the
  test, not in mash, and it is fixed (#745).

### Also in this release, not part of the 0.5.5 contract

These changes rode along with the release. They are deliberate and small, and none is a requirement of the endpoint model:

- A flag that takes a value now refuses a value that starts with `-` unless it is written `--flag=VALUE` (a bare `-` is still a value).
- The operations guide now states, and tests pin, that `mash serve` exits when its standard input closes and never renews its lease after that (within 5 seconds).
- `mashctl init` and `adopt` ask the server whether JetStream is enabled and say what to do when it is not.
- `mash status` says the last wake started a turn only after a wake was actually sent.

### Details

**Added**

- **Make and manage endpoints with `mashctl endpoint`.** `mashctl endpoint create alice/dev/design --public-key design.pub` signs a key the endpoint made itself and writes its JWT to a file (`--make-key` makes the key on the mashctl host instead and writes the creds file). `endpoint reissue`, `endpoint delete`, `endpoint list` and `endpoint show` complete the set; each takes `--json`, and `create` and `delete` take `--dry-run`. There is no default group: write `--group dev` or the three-part address.
- **Make a key and a credentials file with `mash`.** `mash key create --out FILE` writes a new user seed and its public key, and `mash credential create --seed FILE --jwt FILE` joins a seed and the user JWT issued for it into a credentials file. Neither needs a server, a config file or an account key. `credential create` checks that the JWT names the seed's key; it does not check the issuing account or the expiry.
- `mash serve` exiting on stdin EOF is now a documented guarantee, pinned by tests: it releases the endpoint lease and exits within 5 s, an EOF during the first open is acted on once that open returns, and a superseded serve never renews its lease.

**Changed**

- **Every agent now has a three-part address, owner.group.instance, and 0.5.5 does not work with anything made by 0.5.0.** Every agent names its group (there is no default and it cannot be changed later), every credentials file is replaced, old records, addresses and credentials are not read or converted, mail unread in old inboxes is not carried over, `mashctl add`, the old `reissue`, `unzone`, `agent_token` and `MASH_AGENT_TOKEN` are gone, the audit report is version 2, and the first subject slot carries the owner, not the harness. Make agents with `mashctl endpoint create`, from a key each agent makes with `mash key create`; start from a fresh deployment.
- **`mashctl add`, `mashctl reissue` and `mashctl retire` are removed.** Use `mashctl endpoint create`, `mashctl endpoint reissue` and `mashctl endpoint delete`. Endpoints made by 0.5.0 do not work with 0.5.5: make new ones and delete the old ones.
- `mashctl init --local` now makes a server that speaks TLS only. It creates a local certificate authority and a server certificate for `localhost`, `127.0.0.1` and `::1` (ten years, keys mode 0600, in the server directory), and writes the `tls://` URL and `tls_ca` into `config.json`. The quick start runs over TLS with no setting.
- An endpoint's replies can now only reach reply inboxes of its own owner (`_INBOX.<owner>.>`), and an endpoint listens only under its own `_INBOX.<owner>.<name>` prefix. An endpoint of another owner can no longer answer your discovery or presence probes, forge an answer into them, or replay a genuine one, so `mash who` and `mash prompt` cannot be told that an offline endpoint is online by someone outside your owner. Nothing to do for users: new credentials carry the narrower permissions, and the next `mashctl endpoint reissue` of an existing endpoint picks them up.
- **Every `mash` and `mashctl` connection now requires TLS, loopback included.** A plain `nats://` URL (or one with no scheme, or `ws://`) is refused before any connection is made, unless `insecure_plaintext` (`MASH_INSECURE_PLAINTEXT=1`) is set; with it set, mash connects, prints a warning on standard error and shows it in `mash status`. Use a `tls://` URL, and `tls_ca` for a private certificate authority. `mashctl init --local` makes its own certificate authority and runs its server with TLS only, so the quick start needs no override. `insecure_loopback` and `require_tls` (`MASH_INSECURE_LOOPBACK`, `MASH_REQUIRE_TLS`) are removed and are not read ([ADR 0146](https://github.com/bogworks/mash/blob/v0.5.5/docs/decisions/0146-every-mash-and-mashctl-connection-requires-tls-loopback-included-and-one-override-insecure-plaintext-allows-plain-text.md)).

**Removed**

- The `~/.config/mash` fallback is gone: with `XDG_CONFIG_HOME` set to a directory, mash reads its config, `policy.md` and default credentials only from `$XDG_CONFIG_HOME/mash` (or from `MASH_CONFIG_DIR`). With `XDG_CONFIG_HOME` unset, `~/.config/mash` is still the one location. Move files from `~/.config/mash` if you set `XDG_CONFIG_HOME` before. A directory record that cannot be read is skipped with a warning naming its key, in every listing, instead of failing the command. The spool no longer reads the legacy `.session` file or the `pid:N` owner text, and message numbers carry no `L5` form.

**Fixed**

- A flag that takes a value now refuses a value that starts with `-` unless it is written `--flag=VALUE`, so `mashctl add n --note --help` no longer mints a note "--help". A bare `-` is still accepted as a value.
- `mashctl adopt` no longer says JetStream is "enabled for the account" from the account JWT alone: step 3 reports what the JWT gives, and step 6 asks the server as the account's admin user. When the server could not enable JetStream (its store budget, 75% of the free disk by default, is under the account's disk limit), `adopt` and `mashctl init` now say so, with the two fixes, instead of printing a bare `jetstream not enabled` (err_code 10076).
- `mashctl endpoint reissue` takes a file lock (`<creds file>.lock`, flock) that the operating system drops when the run ends or is killed, so a killed run needs nothing removed and the identical command succeeds. The lock works on one machine and a local filesystem; it does not coordinate two machines or a network filesystem that ignores file locks. A lock directory left by an earlier mash is reported by `reissue_busy` and removed once by hand.
- `mashctl endpoint reissue --make-key` keeps the staged key in `<creds file>.pending` until it has read the directory record back and found the new key there. A run that stopped after the new creds file was written, or could not read the record back, is finished by running the same command again, which no longer stages another key and revokes the one the first run installed.
- `status` no longer says "the last wake started a turn" when hooks have run but the server has never sent a channel notification.

[Full changes](https://github.com/bogworks/mash/compare/v0.5.0...v0.5.5)

## [0.5.0] - 2026-11-15

### Summary

**This is a breaking release. If you used zones, read Upgrading before you install it.**

mash v0.5.0 does two things: it retires zones, and it tightens security in the parts that remain.

**Zones are gone.** Zones were our first attempt at letting you control which agents may mail which other agents. In practice they didn't deliver what we wanted from a security policy: they were hard to reason about, they changed the shape of every mail address, and they didn't give a clean answer to "who is allowed to talk to whom". Rather than keep patching them, we're retiring them. A simpler, clearer way to say who can reach whom is planned for 0.6.0.

Until then the rule is plain: **an agent's address is its fence.** Each agent has one inbox, and every agent under the same owner can reach it. All mail uses the same five-part address again (`agents.mail.<agent>.<owner>.<name>`), with no zone in the middle.

**Also in this release:**

- **A record of every credential.** `mashctl` now records each credential it hands out. `mashctl issued check` compares that record with who is actually connected and flags anything mash didn't issue. Revoking a credential is also safer: mash refuses when the record shows the revocation wouldn't actually take effect.
- **Stricter settings you can turn on:**
  - refuse unencrypted connections to remote servers (`MASH_REQUIRE_TLS`);
  - never fall back to a less reliable delivery path (`MASH_SPOOL_REQUIRED`);
  - have the Claude Code plugin refuse a mash binary it can't verify (`MASH_PLUGIN_REQUIRE_VERIFIED`).

  All of them are off by default.
- **A look inside an inbox.** `mashctl inbox inspect <agent>` shows the mail stored for an agent and whether it was delivered, without changing anything.
- **An easier start.** The README now opens with a quick start for one person on one machine, with no account needed.
- **Fixes:**
  - `--help` after any command now prints help and does nothing else. Before, `mashctl init --help` ran `init`.
  - `mashctl init --local` respects `XDG_CONFIG_HOME`.
  - Long or odd prompts are handled safely.

Every download comes with a GitHub build attestation; verify it before you run the binary.

### Upgrading

**If you never used zones**, upgrading is two steps:

1. Install v0.5.0 (the `mash` binary and the plugin).
2. Run `mash admin init` once. It creates the new credential record and hardens a setting on the server.

Also note: `mashctl add` and `mashctl reissue` now need the system account's credentials, so that they can check no zone policy is in force. Pass `--sys-creds FILE`, set `MASH_SYS_CREDS`, or keep a `sys.creds` file next to your admin credentials.

**If you used zones**, the order matters:

1. **If you applied a zone policy, stay on v0.4.0 for now.** There's no automatic way out of an applied zone policy, and `mashctl unzone` refuses to touch it. Run `mashctl audit` to see which case you're in.
2. **Otherwise, remove zones before you upgrade the agents:**
   - Upgrade only `mashctl` first.
   - Run `mashctl unzone <agent>` for each zoned agent. It moves the agent back to the plain address and keeps its stored mail. It is safe to run twice.
   - Give each agent a fresh credential: run `mashctl reissue <agent>` where its credentials file lives. Add `--widen` if it refuses because the agent's old permissions were narrower.
   - Then upgrade the agents. Until every zoned record is converted, `mash serve` refuses to start.
3. **Update your scripts.**
   - The zone options are gone: `--zone`, `set-zone` and `--policy`.
   - So are the v0.4.0 `mashctl policy …` commands.
   - The table in [Upgrading admin tooling across zone removal](https://github.com/bogworks/mash/blob/v0.5.0/docs/operations.md) lists each changed command.
4. **Run `mash admin init` once.**

**Plugin pinning.** The plugin in this release isn't yet pinned to a specific mash binary. A follow-up plugin release, `0.5.0-pin.1`, pins it to v0.5.0. Turn on `MASH_PLUGIN_REQUIRE_VERIFIED` only after you've updated to that plugin.

### Details

**Added**

- **The credential record.** `mashctl` records every credential it issues (from `add`, `reissue` and `init`) in a bucket that only the admin can write.
  - `mashctl issued list` shows the record.
  - `mashctl issued check` reports connected credentials that mash didn't issue, or that it has revoked.
  - [ADR 0101](https://github.com/bogworks/mash/blob/v0.5.0/docs/decisions/0101-every-issued-user-jwt-is-recorded-in-a-bucket-only-the-admin-connection-writes.md) explains the design.
- **`MASH_REQUIRE_TLS`.** Refuses plain-text connections to any host that isn't your own machine. `mash status` now warns about a plain-text remote address in every form ([ADR 0108](https://github.com/bogworks/mash/blob/v0.5.0/docs/decisions/0108-require-tls-makes-endpoint-connections-refuse-plain-text-to-a-remote-host.md)).
- **`MASH_SPOOL_REQUIRED`.** If mail can't be handed to the session safely, it stays in the inbox and you're told, instead of being delivered another way ([ADR 0107](https://github.com/bogworks/mash/blob/v0.5.0/docs/decisions/0107-mail-delivery-fails-closed-under-one-concrete-setting-mash-spool-required.md)).
- **`MASH_PLUGIN_REQUIRE_VERIFIED`.** The plugin refuses an unpinned plugin or an unverified binary, instead of falling back to whatever `mash` is on your PATH ([ADR 0096](https://github.com/bogworks/mash/blob/v0.5.0/docs/decisions/0096-the-plugin-launcher-takes-a-verification-setting-and-the-layer-above-owns-profiles.md)).
- **`mashctl inbox inspect <agent>`.** Shows an agent's stored mail and its delivery state, read-only. Add `--json` for scripts.
- **A lease bucket warning.** An agent warns at startup, and `mash status` shows a line, when a server bucket still needs hardening. `mash admin init` fixes it.

**Changed**

- **Zones are removed** ([ADR 0091](https://github.com/bogworks/mash/blob/v0.5.0/docs/decisions/0091-there-are-no-zones-mail-keeps-the-five-token-subject-and-the-address-is-the-fence.md)):
  - every mail address has five parts;
  - `set-zone`, `--zone` and the zone policy file and its commands are gone;
  - `mash who` no longer shows a zone.
- **Agents are declared with `mashctl add`.** It creates the agent's record, inbox and credential in one step. `reissue`, `retire`, `label` and `unlabel` all work from that record.
- **Bulk changes ask first.** `mashctl reissue --all` and `--changed` list what they would change and only act with `--yes`. A reissue never quietly widens an agent's permissions.
- **A clearer refusal.** When a send is refused, the message now says the credential may be out of date and suggests asking the admin to check it.
- **The README** starts with a quick start for one machine. `nats-server` is listed as a prerequisite.

**Fixed**

- **`--help`** after any command (`mash` or `mashctl`) prints that command's help and does nothing else. Text later on the line, such as a message to `mash send`, is never mistaken for `--help`.
- **Revocations that wouldn't take effect.** `deprovision`, `retire` and a rotating `reissue` refuse when the credential record shows that a credential was issued after the revocation time. NATS would otherwise keep honouring that credential. If the record can't be read, they change nothing ([ADR 0115](https://github.com/bogworks/mash/blob/v0.5.0/docs/decisions/0115-a-revocation-needs-the-issue-record-to-show-a-keys-whole-history-and-the-bucket-says-so-in-its-stream-metadata.md)).
- **Prompts and `mash who`.**
  - Control characters in a live prompt and in `mash who` output are shown as visible escapes.
  - A prompt over 32 KB is refused with a clear error instead of being cut off.
- **`mashctl init --local`** respects `XDG_CONFIG_HOME` on a first run and prints where it will write.
- **System credentials.** `mashctl` refuses system credentials that other users can read.
- **The upgrade check** won't start an agent if it can't read a record that might still be zoned.

Full changes: [v0.4.0...v0.5.0](https://github.com/bogworks/mash/compare/v0.4.0...v0.5.0)

## [0.4.0] - 2026-11-12

### Summary

mash v0.4.0 is a security and groundwork release. It closes a flaw that let any endpoint in an account wipe the shared lease store, hardens several smaller edges, and lays the groundwork for three-part identities (agent, owner and name) without turning them on. It is also the first mash release cut without a signing key on anyone's machine: you verify it against GitHub's build attestations instead. Every commit since v0.3.3 is listed in the [compare view](https://github.com/bogworks/mash/compare/v0.3.3...v0.4.0).

### Upgrading

There is one required step after upgrading, and two things to know about admin tools and the plugin.

1. **Re-run `mash admin init` once after upgrading.** It hardens the lease bucket (`KV_mash-leases`) so it no longer accepts rollup writes, which is the security fix in this release. `mashctl audit` reports a bucket that is not yet hardened, and `mash serve` now warns at start when it sees one.
2. **Upgrade every admin tool before the bucket is hardened.** On a hardened bucket, `mash admin` and `mashctl` from 0.3.x cannot provision, deprovision or retire, and their name locks linger for up to 30 seconds. Endpoints of any version keep working. Any 0.4.0 admin command hardens the bucket as a side effect, so move every admin caller in an account to 0.4.0 together.
3. **Plugin pinning.** The plugin published with this tag is not pinned to a binary. The plugin release that follows it (`0.4.0-pin.1`) pins the launcher to v0.4.0. If you set `MASH_PLUGIN_REQUIRE_VERIFIED`, do so only after updating to that pinned plugin.

### How this release is verified

From v0.4.0, mash releases are keyless. The tag is annotated and unsigned, pushed by the release service. There is no `SHA256SUMS.sig`. Instead, every binary and `SHA256SUMS` carry a GitHub build attestation made by mash's own release workflow on mash's runners. Check a download like this, pinning all four facts:

```sh
gh attestation verify mash-linux-amd64 -R bogworks/mash \
  --signer-workflow bogworks/mash/.github/workflows/release.yml \
  --source-ref refs/tags/v0.4.0 \
  --source-digest 5d907947e06cf6ebc6ff1f2827527dafe85d56f8
sha256sum -c --ignore-missing SHA256SUMS
```

Releases before v0.4.0 still verify with their SSH signatures, and the old signer files stay in the repository, frozen. An attestation says that mash's release workflow built these bytes from this tag on a runner mash operates; it does not say that the runner was uncompromised or that a person reviewed the release. This release was cut under a recorded exception to the new release standard, described in [docs/release.md](docs/release.md) under "Exceptions to the release process".

### Added

- A new optional setting, `agent_token` in config.json or `MASH_AGENT_TOKEN`, selects a three-token identity for the endpoint; unset keeps today's behaviour. Nothing provisions such endpoints yet. A MASH_AGENT_TOKEN that is set but empty is an error once a name is set, not a fallback to the legacy identity.
- The window bridge of a three-token identity with an alias also grants the alias's own legacy lease key, exactly two publish lines: `$KV.mash-leases.<legacy name>` and the direct-get of that key. The endpoint needs it to hold the legacy identity's lease while it reads the legacy inbox. Nothing wider is granted. Nothing mints such a user yet ([ADR 0076](docs/decisions/0076-a-three-token-record-may-carry-the-legacy-identity-it-answers-to-as-an-alias.md)).
- A three-token endpoint whose directory record carries an alias reads two inbox streams: its own `inbox-<flat>` as before, and the legacy inbox stream through the existing `deliver` consumer, so mail already acknowledged is not shown again and nothing is copied. Message numbers are stream-qualified: the own stream stays `#n`, a legacy-stream message shows `#L5` (message, inbox line, digest, wake summary, channel `seqs`, `read`); `send`'s `in_reply_to` and `via` take a bare number and refuse an `L` ref. While it reads the legacy stream the endpoint also holds the legacy identity's lease; if another session holds it, the legacy inbox is not read and `mash status` says `legacy inbox: not read (...)`, and a lease lost mid-session stops the legacy reads and releases unacknowledged legacy mail. A missing, unreadable or invalid alias fails the open; a changed alias takes effect at restart. A legacy-only endpoint's output is unchanged ([ADR 0076](docs/decisions/0076-a-three-token-record-may-carry-the-legacy-identity-it-answers-to-as-an-alias.md)).
- `mashctl audit` lists the retired-identity markers (flat identity, state, retired time) with the bucket's bytes against its cap, and `--strict` fails on a pending marker or when the bucket is at or over 50% of its cap; `audit --compare` fails when a final marker has gone
  ([ADR 0088](docs/decisions/0088-a-retired-new-form-name-is-burned-in-a-durable-bucket-by-a-two-phase-marker-that-is-never-deleted.md)).
- The directory reader now accepts records of three-token identities (keyed `agent_owner_name`); nothing writes them yet. A reader older than this release cannot read such a record, so upgrade every reader before enabling new-form endpoints.
- The directory record may carry an optional `alias` (the legacy owner and name a three-token record answers to during the transition window), admission treats it as part of the identity, and `mash who --long` shows it (`who --json` has an `alias` key only for such a record). Nothing writes it yet. A reader older than this release ignores the field.
- `mashctl audit` lists mash's side of the population from the server and the creds directories, read only: every directory record and inbox stream (form, running or stopped, message count, first and last sequence and time, subjects), the orphans (inbox streams, lease keys and creds files, staged ones included, with no directory record), and for every identity with an alias the drain state of its legacy inbox. `--compare EARLIER.json` reports what moved since an earlier `--json` report. `--json` is stable and versioned; `--strict` exits 1 on a finding and 3 means the audit could not complete. It shows state, not use: see docs/audit.md.
- New optional name-acceptance rule file, format `name-rule@1` ([docs/name-rule.md](docs/name-rule.md), [ADR 0077](docs/decisions/0077-the-name-rule-file-is-versioned-and-mash-reads-two-versions-for-one-window.md)): `name_rule_file` or `MASH_NAME_RULE_FILE` names a JSON file of refusal rules over the agent, owner and name tokens, and `name_rule_required` or `MASH_NAME_RULE_REQUIRED` makes start fail without one (the environment wins; a set-but-empty or non-boolean value is invalid). The rule is checked, before the directory is read, at start, send (sender and recipient), reply, prompt, lookup, provisioning and the alias at start; an incoming prompt whose asker the rule refuses is answered 403 with the rule id and message, and a refused `via` origin is dropped while the mail is still delivered from its sender. A refusal names the rule's `id` and its `message`. A bad file refuses start for `mash` and `mashctl` (`mash hook` stays a silent no-op). `mash status` shows `name rule: <path> sha256:<12 hex> (<N> rules)` or `name rule: none set`. mash ships no rule; with none set behaviour is unchanged. mash reads version 1 only.
- `mashctl` and `mash admin` provision and deprovision an endpoint by its three tokens: `provision NAME --agent A [--owner O] [--alias OWNER/NAME] --user U --account A`, and `deprovision agent.owner.name` (or `NAME --agent A --owner O`). The form needs the `agent_token` setting; a retired identity is refused on provision, a refusal exits 1 and a bad token exits 2. The legacy form is unchanged ([ADR 0074](docs/decisions/0074-an-endpoint-is-three-sap-tokens-and-keys-its-storage-on-their-flat-form.md)).
- Provisioning a three-token identity (admin layer; no CLI verb yet) verifies the alias it will answer to: the legacy record exists, is unzoned, is not claimed by another three-token record, has a different owner, and holds the legacy name rule; a refusal writes nothing. A retired three-token name is recorded in a durable, no-expiry, admin-only KV bucket `mash-retired` (written before anything is revoked, finalised after the purge) and is never provisioned again; there is no release path. Deprovisioning a legacy endpoint is refused with `ErrAliased` while a three-token record aliases it. Legacy-only directories, and the reuse of legacy names, are unchanged. The `mash-retired` bucket is new server state that is not covered by `Limits` ([ADR 0074](docs/decisions/0074-an-endpoint-is-three-sap-tokens-and-keys-its-storage-on-their-flat-form.md), [ADR 0076](docs/decisions/0076-a-three-token-record-may-carry-the-legacy-identity-it-answers-to-as-an-alias.md)).
- Admitting an endpoint now refuses an identity that overlaps an existing directory record, a three-token owner token equal to any legacy owner (in either order of provisioning), a three-token identity while a zoned legacy record exists (and the reverse), and a record the check cannot model; the refusal error names the overlap. Admission holds with or without a name-rule file and runs under a directory-wide lock. Legacy-only deployments are unchanged: legacy records are never compared with each other. Nothing writes a three-token record yet, so no operator sees a refusal until provisioning lands ([ADR 0074](docs/decisions/0074-an-endpoint-is-three-sap-tokens-and-keys-its-storage-on-their-flat-form.md)).
- The permission set mash will mint for a three-token identity is defined: publish to peers of its own owner under its own agent token (mail, prompt, status) and its own exact heartbeat, JetStream and KV lines keyed on the flat identity, and subscribe on its own prompt, status and `_INBOX.<flat>.>`. A configured extra grant is refused when it has a `*` or `>` at or before the agent token, a `>` that is not last, a partial wildcard or an empty token, or when it is for a heartbeat; a three-token identity takes no policy-compiled user. For an identity with an alias, the window bridge adds exact publish and subscribe lines for the legacy alias's mail, prompt, status and heartbeat subjects, and exactly six JetStream lines for the alias's legacy inbox stream and its `deliver` consumer (stream info, consumer info, fetch, ack, two direct-get forms), none wider. Legacy users' permissions are unchanged. Nothing mints such a user yet, so no credentials on the server change with this release ([ADR 0074](docs/decisions/0074-an-endpoint-is-three-sap-tokens-and-keys-its-storage-on-their-flat-form.md), [ADR 0076](docs/decisions/0076-a-three-token-record-may-carry-the-legacy-identity-it-answers-to-as-an-alias.md)).
- With `agent_token` set, `mash serve`, `mash hook` and the CLI verbs (send, read, inbox, who, prompt) run a three-token identity end to end: the lease key, the inbox stream (`inbox-<flat>`), the reply prefix, the directory key, the spool, relay-counter and state paths, the default creds file (`<flat>.creds`) and the signed sender name all derive from the flat form `agent_owner_name`, and the subjects carry the identity's own agent token in place of `cc`; the heartbeat is signed as the flat key. A recipient argument is a legacy name or a flat key. An open fails closed if the endpoint's own directory record is missing or unreadable, or the credentials are not the identity's. A verified presence answer marks only its own record online. Legacy identities keep every existing key and subject byte for byte. `mash admin` and every `mashctl` verb still refuse `agent_token`, and nothing provisions a three-token record yet, so a deployment cannot enable this until the provisioning change lands; peers older than this release cannot read the records ([ADR 0074](docs/decisions/0074-an-endpoint-is-three-sap-tokens-and-keys-its-storage-on-their-flat-form.md)).
- mashctl audit and mash status now report inbox messages evicted by a full inbox.

### Changed

- Release verification (`release-check`, the dry-run job and the documented consumer commands) no longer passes `--deny-self-hosted-runners`, so attestations made on our own self-hosted runners verify; they still pin the signer workflow, repository and ref, and they claim only that the maintainers' runner built the bytes (ADR 0087).
- The adr-lint workflow explicitly allows its data-only checkout of a pull request, which actions/checkout v7 otherwise refuses in a pull_request_target run.
- Every reader that indexes the directory by a record (who, the live-lease join, drift, label, retire, list, apply-limits) now uses the record's directory key, which is the name for every existing record, so a new-form record can never be mistaken for a legacy one of the same name. No output changes for existing records.
- The gate jobs (label-reset, adr-lint, and ci.yml's changes and pr-check) now run on small dedicated `[self-hosted, rh-gate]` runners, so the queue's gate checks no longer wait behind heavy test jobs.
- The per-endpoint directory label cap is now 64 (was 8); the key and value grammar and limits are unchanged, and records with more than 8 labels still read on older builds (ADR 0072).
- The release gate now prints the version and commit as `version: <v> commit: <X>`; it no longer prints a pin line for a downstream consumer.
- A three-token endpoint is shown as `agent.owner.name` (for example `bogworks.x.sap-go-sdk`) everywhere a person reads it: `who`, `inbox`, `read`, message headers, `send` receipts, `status`, hook text, error messages and `mashctl` drift. `send`, `prompt` and the directory lookup accept that form as well as the flat key. `who --names` and the `name` field of `who --json` stay flat, and `who --json` and `mashctl drift --json` gain an optional `identity` field for a three-token endpoint (a drift finding's `subject` stays the flat key), and `who --name` and `who --match` accept a canonical or flat three-token identity and match the full identity. Legacy endpoints are shown as before
  ([ADR 0081](docs/decisions/0081-three-token-identities-are-shown-in-canonical-form.md)).
- The session instructions now say a verified sender's requests are "delegated by the operator of this mash deployment" instead of "delegated by the operator who owns this fleet". The meaning is the same: verified mail is acted on without asking first, unless the operator policy says otherwise.
- mash is built and released with Go 1.27.4 (security fixes in the Go standard library, GO-2026-6859).
- Releases from the cutover version recorded in `docs/release-cutover` (v0.4.0) are keyless: the tag is annotated and unsigned, pushed by the minting service; there is no `SHA256SUMS.sig`; `SHA256SUMS` is an attested subject (it no longer lists `mash.intoto.jsonl`); every attestation check, and `plugin/scripts/pin-plugin --attested`, pins the repository, the release workflow, the tag and the commit. Releases below the cutover verify as before ([ADR 0095](docs/decisions/0095-releases-from-the-cutover-version-are-keyless-the-tag-is-pushed-by-the-minting-service-and-attestations-replace-the-signatures.md)).
- The architecture guide now says plainly that directory labels are unsigned metadata and must not be used for a security decision.
- SECURITY.md now describes the control plane (`mashctl`) and its key custody.

### Fixed

- `mashctl deprovision`, `mashctl retire` and `mash admin deprovision` of a legacy endpoint that a new-form endpoint names as its alias now refuse with one line that names both endpoints, says nothing was changed and says to retire or re-point the new-form endpoint first (exit 1), instead of the generic refusal text ([ADR 0076](docs/decisions/0076-a-three-token-record-may-carry-the-legacy-identity-it-answers-to-as-an-alias.md)).
- A drained removal (`Admin.DeprovisionDrained`) now accepts a new-form `agent_owner_name` key, where it used to refuse it as an
  invalid name. It writes the retired marker pending before the user is revoked and final after the record is purged, finishes
  a removal that stopped after the purge when it is run again, and so the name is never reassigned
  ([ADR 0088](docs/decisions/0088-a-retired-new-form-name-is-burned-in-a-durable-bucket-by-a-two-phase-marker-that-is-never-deleted.md)).
  `mashctl suspend`, `resume`, `suspended` and `deprovision` (and `Admin.Suspend`, `Resume`, `Suspended`) now accept a new-form
  `agent_owner_name` key too, so the drained removal can be reached by the supported commands; a legacy name works as before.
- The lease bucket is hardened: it no longer allows rollup writes, `mash admin init` applies this to an existing bucket, and the admin no longer purges lease keys (it deletes them by revision). Re-run `mash admin init` after upgrading: it hardens the lease bucket, and `mashctl audit` reports a bucket that has not been hardened.
- A legacy-stream message delivered just as the endpoint lost its legacy lease is now nak'd, so a retake gets it within about a second instead of after the 30 s ack wait. This also removes a rare failure of the retake test on loaded runners.
- A new `mash serve` of the same Claude Code session (after `/login`, for one) now takes the endpoint lease from the earlier serve at once instead of reporting the name in use and waiting about 10 seconds. The earlier serve learns of it within about a second, stops delivering and acknowledging mail (the new serve is redelivered what it held), steps down and says it was superseded, naming the new one; `mash status` shows the current holder on every retry; and while a session's serve waits for the lease, `mash hook` tells the session once (then at most every 10 minutes) that mail is off
  ([ADR 0092](docs/decisions/0092-a-new-serve-of-the-same-session-takes-over-the-endpoint-lease.md)).
- `mashctl suspend` reports the final last sequence: it waits for a publish accepted just before the park to land, so a drain to the reported sequence loses nothing.
- `mash send --via` and the send tool's `via` now work when the relayed message came from an endpoint with an agent, owner and name: the signed `Mash-Via` origin may be a legacy endpoint name or that endpoint's `agent_owner_name` key, and a legacy origin signs exactly as before.
- The lease-loss window in a legacy spool write is closed: a loss known at the end of the write is recorded in the same critical section as the entry, so no hook read shows it. A loss after that point can still show the message once, which is a duplicate and never a loss.
- `mash read` and message digests now show terminal control characters in a message body as visible `\xNN` escapes instead of passing them to the terminal.
- `mashctl retire` now refuses a user whose creds claim to be issued in the future, as reissue already does, because the revocation would not cover it.

## [0.3.3] - 2026-11-03

### Added

- `Mash-In-Reply-To` is now signed (`<seq>.<sig>`, its own domain `MASH-IN-REPLY-TO-V1`), and a message can declare that it relays or quotes a message of the sender's own inbox: the MCP `send` tool takes an optional `via` (a #seq in your inbox), sent as a signed `Mash-Via` header. A recipient sees `answers #N` and `relayed from <sender> #N` lines (and tab fields in digest and one-line forms) only when the signature verifies for that message and the sender is verified (or stale); the old unsigned in-reply-to from an older sender, a forged or moved mark and a claimed sender's marks are ignored. A `via` naming a message that is not in your inbox fails and sends nothing. Neither mark changes a trust label or wakes anything; output without them is unchanged. See [ADR 0063](docs/decisions/0063-in-reply-to-is-signed-and-a-sender-can-declare-a-relay-with-a-signed-mash-via-both-are-shown-only-when-they-verify.md).
- Relay-by-asking is traced, not blocked (roadmap N14): `mash serve` logs `trace relay:` lines when an inbound verified mail declares a relay (`Mash-Via`), when a send declares one, and when a send goes to a third endpoint within `relay_window` (default `60s`, `config.json` or `MASH_RELAY_WINDOW`, 1s to 1h) of another endpoint's mail. `mash status` shows `relay trace: inbound with via N, sends within <window>s of another sender's mail N, declared relays N` (per serve run; from a shell it is read from `relay-counters.json` in the endpoint's state directory, with `(since <time>)`, or `none recorded`). Nothing is blocked or delayed and no message body is read: only headers, names and timing. A candidate proves adjacency, not causation. See [ADR 0064](docs/decisions/0064-relay-by-asking-is-traced-not-blocked-content-free-trace-lines-and-counters.md) and "Relay tracing" in the operations guide.
- `mashctl adopt --server URL [--account NAME] --operator-key F --account-key F --sys-creds F [--admin-creds F | --mint-admin] [--owner O] [--dry-run]` takes over a NATS server you already run in operator mode with a full resolver and JetStream. It requires the authority to exist (the operator signing seed, the mash account's identity seed, system account creds, all 0600 files, never printed) and refuses without it. Read-only checks come first and nothing is written until they pass: the seed files, a server that refuses anonymous connections and answers the system account, the account JWT on the resolver (subject, name, expiry), JetStream file storage and its limits, the server set, and whether the operator key issued the account. It then imports the keys into the key store (without replacing a different key), copies the system creds, records or mints admin creds, and writes `config.json` and an open `policy.yaml` only if absent; `--dry-run` prints what it would do and a second run changes nothing. It changes nothing on the server. See [docs/operations.md](docs/operations.md#adopting-an-existing-server).
- `mashctl policy drift [--policy F] [--sys-creds F] [--creds-dir D] [--json]` compares the policy file with the account JWT's zone keys and templates, the key store's zone key files, the directory records (zone, labels, mail subject) and the user creds files on disk (the compare `reissue --changed` uses), reports each state as `ok` or a list of findings, changes nothing, and exits 0 only when all four match. `mashctl policy repair` makes the additive repairs: missing zone key files and the account push (the same code path and refusals as `apply`, which stops the repair), a record's zone and labels, and it lists stale creds with the `mashctl reissue` command without running it. It never revokes a credential or deletes a key file, record or creds file, and it is not a transaction. Credentials minted by other tools are not seen. See [docs/operations.md](docs/operations.md#checking-and-repairing-drift).
- `mashctl suspend NAME`, `mashctl resume NAME` and `mashctl suspended NAME` (also under `mash admin`) stop an endpoint's inbound mail from every sender so its inbox can be drained to a known end before `deprovision` (#130, ADR 0057). Stored mail stays readable; `suspended` exits 0 when suspended, 3 when not and 1 on error, and `suspend` prints the final `last_seq`. Mail sent meanwhile is refused, not queued; a sender cannot tell "suspended" from "no inbox".
- `mashctl policy apply [--dry-run] [--policy FILE] [--sys-creds FILE]` and `mashctl policy diff` push the policy to the MASH account: one scoped signing key per zone (created in the key store when missing), the rest of the account JWT preserved, signed with the operator key, pushed through the system account's claims update, then read back and compared; `diff` and `--dry-run` change nothing and show, per zone, the permission lines added and removed and the account JWT size against its limit. A second apply pushes nothing. A zone the server has but the file lacks is reported and kept, never removed. Apply is not a transaction: an unacknowledged push or a read-back that differs prints `PARTIAL` with what was and was not done and exits 1. See [docs/operations.md](docs/operations.md#applying-the-policy).
- `mashctl init --local [--dir D] [--owner O] [--port P] [--start]` stands up a local operator-mode NATS server with JetStream for one developer: operator, system and MASH account keys in the `mashctl` key store, the account and user JWTs, `mashadmin.creds` and `sys.creds` (0600), a `server.conf` listening on 127.0.0.1 only (no TLS), an open `policy.yaml` and a `config.json` if none exists (an existing one is left alone and the settings to add are printed). It refuses and changes nothing if the target directory already holds any of its files. `--start` starts `nats-server` detached and creates the directory and lease buckets. Plain `mashctl init` is unchanged. See [docs/operations.md](docs/operations.md#local-setup-with-mashctl-init---local).
- Mail subjects carry a zone token (`agents.mail.<kind>.<owner>.<zone>.<name>`), the one label the policy file (`subject_label`) can name: `mashctl set-zone <name> <zone>` moves an endpoint into a zone and `mashctl provision --zone Z` places a new one. Mail to the legacy subject (no zone) still arrives, and an endpoint without a zone keeps it ([ADR-0054](docs/decisions/0054-mail-subjects-carry-a-zone-token-legacy-subjects-stay-valid.md)). The 0.3.2 release did not carry these.

### Changed

- `mash serve` no longer listens to every other endpoint's heartbeat; the `who` tool asks who is online when it is called ([ADR 0066](docs/decisions/0066-mash-serve-asks-who-is-online-when-someone-asks-instead-of-listening-to-every-heartbeat.md)). At 1,000 endpoints that removes about 33,000 deliveries per second from the servers (measured with `scripts/dirbench -hb-mode`). The `who` tool sends one `$SRV.INFO.agents` request, counts the registrations that verify, and reuses the result for 30 seconds (at least 5 seconds between probes after a failed one); it takes about a third of a second when it has to ask. An endpoint is online when it answered the latest probe, so one that stops is offline at the next `who` instead of 90 seconds later. `last_seen` in `who --json` and `seen X ago` now mean the last answer to a presence probe, not the last heartbeat. Heartbeats are still published every 30 seconds, and no permission changed.
- A running endpoint's directory poll reloads only when the directory changed (issue #43, [ADR 0065](docs/decisions/0065-a-directory-poll-reloads-only-when-the-directory-stream-moved-and-is-jittered.md)): each poll is one small unfiltered stream-info request, and the filtered stream info plus one direct get per record happen only when the stream's creation time, message count, last sequence or last time moved, or when `DirectoryFullReload` (20 minutes, jittered per endpoint) is due. Poll waits are jittered plus or minus 25 percent and the first poll is at a random offset, so endpoints do not poll together. Changes still reach a running endpoint within about 15 s; an idle directory no longer costs one direct get per record per endpoint per poll. Endpoint permissions are unchanged. `scripts/dirbench` gains `-mode old|new` to measure it.
- `mashctl` says which server it is about to act on (#183): before every verb that connects to a server or changes one it prints `mashctl: target server <url> (from <source>), owner ... , admin creds ...` to stderr, warns when a `MASH_*` environment variable overrides a different value in `config.json`, and warns when `MASH_NAME` or `MASH_LAUNCH_TOKEN` shows the shell is inside a mash session whose own server the url is. Nothing is refused and the precedence is unchanged; offline verbs print nothing. The `--server` and `--config-dir` flags the issue also asks for are deferred to the CLI redesign (the CLI is frozen); until then use `env -u MASH_URL ...` or run outside the session. See "Which server does mashctl change?" in the operations guide.
- A rule with `log: true` is now a `policy check` error (`rule NAME (line N): log is not implemented yet; remove it`): the field never did anything. `log: false` and a missing `log` are fine ([ADR 0062](docs/decisions/0062-log-is-rejected-until-implemented-check-runs-the-budget-case-variant-labels-warn-usage-errors-exit-2.md), issue 185).
- `mashctl policy check` also runs the JWT size budget and fails (exit 1, the text `show --budget` prints) when the account JWT would exceed 1 MiB; it used to print OK for a file that `apply` refused.
- `policy check` warns when label values for one key differ only in case across endpoints (`Finance`, `finance`), naming the key, both spellings and their lines.
- Every `mashctl` verb now exits 2 for a usage error and 1 for a failure; before, `suspend`, `resume`, `deprovision`, `provision`, `permissions` and `set-zone` exited 1 for a usage error. An unknown flag or stray argument to `init`, `list`, `provision`, `permissions` and the other verbs is now a usage error (it was ignored). `mash status` and `mash version` refuse extra arguments (exit 2), and `mash VERB --help` prints the verb's usage line and exits 0 without running it.
- `mashctl deprovision NAME` for a name that has no record and no inbox prints `no endpoint named NAME` to stderr and exits 1 instead of `deprovisioned`.
- `mashctl permissions NAME` prints a note on stderr that its lines are for hand-made legacy users and that endpoints added under a policy file get narrower permissions; stdout is unchanged.
- An argument that fails validation (an endpoint name, a `--user` or `--account` key, a label key, a zone, an unknown flag's value) is named in the error but no longer printed back, so a seed pasted into the wrong place does not reach the terminal.
- `mashctl adopt` connect-tests the admin creds it ends with before it prints "All checks passed" (#178): a `--mint-admin` user is minted in memory and connected with in the read-only phase (so `--dry-run` reports it and a failure writes nothing), an existing `mashadmin.creds` that cannot connect is refused instead of called valid, and `--admin-creds` was already tested. On an operator with strict signing key usage `--mint-admin` is refused with a clear message (the account identity key cannot sign users); pass `--admin-creds FILE` with a user signed by an account signing key. The summary line says which creds connected (given, minted or kept).
- The policy parser refuses a map key that is not a plain string (for example an unquoted null, ~, number or boolean), with the line number; quote such names.
- `refused by policy: you may not mail <name>` now adds a second sentence: if `mashctl policy explain` allows the pair (or the recipient is not in the policy file), the sender's credentials may be older than the rule, so ask the admin to run `mashctl policy drift`, then `mashctl reissue`. A permission violation is a definite refusal only when exactly one send is waiting on that subject; with several in flight none is refused (delivery stays unknown), each ends as the usual timeout, and that timeout says an unattributed violation was seen. After a send that ended in a timeout, the next send on that subject first runs a flush barrier and a short settle (at most 25 ms) so a violation delayed from the earlier send is not taken for its own; other sends on the subject wait for the barrier, and if it fails that send keeps timeout semantics. When one of several waiting sends times out, a violation can no longer refuse the sole remaining one (it ends as the ambiguous timeout). The refusal now ends with `If you retry, use --id KEY (the send tool's id): it sends at most once within <window>.`, so a retry after a rare false refusal is harmless (#182).
- `mashctl retire` now renames `NAME.creds` to `NAME.creds.retired-<UTC yyyymmddThhmmssZ>` by default (mode kept, reported in its output) so the name can be added again at once; `--delete-creds` still deletes it (issue #184).
- `mashctl add`, a plain `mashctl retire` and `mashctl policy drift` (`record-extra`) say, for a record or creds file with no policy entry, that it looks like an interrupted add and give the exact fix: `mashctl retire NAME --force --delete-creds`, then `mashctl add NAME` (issue #184).
- The policy file lock file (`policy.yaml.lock`) is removed when the command finishes, and a leftover from a killed run is reused; the lock was always released by the kernel on exit, and the lock error now says a leftover file is harmless (issue #184).
- `mashctl add` and `mashctl reissue` run the policy file's `tests:` as `policy apply` does and refuse, with the same `FAIL` lines and `refusing: N of M policy tests in FILE fail (...)`, before any key, record or creds file is created or rewritten (#180, ADR 0060).
- A deny rule whose `from` or `to` selector matches no endpoint is now a `policy check` error, not a warning (a misspelled label fails open under `default: allow`). Set `allow_empty: true` on the rule if the group is intentionally empty and it is a warning again; allow rules keep the warning. The key has no effect on permissions (#180, ADR 0060).
- `policy check` validates the zone label value as a zone (1 to 32 characters, ADR 0054), not as a name (48), so it no longer accepts a zone that `add`, `set-zone` and `apply` refuse. Endpoint names stay 1 to 48 (#180).
- The release gate and the dry run now prove which `slsa-github-generator` commit signed `mash.intoto.jsonl`: every line's Fulcio certificate must carry the commit recorded in `SLSA_GENERATOR_SHA` (`release.yml`, read at the release commit) as its Build Signer Digest and the generic generator at `SLSA_GENERATOR_TAG` as its Build Signer URI, so a tag moved and restored around the run is caught (issue #108). See [docs/release.md](docs/release.md#sbom-and-slsa-provenance).
- `mashctl add` and label edits refuse names and label keys the policy parser would refuse, such as `null` or `true`, up front with a clear error, before anything is created or written.
- The next ADR number is a file, `docs/decisions/NEXT`: a pull request that adds an ADR takes the number it names and bumps it, and `adrlint docs`, run by CI on every event and on the merge queue's combined commit, refuses a stale `NEXT` or a number used twice ([ADR 0070](docs/decisions/0070-the-next-adr-number-is-a-file-next-and-the-lint-refuses-a-duplicate-or-stale-number.md)).
- A session whose hook is draining its held mail is no longer woken on every settle: a crowded backlog (500 messages or half the byte limit) wakes at once unless a hook drained within the idle wake and the drain is making headroom, meaning the backlog has not grown since it, the held size is under half the inbox byte limit, the held count is under 750 (3/2 of the wake count, whatever the growth check saw), and the inbox stream itself is under half its byte limit (read from the stream, at most every 5 seconds; unreadable counts as full). The idle spell runs from the last drain, so a shrinking crowded backlog waits one idle wake after a drain stops ([ADR 0069](docs/decisions/0069-a-crowded-backlog-does-not-wake-a-session-that-a-hook-is-draining.md)).
- `mashctl add` to an existing zone now verifies, with the system account credentials (`MASH_SYS_CREDS`), that the zone key is published in the account JWT as a scoped signer, and refuses otherwise (#220).
- A null item in a list in the policy file (`rules:` then `-`, `- ~`, `- null`) is now refused with its line, instead of being silently dropped.

### Fixed

- The user JWT budget is 3,750 bytes (was 4,000), so a JWT that passes `policy check` can connect at the default control line (#179); about 135 tags fit.

### Security

- `mashctl policy apply`, `repair`, `revoke`, `retire` and `deprovision` build their update from the newest account JWT any server holds (#176): they ask every known server, verify every reply's issuer, take the copy with the highest `iat` (never the first reply), and refuse when two different copies share the newest `iat`. A server that missed an earlier push no longer brings a removed signer or revocation back; older copies are reported (`N of M servers hold an older copy (iat ...); building from the newest`) and the push brings every server to the newest. The base is checked again just before the push, and the read-back compares every answering server with the pushed JWT (a server still holding another copy makes the run `PARTIAL`). The resolver has no conditional update, so a writer outside mashctl between the final re-check and the push is still overwritten: only one writer should hold the operator key and use `nsc` or other tools on the account while an apply runs (see "Applying the policy"). `mashctl policy diff` now exits 1 when it refuses (a conflict, an unknown signing key, an unacknowledged zone outside the policy, a replaced zone key, an unverifiable issuer, ambiguous server copies) and 0 when it only reports differences. A zone name longer than 32 characters is already refused by `policy check` (pinned by a test).
- `mashctl policy apply`, `diff`, `repair`, `drift` and `adopt` verify the account JWT's issuer: its signature must be valid, its subject the account key, and its issuer the operator key or a key declared by the operator JWT (`<key dir>/operator.jwt`, which `init --local` records). `adopt` now refuses where it warned. Apply only adds or removes mash's own zone keys: any other signing key in the account JWT is refused (named by public key and role; `diff` and `drift` report it): mashctl will not carry signing keys it did not create, so they must be removed with the tool that added them. Admin and system connections require TLS: a plain `nats://` URL to a non-loopback host is refused, and a local server made by `init --local` opts in with `insecure_loopback` in `config.json` (or `MASH_INSECURE_LOOPBACK=1`); `init --local --start` connects only to the loopback server it just started and needs no opt-in. See [ADR 0059](docs/decisions/0059-mashctl-checks-who-issued-the-account-jwt-carries-only-its-own-signing-keys-and-requires-tls-for-admin-connections.md).
- `mashctl reissue` no longer leaves a narrowed endpoint's old credentials valid (GHSA-xr7f-xmcm-w98h): a reissue that removes a tag, adds a deny tag, or changes signer or name mints a new user key and revokes the old one. Every reissue now checks the endpoint's directory record, refuses a creds file whose key the record does not name or the account JWT revokes, and needs the server and the system account credentials (ADR 0067).

### Docs

- mashctl docs: `mashctl policy apply` and `mashctl reissue` named correctly, what `init` and `adopt` create and write (`tls_ca` included), and a "Before you push with nsc" section (`nsc pull -A`, `nats://` and `SSL_CERT_FILE`, revocation, rotating a zone key, suspended endpoints); `policy.md` is called the session instruction file, not the policy file.

## [0.3.2] - 2026-11-02

### Added

- `internal/policy`: the policy file (labels, allow and deny rules, a `subject_label` naming the zone, tests) parsed strictly, checked, explained and compiled to one scoped signing key per zone (wildcard lines per allowed zone pair, `{{name()}}` fixed lines) plus `allow:`/`deny:` tags on each endpoint's user for exceptions, with a budget for the account JWT (independent of the number of endpoints) and each user JWT; `admin.ZonePermissions` and `admin.PermissionsTo` build the permission lines. The zone token in the subject lands separately. Not wired to any command yet; see docs/policy-file.md.
- Releases carry a CycloneDX 1.5 SBOM per binary (`mash-<os>-<arch>.cdx.json`, attested and listed in the signed `SHA256SUMS`) and SLSA provenance (`mash.intoto.jsonl`) from the `slsa-github-generator` generic generator, and the publish gate verifies all of them before a release is published. How to verify a download is in [docs/release.md](docs/release.md#verify-a-release) ([ADR-0052](docs/decisions/0051-releases-carry-an-sbom-per-binary-and-slsa-provenance.md)).
- `mashctl`, the admin tool: every `mash admin` verb (init, limits, permissions, provision, set-meta, deprovision, list) is now `mashctl <verb>`, with the same arguments and output. `mash admin` still works as a deprecated alias and prints one line to stderr saying so. `mashctl` is built from source for now (`go install github.com/bogworks/mash/cmd/mashctl@latest`) and is not yet in the release assets.
- The directory record gains `description` (free text, up to 256 characters) and `caps` (up to 8 capability words), set by the admin: `mash admin provision --describe TEXT --cap C...` and `mash admin set-meta --describe TEXT|- --cap C|- --uncap C`. `mash who --cap C` (repeatable, exact, all must be present) finds endpoints by capability, `--match` also searches the description and capabilities, `--long` and `--json` show them, and the `who` tool gains a `cap` parameter. Older readers ignore the new fields ([ADR-0050](docs/decisions/0050-the-directory-record-says-what-an-endpoint-does-with-a-description-and-capabilities.md)).
- `mash who` filters and machine-readable output: `--match TEXT` (name, role, note, labels), `--name`, `--role`, `--owner`, `--label K[=V]` (repeatable), `--online`/`--offline`, `--names` and `--json` (a documented array; `--long` adds `lease`). Filters combine with AND, no match exits 0 with empty output, a bad flag exits 2, and the unfiltered output is unchanged. The `who` tool gains optional `match`, `name`, `role`, `label`, `online` and `format` parameters that use the same filter (see [docs/operations.md](docs/operations.md#finding-endpoints-who-filters-and---json)).
- `mash admin deprovision <name> --user <public-key>` removes the directory record and the inbox only if the record's user is still that NATS user public key, and refuses otherwise, leaving the record, inbox and lease untouched. The record is deleted against the revision that was read, so one replaced in between is never removed; a metadata-only change by another admin in between is retried on the new revision and does not refuse. With no record it refuses, even if an inbox stream exists. Without `--user` nothing changes (issue #9). Provision and both forms of deprovision hold the per-name lock for their whole run, so another admin cannot provision the name between the record delete and the lease fence and lose its new endpoint's inbox.
- `version` and `status` show the Claude Code client (`client: Claude Code <v>`) from the MCP `initialize`; `status` warns (does not refuse) when it is older than 2.1.312, the oldest version a live receipt for the current delivery design passed on, and both tools warn when the plugin's and the binary's major.minor differ. See Requirements in [README.md](README.md) and [docs/troubleshooting.md](docs/troubleshooting.md).
- Boundary tests for the limits that meet at the inbox and the hook (#48): a message at exactly 1 MiB is accepted and one byte over is refused; 15 messages of 1 MiB to an offline endpoint all survive and read back byte for byte, and past the 16 MiB cap the oldest are evicted and the newest kept; emoji, CJK and mixed batches at and past Claude Code's 10,000-unit hook cap are never cut mid-pair or truncated; a burst of 600 `fyi` past `SpoolWakeCount` wakes once, an urgent message behind it is shown first, and the byte backlog wake is exact at `SpoolWakeBytes`. Tests only.

### Changed

- `mash send` and the `send` tool fail at once with `refused by policy: you may not mail <name>` when the server refuses the publish by permissions, instead of waiting out the timeout and reporting that the message "may or may not have been delivered". The refusal is not a timeout (exit code 1, nothing to retry). It needs a sender whose permissions omit the recipient's mail subject; see "Refused sends" in [docs/operations.md](docs/operations.md).
- `quickstart-synadia.sh` cleans up a failed provision with one guarded `mash admin deprovision <name> --user <key>` call instead of listing and then deprovisioning, which closes the window in which another admin's record could be removed.
- A digest no longer acks what it lists: a message the hook only listed (a batch past 10 messages or 16 KiB) stays unread in the inbox until the model calls `read` for its `#seq`, or a later hook run shows it in full, and it never wakes the session or counts in `waking_pending`. The digest says so ("Read each with the read tool (#seq); they stay unread until you do."). After a restart, unread listed mail is listed again once. Amends ADR-0026 and ADR-0024 ([ADR-0046](docs/decisions/0046-a-digest-lists-mail-it-does-not-ack-it.md), #44).
- mash authenticates with operator-mode JWT user credentials only. A creds file with no JWT or seed is refused before any network call with `<path> is not operator-mode JWT credentials (...)`, for endpoints and `mash admin`; `status` and serve warn (`auth: WARN`) when the url carries a password or token (the secret is never printed). The requirement is now stated in the README, [docs/install.md](docs/install.md), [docs/troubleshooting.md](docs/troubleshooting.md) and [docs/faq.md](docs/faq.md).
- Docs: [docs/comparison.md](docs/comparison.md) is one footnoted matrix with an SVG of the core columns, and every diagram in the README and docs is redrawn as SVG in one house style (`docs/images/`).

### Fixed

- Two admins changing the limits of one inbox at the same time can no longer leave the lower cap (and evict mail). `mash admin limits` and `mash admin provision` take a per-stream lock (`limits-lock.<name>` in the lease bucket, 30 s expiry) before reading the stream and hold it through the update; a second admin waits up to 20 s, then fails with `mash: another admin is changing the limits of <name> (lock held until <time>); try again`. Admins' clocks should agree to within a few seconds (see "Changing limits later" in [docs/operations.md](docs/operations.md), [ADR-0048](docs/decisions/0048-limit-changes-take-a-per-stream-kv-lock-with-expiry.md), #27).

## [0.3.1] - 2026-11-01

### Added

- `mash status` prints what a session would see (name, config, creds, channel allowlist, environment, version) from a shell, before Claude Code is launched. It shares the `status` tool's text, connects to nothing, works with `MASH_NAME` unset (printing `name: MISSING`), redacts the same secrets, and says the connection and delivery lines are only known inside a running session. It always exits 0.
- Inbox, bucket and consumer limits are configurable in the `limits` section of config.json, with the defaults unchanged. `mash admin limits [name...]` raises existing inboxes in place and `--show` prints the effective limits. mash never lowers a limit, because that could discard mail; a lower value is refused, naming the endpoint and field, and an unlimited stream is never given a finite cap. `mash admin init` raises a bucket cap that is below the configured one (see "Limits" in [docs/operations.md](docs/operations.md)).
- Build provenance: the release binaries carry a GitHub attestation, and the publish gate verifies it for all four binaries before a release is published. Check one with `gh attestation verify mash-linux-amd64 -R bogworks/mash` ([docs/release.md](docs/release.md)).
- `MASH_CONFIG_DIR` and `MASH_STATE_DIR` name the config directory and the state directory (`spool/` and `hook.log`) exactly: absolute paths, a relative one is an error naming the variable. `MASH_CONFIG_DIR` wins over `XDG_CONFIG_HOME` and the legacy `~/.config/mash`; the state directory otherwise defaults to `$XDG_STATE_HOME/mash` (absolute only) or `~/.local/state/mash`, as before. The plugin hook and `quickstart-synadia.sh` follow, so `--config-dir` no longer has to end in `.config/mash` ([ADR-0044](docs/decisions/0044-config-and-state-directories-are-relocatable.md)).
- Docs: [docs/faq.md](docs/faq.md) and [docs/troubleshooting.md](docs/troubleshooting.md).
- `tls_ca`, `tls_cert` and `tls_key` settings (config.json, or `MASH_TLS_CA`, `MASH_TLS_CERT`, `MASH_TLS_KEY`) for a server with a private CA or one that requires a client certificate; `tls_cert` and `tls_key` must be set together. `mash status` warns on a plain `nats://` url to a non-loopback host, and a server that closes right after the TLS 1.3 handshake gets a hint to check the client certificate settings (see [docs/operations.md](docs/operations.md#troubleshooting)).
- `mash status` prints a `clock:` line and warns when this machine's clock differs from the server's by more than 10 s (live requests fail past 30 s; see [docs/operations.md](docs/operations.md#troubleshooting)).
- `lease_retry`, `transient_budget`, `transient_max`, `cli_timeout` and `admin_timeout` settings (config.json; the `MASH_*` variable wins), with the defaults unchanged. The timings that stay fixed are listed in "Fixed protocol constants" in [docs/operations.md](docs/operations.md).
- `MASH_MANAGED_SETTINGS_DIR` lets `mash status` read Claude Code managed settings from a non-standard install.
- For contributors: a portable-code lint with a ratchet baseline that can only fall (see "Portable code" in [CONTRIBUTING.md](CONTRIBUTING.md), [ADR-0045](docs/decisions/0045-product-code-carries-no-personal-convention-a-ratchet-lint-enforces-it.md)).

### Changed

- A `claude --resume` of a still-running session (same session id; a new `MASH_LAUNCH_TOKEN` is not required, a waiting serve records itself in the spool) no longer drains the lease holder's spool: its hooks are refused, and it is told once in its own session that it is not receiving mail ([ADR-0043](docs/decisions/0043-second-launch-of-a-running-session-is-refused-and-told-once.md)).
- The config directory is `$XDG_CONFIG_HOME/mash` when `XDG_CONFIG_HOME` is absolute and holds a config.json, else `~/.config/mash` as before; `policy.md` and default creds files follow it, falling back to `~/.config/mash` when the XDG directory has no copy (the XDG copy wins when both exist).
- `mash admin provision` refuses to lower the caps of an inbox stream that already exists (a retried provision no longer shrinks an inbox the limits command had raised).
- While wakes are not landing, the endpoint holds a message the spool will not take instead of writing it to the channel, and `status` says whether the last wake started a turn (#40).
- `mash plugin export` derives the marketplace owner from the embedded plugin.json author; the output for the shipped plugin is unchanged.
- Docs: neutral wording for a public repo, and redacted evidence.

### Fixed

- With no `HOME` and no absolute `XDG_CONFIG_HOME`, mash fails with a clear error instead of reading a config path relative to the working directory (everything from the `MASH_*` variables still loads). A leading `~/` or bare `~` in `creds` and `admin_creds` (config.json, `MASH_CREDS`, `MASH_ADMIN_CREDS`) is expanded to the home directory (#42).

## [0.3.0] - 2026-11-01

Changes since 0.2.10.

### Changed

- Mail is delivered by hook. When mash's hooks are installed, the mail arrives as "mash mail" context after a tool call or with a prompt, and the channel event only wakes an idle session with a one-line summary.
- A backlog of `fyi` or `result` mail wakes the session to drain it, so waking mail queued behind it still arrives. A fresh session wakes at once for mail that arrived before its first hook, under spool-from-start.
- The sender of a message is shown by its endpoint name.
- A creds file that other users can read is refused. The file must be mode 0600.
- The directory records an endpoint's role, host and free-form labels (`--label key=value`, up to 8, repeatable on `mash admin provision` and `mash admin set-meta`). `mash admin list` is unchanged.
- Hosted NATS servers are supported better: every KV bucket carries a size cap (an older uncapped bucket is bounded in place by `mash admin init`), the send size cap follows the server's `max_payload`, and `open_timeout` and `call_timeout` suit slow links.
- Each inbox keeps mail for 30 days or 16 MiB, and a consumer is limited in outstanding messages; a backlog of half the consumer's limit wakes the session to drain (see "Inbox limits" in [docs/operations.md](docs/operations.md)).
- `mash serve` starts in 7 round trips, and CLI commands use under 6 NATS round trips, flat in the size of the fleet.
- A message's shown stream sequences are remembered for the session, and only pruned below the consumer's confirmed ack floor, so a redelivery cannot show twice.
- `mash prompt` replies come back on unguessable inboxes, so a spoofed answer is refused.
- mash refuses to sign a connect nonce that did not come from the server (signature oracle).
- The MCP server answers `initialize` with a protocol revision mash supports and never echoes the client's.
- Docs: a host crash or power cut can lose up to about 2 minutes of acknowledged mail unless the NATS server sets `sync_interval: always`; process restarts are safe. See [docs/operations.md](docs/operations.md#durability-and-sync_interval).

### Added

- `mash send --id KEY` makes a send at-most-once across retries. A timed-out send prints the key to retry with.
- The lease record carries `waking_pending` and `waking_pending_at`.
- MCP `version` and `status` tools show the binary, the plugin and the endpoint a session is talking to, and what is missing in a mis-configured mode.
- A Synadia Cloud free-tier quickstart: [docs/synadia.md](docs/synadia.md), with `scripts/quickstart-synadia.sh` (ADR 0038).
- Docs for outside readers and contributors: [SECURITY.md](SECURITY.md) (private vulnerability reporting), [CONTRIBUTING.md](CONTRIBUTING.md), [CODE_OF_CONDUCT.md](CODE_OF_CONDUCT.md), and the architecture decisions as ADRs in `docs/decisions/`. Issue and pull request templates.
- Release process: a publish check and a release gate that refuses to tag while an earlier release is unpublished, and a mail-latency gate against the previous release.

### Removed

- The `--herd` directory field and the legacy herd read. Use `--label key=value` instead.
