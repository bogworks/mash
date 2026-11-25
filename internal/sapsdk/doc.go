// Package sapsdk reaches the SAP Go SDK's identity package (synadia-agents.go, github.com/bogworks/synadia-agents.go/identity)
// behind mash's own types. It is the 0.6.0 sender-identity adapter (stage S4 of the SDK adoption contract, REQ-005).
//
// It depends on internal/sap for the types (sap.SenderHeader, sap.Trust) and never the other way round. The binaries
// link it from the switch-on release (stage W); the release check's linked-package guard no longer refuses this package
// or the SDK identity package (it still refuses the SDK caller and host, which S5 owns). The old code in internal/sap
// stays as the test oracle.
//
// # What it does
//
// Sign, IDSig, VerifyIDSig, VerifyStored and VerifyLive give, for ordinary input, what internal/sap gives: the signed
// header and the id_sig are byte for byte the same. Classify is the pure form of the stored check: it returns the trust
// class and the refusal class and does nothing else (no files, no logs, no counts, no nonce is recorded).
//
// Every verification function takes every Agent-Sender header value (a []string), not one Header.Get value. No value at
// all is the absent class, as today. An empty value is malformed (empty sender header), and so are two values (two
// sender headers). The SDK's strict parser is used as it is: no setting keeps the lenient one.
//
// The key of a sap.Identity is not exported, so this package has its own Identity, built from a seed (NewIdentity) or a
// credentials file (IdentityFromCredsAt), with the same checks as internal/sap's.
//
// # Refusal classes (frozen; REQ-017)
//
// A refusal says a class and never the sender's content: no header value, name, user, SDK error text or payload. The
// classes are plain words. The ten shapes of REQ-005:
//
//	header too long             the value is over 2,048 bytes
//	time not in UTC             ts has an offset, not Z
//	control character in name   name has a control character
//	name too long               name is over 64 UTF-16 units
//	broken character in name    name has a lone surrogate escape
//	a field appears twice       a JSON member repeats
//	space in subject            sub has a space
//	signature unreadable        sig is not base64url of 64 bytes
//	empty sender header         the value is empty
//	two sender headers          two Agent-Sender values
//
// The other malformed cases mash refuses today (internal/sap/sender.go, parse):
//
//	header is not a JSON object   the value is not JSON, or JSON that is not an object
//	version missing               no v
//	version is not a number       v is not a JSON number (a string, a boolean, null)
//	field is not text             account, user, name, sub, ts, nonce or sig is not a string
//	account or user not valid     account or user missing, or not a valid account or user key
//	signed header incomplete      sig without sub, ts and nonce (or with an empty sub)
//	message id not valid          nonce is not 1 to 64 of [A-Za-z0-9_-]
//	time not valid                ts is not a time at all
//
// And the two catch-alls:
//
//	unreadable sender header      any other SDK parse refusal
//	not verified                  a reason for refusing, not a trust kind: Classify gives it only for a forged sender
//	                              (a failed or replayed signature, a wrong subject or time). A claim (no signature)
//	                              and no header have an empty class and keep their trust kind; the caller that
//	                              refuses such mail gives it this reason. Verified, claimed, absent, unknown-signer
//	                              and stale all have an empty class.
//
// An unknown v is the absent class, as in mash's parser.
//
// # Signing refusals
//
// Sign and IDSig return a *SignError. SDK v0.1.0 returns plain errors with no type for most refusals, so the class is
// found from the sentinel (ErrInvalidAgentID) or from the SDK's fixed refusal text, which TestSigningRefusalsMatchTheSDK
// pins to the pinned SDK version; a pin bump that rewords one fails that test, and without it the class would be
// unspecified. The classes it can tell apart in v0.1.0:
//
//	your account is not valid      the account is neither an account key, a name of [A-Za-z0-9_-]+, nor $G
//	the subject is not valid       the subject to sign is not a NATS subject
//	your name is too long          the display name is over 64 UTF-16 units
//	the message id is not valid    the nonce is not 1 to 64 of [A-Za-z0-9_-]
//	unspecified                    anything else: a name the SDK refuses for another reason than its length (a control
//	                               character), an identity with no seed, a signer failure, any other SDK error
//
// # Live verification and the nonce
//
// VerifyLive runs identity.Gate.AdmitPrompt, never Gate.Classify, on a throwaway nonce set, with the directory check as
// GateOptions.PreAccept. The order is: parse; ts, sub and signature; the directory check; and only then the caller's
// NonceSet, which records. A sender the directory does not list records no nonce, so it cannot use up nonce capacity
// (REQ-020), and its replay is refused the same way as its first request. The NonceSet interface of this package says
// fresh, seen or refused-full (the SDK caps are 100,000 entries in all and 10,000 for one sender), which sap.NonceSet's
// bool cannot; a correctly signed sender past a cap is OverNonceCap with its own reason, ReasonNonceCap, and is never
// told that a signed header is required.
//
// The set's window is at least 2*LiveSkew (60 s), not LiveSkew. A header verifies from ts-LiveSkew to ts+LiveSkew, and
// the nonce is recorded at receipt, which can be as early as ts-LiveSkew; the pair must be held until the header can no
// longer verify, 2*LiveSkew after the first receipt. With a window of LiveSkew a header 29 s in the future is admitted,
// forgotten 30 s later while its ts is still good, and admitted again. NewNonceSet raises a smaller window to the floor.
package sapsdk
