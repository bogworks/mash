# What mash needs from NATS

DRAFT 6 (0.5.6). The second document. The default, one-machine guide is
`SKILL.md` beside this file; come here only when the person's answer to
one of its questions is "not the default", or you already run NATS, or
something was lost. Names here follow that guide: owner `acme`, group
`demo`, server directory `$HOME/mash-nats`, mashctl's folder
`$HOME/.config/mash`.

**Other machines are not supported yet.** One machine, one user is the
supported case. Section 4 says what is planned.

**Versions.** Everything here was tested with nats-server v2.15.1 and nsc
v2.15.0 only. No other version is tested; the minimum that works is not
known.

Contents: 1 the settings, limits and users mash needs; 2 other answers
with worked examples; 3 the long form of adopt; 4 other machines; 5 your
own NATS server; 6 loss; 7 where every secret is.

## 1. The settings mash needs

Read this if you already run a NATS server and want mash to use it,
or want to know what the script's `server.conf` gives. mash needs the
seven things listed under the example from the server, and `mashctl adopt`
checks them. These are the real lines the script wrote in the test (account
ids and JWTs, long, are shortened):

```
listen: "127.0.0.1:4222"
tls {
  cert_file: "/home/alice/mash-nats/server.pem"
  key_file: "/home/alice/mash-nats/server.key"
}
jetstream {
  store_dir: "/home/alice/mash-nats/jetstream"
}
operator: "/home/alice/mash-nats/operator.jwt"
system_account: "ABOJ...A3QQ"
resolver {
  type: full
  dir: "/home/alice/mash-nats/resolver"
  allow_delete: true
  interval: "2m"
  timeout: "1.9s"
}
resolver_preload: {
  ABOJ...A3QQ: "<JWT>"
  ABZ3...7ZIK: "<JWT>"
}
```

1. `listen`: where the server accepts connections (this machine only,
   unless you said otherwise).
2. `tls`: every connection is encrypted; mash refuses a plain one. Give
   adopt the CA that signed `cert_file` (`--ca`); it keeps a copy.
3. `jetstream`: mail is kept on disk in `store_dir`.
4. `operator`: operator mode: nobody connects without credentials signed
   by this operator.
5. `system_account`: the account mashctl uses to revoke a lost key.
6. `resolver { type: full }`: the server keeps account records on disk,
   so mashctl can revoke without restarting the server.
7. `resolver_preload`: the accounts (system, and the mash account) the
   server starts knowing.

Two words first. The resolver is how the server finds account records
(a `full` one keeps them on disk, so accounts can change without a
restart). A consumer is a reader of a stream: one stream holds one agent's
mail, and each reader of it is a consumer.

Where the limits are set. The 2 GB of disk, 64 streams and 128 consumers
are not in `server.conf`. They are in the mash account's own JWT, which
the operator signs (the script sets them with `nsc edit account
--js-disk-storage 2G --js-streams 64 --js-consumer 128`, and leaves
memory storage unset). The server reads that JWT from the resolver.
`mashctl adopt` reads the limits from the JWT and then asks the server
whether it really gave JetStream to the account. Streams needed: one per
agent endpoint, plus 5 for mash's own buckets; so 64 streams allows
about 59 agents.

The account's name. mash does not require `MASH`. `mashctl adopt` takes
`--account NAME` and checks it against the name inside the account JWT;
if you omit it, it uses the owner name. They must agree, or adopt
refuses and says which name the JWT has.

The two users `mashctl adopt` is given:

- The admin user (`--admin-credentials`) must belong to the mash account and
  be allowed everything in it: create JetStream streams and key-value
  buckets, publish and subscribe on any subject. mash uses it to make
  the five buckets and each agent's stream. The script's `mashadmin` has
  no permission limits. If the operator key is set to require signing
  keys, the user must be signed by an account signing key (adopt says
  so).
- The system user (`--system-credentials`) must belong to the system
  account and be allowed to ping the server (`$SYS.REQ.SERVER.PING`),
  look up accounts and push an account update
  (`$SYS.REQ.CLAIMS.UPDATE`); adopt checks the first two, and `mashctl
  endpoint delete` uses the third. The script's `sys` user has no
  permission limits.

Also needed: the operator signing seed (`--operator-key`, starts `SO`)
and the account identity seed (`--account-key`, starts `SA`), each as a
0600 file. mashctl keeps both (see section 7).

## 2. Other answers, worked

Each is a change to the create command in part 3 of `SKILL.md`. Say the
change back to the person in plain words before you run it. Everything
else in the guide stays the same, except where a section below says what
differs.

**Another port** (4222 is taken, or you want another):

```sh
bash "$HOME/mash-setup/nats-local-setup.sh" --dir "$HOME/mash-nats" --owner acme --port 4333 --start
```

The script refuses a port already in use. Every later `--check`, `--start`
and the closing text use the recorded port; nothing else to change. The
server still listens on this machine only.

**Another server directory.** `--dir` must be an absolute path under your
home, every component a real directory you own, not writable by others,
and the final folder must not exist. Use it in every later command:

```sh
bash "$HOME/mash-setup/nats-local-setup.sh" --dir "$HOME/srv/mash-nats" --owner acme --start
```

**Another folder for mashctl.** mashctl keeps its files in
`$HOME/.config/mash` unless `MASH_CONFIG_DIR` names another folder. Set it
for every mashctl and mash command, and tell the script so that its closing
text names it:

```sh
bash "$HOME/mash-setup/nats-local-setup.sh" --dir "$HOME/mash-nats" --owner acme --mash-config "$HOME/mash-conf" --start
```

```sh
MASH_CONFIG_DIR="$HOME/mash-conf" "$HOME/.local/bin/mashctl" adopt --from "$HOME/mash-nats"
```

The same rules apply to that folder: private, never copied or backed up.

**A server for other machines** is not made by this script: it listens
on 127.0.0.1 only. A server reachable from other machines is the
one you run yourself (section 5).

**A different JetStream size**: `--js-disk 500M`. **A different account
name**: `--account NAME` (adopt must then be told the same name, section 3).

## 3. The long form of adopt

`mashctl adopt --from DIR` (the default path) reads `mash-setup.json` and
five fixed file names from DIR. The long form names each file and is for a
server this script did not make (section 5), or files kept elsewhere. It
types nothing when the files are yours on this machine; it asks for the CA
fingerprint when it is told to trust a CA it was not given from a folder you
own (the typed check, section 4). Flags: `--ca`, `--operator-key`,
`--account-key`, `--system-credentials`, `--admin-credentials`, `--owner`,
`--account`, `--server`. `--from` and the long flags cannot be mixed.
`--dry-run` changes nothing and never prompts:

```sh
"$HOME/.local/bin/mashctl" adopt --dry-run --server tls://127.0.0.1:4222 --owner acme --account MASH --ca "$HOME/mash-nats/ca.pem" --operator-key "$HOME/mash-nats/export/operator.nk" --account-key "$HOME/mash-nats/export/account.nk" --system-credentials "$HOME/mash-nats/export/sys.creds" --admin-credentials "$HOME/mash-nats/export/mashadmin.creds"
```

Run without `--dry-run`, the same line adopts. Without a terminal, a
fingerprint prompt cannot be answered: adopt exits 4. A server refusing the
credentials also exits 4.

## 4. Other machines

Not supported yet. The script's server listens on 127.0.0.1 only. A server
other machines can reach is the one you run yourself (section 5). What another
machine's user would need: the CA file (`ca.pem`, public), the server
address and an agent's own credentials, never mashctl's folder. They would
confirm the CA by typing its fingerprint, read to them over a channel you
trust (the closing box shows it as eight groups of eight). The command for
that, `mash credential install`, is planned and is not in the tree: do not
invent it. Making endpoints needs the operator and account seeds, which
live only in mashctl's folder, so only one machine you trust may run
`mashctl endpoint create`.

## 5. Your own NATS server

If you run NATS already, you need nothing from this script. Give it the
seven things listed in section 1, then the long form of adopt (section 3).
`--dry-run` tells you what is missing. Run it before anything else, and do
not edit a running server's configuration without the person's yes.

## 6. Loss

There is no backup and no restore; if this machine is lost, set up again
and reissue every endpoint. What each lost file costs is the table in
section 7. If only the server directory is lost, the keys in mashctl's
folder open nothing; set up again. Certificates last ten years; there is
no renew command: set up again.

## 7. Where every secret is, and what you lose

| What | Where | If it is gone |
|---|---|---|
| `ca.key` (CA private key) | server dir | no new server certificate; existing ones keep working |
| `nsc/` (key store) | server dir | no new accounts or users by hand; mashctl and the server keep working |
| `export/` (copies of four files) | server dir | nothing: mashctl holds its own copies |
| mashctl's folder: operator seed, account seed, system and admin credentials, agent creds | `$HOME/.config/mash` | mashctl cannot make, revoke or administer agents; set up again and reissue every endpoint |
| `server.key` | server dir | the server cannot start; set up again |
| `jetstream/` (all mail) | server dir | every stored message |

A process running as you can read all of these. mash does not defend
against that; if it matters to you, run the agent in a sandbox.
