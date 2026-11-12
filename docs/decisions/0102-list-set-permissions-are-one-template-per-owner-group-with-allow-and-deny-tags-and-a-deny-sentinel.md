---
status: accepted
date: 2026-11-12
decision-makers: Randy Bias (mash maintainer); the choices were delegated to the implementers by contract mash-messaging-policy draft-2 (D206), section 7
---

# List-set permissions are one template per owner group with allow and deny tags and a deny sentinel

## Context and Problem Statement

Contract mash-messaging-policy draft-2 (ratified in D206) has mash turn an exact list set into permissions that the NATS
server enforces: one scoped signing key per owner group carries a permission template, and each sender's exceptions are
tags in its user JWT. The contract fixes what must hold (REQ-004 to REQ-006, AT-004) and delegates the tag names, the
deny sentinel's value and the template layout (section 7). It leaves two further points open: what to do with a deny
entry that can only be a mistake, and how the compiler proves the server will enforce what it emitted. The list set's file
format is not decided (DEC-004), so the code, in `internal/listset`, takes the shape a decoded list set would have.

## Decision Drivers

* Fail closed: every template needs an allow line that needs no tag, and a deny tag must exist wherever a deny line does
  (nats-server 2.15.1, verified: allow lines whose tags are all missing leave a deny of everything; a deny line whose tag
  is missing stops the user connecting; one tag expands in every line that uses it).
* One tag per exception, covering mail, prompt, status and heartbeat, and tags as short as the naming grammar allows
  (REQ-008's size limit).
* No tag may be able to name an address outside the account's range (INV-005).
* Check what the server will do, not what the template looks like (REQ-006).

## Considered Options

* One template per owner group; `allow:<owner>.<name>` and `deny:<owner>.<name>` tags with the range as a literal in the
  template; sentinel `deny:_none` (chosen).
* Tag values carrying the whole three-part address, so a tag could name another range.
* One template per sender, with no tags.
* A sentinel `deny:none`.

## Decision Outcome

Chosen option: the first, with these rules.

* **Key and template.** One scoped key per owner group (`range.owner`). The template holds, per verb, the owner line
  (`agents.<verb>.<range>.<owner>.*`) only where the group's switch is shared; a tag line
  `agents.<verb>.<range>.{{tag(allow)}}` for mail, prompt and status (publish) and `agents.hb.<range>.{{tag(allow)}}`
  for heartbeat (subscribe); and the fixed lines, which need no tag: the sender's own heartbeat, reply and plumbing
  subjects, its own prompt and status subjects, its own inbox stream and consumer, and the directory and its lease. The
  fixed lines name the sender through `{{name()}}` and the key's range and owner as literals; the user JWT's name claim
  is the name part of the address, and the owner group comes from the key, never from a tag.
* **Tags.** An allow entry is one tag `allow:<owner>.<name>`, a deny entry one tag `deny:<owner>.<name>`, lower case.
  The range sits in the template, so a tag cannot leave the range and is as short as the grammar allows. A closed verb
  with an empty allow list gets no tag and no sentinel.
* **Deny lines and the sentinel.** When any sender of a group has a deny entry, the template gains deny lines
  `agents.<verb>.<range>.{{tag(deny)}}` (and `agents.hb.<range>.{{tag(deny)}}`) for the shared verbs only, and every user
  of that key carries a deny tag, real or the sentinel `deny:_none`. The sentinel expands to a four-token subject, never
  the five tokens of an address, and `_none` is no part the naming grammar admits, so it addresses nothing.
* **Refused deny entries.** A deny entry is refused when its owner group has no shared verb, when its target is outside
  the sender's own owner group (only the owner line is shared, so nothing else can be taken away), and when the same
  address is on the allow list. Such an entry can only be a mistake. Duplicates in one list are refused as well.
* **Check expands tags as the server does** (allow lines with a missing tag drop out; an allow list that empties gains a
  deny of everything; a missing deny tag is a connect failure), then compares what each sender reaches, for each verb,
  over every address the list set names plus probe addresses in the same group, another owner group and another range,
  with what its lists give. It refuses a mismatch in either direction, a template with no tag-free allow line in either
  direction, any user carrying permission claims or a tag other than `allow:` and `deny:`, and any expanded line that is
  neither an address the lists could give nor one of the sender's fixed lines. Compile runs Check on its own output.

### Consequences

* Good, because a missing or removed tag can only narrow what a sender reaches, and a mistake in a template or a tag is
  caught before anything is signed.
* Good, because one tag per exception holds across verbs and stays short.
* Good, because a wildcard cannot arrive through a tag: the range is fixed in the template and Check compares the
  server's result with the lists.
* Bad, because every user under a deny key carries a tag it does not need, and a group's first deny entry changes the key.
* Bad, because the fixed lines are a copy of `internal/admin`'s unexported list, kept in step by the live test and by
  Check's exact match on non-address lines.

### Confirmation

`internal/listset` tests: AT-002 (each bad entry refused, all named in one error, no output), AT-004 (a checked-in corpus
compiled and checked, with mutations that Check must refuse) and a live test that connects users under the compiled
templates to an embedded nats-server in operator mode and requires the server's publish and subscribe outcomes to equal
Check's simulation.

## Pros and Cons of the Options

### One template per owner group, range literal, `deny:_none` (chosen)

* Good, because it meets REQ-004 to REQ-006 and the size limit, and keeps the range out of every tag.
* Bad, because a deny key needs a tag on every user.

### Whole address in the tag

* Good, because the template line is shorter.
* Bad, because tags are longer and one could name another range, which Check would then have to be the only guard against.

### One template per sender

* Good, because there are no tags.
* Bad, because it is a key per sender, against REQ-004, and a change to one sender re-signs the account.

### Sentinel `deny:none`

* Good, because it reads plainly.
* Bad, because it gains nothing over `_none`, which keeps the earlier sentinel's spelling (ADR 0056) and starts with a
  character the naming grammar never allows at the start of a part.

## More Information

Contract mash-messaging-policy draft-2 (D206), REQ-001 to REQ-006, AT-002, AT-004, section 7. Code: `internal/listset`.
The earlier zone-based templates ([ADR 0056](0056-every-zone-key-carries-both-tag-templates-and-every-user-the-deny-none-placeholder.md))
are superseded by [ADR 0091](0091-there-are-no-zones-mail-keeps-the-five-token-subject-and-the-address-is-the-fence.md);
this record replaces their layout for list sets and does not change them.
