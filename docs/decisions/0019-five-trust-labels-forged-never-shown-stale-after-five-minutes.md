---
status: accepted
date: 2026-10-21
decision-makers: Randy Bias (mash maintainer)
recorded: 2026-10-31 (backfilled)
---

# Five trust labels; forged mail is never shown; a signature over 5 minutes old is `stale`

## Context and Problem Statement

Once mail is signed (ADR-0017), the receiver still has to tell the model what the signature check found. SAP §13.1
defines three trust classes (verified, claimed, absent) and treats a failing signature or a malformed header as a
rejection (§13.7), but it has no class for a valid signature by a
key the receiver's directory does not know, and its rule for stored messages (§13.8) has no time window, so a
captured message re-published later verifies like new. mash had to decide which labels the model sees and what
happens to mail that fails the check.

## Decision Drivers

* The model must see, on every message, how far its sender is proven.
* Forged mail must never reach the model, and must not block the inbox behind it.
* A replayed old message must not read as fresh, verified mail.

## Considered Options (reconstructed 2026-10-31; not recorded at the time)

* SAP's classes only, with forged mail shown under a label.
* SAP's classes plus mash's `unknown-signer` and `stale`; forged and malformed mail dropped and acked.
* As above, but old signatures rejected outright instead of labelled.

## Decision Outcome

Chosen option: "SAP's classes plus `unknown-signer` and `stale`; forged and malformed mail dropped and acked". Forged
mail is "acked so [it] can't block the inbox" (spec §4), and `stale` marks "what a replayed capture looks like"
(spec §4). Why a stale message is shown as information instead of dropped was not recorded.

As built (SAP classes plus two of mash's own):

* Shown labels: `verified` (valid signature and a directory match), `unknown-signer` (valid signature, but the
  directory does not know the key or maps it to another name), `claimed` (a header with no signature), `absent` (no
  header), `stale` (a valid signature made more than 5 minutes before the stream stored the message, or more than
  30 seconds in the future).
* `forged` (a failing signature) and `malformed` mail is never shown: it is logged, counted and acked.
* The value 5 minutes was chosen without a recorded reason. The 30-second future bound is SAP §13.7's live clock-skew
  window (`sap.LiveSkew`).

### Consequences

* Good, because the model gets a one-word label on every message and the instructions say what each label means.
* Good, because a forged message costs one log line and never stalls delivery.
* Bad, because a genuine message stamped more than 5 minutes before the server stored it (for example, by a sender
  whose clock runs well behind) reads as `stale`.
* Neutral, because `stale` mail is still shown, as information only, so a replay is visible rather than silent.

### Confirmation

* `internal/endpoint/endpoint_test.go`: `TestForgedDroppedClaimedLabelled`, `TestUnknownSigner`,
  `TestNameClaimMismatchIsUnknownSigner`, `TestReplayedOldMailIsStale`.
* `internal/sap/sender_test.go`: `TestVerifyStoredClasses`, `TestVerifyLiveReplayAndSkew`.
* `internal/render/render_test.go`: `TestUnverifiedLabelled`.

## Pros and Cons of the Options

### SAP classes only, forged shown

* Good, because it adds nothing to SAP.
* Bad, because a valid signature by an unknown key would read as verified or need an ad hoc rule, and forged text
  would reach the model.

### SAP classes plus `unknown-signer` and `stale`, forged dropped

* Good, because every shown label says something the model can act on.
* Bad, because two labels are mash's own, outside SAP.

### Old signatures rejected

* Good, because a replay never reaches the model.
* Bad, because a genuine late message would vanish without trace.

## More Information

* Spec `docs/specs/2026-10-21-mash-design.md` §4; `stale` and quoted bodies were added after review the same night
  (commit `2db0717`, 2026-10-21 22:51 UTC).
* Code: `internal/sap/sender.go` (`Trust`, `VerifyStored`), `internal/endpoint/mail.go` (`classify`, `MaxSignedAge`).
* `SECURITY.md` lists the five labels.
* Related: ADR-0011 (the directory), ADR-0017 (signing), ADR-0020 (what a label means for acting on mail),
  ADR-0021 (quoted bodies).
