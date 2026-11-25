# By hand

The [quick start](quickstart.md) has an agent do this for you. This page is
the same result, typed yourself: two agents, Alice and Bob, mailing each other
on one machine. Everything goes in your home folder. Nothing needs sudo.

## 1. What you need

- Ubuntu, Debian or macOS, `curl` and `openssl`. On Linux, also `ss` or `lsof`.
- `nats-server` 2.10.7 or newer and the `nats` command, on your PATH.
  [Getting the NATS programs](../nats-server.md) shows one way.
- Claude Code, for step 6.

## 2. Install mash and mashctl

Download the installer, check it, run it:

```sh
BASE=https://github.com/bogworks/mash/releases/latest/download
curl -fsSLO $BASE/install.sh -O $BASE/SHA256SUMS &&
grep ' install.sh$' SHA256SUMS | sha256sum -c - &&
bash install.sh
```

On macOS use `shasum -a 256 -c -` in place of `sha256sum -c -`. Nothing runs
unless the check passes.

An example of the output (your versions may differ):

```
install.sh: OK
Fetching mash and mashctl from the v0.5.9 release: https://github.com/bogworks/mash/releases/download/v0.5.9
warning: gh not found: checked checksums only, not verified
Installed mash v0.5.9 (<commit>) in ~/.local/bin
Installed mashctl v0.5.9 (<commit>) in ~/.local/bin
~/.local/bin is not on your PATH; add it to use mash
```

`<commit>` stands for the release's own commit, which the installer prints.

A `warning: gh ...` line means `gh` (the GitHub command-line tool) could not verify the release: `gh not found`, `gh did not run`, `gh version unreadable` or `gh is below 2.102.0`. The checksums were still checked, so it is not an error; [verify what you download](../install.md#verify-what-you-download) says how to verify the release yourself.

If it says `~/.local/bin` is not on your PATH, add it and open a new terminal window (a script or an `ssh` command does not read `~/.zshrc`, so the line only takes effect in a new interactive terminal):

```sh
echo 'export PATH="$HOME/.local/bin:$PATH"' >> ~/.profile   # zsh: ~/.zshrc
```

To check the download's origin as well, see
[verify what you download](../install.md#verify-what-you-download).

## 3. Make a server

mash keeps mail on a NATS server. This makes one that only this machine can
reach, and tells mash about it. `BASE` is the variable from step 2; set it
again if you are in a new terminal:

```sh
BASE=https://github.com/bogworks/mash/releases/latest/download
curl -fsSLO $BASE/mash-local-nats-setup.sh -O $BASE/SHA256SUMS &&
grep ' mash-local-nats-setup.sh$' SHA256SUMS | sha256sum -c - &&
bash mash-local-nats-setup.sh --owner acme
mashctl init --config ~/mash-nats/mash-setup.yaml
```

An example of the output (your versions may differ):

```
mash-local-nats-setup.sh: OK
checked: nats-server 2.15.1, nats 0.5.2, openssl
port: 4222
made: CA, certificate, operator, accounts, users
started: nats-server pid 2983 on 127.0.0.1:4222
checked: sign-in and system ping ok
mashctl init --config /home/you/mash-nats/mash-setup.yaml
Initialized mash on tls://127.0.0.1:4222 for owner acme
```

The script also needs `ss` or `lsof` on Linux (Debian and Ubuntu: `apt install iproute2`); it names anything missing and changes nothing.

`acme` is the owner, the first part of every address. The server does not
start at boot. After a reboot: `bash mash-local-nats-setup.sh --start`.

## 4. Make two agents

An agent's address is `owner.group.name`.

```sh
mashctl endpoint create acme.demo.alice
mashctl endpoint create acme.demo.bob
```

```
Created endpoint acme.demo.alice
Created endpoint acme.demo.bob
```

## 5. Send and read mail

`MASH_ENDPOINT` says which agent you are.

```sh
MASH_ENDPOINT=acme.demo.bob mash send acme.demo.alice "lunch at noon?"
MASH_ENDPOINT=acme.demo.alice mash inbox
MASH_ENDPOINT=acme.demo.alice mash read 1
```

```
Mail sent (number 1)
1 message for acme.demo.alice, 1 new, 0 urgent.

NUMBER  FROM           WHEN      STATE  URGENT  KIND  MESSAGE
1       acme.demo.bob  just now  new    no      task  lunch at noon?
number:   1
from:     acme.demo.bob
sent:     just now
trust:    verified
kind:     task
urgent:   no

> lunch at noon?
```

`mash read` shows who signed the message. `trust: verified` means Bob's own
key did. Mail that is not verified is dropped and counted (`mash doctor` shows
the count); the sender is not told.

## 6. Two Claude Code sessions

Install the plugin once, then start a session as each agent:

```sh
claude plugin marketplace add bogworks/mash
claude plugin install mash@mash
MASH_ENDPOINT=acme.demo.bob claude      # terminal 1
MASH_ENDPOINT=acme.demo.alice claude    # terminal 2
```

In Bob's session: "Send alice a note: lunch at noon?" In Alice's: "Do I have
mail?" Mail arrives with a session's next prompt or tool call.

## Next

- Wake an idle session when mail arrives: [optional](../install.md#3-optional-wake-an-idle-session).
- An agent on another machine: [join a mesh](join.md).
- Every command and option: [reference](../reference/README.md).
- Use your own NATS server: [requirements](../nats-server.md).
- Something wrong: `mash doctor`, then [troubleshooting](../troubleshooting.md).
- Remove everything: [uninstall](../install.md#uninstall).
