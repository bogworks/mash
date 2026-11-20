# liveat: the live acceptance tests

`go run ./scripts/liveat at001|at002|at003|at004|all|selfcheck --port P` runs AT-001 to AT-003, and AT-4 (the security boundaries), of the 0.5.5 endpoint-model
contract against a build of this checkout, on one local NATS server, in scratch homes, **inside a throwaway container**.

Tests never run on the machine that runs the harness. On that machine `liveat at001` builds the node image
(`Containerfile.at005`: a pinned Debian with bash, openssl, iproute2, the static `mash`, `mashctl`, `nats-server` and
`nodeinit`, the pinned `nats` command and the setup script), starts ONE rootless container of it (label
`mash-at005=<run id>`, name `mash-at005-<run id>-sbx`, `--network none`, no published port, the non-root user `mash`, uid
1000), puts a static `liveat` in it and runs `liveat <the same arguments> --inside` there. Only `--inside` starts a
server; it is refused when `/run/.containerenv` does not exist (not a container, exit 2). Afterwards the host copies
`out/`, the records and `run.log` back with `podman cp` (the homes, keys and server directories stay in the container),
scans them for seeds and JWTs, prints the run's output, and destroys the container and the image by label. The exit
code is the run inside's (1 also when the host's scan finds a seed or a JWT; 4: the sandbox could not be made). Needs
rootless podman and network to pull `docker.io/library/debian:bookworm-slim` (the digest is recorded in the record's
environment) and to download the pinned natscli release (`natscli v0.5.1`, sha256 verified before use; see image.go).

Inside the container, the local server is the one `scripts/mash-local-nats-setup.sh --dir <new dir> --owner alice` makes and starts (the image
carries the checkout's copy; `LIVEAT_SETUP_SCRIPT` names the file to put in the image), followed by `mashctl init --config <dir>/mash-setup.yaml`: TLS only, with its own CA
(`ca.pem`, `server.pem`, `server.key` in the server directory). The script chooses its own port (4222, or the next free one
up to 4272); `P` is only checked free before it starts. It needs `nats`, `nats-server`, `openssl` and `ss`, all in the image, and refuses root (the container user is not root).
Every command of AT-001 to AT-003 connects to `tls://127.0.0.1:<the chosen port>` with `MASH_CA` set to
that `ca.pem` and no `MASH_INSECURE_PLAINTEXT` (REQ-014); AT-001 asserts that and that `mash doctor` says the server row is ok on tls://.
Only AT-003's plain-URL case sets the override, per command, against a counting listener. The server key's text is
searched for in all captured output like a seed.

## AT-4: the security boundaries (`at004`)

`liveat at004 --port P` runs in the same sandbox container as `at001` (the host starts nothing), and is part of `all`. Every
check is a negative test: it fails when its protection is removed.

1. No secret in argv or output. Every secret the scenario makes is registered (seeds, creds files, user JWTs, join files, the operator and
   account keys, the CA and server keys, the admin and system creds). About a hundred `mash` and `mashctl` commands run with `--json`, `--verbose`
   and failure paths, including a seed, a join file, a creds file or a key fed to an option that expects something else. All output, every
   recorded argv of the run (`Runner.Argvs`) and the command line of every process in the container (`/proc/*/cmdline`) are scanned.
2. Secret files are refused over an existing file, a symlink and a dangling symlink, and left byte-identical: the creds file in the config
   folder, the join file of `endpoint create` and `reissue --out`, `mash key create` (seed, `.pub`, `--pub-out`), the key pending file,
   `mash credential create --out` (and `--force` replaces a link, never writes through it).
3. Unverified mail: absent, claimed, unknown-signer and stale mail is published with the sender's own creds (headers crafted with `sap`),
   the recipient's session, `mash inbox` and `mash read` are checked, a signed control message is delivered, and `mashctl endpoint inbox`
   shows each stored message with its trust label.
4. Plain text: a plain `nats://` URL to a non-loopback address with no override is refused before connecting.
5. A deleted endpoint's credential is accepted before the delete and refused after it on the one server. The all-three-servers form is
   AT-005's `refusedOn` / `acceptedOn` on the deleted review key.

A check whose protection is not built is **pending**: the table line says `PEND`, the record says `pending`, it is not counted as a pass,
and the result reads `PASS, with N check(s) pending`. `at4Pending` in `at004.go` lists them with a reason; a listed check that starts to
pass fails the run until its entry is removed, so the list only shrinks. Pending today: the `MASH_DANGEROUSLY_SKIP_NATS_ENCRYPTION`
behaviour (core's R1-2), the refusal of unverified mail by `mash serve`, `mash inbox` and `mash read`, and the `mash join` checks.

## AT-005: three machines, TLS, a cluster, two mashctl machines

```
go run ./scripts/liveat at005 --hosts podman --run-by manual --host-label dev-box --record-out at005-record.md
go run ./scripts/liveat at005 --hosts local      # refused unless run inside a container: the setup script runs only inside a node container
go run ./scripts/liveat at005-cleanup --run RUNID
```

### Running AT-005 by hand

AT-005 is run by hand on a developer machine with rootless podman:

```
go run ./scripts/liveat at005 --hosts podman --run-by manual --host-label dev-box --record-out at005-record.md
```

- `--run-by manual|ci` (required with `--hosts podman`): who ran it. Anything else is a usage error (exit 2).
- `--host-label TEXT` (required with `--hosts podman`): a short label for the machine the containers ran on, such as a
  role name. Letters, digits, dashes and dots only, at most 40 characters; anything else is a usage error (exit 2). The
  label is not looked up: the harness never reads the machine's real host name into the record.
- `--record-out FILE` (optional): also writes the record to FILE (mode 0600; refuses to overwrite an existing file).
  The record is always printed to stdout as well.

The record's environment holds the line `run: manual, host dev-box, substrate podman <version>` (the version is `unknown`
if `podman version` fails). Attach the record file (the path printed at the end as `record file: ...`) to the contract's
evidence. A `--hosts local` record says `run: local loopback (dry setup or developer check; not a record for the
contract)` and its result line says `local substrate: not AT-005 evidence`; `--run-by` and `--host-label` are refused with
`--hosts local`.

The second-owner case: an endpoint of a second owner (`bob/dev/x`, key and credentials made on M3) runs `nodeinit forge` inside M3, which publishes a forged discovery answer and a replayed presence answer into the reply subject of alice's `mash who` on M1; the record must show that the servers refused both and that her offline endpoint `alice/dev/ghost` is not listed as online.

AT-005 needs **rootless podman** and a Go toolchain: the harness builds `nats-server` itself from the version `go.mod`
pins (`CGO_ENABLED=0 go build`, static) and refuses to run if the built binary reports another version; the caller's PATH is not consulted. With no podman the run **fails** (exit 4) with "podman is not installed: AT-005 needs rootless
podman (the CI job provides it); this run did not test anything". It never skips and never passes without podman, and it
fails if `podman info` does not report a rootless install. No test runs on a
production host: nothing is installed, no account or key is made, no directory is made outside the run directory.

What `--hosts podman` makes (all of it labelled `mash-at005=<run id>`, all of it removed when the run ends, pass or fail):

- an image `mash-at005:<run id>`, built from the checkout on `docker.io/library/debian:bookworm-slim` (pulled by tag; the
  digest goes in the run record) with bash, openssl, iproute2, coreutils, grep, sed and gawk, the setup script, `mash`,
  `mashctl`, `nodeinit` (`scripts/liveat/nodeinit`), the `nats-server` built from the `go.mod` pin (static) and the pinned
  `nats` command, in `/usr/local/bin`. The nodes run as the non-root user `mash` (uid 1000). The Containerfile is
  `scripts/liveat/Containerfile.at005`. M1 runs the setup script inside itself (`bash /usr/local/bin/mash-local-nats-setup.sh
  --dir /node/setup --owner alice`, then `--stop`) and the harness copies what it makes to M2 and M3 with `podman cp`;
  nothing is run on the host;

- a network `mash-at005-<run id>` on `10.89.<r>.0/24` (or `<A.B>.<r>.0/24` with `--subnet-base A.B`, a private /16 such as `10.x`, `172.16-31.x` or `192.168`), `r` random and overlapping no podman network, no host route (`ip -4 route show`) and no host interface address;
- three containers `mash-at005-<run id>-m1` to `-m3` at `10.89.<r>.11` to `.13`. Each has its own filesystem
  (HOME is `/node`), no mounted host directory and no published port. The NATS ports 14222-14224 (clients),
  16222-16224 (routes) and 18222 (the monitor of M1) are inside each node's own network namespace, and the certificates
  carry the node address as an IP SAN.

The base image stays in podman's storage after the run (it is not the run's, and another run may use it); the run's own image,
containers and network are removed. Files go in and out of a node with `podman cp` through a temporary file in the run directory that is removed by its exact
path right after, so a seed or a JWT is never on a command line. Long-running processes (`nats-server`, `mash serve`) are
`podman exec -i` client processes of the harness; the harness records each PID and stops it by that PID. Ending the run
removes the containers, so nothing inside outlives it. No process is found or killed by pattern.

The clock-skew case is not part of AT-005 (containers share the host's clock); an in-process test covers it.

### Cleanup

The harness removes what it made at the end of every run and on an interrupt. It then checks that nothing labelled `mash-at005=<its run id>` remains (another run's resources are neither removed nor checked), and a cleanup failure fails the run (the record shows the check and the exit status is 1). `liveat at005-cleanup --run RUNID` removes the leftovers of one run. For a job that was killed outright, run
the always-run step with the job's own run id (printed on the first line of the run, and the value of the label)

```
go run ./scripts/liveat at005-cleanup --run RUNID
```

It removes only the containers, networks and images labelled `mash-at005=RUNID`, found with
`podman ps -a`, `podman network ls` and `podman images` filtered by that exact label, prints each thing it removed, then
verifies none of that run's remain (exit 1 if one does). It never selects by the bare key, so a live run of another
job is untouched. `RUNID` must have the harness's run-id shape (`20261117T010203Z-0a1b2c3d`); anything else (`a=b`, spaces,
patterns) is a usage error (exit 2). With no `--run` it only lists what carries the label key, any run, grouped by run id,
removes nothing and exits 0; read the ids from that list, check the run is dead, and pass one with `--run`.

### Not checked without podman

The unit tests (`go test ./scripts/liveat`) cover the command lines through a recording runner; the real podman
behaviour (the build, `podman cp` into and out of a scratch container, `podman exec` forwarding SIGTERM, rootless
networking) is first exercised by the CI job.
