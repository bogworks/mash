package apply

import (
	"context"
	"fmt"

	"github.com/nats-io/jwt/v2"

	"github.com/bogworks/mash/internal/ctlkeys"
)

// AppliedZonePolicy reports whether the account JWT the servers hold carries a zone policy (ADR 0091): any scoped
// signing key (jwt.UserScope), which is what `policy apply` publishes for each compiled zone, with its permission
// template (the zone label and six-token mail lines). It is read-only and needs the system account view of the
// resolver, so it can be asked only where that exists (mashctl); an endpoint user cannot ask it.
//
// It checks every distinct verified copy the servers hold, so an older copy with a zone scope counts. A lookup that fails, or a copy that does not verify, is an error:
// the caller must not treat "could not tell" as "no policy". An account with no JWT yet has no policy.
func AppliedZonePolicy(ctx context.Context, r Resolver, ks *ctlkeys.Authority, servers int) (bool, error) {
	acctKP, acctPub, err := ks.Account()
	if err != nil {
		return false, err
	}
	acctKP.Wipe()
	trust, err := LoadTrust(ks)
	if err != nil {
		return false, err
	}
	set, serr := r.Servers(ctx)
	if serr != nil {
		return false, fmt.Errorf("cannot confirm the server set: %w", serr)
	}
	toks, err := r.LookupAll(ctx, acctPub, serverExpect(set, servers))
	if err != nil && len(toks) == 0 {
		return false, fmt.Errorf("look up the account JWT: %w", err)
	}
	if expect := serverExpect(set, servers); expect > 0 && len(toks) < expect {
		return false, fmt.Errorf("not every server was checked: only %d of %d servers answered the account lookup", len(toks), expect)
	}
	groups, err := ks.GroupPublics()
	if err != nil {
		return false, err
	}
	groupKeys := map[string]bool{}
	for _, pub := range groups {
		groupKeys[pub] = true
	}
	// Every distinct copy is checked, not just the newest: a server still holding an older JWT with a zone scope is
	// a zone policy the upgrade must not pass over. A copy that does not verify is an error, never "no policy".
	seen := map[string]bool{}
	applied := false
	for _, t := range toks {
		if t == "" || seen[t] {
			continue
		}
		seen[t] = true
		claims, verr := VerifyAccountJWT(t, acctPub, trust)
		if verr != nil {
			return false, fmt.Errorf("refusing the resolver's account JWT: %w", verr)
		}
		if HasZoneScope(claims, groupKeys) {
			applied = true
		}
	}
	return applied, nil
}

// HasZoneScope is true when claims lists a scoped signing key that is not an owner group signer (groupKeys: the issue
// service's public keys, which are scoped but carry no zone policy). A nil claims (no JWT) has none.
func HasZoneScope(claims *jwt.AccountClaims, groupKeys map[string]bool) bool {
	if claims == nil {
		return false
	}
	for k := range claims.SigningKeys {
		if groupKeys[k] {
			continue
		}
		if sc, ok := claims.SigningKeys.GetScope(k); ok {
			if _, isUser := sc.(*jwt.UserScope); isUser {
				return true
			}
		}
	}
	return false
}

// RefuseZonePolicy is AppliedZonePolicy as a refusal: nil when none is applied, store's message when one is.
func RefuseZonePolicy(ctx context.Context, r Resolver, ks *ctlkeys.Authority, servers int, refusal error) error {
	applied, err := AppliedZonePolicy(ctx, r, ks, servers)
	if err != nil {
		return fmt.Errorf("cannot tell whether a zone policy is applied: %w", err)
	}
	if applied {
		return refusal
	}
	return nil
}
