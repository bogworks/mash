An account can now be marked as owned by an outside system, which hands in its list sets: `mashctl policy adopt --submitter KEY`
registers the one key allowed to submit them in a new `mash-adoption` bucket (made like the issue record's: only the admin connection
writes it, history 64, no delete or purge, no endpoint permission), a second run with a different key is the recovery rotation, and
`mashctl policy adopt --show` prints the marker. The marker also records, by public key, the admin user, the system user (adopt refuses without the system credentials) and each `--exempt KEY`: the users the later reconcile never touches. On an adopted account `policy apply`, `policy repair`, `add`, `reissue`, `label`,
`unlabel`, `retire`, `set-meta --label`, `set-zone` and `provision` with `--label` or `--zone` refuse before they change anything, and refuse too when the marker cannot be read;
read-only commands and `deprovision` still work. `policy apply` and `policy repair` now also need the admin credentials, to read the marker.
Adopting a real account is a live-account step, taken only on an explicit go from the maintainers. This version has no list-set intake and no un-adopt command, so an adopted account's policy cannot be changed yet; running one account both
ways is not supported
([ADR 0111](docs/decisions/0111-an-adopted-account-takes-its-list-sets-only-from-its-submitter-and-mashctl-refuses-every-other-policy-change.md)).
