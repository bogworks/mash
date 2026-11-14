package issue

import (
	"context"
	"encoding/json"
	"sort"
	"sync"
	"time"

	"github.com/nats-io/jwt/v2"
)

// AccountClaims fetches the account's current JWT claims, verified: the issue service asks it which signing keys the
// account publishes and with what template.
type AccountClaims interface {
	Fetch(ctx context.Context) (*jwt.AccountClaims, error)
}

// ScopeTTL is how long a verdict on an owner group's signing key is served without asking the account JWT again. A
// change of the account JWT (the key unpublished, made unscoped, or given another template) is therefore seen by the
// service within ScopeTTL at the latest; a verdict is also dropped at once when the list set's template for the group
// or the key itself changes, because both are part of what it was made for.
const ScopeTTL = 3 * time.Second

type scopeVerdict struct {
	pub, want string
	code      string             // "" when the key is published as a scoped signer with exactly the want template
	revoked   jwt.RevocationList // the account JWT's revocations when the verdict was made
	at        time.Time
}

// scopeCache holds one verdict per owner group.
type scopeCache struct {
	mu sync.Mutex
	m  map[string]scopeVerdict
}

// templateKey is a canonical form of a template: permission lists sorted, so equal sets compare equal.
func templateKey(t jwt.UserPermissionLimits) string {
	sortList := func(l jwt.StringList) jwt.StringList {
		out := append(jwt.StringList(nil), l...)
		sort.Strings(out)
		return out
	}
	t.Pub.Allow, t.Pub.Deny = sortList(t.Pub.Allow), sortList(t.Pub.Deny)
	t.Sub.Allow, t.Sub.Deny = sortList(t.Sub.Allow), sortList(t.Sub.Deny)
	b, err := json.Marshal(t)
	if err != nil {
		return "?"
	}
	return string(b)
}

// wantTemplate is the scoped signer template a compiled list set's group permissions make: what policy apply publishes
// (jwt.NewUserScope's defaults with the permissions set).
func wantTemplate(p jwt.Permissions) jwt.UserPermissionLimits {
	t := jwt.NewUserScope().Template
	t.Permissions = p
	return t
}

// checkScope verifies, before anything is signed, that pub is published in the account JWT as a scoped signer whose
// template equals want. It returns CodeNoScope (absent, or published unscoped), CodeTemplate (scoped with another
// template) or CodeNoSigningKey (the account JWT could not be read from every expected server), "" when the key is fit to sign with.
func (is *Issuer) checkScope(ctx context.Context, group, pub string, want jwt.Permissions) (string, jwt.RevocationList) {
	wk := templateKey(wantTemplate(want))
	now := is.now()
	c := &is.scopes
	c.mu.Lock()
	v, ok := c.m[group]
	c.mu.Unlock()
	if ok && v.pub == pub && v.want == wk && now.Sub(v.at) < ScopeTTL && !now.Before(v.at) {
		return v.code, v.revoked
	}
	var code string
	var revoked jwt.RevocationList
	claims, err := is.Claims.Fetch(ctx)
	switch {
	case err != nil || claims == nil:
		// Not cached: the next request asks again. An error is a refusal, never a verdict from the answers that came.
		if err != nil {
			is.log().Warn("account JWT not available", "reason", err.Error())
		}
		return CodeNoSigningKey, nil
	default:
		revoked = make(jwt.RevocationList, len(claims.Revocations))
		for k, t := range claims.Revocations {
			revoked[k] = t
		}
		sc, found := claims.SigningKeys.GetScope(pub)
		us, scoped := sc.(*jwt.UserScope)
		switch {
		case !found || !scoped || us == nil:
			code = CodeNoScope
		case templateKey(us.Template) != wk:
			code = CodeTemplate
		}
	}
	c.mu.Lock()
	if c.m == nil {
		c.m = map[string]scopeVerdict{}
	}
	c.m[group] = scopeVerdict{pub: pub, want: wk, code: code, revoked: revoked, at: now}
	c.mu.Unlock()
	return code, revoked
}
