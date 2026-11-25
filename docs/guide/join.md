# Join an agent from another machine

An agent joins from a join file, as in [by hand](by-hand.md#4-make-two-agents).
The server made there is reachable from one machine only. A second machine
needs a server both can reach, with a certificate that covers its address: in 0.5.9
that is a NATS server you run yourself, which `mashctl init` takes from a
setup file ([operations](../operations.md#setting-mash-up-with-mashctl-init---config)).

The person who runs the server makes the agent and its join file:

```sh
mashctl endpoint create acme.demo.carol --out acme.demo.carol.join
```

```
Created endpoint acme.demo.carol
Join file: acme.demo.carol.join
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

```
Joined as acme.demo.carol (finished an earlier join)
```

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
