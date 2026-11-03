package apply

import (
	"context"
	"errors"
	"fmt"
)

// KeyRevoked reports whether any server's copy of the account JWT revokes the user public key pub (at any time). It
// is a read, and it refuses to answer "not revoked" unless it has seen every server: the server set must be complete
// (as RevokeUsers requires), every expected server must answer the lookup, and every copy must verify against the
// operator trust. Servers may hold different copies, so one copy that lacks a revocation proves nothing; the key is
// revoked if any copy names it. A silent, unverifiable or incomplete set is an error, never false.
func KeyRevoked(ctx context.Context, p RevokeParams, pub string) (bool, error) {
	if p.Servers < 0 {
		return false, fmt.Errorf("revoked: MASH_SERVERS must be a positive number, got %d", p.Servers)
	}
	trust, err := LoadTrust(p.Keys)
	if err != nil {
		return false, fmt.Errorf("revoked: %w", err)
	}
	acctKP, acctPub, err := p.Keys.Account()
	if err != nil {
		return false, err
	}
	acctKP.Wipe()
	set, kerr := p.Resolver.Servers(ctx)
	expect := serverExpect(set, p.Servers)
	if errors.Is(kerr, ErrServerSetRefused) {
		return false, fmt.Errorf("revoked: refusing: %w", kerr)
	}
	if !set.Complete(p.Servers) {
		why := set.Why(p.Servers)
		if kerr != nil {
			why = kerr.Error()
		}
		return false, fmt.Errorf("revoked: refusing: %s", notCompleteRefusal(why))
	}
	toks, err := p.Resolver.LookupAll(ctx, acctPub, expect)
	if err != nil && len(toks) == 0 {
		return false, fmt.Errorf("revoked: look up the account JWT: %w", err)
	}
	if len(toks) < expect {
		return false, fmt.Errorf("revoked: refusing: %s", shortRefusal(fmt.Sprintf("only %d of %d servers answered the account lookup", len(toks), expect)))
	}
	revoked := false
	seen := map[string]bool{}
	for _, t := range toks {
		if t == "" {
			return false, errors.New("revoked: a server holds no account JWT, so its revocation list cannot be read")
		}
		if seen[t] {
			continue
		}
		seen[t] = true
		claims, err := VerifyAccountJWT(t, acctPub, trust)
		if err != nil {
			return false, fmt.Errorf("revoked: refusing the resolver's account JWT: %w", err)
		}
		if _, ok := claims.Revocations[pub]; ok {
			revoked = true
		}
	}
	return revoked, nil
}
