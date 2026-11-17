# liveat: the live acceptance tests

`go run ./scripts/liveat at001|at002|at003|all|selfcheck --port P` runs AT-001 to AT-003 of the 0.5.5 endpoint-model
contract against a build of this checkout, on one local NATS server, in scratch homes.

## AT-005: three machines, TLS, a cluster, two mashctl machines

```
go run ./scripts/liveat at005 --hosts podman     # the acceptance run (CI job)
go run ./scripts/liveat at005 --hosts local      # the harness's own selfcheck on a developer machine (the default)
go run ./scripts/liveat at005 --hosts local --dry-setup
go run ./scripts/liveat at005-cleanup --label mash-at005
```

AT-005 needs **rootless podman** and a `nats-server` on PATH that is a static binary (a release build or
`CGO_ENABLED=0 go build`). With no podman the run **fails** (exit 4) with "podman is not installed: AT-005 needs rootless
podman (the CI job provides it); this run did not test anything". It never skips and never passes without podman, and it
fails if `podman info` does not report a rootless install or if `nats-server` is dynamically linked. No test runs on a
production host: nothing is installed, no account or key is made, no directory is made outside the run directory.

What `--hosts podman` makes (all of it labelled `mash-at005=<run id>`, all of it removed when the run ends, pass or fail):

- an image `mash-at005:<run id>`, built from the checkout: `mash`, `mashctl`, `nodeinit` (`scripts/liveat/nodeinit`) and
  the `nats-server` from PATH, as static binaries in `/bin`, on `FROM scratch`. Nothing is pulled. The Containerfile is
  `scripts/liveat/Containerfile.at005`:

  ```
  FROM scratch
  COPY mash mashctl nats-server nodeinit /bin/
  ENTRYPOINT []
  ```

- a network `mash-at005-<run id>` on `10.89.<r>.0/24`, `r` random and not used by any existing podman network;
- three containers `mash-at005-<run id>-m1` to `-m3` at `10.89.<r>.11` to `.13`. Each has its own filesystem
  (HOME is `/home/node`), no mounted host directory and no published port. The NATS ports 14222-14224 (clients),
  16222-16224 (routes) and 18222 (the monitor of M1) are inside each node's own network namespace, and the certificates
  carry the node address as an IP SAN.

Files go in and out of a node with `podman cp` through a temporary file in the run directory that is removed by its exact
path right after, so a seed or a JWT is never on a command line. Long-running processes (`nats-server`, `mash serve`) are
`podman exec -i` client processes of the harness; the harness records each PID and stops it by that PID. Ending the run
removes the containers, so nothing inside outlives it. No process is found or killed by pattern.

The clock-skew case is not part of AT-005 (containers share the host's clock); an in-process test covers it.

### Cleanup

The harness removes what it made at the end of every run and on an interrupt. For a job that was killed outright, run
the always-run step

```
go run ./scripts/liveat at005-cleanup --label mash-at005
```

It removes the containers, networks and images carrying the label key `mash-at005` (any run id), found with
`podman ps -a`, `podman network ls` and `podman images` filtered by that label, and prints each thing it removed. It
touches nothing without the label.

### Not checked without podman

The unit tests (`go test ./scripts/liveat`) cover the command lines through a recording runner; the real podman
behaviour (the build, `podman cp` into and out of a scratch container, `podman exec` forwarding SIGTERM, rootless
networking) is first exercised by the CI job.
