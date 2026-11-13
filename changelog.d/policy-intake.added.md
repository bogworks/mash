The list-set intake is built, and not yet run by any command: it watches an adopted account's `listset.<account>` key in the range's
policy bucket and, for each revision, reads the adoption marker again (never cached), refuses and reports a document that is unsigned, signed by
any key but the registered submitter, malformed, of an unknown schema or for another account or range, skips the document already in force so a restart does not
turn an applied list set into a refusal, and hands a verified one to the apply stack. A planned rotation of the submitter key
(`next_submitter` in a list set) is recorded in the marker after the list set is applied and completes with the first applied list set
signed by the new key; the old key is refused from the moment it is recorded. `mashctl policy serve`, which will run the intake, comes later
([ADR 0112](docs/decisions/0112-the-list-set-intake-reads-the-adoption-marker-every-time-never-applies-a-refused-document-and-completes-a-rotation-only-after-a-successful-apply.md)).
