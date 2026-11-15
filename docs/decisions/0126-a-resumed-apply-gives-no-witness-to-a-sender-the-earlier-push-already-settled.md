---
status: proposed
date: 2026-11-15
decision-makers: Randy Bias (mash maintainer)
---

# A resumed apply gives no witness to a sender the earlier push already settled

Amends: ADR-0100

## Context and Problem Statement

[ADR 0100](0100-the-applied-gate-proves-old-credentials-refused-by-probe-and-revocation.md) says that when the target
was pushed and the gate does not pass, the target is recorded as pending, and that the operator resolves it by
resubmitting the same list set: it is pushed and gated again, and when applied it is committed. `mashctl policy serve`
replays the pending document when it starts, so a service that was stopped after the push and before the commit should
resume to `applied` on restart.

It does not. Before a push, `policyserve` opens a witness connection per server for each sender the gate will ask a
closed witness for. A witness is closed only by the push: a change to a scoped template closes every connection under
that key at the push ([ADR 0097](0097-a-template-change-on-a-scoped-signing-key-closes-open-connections-and-binds-the-reconnect.md)),
and a push that does not change a template closes nothing. After a stop between the push and the commit the servers
already hold the target. The resumed run opens fresh witnesses under the target, pushes an account JWT with the same
content and a newer issue time, and nothing closes them. The gate requires every witness it is given closed, so the
status stays `partial` with "a connection open before the change is still open", and no later restart changes that. What
should the run do about witnesses when an earlier push already did the work?

## Decision Drivers

* The gate is unchanged: it must keep asking for a closed witness for every sender with a removed reach and every
  removed sender, and `internal/policyapply` is not edited.
* A witness proves that a connection which was open before the change is closed by it. It must not be dropped on a
  guess: a server that lags or is silent must keep every witness it has now.
* A restart must resume to `applied` without lowering a revocation.

## Considered Options

* Settle a sender before opening witnesses, from what every server already holds (chosen).
* Never open witnesses on a resumed run.
* Give the gate a way to accept an open witness after a resume.
* Roll the pending change back and push it again.

## Decision Outcome

Chosen option: "Settle a sender before opening witnesses". Before it opens witnesses, `policyserve` reads the account JWT
each server holds through the system account (`policyapply.NATSCluster.AccountJWTs`) and works out, per sender, whether
the earlier push already settled the change. A sender that is settled gets no witness, and the Applier reports it closed
to the gate, on the servers that were read, through a wrapper around the witnesses. Every other sender is treated as
before.

**What is read.** The condition holds only if every server of the declared `--servers` count answered with a non-empty
account JWT for this account that decodes. A short answer, an empty one, one that does not decode, one for another
account, or any read error settles nothing: every sender keeps its witness, as on a first run.

**What settles a template.** Every server's JWT holds, for each owner group of the target, the scoped signer under the
group's key with the group as its role and permissions equal to the target's, and holds none for an owner group the
target drops. The issue time is not compared. When this holds on every server, the reason of "a template changed" is
gone for every sender.

**What settles a removal.** A sender the change removes, or whose reach it removes, is settled by revocation when it has
at least one issued or rotated key in the issue record and every such key is in the revocation list of every server with
a time at or after the time the credential was issued, which revokes every credential made with that key. A key with no
recorded issue time cannot be judged, so the sender is not settled. A removed reach in a group whose template changed is
also settled once every server holds the target templates. A removed sender with its keys not all revoked keeps its
witness, as does a reach whose group template did not change.

**Why it is sound.**

* A change to a scoped template closes every connection under that key at the push (ADR 0097). Once every server holds
  the target templates, any connection open now was made under the target. A witness opened now could not be closed by
  the push by design, so its being open says nothing about the change, and a witness that can never close would hold the
  status at `partial` for ever.
* A key revoked on every server cannot connect again, and the connection it had was closed by the revocation. A revoked
  sender needs no witness: the one thing the witness would show is already shown by the revocation list.
* The condition is verified on every server of the declared count, from the JWT the server holds, not from an
  acknowledgement.
* Nothing is relaxed for a server that lags or is silent. If one server still holds the old templates, every affected
  sender keeps its witness on every server, so the run cannot reach `applied` until that server holds the target. The
  push of such a run closes the witnesses on the lagging server only, so the run ends `partial`, and the next restart
  finds every server on the target and settles.

### Consequences

* Good, because a `policy serve` stopped after the push and before the commit resumes to `applied` when restarted.
* Good, because the gate and `internal/policyapply` are unchanged and the witness check is as strict as before wherever
  the servers do not all hold the change.
* Bad, because a service stopped between the push and the revocation of a removed sender's keys, with every server on the
  target, gives that sender a witness that cannot close, and the run stays `partial` until the revocation is on every
  server. Closing this needs the removed sender settled by the changed template when the run itself revokes its keys
  after the push; that is not decided here.
* Bad, because a run with a lagging server takes two restarts to reach `applied`.
* Neutral, because one more read of the account JWTs is made before the witnesses, only when a witness would be opened.

The settlement reads the inventory form of the record (the greatest issue time over every retained revision of a key,
ADR 0115), and an incomplete history settles nothing. It rests on the same assumption as the gate's inventory and
`RemoveSender`: every credential mash issued is in the issue record. A credential made outside mash, or one the record
never held, is not revoked by mash and is not seen by the shortcut either, so its connection is not proven closed by
it; the first-list-set reconcile (ADR 0121) is where such credentials are found and removed.

### Confirmation

Unit tests in `internal/policyserve`: `TestSettledSenders` (every server on the target; one server lagging; a server
silent, empty or unreadable; revocations earlier than required or missing on one server; no key or no issue time in the
record; the record unreadable) and `TestSettledSendersWithoutServerStateSettlesNothing`. End to end in `cmd/mashctl`:
`TestPolicyServeResumesAfterKillBetweenPushAndCommit`, `TestPolicyServeResumesAfterKillWhileWaitingOnWitnesses` and
`TestPolicyServeRestartWithALaggingServerKeepsWitnesses`.

## Pros and Cons of the Options

### Settle a sender before opening witnesses (chosen)

* Good, because it relaxes a witness only on proof read from every server.
* Bad, because it reads the servers once more and reasons about the JWT's contents.

### Never open witnesses on a resumed run

* Good, because it is simple.
* Bad, because a resumed run whose push never reached a server would then pass without proof that a connection was closed.

### Give the gate a way to accept an open witness after a resume

* Good, because the logic would sit in one place.
* Bad, because it changes the gate, which ADR 0100 sets as the proof of an applied generation.

### Roll the pending change back and push it again

* Good, because the push is made again with witnesses open before it.
* Bad, because it lowers nothing but it takes every sender's connections down twice, and a stop during the rollback leaves
  the same problem.

## More Information

Amends [ADR 0100](0100-the-applied-gate-proves-old-credentials-refused-by-probe-and-revocation.md) (the resubmission it
describes now resumes to `applied` for a stop after the push). Relies on
[ADR 0097](0097-a-template-change-on-a-scoped-signing-key-closes-open-connections-and-binds-the-reconnect.md), whose text
is unchanged.
