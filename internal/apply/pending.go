package apply

import (
	"context"
	"errors"
	"fmt"
	"sort"

	"github.com/nats-io/jwt/v2"

	"github.com/bogworks/mash/internal/ctlkeys"
)

// PendingRevocation is a user key that mash has revoked (the issue record or the retired-name bucket says so) and that
// the account JWT of Lacking answering servers does not list as revoked.
type PendingRevocation struct {
	Key     string
	Lacking int
}

// RevocationReport is what the servers hold of the revocations mash recorded (ADR 0142). Declared is the server count
// the answers are counted against, Answered how many servers answered the lookup and Silent how many did not (a silent
// server is pending: it cannot be shown to hold anything). Pending is every recorded key some answering server lacks,
// sorted by key. There is no storage of its own: it is derived from the keys it is given and the servers' copies, so it
// is empty again as soon as every server holds every revocation.
type RevocationReport struct {
	Declared, Answered, Silent int
	Keys                       int // how many recorded keys at least one answering server lists as revoked
	Pending                    []PendingRevocation
}

// Converged says every recorded revocation is held by every declared server.
func (r RevocationReport) Converged() bool {
	return len(r.Pending) == 0 && (r.Silent == 0 || r.Keys == 0)
}

// RevocationsPending compares the revocations of keys (user public keys the issue record or the retired-name bucket marks
// revoked) with the account JWT every server holds. servers is the declared count (0: not given). It changes nothing. A
// copy that does not verify against the operator is an error, never "converged"; a silent server is counted in Silent.
func RevocationsPending(ctx context.Context, r Resolver, ks *ctlkeys.Authority, servers int, keys []string) (RevocationReport, error) {
	var rep RevocationReport
	uniq := map[string]bool{}
	for _, k := range keys {
		if k != "" {
			uniq[k] = true
		}
	}
	rep.Keys = len(uniq)
	acctKP, acctPub, err := ks.Account()
	if err != nil {
		return rep, err
	}
	acctKP.Wipe()
	trust, err := LoadTrust(ks)
	if err != nil {
		return rep, err
	}
	set, serr := r.Servers(ctx)
	if errors.Is(serr, ErrServerSetRefused) {
		return rep, fmt.Errorf("refusing: %w", serr)
	}
	rep.Declared = max(set.need(servers), len(set.Names), 1)
	toks, err := r.LookupAll(ctx, acctPub, rep.Declared)
	if err != nil && len(toks) == 0 {
		return rep, fmt.Errorf("look up the account JWT: %w", err)
	}
	rep.Answered = len(toks)
	rep.Silent = max(rep.Declared-rep.Answered, 0)
	claims := map[string]*jwt.AccountClaims{}
	for _, t := range toks {
		if t == "" || claims[t] != nil {
			continue
		}
		c, verr := VerifyAccountJWT(t, acctPub, trust)
		if verr != nil {
			return rep, fmt.Errorf("refusing the resolver's account JWT: %w", verr)
		}
		claims[t] = c
	}
	sorted := make([]string, 0, len(uniq))
	for k := range uniq {
		sorted = append(sorted, k)
	}
	sort.Strings(sorted)
	rep.Keys = 0
	for _, k := range sorted {
		lacking := 0
		for _, t := range toks {
			if c := claims[t]; c == nil {
				lacking++ // a server with no JWT lists nothing
			} else if _, ok := c.Revocations[k]; !ok {
				lacking++
			}
		}
		if lacking == len(toks) {
			continue // no answering server lists it: it was never revoked here (retired with --no-revoke), so nothing is pending
		}
		rep.Keys++
		if lacking > 0 {
			rep.Pending = append(rep.Pending, PendingRevocation{Key: k, Lacking: lacking})
		}
	}
	return rep, nil
}
