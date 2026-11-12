package apply

import (
	"context"
	"fmt"

	"github.com/nats-io/jwt/v2"

	"github.com/bogworks/mash/internal/ctlkeys"
)

// AppliedZonePolicy reports whether the account JWT the servers hold carries a zone policy (ADR 0091): any scoped
// signing key (jwt.UserScope), which is what `policy apply` publishes for each compiled zone, with its permission
// template (the subject_label and six-token mail lines). It is read-only and needs the system account view of the
// resolver, so it can be asked only where that exists (mashctl); an endpoint user cannot ask it.
//
// It reads the newest verified copy any server holds. A lookup that fails, or a copy that does not verify, is an error:
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
	set, _ := r.Servers(ctx)
	base, err := lookupBase(ctx, r, acctPub, trust, serverExpect(set, servers), "look up the account JWT")
	if err != nil {
		return false, err
	}
	return HasZoneScope(base.claims), nil
}

// HasZoneScope is true when claims lists any scoped signing key. A nil claims (no JWT) has none.
func HasZoneScope(claims *jwt.AccountClaims) bool {
	if claims == nil {
		return false
	}
	for k := range claims.SigningKeys {
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
