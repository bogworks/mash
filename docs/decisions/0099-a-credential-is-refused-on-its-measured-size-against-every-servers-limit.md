---
status: accepted
date: 2026-11-12
decision-makers: Randy Bias (mash maintainer)
---

# A credential is refused on its measured size against every server's limit

## Context and Problem Statement

A sender's user JWT travels in the CONNECT line, which a server reads up to its `max_control_line` (4,096 bytes by
default) and then closes the connection. Every exception on a sender's allow or deny list is a tag on its JWT, so a long
list makes a credential a server will not take, and the sender fails only when it tries to connect. The account JWT has a
limit too. Until now mash checked one fixed constant for the user JWT (3,750 bytes, a stand-in with headroom, not the whole
line, not per server) and a tag count warning. A cluster can run different settings on different servers. How does mash
decide that a credential fits, and what does it do when one does not?

## Decision Drivers

* A credential that fits only some servers fails only on the others, at connect time, far from the change that caused it.
* Exceptions must never be dropped, merged or widened to make a credential fit: that would silently change policy.
* A limit stated as a count is wrong for some names: the room depends on the length of the names in the tags.
* A server's limit must not be guessed.
* Nothing may be signed, issued or pushed when the set is refused.

## Considered Options

* Measure the full CONNECT line of each sender and each account JWT against the limit of every server, learned from the
  servers, and refuse the whole set on any miss (chosen).
* Keep one fixed constant for the user JWT and a count of tags.
* Cap the count of exceptions (100) and refuse above it.
* Shrink the JWT (drop or merge entries) until it fits.

## Decision Outcome

mash measures, before it signs or pushes anything:

* each sender's user JWT inside the CONNECT line the client sends (the JSON argument: the JWT, its signature, the client
  name `mash <endpoint>`, the protocol and option fields), against each server's `max_control_line`; and
* each account JWT against each server's limit for it: the jwt decoder cap of 1 MiB, lowered to the server's
  `max_payload` when that is smaller, because the claims update is one message of that size.

The measure is the bytes nats-server counts: the argument of the CONNECT, built the way nats.go builds it, with a version
number placeholder wider than the real one and the longer spelling of `tls_required`. It is therefore never below the
server's own count and above it by at most 6 bytes; a calibration test against a real server pins that.

The limits come from the servers. `policy apply` asks every server for its limits with the system account `VARZ`
request, whose reply names the server and carries `max_control_line` and `max_payload`. `add` and `reissue` do the same
when system account credentials can be found; without them, and for the offline `policy check` and `policy show`, the
measure is against the default limits and names them as `default server limit`. A server that does not answer is not
assumed small or large: the command refuses and names it, unless the operator gives the limit (`--max-control-line` on
apply, or `MASH_MAX_CONTROL_LINE`), which stands for every server that did not answer and caps one that did.

When any credential does not fit any server, the whole list set is refused: the error names each sender or account, its
size, the limit and the server, and nothing is signed, issued or pushed. mash refuses on measured size, never on a
count, and never drops, merges or widens an entry. The old fixed constant and the tag count warning are removed.

The check is built against the compile that ships, the list set compile of ADR 0094 (`listset.Compile`): each sender's
user JWT is built from its name and tags, signed by a throwaway scoped key on behalf of the account, and the account JWT
carries every owner group's template as a scoped signer. A tag is `allow:<owner>.<name>` or `deny:<owner>.<name>` (the
range is a literal in the template), so room depends on the length of the receiver's owner part and name. The deny
sentinel is one tag, carried only by senders of a group whose key has a deny entry and who have no deny of their own.
`listset.SizeCheck` is the entry point and the only listset measurement: it builds each sender's user JWT and the
account JWT and measures the user's whole CONNECT line with `policy.ConnectLineBytes` (the JWT, a full-length signature,
the client name `mash <name>` and the options), the measure calibrated live against nats.go and a real nats-server
(`internal/policy/limits_live_test.go`). `internal/listset` imports `internal/policy` for that measure; `internal/policy`
does not import `internal/listset`. It is not wired into `policy apply` yet: today's zone compile, which apply still
uses, keeps the measured check (`policy.BudgetFor`) until zones are removed, and the list set consumer calls
`SizeCheck` (through `listset.DryRun`) when it replaces it.

The documents state the limit as a measured table by owner part and name length, with worked examples, and the way out:
use a shared verb instead of a long list of exceptions.

### Consequences

* Good, because a credential that cannot connect on some server is refused before it exists, naming the server.
* Good, because a cluster is limited by its smallest server, and the message says which.
* Good, because policy is never changed to make a credential fit.
* Bad, because apply, add and reissue now make one more request to the system account.
* Bad, because the measure refuses a credential within 6 bytes above a limit that the server would take.
* Bad, because the room depends on the length of the owner part and the names, so there is no single number.

### Confirmation

Tests cover the measure against a real in-process server at several `max_control_line` values (the largest tag count that
connects, and the measure within its documented slack), the CONNECT line a real client writes captured and compared with
the measure, the documented table of exceptions that fit (asserted against `listset.SizeCheck`), for allow exceptions on a closed verb, with and without the sentinel, and
deny exceptions, at several owner part and name lengths of list sets (the largest set accepted and one more refused with
the sender, count, size and limit), a set refused whole when one of several servers has a smaller limit with nothing signed
or pushed, an account JWT over one server's limit refused naming the account, a server that gives no limit refused
unless the operator supplies one, and the limits read from a live server's `VARZ` reply.

## Pros and Cons of the Options

### Measure the full line against every server's limit and refuse the whole set (chosen)

* Good, because it is exactly the condition under which a connect fails.
* Good, because the limit of each server is its own, learned and not assumed.
* Bad, because it needs the system account to learn the limits, or the operator's word.

### Keep one fixed constant and a tag count

* Good, because it needs no server.
* Bad, because a constant is not the limit of any server, and a cluster of different settings has no single value.
* Bad, because a count is wrong for long names and for short ones.

### Cap the count of exceptions at 100

* Good, because it is simple to state.
* Bad, because 100 does not fit at long names, and is far below the limit at very short ones.

### Shrink the JWT until it fits

* Good, because the command would succeed.
* Bad, because dropping or merging an exception changes who may mail whom without saying so.

## More Information

* The limit, with worked examples: [the policy file](../policy-file.md#how-many-exceptions-one-sender-can-have).
