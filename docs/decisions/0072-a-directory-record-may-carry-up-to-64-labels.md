---
status: accepted
date: 2026-11-03
decision-makers: Randy Bias (mash maintainer)
---

# A directory record may carry up to 64 labels

## Context and Problem Statement

A directory record carried at most 8 labels ([ADR 0050](0050-the-directory-record-says-what-an-endpoint-does-with-a-description-and-capabilities.md)).
Labels are the vocabulary the policy file selects on ([ADR 0053](0053-who-may-mail-whom-is-a-policy-file-compiled-by-mashctl-into-nats-permissions.md)),
and a fleet that groups endpoints by team, project, tier, region and capability reaches 8 quickly
([#235](https://github.com/bogworks/mash/issues/235)). Is 8 the right cap?

## Decision Drivers

* The cap should stop a runaway record, not shape how an operator models a fleet.
* The record must stay far below the NATS KV value limit.
* An older build must not be broken by a record a newer build wrote.

## Considered Options

* Raise the cap to 64 (chosen).
* Keep 8.
* Remove the cap.

## Decision Outcome

`store.MaxLabels` is 64. The key grammar (`[a-z][a-z0-9_.-]{0,31}`) and the value limit (64 printable characters, no
tab or newline) are unchanged. The number of labels a record carries has nothing to do with the user JWT's tags: that
budget (3,750 bytes, [ADR 0067](0067-a-reissue-that-narrows-an-endpoint-rotates-its-user-key-and-revokes-the-old-one.md))
is spent by the policy's allow and deny tags, not by labels.

Record size: 64 labels of a 32 character key and a 64 character value are about 6 to 7 KB of JSON. The directory
bucket sets no value size limit of its own, so a value is bounded by the server's `max_payload`, which mash leaves at
the nats-server default of 1 MiB (mash sets none; the 1 MiB `store.MaxMsgSize` applies to inbox streams). The largest
record is under 1% of that. `TestMaxLabelsRoundTrip` provisions 64 maximum-length labels and reads them back through
the in-process server.

Compatibility: the cap is enforced only where labels are written (`admin.Provision` and `SetMeta`, `mashctl add` and
`label`, `mashctl policy check`), never where a record is read. An older endpoint, `mashctl who` or any reader of a
record with more than 8 labels still reads it. An older `mashctl` or admin build that edits such an endpoint's
labels (`label`, `unlabel`, `set-meta`, `add`) refuses the whole set until it is upgraded, as the set is over its cap.
In a fleet with mixed versions, upgrade `mashctl` before any admin writes more than 8 labels.

### Consequences

* Good, because labels can express a real fleet's vocabulary.
* Good, because readers were never capped, so no migration is needed.
* Bad, because an older `mashctl` cannot edit labels on an endpoint that has more than 8 until it is upgraded.
* Bad, because a policy file may now carry larger label sets, so the compiled permissions and a reissue are checked
  against the user JWT budget as before, and an endpoint can reach it sooner.

### Confirmation

`TestValidLabelsBoundary` and `TestParseRecordAcceptsManyLabels` (`internal/store/store_test.go`: 64 valid, 65
refused with `mash: 65 labels; at most 64`; a stored record with 20 labels parses), `TestMaxLabelsRoundTrip` and
`TestSetMetaLabels` (`internal/admin/meta_test.go`) and `TestPolicyLabelCapBoundary` (`internal/policy/policy_test.go`).

## Pros and Cons of the Options

### Raise the cap to 64 (chosen)

* Good, because it fits real fleets and stays tiny against the KV value limit.
* Bad, because older builds refuse to edit a record above their cap.

### Keep 8

* Good, because nothing changes.
* Bad, because operators run out of labels for the policy vocabulary.

### Remove the cap

* Good, because there is no number to choose.
* Bad, because a record then has no bound but `max_payload`, and every reader pays for a runaway one.

## More Information

* Issue [#235](https://github.com/bogworks/mash/issues/235).
* Code: `internal/store/store.go` (`MaxLabels`, `ValidLabels`, `ParseRecord`).
* Related: [ADR 0050](0050-the-directory-record-says-what-an-endpoint-does-with-a-description-and-capabilities.md), [ADR 0053](0053-who-may-mail-whom-is-a-policy-file-compiled-by-mashctl-into-nats-permissions.md).
