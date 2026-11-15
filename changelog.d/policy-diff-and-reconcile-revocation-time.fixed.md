`mashctl policy diff` now gives the intake's verdict in four more cases.

- It checks a `next_submitter` against the same held keys the intake uses: the key store's keys, the owner groups' keys and the admin user's key.
- It refuses, at stage `adoption`, an adoption marker that names another account.
- During a pending rotation it reads the signer from the JWS, so an unchanged list set signed by the pending key is not refused as `unchanged`.
- Its JSON plan has a `rotation` object.

`mashctl policy reconcile --yes` now stamps each revocation at the time the plan gives it, so a connected credential with a future issue time no longer makes it refuse.
