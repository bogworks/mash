package policy

import (
	"fmt"
	"regexp"
	"slices"
	"sort"
	"strings"

	"github.com/nats-io/jwt/v2"

	"github.com/bogworks/mash/internal/admin"
	"github.com/bogworks/mash/internal/config"
)

// Compiled is a policy file resolved against its declared endpoints into the layout of one scoped signing key per
// zone: the key's permission template (fixed per-endpoint lines, one MAIL wildcard line per allowed recipient zone, and
// MAIL tag templates for exceptions; prompt and status are two open fixed lines, not policed: ADR 0054) and, per endpoint, the recipients it may address and the tags its user JWT carries.
// The account JWT holds one key per zone, so its size does not depend on the number of endpoints.
type Compiled struct {
	Owner        string
	Default      string
	SubjectLabel string
	Zones        []CompiledZone     // sorted by name
	Endpoints    []CompiledEndpoint // sorted by name
}

// CompiledZone is one zone's scoped signing key.
type CompiledZone struct {
	Name string
	// Perms is the key's permission template for MAIL (admin.ZonePermissions: the allow and the deny tag template always, so the
	// key does not change when the zone gets its first deny exception). Its {{name()}} and {{tag()}} templates expand per user; see Effective.
	Perms jwt.Permissions
	// Wildcards are the recipient zones the key lets every endpoint of this zone address whole, sorted.
	Wildcards []string
	// Endpoints are the zone's endpoint names, sorted.
	Endpoints []string
}

// CompiledEndpoint is one sender's compiled result.
type CompiledEndpoint struct {
	Name   string
	Zone   string
	Labels map[string]string
	// Recipients are the declared endpoints this sender may MAIL, sorted. Prompt and status probes are open and not
	// listed here (ADR 0054). Never nil: empty means none.
	Recipients []string
	// Tags are the tags the endpoint's user JWT carries, sorted: allow:<zone>.<name> or allow:<zone>.* for what the
	// zone key does not grant it, deny:<zone>.<name> or deny:<zone>.* for what it grants but the file forbids, and
	// admin.NoDenyTag always (every zone key has the deny template and nats-server refuses a user with no deny tag). Never nil.
	Tags []string
}

// MailSubjectPattern is the subject pattern of the mail addressed to endpoint name of zone under owner, from any
// agent token: agents.mail.*.<owner>.<zone>.<name>. Only mail carries the zone token; prompt and status keep SAP's
// five-token subjects (ADR 0054).
func MailSubjectPattern(owner, zone, name string) string {
	return subjectPattern("mail", owner, zone, name)
}

func subjectPattern(verb, owner, zone, name string) string {
	return "agents." + verb + ".*." + owner + "." + zone + "." + name
}

// Compile resolves the file for owner. The (mail) recipients of a declared sender are the declared endpoints (itself
// included) that some allow rule grants and no deny rule forbids, or, where no rule matches the pair, what the
// default says; the order of rules never matters. Compile fails on an invalid owner (config.ValidName, the rule sap
// subjects need) or an invalid file. Expanding each endpoint's permissions (Effective) yields exactly those
// mail recipients: never more, never fewer.
func Compile(f *File, owner string) (*Compiled, error) {
	if !config.ValidName(owner) {
		return nil, fmt.Errorf("policy: owner %q is not a valid name (lower-case letters, digits and single hyphens, starting with a letter, 1-48 characters)", owner)
	}
	if err := f.checked(); err != nil {
		return nil, err
	}
	p := f.plan()
	c := &Compiled{Owner: owner, Default: f.defaultName(), SubjectLabel: f.subjectLabel()}
	for _, z := range p.zones {
		perms := admin.ZonePermissions(owner, z, p.wildcards[z])
		c.Zones = append(c.Zones, CompiledZone{Name: z, Perms: perms, Wildcards: append([]string{}, p.wildcards[z]...), Endpoints: append([]string{}, p.members[z]...)})
	}
	for _, name := range f.names() {
		labels := map[string]string{}
		for k, v := range f.Endpoints[name].Labels {
			labels[k] = v
		}
		c.Endpoints = append(c.Endpoints, CompiledEndpoint{
			Name: name, Zone: p.zone[name], Labels: labels,
			Recipients: p.recipients[name], Tags: p.tags[name],
		})
	}
	return c, nil
}

func (c *Compiled) zone(name string) (CompiledZone, bool) {
	i := sort.Search(len(c.Zones), func(i int) bool { return c.Zones[i].Name >= name })
	if i < len(c.Zones) && c.Zones[i].Name == name {
		return c.Zones[i], true
	}
	return CompiledZone{}, false
}

func (c *Compiled) endpoint(name string) (CompiledEndpoint, bool) {
	i := sort.Search(len(c.Endpoints), func(i int) bool { return c.Endpoints[i].Name >= name })
	if i < len(c.Endpoints) && c.Endpoints[i].Name == name {
		return c.Endpoints[i], true
	}
	return CompiledEndpoint{}, false
}

// Effective is the permission set nats-server gives the user of endpoint name: the zone key's template with {{name()}}
// replaced by the name and {{tag(x)}} by one subject per "x:" tag of the user (a line whose tag template finds no tag
// is dropped from an allow list; in a deny list nats-server refuses the user instead, and so does Effective). A tag
// template yields one line per tag value, so the deny line comes out once for NoDenyTag and once per real deny tag. It
// evaluates the same expansion nats-server does at connect time, so tests can compare the file's decisions with what
// would be enforced. Only mail is policed: prompt and status publishes are open, five-token lines.
func (c *Compiled) Effective(name string) (jwt.Permissions, error) {
	e, ok := c.endpoint(name)
	if !ok {
		return jwt.Permissions{}, fmt.Errorf("policy: %q is not a compiled endpoint", name)
	}
	z, ok := c.zone(e.Zone)
	if !ok {
		return jwt.Permissions{}, fmt.Errorf("policy: endpoint %s: zone %q is not compiled", name, e.Zone)
	}
	var out jwt.Permissions
	for _, l := range []struct {
		src    jwt.StringList
		dst    *jwt.StringList
		isDeny bool
	}{
		{z.Perms.Pub.Allow, &out.Pub.Allow, false}, {z.Perms.Pub.Deny, &out.Pub.Deny, true},
		{z.Perms.Sub.Allow, &out.Sub.Allow, false}, {z.Perms.Sub.Deny, &out.Sub.Deny, true},
	} {
		for _, line := range l.src {
			subjects, err := expandTemplate(line, name, e.Tags, l.isDeny)
			if err != nil {
				return jwt.Permissions{}, fmt.Errorf("policy: endpoint %s: %w", name, err)
			}
			l.dst.Add(subjects...)
		}
	}
	return out, nil
}

var mustache = regexp.MustCompile(`{{2}([^}]+)}{2}`)

// expandTemplate mirrors nats-server's expansion of one permission line for a scoped-key user: {{name()}} is the
// user's name; {{tag(x)}} takes every "x:<value>" tag (tag names compare lower case) and emits one line per value,
// as a product when a line has several tag templates.
func expandTemplate(line, name string, tags []string, isDeny bool) ([]string, error) {
	out := []string{line}
	for _, tok := range slices.Compact(mustache.FindAllString(line, -1)) {
		op := strings.TrimSpace(strings.TrimSuffix(strings.TrimPrefix(tok, "{{"), "}}"))
		var vals []string
		switch {
		case strings.EqualFold(op, "name()"):
			vals = []string{name}
		case len(op) > 5 && strings.EqualFold(op[:4], "tag(") && strings.HasSuffix(op, ")"):
			prefix := strings.ToLower(op[4:len(op)-1]) + ":"
			for _, t := range tags {
				if v, ok := strings.CutPrefix(strings.ToLower(t), prefix); ok {
					vals = append(vals, v)
				}
			}
			if len(vals) == 0 {
				if isDeny {
					return nil, fmt.Errorf("generated invalid subject %q: %q is not defined (nats-server refuses the user)", line, op)
				}
				return nil, nil
			}
		default:
			return nil, fmt.Errorf("template operation in %q: %q is not defined", line, op)
		}
		var next []string
		for _, s := range out {
			for _, v := range vals {
				next = append(next, strings.ReplaceAll(s, tok, v))
			}
		}
		out = next
	}
	return out, nil
}
