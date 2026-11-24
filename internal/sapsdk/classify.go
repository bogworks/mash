package sapsdk

import (
	"encoding/json"
	"errors"
	"strings"
	"time"
	"unicode"
	"unicode/utf16"

	"github.com/bogworks/synadia-agents.go/identity"
	"github.com/nats-io/nats.go"

	"github.com/bogworks/mash/internal/sap"
)

// The refusal classes of a header. The strings are frozen (REQ-017); the package doc lists what each means.
const (
	ClassHeaderTooLong  = "header too long"
	ClassTimeNotUTC     = "time not in UTC"
	ClassControlChar    = "control character in name"
	ClassNameTooLong    = "name too long"
	ClassBrokenChar     = "broken character in name"
	ClassFieldTwice     = "a field appears twice"
	ClassSpaceInSubject = "space in subject"
	ClassSigUnreadable  = "signature unreadable"
	ClassEmpty          = "empty sender header"
	ClassTwoHeaders     = "two sender headers"

	ClassNotJSON     = "header is not a JSON object"
	ClassNoVersion   = "version missing"
	ClassVersionType = "version is not a number"
	ClassFieldType   = "field is not text"
	ClassAccountUser = "account or user not valid"
	ClassIncomplete  = "signed header incomplete"
	ClassNonce       = "message id not valid"
	ClassTimeInvalid = "time not valid"

	ClassUnreadable  = "unreadable sender header"
	ClassNotVerified = "not verified"
)

// MalformedClasses is every class a malformed header can have.
var MalformedClasses = []string{
	ClassHeaderTooLong, ClassTimeNotUTC, ClassControlChar, ClassNameTooLong, ClassBrokenChar, ClassFieldTwice,
	ClassSpaceInSubject, ClassSigUnreadable, ClassEmpty, ClassTwoHeaders,
	ClassNotJSON, ClassNoVersion, ClassVersionType, ClassFieldType, ClassAccountUser, ClassIncomplete, ClassNonce,
	ClassTimeInvalid, ClassUnreadable,
}

// readValue reduces every Agent-Sender value to the one the SDK reads. absent is true for no value at all. A non-empty
// class means the header is malformed before it is read.
func readValue(values []string) (value, class string, absent bool) {
	switch len(values) {
	case 0:
		return "", "", true
	case 1:
		if values[0] == "" {
			return "", ClassEmpty, false
		}
		return values[0], "", false
	}
	return "", ClassTwoHeaders, false
}

func message(subject string, payload []byte, value string) identity.Message {
	return identity.Message{Subject: subject, Data: payload, Header: nats.Header{sap.HeaderName: {value}}}
}

// verdict is one header's outcome.
type verdict struct {
	h     sap.SenderHeader
	trust sap.Trust
	class string // the malformed class, ClassNotVerified (forged only), or empty
}

func fromSender(s *identity.Sender, err error, value string) verdict {
	if err != nil {
		var e *identity.Error
		if !errors.As(err, &e) {
			return verdict{trust: sap.TrustMalformed, class: ClassUnreadable}
		}
		if e.Code == 400 {
			return verdict{trust: sap.TrustMalformed, class: malformedClass(value, e)}
		}
		// Any other refusal is a failed signature, subject, time or replay check: forged. The header parsed, or it
		// would be a 400.
		out := verdict{trust: sap.TrustForged, class: ClassNotVerified}
		if h, perr := identity.ParseHeader(value); perr == nil && h != nil {
			out.h = fromSDK(*h)
		}
		return out
	}
	switch identity.TrustOf(s) {
	case identity.TrustVerified:
		return verdict{h: fromSDK(s.Header), trust: sap.TrustVerified}
	case identity.TrustClaimed:
		return verdict{h: fromSDK(s.Header), trust: sap.TrustClaimed}
	}
	return verdict{trust: sap.TrustAbsent}
}

func stored(values []string, subject string, payload []byte) verdict {
	value, class, absent := readValue(values)
	if absent {
		return verdict{trust: sap.TrustAbsent}
	}
	if class != "" {
		return verdict{trust: sap.TrustMalformed, class: class}
	}
	s, err := identity.VerifyStored(message(subject, payload, value), identity.StoredOptions{})
	return fromSender(s, err, value)
}

// VerifyStored classifies a stored message (§13.8): signature and sub only. values is every Agent-Sender value of the
// message. A malformed header gives an empty SenderHeader; a forged one gives the header it claimed.
func VerifyStored(values []string, arrivalSubject string, payload []byte) (sap.SenderHeader, sap.Trust) {
	v := stored(values, arrivalSubject, payload)
	return v.h, v.trust
}

// Classify is the pure form of VerifyStored: the trust class and the refusal class of a stored message. The class is
// a malformed class (see the package doc) for a malformed header, ClassNotVerified for a forged one and empty for
// every other trust (verified, claimed, absent; this package never produces unknown-signer or stale). Claimed and
// absent mail keeps its trust kind and has no class: the caller decides which unverified mail it refuses and gives it
// the reason ClassNotVerified. It reads no file, writes no log, counts nothing and records no nonce.
func Classify(subject string, values []string, payload []byte) (sap.Trust, string) {
	v := stored(values, subject, payload)
	return v.trust, v.class
}

// malformedClass maps an SDK 400 to a class by the SDK's own Detail prefix, and looks at the value for the three the
// SDK says with one text (name, subject, time). The SDK's text itself is never kept.
func malformedClass(value string, e *identity.Error) string {
	d := e.Detail
	has := func(p string) bool { return strings.HasPrefix(d, p) }
	switch {
	case has("value exceeds"):
		return ClassHeaderTooLong
	case has("not a JSON object"):
		return ClassNotJSON
	case has("a member name is repeated"):
		return ClassFieldTwice
	case has("`v` is missing"):
		return ClassNoVersion
	case has("`v` must be a JSON number"):
		return ClassVersionType
	case has("`name` contains a lone surrogate"):
		return ClassBrokenChar
	case has("`name` is not a valid"):
		return nameClass(field(value, "name"))
	case has("`sub` is not a NATS subject"):
		return subjectClass(value)
	case has("`ts` is not"):
		if _, err := time.Parse(time.RFC3339Nano, field(value, "ts")); err == nil {
			return ClassTimeNotUTC
		}
		return ClassTimeInvalid
	case has("`nonce` must match"):
		return ClassNonce
	case has("`sig` is not base64url"):
		return ClassSigUnreadable
	case has("`sig` requires"):
		return ClassIncomplete
	case has("identity: invalid agent ID"), has("`account` is missing"), has("`user` is missing"):
		return ClassAccountUser
	case strings.HasSuffix(d, "` must be a string"):
		return ClassFieldType
	}
	return ClassUnreadable
}

// field is the string value of one top-level member of a header the SDK has already read as a JSON object.
func field(value, key string) string {
	var obj map[string]json.RawMessage
	if json.Unmarshal([]byte(value), &obj) != nil {
		return ""
	}
	var s string
	_ = json.Unmarshal(obj[key], &s)
	return s
}

func nameClass(name string) string {
	for _, r := range name {
		if r <= 0x1f || (r >= 0x7f && r <= 0x9f) || r == 0x2028 || r == 0x2029 {
			return ClassControlChar
		}
	}
	if len(utf16.Encode([]rune(name))) > identity.MaxNameLength {
		return ClassNameTooLong
	}
	return ClassUnreadable
}

func subjectClass(value string) string {
	sub := field(value, "sub")
	if sub == "" && field(value, "sig") != "" {
		return ClassIncomplete // mash read a signed header with an empty sub as incomplete
	}
	if strings.IndexFunc(sub, func(r rune) bool { return unicode.IsSpace(r) || r == 0xFEFF }) >= 0 {
		return ClassSpaceInSubject
	}
	return ClassUnreadable // an empty token or a control character: the SDK refuses it, not one of the ten
}
