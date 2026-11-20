Generated from spec/*.json by go run ./scripts/refgen. Do not edit.

# Setup scripts

The scripts that install mash and set up a local NATS server.

## install.sh

Finds the machine's type, downloads mash and mashctl from the same release,
checks each against the release's checksums, puts them in ~/.local/bin unless
MASH_INSTALL_DIR names another folder, and prints one line for each, with the
path it actually used: 'Installed mash v0.5.6 (3f9a41c) in ~/.local/bin'. It
asks nothing and needs no sudo. If that folder is not on the PATH it says so
in one line.

```sh
curl -fsSL https://github.com/bogworks/mash/releases/latest/download/install.sh | bash
```

## mash-local-nats-setup.sh

For a NATS server on this machine. It works on Linux and on macOS, where many
developers will first try mash. First install nats-server with your package
manager (version 2.10.7 or newer) and the nats command: Homebrew has it, and
on Debian and Ubuntu, where there is no package for it, it comes from its
GitHub release. The script also needs openssl, to make the server's
certificate. It names anything missing, says where to get it, and stops. It
does not need nsc. If port 4222 is already taken, as it is where a packaged
nats-server runs as a system service, the script leaves that service alone,
uses the next free port and says which. It asks one question, the owner's
name, reading it from the terminal. It makes a server that only this machine
can reach, with encryption on, starts it as your own user, and writes the
setup file. Everything goes in ~/mash-nats unless --dir names another folder.
Its last line is the next command, with the path it actually wrote: 'mashctl
init --config ~/mash-nats/mash-setup.yaml' by default. Guides and skills take
that path from the script's output, never from memory. The NATS account it
makes is named after the owner; the system account keeps NATS's own name,
SYSTEM. The server runs as your own user's process, not as a system service,
so it does not come back after a reboot: run the script again with --start to
start it (it never creates anything then), --stop to stop it, or --remove to
remove it.

```sh
curl -fsSL https://github.com/bogworks/mash/releases/latest/download/mash-local-nats-setup.sh | bash
```

## For an agent

The setup skill does not pipe a download into a shell. It downloads each
script, checks it against the release's checksums, shows the person what it is
about to run, and runs it after a yes.

```sh
(the setup skill)
```
