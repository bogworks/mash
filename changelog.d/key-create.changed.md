`mash key create` now follows the specification. `--out` defaults to `mash.seed` in the current folder, and the public key goes
beside the seed with `.pub` added to its whole name: `bob.seed` and `bob.seed.pub` (it used to replace `.seed` with `.pub`). It
prints `Created bob.seed and bob.seed.pub.`, or `error: bob.seed already exists; nothing was written` (exit 1). `--json` prints
`{"seed": "bob.seed", "public_key": "bob.seed.pub"}`, the two paths. `--pub-out` and `--dry-run` are gone.
