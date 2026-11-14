package apply

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/nats-io/jwt/v2"
	"github.com/nats-io/nkeys"

	"github.com/bogworks/mash/internal/ctlkeys"
)

// ErrNotConfirmed wraps every failure after the revocation was pushed: the update may be on some servers or all of
// them, but it was not confirmed on every one. Running the revoke again is safe.
var ErrNotConfirmed = errors.New("the revocation was pushed but not confirmed")

// RevokeParams is one revocation of user keys in the account JWT.
type RevokeParams struct {
	Keys     *ctlkeys.Authority
	Resolver Resolver
	// Servers is the number of servers in the deployment (--servers); 0 means not given.
	Servers int
	// LockWait is how long to wait for the key directory lock (default 5s).
	LockWait time.Duration
	// VerifyTries and VerifyWait bound the read-back after the push (defaults 5 and 200ms).
	VerifyTries int
	VerifyWait  time.Duration
	// Now is the revocation time; nil means time.Now. NATS refuses a user JWT issued at or before it.
	Now func() time.Time
}

// RevokeResult says what a RevokeUsers call did.
type RevokeResult struct {
	// Revoked are the keys whose revocation this call set or raised (Raised is the subset that had an older one);
	// Already are the keys whose existing revocation already covers now (left as it was).
	Revoked, Already, Raised []string
	// Acked is how many servers acknowledged the update (0 when nothing was pushed).
	Acked int
	// Complete says the acknowledging servers are the whole server set (always true when nothing was pushed).
	Complete bool
	// Warning is a neutral line when some servers held an older copy of the account JWT than the one the revocation
	// was built from ("" when all agreed).
	Warning string
}

// RevokeUsers adds a revocation for each user public key to the account JWT, re-signs it with the operator key and
// pushes it to every known server. A key whose revocation is older than now is raised to now (a JWT issued after the
// old time would otherwise stay valid); one that already covers now is left as it is. A revocation is never lowered or
// removed. Every key is validated before any I/O.
func RevokeUsers(ctx context.Context, p RevokeParams, users []string) (RevokeResult, error) {
	var res RevokeResult
	if len(users) == 0 {
		return res, errors.New("revoke: no user keys given")
	}
	for _, u := range users {
		if !nkeys.IsValidPublicUserKey(u) {
			return res, fmt.Errorf("revoke: %q is not a NATS user public key (U, then 55 upper-case base32 characters)", u)
		}
	}
	if p.Servers < 0 {
		return res, fmt.Errorf("revoke: MASH_SERVERS must be a positive number, got %d", p.Servers)
	}
	now := p.Now
	if now == nil {
		now = time.Now
	}
	wait := p.LockWait
	if wait <= 0 {
		wait = defaultLockWait
	}
	unlock, err := p.Keys.Lock(wait)
	if err != nil {
		return res, err
	}
	defer unlock()
	trust, err := LoadTrust(p.Keys)
	if err != nil {
		return res, fmt.Errorf("revoke: refusing: %w (nothing was changed)", err)
	}
	opKP, _, err := p.Keys.Operator()
	if err != nil {
		return res, err
	}
	defer opKP.Wipe()
	acctKP, acctPub, err := p.Keys.Account()
	if err != nil {
		return res, err
	}
	acctKP.Wipe()
	known, err := knownSigningKeys(p.Keys, acctPub)
	if err != nil {
		return res, fmt.Errorf("revoke: %w (nothing was changed)", err)
	}

	set, kerr := p.Resolver.Servers(ctx)
	expect := serverExpect(set, p.Servers)
	if errors.Is(kerr, ErrServerSetRefused) {
		return res, fmt.Errorf("revoke: refusing: %w (nothing was changed)", kerr)
	}
	if !set.Complete(p.Servers) {
		why := set.Why(p.Servers)
		if kerr != nil {
			why = kerr.Error()
		}
		return res, fmt.Errorf("revoke: refusing: %s (nothing was changed)", notCompleteRefusal(why))
	}

	var claims *jwt.AccountClaims
	var token string
	var want map[string]int64 // the least revocation time each key must show on read-back
	for attempt := 0; ; attempt++ {
		base, err := lookupBase(ctx, p.Resolver, acctPub, trust, expect, "look up the account JWT")
		if err != nil {
			return res, fmt.Errorf("revoke: %w (nothing was changed)", err)
		}
		if base.short != "" {
			return res, fmt.Errorf("revoke: refusing: %s (nothing was changed)", shortRefusal(base.short))
		}
		cur := base.token
		claims = base.claims
		res.Warning = base.warn
		if cur == "" {
			return res, fmt.Errorf("revoke: the resolver has no JWT for the MASH account %s: is the account key in the key store the account of this server? (nothing was changed)", acctPub)
		}
		var unknown []string
		for _, k := range sortedKeys(claims.SigningKeys) {
			if known[k] {
				continue
			}
			u := unknownKey{key: k}
			if sc, _ := claims.SigningKeys.GetScope(k); sc != nil {
				if us, ok := sc.(*jwt.UserScope); ok && us != nil {
					u.role = us.Role
				}
			}
			unknown = append(unknown, u.describe())
		}
		if len(unknown) > 0 {
			return res, fmt.Errorf("revoke: refusing: the account JWT carries signing keys mashctl did not create: %s. %s Remove it, then revoke again (nothing was changed)", strings.Join(unknown, "; "), unknownKeyNote)
		}
		res.Revoked, res.Already, res.Raised = nil, nil, nil
		want = map[string]int64{}
		stamp := now()
		if claims.Revocations == nil {
			claims.Revocations = jwt.RevocationList{}
		}
		for _, u := range dedupe(users) {
			old, has := claims.Revocations[u]
			if has && old >= stamp.Unix() {
				res.Already = append(res.Already, u)
				want[u] = old
				continue
			}
			claims.Revocations.Revoke(u, stamp)
			res.Revoked = append(res.Revoked, u)
			want[u] = stamp.Unix()
			if has {
				res.Raised = append(res.Raised, u)
			}
		}
		if len(res.Revoked) == 0 {
			// Every key is revoked on the resolver, but an earlier push may have reached only some servers: send the
			// current token again, unchanged (no re-sign), and confirm it like a new revocation.
			token = cur
		} else {
			token, err = claims.Encode(opKP)
			if err != nil {
				return res, fmt.Errorf("revoke: encode the account JWT: %w (nothing was changed)", err)
			}
		}
		// Someone else may have pushed since the read: compute again from what is there now, once.
		again, err := lookupBase(ctx, p.Resolver, acctPub, trust, expect, "look up the account JWT before the push")
		if err != nil {
			return res, fmt.Errorf("revoke: %w (nothing was pushed)", err)
		}
		if again.short != "" {
			return res, fmt.Errorf("revoke: refusing: %s (nothing was pushed)", shortRefusal(again.short))
		}
		if again.token == cur {
			break
		}
		if attempt == 1 {
			return res, errors.New("revoke: the account JWT changed twice while the revocation was computed: something else is changing it; run the command again (nothing was pushed)")
		}
	}

	acks, why := pushAndCheck(ctx, p.Resolver, token, set.Names)
	res.Acked = len(acks)
	if why != "" {
		return res, fmt.Errorf("revoke: %w: the update was not acknowledged: %s; running the revoke again is safe", ErrNotConfirmed, why)
	}
	confirm := map[string]int64{}
	for _, u := range res.Revoked {
		confirm[u] = want[u]
	}
	if len(confirm) == 0 {
		confirm = want
	}
	if msg := verifyRevoked(ctx, p, acctPub, trust, token, expect, confirm); msg != "" {
		return res, fmt.Errorf("revoke: %w: %d server(s) acknowledged the update but the read-back %s; running the revoke again is safe", ErrNotConfirmed, len(acks), msg)
	}
	res.Complete = set.Complete(p.Servers)
	if !res.Complete {
		why := set.Why(p.Servers)
		if kerr != nil {
			why = kerr.Error()
		}
		return res, fmt.Errorf("revoke: %w: the server set could not be verified (%s): %d server(s) that replied acknowledged the update, the others may not hold it; running the revoke again is safe", ErrNotConfirmed, why, len(acks))
	}
	return res, nil
}

func dedupe(l []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, s := range l {
		if !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	sort.Strings(out)
	return out
}

// verifyRevoked reads the account JWT back from every server until each answering one holds token, the JWT that was
// pushed, and shows every key in want with a revocation at least as new as its time; "" is a match.
func verifyRevoked(ctx context.Context, p RevokeParams, acctPub string, trust Trust, token string, need int, want map[string]int64) string {
	return readBack(ctx, p.Resolver, acctPub, trust, need, p.VerifyTries, p.VerifyWait, token, func(got *jwt.AccountClaims) string {
		var miss []string
		for u, ts := range want {
			if cur, ok := got.Revocations[u]; !ok || cur < ts {
				miss = append(miss, u)
			}
		}
		sort.Strings(miss)
		if len(miss) == 0 {
			return ""
		}
		return "does not show the revocation of " + strings.Join(miss, ", ")
	})
}
