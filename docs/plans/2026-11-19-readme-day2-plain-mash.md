## Day 2 with mash

The first page, [two agents and you](2026-11-19-readme-quickstart-plain-mash.md),
made three endpoints on one machine and deleted them. This page is what comes
after: an endpoint on another machine, a reissued key, a deletion with its
proof, stopping and starting the server, removing everything, and what each
failing `mash status` means. Every address is `acme.demo.name`; every
command that picks an endpoint uses `MASH_ENDPOINT`.

## An endpoint on another machine

DEPENDS ON THE SCOPE RULING. The commands are the ones the previous release had, but the
install on the second machine now asks the person to type the server address
and the CA fingerprint (the targets' first-install rule). If the 0.5.6 scope
drops that, this section loses the two typed lines and the `checked` lines
and the rest stands. Nothing here is on the first page.

Install `mash` on the second machine as the first page does (prerequisite 2,
its first command and the PATH commands). The key is made THERE, so the
private key never travels:

```sh
mash key create --out bob.key
```

<!-- from: targets#key -->
```
$ mash key create --out bob.key
Created a key pair that proves this endpoint is who it says it is.
  private key  bob.key  keep this on this machine
  public key   bob.pub  share this one

NEXT: give bob.pub to whoever runs your mash server (you, if that is this
      machine). They run:
  mashctl endpoint create <owner>.<group>.bob --public-key bob.pub
```

Copy `bob.pub` to the machine that runs `mashctl`, and run there:

```sh
mashctl endpoint create acme.demo.bob --public-key bob.pub
```

<!-- from: targets#server -->
```
$ mashctl endpoint create acme.demo.bob --public-key bob.pub
Created endpoint acme.demo.bob.
The token says, signed by your server, that bob.pub is this endpoint.

  wrote  acme.demo.bob.token  (mode 600)

Give the person these apart from the token, by another route if you can:
  server          tls://myserver:4222; the person types it
  ca.pem          the file that identifies the authority (the CA) that
                  signed your server's certificate; it holds no secret
  CA fingerprint  a checksum of ca.pem; the person types it when installing
    18b3f713-809c9df9-4b85107f-964cd475-9b1e08e8-03a1e87d-1c00ba96-7bc06c52

NEXT: take acme.demo.bob.token and ca.pem to the endpoint's machine and run
      there; mash asks for the server address and the fingerprint above:
  mash credential install --key bob.key --token acme.demo.bob.token --ca ca.pem
```

Take `acme.demo.bob.token` and `ca.pem` to the second machine, and give the
person the server address and the fingerprint apart from the token. There:

```sh
mash credential install --key bob.key --token acme.demo.bob.token \
  --ca ca.pem
```

<!-- from: targets#install-main -->
```
$ mash credential install --key bob.key --token acme.demo.bob.token \
    --ca ca.pem
This is the first install on this machine, so mash needs to know who to
trust. Type what whoever runs your mash server gave you, apart from the
token. mash never prints the values it asks for.

Type the server address: tls://myserver:4222
Type the CA fingerprint: 18b3f713-809c9df9-4b85107f-964cd475-9b1e08e8-03a1e87d-1c00ba96-7bc06c52
fingerprint matches
checked  the server at myserver:4222 presents a certificate from this CA
checked  the token is signed by the account the server serves (acme)

Installed credentials for acme.demo.bob.
They hold bob's private key and the token. Keep bob.key as the backup.

  wrote   ~/.config/mash/acme.demo.bob.creds  (mode 600)
  wrote   ~/.config/mash/config.json          (server, CA)
  copied  ~/.config/mash/ca.pem               (from ca.pem, mode 644)
  wrote   ~/.config/mash/trust.json           (account, server, CA)  (mode 600)
  switched to the new generation  (all four files, at once)

The token file is not needed any more and mash kept no copy: delete
acme.demo.bob.token.

NEXT: check that acme.demo.bob can sign in:
  mash status
```

Then `mash status` there is healthy (the first page shows it).

## Reissue a key

Reissuing makes a new key and refuses the old one, and keeps the inbox. It
asks you to type the address. PENDING the mashctl lane: this is its text, not
yet built. The session that runs as Alice is signed out until it restarts.

```sh
mashctl endpoint reissue acme.demo.alice --make-key
```

<!-- external: PENDING ctl lane: mashctl endpoint reissue, from the mashctl targets revision 15, B1 -->
```
$ mashctl endpoint reissue acme.demo.alice --make-key

Reissuing acme.demo.alice will:
  - make a new key here and refuse alice's old key from now
  - sign out the session that runs as alice, until it is restarted
  - show mail alice sent before now as [unknown-signer] in alice's inbox,
    because the old key signed it and mash no longer accepts that key
  - keep alice's inbox and its 2 unread messages

Type the address to reissue it: acme.demo.alice

Reissued acme.demo.alice.
  Checked: the server now refuses alice's old key.
  Unread mail in alice's inbox is kept.

  wrote   ~/.config/mash/acme.demo.alice.creds  (mode 600; the new key)

Restart the session that runs as alice, on this machine, so it uses the new
credentials. Until then alice is signed out.
```

## Delete an endpoint, and see the proof

**This cannot be undone.** The address can never be used again and its inbox
goes with it. `mashctl` says what will be lost and asks you to type the
address.

```sh
mashctl endpoint delete acme.demo.bob
```

<!-- from: targets#ctl-delete -->
```
$ mashctl endpoint delete acme.demo.bob

Deleting acme.demo.bob will lose:
  - its inbox, with 1 message
  - its address: it is retired for good and cannot be used again
  - its key: the server will refuse bob's credentials from now, on every
    machine

Type the address to delete it: acme.demo.bob

Deleted acme.demo.bob. Its address cannot be used again.
  Checked: the server now refuses bob's old credentials.
  Its inbox is deleted, with 1 message.

Nothing can restore a deleted address.
```

Nothing follows a delete. The proof is on the endpoint's own machine, where
its credentials may still be:

```sh
export MASH_ENDPOINT=acme.demo.bob
mash status
```

<!-- from: targets#status-deleted -->
```
$ mash status
acme.demo.bob: mail does NOT work: this endpoint was DELETED on the server.

  version       0.5.6
  server        tls://myserver:4222
                alive, encrypted (checked 09:42)
  config        ok
  credentials   REFUSED (endpoint DELETED)

fix: its address cannot be used again. For a new endpoint, make a new key,
     then give its .pub file to whoever runs your mash server:
  mash key create --out <new-name>.key
```

The file `~/.config/mash/acme.demo.bob.creds` there holds a dead private key:
delete it. If the credentials were refused for another reason, the screen
below ("credentials refused") is the one you get.

## Stop and start the server

mash does not start or stop a server; the helper that made it does. Both
commands act only on the process the helper recorded.

```sh
bash "$HOME/mash-setup/nats-local-setup.sh" --dir "$HOME/mash-nats" --stop
```

While it is stopped `mash status` says so, and mail already sent stays on the
server until it is read:

<!-- from: targets#status-unreachable -->
```
$ mash status
acme.demo.bob: mail does NOT work: the server did not answer within 2s.

  version       0.5.6
  server        tls://myserver:4222
                NOT REACHABLE (last reached 3h ago)
  config        ok
  credentials   not checked (the server was not reached)

fix: check that the server is running and that "server" in
     ~/.config/mash/config.json is right.
```

```sh
bash "$HOME/mash-setup/nats-local-setup.sh" --dir "$HOME/mash-nats" --start
```

## Remove everything

Each of these cannot be undone, and the first types back the directory name.
They remove the server and its mail, mashctl's keys and every endpoint's
credentials on this machine, the downloads, the programs and the plugin.

```sh
bash "$HOME/mash-setup/nats-local-setup.sh" --dir "$HOME/mash-nats" --remove
```

```sh
rm -r "$HOME/.config/mash" "$HOME/mash-setup"
rm "$HOME/.local/bin/mash" "$HOME/.local/bin/mashctl"
```

The plugin (uninstall it before its folder goes):

```sh
claude plugin uninstall mash@mash
claude plugin marketplace remove mash
rm -r ./mash-plugin
```

The `PATH` line the first page added to `~/.zshrc` or `~/.bashrc` is harmless;
delete it if you like. `MASH_ENDPOINT` lives in your shell: `unset` it or
close the shell.

## When `mash status` says something else

Every screen has the same shape: a verdict, four rows, and a `fix:` unless
mail works. The exit code is 1 when mail does not work, 6 when the server did
not answer, and 4 when the server refused the credentials.

Credentials refused (exit 4). The server answered and does not accept the key
or the token: it was deleted or reissued. If reissued, move the old file
aside and install the new one, as the fix says.

<!-- from: targets#status-refused -->
```
$ mash status
acme.demo.bob: mail does NOT work: the server refused its credentials.

  version       0.5.6
  server        tls://myserver:4222
                alive, encrypted (checked 09:42)
  config        ok
  credentials   REFUSED (deleted, or its key was reissued)

fix: ask whoever runs your mash server which of the two it was. If the key
     was reissued, move the old credentials aside, then install the new key
     and token:
  mv ~/.config/mash/acme.demo.bob.creds ~/.config/mash/acme.demo.bob.creds.old
  mash credential install --key <key file> --token <token file>
```

No credentials on this machine:

<!-- from: targets#status-nocreds -->
```
$ mash status
acme.demo.bob: mail does NOT work: no credentials on this machine.

  version       0.5.6
  server        tls://myserver:4222
                alive, encrypted (checked 09:42)
  config        ok
  credentials   MISSING

fix: install the credentials made for this endpoint:
  mash credential install --key <key file> --token <token file>
```

A variable overriding the settings for this run (exit 1). mash reports
`MASH_SERVER` or `MASH_CA` when it differs from the settings, and does not
ask that server:

<!-- from: targets#status-env-override -->
```
$ export MASH_SERVER=tls://other:4222
$ mash status
acme.demo.bob: mail does NOT work: MASH_SERVER overrides config.json.

  version       0.5.6
  server        tls://other:4222
                not checked
                MASH_SERVER overrides config.json: tls://other:4222
                instead of tls://myserver:4222
  config        ok
  credentials   not checked (the server was not asked)

fix: MASH_SERVER is overriding the server in config.json for this run. Stop
     overriding it in this shell:
  unset MASH_SERVER
```

A plain `nats://` address, from the same variable (exit 1):

<!-- from: targets#status-plain-refused -->
```
$ export MASH_SERVER=nats://myserver:4222
$ mash status
acme.demo.bob: mail does NOT work: the server address is not encrypted.

  version       0.5.6
  server        nats://myserver:4222
                NOT ENCRYPTED (refused; not connected)
                MASH_SERVER overrides config.json: nats://myserver:4222
                instead of tls://myserver:4222
  config        ok
  credentials   not checked (the connection was refused)

fix: use a tls:// address. MASH_SERVER is overriding the server in
     config.json for this run. (With MASH_INSECURE_PLAINTEXT=1 credentials
     and mail would cross the network unencrypted: test servers only.)
     Stop overriding it in this shell:
  unset MASH_SERVER
```

No configuration yet (this fix asks for the server address and fingerprint:
it depends on the scope ruling, like the section on another machine):

<!-- from: targets#status-noconfig -->
```
$ mash status
mail does NOT work: mash has no configuration on this machine.

  version       0.5.6
  server        NOT SET
                not checked
  config        MISSING (~/.config/mash/config.json)
  credentials   not checked

fix: install the credentials your server made. mash asks for the server
     address and the CA fingerprint that whoever runs your mash server gave
     you. Add --ca <ca file> for a server with its own CA:
  mash credential install --key <key file> --token <token file>
```

## Notes for reviewers

Not README text. This page is the "Day 2" companion of the plain-path quick
start (`2026-11-19-readme-quickstart-plain-mash.md`). Every `from` block is
copied by `p2/plain-gen.py` from the targets by its `packet:` key and checked
byte for byte by `p2/lint-plain.py`.

Depends on the scope ruling: "An endpoint on another machine" and the
no-configuration screen. They use `mash credential install` with the typed
server address and CA fingerprint and the `trust.json` lines of the targets
(`install-main`). The plain 0.5.5-style flow without them is
`mash key create`, `mashctl endpoint create --public-key`, and
`mash credential install --key --token` with no typed lines; the targets have
no block for it, so this page does not show one. If the ruling drops the
trust work, `install-main` and `status-noconfig` must be regenerated first.

Dependencies on other lanes:

- ctl: `mashctl endpoint reissue` is DRAFT15's B1 text, copied as external.
  Its delete for a REMOTE endpoint prints a `NEXT:` (B4); the mash targets'
  `ctl-delete` has none (the no-NEXT-after-delete rule). The delete proof here
  uses the targets' block. Whether `status` can say DELETED, not only REFUSED,
  needs the server to say so (targets N11): until then the refused screen is
  what appears.
- policy: `--stop`, `--start` and `--remove` and their confirmation are from
  the helper guide revision 5 (section 6 and "Remove it completely"). Their
  output is not shown: it is not in the mash targets.
- Exit 4 means only "the server refused the credentials" (the lead's ruling);
  "no terminal" for a confirmation is exit 2. mashctl's own targets still use
  4 for the second: ctl must agree.
- `status-env-override` is a new block of the targets, added in the same
  change as this page (the freeze fix for the environment override).

If the scope changes: dropping the trust work removes the two typed lines
from the other-machine flow; dropping `mashctl endpoint reissue` removes that
section; nothing else here depends on it.
