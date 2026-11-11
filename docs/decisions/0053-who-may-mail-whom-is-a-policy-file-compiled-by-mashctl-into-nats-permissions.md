---
status: accepted; amended by ADR-0091 (the zone is removed)
date: 2026-11-02
decision-makers: Randy Bias (mash maintainer)
---

# Who may mail whom is a policy file of labels and rules; one label, the zone, lives in the mail subject, and mashctl compiles the rules into each zone's NATS permissions

## Context and Problem Statement

Every endpoint's publish allow-list already decides who it may mail: the design spec calls the permission set "the
whole security model" (section 8), and its one mail line, `agents.mail.*.<owner>.*`, lets every endpoint of an owner
mail every other. `policy.md` is free text appended to the session's instructions and governs nothing a client cannot
ignore. The directory's labels (role, host, up to eight `key=value`) are shown by `who --long` and read by nothing
else. mash needs a way to restrict the many-to-many mesh that a client cannot talk around, that a solo developer never
has to think about, that an IT department can slice by labels, and that scales to thousands of endpoints. Spelling out
recipient names in a flat subject space is quadratic and was measured to exhaust the account JWT at 87 endpoints; only
structure in the subject makes a rule's size independent of the number of endpoints.

## Decision Drivers

* Enforcement at the server, nothing of ours in the message path, no component at connect time.
* Default open for one person; deny by default possible for an organisation.
* Policy in and out as structured data, testable before it applies, explainable after.
* Labels are claims by the key holder, never set by an endpoint about itself.
* A denied sender learns at once that it was refused and why.
* Size independent of the number of endpoints.

## Considered Options

* Keep `policy.md` as advice to the model.
* A router or relay that evaluates policy per message.
* An auth callout service deciding permissions at connect time.
* A policy file compiled into literal per-recipient permissions (quadratic; measured dead at 87 endpoints).
* A policy file with one label in the mail subject, compiled into wildcard permissions per zone plus tag exceptions.

## Decision Outcome

Chosen option: the policy file with a zone in the subject. The file is YAML with `default: allow|deny`, `subject_label`
(the label carried in the subject; `zone` by default), `endpoints` (each with `labels`), `rules` (each with a `name`,
an `action`, a `from` selector and a `to` selector, optional `log`) and `tests`. A selector is a map of `key=value`
pairs that must all match; `{}` is every endpoint. Rules are a union; any matching deny beats any allow; the default
applies only when no rule matched; order in the file means nothing. One rule covers every subject an endpoint may
address to a peer: mail, live prompt and status probe.

The mail subject becomes `agents.mail.cc.<owner>.<zone>.<name>`; an endpoint with no zone label is in zone `default`.
The zone is a single token. The inbox stream's filter becomes `agents.mail.*.<owner>.<zone>.<name>`; senders resolve a
recipient's subject through the directory, as they already do, so the extra token is invisible to them. The prompt and
status subjects, and whether they follow the same layout, are not decided here and stay as they are until the
maintainer rules; the heartbeat keeps SAP's five tokens.

`mashctl` compiles the file: a rule whose selectors are zones becomes one wildcard line per allowed zone
(`agents.mail.*.<owner>.<zoneB>.*`) on the sending zone's scoped key, beside the fixed per-endpoint lines as
`{{name()}}` templates ([ADR 0052](0052-mashctl-mints-nats-users-and-holds-the-account-signing-key.md)); a rule on any
other label or on a named endpoint is an exception, compiled to `allow:<name>` tags on the endpoints it touches and
expanded by a `{{tag(allow)}}` template; a sender allowed nothing gets an explicit deny. The file is the source of
truth: the directory's labels and the account's keys are compiled outputs, and `mashctl drift` reports where they
disagree. `apply` provisions what is new, pushes the account, verifies the resolver's answer and names anything partial;
it is not a transaction across NATS claims, JetStream resources and local files, and says so. `compile` measures every
JWT and refuses one that will not push or connect.

`mash send` listens for the server's asynchronous permissions-violation error and fails at once with "refused by
policy: you may not mail <name>" instead of waiting for the acknowledgement timeout (measured 2026-11-01: 0.1 to
0.4 ms against a 5 s timeout), when that error matches an active mail publish the compiled policy denies; on any
ambiguity the existing "delivery unknown" timeout applies.

The router and the callout are rejected because the maintainer limits mash to what NATS does by itself: layer-3/4
firewalling on subjects. Literal lists are rejected by measurement. `policy.md` stays only as advice about what a model
should decline; it is not policy.

### Consequences

* Good: a client cannot ignore the policy; the default file compiles to one wildcard line and changes nothing for a
  solo developer; an IT file can be checked, tested and applied with a dry run, and read back as JSON; size does not
  grow with endpoints (21 KB at 20 zones, measured).
* Good: labels already in the directory become meaningful without a new store.
* Bad: the mail subject layout changes; SAP tools expecting five tokens would not find mash's mail subjects. mash
  resolves subjects through its directory, and the heartbeat keeps SAP's shape.
* Bad: only the zone compiles to wildcards; rules on other labels are exceptions carried as tags, and an endpoint with
  many exceptions grows its own JWT (about 170 tags fit the default connect line). `check` reports the count. Tags are
  lower-cased, so names used in exceptions must be lower case.
* Bad: a valid file can still be wrong: a misspelled label matches nothing and falls to the default. `check` rejects
  unknown fields and duplicate keys, warns on rules that match no endpoint, and `apply` shows the changed
  sender-to-recipient matrix; a restrictive apply needs tests across each changed boundary.
* Relay by asking (A mails B "forward this to C") is not addressed by this model and cannot be at this layer; it is
  recorded and instrumented, not refused (separate record, later).
* `mash admin set-meta` is removed once the file is the source of labels: labels are edited in the file.
* Not yet true in code: all of it. It lands with the later changes of the `mashctl` plan.

### Confirmation

* The policy package's tests and golden files confirm the semantics: union, deny beats allow, default only when
  nothing matched, explicit deny for a sender allowed nobody, zone wildcards, and exceptions as tags.
* `mashctl` apply, diff, drift and repair tests run against an in-process NATS server in operator mode and confirm a
  rule change reaches a connected endpoint without new credentials, including during a consume, and that a zone-A user
  can mail only the zones its rules allow.
* A `mash send` test confirms the fast refusal and the unchanged timeout path.

## Pros and Cons of the Options

### Keep `policy.md` as advice

* Good, because there is nothing to build.
* Bad, because a client can ignore it.

### A router or relay

* Good, because it can evaluate anything per message.
* Bad, because it puts a component of ours in the message path and reads payloads.

### An auth callout

* Good, because permissions could be decided per connection.
* Bad, because it is a service that must run at connect time and that NATS alone does not need.

### A policy file compiled into literal per-recipient permissions

* Good, because the subject layout stays as it is.
* Bad, because the size is quadratic and the account JWT cannot be pushed past 86 endpoints.

### A policy file with a zone in the subject

* Good, because the server enforces it, nothing of ours is in the path, and size is independent of endpoints.
* Bad, because the zone becomes part of the address, exceptions are tags, and relay by asking is outside it.

## More Information

* Sources: the maintainer's rulings (2026-11-01 and 2026-11-02); the research document "Who Can Talk to Whom on Mash"
  and its decision table; the refusal spike and the scale spike (nats-server 2.15.x, nats.go 1.55.0, jwt/v2 2.8.2);
  prior art read for the file shape (Kubernetes selectors, Tailscale rules and tests, nftables and pf check-then-apply,
  Cedar forbid-over-permit).
