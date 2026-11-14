The release gate's policy tests now include live tests for a deny-all sender (AT-005), for the owner line dropped and a
deny holding with it (the second half of AT-003), and for the open reply and plumbing subjects a raw client reaches
(AT-020, pinned to a table in the [threat model](docs/security/threat-model.md)); the `policy-tests` step requires them.
