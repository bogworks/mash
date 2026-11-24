Generated from spec/*.json by go run ./scripts/refgen. Do not edit.

# Setup scripts

The scripts that install mash and set up a local NATS server.

## mash-migrate-0.5.5.sh

For a machine that ran mash 0.5.5. Run once. It rewrites config.json's two
renamed keys (url becomes servers, tls_ca becomes ca), keeps the old file
beside it as a backup, prints one line saying what it changed, and touches
nothing on the server. Addresses written with slashes and the old variable
names are not its business: they are refused where they are used, each with
its own error line.

```sh
curl -fsSL <release base>/mash-migrate-0.5.5.sh | bash
```

## install.sh

Finds the machine's type, downloads mash and mashctl from the same release,
checks each against the release's checksums, and against the release's signed
record too when gh 2.102.0 or newer is there to do it, printing one line when
that check passes: 'Verified the release attestation for bogworks/mash
v0.5.6', checked against the commit the release tag points to (with no gh, a
gh that does not run, a gh whose version it cannot read, or one older than
2.102.0, it prints one warning line on stderr that says which, such as
'warning: gh not found: checked checksums only, not verified'; if the check
fails it prints 'error: the release failed verification; nothing was
installed' on stderr, installs nothing and exits 1; if it cannot read that
commit it prints 'error: could not read the commit of bogworks/mash v0.5.6;
nothing was installed' on stderr, installs nothing and exits 1), puts them in
~/.local/bin unless MASH_INSTALL_DIR names another folder, and prints one line
for each, with the path it actually used: 'Installed mash v0.5.6 (3f9a41c) in
~/.local/bin'. It asks nothing and needs no sudo. If that folder is not on the
PATH it says so in one line. If the folder, or a folder above it, can be
written by its group or by other users, it installs nothing and names that
folder and the fix: chmod go-w with the folder in single quotes, then run it
again.

```sh
curl -fsSL <release base>/install.sh | bash
```

## mash-local-nats-setup.sh

For a NATS server on this machine. It works on Linux and on macOS, where many
developers will first try mash. First install nats-server (version 2.10.7 or
newer) and the nats command. nats-server comes from your package manager. The
nats command has no Debian or Ubuntu package: on those, get it from the
natscli GitHub release. Homebrew has both. The script also needs openssl, to
make the server's certificate. If nats-server is not on the PATH, it also
looks in /usr/sbin, /usr/local/sbin and /sbin, as Debian's package installs it
there; it uses one found there only if it and its folder are owned by root and
not writable by others, and says which one it uses. It names anything missing,
says where to get it, and stops. It does not need nsc. If port 4222 is already
taken, as it is where a packaged nats-server runs as a system service, the
script leaves that service alone, uses the next free port and says which. It
asks one question, the owner's name, reading it from the terminal; --owner
NAME answers it on the command line and nothing is asked, which is the form an
agent or a script uses, since it has no terminal. It makes a server that only
this machine can reach, with encryption on, starts it as your own user, and
writes the setup file. That is its whole scope: a join file made against it
works only on this machine. Two machines need a server both can reach, with a
certificate that covers its address; in 0.5.6 that is a NATS server you run
yourself, which mashctl init takes from a setup file (docs/install.md).
Planned for 0.6.0: a script option for a reachable address and certificate.
Everything goes in ~/mash-nats unless --dir names another folder. Its last
line is the next command, with the path it actually wrote: 'mashctl init
--config ~/mash-nats/mash-setup.yaml' by default. Guides and skills take that
path from the script's output, never from memory. The NATS account it makes is
named after the owner; the system account keeps NATS's own name, SYSTEM. The
server runs as your own user's process, not as a system service, so it does
not come back after a reboot: run the script again with --start to start it
(it never creates anything then), --stop to stop it, or --remove to remove it.
When --start finds its server process already running, it first checks that
the server answers: if it does, it says 'already running'; if the process is
alive but the server does not answer, it stops with an error and changes
nothing (it does not kill or restart), and the hint is to run --stop, then
--start. After --remove it prints two lines: that mashctl's folder still
points at the server just removed, and how to set up again (a new
configuration folder, or moving the old one aside).

```sh
curl -fsSL <release base>/mash-local-nats-setup.sh | bash
```

## &lt;release base&gt;

Every address in these commands is written from the release's own setting, so
a fork or a mirror points at itself; in the project's own releases it is the
address shown here. Two printed lines carry an address: the hint for a folder
left by mash 0.5.5, which is written from the release's own setting, and the
plugin's 'mash is not installed' line, which carries the address shown here,
written into the launcher (mash hook).

```sh
https://github.com/bogworks/mash/releases/latest/download
```

## For an agent

The setup skill does not pipe a download into a shell. It downloads each
script, checks it against the release's checksums, shows the person what it is
about to run, and runs it after a yes.

```sh
(the setup skill)
```
