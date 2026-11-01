---
status: accepted
date: 2026-10-21
decision-makers: Randy Bias (mash maintainer)
recorded: 2026-10-31 (backfilled)
---

# Every message is signed per SAP §13 with the endpoint's own NATS key

## Context and Problem Statement

Agents act on mail, so a receiver must know who sent each message. NATS authenticates a connection, not a message: a
stored message carries no proof of which user published it. The Synadia Agent Protocol (SAP) 0.3, §13, defines an
`Agent-Sender` header that the sender signs with the ed25519 key behind its NATS user (its "nkey"). mash had to decide
how its mail proves its sender.

## Decision Drivers

* The receiver must be able to verify a sender from the stored message alone, after the fact.
* mash invents no protocol where SAP has one (ADR-0003).
* No extra key to issue, store or rotate beyond the NATS user each endpoint already has (ADR-0007).

## Considered Options (reconstructed 2026-10-31; not recorded at the time)

* Sign every message per SAP §13 with the endpoint's own NATS user key.
* A separate mash signing key per endpoint.
* No per-message signature; rely on NATS connection authentication and subject permissions.

## Decision Outcome

Chosen option: "sign per SAP §13 with the endpoint's own NATS key", because "every message is signed by the sender's
own NATS key (§13), so the receiver knows who sent it" (spec §1). Reusing SAP's header instead of a new scheme
follows from ADR-0003 (reconstructed).

As built (SAP layer, inherited; mash applies it):

* Every message mash publishes carries a signed `Agent-Sender` header: mail, heartbeats, and the prompt requests sent
  by `mash prompt`. Replies are the exception: live-prompt response chunks and `status` replies are published
  unsigned, as SAP allows. The signed input is the tag `AGENT-SENDER-V1`, then account, user, subject, timestamp, nonce and
  the SHA-256 of the body (`internal/sap/sender.go`, `signedInput`). The identity comes from the endpoint's
  credentials file (`sap.IdentityFromCreds`); there is no separate mash key.
* The header's `name` field is not signed. The receiver takes the sender's name from the directory, which maps NATS
  user to endpoint name (ADR-0011); a header naming another name gets `[unknown-signer]` (ADR-0019).
* Stored mail is checked per SAP §13.8: signature and subject only, with no time window and no nonce set. mash adds its
  own age check (`stale`, ADR-0019).
* Mail sets `Nats-Msg-Id`, the NATS JetStream de-duplication id, to the nonce.

### Consequences

* Good, because any SAP tool can verify mash mail, and mash can verify any SAP sender.
* Good, because a stored message stays verifiable for as long as it is kept.
* Bad, because a stolen credentials file lets the thief sign as that endpoint until its NATS user is revoked.
* Neutral, because the signature proves which key signed, not which name; naming rests on the directory.

### Confirmation

* `internal/sap/sender_test.go`: `TestSignKnownAnswerB13` (a known-answer vector), `TestVerifyStoredClasses`,
  `TestVerifyLiveReplayAndSkew`, `TestSignVerifyRoundTripFreshKey`, `TestIDSigRoundTrip`.
* `internal/endpoint/endpoint_test.go`: `TestUnknownSigner`, `TestNameClaimMismatchIsUnknownSigner`,
  `TestForgedDroppedClaimedLabelled`.

## Pros and Cons of the Options

### SAP §13 with the NATS user key

* Good, because it is a published format and needs no new key.
* Bad, because the signing key is also the connection credential, so one leak gives both.

### A separate mash signing key

* Good, because signing could be revoked apart from connecting.
* Bad, because it is a second key to issue and store, and a format no other SAP tool reads.

### No per-message signature

* Good, because it costs nothing.
* Bad, because a stored message would carry no proof of its sender, and mash's subject permissions alone do not name
  the publisher of a message read later.

## More Information

* Spec `docs/specs/2026-10-21-mash-design.md` §1 and §4; SAP 0.3 core protocol §13.5-§13.8 and §13.10-§13.11.
* Code: `internal/sap/sender.go`, `internal/endpoint/mail.go` (`classify`, send path).
* Related: ADR-0003 (SAP plus a mail verb), ADR-0007 (one NATS user per endpoint), ADR-0011 (the directory),
  ADR-0018 (mash's own signed marks), ADR-0019 (trust labels).
