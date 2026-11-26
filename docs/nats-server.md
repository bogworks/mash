# Bring your own NATS server

This page is for you if you run your own `nats-server` and want mash to use it. It lists everything that server has to provide, and then shows how to connect mash to it. If you only want a server on your own machine, you do not need this page: the setup script makes one that meets every requirement below. See [Installing mash](install.md). Where NATS sits under mash is in [the stack](guide/stack.md).

The requirements apply to mash 0.5.7. They are what mash checks or creates; where this page says "not checked", mash does not test it for you.

## Requirements

1. **nats-server 2.10.7 or newer.** Tested on 2.15.1.
2. **Operator mode with a full resolver.** An operator key signs accounts, and the server keeps the account JWTs itself.
3. **A system account**, with a user whose credentials you can give to `mashctl`.
4. **One account for mash**, which already exists on the server, with JetStream and file storage. If the operator requires signing keys (`strict_signing_key_usage`), give `mashctl` one as `signing_key`.
5. **JetStream limits on that account** large enough for your endpoints (see [Sizing](#sizing)).
6. **TLS.** Clients connect with `tls://` (or `wss://`) URLs, loopback included.
7. **Credentials and keys as files** that you hand to `mashctl init` (see [Keys and creds mashctl needs](#keys-and-creds-mashctl-needs)).

### Details

**Version.** The setup script refuses anything older than 2.10.7. Neither `mash` nor `mashctl` checks the server's version, so on your own server this is a floor we know works, not a gate. We ran the full flow below against 2.15.1 and 2.10.7.

**Operator mode with a full resolver.** Users sign in with JWT credentials files (a signed file that names one user and what it may do). Token, user/password and plain nkey sign-in are not supported. The resolver must be `type: full`, because `mashctl` asks it for the account's JWT and, when it cancels a deleted endpoint's key, pushes a new account JWT through it. `mashctl init` refuses a server that accepts a connection with no credentials, and one whose system account does not answer.

**System account.** `mashctl` signs in to it with the credentials you give as `system_creds`. It uses them for three things: asking each server for its list (a cluster), looking up the account's JWT, and pushing the updated account JWT when an endpoint's key is revoked.

**The account.** `mashctl init` cannot create the account. It must already be on the resolver, signed by the operator key you give, not expired, and its JWT's subject must be the account key you give.

**Strict signing key usage.** By default `mashctl` signs every endpoint's user with the account's identity key, the `account_key` below. An operator that requires users to be signed by an account signing key (`strict_signing_key_usage: true`) refuses those users, so set `signing_key` below to the account's signing seed and `mashctl` signs with that instead. `init` checks that the key is one of the account JWT's signing keys. When the account JWT lists signing keys and no `signing_key` is set (and none is stored yet), `init` prints one warning, `warning: no signing_key in <setup file>; this server may require one`, and carries on (exit 0): it does not read the operator, so the listed keys are its only sign. Without the setting, leave strict signing off: `init` does not check this, and on a strict server the new users are refused with "Authorization Violation".

**JetStream with file storage.** mash keeps everything on file storage and uses no memory storage. `init` refuses an account whose JWT gives it no disk storage (disk limit 0). If the account uses tiered limits, it needs an `R1` tier, because every mash stream has one replica. The server itself must also have been able to switch JetStream on for the account: nats-server gives JetStream a budget of 75% of the free disk by default, and an account asking for more than what is left silently gets no JetStream. `init` asks the server and says so if that happened.

**A cluster.** If your servers form a cluster, list every one in `servers`. `init` refuses a setup file that lists fewer servers than the cluster has, because a revocation has to be confirmed on each of them.

## Getting nats-server and the nats command

You need two programs, on the `PATH` of the user who runs mash and its setup script (the setup script also finds `nats-server` in `/usr/sbin`, where Debian's package puts it):

- **`nats-server` 2.10.7 or newer.** Tested with 2.15.1.
- **`nats`** (natscli). Tested with 0.5.2. The setup script needs it; mash itself does not.

Any way of getting them works. Here is one way, with no `sudo`: download each release from GitHub, check it against the release's `SHA256SUMS`, and put it in `~/.local/bin`. You need `curl`, `sha256sum` (on macOS, `shasum -a 256`) and `tar`. The commands below are for Linux amd64.

First make the folder:

```sh
install -d -m 755 ~/.local/bin
```

**nats-server:**

```sh
V=2.15.1 OS=linux ARCH=amd64
curl -fsSLO https://github.com/nats-io/nats-server/releases/download/v$V/nats-server-v$V-$OS-$ARCH.tar.gz
curl -fsSL -o nats-server-SHA256SUMS https://github.com/nats-io/nats-server/releases/download/v$V/SHA256SUMS
grep " nats-server-v$V-$OS-$ARCH.tar.gz\$" nats-server-SHA256SUMS | sha256sum -c - &&
  tar -xzf nats-server-v$V-$OS-$ARCH.tar.gz &&
  install -m 0755 nats-server-v$V-$OS-$ARCH/nats-server ~/.local/bin/nats-server
```

**nats:** natscli ships as a zip file. If you have `unzip`, use `unzip -q nats-$V-$OS-$ARCH.zip` in place of the `python3` line.

```sh
V=0.5.2 OS=linux ARCH=amd64
curl -fsSLO https://github.com/nats-io/natscli/releases/download/v$V/nats-$V-$OS-$ARCH.zip
curl -fsSL -o nats-SHA256SUMS https://github.com/nats-io/natscli/releases/download/v$V/SHA256SUMS
grep " nats-$V-$OS-$ARCH.zip\$" nats-SHA256SUMS | sha256sum -c - &&
  python3 -m zipfile -e nats-$V-$OS-$ARCH.zip . &&
  install -m 0755 nats-$V-$OS-$ARCH/nats ~/.local/bin/nats
```

The checksum line must print `OK`. The `&&` after it means nothing is unpacked or installed when the check fails. Then make sure `~/.local/bin` is on your `PATH`, and check:

```sh
export PATH="$HOME/.local/bin:$PATH"
nats-server --version
nats --version
```

They print `nats-server: v2.15.1` and `0.5.2`. Both projects publish the same files for `ARCH=arm64` and for `OS=darwin`. We tested only `linux` and `amd64`.

### If you used a system package

Check where the package put the program and whether it started a service. For example, Debian's and Ubuntu's `nats-server` package (2.10.27 on Debian 13):

- puts the binary at `/usr/sbin/nats-server`, which is not on a normal user's `PATH`. The setup script also looks in `/usr/sbin`, `/usr/local/sbin` and `/sbin`, and uses a `nats-server` there if it and its folder are owned by root and not writable by others. Put it on your `PATH` to run it by hand;
- starts a system service, `nats-server.service`, at boot. With the package's own `/etc/nats-server.conf` it listens on `127.0.0.1:4222` with no authentication, so any process on the machine can use it, and mash cannot use a server like that. The setup script then takes the next free port. If nothing needs that service, an administrator can stop it and keep it from starting again with `sudo systemctl disable --now nats-server`.

## TLS

Every connection mash makes needs TLS, to any host, including `127.0.0.1`. That covers `mashctl`, the `mash` commands and a session's own connection.

- The URL must start with `tls://` or `wss://`. A `nats://`, `ws://` or scheme-less URL is refused before any connection is made, with exit code 1.
- For reference only: the environment variable `MASH_DANGEROUSLY_SKIP_NATS_ENCRYPTION=1` turns this check off, and every command then warns that encryption is off. It exists for mash's own tests. Do not set it: anyone on the network could then read your mail.
- **A private CA is supported.** Set `ca` in the setup file to the PEM file of the CA that signed the server's certificate. `init` saves that value in `config.json`, so later commands use it. `MASH_CA` and `--ca` override it for one command only and are never saved, so don't rely on them at `init`. When you set it, it replaces the system trust store; it does not add to it. Without it, the server's certificate must chain to your system's trusted roots.
- A server that requires client certificates: set `tls_cert` and `tls_key` (or `MASH_TLS_CERT` and `MASH_TLS_KEY`) together, in `config.json` or the environment. The setup file does not carry them.
- The certificate must name the host you put in the URL. This page's example uses `127.0.0.1`.

With a private CA and no `ca` set, `mashctl init` stops with: "the server's certificate is not trusted: set "ca" or MASH_CA to the PEM file of the CA that signed it".

## Keys and creds mashctl needs

`mashctl init` takes four files, and a fifth, `signing_key`, when your operator requires signing keys, and keeps copies of some of them. Every one must be a regular file you own, mode `0600`. mash never prints a seed, and the setup file holds paths, never keys.

| Setup file key | What it is | What it is for |
| --- | --- | --- |
| `operator_key` | The operator seed (starts with `SO`): the key that signs the account's JWT. This is the operator's identity key when the server was set up with the `nats` command, or a signing key if your operator uses one. | Re-signing the account JWT when an endpoint's key is revoked. |
| `signing_key` | Optional. A 0600 file holding one of the account's **signing** seeds (starts with `SA`; its public key is listed in the account JWT's signing keys). Set it when the operator has `strict_signing_key_usage: true`. | Signing each endpoint's user credentials, in place of `account_key`. |
| `account_key` | The account's **identity** seed (starts with `SA`), the one whose public key is the account JWT's subject. It is not an account signing key. | Signing each endpoint's user credentials when `signing_key` is not set; also the account the users belong to. |
| `system_creds` | Credentials of a user in the system account. | Everything in "System account" above. |
| `admin_creds` | Credentials of a user in the mash account. | The administrator's connection: making and removing inboxes, the directory and the records of what was issued. |

Where they end up: `init` copies the seeds into `~/.config/mash/keys/` (`operator.nk`, `account.nk` and, with `signing_key`, `signing.nk` with its record `signing.pub`; the folder is `0700`, the files `0600`) and the system credentials into `~/.config/mash/server/sys.creds` (folder `0700`, file `0600`). The admin credentials stay where you put them, and `config.json` points at them. Set `MASH_CONFIG_DIR` or `--config-dir` for another folder, or `MASH_KEYS_DIR` (an absolute path) for the keys only.

Think about what this means: the operator key is the strongest key your server has, and `mashctl` keeps a copy on the machine where you run it, protected only the way your other files are. The `mash` program on a session's machine never holds any of these; an endpoint has only its own credentials.

`init` never replaces what a config folder already holds. If you set up again with a different operator key, account key, server, credentials or owner, it refuses and changes nothing. Use a new folder (`--config-dir`) or move the old one aside yourself.

## Sizing

mash makes these on the mash account. All use file storage and one replica.

| Name | Made | Size cap | Notes |
| --- | --- | --- | --- |
| `KV_mash-directory` | once, by `init` | 16 MiB | endpoint records |
| `KV_mash-leases` | once, by `init` | 4 MiB | who is online; entries expire after 90 seconds |
| `KV_mash-retired` | once, by `init` | 64 MiB | one small marker per deleted address, kept for good |
| `KV_mash-issued` | once, by `init` | 8 MiB | a record of every credential mash issued |
| `inbox-<owner>_<group>_<name>` | per endpoint | 16 MiB (default) | 30 days of mail at most, oldest dropped first; one durable consumer; messages up to 1 MiB |

(`inbox-acme_demo_bob` is what we saw for the endpoint `acme.demo.bob`.)

So, for N endpoints with the defaults, the account needs at least:

- **Streams:** 4 + N. Each KV bucket is a stream.
- **Consumers:** 1 per stream (the inbox's durable consumer). The account's consumer limit applies to each stream.
- **Disk:** 92 MiB + 16 MiB x N. For example, 20 endpoints: 412 MiB.

Round up. The script's account is given 2 GiB of disk, 1024 streams and 1024 consumers. Disk binds first: 92 + 16 x N must fit in 2048 MiB, which gives N = 122 endpoints. The stream limit (4 + N) and the consumer limit of 1024 are no longer the binding ones. Servers set up earlier keep their old limits (64 streams and 128 consumers). To raise the stream and consumer limits of such a server, use the steps in [Account limits](operations.md#account-limits): `nats auth account edit` and `nats auth account push` with the system credentials, no restart. To host more endpoints, raise the account's disk limit by 16 MiB per endpoint: on an `nsc`-managed server that is the `JS_DISK` variable in the `nsc` steps in [operations.md](operations.md#the-account), and on a script-made server it is `--js-disk BYTES` on the same `nats auth account edit` (the script itself fixes the disk at 2147483648 bytes and has no such variable). Some of these numbers differ from what older text says; the table above is what the 0.5.6 code creates. The inbox cap and the three bucket caps can be raised in `config.json` under `limits` (`inbox_max_bytes`, `inbox_max_age`, `directory_max_bytes`, `lease_max_bytes`, `inbox_max_consumers`); `mashctl limits show` prints what is in force and `mashctl limits apply` raises existing inboxes. mash never lowers a cap.

If you use a smaller account limit than the table, `init` prints a warning. The warning uses the table's numbers: one endpoint needs 5 streams, 1 consumer and 108 MiB of disk (the 4 buckets, 92 MiB, and one inbox, 16 MiB), and each further endpoint adds 1 stream and 16 MiB. It follows the caps in `config.json` under `limits`; on a re-init, a bucket whose cap was raised earlier counts at its live cap, since `init` never lowers a cap.

## Connect mash to your server

mash 0.5.7 has one way to connect to a server you already have: `mashctl init --config FILE`. (`mashctl adopt`, `mashctl init --local` and `mashctl init --start` are gone.) `mashctl` does not install, configure or start NATS.

Write a setup file. It is YAML, and every key except `ca` and `signing_key` is required:

```yaml
servers: [tls://nats.example.com:4222]
ca: /etc/mash/ca.pem
owner: acme
admin_creds: /etc/mash/mashadmin.creds
system_creds: /etc/mash/sys.creds
operator_key: /etc/mash/operator.nk
account_key: /etc/mash/account.nk
```

- `servers` is a list of addresses, with no user name or password in them.
- `ca` is optional; leave it out when the server's certificate chains to your system roots.
- `owner` is your first owner: lower-case letters, digits and single hyphens, starting with a letter, up to 32 characters. (`init`'s message says 48; see [#1172](https://github.com/bogworks/mash/issues/1172).)
- Relative paths are read from the folder the setup file is in.
- A value that looks like a key (a seed or PEM text) instead of a path is refused.

Then preview and run it:

```sh
mashctl init --config setup.yaml --dry-run
mashctl init --config setup.yaml
```

You should see `Initialized mash on tls://nats.example.com:4222 for owner acme`. Running it again with the same file prints `Already initialized ...; nothing changed`. `init` checks everything first and writes nothing until the checks pass.

Make an endpoint:

```sh
mashctl endpoint create acme.demo.bob --out bob.join
mashctl endpoint list
```

## Check it

```sh
mashctl status
mashctl doctor
```

`mashctl status` is one line, for example `tls://127.0.0.1:4222: ok, 1 endpoint, 0 online`. `mashctl doctor` checks the config, the server, sign-in, mash's storage and the endpoint records, changes nothing, and says how to fix what is wrong; a healthy server ends with `No problems.`. See [doctor](doctor.md) and [Troubleshooting](troubleshooting.md).

## What mash does not need

- It does not need the `nats` command or `nsc` to run. (The setup script uses the `nats` command; `nsc` is one way to make the account yourself.)
- It does not need memory storage, a clustered or replicated stream, or any change to the server's code.
- It does not support a hosted service where you do not hold the operator key, such as Synadia Cloud, through `mashctl` in 0.5.7: revoking an endpoint needs the operator key, and setting up the account needs admin creds in it ([Synadia Cloud](synadia.md)).
- Endpoints do not need the operator or account keys, only their own credentials.

For a cluster, certificates and a second `mashctl` machine, see [TLS, clusters and a second mashctl machine](tls-and-clusters.md).
