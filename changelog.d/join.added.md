`mash join FILE` installs an endpoint on this machine from the join file whoever runs your mesh gave you (`mashctl endpoint create
acme.demo.bob --out acme.demo.bob.join`). It copies the server addresses and the certificate authority's certificate into the
configuration folder and writes the endpoint's creds file there (mode 0600), then prints `Joined as acme.demo.bob.`, and
`mash status` works. When the endpoint made its own key, the join file holds no seed and `--key bob.seed` supplies it. An endpoint
already installed is refused unless you pass `--replace`, which replaces that endpoint's creds and nothing else. A join file for a
different mesh, or one with a different certificate authority (or none, where the folder has one), is refused with or without
`--replace`, because one configuration folder serves one mesh and trusts one certificate authority (`--config-dir` names another).
Two joins into one folder run one after the other. `--dry-run` says what would happen and writes nothing. It writes nothing
unless it can write everything, and it never prints a seed or a token.
