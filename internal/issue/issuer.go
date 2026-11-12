package issue

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"time"

	"github.com/nats-io/jwt/v2"
	"github.com/nats-io/nkeys"

	"github.com/bogworks/mash/internal/listset"
	"github.com/bogworks/mash/internal/store"
)

// WranglerRegistry says which wrangler public keys are registered for a host and an owner group. How a wrangler's key
// comes to be registered is open (DEC-002), so the issue service only asks. A nil or empty result registers no one.
type WranglerRegistry interface {
	Lookup(host, group string) []string
}

// ListSetInForce is the list set the account runs under: its compiled form and the generation it has. The generation is
// 0 until the policy gate lands.
type ListSetInForce interface {
	InForce(ctx context.Context) (*listset.Compiled, int, error)
}

// Signer holds the owner groups' scoped signing keys. GroupKey returns the key of "range.owner"; the caller wipes it.
type Signer interface {
	GroupKey(group string) (nkeys.KeyPair, error)
}

// Record is the issue record as the service needs it.
type Record interface {
	// Lookup returns the record of publicKey, nil when there is none.
	Lookup(ctx context.Context, publicKey string) (*store.Issued, error)
	// Record writes the entry; an error means nothing may be issued.
	Record(ctx context.Context, r store.Issued) error
}

// MaxNonces caps the replay set; a service holding that many unexpired nonces refuses more (CodeBusy). A wrangler issues
// a few requests an hour, so the cap is never met by honest use.
const MaxNonces = 65536

// Issuer signs user JWTs for public keys. Its replay set lives in memory only: a restart forgets it. A replay after a
// restart is refused all the same once the request's expiry has passed, and the expiry is at most MaxWindow ahead of the
// moment the request was made, so a restart opens a replay window of at most MaxWindow.
type Issuer struct {
	Registry   WranglerRegistry
	ListSet    ListSetInForce
	Signer     Signer
	Record     Record
	AccountPub string
	Log        *slog.Logger
	// Now is the clock; nil means time.Now.
	Now func() time.Time

	mu     sync.Mutex // serialises the check of the record and its write, and guards nonces
	nonces map[string]time.Time
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
	if _, err := r.AddressParts(); err == nil {
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
	id, _ := r.AddressParts()
	group := id.Agent + "." + id.Owner

	if is.Registry == nil || !keyIn(r.Wrangler, is.Registry.Lookup(r.Host, group)) {
		return nil, refuse(CodeUnregistered, "that wrangler key is not registered for this host and owner group")
	}
	if !r.verifyProof() {
		return nil, refuse(CodeBadProof, "the proof does not verify over the request")
	}
	// The nonce is taken only now, for a request that proved itself, so a forged request cannot use up a real nonce.
	if rf := is.takeNonce(r.Nonce, exp, now); rf != nil {
		return nil, rf
	}

	compiled, gen, err := is.ListSet.InForce(ctx)
	if err != nil || compiled == nil {
		return nil, refuse(CodeNoListSet, "no list set is in force")
	}
	var user *listset.User
	for i := range compiled.Users {
		if compiled.Users[i].Sender == r.Address {
			user = &compiled.Users[i]
			break
		}
	}
	if user == nil {
		return nil, refuse(CodeUnlisted, "that address is not a sender in the list set in force")
	}

	// From here to the write, one issue at a time: two requests for one key must not both pass the binding check.
	is.mu.Lock()
	defer is.mu.Unlock()
	cur, err := is.Record.Lookup(ctx, r.PublicKey)
	if err != nil {
		return nil, refuse(CodeRecordFailed, "the issue record could not be read")
	}
	if cur != nil {
		if cur.RevokedAt != "" {
			return nil, refuse(CodeRevoked, "that public key has been revoked")
		}
		if cur.Address != r.Address {
			return nil, refuse(CodeBound, "that public key is already bound to another address")
		}
	}

	kp, err := is.Signer.GroupKey(group)
	if err != nil {
		return nil, refuse(CodeNoSigningKey, "the signing key of this owner group is not available")
	}
	defer kp.Wipe()
	pub, err := kp.PublicKey()
	if err != nil || !nkeys.IsValidPublicAccountKey(pub) || pub == is.AccountPub {
		// A user signed by the account identity key would carry no restrictions: never.
		return nil, refuse(CodeNoSigningKey, "the signing key of this owner group is not a scoped signing key")
	}

	uc := jwt.NewUserClaims(r.PublicKey)
	uc.Name = user.Name
	uc.IssuerAccount = is.AccountPub
	uc.UserPermissionLimits = jwt.UserPermissionLimits{} // the scoped key's template applies only to a user with none
	uc.Tags.Add(user.Tags...)
	token, err := uc.Encode(kp)
	if err != nil {
		return nil, refuse(CodeInternal, "signing failed")
	}
	rec := store.Issued{
		PublicKey: r.PublicKey, Address: r.Address, Owner: id.Owner, Zone: group, Kind: store.IssuedEndpoint,
		Tags: append([]string{}, user.Tags...), Generation: gen, Purpose: purpose,
		IssuedAt: time.Unix(uc.IssuedAt, 0).UTC().Format(time.RFC3339),
	}
	if err := is.Record.Record(ctx, rec); err != nil {
		return nil, refuse(CodeRecordFailed, "the issue could not be recorded, so nothing is issued")
	}
	return &Issued{JWT: token, Tags: rec.Tags, Record: rec}, nil
}

// takeNonce records nonce until exp and refuses one already held. Expired nonces are dropped as it goes.
func (is *Issuer) takeNonce(nonce string, exp, now time.Time) *Refusal {
	is.mu.Lock()
	defer is.mu.Unlock()
	if is.nonces == nil {
		is.nonces = map[string]time.Time{}
	}
	for n, e := range is.nonces {
		if !e.After(now) {
			delete(is.nonces, n)
		}
	}
	if _, seen := is.nonces[nonce]; seen {
		return refuse(CodeReplay, "that nonce has been used")
	}
	if len(is.nonces) >= MaxNonces {
		return refuse(CodeBusy, "too many requests in flight; try again later")
	}
	is.nonces[nonce] = exp
	return nil
}

// ErrNotConfigured is returned by Validate for an Issuer with a part missing.
var ErrNotConfigured = errors.New("issue: the issuer is missing a registry, list set, signer, record or account key")

// Check refuses an Issuer that cannot work.
func (is *Issuer) Check() error {
	if is.Registry == nil || is.ListSet == nil || is.Signer == nil || is.Record == nil || !nkeys.IsValidPublicAccountKey(is.AccountPub) {
		return ErrNotConfigured
	}
	return nil
}
