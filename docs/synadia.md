# Synadia Cloud

`mashctl` in mash 0.5.7 does not support Synadia Cloud, or any hosted NATS where you do not hold the operator key. The issue is [#876](https://github.com/bogworks/mash/issues/876).

## What works today

The `mash` program, the one a session runs, connects over TLS to any conforming NATS server it has credentials for. It needs a creds file for a user with the permissions `mashctl endpoint permissions` prints, and the account must already hold mash's storage and the endpoint's directory record and inbox, which `mashctl` creates. On an account `mashctl` has not set up, `mash` fails with "endpoint ... is not provisioned".

## What is missing, and why

`mashctl` needs three kinds of authority, and a hosted service holds one of them for you:

- **Making an endpoint's user** needs an account signing key. Since 0.5.7 that is the optional `signing_key` setup key (see [Bring your own NATS server](nats-server.md#requirements)), so signing users is no longer what blocks a hosted service.
- **Revoking a deleted or reissued endpoint's key** needs the account JWT signed again with the operator key and pushed to the servers through the system account (`operator_key` and `system_creds` in the setup file). On a hosted service the provider holds the operator key, so you cannot give it to `mashctl`.
- **Setting up the account's storage** (the directory, lease, retired-name and issue-record buckets) needs admin creds for a user in the account (`admin_creds`).

So the gap after 0.5.7 is revocation and account set-up on a hosted service, not user signing. `mashctl init` takes no hosted-service option, and an earlier release's quickstart script for Synadia Cloud is gone.

## What to use instead

Run your own NATS server that meets the [requirements](nats-server.md#requirements), and set it up with `mashctl init --config`. For one machine, the release's `mash-local-nats-setup.sh` makes such a server (see the [setup script reference](reference/setup.md)); [by hand](guide/by-hand.md) uses it. For a server that several machines reach, see [Operating mash](operations.md) and [TLS, clusters and a second mashctl machine](tls-and-clusters.md).

## Plans

Support in `mashctl` for a hosted service ([#876](https://github.com/bogworks/mash/issues/876)) and the guides for a local server and for Synadia Cloud ([#1037](https://github.com/bogworks/mash/issues/1037)) are both 0.6.0 work, and the Synadia Cloud guide depends on the support. Neither has a design yet, and nothing here promises it.
