package sapsdk

import (
	"context"
	"sync/atomic"
	"time"

	"github.com/bogworks/synadia-agents.go/identity"

	"github.com/bogworks/mash/internal/sap"
)

// LiveSkew is the replay window: the ts skew and the nonce window.
const LiveSkew = sap.LiveSkew

// NonceStatus is what a NonceSet says about a (user, nonce) pair it was offered.
type NonceStatus int

const (
	// NonceFresh: the pair was new and is now recorded.
	NonceFresh NonceStatus = iota
	// NonceSeen: the pair was already there, a replay. Nothing is recorded.
	NonceSeen
	// NonceFull: the set refuses the pair, because it is full or the sender is at its quota. Nothing is recorded. This
	// is a refusal of the sender for now, and not a replay.
	NonceFull
)

// NonceSet remembers (user, nonce) pairs for a window. Seen records a fresh pair.
type NonceSet interface {
	Seen(user, nonce string, now time.Time) NonceStatus
}

// SDKNonceSet is the SDK's nonce set behind NonceSet: 100,000 entries in all and 10,000 for one user by default.
type SDKNonceSet struct {
	set *identity.NonceSet
	now atomic.Int64 // unix nanoseconds of the last clock a caller passed in
}

// NewNonceSet returns a set that forgets pairs after window (at least LiveSkew), with the SDK's default caps.
func NewNonceSet(window time.Duration) *SDKNonceSet { return NewNonceSetCaps(window, 0, 0) }

// NewNonceSetCaps is NewNonceSet with the caps given (zero is the SDK's default).
func NewNonceSetCaps(window time.Duration, maxEntries, maxPerSender int) *SDKNonceSet {
	if window < LiveSkew {
		window = LiveSkew // the SDK needs the set's window to cover the replay window
	}
	s := &SDKNonceSet{}
	s.set = identity.NewNonceSet(identity.NonceSetOptions{Window: window, MaxEntries: maxEntries, MaxPerSender: maxPerSender,
		Now: func() time.Time { return time.Unix(0, s.now.Load()).UTC() }})
	return s
}

// Seen records the pair at now when it is fresh. A full set and a sender at its quota are NonceFull: the SDK's set
// fails closed.
func (s *SDKNonceSet) Seen(user, nonce string, now time.Time) NonceStatus {
	s.now.Store(now.UnixNano())
	switch s.set.Record(user, nonce, now) {
	case identity.Recorded:
		return NonceFresh
	case identity.SetFull, identity.SenderQuotaExceeded:
		return NonceFull
	}
	return NonceSeen // Replayed; Expired cannot happen, the pair is recorded at now
}

// Len is the number of unexpired pairs the set holds, at the last clock it was given.
func (s *SDKNonceSet) Len() int { return s.set.Len() }

// Contains reports whether the pair is held, without recording anything.
func (s *SDKNonceSet) Contains(user, nonce string) bool { return s.set.Seen(user, nonce) }

// Outcome is the result of a live check, in the terms W maps to a reply.
type Outcome string

const (
	// Admitted: a verified, listed sender with a fresh nonce. The nonce is recorded.
	Admitted Outcome = "admitted"
	// Absent: no header, or an unknown v. Reply 401, ReasonSignedRequired.
	Absent Outcome = "absent"
	// Claimed: a header without a signature. Reply 401, ReasonSignedRequired.
	Claimed Outcome = "claimed"
	// Malformed: the header is malformed. Reply 400, ReasonMalformed. Class says which.
	Malformed Outcome = "malformed"
	// NotVerified: a failed signature, subject or time check, or a replayed nonce. Reply 401, ReasonSignedRequired.
	NotVerified Outcome = "not verified"
	// NotListed: a verified signature by a key the directory does not list. Reply 403. No nonce is recorded.
	NotListed Outcome = "not listed"
	// OverNonceCap: a verified, listed sender the nonce set refuses because it is full or the sender is at its
	// quota. Reply 401, ReasonNonceCap.
	OverNonceCap Outcome = "over nonce cap"
)

// The reply texts this package decides. The malformed text and the signed-required text are the ones endpoint/service.go
// replies with today.
const (
	ReasonMalformed      = "malformed Agent-Sender header"
	ReasonSignedRequired = "a signed Agent-Sender header is required (min_sender_trust=signed)"
	ReasonNotListed      = "the sender is not a mash endpoint"
	ReasonNonceCap       = "too many recent prompts: try again shortly"
)

// LiveResult is a live check's outcome.
type LiveResult struct {
	Outcome Outcome
	Header  sap.SenderHeader
	Trust   sap.Trust
	// Class is the malformed class for Malformed, ClassNotVerified for the other refusals of a sender that could not be
	// proved (Absent, Claimed, NotVerified), and empty otherwise.
	Class string
}

// Status is the reply status for the outcome: 0 for Admitted.
func (r LiveResult) Status() int {
	switch r.Outcome {
	case Admitted:
		return 0
	case Malformed:
		return 400
	case NotListed:
		return 403
	}
	return 401
}

// Reason is the reply text for the outcome: empty for Admitted.
func (r LiveResult) Reason() string {
	switch r.Outcome {
	case Admitted:
		return ""
	case Malformed:
		return ReasonMalformed
	case NotListed:
		return ReasonNotListed
	case OverNonceCap:
		return ReasonNonceCap
	}
	return ReasonSignedRequired
}

// VerifyLive classifies a live prompt (§13.7) in this order: parse, ts window, subject and signature, with nothing
// recorded; then listed (the directory check, nil admits every key); and only then nonces.Seen, which records. A key that
// is not listed records no nonce. values is every Agent-Sender value of the request.
func VerifyLive(values []string, arrivalSubject string, payload []byte, now time.Time, nonces NonceSet, listed func(user, account string) bool) LiveResult {
	value, class, absent := readValue(values)
	switch {
	case absent:
		return LiveResult{Outcome: Absent, Trust: sap.TrustAbsent, Class: ClassNotVerified}
	case class != "":
		return LiveResult{Outcome: Malformed, Trust: sap.TrustMalformed, Class: class}
	}
	// The gate gets a throwaway nonce set, so it records nothing the caller can see; the caller's set is asked last.
	gate, err := identity.NewGate(identity.GateOptions{ReplayWindow: LiveSkew, PreAccept: listed,
		Now: func() time.Time { return now }})
	if err != nil {
		return LiveResult{Outcome: NotVerified, Trust: sap.TrustForged, Class: ClassNotVerified} // fail closed
	}
	adm := gate.AdmitPrompt(context.Background(), message(arrivalSubject, payload, value))
	if adm.Err != nil {
		switch adm.Err.Code {
		case 400:
			return LiveResult{Outcome: Malformed, Trust: sap.TrustMalformed, Class: malformedClass(value, adm.Err)}
		case 403:
			r := LiveResult{Outcome: NotListed, Trust: sap.TrustVerified}
			if h, perr := identity.ParseHeader(value); perr == nil && h != nil {
				r.Header = fromSDK(*h)
			}
			return r
		}
		r := LiveResult{Outcome: NotVerified, Trust: sap.TrustForged, Class: ClassNotVerified}
		if h, perr := identity.ParseHeader(value); perr == nil && h != nil {
			r.Header = fromSDK(*h)
		}
		return r
	}
	switch identity.TrustOf(adm.Sender) {
	case identity.TrustVerified:
		h := fromSDK(adm.Sender.Header)
		switch nonces.Seen(h.User, h.Nonce, now) {
		case NonceFresh:
			return LiveResult{Outcome: Admitted, Header: h, Trust: sap.TrustVerified}
		case NonceFull:
			return LiveResult{Outcome: OverNonceCap, Header: h, Trust: sap.TrustVerified}
		}
		return LiveResult{Outcome: NotVerified, Header: h, Trust: sap.TrustForged, Class: ClassNotVerified} // a replay
	case identity.TrustClaimed:
		return LiveResult{Outcome: Claimed, Header: fromSDK(adm.Sender.Header), Trust: sap.TrustClaimed, Class: ClassNotVerified}
	}
	return LiveResult{Outcome: Absent, Trust: sap.TrustAbsent, Class: ClassNotVerified}
}
