package issue

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"time"

	"github.com/nats-io/nkeys"

	"github.com/bogworks/mash/internal/listset"
	"github.com/bogworks/mash/internal/store"
)

// RequesterRegistry says which requester public keys are registered for a host and an owner group. How a requester's key
// comes to be registered is open (DEC-002), so the issue service only asks. A nil or empty result registers no one.
type RequesterRegistry interface {
	Lookup(host, group string) []string
}

// ListSetInForce is the list set the account runs under: its compiled form and the generation it has. The generation is
// 0 until the policy gate lands.
type ListSetInForce interface {
	InForce(ctx context.Context) (*listset.Compiled, int, error)
}

// Signer holds the owner groups' scoped signing keys. GroupKey returns the key of the owner group (the owner); the caller wipes it.
type Signer interface {
	GroupKey(group string) (nkeys.KeyPair, error)
}

// Record is the issue record as the service needs it.
type Record interface {
	// Lookup returns the record of publicKey, nil when there is none.
	Lookup(ctx context.Context, publicKey string) (*store.Issued, error)
	// RecordChecked writes the entry as a compare-and-set that refuses, at the revision it writes at, a key whose
	// record has revoked_at set (store.ErrKeyRevoked) or is bound to another address (store.ErrKeyBoundElsewhere).
	// An error means nothing may be issued: the write is the arbiter, the Lookup before it only an early refusal.
	RecordChecked(ctx context.Context, r store.Issued) error
}

// MaxNonces caps the replay set; a service holding that many unexpired nonces refuses more (CodeBusy). A requester issues
// a few requests an hour, so the cap is never met by honest use.
const MaxNonces = 65536

// Issuer signs user JWTs for public keys. Its replay set lives in memory only: a restart forgets it. A replay after a
// restart is refused all the same once the request's expiry has passed, and the expiry is at most MaxWindow ahead of the
// moment the request was made, so a restart opens a replay window of at most MaxWindow.
type Issuer struct {
	Registry RequesterRegistry
	ListSet  ListSetInForce
	Signer   Signer
	Record   Record
	// Claims reads the account JWT the signing key is verified against (see ScopeTTL).
	Claims     AccountClaims
	AccountPub string
	// Directory is the seam Authority reads for a legacy endpoint (see Authority.Directory); nil refuses one.
	Directory Directory
	Log       *slog.Logger
	// Now is the clock; nil means time.Now.
	Now func() time.Time

	mu     sync.Mutex // guards nonces and nothing else: it is never held across signing or I/O
	nonces nonceSet
	scopes scopeCache
}

// Issued is a successful issue: the signed JWT and what mash recorded.
type Issued struct {
	JWT    string
	Tags   []string
	Record store.Issued
}

func (is *Issuer) now() time.Time {
	if is.Now != nil {
		return is.Now()
	}
	return time.Now()
}

func (is *Issuer) log() *slog.Logger {
	if is.Log != nil {
		return is.Log
	}
	return slog.New(slog.DiscardHandler)
}

// logRefusal writes one line: the code and the request's address, public key and host, each only when it has the shape of
// what it should be, so a seed sent in the wrong field cannot reach the log. It never holds a JWT.
func (is *Issuer) logRefusal(rf *Refusal, r Request, purpose string) {
	addr, key, host := "(invalid)", "(invalid)", "(invalid)"
	if _, _, err := r.AddressParts(); err == nil {
		addr = r.Address
	}
	if nkeys.IsValidPublicUserKey(r.PublicKey) {
		key = r.PublicKey
	}
	if loggableHost(r.Host) {
		host = r.Host
	}
	is.log().Warn("issue refused", "code", rf.Code, "address", addr, "public_key", key, "host", host, "purpose", purpose)
}

// loggableHost reports whether h is short, made of host-name characters only and not a NATS seed: a host is free text,
// and a seed put in it by mistake must not reach the log.
func loggableHost(h string) bool {
	if !validHost(h) || len(h) > 64 {
		return false
	}
	for _, c := range h {
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '.' || c == '-' || c == '_') {
			return false
		}
	}
	if kp, err := nkeys.FromSeed([]byte(h)); err == nil {
		kp.Wipe()
		return false
	}
	return true
}

// Issue signs a user JWT for r.PublicKey and records it, or returns a *Refusal (or an error for a failure that is not a
// refusal of the request) and signs and records nothing. purpose is store.PurposeIssue for the NATS service and
// store.PurposeRotation for the rotation code of REQ-014.
func (is *Issuer) Issue(ctx context.Context, r Request, purpose string) (*Issued, error) {
	out, rf := is.issue(ctx, r, purpose)
	if rf != nil {
		is.logRefusal(rf, r, purpose)
		return nil, rf
	}
	return out, nil
}

func (is *Issuer) issue(ctx context.Context, r Request, purpose string) (*Issued, *Refusal) {
	if purpose != store.PurposeIssue && purpose != store.PurposeRotation {
		return nil, refuse(CodeBadPurpose, "the purpose of an issue is issue or rotation")
	}
	if rf := r.Validate(); rf != nil {
		return nil, rf
	}
	now := is.now()
	exp := time.Unix(r.Expires, 0)
	if !exp.After(now) {
		return nil, refuse(CodeExpired, "the request has expired")
	}
	if exp.After(now.Add(MaxWindow)) {
		return nil, refuse(CodeWindow, "the request expires more than %s ahead", MaxWindow)
	}

	owner, _, _ := r.AddressParts()
	group := owner

	if is.Registry == nil || !keyIn(r.Requester, is.Registry.Lookup(r.Host, group)) {
		return nil, refuse(CodeUnregistered, "that requester key is not registered for this host and owner group")
	}
	if !r.verifyProof() {
		return nil, refuse(CodeBadProof, "the proof does not verify over the request")
	}
	// The endpoint tag comes from mash's own function over the endpoint the request names, never from a tag in the
	// request, and is refused when the endpoint's owner and name are not the address's: a credential cannot point at
	// another address's inbox. Nothing is signed or reserved before this. Sign makes the same check again; it is made
	// here too so that this refusal still comes before the nonce is taken.
	if _, err := endpointTag(r.Endpoint, r.Address); err != nil {
		return nil, refuse(CodeEndpoint, "the endpoint is not at the address of the request")
	}
	// The nonce is reserved only now, for a request that proved itself, so a forged request cannot use one up, and so a
	// duplicate that arrives meanwhile is refused. It stays reserved only if a credential is returned: every refusal
	// below releases it, so a corrected request with the same nonce (the proof covers it) is not a replay.
	if rf := is.takeNonce(r.Nonce, exp, now); rf != nil {
		return nil, rf
	}
	issued := false
	defer func() {
		if !issued {
			is.releaseNonce(r.Nonce)
		}
	}()

	out, rf := is.authority().Sign(ctx, r.Address, r.Endpoint, r.PublicKey, purpose)
	if rf != nil {
		return nil, rf
	}
	issued = true
	return out, nil
}

// authority is the signing core of this issuer over its own parts and its own scope cache. It is made per call so a
// part a caller sets later is the one used.
func (is *Issuer) authority() *Authority {
	return &Authority{ListSet: is.ListSet, Signer: is.Signer, Record: is.Record, Claims: is.Claims, AccountPub: is.AccountPub,
		Directory: is.Directory, Log: is.Log, Now: is.Now, shared: &is.scopes}
}

// bindingRefusal is the refusal for a key whose record cur is revoked or bound to another address than address.
func bindingRefusal(cur *store.Issued, address string) *Refusal {
	if cur == nil {
		return nil
	}
	if cur.RevokedAt != "" {
		return refuse(CodeRevoked, "that public key has been revoked")
	}
	if cur.Address != address {
		return refuse(CodeBound, "that public key is already bound to another address")
	}
	return nil
}

// takeNonce reserves nonce until exp and refuses one already held or a full set (see nonceSet).
func (is *Issuer) takeNonce(nonce string, exp, now time.Time) *Refusal {
	is.mu.Lock()
	defer is.mu.Unlock()
	switch is.nonces.take(nonce, exp, now) {
	case CodeReplay:
		return refuse(CodeReplay, "that nonce has been used")
	case CodeBusy:
		return refuse(CodeBusy, "too many requests in flight; try again later")
	}
	return nil
}

func (is *Issuer) releaseNonce(nonce string) {
	is.mu.Lock()
	defer is.mu.Unlock()
	is.nonces.release(nonce)
}

// ErrNotConfigured is returned by Validate for an Issuer with a part missing.
var ErrNotConfigured = errors.New("issue: the issuer is missing a registry, list set, signer, record, account claims or account key")

// Check refuses an Issuer that cannot work.
func (is *Issuer) Check() error {
	if is.Registry == nil || is.ListSet == nil || is.Signer == nil || is.Record == nil || is.Claims == nil || !nkeys.IsValidPublicAccountKey(is.AccountPub) {
		return ErrNotConfigured
	}
	return nil
}
