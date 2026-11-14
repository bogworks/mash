The release gate has a named `policy-tests` step, also run alone as `scripts/release-check --policy-tests`, and a
`policy-tests` CI job runs it when the NATS server version in `go.mod` changes and on every push to `main`: the listed
policy tests (compile, live and template-change) must start and pass, or the release is refused
([docs/release.md](docs/release.md#policy-tests-on-every-release)).
