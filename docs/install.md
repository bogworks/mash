# Installing mash

This walks through one machine running Claude Code sessions with mash. Provisioning the server side (the account, endpoint users and their inboxes) is in [operations.md](operations.md).

**Requirement:** mash needs a NATS server that you run yourself, in operator mode with JWT user credentials. An endpoint's SAP signing identity is its NATS user key (the key in its creds file's user JWT; see [sap.md](sap.md)). Every server requirement is on [Bring your own NATS server](nats-server.md#requirements).

## Requirements

- A NATS server that meets the [requirements](nats-server.md#requirements). To run one on this machine, see [getting nats-server](nats-server.md#getting-nats-server-and-the-nats-command). You do not need `nats-server` on a machine where you only run endpoints, or if you already have a server. `mashctl` does not install or start NATS.
- Claude Code 2.1.312 or newer (the oldest version a live receipt passed on; mash does not refuse older ones). The plugin and the `mash` binary should be the same release (for example 0.5.8).

## 1. The binary

Supported platforms: Linux and macOS (amd64, arm64), the release targets (`mash-` and `mashctl-` for `{linux,darwin}-{amd64,arm64}`, each with a `.cdx.json`, next to `install.sh`, `mash-local-nats-setup.sh` and `SHA256SUMS`; see [release.md](release.md)). Native Windows is not supported: the spool lock and the hooks need Unix file locking and a POSIX shell. WSL 2 with the Linux binary is expected to work but has not been tested.

mash is one static Go binary, and `install.sh` is the install: it puts `mash` and `mashctl` on your machine, checked. **Install it first (below) until your plugin release is pinned**: the plugin fetches and verifies its own copy only once it carries a `mash.pins` file from a release (the post-release commit described next), and an unpinned plugin, which is what a checkout of the repository gives you, uses the `mash` on your `PATH` and does not start without one. With a pinned plugin there is nothing to install by hand: no `go install`, no `PATH` edit, no root.

- A `SessionStart` hook prefetches the binary into the plugin's data directory (`${CLAUDE_PLUGIN_DATA}/bin`) on first use, so the MCP server starts without a download. The server launcher downloads it as a fallback.
- Once pinned after a release (a post-release commit of `plugin/scripts/mash.pins`), the plugin carries a `mash.pins` file beside its launcher with the release version and the sha256 of each platform binary (linux and darwin, amd64 and arm64); without it the launcher is unpinned and uses `mash` from PATH. A download that does not match is refused and deleted, and the binary must report the pinned version before it is used. Pinning also bumps the plugin's version to `<version>-pin.1`, so installed plugins update and receive the pin. Updating the plugin updates the binary.
- Resolution order: `$MASH_BIN` (an explicit override), the downloaded copy for the pinned version, then (pinned) a fresh download of it, falling back to `mash` on the `PATH` only if that download fails, so an older install on the `PATH` never overrides the pin. Hooks never wait more than a few seconds for a download (a 5 s budget, then `PATH`, and no retry for 5 minutes after a failure). An unpinned plugin uses `mash` on the `PATH` only and never downloads.
- Verification (provisional): the environment variable `MASH_PLUGIN_REQUIRE_VERIFIED` decides whether the launcher may ever run a binary it has not verified against `mash.pins`. It is off by default, which is today's behaviour: a failed pinned download falls back to `mash` on the `PATH` with a warning naming what failed, an unpinned plugin uses `mash` on the `PATH` with a warning naming the pins file, and a cached downloaded binary is used as it is, except that once per session (when the session's `__prefetch` step or `serve` starts, never in a hook) the launcher re-hashes it against `mash.pins`: a copy that does not match is moved aside to `mash-<version>.bad-<time>` in the same folder, and a fresh one is downloaded and verified. `mash doctor` shows a `launcher` row in a session the plugin started: `ok` with `pinned mash`, or `warn` with `mash from PATH, not the pinned one` when the fallback ran. When it is on (`1`, `true`, `yes` or `on`; `0`, `false`, `no` and `off` are off), the launcher exits 127, with what failed and how to turn the setting off on stderr, when the plugin is unpinned (no `mash.pins`, or one that is empty, unreadable or has no valid `version=` line), when the cached downloaded binary's sha256 does not match `mash.pins` (it is checked on every run, which costs a hash of the binary, and it is neither re-fetched nor replaced by `mash` on the `PATH`), and when the pinned binary cannot be fetched or fails verification (a failed download, a sha256 mismatch, no pinned sha256 for your platform, an unsupported OS or architecture, no hashing tool, a downloaded binary that does not run or reports the wrong version, or a hook skipping a download that failed recently). It never runs a `mash` from the `PATH`. When it is on, a hook therefore keeps refusing for 300 s (5 minutes) after a failed download; that is by design. Any other value, including an empty one, is treated as on, with a note naming it. mash only reads this concrete setting: mash has no profile that groups settings: you set each one. The variable's name, its source and the default are provisional and may change; the launcher reads the setting in one place. `$MASH_BIN` is an explicit operator choice, trusted as given and never verified, whether the setting is on or off, and a successful verified download and the prefetch are not affected.
- The MCP server runs `${CLAUDE_PLUGIN_ROOT}/scripts/mash` directly, so the launcher needs its exec bit: git-based marketplace installs (`claude plugin marketplace add bogworks/mash`, then `claude plugin install mash@mash`) preserve the mode. The hooks run it as `sh ${CLAUDE_PLUGIN_ROOT}/scripts/mash`, which needs no exec bit.
- Offline, or on an unsupported platform, the plugin uses a `mash` already on the `PATH` or a copy it downloaded earlier; if there is neither, it says so on stderr and the server does not start.

To install the binaries (required for an unpinned plugin; also for the CLI in section 5), run the install script of the release. This is the install, three lines and a run:

```sh
BASE=https://github.com/bogworks/mash/releases/latest/download
curl -fsSLO $BASE/install.sh -O $BASE/SHA256SUMS &&
grep ' install.sh$' SHA256SUMS | sha256sum -c - &&  # macOS: shasum -a 256 -c -
bash install.sh
```

It finds the machine's type (Linux or macOS, amd64 or arm64), downloads `mash` and `mashctl` from the same release together with that release's `SHA256SUMS`, checks each binary against it, and puts both in `~/.local/bin` (`MASH_INSTALL_DIR` names another folder; `MASH_VERSION=v0.5.10` pins a release, and without it the script first looks up the latest release's tag and downloads from that tag; `MASH_REPO=owner/name` names another repository, default `bogworks/mash`, for the download, the tag lookup and the attestation check; `MASH_API_BASE` names where the tag's commit is looked up when gh cannot, default `https://api.github.com`; a `MASH_RELEASE_BASE` mirror needs `MASH_VERSION`). It prints one line for each, with the path it used, for example `Installed mash v0.5.10 (<commit>) in ~/.local/bin`, asks nothing and needs no `sudo`. If the folder is not on your `PATH` it says so in one line. A symbolic link to the folder is followed. It refuses a folder that belongs to another user, or that, or any folder above it, other users can write to (a root-owned sticky folder such as `/tmp` above it is fine), and on any failed check nothing is installed; both binaries are staged before either is put in place. Ctrl-C, a closed terminal or a `kill` (SIGINT, SIGTERM, SIGHUP) while it swaps them in leaves both old or both new, never one of each; another signal, such as `kill -9` or SIGQUIT, can still split them in a very small window.

If you want a NATS server on this machine, `mash-local-nats-setup.sh` is the other script of the release; [by hand, step 3](guide/by-hand.md#3-make-a-server) uses it.

### Verify what you download

Every file of a release is covered by `SHA256SUMS`. `SHA256SUMS`, the binaries and the two scripts (`install.sh`, `mash-local-nats-setup.sh`) are also subjects of the release's build provenance attestation, which GitHub signs for the release's own workflow.

What `install.sh` does on its own:

- It always checks each binary against `SHA256SUMS`. Both come from the same download, so on their own they show the files are intact, not who made them.
- With `gh` 2.102.0 or newer (the [GitHub CLI](https://cli.github.com/)) it also verifies `SHA256SUMS` against the attestation before it installs anything, and prints `Verified the release attestation for bogworks/mash vX.Y.Z` on stdout when it does (signed by the workflow `bogworks/mash/.github/workflows/release.yml`, from the source ref `refs/tags/vX.Y.Z` of the release being installed and from the commit that tag points to, which it reads with `gh api` or, if gh is not logged in, from the public GitHub API). If it cannot read the commit, it installs nothing and prints `error: could not read the commit of bogworks/mash vX.Y.Z; nothing was installed` on stderr, and exits 1; the hint line, `hint: gh auth login, then run the install again`, appears only when gh is not logged in (a logged-in gh is not held to the public API's limit of 60 lookups an hour). If the check fails it installs nothing and prints `error: the release failed verification; nothing was installed` on stderr, and exits 1. [Why 2.102.0](security/verify-your-download.md#what-you-need).
- With no `gh`, a `gh` that does not run, one whose version it cannot read, or one older than 2.102.0 (a 2.102.0 prerelease counts as older), it prints one warning line on stderr that says which (`warning: gh not found: checked checksums only, not verified`; the others read `gh did not run`, `gh version unreadable` and `gh is below 2.102.0` in place of `gh not found`). Then it installs on the checksums, which show the files are intact but not who made them. To authenticate the download, get a current `gh` and run the steps below. (This is from 0.5.9. The `install.sh` of 0.5.8 and earlier uses any `gh` that has `gh attestation verify`, and with gh 2.48 to 2.67 it stops and installs nothing.) The check below is the manual way to do the same, and you can run it before you run the script.

To check the script and the attestation yourself, with a current `gh`:

```sh
BASE=https://github.com/bogworks/mash/releases/download/v0.5.10   # use the tag of the release you want, on every line; for another repository (MASH_REPO) change bogworks/mash on every line too
curl -fsSLO $BASE/install.sh -O $BASE/SHA256SUMS -O $BASE/mash.intoto.jsonl &&
COMMIT=$(curl -fsSL -H 'Accept: application/vnd.github.sha' https://api.github.com/repos/bogworks/mash/commits/v0.5.10) &&
gh attestation verify SHA256SUMS -R bogworks/mash --bundle mash.intoto.jsonl \
  --signer-workflow bogworks/mash/.github/workflows/release.yml --source-ref refs/tags/v0.5.10 --source-digest "$COMMIT" &&
grep ' install.sh$' SHA256SUMS | sha256sum -c - &&           # macOS: shasum -a 256 -c -
less install.sh && bash install.sh
```

When the attestation check passes, `gh attestation verify` can print nothing and exits 0; on a failure it prints an error and exits 1, and the `&&` stops everything after it.

To check a binary's SBOM (a CycloneDX list of what it was built from), which the release workflow also attests, verify it against the same workflow and tag. The binary here is `mash` for Linux on amd64; use the file for your platform:

```sh
TAG=v0.5.10   # the tag of the release you want
curl -fsSLO https://github.com/bogworks/mash/releases/download/$TAG/mash-linux-amd64 &&
gh attestation verify mash-linux-amd64 -R bogworks/mash --predicate-type https://cyclonedx.org/bom \
  --signer-workflow bogworks/mash/.github/workflows/release.yml --source-ref refs/tags/$TAG
```

At a terminal it prints `Verification succeeded!` and the attestation it matched, and exits 0 (captured or piped, it prints nothing and still exits 0); on a failure it prints an error and exits 1. The SBOM check applies to the binaries only: the scripts have no SBOM.

If `gh` is missing or older than 2.102.0, skip the `gh attestation verify` lines: the `grep` line still shows the script is the release's own file, and the TLS caveat above applies. Any failure of a check: stop and run nothing.

An agent does not pipe a download into a shell: the setup skill downloads each script, checks it against `SHA256SUMS`, shows you what it is about to run and runs it after a yes. Both scripts are also the repository's own `scripts/` files at the release commit, byte for byte; the release check compares them ([ADR 0150](decisions/0150-releases-carry-install-and-local-nats-setup-scripts-as-attested-assets.md)).

To build from source instead, `go install github.com/bogworks/mash/cmd/mash@v0.5.10` and `go install github.com/bogworks/mash/cmd/mashctl@v0.5.10` need Go 1.27.4 or newer; the result is not the attested build. Use the tag of the release you want, not `@latest`.

## 2. The plugin

The mash repository is its own Claude Code marketplace:

```sh
claude plugin marketplace add bogworks/mash
claude plugin install mash@mash
```

The plugin declares an MCP server, `mash serve` (started through the plugin's launcher script, with `MASH_SPOOL_FROM_START=1`), a SessionStart hook that prefetches the pinned binary, and two hooks, PostToolUse and UserPromptSubmit, which run `mash hook`. Mail reaches the session through those hooks, as "mash mail" context; see [Hook delivery](operations.md). A session without a mash identity (no `MASH_ENDPOINT`) gets an inert server with only the `version`, `status` and `doctor` tools, so it is safe to install for every session.

**Install scope.** `claude plugin install` installs at user scope by default (`--scope user`), which records the plugin in `enabledPlugins` in `~/.claude/settings.json` so every session on the machine has mash; a session without `MASH_ENDPOINT` stays inert. Project scope (`.claude/settings.json`) and local scope (`.claude/settings.local.json`) enable it in one repository only, so the mash server and hooks are absent in every other project. A narrower scope overrides a wider one, so a project can turn off a user-scope install. An organisation can force-enable `mash@mash` in managed `enabledPlugins`, which Claude Code lists at Managed scope and users cannot disable in `/plugin`; that is also what keeps the hooks under `allowManagedHooksOnly` (below). The `status` and `doctor` tools say what is missing in a session. See [Choose an install scope](https://code.claude.com/docs/en/plugins/install#choose-an-install-scope) in the Claude Code docs.

Mail needs those hooks to run. They do not run with `disableAllHooks: true` in any Claude Code settings file, or under a managed `allowManagedHooksOnly` unless an administrator force-enables `mash@mash` in managed `enabledPlugins`. Then mash stops waking the session after a few tries and mail waits for a read with the `read` tool; see [Hook delivery](operations.md).

<a id="3-allow-the-channel"></a><a id="3-allow-the-channel-once-per-machine"></a>
## 3. Optional: wake an idle session

**You can skip this section.** Without it, mail still arrives at the session's next prompt or tool call. The idle wake, which rouses a session that is waiting for input, needs Claude Code to load mash's channel. Claude Code's channels page says: "During the preview, `--channels` only accepts plugins from an Anthropic-maintained allowlist, or from your organization's allowlist if an admin has set `allowedChannelPlugins`" ([Claude Code channels](https://code.claude.com/docs/en/channels)). mash is not on Anthropic's list, so what you do depends on your account:

- **Personal account.** The idle wake needs Claude Code's development flag today. Claude Code shows a warning screen and asks you to confirm at every start; whether to use it is your choice. It lets a plugin that is not on Anthropic's list push a message into your session.

  ```sh
  MASH_ENDPOINT=alice.dev.design claude --name alice.dev.design --dangerously-load-development-channels plugin:mash@mash
  ```

- **Team or Enterprise organisation.** Channels are off until an owner turns them on (`channelsEnabled`), and an administrator must add mash to `allowedChannelPlugins` in the managed settings (Organization settings > Claude Code > Channels). Then no flag is needed:

  ```sh
  MASH_ENDPOINT=alice.dev.design claude --name alice.dev.design --channels plugin:mash@mash
  ```

- **Neither.** Start the session without a channel flag. Mail arrives at the next prompt or tool call and an idle session is not woken.

`--name` puts the address in the prompt box and the terminal title, so you can tell who you are talking to; `/rename alice.dev.design` does the same for a session already open. The setup skill writes one command per agent with the right flags inside, so you type one word per session. Without a channel, hooks still deliver mail ([Hook delivery](operations.md)).

## 4. Configuration

On a machine that runs `mashctl`, `mashctl init --config FILE` writes `servers`, `ca`, `owner`, `admin_creds` and `system_creds` to this file from a setup file (see [Setting mash up](operations.md#setting-mash-up-with-mashctl-init---config)); then make each endpoint with `mashctl endpoint create` ([Adding an endpoint](operations.md#adding-an-endpoint)). The rest of this section describes the file.

`~/.config/mash/config.json`, or `$XDG_CONFIG_HOME/mash/config.json` when `XDG_CONFIG_HOME` is an absolute path and that file exists (an existing `~/.config/mash` install keeps working). With no `HOME` and no absolute `XDG_CONFIG_HOME`, mash stops with an error unless everything comes from the `MASH_*` variables. A leading `~/` in `creds` and `admin_creds` (and in `MASH_CREDS` and `MASH_ADMIN_CREDS`) is expanded to your home; `~user/` is not. `policy.md` and the default creds files live in the same directory; when the XDG directory has no copy of one, mash also finds it in `~/.config/mash` (the XDG copy wins when both exist). A relative `admin_creds`, `ca`, `tls_cert` or `tls_key` in `config.json` is resolved against the config directory, not the working directory. `config.json` that is writable by every user is refused; run `chmod go-w` on it.

```json
{ "servers": ["tls://nats.example.com:4222"], "endpoint": "alice.dev.design" }
```

| Key | Env | Meaning |
|---|---|---|
| `servers` | `MASH_SERVER`, `--server` | The NATS server or servers. In the environment a comma list; in `config.json` a JSON array of strings, or one string with commas; the flag repeats. Use `tls://`; the rules (private CA, client certificates, the one override) are on [TLS](nats-server.md#tls), and [Turning encryption off](#turning-encryption-off) covers the override. |
| `ca` | `MASH_CA`, `--ca` | PEM file of the CA that signed the server's certificate, for a private CA. Without it the system trust store is used. |
| `tls_cert`, `tls_key` | `MASH_TLS_CERT`, `MASH_TLS_KEY` | PEM client certificate and its private key, for a server that requires client certificates (mutual TLS). Set both or neither. |
| `endpoint` | `MASH_ENDPOINT`, `--endpoint` | The full address of the endpoint a session runs as, `owner.group.name`, for example `alice.dev.design`. Each token is lower-case letters, digits and single hyphens. The owner, the first token, is also the fence a credential reaches. See "Which endpoint" below. Apps started from a GUI or IDE usually do not inherit your shell's variables, so set it where Claude Code is launched (the IDE's own environment setting, or the launch command). `mash status` shows whether the endpoint is ready, before you launch. |
| `owner` | (none) | `mashctl` only: the owner it acts for when no endpoint is set. Written by `mashctl init --config`. `mash` ignores it. |
| `creds` | `MASH_CREDS`, `--creds` | The endpoint's credentials file. Default `<owner>.<group>.<name>.creds` in the config directory (`~/.config/mash/` by default). Setting it turns off the pick of the one installed endpoint: with no endpoint set, the session stays inert. |
| | `MASH_CONFIG_DIR`, `--config-dir` | The config directory (`config.json`, `policy.md`, default creds). An absolute path (a relative one is an error); it wins over `XDG_CONFIG_HOME` and `~/.config/mash`, which is not consulted. |
| | `MASH_STATE_DIR` | The directory that holds `spool/` and `hook.log`. An absolute path (a relative one is an error). Default `$XDG_STATE_HOME/mash` if `XDG_STATE_HOME` is absolute, else `~/.local/state/mash`. |
| | `MASH_RELEASE_BASE` | Plugin launcher only: the base URL the pinned binary is downloaded from, default `https://github.com/bogworks/mash/releases/download`; the launcher fetches `<base>/v<version>/mash-<os>-<arch>`. It changes only where the bytes come from: the sha256 pin still applies, and a download that does not match is refused and deleted. Plain `http` is not refused, so use an `https` base. |
| | `MASH_PLUGIN_ROOT` | Set by the plugin's `.mcp.json` to the plugin's own root (`${CLAUDE_PLUGIN_ROOT}`). `mash serve` reads the plugin's version from `<root>/.claude-plugin/plugin.json` for its version report. You normally never set it. |
| | `MASH_LAUNCH_TOKEN` | Optional. Names this launch of the agent (up to 128 of `A-Za-z0-9._-`); written into the endpoint's lease as `launch_token` so a supervisor can tell which launch holds it. It also ties a session's hooks to its `mash serve`: a hook that carries the same non-empty token as serve is recognised as this session's even where its process chain cannot be followed (wrappers, sandboxes, IDE hosts). Use a fresh value for each launch (for example `MASH_LAUNCH_TOKEN=$(uuidgen) claude ...`); a value reused across launches defeats the hook-ownership rule. |
| `settle_ms` | `MASH_SETTLE_MS` | Quiet time before a batch is pushed. Default 100. |
| `batch_msgs`, `batch_bytes` | `MASH_BATCH_MSGS`, `MASH_BATCH_BYTES` | A batch up to 10 messages and 16 KiB is shown in full; more becomes a digest. |
| `spool_from_start` | `MASH_SPOOL_FROM_START` | `1`/`true` (or `0`/`false`; anything else is an error). Spool mail from the first message, to be shown by the hooks, instead of delivering it by channel until a hook has run: Claude Code drops channel events that arrive before its first prompt. The plugin sets it. Mail that wakes (not fyi or result) wakes the session at once until the first hook has run. Without a usable spool, mail is held unacked in the inbox (read it with the inbox and read tools). |
| `spool_required` | `MASH_SPOOL_REQUIRED` | `1`/`true` (or `0`/`false`; anything else is an error). Default off. Fail closed: mail is never delivered by the channel. It is spooled from the first message and acked only after a hook has drained it; mail the spool cannot take (no spool, or a failing one) is held unacked in the inbox, and the failure is reported on stderr, and in a channel notice. A spool that cannot be made at start is retried every 5 seconds. Held mail is read with the inbox and read tools, and is delivered once the spool recovers. |
| `accept_unverified` | `MASH_ACCEPT_UNVERIFIED`, or the `mash serve` flag `--accept-unverified` | `1`/`true` (or `0`/`false`; anything else is an error; in `config.json` a JSON `true` or `false`). Default off. By default only mail whose trust is `verified` reaches the session, `mash inbox` and `mash read`; mail that is unsigned (`absent`), signed by an unknown key (`unknown-signer`), `claimed` or `stale` is dropped and counted, `mash doctor` shows the count, and the sender is not told. When set, that mail is delivered instead, with its trust label, as it was before 0.5.6. The count is kept in `refused-unverified` in the endpoint's spool folder. |
| `idle_wake` | `MASH_IDLE_WAKE` | Hook mode: how long spooled mail waits for a hook before serve wakes the idle session. A Go duration, 5s to 30m. Default `45s`; re-wakes double from there, to 30 minutes. |
| `relay_window` | `MASH_RELAY_WINDOW` | serve: how soon after mail from one sender a send to another endpoint is traced as a relay candidate (see "Relay tracing" in operations.md). Nothing is blocked. A Go duration, 1s to 1h. Default `60s`. |
| `open_timeout` | `MASH_OPEN_TIMEOUT` | How long a command or serve may spend opening: connecting to its data and checking its identity. A Go duration above zero (for example `"45s"`), for slow links. Default `20s`. |
| `call_timeout` | `MASH_CALL_TIMEOUT` | How long one command, or one tool call in serve, may spend on its requests once open. A Go duration above zero. Default `15s`. |
| `lease_retry` | `MASH_LEASE_RETRY` | serve: the wait between attempts to take an endpoint's lease while another session holds it. A Go duration above zero. Default `10s`. |
| `transient_budget` | `MASH_TRANSIENT_BUDGET` | serve: how long transient failures (timeouts, no responders, a dropped connection) may repeat while waiting for the lease before the wait ends. A Go duration above zero. Default `15m`. |
| `transient_max` | `MASH_TRANSIENT_MAX` | serve: the retry backoff after transient failures doubles from `lease_retry` up to this. A Go duration above zero. Default `2m`. |
| `cli_timeout` | `MASH_CLI_TIMEOUT` | The total deadline of one command (`send`, `inbox`, `who` and the rest), open included. A Go duration above zero. Default `15m`. |
| `wake_count` | `MASH_WAKE_COUNT`, or the `mash serve` flag `--wake-count N` | How many waiting messages are too many: above it `mash serve` wakes the session and `mash inbox` says "More than N are waiting." Any whole number, 0 or more. Default `15`; `0` turns it off. `mash serve` refuses a value that is not one (exit 2); other commands ignore it and use the default. |
| `wake_age` | `MASH_WAKE_AGE`, or the `mash serve` flag `--wake-age DURATION` | How long the oldest waiting message may wait: past it `mash serve` wakes the session and `mash inbox` says "The oldest has waited ..." past it. A whole number and `m`, `h` or `d` (for example `30m`, `1h`, `2d`), or a Go duration such as `90s`; any positive length. Default `1h`; `0` turns it off. `mash serve` refuses a value that is not one (exit 2); other commands ignore it and use the default. |
| `admin_timeout` | `MASH_ADMIN_TIMEOUT` | The total deadline of one `mashctl` command. A Go duration above zero, for a large mesh or a slow link. Default `30s`. |

The environment wins over the file.

**Global flags.** `mash` and `mashctl` accept `--endpoint ADDR`, `--creds`, `--server` (repeatable), `--ca`, `--config-dir`, `--detail`, `--json` and `--no-color` anywhere in the command line. A setting comes from the flag first, then the environment, then `config.json`.

**Installing an endpoint.** `mash join FILE` installs the endpoint a join file describes. It writes `<address>.creds` (mode 0600) into the config directory, the server addresses into `servers` in `config.json`, and, when the server uses its own certificate authority, the certificate as `ca.pem` in the same directory with `ca` pointing at it. It needs no endpoint and no server. When the join file was made for a public key you supplied (`mash key create`), add `--key` with your seed file. One config directory serves one mesh: a join file for other servers, or for another certificate authority (compared with the file `ca` names in `config.json`, else `ca.pem`), or none where the folder has one, or one where the folder has none and already holds an endpoint, is refused, with or without `--replace`, and `--config-dir` names another directory. The certificate must be certificates only (a key or other text in it is refused before anything is written), and the join file's credentials must be for the address it names. The creds are installed first, then `ca.pem`, then `config.json`; a failure after the creds removes what the run made. Joins into one directory run one after the other. `--replace` replaces the creds of an installed endpoint, and nothing else (not the servers, the certificate authority or `config.json`), and `--dry-run` writes nothing. A join file that holds a seed, and the file `--key` names, must be plain files owned by you and readable by you alone (`chmod 600`); mash refuses them otherwise, before reading. A join file that holds only a user JWT is not checked.

**Which endpoint.** `MASH_ENDPOINT` (or `endpoint` in `config.json`, or `--endpoint`) holds the full address `owner.group.name`, for example `alice.dev.design`. If none is set, mash uses the one endpoint whose creds file `<owner>.<group>.<name>.creds` (mode 0600, owned by you) sits directly in the config directory. If several are installed, every command except `mash doctor` stops with `more than one endpoint is installed here: a, b` and you name one; `mash doctor` checks them all. A value that is not three dotted parts is a configuration error naming `MASH_ENDPOINT`. `mashctl` never takes its owner from an installed creds file. Its owner is the owner part of an endpoint you set (`--endpoint`, `MASH_ENDPOINT` or `endpoint` in `config.json`; any group and name will do), else the `owner` key of `config.json` (`mashctl init --config` writes it). `mash` ignores the `owner` key.

Credentials files must be mode 0600; they hold the endpoint's private key, and mash refuses to use a credentials file that other users can read (fix it with `chmod 600`). `mashctl` applies the same check to the admin credentials and to the system account credentials (`system_creds` in `config.json`). A symlink is followed and the file it points to is checked.

Optional: the session instruction file `~/.config/mash/policy.md` is appended to every session's instructions. By default, mail from a verified mash endpoint is treated as a delegated request from the operator of this mash deployment; use `policy.md` to carve out what still needs a human, for example "Ask me before spending money or touching production."

### Turning encryption off

Every mash connection requires TLS (see [TLS](nats-server.md#tls)). A plain URL is refused before any connection is made, exit code 1, with the host, the variable below and the fix.

There is one way to allow plain text, for `mash` and `mashctl` alike: set the environment variable `MASH_DANGEROUSLY_SKIP_NATS_ENCRYPTION=1`. It is not a setting: there is no flag and no `config.json` key. Its only value is `1` (empty or `0` is off; anything else is an error). While it is set, every command of both tools first prints this line on stderr, and nothing extra on stdout (`--json` stays clean):

```
warning: no encryption to the server (MASH_DANGEROUSLY_SKIP_NATS_ENCRYPTION)
```

Use it for a throwaway server in a test, never on a network you do not control: plain text lets anyone on the network read mail and obtain signatures. The release's `mash-local-nats-setup.sh` makes a certificate authority for a local server and does not need it.

The old names are gone with no alias: `MASH_INSECURE_PLAINTEXT` in the environment and `insecure_plaintext` in `config.json` are refused, exit 1, with an error that names `MASH_DANGEROUSLY_SKIP_NATS_ENCRYPTION=1`.

## 5. Launch a session

```sh
MASH_ENDPOINT=alice.dev.design claude --name alice.dev.design
```

`--name` shows the address as the session's name. To wake an idle session as well, add the channel flag that fits your account ([section 3](#3-optional-wake-an-idle-session)).

The session registers as a SAP agent, takes the lease on its name, and starts receiving mail. If another live session already holds the name, this one starts without mail tools and says why.

Keep this machine's clock in sync (NTP): more than 30 s of skew from the server makes peers look offline and mail `[stale]`. See [Troubleshooting](troubleshooting.md#a-peer-shows-offline-in-who).

In the session, Claude has eight tools: `send`, `read`, `inbox`, `who`, `reply`, `version`, `status` and `doctor`. `status` says in one line whether the endpoint is ready (`<endpoint>: ready, offline`, `ready, online` when an agent session holds the endpoint open now, or `not ready: <reason>`). `doctor` checks the config, the creds, the server, the sign-in and the delivery of mail one at a time and says what to fix. A session with no `MASH_ENDPOINT` gets only `version`, `status` and `doctor`. `version` shows the mash binary's version and commit, the plugin's version and this endpoint's address. `mash status --detail` also says `holds endpoint`, `hooks` (`ran` once the agent host's hooks have delivered mail in this session) and `storage`; outside a Claude Code session (a plain shell) it shows `session: none` instead; `mash doctor`'s `delivery` row warns while the hooks have not run yet and fails when they are not delivering ([troubleshooting](troubleshooting.md#mail-is-waiting-but-never-shown-hooks-not-firing)). `mash status` and `mash doctor` from a shell give the same answers for the environment they run in, so a missing name or creds shows before you launch. `mash status` exits 1 when the endpoint is not ready, and `mash doctor` exits 1 when a check fails. From a shell, the same identity can use the CLI:

```sh
MASH_ENDPOINT=alice.dev.design mash who
MASH_ENDPOINT=alice.dev.design mash send <owner>.dev.review "Spec is at docs/specs/x.md; please review section 4."
MASH_ENDPOINT=alice.dev.design mash send <owner>.dev.review --id spec-review-1 "Spec is at docs/specs/x.md."   # safe to re-run: stored once
MASH_ENDPOINT=alice.dev.design mash prompt <owner>.dev.review "Are you still on the auth bug?"
MASH_ENDPOINT=alice.dev.design mash inbox --after 120 --limit 50   # the 50 messages after number 120, oldest first
```

A live prompt is at most 32768 bytes; a longer one is refused with an error, so send the longer text as mail.

### Sessions and resume

The endpoint's address belongs to the launch, not to the conversation. The address comes only from `MASH_ENDPOINT` in the environment Claude Code starts with ([ADR 0012](decisions/0012-endpoint-chosen-per-launch-by-mash-name.md)).

- `claude --resume` or `--continue` without `MASH_ENDPOINT` gives an inert session. With a different address, the conversation gets that address's mailbox, and reusing an address in an unrelated conversation hands it that endpoint's backlog. Resume with the same `MASH_ENDPOINT`.
- One live session holds an address. A session that finds its address held starts without mail tools and retries. Do not resume a conversation while its original process is still running: a second process in the same Claude session can drain the holder's spooled mail without any signal ([receipt](evidence/2026-11-01-dual-resume/RECEIPT.md)).
- The lease expires 90 seconds after its last refresh (`store.LeaseTTL`), and a running session refreshes it every 30 seconds (`endpoint.LeaseRefresh`). A laptop that sleeps longer than 90 seconds loses it. When the session wakes, if nobody else took the name, `mash serve` takes it back. If another session took it, the loss is final: lease loss after a start is terminal, and the session must be restarted. Run `mash who` to see whether the name is still held, and restart with the same `MASH_ENDPOINT`; the restart takes the name back once the other holder is gone.

### Lease fields a supervisor can read

The endpoint's lease (key `<name>` in the `mash-leases` KV bucket, rewritten every 30 s and expiring after `store.LeaseTTL`, 90 s) is a JSON object. Besides the holder's identity (`instance_id`, `host`, `pid`, `since`, `version`, ...) and `mcp_seen`, it carries the waking-mail count:

- `waking_pending` (integer, omitted when 0): unread mail that should wake the session: kind `blocker`, `question` or `task` (mail with no or an unknown kind counts as `task`), plus any verified urgent mail whatever its kind. `fyi` and `result` mail does not count. "Unread" is mail the session has not been shown: held for a hook or unacked in the inbox; mail already shown on the channel or by a hook is gone from the count. Reading a message with the `read` tool does not remove it until it is shown.
- `waking_pending_at` (RFC3339 UTC): when the count was computed. It is set whenever the count is known, including 0, so `waking_pending` absent with a fresh `waking_pending_at` means none, and no `waking_pending_at` means unknown (an older mash, or serve has not yet accounted for the mail already waiting at its start). After a start the count stays unknown until that startup backlog has been received, or the server shows nothing outstanding beyond what serve holds; mail a previous session left unacked is redelivered only after the 30 s ack wait, so the count can be unknown that long. It is never guessed from elapsed time.

A reader treats the count as fresh when `waking_pending_at` is within 90 s (the lease TTL). It is rewritten on every refresh and when the count changes (at most one extra write per 5 s, a burst of mail being one write). Readers must ignore fields they do not know. Limits are in `internal/endpoint/waking.go`.

The CLI never takes the lease, so it works alongside the running session.

`send --id KEY` (the send tool's `id`) is an idempotency key, 1 to 128 printable ASCII characters. Re-running a send with the same key to the same recipient from the same endpoint, within the inbox stream's two-minute duplicate window, stores nothing new: it succeeds, reports the original number and says it was a duplicate (`Mail already sent; nothing new stored`). If a send times out waiting for the stream's acknowledgement, the message may or may not have landed; the error prints a key to retry with (`mash send <to> --id <key> ...`), which delivers it at most once, but only within that two-minute window: after it a retry may store a second copy, which the recipient shows as a new message. Without `--id` every send is a new message.

## Versions that work together

| Part | Version |
|---|---|
| `nats-server` | 2.10.7 or newer (tested on 2.15), only where you run the server |
| Claude Code | 2.1.312 or newer (older ones are not refused, but are not tested) |
| `mash`, `mashctl` and the `mash` plugin | the same release (for example 0.5.9) |
| Go, to build from source | 1.27.4 or newer |

## Uninstall

Remove only what you made. Stop any Claude Code session that uses mash first.

- The programs: delete `mash` and `mashctl` from the folder `install.sh` named (`~/.local/bin`, or `MASH_INSTALL_DIR`).
- The plugins: `claude plugin uninstall mash@mash`, and `claude plugin uninstall mash-setup@mash` if you installed it.
- Your configuration and keys: the folder `~/.config/mash` (or the folder `MASH_CONFIG_DIR` named, or `$XDG_CONFIG_HOME/mash`; never `XDG_CONFIG_HOME` itself). It holds each endpoint's creds file, so deleting it cannot be undone. Mail already stored on the server stays there.
- Mail kept on this machine: the state folder (`~/.local/state/mash`, or `MASH_STATE_DIR`).
- A server made by the local setup script: `bash mash-local-nats-setup.sh --remove` stops it and deletes its folder, every mailbox and every message ([by hand](guide/by-hand.md#3-make-a-server)). Afterwards mashctl's config folder (`~/.config/mash`) stays and still points at the removed server: move it aside, or run `mashctl init --config <file> --config-dir <new folder>` to set up again (the script prints this note).
