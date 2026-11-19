## Day 2 with mash

The first page, [two agents and you](2026-11-19-readme-quickstart-plain-mash.md),
made three endpoints on one machine and deleted them. This page is what comes
after: an endpoint on another machine, a reissued key, a deletion with its
proof, stopping and starting the server, removing everything, and what each
failing `mash status` means. Every address is `acme.demo.name`; every
command that picks an endpoint uses `MASH_ENDPOINT`.

## An endpoint on another machine

Nothing here is on the first page. The key is made on the second machine, so
the private key never travels. Install `mash` there as the first page does
(prerequisite 2, its first command, and the PATH lines); `mashctl` stays on
the machine that runs the server. There:

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
  server          tls://myserver:4222
  ca.pem          the file that identifies the authority (the CA) that
                  signed your server's certificate; it holds no secret
  CA fingerprint  a checksum of ca.pem; the person compares it when installing
    18b3f713-809c9df9-4b85107f-964cd475-9b1e08e8-03a1e87d-1c00ba96-7bc06c52

NEXT: take acme.demo.bob.token and ca.pem to the endpoint's machine and run
      there; mash shows the server and the fingerprint to compare:
  mash credential install --key bob.key --token acme.demo.bob.token --server tls://myserver:4222 --ca ca.pem
```

Take `acme.demo.bob.token` and `ca.pem` to the second machine. Send the
person the server address and the CA fingerprint by another route than the
token: they decide whether the CA file is yours. There, use the server address
you were sent (the screens show `tls://myserver:4222`):

```sh
mash credential install --key bob.key --token acme.demo.bob.token \
  --server tls://myserver:4222 --ca ca.pem
```

<!-- from: targets#install-main -->
```
$ mash credential install --key bob.key --token acme.demo.bob.token \
    --server tls://myserver:4222 --ca ca.pem
First install on this machine. mash will connect only to this server, and
trust only server certificates signed by this CA:
  server          tls://myserver:4222
  CA fingerprint  of ca.pem
    18b3f713-809c9df9-4b85107f-964cd475-9b1e08e8-03a1e87d-1c00ba96-7bc06c52
Compare both with what whoever runs your mash server sent you.

checked  the server at myserver:4222 presents a certificate from this CA

Installed credentials for acme.demo.bob.
They hold bob's private key and the token. Keep bob.key as the backup.

  wrote   ~/.config/mash/acme.demo.bob.creds  (mode 600)
  wrote   ~/.config/mash/config.json          (server, CA)
  copied  ~/.config/mash/ca.pem               (from ca.pem, mode 644)

The token file is not needed any more and mash kept no copy: delete
acme.demo.bob.token.

NEXT: check that acme.demo.bob can sign in:
  mash status
```

Nothing is typed back: compare the server and the fingerprint it shows with
what you were sent, and stop if they differ (it lists the files it wrote:
remove them). Then `mash status` there is healthy (the first page shows it).

## Reissue a key

Reissuing makes a new key and refuses the old one, and keeps the inbox. It
asks you to type the address. PENDING the mashctl lane: this is its text, not
yet built. The session that runs as Alice is signed out until it restarts.

```sh
mashctl endpoint reissue acme.demo.alice --make-key
```

<!-- external: PENDING ctl lane: mashctl endpoint reissue, from the mashctl targets revision 19, B1 -->
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
address. It leaves the endpoint's credentials file where it was.

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
its credentials are still:

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
  mailbox       not checked

fix: its address cannot be used again. For a new endpoint, make a new key,
     then give its .pub file to whoever runs your mash server:
  mash key create --out <new-name>.key
```

The file `~/.config/mash/acme.demo.bob.creds` there holds a dead private key:
delete it with `rm`. If the credentials were refused for another reason (a
reissued key), the screen under "Credentials refused" below is the one you get.

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
  mailbox       not checked

fix: check that the server is running and that "server" in
     ~/.config/mash/config.json is right.
```

```sh
bash "$HOME/mash-setup/nats-local-setup.sh" --dir "$HOME/mash-nats" --start
```

The server does not survive a reboot: run the start command again after one.

## Remove everything

Each of these cannot be undone, and the first types back the directory name.
They remove the server and its mail, mashctl's keys and every endpoint's
credentials on this machine, the downloads, the programs and the plugin.

```sh
bash "$HOME/mash-setup/nats-local-setup.sh" --dir "$HOME/mash-nats" --remove
```

```sh
rm -r "$HOME/.config/mash" "$HOME/mash-setup"
rm "$HOME/.local/bin/mash" "$(go env GOPATH)/bin/mashctl"
rm SHA256SUMS mash.intoto.jsonl
```

The last line is for the folder you downloaded the release files into. The
plugin (uninstall it before its folder goes):

```sh
claude plugin uninstall mash@mash
claude plugin marketplace remove mash
rm -r ./mash-plugin
```

The `PATH` line the first page added to `~/.zshrc` or `~/.bashrc` is harmless;
delete it if you like. `MASH_ENDPOINT` lives in your shell: `unset` it or
close the shell.

## When the install check fails

The paste on the first page stops at the first failing step and installs
nothing. A `SHA256SUMS` that was changed after the release fails like this:

<!-- from: targets#install-check.3 -->
```
✗ Sigstore verification failed

Error: verifying with issuer "sigstore.dev"
```

Delete the downloaded files and do not go on. A line that says `FAILED` after
`sha256sum` or `shasum` means the same.

## When `mash status` says something else

Every screen has the same shape: a verdict, five rows, and a `fix:` unless
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
  mailbox       not checked

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
  mailbox       not checked

fix: install the credentials made for this endpoint:
  mash credential install --key <key file> --token <token file>
```

No configuration yet (the fix asks for the server address and the CA file
whoever runs the server sent you):

<!-- from: targets#status-noconfig -->
```
$ mash status
mail does NOT work: mash has no configuration on this machine.

  version       0.5.6
  server        NOT SET
                not checked
  config        MISSING (~/.config/mash/config.json)
  credentials   not checked
  mailbox       not checked

fix: install the credentials your server made. Give the server address and
     the CA file whoever runs your mash server sent you:
  mash credential install --key <key file> --token <token file> --server <address> --ca <ca file>
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
  mailbox       not checked

fix: MASH_SERVER is overriding the server in config.json for this run. Stop
     overriding it in this shell:
  unset MASH_SERVER
```

A plain `nats://` address, from the same variable (exit 1). The refusal names
the variable that would allow it, `MASH_DANGEROUSLY_SKIP_NATS_ENCRYPTION=1`:
with it credentials and mail cross the network unencrypted, so it is for test
servers only, and an agent must not set it:

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
  mailbox       not checked

fix: use a tls:// address. MASH_SERVER is overriding the server in
     config.json for this run. (With MASH_DANGEROUSLY_SKIP_NATS_ENCRYPTION=1
     credentials and mail would cross the network unencrypted: test servers
     only.) Stop overriding it in this shell:
  unset MASH_SERVER
```

When that variable is set anyway, status reports it (exit 0). With a plain
address it says so:

<!-- from: targets#status-plain-warning -->
```
$ export MASH_SERVER=nats://myserver:4222
$ export MASH_DANGEROUSLY_SKIP_NATS_ENCRYPTION=1
$ mash status
acme.demo.bob: mail works (1 warning).

  version       0.5.6
  server        nats://myserver:4222
                alive, NOT ENCRYPTED (checked 09:42)
                MASH_SERVER overrides config.json: nats://myserver:4222
                instead of tls://myserver:4222
                allowed by MASH_DANGEROUSLY_SKIP_NATS_ENCRYPTION
  config        ok
  credentials   ok
  mailbox       2 waiting

warning: anyone on the network can read your credentials and your mail.
         MASH_SERVER is overriding the server in config.json for this run.
         Use this only for a test server.
fix: stop overriding it in this shell:
  unset MASH_SERVER MASH_DANGEROUSLY_SKIP_NATS_ENCRYPTION
```

and with a tls address it says the variable is set and not used:

<!-- from: targets#status-plain-inherited -->
```
$ mash status
acme.demo.bob: mail works (1 warning).

  version       0.5.6
  server        tls://myserver:4222
                alive, encrypted (checked 09:42)
                MASH_DANGEROUSLY_SKIP_NATS_ENCRYPTION is set (not used)
  config        ok
  credentials   ok
  mailbox       0 waiting

warning: MASH_DANGEROUSLY_SKIP_NATS_ENCRYPTION is set; mail may be unencrypted.
fix: unset it unless this is a test server:
  unset MASH_DANGEROUSLY_SKIP_NATS_ENCRYPTION
```

Claude Code was started without the mash channel (exit 0): mail still works,
but an idle session is not woken.

<!-- from: targets#status-claude -->
```
$ mash status
acme.demo.bob: mail works (1 warning).

  version       0.5.6
  server        tls://myserver:4222
                alive, encrypted (checked 09:42)
  config        ok
  credentials   ok
  mailbox       3 waiting

warning: an idle Claude Code session is not woken by new mail; the mail
         waits for its next prompt.
fix: start Claude Code with the mash channel, from the terminal you want it in:
  claude --dangerously-load-development-channels plugin:mash@mash
```

More than 15 messages waiting (exit 0): mail works, and the session is woken
at most every 10 minutes while it stays over.

<!-- from: targets#status-mailbox-over -->
```
$ mash status
acme.demo.bob: mail works (1 warning).

  version       0.5.6
  server        tls://myserver:4222
                alive, encrypted (checked 09:42)
  config        ok
  credentials   ok
  mailbox       22 waiting, OVER THE LIMIT OF 15

warning: 22 messages are waiting for acme.demo.bob, and the oldest has
         waited 12m. mash serve wakes the session at most every 10 minutes
         while the mailbox is over the limit.
fix: have the session read its mail. To change the limit, add this line to
     ~/.config/mash/config.json (0 turns the check off):
    "mailbox_limit": 40
```

## Notes for reviewers

Not README text. This page is the "Day 2" companion of the plain-path quick
start (`2026-11-19-readme-quickstart-plain-mash.md`), rebuilt for the ruled
scope (decision 264, contract rev 15). Every `from` block is copied by
`p2/plain-gen.py` from the targets (revision 7) by its `packet:` key and checked
byte for byte by `p2/lint-plain.py`, which also rejects trust, generation,
plugin-install, install-script and `MASH_INSECURE_PLAINTEXT` text, and the
plain-text override anywhere before the failing-status section.

Real size: 521 lines in this file, of which 477 are
page text above these notes.

Dependencies on other lanes:

- ctl: `mashctl endpoint reissue` is revision 19's B1 text, copied as external.
  revision 19's `delete` for a same-machine endpoint adds `Its credentials file
  here was left as it was; mash status will report it as DELETED.`, which the
  targets' `ctl-delete` does not show. Whether `status` can say DELETED, not
  only REFUSED, needs the server to say so (targets N11): until then the
  refused screen is what appears. `--delete-creds` (typed confirmation) is
  ctl's; this page deletes the file by hand.
- policy: `--stop`, `--start` and `--remove` and the confirmation are from the
  helper guide revision 7 (section 6). Their output is not shown: it is not in the
  mash targets. The guide's `--remove` also needs a terminal and a typed
  directory path.
- targets: `credential install` on the other machine takes `--server` and
  `--ca` on a first install and shows the fingerprint to compare, as the
  targets print it. The brief's form `[--server URL]` is the same command with
  the flag optional; the targets make it required on a first install. The
  address in the pasted block is the targets' `myserver`; the page says to use
  the one you were sent. `install-check.3` (a failed attestation) is `gh`'s
  own text from a real run on the v0.5.5 bundle.
- Exit 4 means not authorized, with two messages: the server refused the
  credentials, or a person must run a confirmation at a terminal (INV-004).
- The status screens carry the mailbox row (REQ-015); the "More than 15"
  screen is the targets' `status-mailbox-over` with 22 waiting.
- The remove block uses `$(go env GOPATH)/bin/mashctl`: the first page puts
  `mashctl` there, not in `~/.local/bin` (the previous Day 2 removed the wrong
  path).

If the scope changes: a ruling that brings back the trust record changes
`install-main` and `status-noconfig` first; dropping `mashctl endpoint
reissue` removes that section.
