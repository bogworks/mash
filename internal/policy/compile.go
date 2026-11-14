package policy

import (
	"fmt"
	"sort"

	"github.com/bogworks/mash/internal/config"
)

// Compiled is a policy file resolved against its declared endpoints: per endpoint, the recipients it may mail. There
// are no zones (ADR 0091): the compile no longer produces signing keys, permission templates or tags, and the server
// enforces no rule of the file until the label policy model is ruled.
type Compiled struct {
	Owner     string
	Default   string
	Endpoints []CompiledEndpoint // sorted by name
}

// CompiledEndpoint is one sender's compiled result.
type CompiledEndpoint struct {
	Name   string
	Labels map[string]string
	// Recipients are the declared endpoints this sender may MAIL, sorted. Prompt and status probes are open and not
	// listed here (ADR 0055). Never nil: empty means none.
	Recipients []string
}

// Compile resolves the file for owner. The (mail) recipients of a declared sender are the declared endpoints (itself
// included) that some allow rule grants and no deny rule forbids, or, where no rule matches the pair, what the
// default says; the order of rules never matters. Compile fails on an invalid owner (config.ValidName, the rule sap
// subjects need) or an invalid file.
func Compile(f *File, owner string) (*Compiled, error) {
	if !config.ValidName(owner) {
		return nil, fmt.Errorf("policy: owner %q is not a valid name (lower-case letters, digits and single hyphens, starting with a letter, 1-48 characters)", owner)
	}
	if err := f.checked(); err != nil {
		return nil, err
	}
	rec := f.recipients()
	c := &Compiled{Owner: owner, Default: f.defaultName()}
	for _, name := range f.names() {
		labels := map[string]string{}
		for k, v := range f.Endpoints[name].Labels {
			labels[k] = v
		}
		c.Endpoints = append(c.Endpoints, CompiledEndpoint{Name: name, Labels: labels, Recipients: rec[name]})
	}
	return c, nil
}

func (c *Compiled) endpoint(name string) (CompiledEndpoint, bool) {
	i := sort.Search(len(c.Endpoints), func(i int) bool { return c.Endpoints[i].Name >= name })
	if i < len(c.Endpoints) && c.Endpoints[i].Name == name {
		return c.Endpoints[i], true
	}
	return CompiledEndpoint{}, false
}
