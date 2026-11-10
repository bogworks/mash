---
status: accepted
date: 2026-11-10
decision-makers: Randy Bias (mash maintainer)
---

# A new serve of the same session takes over the endpoint lease

## Context and Problem Statement

On an account switch inside one Claude Code session (`/login`), Claude Code started a new `mash serve` while the old one,
under the same parent process on the same host, was still alive and still held the endpoint lease. The new serve found
the name in use and turned mail off; `mash status` kept naming the old pid after the old serve had been stopped; the new
serve took the lease only about 10 seconds later, on its retry. The lease already lets a restarted session take the lease
from a dead holder on the same machine. Should it also let a new serve take it from a live holder that belongs to the
same session, and what do the two serves, the hook and `status` say meanwhile?

## Decision Drivers

* A session must not lose its mail for a retry interval because its own earlier serve has not yet exited.
* A takeover across sessions or machines would let one session read another's mail: it must be refused on every
  mismatch the lease can show.
* The earlier serve must stop consuming and refreshing, and say why, so nobody is told it holds the name.
* While the session does not hold the lease, the model must be told, not left with silence.

## Considered Options

* Record the session in the lease and let a new serve of the same session take it over (chosen).
* Wait for the old serve to exit, with a shorter retry.
* Detect the old serve's death by its stdin closing only.

## Decision Outcome

The lease value gains three optional fields: `ppid` (the serving process's parent pid), `pstart` (that parent's start time,
from the same mechanism as the serve's own `start`, so a reused pid is told from the parent) and `session` (the
`CLAUDE_CODE_SESSION_ID` of the serve's environment). Each is absent when unknown.

When a lease is held, the taker takes it over with the compare-and-set update used for a dead holder if the holder has
the same `machine` as the taker and either the same non-empty `session`, or the same non-empty `ppid` and the same
non-empty `pstart`. It never takes the lease when a field is absent on either side, when the machines differ or one is
unknown, when the taker's parent pid is 1 or less, or when both sides carry a launch token and the tokens differ (another
launch of a running session, ADR 0043). It logs `lease: took over from the same session's earlier serve <instance> (pid
P)`. The rule applies to the legacy identity's lease (ADR 0076's alias) by the same code. A same-session holder that is
not taken because the compare-and-set lost to a newer write is retried after 1 second, up to 5 times in a row, then every
10 seconds as before.

The earlier serve learns of the takeover at once. Besides the refresh, it reads its lease key every second (and the
legacy identity's key, for an endpoint with an alias); an entry that is another instance's, or a deprovision's tombstone,
marks the lease lost with the same error a failed refresh gives, which now names the new holder and says whether it is of
the same session. A JetStream KV Watch would push the change but creates an ordered consumer on the lease bucket, which
no endpoint's credentials allow; the read needs only the direct get of its own key that they already have. An absent or
deleted key is not a loss (the refresh re-takes a free lease or finds who took it).

From the moment the lease is lost, the endpoint delivers nothing and acknowledges nothing, for any stream, not only the
legacy one (the rule of ADR 0076's legacy lease, generalised to the own lease): mail in the consumer's pull buffer,
queued, in the deliver loop's batch or held for a hook is nak'd, its waiting spool entries are removed, and mail already
delivered but not yet acknowledged gets no ack (the ack check is made at each ack). JetStream redelivers all of it to the
new holder at once; a repeat is the safe side, a lost message is not. Without this the earlier serve, still pulling the
shared consumer until its next refresh, could ack mail it wrote to a client no longer processing its pipe, and that mail
would never reach the new serve.

For a same-session holder the earlier serve then stops consuming and refreshing, closes its endpoint (which never touches
the new holder's lease), and becomes superseded: its tools answer that it was superseded by a newer mash serve of the same
session (naming the pid) and that mail is handled there, `status` shows `connection: SUPERSEDED` with the new holder, and
it exits when its stdin closes or its parent changes. A lease lost to another session ends the serve as before.

While a serve waits for the lease it records the holder (pid, host, since), its own pid and a per-serve instance token,
its parent and its session in `<spool>/.lease-wait`, rewritten on every retry. The files are shared by every serve of the
endpoint, so a serve removes `.lease-wait` (and `.lease-wait-told`) when it takes the lease or exits only if the file still
names its own pid and instance; when two live serves wait, the last writer's state stands and the other's later removal
does not clear it. The hook removes the files only when their serve process is gone. `mash hook` of that session
(the serve's session id, or the serve's parent among its ancestors) prints `mash: this session does not hold endpoint
<name> (held by pid P on H); mail is off until it recovers` on the first run after the state appears and at most every 10
minutes after, keeping the rate limit in `<spool>/.lease-wait-told`. It prints nothing for an absent state, for a state
whose serve process is gone, or for the hook of another session. The holder that `status`, the tools and the channel notice
name is the one found by the latest retry.

`mash serve` exits when its input reaches end of file (`internal/mcp/server.go`, `Run`); that was already so and is
covered by a test. It is not used here because in the observed case the old serve's pipe stayed open.

### Consequences

* Good, because the new serve delivers at once, with no retry delay.
* Good, because the earlier serve says what happened instead of reporting a name it no longer holds.
* Good, because the session is told when its mail is off.
* Good, because the earlier serve cannot ack mail the new serve should receive.
* Bad, because the earlier serve reads its lease key every second (one direct get per lease held), and a window of up to
  a second remains in which mail it receives is delivered to a client that may be gone; it is not acknowledged, so the new
  serve is redelivered it.
* Bad, because the takeover trusts the session id and parent recorded in the lease, which the holder writes. Trust
  statement: session id and parent identity are claims written by the holder. A process holding the endpoint's credentials
  on the same machine is already the endpoint (it can publish and consume as it), so a same-session takeover adds no
  authority. Takeover is refused for different sessions, other machines, missing fields, and different launch tokens.

### Confirmation

Tests cover the takeover by session id and by parent (the parent only when a side has no session id), the legacy lease, and
the cases that must not take over (another session id, two different session ids with the same parent, another parent, the same parent with another start time, another machine, absent fields, a parent of 1 or
less, another launch token). They cover the earlier serve's step-down and superseded answers and exit, learning of the loss within a second with a
one-hour refresh interval (own and legacy lease), no ack by the earlier serve of mail it held (consumer ack floor) and
exactly-once delivery to the new serve, the short retry, the refreshed holder, the hook notice (emit, rate limit, stale,
cleared, other session) and the ownership-checked removal of the lease-wait state by two waiting serves.

## Pros and Cons of the Options

### Take over by session identity (chosen)

* Good, because it is immediate and uses the compare-and-set that already guards takeovers.
* Bad, because the lease carries more fields.

### Wait for the old serve to exit, with a shorter retry

* Good, because it adds no rule.
* Bad, because the old serve may not exit while its pipe stays open, and mail stays off meanwhile.

### Detect the old serve's death by its stdin closing only

* Good, because it is simple.
* Bad, because in the observed case the pipe stayed open.

## More Information

* Related: [ADR 0043](0043-second-launch-of-a-running-session-is-refused-and-told-once.md), [ADR 0076](0076-a-three-token-record-may-carry-the-legacy-identity-it-answers-to-as-an-alias.md).
