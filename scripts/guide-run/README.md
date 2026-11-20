# guide-run: a newcomer run of the walkthrough

`guide-run.sh` follows `docs/guide/walkthrough.md` in one container, from
nothing to two agents mailing and then an endpoint deleted and its credential
refused. It records the real output of each command, and `fill` puts those
recordings into the guide's output blocks. Outputs are captured, never typed.

## Venue rules (ruled by x-lead)

- This host only. Rootless podman. Nothing installed on the host, no sudo.
- The official `debian:stable` image only; its digest is in the receipt.
- One container and one image per run, named after the run id, removed on
  exit by the recorded name and id. No globs. Run `guide-run.sh` only by its
  own name; it never kills by pattern.
- Non-root inside (`tester`). No ports published. `--network none`: all inputs
  go in at build time or by `podman cp`. Nothing is aimed at the LAN or the
  tailnet.
- Build time may use the distro mirror and the pinned natscli download (the
  same version and sha256 pin as `mash-local-nats-setup_test.sh`).
- More than 20 GB free on the host before a run, or it refuses.

## Modes

Rehearsal only. `--release-dir DIR` is a local folder that looks like a
release, with exactly the files `install.sh` and the setup skill expect:
`SHA256SUMS`, `install.sh`, `mash-local-nats-setup.sh`, `mash-linux-amd64`,
`mashctl-linux-amd64`. The run copies it into the container and runs the
published commands as a newcomer would:
`MASH_VERSION=<v> MASH_RELEASE_BASE=file://<dir> MASH_INSTALL_ALLOW_FILE=1
bash install.sh`, after the checksum check of `install.sh` against the
folder's `SHA256SUMS` (`docs/install.md`). A folder that lacks a file is
refused, naming what is missing.

## How to run

```
bash scripts/guide-run/guide-run.sh --make-release-dir /path/rel [--version v0.0.0-rehearsal]
bash scripts/guide-run/guide-run.sh --release-dir /path/rel [--version V] \
    [--scratch DIR] [--report FILE]
bash scripts/guide-run/guide-run.sh fill RUN_DIR [--guide FILE]
bash scripts/guide-run/guide-run.sh selftest
```

- `--make-release-dir` builds static linux/amd64 `mash` and `mashctl`
  (`CGO_ENABLED=0`, `-trimpath`, the version stamped in), copies the two
  scripts and writes `SHA256SUMS`. `--version` must match on the run.
- The run prints `ok:` and `FAIL:` lines and their totals, writes the receipt
  (default `RUN_DIR/receipt.md`) and prints `run folder: ...`. It exits
  non-zero on any FAIL. Wall time is in the receipt. If every assert passes
  it writes `RUN_DIR/assert.ok`.
- `fill` refuses a run folder without `assert.ok`, and any recorded output
  that holds a seed, a credential or a JWT. It fills only the ids whose step
  succeeded (`out/<id>.ok`); every other block stays as it is. It needs
  `python3` on the host.
- `selftest` needs no podman. `go test ./scripts/ -run GuideRun` runs it with
  `bash -n` and `shellcheck`.

The run folder keeps, for each id, `out/<id>.txt` (stdout and stderr
together), `out/<id>.cmd` (the command lines), `out/<id>.rc` (the exit
statuses) and `out/<id>.ok` when the step did what it should.

## Block ids

Each id is a hidden comment `<!-- output: ID -->` in the guide, directly above
a fenced block or above an `Output:` line holding a placeholder. `fill` rewrites
the block (or the placeholder) with the recorded output, keeping the comment,
so it can run again. It normalises the home folder to `/home/alice` and strips
trailing spaces; timestamps stay as captured. A captured line over 80
columns is left as it is and reported.

| id          | guide step | what is recorded                                      |
|-------------|------------|-------------------------------------------------------|
| install     | 2          | checksum check, `install.sh`, `mash version`, `mashctl version` |
| server      | 3          | checksum check, the setup script `--owner acme`, then the `mashctl init --config` line it printed last (taken from its output) |
| endpoints   | 4          | `mashctl endpoint create` for bob and alice           |
| status      | 4          | `mash status` as bob and as alice                     |
| send        | 5          | the two `mash send` commands                          |
| inbox       | 5          | alice's `mash inbox`                                  |
| read        | 5          | alice's `mash read 2`                                 |
| json-send   | 7          | `mash send --json "ping"`                             |
| json-read   | 7          | `mash read 3 --json`                                  |
| json-inbox  | 7          | `mash inbox --json`                                   |
| json-jq     | 7          | `mash read 3 --json \| jq -r .body`                   |
| delete      | 8          | `mashctl endpoint delete acme.demo.bob --yes`         |
| refused     | 8          | `mash status` as bob after the delete (exit non-zero) |
| lifecycle   | 10         | the setup script `--stop`, `--start`, `--remove`      |

`delete` uses `--yes`, which `spec/mashctl.json` offers in place of typing
the address on a terminal. `--remove` needs a terminal: the run uses
`podman exec -t` and answers the prompt with the folder through a pty helper.
Mail is awaited with a bounded retry (30 tries, one second apart).

## What it does not prove

- The Claude Code steps (guide step 6). There is no model here. The block id
  `claude-code` is not recorded and `fill` leaves it alone: it needs a
  recorded manual session. NOTE: the step 6 transcript is still a placeholder.
- A real macOS machine.
- The published-release mode (the real download from GitHub, `gh attestation
  verify`): not built yet. The run installs from a local folder, so the
  binaries are the ones built from this checkout, not the attested release.
- `mash join` (step 9): not built (S5), so there is no step for it.
- Anything a container cannot show: a reboot, other users on the host.
