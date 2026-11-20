`mash join FILE` installs an endpoint on this machine from the join file whoever runs your mesh gave you (`mashctl endpoint create
acme.demo.bob --out acme.demo.bob.join`). It copies the server addresses and the certificate authority's certificate into the
configuration folder and writes the endpoint's creds file there (mode 0600), then prints `Joined as acme.demo.bob.`, and
`mash status` works. When the endpoint made its own key, the join file holds no seed and `--key bob.seed` supplies it. An endpoint
already installed is refused unless you pass `--replace`; a join file for a different mesh is refused, because one configuration
folder serves one mesh (`--config-dir` names another). `--dry-run` says what would happen and writes nothing. It writes nothing
unless it can write everything, and it never prints a seed or a token.
