## Day 2 with mash

The first page, [two agents and you](2026-11-19-readme-quickstart-plain-mash.md),
made three endpoints on one machine and deleted them. This page is what comes
after: what mash says when two endpoints are installed and none is chosen, an
endpoint on another machine, deleting an endpoint and checking it is gone,
stopping and starting the server, removing everything, and what each failing
`mash status` means. Every address is `acme.demo.name`; every command that
picks an endpoint uses `MASH_ENDPOINT`. The screens are written with the names
`bob` and `alice` from the first page: yours show your own.

## When two endpoints are installed and none is chosen

mash never guesses between endpoints. This is what it says when two are
installed on a machine and none is chosen, with `alice` and `bob` as on the
first page; its fix is the last line. It is a refusal on purpose (exit 1), not
a fault:

<!-- from: targets#status-choose -->
```
$ mash status
mail does NOT work: 2 endpoints are installed here and none is chosen.

  version       0.5.6
  server        tls://localhost:4222
                alive, encrypted (checked 09:42)
  config        ok
  credentials   not checked (acme.demo.alice, acme.demo.bob)
  mailbox       not checked

fix: mash never guesses between them. To use acme.demo.alice (or
     acme.demo.bob), add this line to ~/.config/mash/config.json:
    "endpoint": "acme.demo.alice"
     For this shell only, and the Claude Code you start from it:
  export MASH_ENDPOINT=acme.demo.alice
```

Choose one for a shell with `export MASH_ENDPOINT=...`, or for the machine
with the `"endpoint"` line in `~/.config/mash/config.json`, as the fix says.

## An endpoint on another machine

The server the first page made listens on this machine only; another machine
needs a server it can reach, which the helper does not set up in 0.5.6, and
[the requirements page](https://github.com/bogworks/mash/blob/main/docs/tls-and-clusters.md)
says what that server needs.

Given such a server, this adds the endpoint `acme.demo.carol` on a second
machine. The private key never travels: it is made on the machine that will
use it. Only the public key and the token cross between machines.

1. On the second machine, install `mash` as the first page does (Prerequisite
   2, its first command, which needs `gh`) and put `~/.local/bin` on the PATH.
   `mashctl` stays on the machine that runs it.
2. On the second machine, run `mash key create --out carol.key`. It makes
   `carol.key` (the private key: it stays there) and `carol.pub` (the public
   key).
3. Copy `carol.pub` to the machine that runs `mashctl` and run there
   `mashctl endpoint create acme.demo.carol --public-key carol.pub`. It writes
   the token, `acme.demo.carol.token`, in the folder you ran it from.
4. Take `acme.demo.carol.token` and the CA file to the second machine
   (`~/.config/mash/ca.pem` on the machine that runs `mashctl`; it holds no
   secret). Send the person the server address and the CA's fingerprint by
   another route than the token.
5. On the second machine, run `mash credential install --key carol.key --token
   acme.demo.carol.token --ca ca.pem --server` followed by the address of the
   server (a `tls://` address). On a first install both `--ca` and `--server`
   are required; each missing one is a usage error and nothing is read. mash
   shows the server address and the CA's fingerprint (eight groups of eight
   lower-case hex digits joined by `-`): compare both with what you were sent,
   and stop if they differ. Nothing is typed back. It writes the CA copy, the
   credentials and, last, the configuration, so running the same command
   again completes an interrupted install. It never changes a file that is
   already installed: on a difference it says which file to move aside.
   Delete `acme.demo.carol.token` afterwards: mash kept no copy.
6. `mash status` there is healthy (the first page shows the screen).

## Delete an endpoint on another machine, and see the proof

**This cannot be undone.** The address can never be used again and its inbox
goes with it. On the machine that runs `mashctl`, the command is the one from
the first page's Step 5, `mashctl endpoint delete acme.demo.carol`: it says
what will be lost, asks you to type the address, and prints no next-step hint.
It leaves the endpoint's credentials file where it was.

The proof is on the endpoint's own machine, where its credentials still are:
`mash status` there says the endpoint was DELETED on the server and exits 4
(the first page's Step 5 shows the screen, with `bob`'s name). The file
`~/.config/mash/acme.demo.carol.creds` there holds a dead private key: delete
it with `rm`. Close the endpoint's Claude Code session on that machine first,
if one is running.

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
  server        tls://localhost:4222
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

Each of these cannot be undone, and the first asks you to type the folder
name back (it needs a terminal). They remove the server and its mail,
mashctl's keys and every endpoint's credentials on this machine, the
downloads, the programs and the plugin.

```sh
bash "$HOME/mash-setup/nats-local-setup.sh" --dir "$HOME/mash-nats" --remove
```

```sh
rm -r "$HOME/.config/mash" "$HOME/mash-setup"
rm "$HOME/.local/bin/mash" "$(go env GOPATH)/bin/mashctl"
```

`~/mash-setup` holds the helper and the downloads of the first page. The
plugin (uninstall it before its folder goes; the folder is the one the first
page exported it to):

```sh
claude plugin uninstall mash@mash
claude plugin marketplace remove mash
rm -r "$HOME/mash-plugin"
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

Delete the downloads (`rm -r "$HOME/mash-setup/dl"`) and do not go on. A line
that says `FAILED` after `sha256sum` means the same.

## When `mash status` says something else

Every screen has the same shape: a verdict, five rows, and a `fix:` unless
mail works. When mail works the exit code is 0, with or without a warning;
when it does not it is 1, except 6 when the server did not answer and 4 when
the server refused the credentials. If the server did not answer, check that
it is running: the helper's `--start` above starts it, and it needs starting
again after a reboot.

Credentials refused (exit 4). The server answered and does not accept the key
or the token: the endpoint was deleted, or its key was replaced. The fix says
what to do.

<!-- from: targets#status-refused -->
```
$ mash status
acme.demo.bob: mail does NOT work: the server refused its credentials.

  version       0.5.6
  server        tls://localhost:4222
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
  server        tls://localhost:4222
                alive, encrypted (checked 09:42)
  config        ok
  credentials   MISSING
  mailbox       not checked

fix: install the credentials made for this endpoint:
  mash credential install --key <key file> --token <token file>
```

No configuration yet (a machine where nothing was installed): the screen says
so, and its fix is the `mash credential install` command with `--server` and
`--ca` from "An endpoint on another machine" above.

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
                instead of tls://localhost:4222
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
$ export MASH_SERVER=nats://localhost:4222
$ mash status
acme.demo.bob: mail does NOT work: the server address is not encrypted.

  version       0.5.6
  server        nats://localhost:4222
                NOT ENCRYPTED (refused; not connected)
                MASH_SERVER overrides config.json: nats://localhost:4222
                instead of tls://localhost:4222
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
$ export MASH_SERVER=nats://localhost:4222
$ export MASH_DANGEROUSLY_SKIP_NATS_ENCRYPTION=1
$ mash status
acme.demo.bob: mail works (1 warning).

  version       0.5.6
  server        nats://localhost:4222
                alive, NOT ENCRYPTED (checked 09:42)
                MASH_SERVER overrides config.json: nats://localhost:4222
                instead of tls://localhost:4222
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
  server        tls://localhost:4222
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
  server        tls://localhost:4222
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
  server        tls://localhost:4222
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

## Not available until the 0.5.6 release

Replacing an endpoint's key while keeping its inbox (`mashctl endpoint
reissue`) is not available until the release. Until then a key cannot be
replaced: to stop an endpoint's key from working, delete the endpoint (above)
and make a new one with a new name, because a deleted address cannot be used
again.
