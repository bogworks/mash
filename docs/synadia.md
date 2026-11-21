# Synadia Cloud

`mashctl` in mash 0.5.6 does not support Synadia Cloud, or any hosted NATS where you do not hold the operator key, for provisioning and administration. The `mash` endpoint binary connects over TLS to any conforming NATS server it has credentials for. The issue is [#876](https://github.com/bogworks/mash/issues/876).

## What is not supported

`mashctl` cannot set up or manage a mesh on a hosted NATS service. An earlier release shipped a quickstart script for Synadia Cloud. It is gone, and `mashctl init` takes no hosted-service option.

## Why

`mashctl` signs each endpoint's NATS user itself, and it revokes a deleted or reissued endpoint's key by signing a new account JWT and pushing it to the server. For that it needs the operator key, the account key and the system account's credentials, all set in the file you give to `mashctl init --config` (see [Setting mash up with `mashctl init --config`](operations.md#setting-mash-up-with-mashctl-init---config)). On a hosted service the provider holds the operator key, so you cannot give it to `mashctl`.

`mash` itself, the program a session runs, only needs a creds file for a user with the permissions `mashctl endpoint permissions` prints, and it connects to any conforming server over TLS. What is missing is the way to make and revoke those users and to set up the account's storage on a hosted service.

## What to use instead

Run your own NATS server in operator mode with JetStream, and set it up with `mashctl init --config`. For one machine, the release's `mash-local-nats-setup.sh` makes such a server (see the [setup script reference](reference/setup.md)); the [walkthrough](guide/walkthrough.md) uses it. For a server that several machines reach, see [Operating mash](operations.md) and [TLS, clusters and a second mashctl machine](tls-and-clusters.md).

Guides for a local server and for Synadia Cloud are planned for 0.6.0 ([#1037](https://github.com/bogworks/mash/issues/1037)). Support in `mashctl` itself is tracked in #876.
