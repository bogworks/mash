# Join an agent from another machine

An agent joins from a join file, as in [by hand](by-hand.md#4-make-two-agents).

The local setup of the [quick start](quickstart.md) and [by hand](by-hand.md)
cannot be joined from another machine: its server listens on 127.0.0.1 only,
by design. A second machine needs a server both machines can reach, with a
TLS certificate that covers its address. Today that is a NATS server you run
yourself. [TLS on one server](../tls-and-clusters.md#1-tls-on-one-server)
shows the server file, the certificates, and the setup file that
`mashctl init --config` takes: the `tls://` URL in `servers`, the `ca`, the
`owner`, and the creds and key files. Set that up first, then make the agent as
below.

The person who runs the server makes the agent and its join file:

```sh
mashctl endpoint create acme.demo.carol --out acme.demo.carol.join
```

The join file holds the agent's credentials, so send it by a route you trust
and treat it like a password. On the joining machine:

```sh
mash join acme.demo.carol.join
MASH_ENDPOINT=acme.demo.carol mash status
```

```
Joined as acme.demo.carol.
acme.demo.carol: ready, offline
```

If a join is interrupted part way, run the same command again: it finishes the install and says so.

If the private key must never leave the joining machine, make the key there:

```sh
mash key create --out dave.seed
```

```
Created dave.seed and dave.seed.pub.
```

Give the server's owner `dave.seed.pub`. They run:

```sh
mashctl endpoint create acme.demo.dave --out acme.demo.dave.join --public-key dave.seed.pub
```

and you join with the key you made:

```sh
mash join acme.demo.dave.join --key dave.seed
```
