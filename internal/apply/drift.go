package apply

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/nats-io/jwt/v2"

	"github.com/bogworks/mash/internal/config"
	"github.com/bogworks/mash/internal/ctlkeys"
)

// Finding is one thing a state of `mashctl policy drift` reports. Subject is what it is about; Lines are detail lines.
type Finding struct {
	Kind     string   `json:"kind"`
	Subject  string   `json:"subject,omitempty"`
	Identity string   `json:"identity,omitempty"` // additive (ADR 0081): the canonical agent.owner.name of a three-token Subject, omitted otherwise
	Detail   string   `json:"detail"`
	Lines    []string `json:"lines,omitempty"`
}

// Finding kinds of AccountFindings.
const (
	// KindZonePolicyApplied: the account JWT carries a scoped signing key, which only a zone policy publishes (ADR 0091).
	KindZonePolicyApplied = "zone-policy-applied"
	KindUnknownKey        = "signing-key-unknown"
	// Findings of the servers' agreement on the account JWT.
	KindServersDiffer     = "servers-differ"
	KindServersSilent     = "servers-silent"
	KindServersUnverified = "servers-unverified"
)

// jwtGroup is one distinct account JWT the servers answered with, and how many servers hold it.
type jwtGroup struct {
	token string
	n     int
}

// agreement is what every server answered to the claims lookup.
type agreement struct {
	answered, need int
	groups         []jwtGroup // distinct answers, most common first
}

// collectAgreement asks every server (LookupAll) for the account JWT and groups the answers; need is the known
// server count.
func collectAgreement(ctx context.Context, r Resolver, acct string, need int) (agreement, error) {
	need = max(need, 1)
	toks, err := r.LookupAll(ctx, acct, need)
	a := agreement{answered: len(toks), need: need}
	if err != nil && len(toks) == 0 {
		return a, err
	}
	count := map[string]int{}
	for _, t := range toks {
		count[t]++
	}
	for t, n := range count {
		a.groups = append(a.groups, jwtGroup{t, n})
	}
	sort.Slice(a.groups, func(i, j int) bool {
		if a.groups[i].n != a.groups[j].n {
			return a.groups[i].n > a.groups[j].n
		}
		return a.groups[i].token < a.groups[j].token
	})
	return a, nil
}

// describe names each distinct JWT by its issued-at and a short hash of the token (no secret is shown).
func (a agreement) describe() string {
	var parts []string
	for _, g := range a.groups {
		if g.token == "" {
			parts = append(parts, fmt.Sprintf("no JWT on %d server(s)", g.n))
			continue
		}
		iat := "unreadable"
		if c, err := jwt.DecodeAccountClaims(g.token); err == nil {
			iat = time.Unix(c.IssuedAt, 0).UTC().Format("2006-01-02T15:04:05Z")
		}
		sum := sha256.Sum256([]byte(g.token))
		parts = append(parts, fmt.Sprintf("iat %s sha %s on %d server(s)", iat, hex.EncodeToString(sum[:4]), g.n))
	}
	return strings.Join(parts, ", ")
}

// differs says the answers are not one and the same non-empty JWT.
func (a agreement) differs() bool {
	return len(a.groups) != 1 || a.groups[0].token == ""
}

func (a agreement) differText() string {
	return fmt.Sprintf("servers differ: %d of %d servers answered; %d distinct account JWTs (%s)", a.answered, a.need, len(a.groups), a.describe())
}

// AccountFindings reads the account JWT the servers hold and reports what is wrong with it now that zones are removed
// (ADR 0091): a scoped signing key (the zone policy of an older release is still applied), a signing key mashctl did
// not create, and servers that answer with different JWTs, fewer answers than the known server count, or an
// unverifiable server set. servers is the operator's server count (0: not given). It changes nothing.
func AccountFindings(ctx context.Context, r Resolver, ks *ctlkeys.Authority, servers int) ([]Finding, error) {
	var out []Finding
	acctKP, acctPub, err := ks.Account()
	if err != nil {
		return nil, err
	}
	acctKP.Wipe()
	trust, err := LoadTrust(ks)
	if err != nil {
		return nil, err
	}
	cur, err := r.Lookup(ctx, acctPub)
	if err != nil {
		return nil, fmt.Errorf("look up the account JWT: %w", err)
	}
	set, serr := r.Servers(ctx)
	if errors.Is(serr, ErrServerSetRefused) {
		return nil, fmt.Errorf("refusing: %w", serr)
	}
	need := max(set.need(servers), len(set.Names), 1)
	ag, aerr := collectAgreement(ctx, r, acctPub, need)
	switch {
	case aerr != nil:
		return nil, fmt.Errorf("look up the account JWT on every server: %w", aerr)
	case ag.answered < ag.need:
		out = append(out, Finding{Kind: KindServersSilent, Detail: fmt.Sprintf("only %d of %d servers answered the account JWT lookup (cannot say which: a server may be down or partitioned)", ag.answered, ag.need)})
	case !set.Complete(servers):
		why := set.Why(servers)
		if serr != nil {
			why = serr.Error()
		}
		out = append(out, Finding{Kind: KindServersUnverified, Detail: "the server set could not be verified, so a server may hold another account JWT: " + why})
	}
	// Report on the newest copy any server holds (highest iat), not whichever answered first.
	var newest int64
	for _, g := range ag.groups {
		if c, derr := jwt.DecodeAccountClaims(g.token); derr == nil && c.IssuedAt > newest {
			newest, cur = c.IssuedAt, g.token
		}
	}
	if ag.answered > 0 && len(ag.groups) > 1 {
		out = append(out, Finding{Kind: KindServersDiffer, Detail: ag.differText()})
	}
	if cur != "" {
		claims, err := VerifyAccountJWT(cur, acctPub, trust)
		if err != nil {
			return nil, fmt.Errorf("refusing the account JWT: %w", err)
		}
		known, _ := knownSigningKeys(ks, acctPub)
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
			if u.role != "" {
				out = append(out, Finding{Kind: KindZonePolicyApplied, Subject: config.Clean(u.key),
					Detail: fmt.Sprintf("the account JWT holds the %s: a zone policy from an older release is applied, and zones are removed (ADR 0091); stay on the last zoned release until the label policy model ships", u.describe())})
				continue
			}
			out = append(out, Finding{Kind: KindUnknownKey, Subject: config.Clean(u.key),
				Detail: fmt.Sprintf("the account JWT holds the %s, which mashctl did not create. %s", u.describe(), unknownKeyNote)})
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Kind != out[j].Kind {
			return out[i].Kind < out[j].Kind
		}
		return out[i].Subject < out[j].Subject
	})
	return out, nil
}
