package main

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/bogworks/mash/internal/endpoint"
)

const whoUsage = "usage: mash who [--long] [--json] [--names] [--match TEXT] [--name SUB] [--owner O] [--role SUB] [--label K[=V]]... [--cap C]... [--online|--offline]"

// whoOutput is how `mash who` renders its result.
type whoOutput struct{ Long, JSON, Names bool }

// ParseWhoFlags reads the arguments after `who`. A flag that takes a value accepts "--flag v" and "--flag=v".
func ParseWhoFlags(args []string) (endpoint.WhoFilter, whoOutput, error) {
	var f endpoint.WhoFilter
	var o whoOutput
	bad := func(format string, a ...any) (endpoint.WhoFilter, whoOutput, error) {
		return endpoint.WhoFilter{}, whoOutput{}, fmt.Errorf("%s\nmash who: %s", whoUsage, fmt.Sprintf(format, a...))
	}
	for i := 0; i < len(args); i++ {
		name, val, hasVal := strings.Cut(args[i], "=")
		switch name {
		case "--long", "--json", "--names", "--online", "--offline":
			if hasVal {
				return bad("%s takes no value", name)
			}
			switch name {
			case "--long":
				o.Long = true
			case "--json":
				o.JSON = true
			case "--names":
				o.Names = true
			case "--online":
				f.Online = true
			case "--offline":
				f.Offline = true
			}
		case "--match", "--name", "--owner", "--role", "--label", "--cap":
			if !hasVal {
				if i+1 >= len(args) {
					return bad("%s needs a value", name)
				}
				i++
				val = args[i]
			}
			switch name {
			case "--match":
				f.Match = val
			case "--name":
				f.Name = val
			case "--owner":
				f.Owner = val
			case "--role":
				f.Role = val
			case "--label":
				f.Labels = append(f.Labels, val)
			case "--cap":
				f.Caps = append(f.Caps, val)
			}
		default:
			return bad("unknown argument %q", args[i])
		}
	}
	if o.JSON && o.Names {
		return bad("--json and --names are exclusive")
	}
	if err := f.Validate(); err != nil {
		return bad("%v", err)
	}
	return f, o, nil
}

// whoNames renders one name per line.
func whoNames(ps []endpoint.Presence) string {
	names := make([]string, len(ps))
	for i, p := range ps {
		names[i] = p.Record.Name
	}
	return strings.Join(names, "\n")
}

// whoLease and whoEntry are the stable --json schema (docs/operations.md).
type whoLease struct {
	Host    string `json:"host,omitempty"`
	Version string `json:"version,omitempty"`
}

type whoEntry struct {
	Name        string            `json:"name"`
	Owner       string            `json:"owner"`
	Online      bool              `json:"online"`
	LastSeen    string            `json:"last_seen,omitempty"`
	Role        string            `json:"role,omitempty"`
	Host        string            `json:"host,omitempty"`
	Note        string            `json:"note,omitempty"`
	Description string            `json:"description,omitempty"`
	Caps        []string          `json:"caps,omitempty"`
	Labels      map[string]string `json:"labels,omitempty"`
	Lease       *whoLease         `json:"lease,omitempty"`
}

// whoJSON renders the presences as a JSON array (`[]` when none). Map keys encode sorted. lease is added only when
// long is set and a live lease is known.
func whoJSON(ps []endpoint.Presence, long bool, live map[string]endpoint.LeaseInfo) string {
	out := make([]whoEntry, 0, len(ps))
	for _, p := range ps {
		r := p.Record
		e := whoEntry{Name: r.Name, Owner: r.Owner, Online: p.Online, Role: r.Role, Host: r.Host, Note: r.Note, Description: r.Description}
		if len(r.Caps) > 0 {
			e.Caps = r.Caps
		}
		if len(r.Labels) > 0 {
			e.Labels = r.Labels
		}
		if !p.LastSeen.IsZero() {
			e.LastSeen = p.LastSeen.UTC().Format(time.RFC3339)
		}
		if li, ok := live[r.Name]; ok && long {
			e.Lease = &whoLease{Host: li.Host, Version: li.Version}
		}
		out = append(out, e)
	}
	b, _ := json.MarshalIndent(out, "", "  ")
	return string(b)
}

// whoRender renders an already filtered list. filtered says a filter was given: then an empty text result is empty,
// and the "no endpoints" text is only for an empty directory.
func whoRender(ps []endpoint.Presence, o whoOutput, filtered bool, live map[string]endpoint.LeaseInfo) string {
	switch {
	case o.JSON:
		return whoJSON(ps, o.Long, live)
	case o.Names:
		return whoNames(ps)
	case len(ps) == 0 && filtered:
		return ""
	}
	return whoText(ps, o.Long, live)
}
