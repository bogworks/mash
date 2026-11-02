package policy

import (
	"fmt"
	"slices"

	"github.com/nats-io/jwt/v2"

	"github.com/bogworks/mash/internal/admin"
	"github.com/bogworks/mash/internal/config"
)

// Compiled is a policy file resolved against its declared endpoints: for every endpoint, the recipients it may
// address and the full NATS permission set that grants exactly that.
type Compiled struct {
	Owner     string
	Default   string
	Endpoints []CompiledEndpoint // sorted by name
}

// CompiledEndpoint is one sender's compiled result.
type CompiledEndpoint struct {
	Name   string
	Labels map[string]string
	// Recipients are the declared endpoints this sender may mail, prompt and probe, sorted. Never nil: empty means
	// none, and the permissions then carry no mail, prompt or status line.
	Recipients []string
	// Perms is admin.PermissionsTo(owner, name, Recipients): the fixed per-endpoint lines plus one publish line per
	// recipient and verb. It is never empty.
	Perms jwt.Permissions
}

// Compile resolves the file for owner. For every declared sender the recipients are the declared endpoints (the
// sender itself included) that some allow rule grants and no deny rule forbids, or, where no rule matches the pair,
// what the default says. The order of rules never matters. Compile fails on an invalid owner (config.ValidName, the
// rule sap subjects need) or an invalid file; it never widens: a pair is granted only if Explain says allow.
func Compile(f *File, owner string) (*Compiled, error) {
	if !config.ValidName(owner) {
		return nil, fmt.Errorf("policy: owner %q is not a valid name (lower-case letters, digits and single hyphens, starting with a letter, 1-48 characters)", owner)
	}
	if err := f.checked(); err != nil {
		return nil, err
	}
	names := f.names()
	c := &Compiled{Owner: owner, Default: f.defaultName(), Endpoints: make([]CompiledEndpoint, 0, len(names))}
	for _, from := range names {
		rec := []string{}
		for _, to := range names {
			if f.decide(from, to).allowed {
				rec = append(rec, to)
			}
		}
		slices.Sort(rec)
		labels := map[string]string{}
		for k, v := range f.Endpoints[from].Labels {
			labels[k] = v
		}
		c.Endpoints = append(c.Endpoints, CompiledEndpoint{
			Name: from, Labels: labels, Recipients: rec,
			Perms: admin.PermissionsTo(owner, from, rec),
		})
	}
	return c, nil
}
