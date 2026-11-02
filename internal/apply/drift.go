package apply

import (
	"context"
	"errors"
	"fmt"
	"sort"

	"github.com/bogworks/mash/internal/policy"
)

// Finding is one way the account JWT on the resolver differs from the policy file (`mashctl policy drift`, state
// "account"). Subject is the zone; Lines are the permission lines added ("+") and removed ("-") going from the server's
// template to the file's.
type Finding struct {
	Kind    string   `json:"kind"`
	Subject string   `json:"subject,omitempty"`
	Detail  string   `json:"detail"`
	Lines   []string `json:"lines,omitempty"`
}

// Finding kinds of AccountFindings.
const (
	KindBudget        = "over-budget"
	KindZoneMissing   = "zone-missing-on-server"
	KindZoneTemplate  = "zone-template-differs"
	KindKeyConflict   = "zone-key-differs"
	KindZoneNotInFile = "zone-not-in-file"
)

// AccountFindings compares the policy file's compiled zones with the scoped signing keys of the account JWT the
// resolver holds, with the same code `policy diff` uses, and reports every difference and every refusal condition apply
// would report (a server key that is not the key store's, a zone the file lacks) as a finding. It changes nothing: no
// key is created and nothing is pushed. p.Compiled, p.Keys and p.Resolver are used; the result is empty when the
// resolver's copy matches. It reads one resolver's copy: it does not show that every server holds it (apply does).
func AccountFindings(ctx context.Context, p Params) ([]Finding, error) {
	p.Mode, p.collectAll = Diff, true
	var out []Finding
	budget := p.Budget
	if budget == nil {
		budget = policy.Budget
	}
	if rep, err := budget(p.Compiled); err != nil || !rep.OK {
		if err == nil {
			err = errors.New("over budget")
		}
		out = append(out, Finding{Kind: KindBudget, Detail: fmt.Sprintf("the policy does not fit the JWT budgets: %v", err)})
	}
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
	cur, err := p.Resolver.Lookup(ctx, acctPub)
	if err != nil {
		return nil, fmt.Errorf("look up the account JWT: %w", err)
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
		out = append(out, Finding{Kind: KindKeyConflict, Subject: cf.zone,
			Detail: fmt.Sprintf("the server's signing key is %s, the key store's is %s", cf.serverKey, local)})
	}
	for _, o := range b.unacked {
		out = append(out, Finding{Kind: KindZoneNotInFile, Subject: o.zone,
			Detail: fmt.Sprintf("signing key %s is on the server with its old permissions and the policy file has no such zone", o.key)})
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
