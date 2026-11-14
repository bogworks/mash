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
	"github.com/bogworks/mash/internal/policy"
)

// Finding is one way the account JWT on the resolver differs from the policy file (`mashctl policy drift`, state
// "account"). Subject is the zone; Lines are the permission lines added ("+") and removed ("-") going from the server's
// template to the file's.
type Finding struct {
	Kind     string   `json:"kind"`
	Subject  string   `json:"subject,omitempty"`
	Identity string   `json:"identity,omitempty"` // additive (ADR 0081): the canonical agent.owner.name of a three-token Subject, omitted otherwise
	Detail   string   `json:"detail"`
	Lines    []string `json:"lines,omitempty"`
}

// Finding kinds of AccountFindings.
const (
	KindBudget        = "over-budget"
	KindZoneMissing   = "zone-missing-on-server"
	KindZoneTemplate  = "zone-template-differs"
	KindKeyConflict   = "zone-key-differs"
	KindZoneNotInFile = "zone-not-in-file"
	KindUnknownKey    = "signing-key-unknown"
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

// verifyAll reads the account JWT from every server until all need of them answer with want; "" is that, else what was
// found. It retries like verifyState, since a server may still be applying the update.
func verifyAll(ctx context.Context, p Params, acct string, need int, want string) string {
	tries, wait := p.VerifyTries, p.VerifyWait
	if tries <= 0 {
		tries = defaultVerifyTries
	}
	if wait <= 0 {
		wait = defaultVerifyWait
	}
	last := ""
	for i := 0; i < tries; i++ {
		if i > 0 {
			select {
			case <-ctx.Done():
				return "the check was interrupted: " + ctx.Err().Error()
			case <-time.After(wait):
			}
		}
		a, err := collectAgreement(ctx, p.Resolver, acct, need)
		switch {
		case err != nil:
			last = "could not ask every server for the account JWT: " + err.Error()
		case a.answered < a.need:
			last = fmt.Sprintf("only %d of %d servers answered the account JWT lookup (a server may be down or partitioned; %s)", a.answered, a.need, a.describe())
		case len(a.groups) != 1 || a.groups[0].token != want:
			last = a.differText()
		default:
			return ""
		}
	}
	return last
}

// AccountFindings compares the policy file's compiled zones with the scoped signing keys of the account JWT the
// resolver holds, with the same code `policy diff` uses, and reports every difference and every refusal condition apply
// would report (a server key that is not the key store's, a zone the file lacks) as a finding. It changes nothing: no
// key is created and nothing is pushed. p.Compiled, p.Keys and p.Resolver are used; the result is empty when the
// resolver's copy matches. It builds the comparison from the newest copy any server holds (highest iat) and also asks every server (Resolver.LookupAll): servers that answer with different JWTs, fewer answers than the known server count, or an unverifiable server set are findings too. Params.Servers is the operator's server count.
func AccountFindings(ctx context.Context, p Params) ([]Finding, error) {
	p.Mode, p.collectAll = Diff, true
	var out []Finding
	opKP, _, err := p.Keys.Operator()
	if err != nil {
		return nil, err
	}
	defer opKP.Wipe()
	acctKP, acctPub, err := p.Keys.Account()
	if err != nil {
		return nil, err
	}
	acctKP.Wipe()
	if p.trust, err = LoadTrust(p.Keys); err != nil {
		return nil, err
	}
	cur, err := p.Resolver.Lookup(ctx, acctPub)
	if err != nil {
		return nil, fmt.Errorf("look up the account JWT: %w", err)
	}
	set, serr := p.Resolver.Servers(ctx)
	if errors.Is(serr, ErrServerSetRefused) {
		return nil, fmt.Errorf("refusing: %w", serr)
	}
	budget := p.Budget
	if budget == nil {
		budget = policy.BudgetFor
	}
	limits, lerr := Limits(ctx, p.Resolver, set, p.MaxControlLine)
	if lerr != nil {
		out = append(out, Finding{Kind: KindBudget, Detail: fmt.Sprintf("the policy cannot be measured against the servers' limits: %v", lerr)})
	} else {
		p.limits = limits
		if rep, err := budget(p.Compiled, limits); err != nil || !rep.OK {
			if err == nil {
				err = errors.New("over budget")
			}
			out = append(out, Finding{Kind: KindBudget, Detail: fmt.Sprintf("the policy does not fit the JWT budgets: %v", err)})
		}
	}
	need := max(set.need(p.Servers), len(set.Names), 1)
	ag, aerr := collectAgreement(ctx, p.Resolver, acctPub, need)
	switch {
	case aerr != nil:
		return nil, fmt.Errorf("look up the account JWT on every server: %w", aerr)
	case ag.answered < ag.need:
		out = append(out, Finding{Kind: KindServersSilent, Detail: fmt.Sprintf("only %d of %d servers answered the account JWT lookup (cannot say which: a server may be down or partitioned)", ag.answered, ag.need)})
	case !set.Complete(p.Servers):
		why := set.Why(p.Servers)
		if serr != nil {
			why = serr.Error()
		}
		out = append(out, Finding{Kind: KindServersUnverified, Detail: "the server set could not be verified, so a server may hold another account JWT: " + why})
	}
	// Compare against the newest copy any server holds (highest iat), not whichever answered first; build verifies it.
	var newest int64
	for _, g := range ag.groups {
		if c, derr := jwt.DecodeAccountClaims(g.token); derr == nil && c.IssuedAt > newest {
			newest, cur = c.IssuedAt, g.token
		}
	}
	if ag.answered > 0 && len(ag.groups) > 1 {
		out = append(out, Finding{Kind: KindServersDiffer, Detail: ag.differText()})
	}
	b, err := build(p, opKP, acctPub, cur, map[string]bool{}, map[string]bool{})
	if err != nil {
		return nil, err
	}
	for _, cf := range b.conflicts {
		local := cf.localKey
		if local == "" {
			local = "none (the zone key file is missing)"
		}
		out = append(out, Finding{Kind: KindKeyConflict, Subject: config.Clean(cf.zone),
			Detail: fmt.Sprintf("the server's signing key is %s, the key store's is %s", config.Clean(cf.serverKey), local)})
	}
	for _, o := range b.unacked {
		out = append(out, Finding{Kind: KindZoneNotInFile, Subject: config.Clean(o.zone),
			Detail: fmt.Sprintf("signing key %s is on the server with its old permissions and the policy file has no such zone", config.Clean(o.key))})
	}
	for _, u := range b.unknown {
		out = append(out, Finding{Kind: KindUnknownKey, Subject: config.Clean(u.key),
			Detail: fmt.Sprintf("the account JWT holds the %s, which mashctl did not create. %s", u.describe(), unknownKeyNote)})
	}
	for _, zp := range b.plans {
		switch zp.state {
		case zoneNew:
			d := "the account JWT has no scoped signing key for this zone"
			if zp.keyIsNew {
				d += ", and the key store has no key file for it"
			}
			out = append(out, Finding{Kind: KindZoneMissing, Subject: zp.name, Detail: d, Lines: zp.changes})
		case zoneChanged:
			d := "the zone's template on the server differs from the file's"
			if zp.otherDiff {
				d = "the permission lines are the same; the role or limits of the key differ"
			}
			out = append(out, Finding{Kind: KindZoneTemplate, Subject: zp.name, Detail: d, Lines: zp.changes})
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
