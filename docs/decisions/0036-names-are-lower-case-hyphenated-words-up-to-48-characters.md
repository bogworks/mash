---
status: accepted
date: 2026-10-21
decision-makers: Randy Bias (mash maintainer)
recorded: 2026-10-31 (backfilled)
---

# Names and owners are lower-case words joined by single hyphens, 1-48 characters

## Context and Problem Statement

An endpoint's name and its owner's name appear in many places: as tokens in SAP subjects
(`agents.<verb>.cc.<owner>.<name>`, ADR-0005), inside JetStream stream names (the inbox stream `inbox-<name>`,
ADR-0009), as keys in the directory KV bucket (NATS's key-value store, ADR-0011), as NATS user names, in
file paths for creds and spools, and in the text a session reads. mash needed one name rule that is safe in all of
them, and a rule for how a sender's name is shown to a recipient.

## Decision Drivers

* A name must be a valid NATS subject token and stream-name part, and safe as a file name.
* A name must be readable by people and models in rendered mail.
* One rule for endpoint and owner names, checked in one place.

## Considered Options (reconstructed 2026-10-31; not recorded at the time)

* Any valid NATS subject token
* Lower-case letters and digits only
* Lower-case words joined by single hyphens, starting with a letter, with a length cap

## Decision Outcome

Chosen option: "lower-case words joined by single hyphens, 1-48 characters". No reason was recorded for the hyphen
rule or the length. The rule keeps a name safe in a subject, a stream name and a file path (reconstructed). The value
48 was chosen without a recorded reason.

As built (mash layer):

* `config.ValidName`: `^[a-z][a-z0-9]*(-[a-z0-9]+)*$` and 1-48 characters, for `MASH_NAME`, the owner, and the admin
  commands that provision and remove endpoints (`internal/config/config.go`, `internal/admin/admin.go`).
  `mash init synadia` turns a default name such as `<user>-<host>` into a valid one and caps it at 48.
* Display (`render.Sender`, as changed by PR #170 on 2026-10-31): a sender is shown by its endpoint name as is. A
  sender of another owner is shown as `owner/name`. A sender not in the directory is shown by name alone. A hyphen
  inside a name has no meaning to mash and is never split.

### Consequences

* Good, because a valid name can be used unescaped in every subject, stream, key, user name and path mash builds.
* Good, because the display rule is the same for every deployment; a name means what it says.
* Bad, because names cannot use upper case, underscores, dots or non-ASCII letters.
* Neutral, because group structure that a deployment wants to record goes in directory labels (PR #170), not in
  the name's shape.

### Confirmation

* `internal/config/config_test.go`: `TestRejectsBadNames`, `TestInertWithoutName`.
* `internal/render/render_test.go`: `TestSenderRules` (including "hyphenated names are never split").
* `internal/initsynadia/names_test.go`: `TestSanitize`, `TestSanitizeLongNameIsCappedAndValid`,
  `TestDefaultEndpointName`.
* `cmd/mash/init_test.go`: `TestInitSynadiaRejectsBadNames`.

## Pros and Cons of the Options

### Any valid subject token

* Good, because it is the least restrictive.
* Bad, because tokens may contain characters that are awkward or unsafe in stream names, file paths and text.

### Letters and digits only

* Good, because it is the simplest rule.
* Bad, because multi-word names become hard to read.

### Hyphen-joined lower-case words, capped

* Good, because names are readable and safe everywhere mash uses them.
* Bad, because the cap and character set are stricter than NATS requires.

## More Information

* Design spec §2 ("Terms": name); `docs/plans/2026-10-22-mash-v0.1.md` (constraints).
* PR #170 (2026-10-31: generic directory labels; sender shown by endpoint name), with a `render-contract:` commit
  (ADR-0034).
* Supersession: from PR #17 (2026-10-28) until PR #170, the display treated a two-word name as `group-role`: when
  sender and receiver of the same owner shared the first word, only the second word was shown (`review` for
  `acme-review`), following one consumer's naming convention. PR #170 reversed that; this
  ADR records the rule after PR #170.
* Related: ADR-0005 (subjects), ADR-0006 (one account; names are account-wide), ADR-0009 (inbox streams), ADR-0011
  (the directory), ADR-0034 (text contract).
