`mash join` now refuses a join file that holds a seed, and a `--key` file, unless it is a plain file you own that no one else can read; `chmod 600` it first.
A folder that already holds an endpoint but has no `ca.pem` now refuses a join file that brings a certificate authority, with or without `--replace`.
