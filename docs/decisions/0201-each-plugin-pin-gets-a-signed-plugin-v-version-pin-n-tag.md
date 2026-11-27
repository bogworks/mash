---
status: accepted
date: 2026-11-27
decision-makers: the planner (a planner ruling of 2026-11-27, which Randy Bias, the mash maintainer, may overturn)
---

# Each plugin pin gets a signed `plugin-v<version>-pin.<n>` tag

## Context and Problem Statement

A release tag such as v0.6.1 is made before the plugin's pin for that release, because the pin names the published
binaries and so can only be written after they exist (release step 8). A person who adds the marketplace at a release
tag therefore gets a plugin that still pins the previous release: at v0.6.1 it runs mash 0.6.0. The default branch has
the pin, but it moves, and a person who wants a fixed, checkable plugin has nothing to name.

How does a person get a plugin whose pin is fixed, without changing when releases are tagged and without starting a
release build?

## Decision Drivers

* A plugin whose pin is fixed and checkable by a name.
* The pin must name binaries that already exist.
* A new tag must not start `release.yml`.
* A new kind of public tag is a standing decision ([ADR 0002](0002-record-architecture-decisions-as-madr-records.md)).

## Considered Options

* (a) After each pin pull request merges, tag the pin commit `plugin-v<version>-pin.<n>`, signed, and keep `release.yml` from starting on it.
* (b) Put the pin before the release tag.
* (c) No pin tags: use the default branch only.

## Decision Outcome

Chosen option: "(a) a pin tag that `release.yml` ignores", because it gives a person a fixed name for the pinned
plugin and changes nothing about how a release is built or tagged.

* After each pin pull request merges, release operations create a signed tag `plugin-v<version>-pin.<n>` on the pin
  commit, for example `plugin-v0.6.1-pin.1`, with the same release signing key as the release tags
  ([ADR 0199](0199-from-signed-from-a-release-tag-is-signed-with-the-mash-release-key-and-the-attestations-stay.md)).
* `release.yml` does not start on it: its trigger is `tags: ['v*']`, which does not match a tag that begins `plugin-v`.
* To pin the plugin, a person adds the marketplace at that tag, `claude plugin marketplace add bogworks/mash#plugin-v0.6.1-pin.1`;
  otherwise they use the default branch. The install page and `docs/release.md` say so, from 0.6.1's pin onward.
* No such tag exists before this merges.

### Consequences

* Good, because a person can name a fixed, signed plugin whose pin is built.
* Good, because the release build and the release tags are untouched.
* Bad, because there is one more tag to make after each release, by release operations.

### Confirmation

`release.yml`'s `tags` trigger stays `['v*']`; a pin tag does not start a release run. The pin tag verifies with `git -c
gpg.ssh.allowedSignersFile=docs/allowed_signers verify-tag plugin-v<version>-pin.<n>`.

## Pros and Cons of the Options

* (a) A pin tag that `release.yml` ignores: a fixed name for the pinned plugin, one extra tag to make.
* (b) The pin before the release tag: rejected, because the pin would name binaries that are not yet built.
* (c) No pin tags, default branch only: nothing to maintain, and still what most people do, but there is no fixed name.

## More Information

Adds a kind of public tag; changes no earlier decision.
