# liveat: the live acceptance tests

`go run ./scripts/liveat at001|at002|at003|all|selfcheck --port P` runs AT-001 to AT-003 of the 0.5.5 endpoint-model
contract against a build of this checkout, on one local NATS server, in scratch homes.

## AT-005: three machines, TLS, a cluster, two mashctl machines

```
go run ./scripts/liveat at005 --hosts podman --run-by manual --host-label dev-box --record-out at005-record.md
go run ./scripts/liveat at005 --hosts local      # the harness's own selfcheck on a developer machine (the default)
go run ./scripts/liveat at005 --hosts local --dry-setup
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

- an image `mash-at005:<run id>`, built from the checkout: `mash`, `mashctl`, `nodeinit` (`scripts/liveat/nodeinit`) and
  the `nats-server` built from the `go.mod` pin, as static binaries in `/bin`, on `FROM scratch`. Nothing is pulled. The Containerfile is
  `scripts/liveat/Containerfile.at005`:

  ```
  FROM scratch
  COPY mash mashctl nats-server nodeinit /bin/
  ENTRYPOINT []
  ```

- a network `mash-at005-<run id>` on `10.89.<r>.0/24` (or `<A.B>.<r>.0/24` with `--subnet-base A.B`, a private /16 such as `10.x`, `172.16-31.x` or `192.168`), `r` random and overlapping no podman network, no host route (`ip -4 route show`) and no host interface address;
- three containers `mash-at005-<run id>-m1` to `-m3` at `10.89.<r>.11` to `.13`. Each has its own filesystem
  (HOME is `/node`), no mounted host directory and no published port. The NATS ports 14222-14224 (clients),
  16222-16224 (routes) and 18222 (the monitor of M1) are inside each node's own network namespace, and the certificates
  carry the node address as an IP SAN.

Files go in and out of a node with `podman cp` through a temporary file in the run directory that is removed by its exact
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
