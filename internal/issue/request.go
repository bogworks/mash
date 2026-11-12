// Package issue is the issue service (REQ-013, ADR 0104): it takes a request that holds a sender's PUBLIC key and the
// address the key is for, countersigned by the wrangler on the sender's host, and returns a user JWT for that key,
// signed with the owner group's scoped signing key and carrying the tags mash's own table gives that address.
//
// No private key is made, received, stored or logged on this path. Nothing in a request except the address, the public
// key and the proof decides what is signed: tags, permissions, limits, a name and an expiry in a request are ignored.
package issue

import (
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/nats-io/nkeys"

	"github.com/bogworks/mash/internal/sap"
)

// RequestVersion is the only version of the request this service accepts.
const RequestVersion = 1

// ProofPrefix opens the canonical bytes the wrangler signs; it names the format so a signature made for another
// purpose cannot be taken for an issue proof.
const ProofPrefix = "mash-issue-v1"

const (
	// MaxRequestBytes is the largest request the service reads. A request is about 500 bytes.
	MaxRequestBytes = 4096
	// MaxWindow is how far ahead of the service's clock a request's expiry may lie. It also bounds how long a nonce is
	// held, so it bounds the replay set.
	MaxWindow = 5 * time.Minute
	// MinNonceBytes and MaxNonceBytes bound the decoded nonce.
	MinNonceBytes = 16
	MaxNonceBytes = 64
	// MaxHostLen is the longest host name accepted.
	MaxHostLen = 253
)

// Request is an issue request. Its JSON object may carry other fields (tags, permissions, ...): they are not decoded
// and never read.
type Request struct {
	Version   int    `json:"version"`
	Address   string `json:"address"`    // range.owner.name
	PublicKey string `json:"public_key"` // a NATS user public key; never a seed
	Host      string `json:"host"`       // the name the wrangler is registered under
	Nonce     string `json:"nonce"`      // base64url, at least MinNonceBytes random bytes
	Expires   int64  `json:"expires"`    // unix seconds
	Proof     string `json:"proof"`      // base64url ed25519 signature by the wrangler over CanonicalBytes
	Wrangler  string `json:"wrangler"`   // the wrangler's public key
}

// Response is the reply: a JWT, or an error and its stable code.
type Response struct {
	JWT   string `json:"jwt,omitempty"`
	Error string `json:"error,omitempty"`
	Code  string `json:"code,omitempty"`
}

// The stable refusal codes.
const (
	CodeMalformed    = "malformed_request"
	CodeTooLarge     = "request_too_large"
	CodeBadKey       = "invalid_public_key"
	CodeExpired      = "expired"
	CodeWindow       = "expiry_too_far"
	CodeReplay       = "replayed_nonce"
	CodeUnregistered = "wrangler_not_registered"
	CodeBadProof     = "bad_proof"
	CodeUnlisted     = "address_not_listed"
	CodeRevoked      = "key_revoked"
	CodeBound        = "key_bound_elsewhere"
	CodeNoSigningKey = "signing_key_unavailable"
	CodeNoListSet    = "no_list_set"
	CodeRecordFailed = "record_failed"
	CodeSubject      = "address_subject_mismatch"
	CodeBusy         = "busy"
	CodeInternal     = "internal"
	CodeBadPurpose   = "invalid_purpose"
	CodeNoRegistry   = "no_registry"
)

// Refusal is the error Issue returns when it declines to sign. Its Msg is safe to send back to the requester.
type Refusal struct{ Code, Msg string }

func (r *Refusal) Error() string { return r.Code + ": " + r.Msg }

func refuse(code, format string, a ...any) *Refusal {
	return &Refusal{Code: code, Msg: fmt.Sprintf(format, a...)}
}

// CanonicalBytes are the bytes the wrangler signs:
//
//	mash-issue-v1\n<address>\n<public_key>\n<host>\n<nonce>\n<expires>
//
// with the address, public key, host and nonce as the request carries them and expires in decimal. The address is
// inside the signed bytes, so a proof made for one address cannot be used for another. Validate has already refused a
// newline in any field.
func CanonicalBytes(address, publicKey, host, nonce string, expires int64) []byte {
	return []byte(ProofPrefix + "\n" + address + "\n" + publicKey + "\n" + host + "\n" + nonce + "\n" + strconv.FormatInt(expires, 10))
}

// Canonical is CanonicalBytes of r.
func (r Request) Canonical() []byte {
	return CanonicalBytes(r.Address, r.PublicKey, r.Host, r.Nonce, r.Expires)
}

// Parse decodes a request, refusing one over MaxRequestBytes, one that is not a JSON object, and one with a field
// that is not shaped as the format says. Fields it does not know are dropped unread.
func Parse(data []byte) (Request, *Refusal) {
	var r Request
	if len(data) > MaxRequestBytes {
		return r, refuse(CodeTooLarge, "the request is larger than %d bytes", MaxRequestBytes)
	}
	if err := json.Unmarshal(data, &r); err != nil {
		return Request{}, refuse(CodeMalformed, "the request is not a JSON object of the issue format")
	}
	if rf := r.Validate(); rf != nil {
		return Request{}, rf
	}
	return r, nil
}

// Validate checks the shape of every field that is used. It does not check the proof, the time or any table.
func (r Request) Validate() *Refusal {
	if r.Version != RequestVersion {
		return refuse(CodeMalformed, "version must be %d", RequestVersion)
	}
	if _, err := r.AddressParts(); err != nil {
		return refuse(CodeMalformed, "address is not range.owner.name")
	}
	if !nkeys.IsValidPublicUserKey(r.PublicKey) {
		return refuse(CodeBadKey, "public_key is not a NATS user public key")
	}
	if !nkeys.IsValidPublicUserKey(r.Wrangler) {
		return refuse(CodeMalformed, "wrangler is not a NATS user public key")
	}
	if !validHost(r.Host) {
		return refuse(CodeMalformed, "host is empty, too long or has a space or control character")
	}
	nb, err := base64.RawURLEncoding.DecodeString(r.Nonce)
	if err != nil || len(nb) < MinNonceBytes || len(nb) > MaxNonceBytes {
		return refuse(CodeMalformed, "nonce must be %d to %d random bytes, base64url without padding", MinNonceBytes, MaxNonceBytes)
	}
	if r.Expires <= 0 {
		return refuse(CodeMalformed, "expires must be a unix time in seconds")
	}
	if pb, err := base64.RawURLEncoding.DecodeString(r.Proof); err != nil || len(pb) != 64 {
		return refuse(CodeMalformed, "proof must be a 64-byte ed25519 signature, base64url without padding")
	}
	return nil
}

func validHost(h string) bool {
	if h == "" || len(h) > MaxHostLen {
		return false
	}
	for _, c := range h {
		if c <= ' ' || c == 0x7f || c > '~' {
			return false
		}
	}
	return true
}

// AddressParts is the range, owner and name of the request's address.
func (r Request) AddressParts() (sap.EndpointIdentity, error) {
	id, err := sap.ParseCanonical(r.Address)
	if err != nil {
		return id, err
	}
	if strings.Join([]string{id.Agent, id.Owner, id.Name}, ".") != r.Address {
		return id, errors.New("address is not in canonical form")
	}
	return id, nil
}

// Group is "range.owner" of the request's address; "" when the address does not parse.
func (r Request) Group() string {
	id, err := r.AddressParts()
	if err != nil {
		return ""
	}
	return id.Agent + "." + id.Owner
}

// verifyProof reports whether the request's proof is the wrangler's signature over the canonical bytes.
func (r Request) verifyProof() bool {
	kp, err := nkeys.FromPublicKey(r.Wrangler)
	if err != nil {
		return false
	}
	sig, err := base64.RawURLEncoding.DecodeString(r.Proof)
	if err != nil {
		return false
	}
	return kp.Verify(r.Canonical(), sig) == nil
}

// SignProof is the wrangler side: the proof of a request, signed with the wrangler's key pair. It is here so tests
// and the wrangler's own code share one definition of the canonical bytes.
func SignProof(wrangler nkeys.KeyPair, address, publicKey, host, nonce string, expires int64) (string, error) {
	sig, err := wrangler.Sign(CanonicalBytes(address, publicKey, host, nonce, expires))
	if err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(sig), nil
}

// keyIn reports whether key is one of keys, comparing in constant time.
func keyIn(key string, keys []string) bool {
	found := false
	for _, k := range keys {
		if subtle.ConstantTimeCompare([]byte(k), []byte(key)) == 1 {
			found = true
		}
	}
	return found
}
