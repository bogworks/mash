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
  go in at build time, by `podman cp`, or (published mode only) through the
  egress proxy's socket. Nothing is aimed at the LAN or the tailnet.
- Build time may use the distro mirror and the pinned natscli download (the
  same version and sha256 pin as `mash-local-nats-setup_test.sh`).
- More than 20 GB free on the host before a run, or it refuses.

## Modes

**Rehearsal** (`--release-dir DIR`): no network at all. `--release-dir DIR` is a local folder that looks like a
release, with exactly the files `install.sh` and the setup skill expect:
`SHA256SUMS`, `install.sh`, `mash-local-nats-setup.sh`, `mash-linux-amd64`,
`mashctl-linux-amd64`. The run copies it into the container and runs the
published commands as a newcomer would:
`MASH_VERSION=<v> MASH_RELEASE_BASE=file://<dir> MASH_INSTALL_ALLOW_FILE=1
bash install.sh`, after the checksum check of `install.sh` against the
folder's `SHA256SUMS` (`docs/install.md`). A folder that lacks a file is
refused, naming what is missing. The Claude Code structural check uses the
three plugin files of this checkout.

**Published** (`--published --version vX.Y.Z [--only-install]`): the newcomer
follows the real route. The container downloads `SHA256SUMS`, `install.sh` and
(for the server step) `mash-local-nats-setup.sh` from
`https://github.com/bogworks/mash/releases/download/<version>/` with `curl`,
checks them against `SHA256SUMS`, and runs `install.sh` with neither
`MASH_RELEASE_BASE` nor `MASH_INSTALL_ALLOW_FILE`. `--version` is required (the
run names exactly the release it follows; it does not resolve "latest", which
is the simpler choice). `--release-dir` is refused. A release that does not
exist (404) fails the install step with curl's 404 text, the run exits non-zero
and no other step runs. A full published run cannot pass before the release
carries `install.sh` and the setup script (v0.5.6 on); `--only-install` stops
after the install step and the egress checks, and never writes `assert.ok`.

The container stays on `--network none`. Egress is one thing only: a host-side
allowlist proxy, `egressproxy/main.go` (stdlib, own tests). The harness builds
it with the host's `go` into the run folder, starts it in the background (pid
recorded, stopped by that pid in the exit trap, socket removed by its exact
path), and bind-mounts the socket's folder read-only at `/run/egress`. The
socket lives in a short private folder (`$XDG_RUNTIME_DIR/gr-<run id>`, mode
0700, removed at the end) because a UNIX socket path is limited to about 107
bytes, which a run folder under a long scratch path exceeds. It
listens on a UNIX socket (mode 0600) and speaks `CONNECT` only, to
`github.com:443`, `objects.githubusercontent.com:443` and
`release-assets.githubusercontent.com:443` (exact names, no wildcard; GitHub
redirects release downloads to the last one today). It resolves the name on the host, refuses the whole name if any
answer is not a public unicast address (loopback, private, link-local, CGNAT
100.64.0.0/10 which is the tailnet, ULA, multicast, unspecified, documentation
and embedding ranges) and dials the vetted address, not the name. Every request
goes to `RUN_DIR/egress.log` (host, allowed or refused, reason, bytes), and the
receipt carries the allowlist and a summary of it.

In the container, `socat` (installed at build time) listens on
`127.0.0.1:3128` as `tester` and forwards to the socket; its pid is recorded
and killed by pid at the end. `https_proxy` is set only on the commands that
download, never globally. To let `tester` connect to a 0600 socket that the host
user owns, published mode runs the container with
`--userns=keep-id:uid=1000,gid=1000`.

Negative checks run inside the container (step `egress`, not a guide block):
`curl https://example.com` through the proxy is refused with 403; a direct
`curl --noproxy '*' https://github.com` fails (no network); a release that does
not exist fails with the 404 text.

## How to run

```
bash scripts/guide-run/guide-run.sh --make-release-dir /path/rel [--version v0.0.0-rehearsal]
bash scripts/guide-run/guide-run.sh --release-dir /path/rel [--version V] \
    [--scratch DIR] [--report FILE]
bash scripts/guide-run/guide-run.sh --published --version vX.Y.Z [--only-install] \
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
| endpoints   | 4          | `mashctl endpoint create --out` then `mash join`, for bob and alice |
| status      | 4          | `mash status` as bob and as alice                     |
| send        | 5          | the two `mash send` commands                          |
| inbox       | 5          | alice's `mash inbox`                                  |
| read        | 5          | alice's `mash read 2`                                 |
| json-send   | 7          | `mash send --json "ping"`                             |
| json-read   | 7          | `mash read 3 --json`                                  |
| json-inbox  | 7          | `mash inbox --json`                                   |
| json-jq     | 7          | `mash read 3 --json \| jq -r .body`                   |
| delete      | 9          | `mashctl endpoint delete acme.demo.bob`, the address typed at the prompt on a terminal |
| refused     | 8          | `mash status` as bob after the delete (exit non-zero) |
| lifecycle   | 10         | the setup script `--stop`, `--start`, `--remove`      |

`delete` is run as the guide shows it: `podman exec -t` and a pty helper types
the address at the "Type the address to confirm" prompt, as a person would
(never `--yes`); the screen output is the recorded block. `--remove` needs a
terminal in the same way and is answered with the folder.
Mail is awaited with a bounded retry (30 tries, one second apart).

## What it does not prove

- The Claude Code steps (guide step 7). There is no model here: **Claude Code
  step: structural check only, no model API.** The block id `claude-code` is
  not recorded and `fill` leaves it alone: it needs a recorded manual session.
  The structural step `claude-code-structure` (not a fill target) runs
  `claude --version` only if `claude` exists (it does not: it records "not run:
  no Claude Code in the container") and checks the plugin tree statically:
  `plugin/.claude-plugin/plugin.json`, `plugin/.mcp.json` and
  `plugin/hooks/hooks.json` exist and parse as JSON, exactly one MCP server,
  exactly the three hooks (SessionStart, PostToolUse, UserPromptSubmit). In
  rehearsal the tree is this checkout's; in published mode there is no plugin
  tree in the release assets, so it records "not run: plugin tree is checked in
  rehearsal mode".
- A real macOS machine.
- `gh attestation verify`: the container has no `gh`, so `install.sh` says the
  checksums come from the same download. In rehearsal the binaries are built
  from this checkout, not the attested release.
- `mash join` (step 9): not built (S5), so there is no step for it.
- Anything a container cannot show: a reboot, other users on the host.
