package apply

import (
	"context"
	"errors"
	"fmt"

	"github.com/nats-io/jwt/v2"
)

// accountClaims reads the account JWT from every server and returns each distinct verified copy. It is a read, and it
// refuses to answer unless it has seen every server: the server set must be complete (as RevokeUsers requires), every
// expected server must answer the lookup, every server must hold a JWT, and every copy must verify against the
// operator trust. A silent, unverifiable or incomplete set is an error.
func accountClaims(ctx context.Context, p RevokeParams) ([]*jwt.AccountClaims, error) {
	if p.Servers < 0 {
		return nil, fmt.Errorf("revoked: MASH_SERVERS must be a positive number, got %d", p.Servers)
	}
	trust, err := LoadTrust(p.Keys)
	if err != nil {
		return nil, fmt.Errorf("revoked: %w", err)
	}
	acctKP, acctPub, err := p.Keys.Account()
	if err != nil {
		return nil, err
	}
	acctKP.Wipe()
	set, kerr := p.Resolver.Servers(ctx)
	expect := serverExpect(set, p.Servers)
	if errors.Is(kerr, ErrServerSetRefused) {
		return nil, fmt.Errorf("revoked: refusing: %w", kerr)
	}
	if !set.Complete(p.Servers) {
		why := set.Why(p.Servers)
		if kerr != nil {
			why = kerr.Error()
		}
		return nil, fmt.Errorf("revoked: refusing: %s", notCompleteRefusal(why))
	}
	toks, err := p.Resolver.LookupAll(ctx, acctPub, expect)
	if err != nil && len(toks) == 0 {
		return nil, fmt.Errorf("revoked: look up the account JWT: %w", err)
	}
	if len(toks) < expect {
		return nil, fmt.Errorf("revoked: refusing: %s", shortRefusal(fmt.Sprintf("only %d of %d servers answered the account lookup", len(toks), expect)))
	}
	var out []*jwt.AccountClaims
	seen := map[string]bool{}
	for _, t := range toks {
		if t == "" {
			return nil, errors.New("revoked: a server holds no account JWT, so its copy cannot be read")
		}
		if seen[t] {
			continue
		}
		seen[t] = true
		claims, err := VerifyAccountJWT(t, acctPub, trust)
		if err != nil {
			return nil, fmt.Errorf("revoked: refusing the resolver's account JWT: %w", err)
		}
		out = append(out, claims)
	}
	return out, nil
}

// KeyRevoked reports whether any server's copy of the account JWT revokes the user public key pub (at any time). It
// refuses to answer "not revoked" unless it has seen every server (see accountClaims). Servers may hold different
// copies, so one copy that lacks a revocation proves nothing; the key is revoked if any copy names it.
func KeyRevoked(ctx context.Context, p RevokeParams, pub string) (bool, error) {
	copies, err := accountClaims(ctx, p)
	if err != nil {
		return false, err
	}
	for _, c := range copies {
		if _, ok := c.Revocations[pub]; ok {
			return true, nil
		}
	}
	return false, nil
}

// SignerPublished reports whether every server's copy of the account JWT lists signerPub as a scoped signing key
// whose role is role (the zone a new user JWT will be signed for). A user JWT signed by a key that some server's
// account JWT lacks cannot connect there. It is false if any copy lacks the key or scopes it to another role; a
// silent, unverifiable or incomplete server set is an error (see accountClaims).
func SignerPublished(ctx context.Context, p RevokeParams, signerPub, role string) (bool, error) {
	copies, err := accountClaims(ctx, p)
	if err != nil {
		return false, err
	}
	for _, c := range copies {
		sc, ok := c.SigningKeys.GetScope(signerPub)
		if !ok {
			return false, nil
		}
		us, _ := sc.(*jwt.UserScope)
		if us == nil || us.Role != role {
			return false, nil
		}
	}
	return true, nil
}
