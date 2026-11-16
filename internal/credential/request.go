// Package credential is the primitive "public key up, credential down", for a caller that holds the account's keys: it
// takes the public key an endpoint made for itself and an address, and returns a user JWT signed with the owner group's
// scoped signing key, with the issue record written first. No private key is made, received, stored or logged on this
// path, and no outside system, requester, registry, proof or nonce is needed: holding the owner group's signing key is
// the trust. The signing steps are issue.Authority.Sign, the same ones the issue service takes after it has
// authenticated a request, so a person and a service meet one list of refusals.
//
// The endpoint side is two pure helpers: NewKey makes a user key, and Assemble makes the contents of a creds file from
// the returned JWT and the endpoint's own seed.
//
// Nothing here is a command. Request and Result are plain data that marshal as JSON.
package credential

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
	"unicode"

	"github.com/nats-io/nkeys"

	"github.com/bogworks/mash/internal/issue"
	"github.com/bogworks/mash/internal/store"
)

// The purposes of a request.
const (
	PurposeIssue    = store.PurposeIssue
	PurposeRotation = store.PurposeRotation
)

// CodeSeed is the refusal code of a request in which a private key (a NATS seed) appears, in any field.
const CodeSeed = "seed_in_request"

// MaxRequestBytes is the largest request Parse reads.
const MaxRequestBytes = 4096

// Request asks for a credential for a public key.
type Request struct {
	// Address is owner.name: the address the credential is for. The owner is the owner group.
	Address string `json:"address"`
	// Endpoint is agent.owner.name, the endpoint the credential is for; its owner and name are the address's.
	Endpoint string `json:"endpoint"`
	// PublicKey is a NATS user public key; never a seed.
	PublicKey string `json:"public_key"`
	// Purpose is "issue" (the default when empty) or "rotation".
	Purpose string `json:"purpose,omitempty"`
}

// Refusal says why a request was refused: a stable code (the issue service's vocabulary, and CodeSeed) and a sentence
// that is safe to show.
type Refusal struct {
	Code   string `json:"code"`
	Detail string `json:"detail"`
}

func (r *Refusal) Error() string { return r.Code + ": " + r.Detail }

func refuse(code, format string, a ...any) *Refusal {
	return &Refusal{Code: code, Detail: fmt.Sprintf(format, a...)}
}

// LooksLikeSeed reports whether s is, or holds as a whitespace- or punctuation-separated word, a NATS seed of any kind.
// It is how a private key put in the wrong field is recognised so that it is refused and never echoed.
func LooksLikeSeed(s string) bool {
	for _, tok := range strings.FieldsFunc(s, func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsDigit(r) }) {
		if len(tok) < 50 || len(tok) > 64 {
			continue
		}
		if _, raw, err := nkeys.DecodeSeed([]byte(tok)); err == nil {
			clear(raw)
			return true
		}
	}
	return false
}

// EffectivePurpose is Purpose, or "issue" when it is empty.
func (r Request) EffectivePurpose() string {
	if r.Purpose == "" {
		return PurposeIssue
	}
	return r.Purpose
}

// AddressParts is the owner and name of the address; an error when it is not owner.name.
func (r Request) AddressParts() (owner, name string, err error) {
	return issue.Request{Address: r.Address}.AddressParts()
}

// Validate checks the shape of every field. A seed in any field is refused first, whatever else is wrong, and the
// refusal never carries the field's text.
func (r Request) Validate() *Refusal {
	for _, f := range []string{r.Address, r.Endpoint, r.PublicKey, r.Purpose} {
		if LooksLikeSeed(f) {
			return refuse(CodeSeed, "a private key (seed) is in the request: send only the public key; the key you put there should be treated as exposed")
		}
	}
	switch r.EffectivePurpose() {
	case PurposeIssue, PurposeRotation:
	default:
		return refuse(issue.CodeBadPurpose, "purpose is issue or rotation")
	}
	if _, _, err := r.AddressParts(); err != nil {
		return refuse(issue.CodeMalformed, "address is not owner.name")
	}
	if _, err := (issue.Request{Address: r.Address, Endpoint: r.Endpoint}).EndpointIdentity(); err != nil {
		return refuse(issue.CodeMalformed, "endpoint is not agent.owner.name")
	}
	if !nkeys.IsValidPublicUserKey(r.PublicKey) {
		return refuse(issue.CodeBadKey, "public_key is not a NATS user public key")
	}
	return nil
}

// Parse decodes a request from JSON. A seed anywhere in the bytes, in a known field or not, is refused before anything
// is decoded; an unknown field, a value that is not a string, a second JSON value or more than MaxRequestBytes is
// malformed. The refusal never carries the request's text.
func Parse(data []byte) (Request, *Refusal) {
	if len(data) > MaxRequestBytes {
		return Request{}, refuse(issue.CodeTooLarge, "the request is larger than %d bytes", MaxRequestBytes)
	}
	if LooksLikeSeed(string(data)) {
		return Request{}, refuse(CodeSeed, "a private key (seed) is in the request: send only the public key; the key you put there should be treated as exposed")
	}
	var r Request
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&r); err != nil || dec.More() {
		return Request{}, refuse(issue.CodeMalformed, "the request is not a JSON object with address, endpoint, public_key and purpose")
	}
	if rf := r.Validate(); rf != nil {
		return Request{}, rf
	}
	return r, nil
}

// shaped returns s only when it has the shape of what a field should be, so a seed or free text put in a field cannot
// reach a result or a log.
func shaped(s string, ok bool) string {
	if ok {
		return s
	}
	return ""
}
