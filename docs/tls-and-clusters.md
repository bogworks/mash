# TLS, clusters and a second mashctl machine

This page sets up mash over TLS, on a cluster of three NATS servers, and with a second machine that runs `mashctl`. It adds to [operations.md](operations.md) and does not replace it. Read [Setting mash up with `mashctl init --config`](operations.md#setting-mash-up-with-mashctl-init---config) and [Several servers](operations.md#several-servers) first.

Everything here uses settings that exist in mash 0.5.6 (`servers`, `ca`, `tls_cert`, `tls_key`). Nothing is a new feature. The steps were last run by hand on 0.5.5, before the settings were renamed, and have not been run again on 0.5.6. What was run and what was not is in [Not tested in 0.5.6](#not-tested-in-056) at the end.

Names used below: `nats.example.com` is a server, `n1.example.com`, `n2.example.com` and `n3.example.com` are the three servers of a cluster, `acme` is the owner.

## 1. TLS on one server

### The server

Add a `tls` block to the server's `server.conf`. It is a top-level block, next to `listen`:

```
listen: 0.0.0.0:4222
tls: {
  cert_file: "/etc/nats/server.pem"
  key_file: "/etc/nats/server.key"
  timeout: 5
}
```

`mashctl` does not make certificates or write server files. Make a certificate authority (`ca.pem`, `ca.key`) and a server certificate (`server.pem`, `server.key`) with your own tools; the commands under [Certificates](#certificates) show one way. The certificate must name the address the clients use, and `listen` must be an address they can reach. An existing operator-mode server needs only the block.

For mutual TLS (the server also checks a certificate from each client) add the CA and `verify`:

```
tls: {
  cert_file: "/etc/nats/server.pem"
  key_file: "/etc/nats/server.key"
  ca_file: "/etc/nats/ca.pem"
  verify: true
  timeout: 5
}
```

### The certificates

Make a certificate authority (CA) once. Keep `ca.key` off the servers; it signs every certificate.

```sh
openssl req -x509 -newkey rsa:4096 -sha256 -days 365 -nodes \
  -keyout ca.key -out ca.pem -subj "/CN=mash CA" \
  -addext "basicConstraints=critical,CA:TRUE" \
  -addext "keyUsage=critical,keyCertSign,cRLSign"
```

Make a certificate for the server. The client checks the host name or address in its URL against the certificate's subject alternative names (SAN), so list every name and address clients use:

```sh
openssl req -newkey rsa:2048 -nodes -keyout server.key -out server.csr \
  -subj "/CN=nats.example.com"
printf 'subjectAltName=DNS:nats.example.com,IP:203.0.113.10\nbasicConstraints=CA:FALSE\nextendedKeyUsage=serverAuth,clientAuth\n' > server.ext
openssl x509 -req -in server.csr -CA ca.pem -CAkey ca.key -CAcreateserial \
  -out server.pem -days 365 -sha256 -extfile server.ext
openssl verify -CAfile ca.pem server.pem
openssl x509 -in server.pem -noout -ext subjectAltName
```

Replace the DNS name and the IP address with your own. A URL that names a host the certificate does not list fails the check. If clients use only a name, leave out the `IP:` entry.

For mutual TLS, make one client certificate the same way, with the subject `/CN=mashctl` and `extendedKeyUsage=clientAuth` in its extension file, and sign it with the same CA.

### The clients

Copy `ca.pem` (not `ca.key`) to every machine that connects. In `config.json`:

```json
{
  "servers": ["tls://nats.example.com:4222"],
  "endpoint": "acme.dev.design",
  "admin_creds": "/home/me/.config/mash/admin.creds",
  "ca": "/home/me/.config/mash/ca.pem"
}
```

`mashctl init --config` writes `servers`, `ca`, `owner`, `admin_creds` and `system_creds` to `config.json` from its setup file.

| Setting | Variable | Use |
|---|---|---|
| `servers` | `MASH_SERVER`, `--server` | `tls://host:port`; several separated by commas in the environment, or a JSON array in the file. |
| `ca` | `MASH_CA`, `--ca` | The CA that signed the server's certificate (see [TLS](nats-server.md#tls)). |
| `tls_cert`, `tls_key` | `MASH_TLS_CERT`, `MASH_TLS_KEY` | A client certificate and its key, for a server with `verify: true`. Set both or neither. |

A variable overrides the same key in `config.json`. A relative path in `config.json` is resolved against the config directory. The files are read at connect time.

The same settings serve `mashctl`, `mash serve`, `mash hook` and the other `mash` commands. Every machine that connects to a server with `verify: true` needs a client certificate, endpoints included.

### What fails, and what it says

| You did | mash says |
|---|---|
| Left out `ca` for a private CA | `x509: certificate signed by unknown authority (the server's certificate is not trusted: set ca or MASH_CA ...)` |
| Connected without a client certificate to a server with `verify: true` | `the server refused the client certificate or requires one: set tls_cert and tls_key ...` |
| Set `tls_cert` without `tls_key` | `tls_cert and tls_key (MASH_TLS_CERT and MASH_TLS_KEY) must be set together` |
| Named a host the certificate does not list | an `x509` host name error. Add the name to the SAN and sign again. |

### Plain text and `MASH_DANGEROUSLY_SKIP_NATS_ENCRYPTION`

Every mash connection requires TLS, and the one override is the environment variable `MASH_DANGEROUSLY_SKIP_NATS_ENCRYPTION=1`; the rules are on [TLS](nats-server.md#tls). The release's local setup script, `mash-local-nats-setup.sh` (see the [setup script reference](reference/setup.md)), makes a private certificate authority and a server certificate for 127.0.0.1, so the local quick start runs over TLS with no extra setting.

A plain URL without it fails with exit code 2 and this text:

```
mash: refusing a plain nats:// connection to "HOST": every mash connection requires TLS, to any host (loopback included); set MASH_DANGEROUSLY_SKIP_NATS_ENCRYPTION=1 to allow plain text; the fix is to use a tls:// URL with the server's certificate (MASH_CA or "ca" for a private CA)
```

With it set, every command of both tools prints this line on stderr, once, before it does anything else, and nothing extra on stdout:

```
warning: encryption to the server is OFF (MASH_DANGEROUSLY_SKIP_NATS_ENCRYPTION is set)
```

Use it only for a throwaway server in a test: plain text lets anyone on the network read mail and obtain signatures. It does not turn a `tls://` connection off.

`mash doctor` shows the result on its `server` row:

- `server    ok    tls://myserver:4222` for a `tls://` URL that a server answers.
- `server    warn  ... is plain text: MASH_DANGEROUSLY_SKIP_NATS_ENCRYPTION is set` when the override is set and the URL is plain.
- `server    FAIL  ...` when the override is not set and the URL is plain; the connection is refused before it is made.

### `mashctl init` with a `tls://` URL

On the machine that will run `mashctl`, with `ca.pem` copied to it, write the setup file:

```yaml
servers:
  - tls://nats.example.com:4222
ca: /home/me/ca.pem
owner: acme
admin_creds: admin.creds
system_creds: sys.creds
operator_key: operator.nk
account_key: account.nk
```

and run `mashctl init --config setup.yaml`. `init` uses the `ca` of the setup file for its own connections, and `tls_cert` and `tls_key` from the loaded configuration (the variables or an existing `config.json`) when the servers use `verify: true`. It writes `config.json` with `servers`, `ca`, `owner`, `admin_creds` and `system_creds`, and makes the directory, lease and other buckets once. A server whose certificate the `ca` does not vouch for is refused with a message that names the `ca` key. The seed and creds files must be mode 0600.

## 2. A cluster of three servers

Follow [A three-node cluster](operations.md#a-three-node-cluster) for the account, the resolver and the keys. This section adds TLS to it.

### Certificates

Each server needs a certificate whose SAN names the host that clients and the other servers use for it. Make one per server with the CA from section 1:

```sh
for n in n1 n2 n3; do
  openssl req -newkey rsa:2048 -nodes -keyout $n.key -out $n.csr -subj "/CN=$n.example.com"
  printf "subjectAltName=DNS:$n.example.com\nbasicConstraints=CA:FALSE\nextendedKeyUsage=serverAuth,clientAuth\n" > $n.ext
  openssl x509 -req -in $n.csr -CA ca.pem -CAkey ca.key -CAcreateserial \
    -out $n.pem -days 365 -sha256 -extfile $n.ext
done
```

`clientAuth` is needed because a route connection is a TLS connection in both directions. Copy `ca.pem`, `nK.pem` and `nK.key` to server K.

### The server file of node 1

This is the node file from operations.md with the client `tls` block and a `tls` block inside `cluster`. Each node changes `server_name`, `listen`, the two directories, the cluster `listen`, `routes`, and its certificate files.

```
server_name: mash-n1
listen: 0.0.0.0:4222
operator: "/etc/nats/operator.jwt"
system_account: <the system account key of your server>
resolver: {
  type: full
  dir: "/var/lib/mash/n1/resolver"
  allow_delete: false
  interval: "5s"
}
resolver_preload: {
  <the system and account JWTs of your server>
}
jetstream: {
  store_dir: "/var/lib/mash/n1/jetstream"
}
tls: {
  cert_file: "/etc/nats/n1.pem"
  key_file: "/etc/nats/n1.key"
  timeout: 5
}
cluster: {
  name: mashc
  listen: 0.0.0.0:6222
  routes: [nats-route://n2.example.com:6222, nats-route://n3.example.com:6222]
  tls: {
    cert_file: "/etc/nats/n1.pem"
    key_file: "/etc/nats/n1.key"
    ca_file: "/etc/nats/ca.pem"
    verify: true
    timeout: 5
  }
}
```

`verify: true` in the `cluster` block makes the servers check each other's certificate against the CA. A node that was down while a key was revoked accepts the revoked key until its resolver syncs; see the `interval` note in operations.md.

Open the client port (4222) to the machines that run `mashctl` and endpoints, and the cluster port (6222) only between the servers.

### What `mashctl` needs

- `servers` lists every server: `"servers": ["tls://n1.example.com:4222", "tls://n2.example.com:4222", "tls://n3.example.com:4222"]`. The code takes a comma-separated list in `MASH_SERVER`, a repeated `--server`, or an array in `servers`, and each entry must be reachable by the name its certificate lists. A connection succeeds if any one listed server is up. mash dials only the listed servers; it ignores the servers a server advertises.
- The number of servers in `servers` is how many servers the cluster has. A revocation (`endpoint delete`, `endpoint reissue`) waits for every listed server to acknowledge the account update, and refuses when fewer answer. `endpoint create` does the same. `mashctl init` warns when the server is in a cluster, so list every server.
- `system_creds` in `config.json` is the system account credentials: the `sys.creds` that `init` put in `<config dir>/server/`. There is no flag or environment variable for it.

```sh
mashctl endpoint delete acme.dev.design
```

A successful revocation prints `servers acknowledged the revocation: 3`. `mashctl doctor` shows `keys       ok` when the cluster formed and every one of the three servers answered; with fewer it fails the `keys` check and says why.

An endpoint's `servers` may list the servers the same way. An inbox lives on one server (one replica), so an inbox is unavailable while that server is down; see [One server down](operations.md#one-server-down).

## 3. A second mashctl machine

### How it signs (DEC-004)

The second machine signs as the first does. It holds the same operator signing seed and the same account identity seed. It does not get its own signing key: mashctl refuses a signing key in the account JWT that it did not make ([ADR 0059](decisions/0059-mashctl-checks-who-issued-the-account-jwt-carries-only-its-own-signing-keys-and-requires-tls-for-admin-connections.md)), so a machine with a key of its own is not supported.

A person sets it up with `mashctl init --config`, the same command that sets up the first machine, run on the second machine.

### What to copy

Copy these three files from the first machine, over a channel you trust (not mail, not chat), each as mode 0600:

- `operator.nk` from the key store (`<config dir>/keys`, or `$MASH_KEYS_DIR`): the operator signing seed.
- `account.nk` from the key store: the account identity seed.
- `sys.creds` from `<config dir>/server/`: the system account credentials.

Also copy `ca.pem`, and the client certificate and key if the servers use `verify: true`.

Also give the second machine an admin creds file of its own: a user of the account, made with `nsc` and signed by an account signing key if the operator uses strict signing key usage. Then write a setup file on the second machine that names the three files and the admin creds, and run `init`:

```sh
mashctl init --config setup.yaml
```

`init` imports the two seeds into the second machine's key store, copies `sys.creds` to `<config dir>/server/`, finds that the buckets exist already, and writes `config.json`. Delete the copies of the seed files when it finishes. Then edit `config.json` so `servers` lists all three servers (or list them in the setup file).

### What must not be copied

- The first machine's admin creds. The second machine gets its own, so each admin credential is its own user in the issue record.
- The key store directory as a whole. Copy the two seeds; `init` writes the store.
- `system.nk`. The commands in this page do not need it, and `init` does not import it.
- An endpoint's `.seed` file. It never leaves the endpoint's machine.
- Anything from `server/` of the first machine other than `sys.creds`, such as `server.conf` or the resolver and JetStream directories.

Anyone who holds `operator.nk` and `account.nk` can mint any user, change any permission and revoke any key. Keep them on the two machines, in a directory (mode 0700) that agent sessions cannot read, as [Where the key store lives](operations.md#where-the-key-store-lives) says.

### Do not write from both machines at once

`mashctl` serializes account-changing commands with a lock file in its own key directory (`apply.lock`). Two machines do not share that file, so the lock does not coordinate them. Two hosts that hold copies of the keys are not coordinated: do not run `endpoint create`, `endpoint reissue` or `endpoint delete` on both at once. Run one, wait for it to finish, then run the next. Keep both machines' clocks in sync (NTP).

### An endpoint on a third machine

The endpoint's key is made on the endpoint's machine and its seed never leaves it. This is the three-command form from [mashctl.md](mashctl.md) and [An endpoint on another machine](operations.md#an-endpoint-on-another-machine). Either `mashctl` machine can do the signing:

```sh
# on the endpoint's machine (M3)
mash key create --out design.seed
# copy design.seed.pub to a mashctl machine

# on a mashctl machine (M1 or M2)
mashctl endpoint create acme.dev.design --out acme.dev.design.join --public-key design.seed.pub
# copy acme.dev.design.join back to M3

# on the endpoint's machine (M3)
mash join acme.dev.design.join --key design.seed
```

Only `design.seed.pub` and the join file travel. The join file carries the servers and the CA certificate, so `mash join` writes `servers` and `ca` into M3's configuration folder. M3 also needs a client certificate when the servers require one (section 1). M3 can name any one of the three servers in `servers`, or all three.

## Not tested in 0.5.6

> The 0.5.5 acceptance run tested TLS, a three-server cluster and two `mashctl` machines by hand, in rootless containers on one host: three NATS servers of one cluster with TLS on the client and route connections, endpoints on separate nodes (a session on one node connecting to another node's server, mail each way), and two `mashctl` machines on one account (the same address created from both at the same moment: one succeeded, the other was refused `address_taken`). It did not test real separate hosts or a physical network, clock drift between hosts, or certificate expiry and renewal.

While this page was written, these steps were also run on one host, on loopback, with `nats-server` 2.15.1 and OpenSSL 4.1.0, using mash 0.5.5, which named the server list `url` and the CA setting `tls_ca`:

- One operator-mode server, with the `tls` block above added: `mashctl` over `tls://localhost` failed without `ca` and worked with it.
- The same server with `verify: true`: a connection without a client certificate failed with the message above, and one with `tls_cert` and `tls_key` worked.
- `mashctl` set up with a `tls://` URL and its own admin user in a second, empty config directory, then `endpoint create --public-key` from it, `mash key create` and `mash join` in a third, and `endpoint delete` with `system_creds` in `config.json`.
- Three servers in one cluster with the `cluster` TLS block and `verify: true`, one shared certificate for all three, listing `localhost` and `127.0.0.1`: the setup step reported `3 server(s) replied`, then bucket creation, `mashctl doctor` (the `keys` check, with all three servers answering), `endpoint create` and `endpoint delete` (`3 server(s) acknowledged`) worked with a three-entry server list.

What was not run, by the acceptance run or by these steps:

- Real separate hosts, and any physical network between hosts: the acceptance run's containers share one host, and the steps above ran on loopback.
- Clock drift between hosts.
- Certificates with public DNS names in the SAN (the acceptance run used IP addresses), and a certificate per server issued by a real CA.
- A client list where one server is down, and a revocation with one server down.
- Certificate expiry and renewal, and a server that needs a restart to take a new certificate.
- The OpenSSL commands with versions other than 4.1.0.
