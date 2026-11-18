# mash 0.5.5 demo

Two mash endpoints exchange signed mail over TLS inside one throwaway container. Run every command from the repository root.

## Run it

    sh demos/v055-container/demo-0.5.5.sh up
    sh demos/v055-container/demo-0.5.5.sh send
    sh demos/v055-container/demo-0.5.5.sh down

`status` shows whether the demo container and image exist. You need podman (rootless is fine), Go, curl, and optionally gh. No root, and nothing is pulled from a registry. The first `up` downloads and builds for a minute or two; later runs reuse the files in `demos/v055-container/downloads/` and `demos/v055-container/build/`. Both are git-ignored, as are `src-*/` and `state/`. `DEMO_PORT` (default 24222) picks the server port inside the container; the script checks the host and moves to the next free port if it is busy, and says so.

## What you will see

**up**
1. Downloads the mash 0.5.5 Linux binary from the GitHub release and checks it against the release's SHA256SUMS. A mismatch stops the script. It also runs `gh attestation verify` (this proves the binary was built by the release workflow from the v0.5.5 tag); if that fails it only warns.
2. Builds mashctl, nats-server 2.15.1 and a tiny helper (nodeinit) as static binaries. mashctl is not a release asset, so it is built from source.
3. Builds an image `mash-demo-0.5.5:1` from nothing (FROM scratch: four binaries, no shell) and starts container `mash-demo-055`.
4. Inside the container, `mashctl init --local` starts a NATS server that accepts TLS only, with its own certificate authority. Then it creates two endpoints, alpha and beta, in one demo group, and lists them. Their keys are made inside the container and never printed.

**send** (every step prints a heading and the exact command)
1. `mash status` for alpha: config, credentials, and "tls: OK".
2. alpha sends "hello from alpha" to beta. beta's inbox shows it labelled `[verified]`, which means alpha's own key signed it.
3. beta replies; alpha's inbox shows the reply, also `[verified]`.
4. Refused case: a send using a plain `nats://` URL with no override. mash refuses before connecting, exits 2, and names MASH_INSECURE_PLAINTEXT. beta's inbox afterwards still holds only the one real message.

`status` for alpha also prints "channel allowlist: MISSING". That is about Claude Code sessions, not about this demo, and is expected here.

## What this proves

Two separate endpoints exchange durable, signed mail through a real NATS server over TLS, the trust label reflects a real signature check, and plain text is refused.

## Two answers

**1. Two Claude Code sessions in the container?** Not possible: the image has no shell and no node. A host-side variant is feasible but not built, if `claude` is installed on the host. It would need: the two endpoints' creds files copied out of the container (`/demo/config/<address>.creds`, for example with `podman cp`), the CA (`/demo/config/server/ca.pem`), a way for the host to reach the server (publish the port when the container is started; today the server listens on the container's own loopback), `MASH_URL`, `MASH_TLS_CA` and `MASH_CREDS` pointing at those files, and the host mash binary's `mash plugin export DIR` to install the plugin. The creds files hold secret keys, so copy them with care.

**2. macOS.** The v0.5.5 release has `mash-darwin-amd64` and `mash-darwin-arm64` (plus linux amd64/arm64, a `.cdx.json` SBOM for each, `mash.intoto.jsonl`, SHA256SUMS and quickstart-synadia.sh). On a Mac, mashctl is not an asset, so you need a Go 1.27.4+ toolchain to `go install` it, and nats-server must be installed separately.

## Clean up

`sh demos/v055-container/demo-0.5.5.sh down` removes only the container `mash-demo-055` (which stops the server) and the image `mash-demo-0.5.5:1`, by exact name. Downloads and builds stay in `demos/v055-container/` for next time; delete `downloads/`, `build/`, `src-v0.5.5/` and `state/` there to remove them.
