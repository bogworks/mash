**Breaking:** `mash credential create` is removed, with no alias. `mash join` replaces it: `mashctl endpoint create ... --out
FILE.join` now writes the file `mash join` installs, so there is no JWT file to carry and no creds file to assemble by hand. For
an endpoint that made its own key, run `mash join FILE.join --key bob.seed`.
