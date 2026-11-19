# Parked: guide parts 4 and 5 (DRAFT7 text)

Taken off the reader's page for DRAFT8 (planner, lead #31). To be
rewritten from the BUILT commands with real output, after ctl builds
`adopt --from` and the release file exists. Not for review before then.
Reviewer notes belong in this file, not in the guide.

## 4. Install mash and mashctl

Check what you have (read-only). Expected: two "No such file" errors and
`checked`:

```sh
"$HOME/.local/bin/mash" version; "$HOME/.local/bin/mashctl" version; echo checked
```

If one prints a version, stop and ask. Debian and Ubuntu put
`$HOME/.local/bin` on your PATH at your next login; until then every
block here spells the full path.

The releases are signed by the project's own build, and GitHub's `gh`
checks that signature. Debian's own `gh` is too old, so fetch the pinned
one (v2.105.0, its hash is checked). Nothing is installed if the hash is
wrong:

```sh
mkdir -p "$HOME/mash-setup/dl" "$HOME/.local/bin" && cd "$HOME/mash-setup/dl" && curl -fsSLO https://github.com/cli/cli/releases/download/v2.105.0/gh_2.105.0_linux_amd64.tar.gz && echo "79e7ab5db738c48777b696e374f8b428f0647debfb424f44d2f426370407fe2f  gh_2.105.0_linux_amd64.tar.gz" | sha256sum -c - && tar -xzf gh_2.105.0_linux_amd64.tar.gz && install -m 755 gh_2.105.0_linux_amd64/bin/gh "$HOME/.local/bin/gh"
```

Expected: `gh_2.105.0_linux_amd64.tar.gz: OK`. Download the release files
into the same folder. The signed record, `mash.intoto.jsonl`, covers
every binary:

```sh
cd "$HOME/mash-setup/dl" && for f in mash-linux-amd64 mashctl-linux-amd64 mash.intoto.jsonl SHA256SUMS; do curl -fsSLO "https://github.com/bogworks/mash/releases/download/v0.5.6/$f"; done
```

(mashctl as a release file is PENDING: if the second one answers `404`,
mashctl is not a release asset yet; ask the person.) Verify each program
against the signed record. One command per file; it needs no login. No
output is printed when the output is not a terminal, so the exit code is
the answer:

```sh
cd "$HOME/mash-setup/dl" && "$HOME/.local/bin/gh" attestation verify mash-linux-amd64 -R bogworks/mash --bundle mash.intoto.jsonl --signer-workflow bogworks/mash/.github/workflows/release.yml --source-ref refs/tags/v0.5.6 && echo "verified mash"
```

```sh
cd "$HOME/mash-setup/dl" && "$HOME/.local/bin/gh" attestation verify mashctl-linux-amd64 -R bogworks/mash --bundle mash.intoto.jsonl --signer-workflow bogworks/mash/.github/workflows/release.yml --source-ref refs/tags/v0.5.6 && echo "verified mashctl"
```

Expected: `verified mash`, then `verified mashctl`. `Verification failed`,
`FAILED` or any error means stop: nothing is installed yet, and the files
stay in `$HOME/mash-setup/dl`. Check the file hashes too:

```sh
cd "$HOME/mash-setup/dl" && grep -e ' mash-linux-amd64$' -e ' mashctl-linux-amd64$' SHA256SUMS | sha256sum -c -
```

Expected: `mash-linux-amd64: OK` and `mashctl-linux-amd64: OK`. Install, only
after all four checks passed:

```sh
cd "$HOME/mash-setup/dl" && chmod +x mash-linux-amd64 mashctl-linux-amd64 && mv mash-linux-amd64 "$HOME/.local/bin/mash" && mv mashctl-linux-amd64 "$HOME/.local/bin/mashctl"
```

```sh
"$HOME/.local/bin/mashctl" version
```

Expected: one line with mashctl's version, 0.5.6.

## 5. Adopt the server, then test

You run this as the same user that ran the script, on the same machine,
so mashctl is not asked to type the CA fingerprint: the files are
already yours. (A person on another machine does type it; see
`WHAT-MASH-NEEDS.md`.) First the read-only check, which changes nothing:

```sh
"$HOME/.local/bin/mashctl" adopt --from "$HOME/mash-nats" --dry-run
```

Expected: numbered checks, each `ok` (the files; the server refuses a
connection without credentials and accepts the system credentials; the
account exists; storage is given; the system user works; the operator
key signed the account; the admin credentials connect), then `All checks
passed.` and a list of what adopt would do. A failed check says what was
wrong and that nothing was changed. Now the real one:

```sh
"$HOME/.local/bin/mashctl" adopt --from "$HOME/mash-nats"
```

Expected: the same checks, `All checks passed.`, one `copied` line per
file (the CA, the two keys, the two credentials, with modes), a
`config.json` line, and a line that mash's storage is ready. mashctl
now has its own copies in `$HOME/.config/mash`, so `export/` is spare.

The modes of mashctl's folder (it holds keys, so it must not be open):

```sh
stat -c '%a %n' "$HOME/.config/mash" "$HOME/.config/mash"/*
```

This is the planned output of the 0.5.6 mashctl, to be re-checked against
the real command once it exists (0.5.5's adopt copies no CA). Expected, in
this order (the list is sorted by name): `700` for the
folder, `644` for `ca.pem`, `600` for `config.json`, `700` for `keys` and
`700` for `server`. The CA is meant to be 0644: do not change it. If any
other number differs, `chmod` it to the value above and tell the person.

### Alice and Bob

The group `demo` comes into being when the first agent uses it.
`--make-key` makes the agent's own key on this machine and signs it;
nothing is asked, because the signing keys are already yours:

```sh
"$HOME/.local/bin/mashctl" endpoint create acme.demo.alice --make-key --creds-out "$HOME/.config/mash/acme.demo.alice.creds"
```

Expected: a line saying the endpoint was created, its address
`acme.demo.alice`, and the path of its `.creds` file. Bob the same way:

```sh
"$HOME/.local/bin/mashctl" endpoint create acme.demo.bob --make-key --creds-out "$HOME/.config/mash/acme.demo.bob.creds"
```

```sh
"$HOME/.local/bin/mashctl" endpoint list
```

Expected: two lines, `acme.demo.alice` and `acme.demo.bob`, each with
its creation time, then `2 endpoints`. `mash` finds the server and the
agent's credentials from mashctl's folder and the address you name.
Alice sends, Bob reads:

```sh
MASH_ENDPOINT=acme.demo.alice "$HOME/.local/bin/mash" send acme.demo.bob "Hello Bob"
```

Expected: `sent #1 from acme.demo.alice to acme.demo.bob`.

```sh
MASH_ENDPOINT=acme.demo.bob "$HOME/.local/bin/mash" inbox
```

Expected: one line that starts `#1`, then `from acme.demo.alice
[verified]`, the time, `9 B` and `Hello Bob`. `[verified]` means the
message is signed by Alice's key. It works.

The script's `export/` folder still holds spare copies of the keys and
credentials, and the CA key and signing store stay in the server
directory. Moving those off this machine is optional and is in
`WHAT-MASH-NEEDS.md`.


