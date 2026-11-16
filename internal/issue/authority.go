package issue

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"github.com/nats-io/jwt/v2"
	"github.com/nats-io/nkeys"

	"github.com/bogworks/mash/internal/listset"
	"github.com/bogworks/mash/internal/store"
)

// Authority is the signing core of the issue path: everything after a request has been authenticated. Given an address, an
// endpoint, a user public key and a purpose it finds the sender in the list set in force and the owner group's template,
// checks that the group's scoped signing key is published in the account JWT with that template, refuses a revoked or
// bound key, signs a user JWT that carries no permissions of its own, and writes the issue record as a checked
// compare-and-set before it returns the JWT.
//
// It authenticates no one. The issue service (Issuer) calls it after its registry, proof, expiry and nonce steps; a caller
// that holds the group's signing key calls it directly, and holding that key is its trust. Both meet one list of refusals.
type Authority struct {
	ListSet ListSetInForce
	Signer  Signer
	Record  Record
	// Claims reads the account JWT the signing key is verified against (see ScopeTTL).
	Claims     AccountClaims
	AccountPub string
	Log        *slog.Logger
	// Now is the clock; nil means time.Now.
	Now func() time.Time

	// shared, when set, is the scope cache to use instead of this Authority's own: an Issuer that makes an Authority per
	// call keeps one cache across calls.
	shared *scopeCache
	scopes scopeCache
}

// ErrAuthorityNotConfigured is returned by Check for an Authority with a part missing.
var ErrAuthorityNotConfigured = errors.New("issue: the authority is missing a list set, signer, record, account claims or account key")

// Check refuses an Authority that cannot work.
func (a *Authority) Check() error {
	if a.ListSet == nil || a.Signer == nil || a.Record == nil || a.Claims == nil || !nkeys.IsValidPublicAccountKey(a.AccountPub) {
		return ErrAuthorityNotConfigured
	}
	return nil
}

func (a *Authority) now() time.Time {
	if a.Now != nil {
		return a.Now()
	}
	return time.Now()
}

func (a *Authority) log() *slog.Logger {
	if a.Log != nil {
		return a.Log
	}
	return slog.New(slog.DiscardHandler)
}

func (a *Authority) cache() *scopeCache {
	if a.shared != nil {
		return a.shared
	}
	return &a.scopes
}

// endpointTag is the endpoint tag of endpoint, refused when the endpoint is not in canonical form or its owner and name
// are not the address's.
func endpointTag(endpoint, address string) (string, error) {
	r := Request{Address: address, Endpoint: endpoint}
	id, err := r.EndpointIdentity()
	if err != nil {
		return "", err
	}
	return listset.EndpointTag(id, address)
}

// Sign signs a user JWT for publicKey at address and records it, or returns a *Refusal and signs and records nothing.
// purpose is store.PurposeIssue or store.PurposeRotation. The caller has authenticated whoever asks; Sign decides only
// what may be signed.
func (a *Authority) Sign(ctx context.Context, address, endpoint, publicKey, purpose string) (*Issued, *Refusal) {
	if purpose != store.PurposeIssue && purpose != store.PurposeRotation {
		return nil, refuse(CodeBadPurpose, "the purpose of an issue is issue or rotation")
	}
	r := Request{Address: address}
	owner, _, err := r.AddressParts()
	if err != nil {
		return nil, refuse(CodeMalformed, "address is not owner.name")
	}
	if !nkeys.IsValidPublicUserKey(publicKey) {
		return nil, refuse(CodeBadKey, "public_key is not a NATS user public key")
	}
	group := owner

	// The endpoint tag comes from mash's own function over the endpoint, never from a tag in the request, and is refused
	// when the endpoint's owner and name are not the address's: a credential cannot point at another address's inbox.
	epTag, err := endpointTag(endpoint, address)
	if err != nil {
		return nil, refuse(CodeEndpoint, "the endpoint is not at the address of the request")
	}

	compiled, gen, err := a.ListSet.InForce(ctx)
	if err != nil || compiled == nil {
		if err != nil {
			a.log().Warn("no list set in force", "reason", err.Error())
		}
		return nil, refuse(CodeNoListSet, "no list set is in force")
	}
	var user *listset.User
	for i := range compiled.Users {
		if compiled.Users[i].Sender == address {
			user = &compiled.Users[i]
			break
		}
	}
	if user == nil {
		return nil, refuse(CodeUnlisted, "that address is not a sender in the list set in force")
	}
	var tpl *listset.Template
	for i := range compiled.Templates {
		if compiled.Templates[i].Group == group {
			tpl = &compiled.Templates[i]
			break
		}
	}
	if tpl == nil {
		return nil, refuse(CodeTemplate, "the list set has no template for this owner group")
	}

	// An early refusal, so a revoked or bound key costs no signing. The record write below decides.
	cur, err := a.Record.Lookup(ctx, publicKey)
	if err != nil {
		return nil, refuse(CodeRecordFailed, "the issue record could not be read")
	}
	if rf := bindingRefusal(cur, address); rf != nil {
		return nil, rf
	}

	kp, err := a.Signer.GroupKey(group)
	if err != nil {
		return nil, refuse(CodeNoSigningKey, "the signing key of this owner group is not available")
	}
	defer kp.Wipe()
	pub, err := kp.PublicKey()
	if err != nil || !nkeys.IsValidPublicAccountKey(pub) || pub == a.AccountPub {
		// A user signed by the account identity key would carry no restrictions: never.
		return nil, refuse(CodeNoSigningKey, "the signing key of this owner group is not a scoped signing key")
	}
	// The key must be published in the account JWT as a scoped signer with this group's template: an unscoped key, or
	// one with another template, would let the user carry more than the list set allows.
	scopeCode, revoked := a.checkScope(ctx, group, pub, tpl.Permissions)
	switch scopeCode {
	case "":
	case CodeNoScope:
		return nil, refuse(CodeNoScope, "the signing key of this owner group is not published in the account JWT as a scoped signing key")
	case CodeTemplate:
		return nil, refuse(CodeTemplate, "the scoped signing key of this owner group has a template other than the list set's")
	default:
		return nil, refuse(CodeNoSigningKey, "the account JWT could not be read from every expected server, so the signing key could not be verified")
	}

	// A key the account JWT revokes is refused whatever its issue record says (it may have been revoked outside
	// mashctl, so the record has no revoked_at). Any entry refuses: a newer JWT must not restore the key's access.
	if _, ok := revoked[publicKey]; ok {
		return nil, refuse(CodeRevoked, "that public key has been revoked")
	}

	uc := jwt.NewUserClaims(publicKey)
	uc.Name = user.Name
	uc.IssuerAccount = a.AccountPub
	uc.UserPermissionLimits = jwt.UserPermissionLimits{} // the scoped key's template applies only to a user with none
	uc.Tags.Add(user.Tags...)
	uc.Tags.Add(epTag)
	token, err := uc.Encode(kp)
	if err != nil {
		return nil, refuse(CodeInternal, "signing failed")
	}
	rec := store.Issued{
		PublicKey: publicKey, Address: address, Owner: owner, Kind: store.IssuedEndpoint,
		Tags: append([]string{}, uc.Tags...), Generation: gen, Purpose: purpose,
		IssuedAt: time.Unix(uc.IssuedAt, 0).UTC().Format(time.RFC3339),
	}
	// The token leaves this function only if the checked write succeeded; on any error it is dropped here.
	if err := a.Record.RecordChecked(ctx, rec); err != nil {
		switch {
		case errors.Is(err, store.ErrKeyRevoked):
			return nil, refuse(CodeRevoked, "that public key has been revoked")
		case errors.Is(err, store.ErrKeyBoundElsewhere):
			return nil, refuse(CodeBound, "that public key is already bound to another address")
		}
		return nil, refuse(CodeRecordFailed, "the issue could not be recorded, so nothing is issued")
	}
	return &Issued{JWT: token, Tags: rec.Tags, Record: rec}, nil
}
