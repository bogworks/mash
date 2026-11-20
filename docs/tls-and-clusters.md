# TLS, clusters and a second mashctl machine

This page sets up mash over TLS, on a cluster of three NATS servers, and with a second machine that runs `mashctl`. It adds to [operations.md](operations.md) and does not replace it. Read [Adopting an existing server](operations.md#adopting-an-existing-server) and [Several servers](operations.md#several-servers) first.

Everything here uses settings that exist in mash 0.5.5. Nothing is a new feature. What was run and what was not is in [Not tested in 0.5.5](#not-tested-in-055) at the end.

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

`mashctl init --local` writes a TLS-only server on `listen: 127.0.0.1:...`. It makes a local certificate authority (`ca.pem`, `ca.key`) and a server certificate (`server.pem`, `server.key`) in the server directory, and the `tls` block in `server.conf` names the certificate and key. The certificate is valid for `localhost`, `127.0.0.1` and `::1` only. For clients on other hosts, replace the certificate and key with ones that name the address the clients use, and set `listen` to an address they can reach. An existing operator-mode server needs only the block.

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
  "admin_creds": "/home/me/.config/mash/server/mashadmin.creds",
  "ca": "/home/me/.config/mash/ca.pem"
}
```

If `init --local` made the file, it already has a `tls://` URL and `ca` naming its `ca.pem`.

| Setting | Variable | Use |
|---|---|---|
| `servers` | `MASH_SERVER`, `--server` | `tls://host:port`; several separated by commas in the environment, or a JSON array in the file. |
| `ca` | `MASH_CA`, `--ca` | The CA that signed the server's certificate. Without it the system trust store is used. |
| `tls_cert`, `tls_key` | `MASH_TLS_CERT`, `MASH_TLS_KEY` | A client certificate and its key, for a server with `verify: true`. Set both or neither. |
| `insecure_plaintext` | `MASH_INSECURE_PLAINTEXT` | `1` allows plain text (below). Off by default. |

A variable overrides the same key in `config.json`. A relative path in `config.json` is resolved against the config directory. The files are read at connect time.

The same settings serve `mashctl`, `mash serve`, `mash hook` and the other `mash` commands. Every machine that connects to a server with `verify: true` needs a client certificate, endpoints included.

### What fails, and what it says

| You did | mash says |
|---|---|
| Left out `ca` for a private CA | `x509: certificate signed by unknown authority (the server's certificate is not trusted: set ca or MASH_CA ...)` |
| Connected without a client certificate to a server with `verify: true` | `the server refused the client certificate or requires one: set tls_cert and tls_key ...` |
| Set `tls_cert` without `tls_key` | `tls_cert and tls_key (MASH_TLS_CERT and MASH_TLS_KEY) must be set together` |
| Named a host the certificate does not list | an `x509` host name error. Add the name to the SAN and sign again. |

### Plain text and `MASH_INSECURE_PLAINTEXT`

Every mash connection requires TLS, to any host, loopback included: `mashctl`, `mash serve`, `mash hook` and the commands `send`, `read`, `inbox`, `who`, `prompt`, `status` and `doctor`. A plain `nats://` or `ws://` URL is refused before any connection is made, and so is any other scheme that is not `tls://` or `wss://`. `mashctl init --local` makes a certificate authority and a server certificate, so the quick start runs over TLS with no extra setting. The one override is `insecure_plaintext` (`MASH_INSECURE_PLAINTEXT`): `1`/`true` or `0`/`false`, anything else is an error, and the variable beats `config.json`. 

A plain URL without the override fails with exit code 2 and this text:

```
mash: refusing a plain nats:// connection to "HOST": every mash connection requires TLS, to any host (loopback included); set insecure_plaintext (MASH_INSECURE_PLAINTEXT=1) to allow plain text; the fix is to use a tls:// URL with the server's certificate (ca for a private CA)
```

With the override set, each command connects and prints this line on stderr:

```
mash: WARNING: plain-text connection to HOST (insecure_plaintext is set); anyone on the network can read mail and obtain signatures
```

Use it only for a throwaway server in a test. Never use it on a network you do not control: plain text lets anyone on the network read mail and obtain signatures. It does not turn a `tls://` connection off.

`mash doctor` shows the result on its `server` row:

- `server    ok    tls://myserver:4222` for a `tls://` URL that a server answers.
- `server    warn  ... is plain text: insecure_plaintext is set` when the override is set and the URL is plain.
- `server    FAIL  ...` when the override is not set and the URL is plain; the connection is refused before it is made.

### `mashctl adopt` with a `tls://` URL

On the machine that will run `mashctl`, with `ca.pem` copied to it:

```sh
MASH_CA=/home/me/ca.pem mashctl adopt --server tls://nats.example.com:4222 \
  --owner acme --account MASH \
  --operator-key operator.nk --account-key account.nk --sys-creds sys.creds --mint-admin
```

Adopt uses the TLS settings of the loaded configuration (`ca`, `tls_cert`, `tls_key`, from the variables or from an existing `config.json`). When it writes `config.json` it records `servers`, `admin_creds` and the TLS settings that were set. Then run `mashctl init` once to create the directory and lease buckets. The seed files must be mode 0600, and `--account` must be the account's name (`MASH` unless you chose another).

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

This is the template from operations.md with the client `tls` block and a `tls` block inside `cluster`. Each node changes `server_name`, `listen`, the two directories, the cluster `listen`, `routes`, and its certificate files.

```
server_name: mash-n1
listen: 0.0.0.0:4222
operator: "/etc/nats/operator.jwt"
system_account: <the system account key from init --local's server.conf>
resolver: {
  type: full
  dir: "/var/lib/mash/n1/resolver"
  allow_delete: false
  interval: "5s"
}
resolver_preload: {
  <copy this block from the server.conf that init --local wrote>
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
- `--servers 3` or `MASH_SERVERS=3`: how many servers the cluster has. A revocation (`endpoint delete`, `endpoint reissue`) then waits for every server to acknowledge the account update, and refuses when fewer answer. `endpoint create` takes the flag too. `adopt` prints `3 server(s) replied` and a warning to set it.
- `--sys-creds FILE` or `MASH_SYS_CREDS`: the system account credentials. Without either, `mashctl` uses the `sys.creds` that `adopt` put in `<config dir>/server/`.

```sh
export MASH_SERVERS=3
mashctl endpoint delete acme/dev/design --sys-creds /secure/sys.creds
```

A successful revocation prints `servers acknowledged the revocation: 3`. `mashctl issued check` ends with `3 of 3 servers answered` when the cluster formed.

An endpoint's `servers` may list the servers the same way. An inbox lives on one server (one replica), so an inbox is unavailable while that server is down; see [One server down](operations.md#one-server-down).

## 3. A second mashctl machine

### How it signs (DEC-004)

The second machine signs as the first does. It holds the same operator signing seed and the same account identity seed. It does not get its own signing key: mashctl refuses a signing key in the account JWT that it did not make ([ADR 0059](decisions/0059-mashctl-checks-who-issued-the-account-jwt-carries-only-its-own-signing-keys-and-requires-tls-for-admin-connections.md)), so a machine with a key of its own is not supported in 0.5.5.

A person sets it up with `mashctl adopt`, the same command that adopts a server, run on the second machine.

### What to copy

Copy these three files from the first machine, over a channel you trust (not mail, not chat), each as mode 0600:

- `operator.nk` from the key store (`<config dir>/keys`, or `$MASH_KEYS_DIR`): the operator signing seed.
- `account.nk` from the key store: the account identity seed.
- `sys.creds` from `<config dir>/server/`: the system account credentials.

Also copy `ca.pem`, and the client certificate and key if the servers use `verify: true`.

Then run `adopt` on the second machine:

```sh
mashctl adopt --server tls://n1.example.com:4222 --owner acme --account MASH \
  --operator-key operator.nk --account-key account.nk --sys-creds sys.creds --mint-admin
```

Adopt imports the two seeds into the second machine's key store, copies `sys.creds` to `<config dir>/server/` and, with `--mint-admin`, makes an admin credential of its own there. Delete the copies of the seed files when it finishes. Then edit `config.json` so `servers` lists all three servers, set `MASH_SERVERS=3`, and do not run `mashctl init --local` or `mashctl init` there: the buckets exist already.

### What must not be copied

- `mashadmin.creds`. The second machine makes its own with `--mint-admin`. Each admin credential is then its own user in the issue record.
- The key store directory as a whole. Copy the two seeds; adopt writes the store.
- `system.nk`. The commands in this page do not need it, and adopt does not import it.
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
# copy design.pub to a mashctl machine

# on a mashctl machine (M1 or M2)
mashctl endpoint create acme/dev/design --public-key design.pub --servers 3
# copy acme.dev.design.jwt back to M3

# on the endpoint's machine (M3)
mash credential create --seed design.seed --jwt acme.dev.design.jwt
```

Only `design.pub` and the JWT file travel. M3 also needs `ca.pem` and the settings in section 1: `servers`, `ca`, and a client certificate when the servers require one. M3 can name any one of the three servers in `servers`, or all three.

## Not tested in 0.5.5

> The 0.5.5 acceptance run (AT-005) tested TLS, a three-server cluster and two `mashctl` machines by hand, in rootless containers on one host: three NATS servers of one cluster with TLS on the client and route connections, endpoints on separate nodes (a session on one node connecting to another node's server, mail each way), and two `mashctl` machines on one account (the same address created from both at the same moment: one succeeded, the other was refused `address_taken`). It did not test real separate hosts or a physical network, clock drift between hosts, or certificate expiry and renewal.

While this page was written, these steps were also run on one host, on loopback, with `nats-server` 2.15.1 and OpenSSL 4.1.0:

- One operator-mode server made by `mashctl init --local`, with the `tls` block above added: `mashctl init` over `tls://localhost` failed without `ca` and worked with it.
- The same server with `verify: true`: a connection without a client certificate failed with the message above, and one with `tls_cert` and `tls_key` worked.
- `mashctl adopt` with a `tls://` URL and `--mint-admin` into a second, empty config directory, then `endpoint create --public-key` from it, `mash key create` and `mash credential create` in a third, and `endpoint delete --servers 1 --sys-creds FILE`.
- Three servers in one cluster with the `cluster` TLS block and `verify: true`, one shared certificate for all three, listing `localhost` and `127.0.0.1`: `adopt` reported `3 server(s) replied`, `mashctl init`, `issued check` (`3 of 3 servers answered`), `endpoint create --servers 3` and `endpoint delete --servers 3` (`3 server(s) acknowledged`) worked with a three-entry `url`.

What was not run, by AT-005 or by these steps:

- Real separate hosts, and any physical network between hosts: the AT-005 containers share one host, and the steps above ran on loopback.
- Clock drift between hosts.
- Certificates with public DNS names in the SAN (AT-005 used IP addresses), and a certificate per server issued by a real CA.
- A client list where one server is down, and a revocation with one server down.
- Certificate expiry and renewal, and a server that needs a restart to take a new certificate.
- The OpenSSL commands with versions other than 4.1.0.
