package cmds

import (
	"context"
	"slices"
	"sort"
	"strings"

	"github.com/bogworks/mash/internal/cliframe"
	"github.com/bogworks/mash/internal/cmdcore"
	"github.com/bogworks/mash/internal/cmdcore/render"
	"github.com/bogworks/mash/internal/config"
)

// WhoEntry is one endpoint of the directory as the service knows it. Role, Host and Description are empty when nothing
// is recorded; Capabilities and Labels are empty when it has none.
type WhoEntry struct {
	Address      string // owner.group.name
	Online       bool
	Role         string
	Host         string
	Description  string
	Capabilities []string
	Labels       map[string]string
}

// Directory is the service Who needs from Env.Svc: every endpoint the caller can see, and whether each is online.
type Directory interface {
	Who(ctx context.Context) ([]WhoEntry, error)
}

// WhoParams are the arguments of mash who and of the MCP tool who.
type WhoParams struct {
	Prefix     string   `json:"prefix,omitempty" arg:"prefix,optional" desc:"Only endpoints under this owner (acme) or this owner and group (acme.demo), or the one endpoint with this full address. acme.* and acme.demo.* are accepted and mean the same as acme and acme.demo."`
	Online     bool     `json:"online,omitempty" flag:"--online" desc:"Only endpoints that are online."`
	Offline    bool     `json:"offline,omitempty" flag:"--offline" desc:"Only endpoints that are offline."`
	Role       string   `json:"role,omitempty" flag:"--role" value:"text" desc:"Only endpoints whose role contains this text."`
	Capability []string `json:"capability,omitempty" flag:"--capability" value:"name" repeat:"true" desc:"Only endpoints that have this capability. May be given more than once; all must be present."`
	Label      []string `json:"label,omitempty" flag:"--label" value:"label" repeat:"true" desc:"Only endpoints with this label, as name or name=value. May be given more than once; all must match."`
	Wide       bool     `json:"wide,omitempty" flag:"--wide" desc:"More columns: role, host and description, where recorded. May pass 80 columns."`
}

// WhoEndpoint is one endpoint of the result, always with every field, whatever --wide or the tool's wide says (that
// changes only the text). Role, Host and Description are null when nothing is recorded; Capability is a list and Labels
// an object, empty when there are none.
type WhoEndpoint struct {
	Address     string            `json:"address"`
	Online      bool              `json:"online"`
	Role        *string           `json:"role"`
	Host        *string           `json:"host"`
	Description *string           `json:"description"`
	Capability  []string          `json:"capability"`
	Labels      map[string]string `json:"labels"`
}

// WhoResult is the endpoints that matched, sorted by address. The JSON form is always this object, never a bare array.
// The three fields below it are what the text says when nothing matched and are not part of it.
type WhoResult struct {
	Endpoints []WhoEndpoint `json:"endpoints"`

	prefix string
	only   string // "online", "offline" or ""
	wide   bool
}

// Text is the table: ADDRESS and STATE, and with --wide ROLE, HOST and DESCRIPTION ("-" where nothing is recorded), or
// the sentence saying nothing matched.
func (r WhoResult) Text(_ cmdcore.Opts) string {
	if len(r.Endpoints) == 0 {
		s := "No "
		if r.only != "" {
			s += r.only + " "
		}
		s += "endpoints"
		if r.prefix != "" {
			s += " under " + r.prefix
		}
		return s
	}
	cols := []string{"address", "state"}
	if r.wide {
		cols = append(cols, "role", "host", "description")
	}
	or := func(p *string) string {
		if p == nil || *p == "" {
			return "-"
		}
		return config.Clean(*p)
	}
	rows := make([][]string, 0, len(r.Endpoints))
	for _, e := range r.Endpoints {
		state := "offline"
		if e.Online {
			state = "online"
		}
		row := []string{config.Clean(e.Address), state}
		if r.wide {
			row = append(row, or(e.Role), or(e.Host), or(e.Description))
		}
		rows = append(rows, row)
	}
	return render.Table(cols, rows, 0, false)
}

// underPrefix reports whether address is the prefix itself or lies under it: acme.demo matches acme.demo.bob and not
// acme.demobob, and acme.demo.bob does not match acme.demo.bobby.
func underPrefix(address, prefix string) bool {
	return prefix == "" || address == prefix || strings.HasPrefix(address, prefix+".")
}

type labelFilter struct {
	name, value string
	hasValue    bool
}

// parseLabelFilters reads each --label as name (the label is present) or name=value (it has exactly that value).
func parseLabelFilters(specs []string) ([]labelFilter, error) {
	var out []labelFilter
	for _, s := range specs {
		name, value, hasValue := strings.Cut(s, "=")
		if name == "" || hasValue && value == "" {
			return nil, cliframe.Usagef("--label %q is not name or name=value", s).WithHint("mash who --help")
		}
		out = append(out, labelFilter{name, value, hasValue})
	}
	return out, nil
}

// matchesFilters reports whether e passes every filter: the role contains the text (lowercased in role, any case in
// the record), each capability is present as an exact word, and each label is present and, when given, has the value.
func matchesFilters(e WhoEntry, role string, caps []string, labels []labelFilter) bool {
	if role != "" && !strings.Contains(strings.ToLower(e.Role), role) {
		return false
	}
	for _, c := range caps {
		if !slices.Contains(e.Capabilities, strings.TrimSpace(c)) {
			return false
		}
	}
	for _, l := range labels {
		v, ok := e.Labels[l.name]
		if !ok || l.hasValue && v != l.value {
			return false
		}
	}
	return true
}

func copyLabels(m map[string]string) map[string]string {
	out := make(map[string]string, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}

func ptr(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

// Who is the who command.
var Who = cmdcore.Command[WhoParams, WhoResult]{
	Name:        "who",
	Group:       "Everyday",
	Summary:     "List the endpoints you can see, and which are online.",
	Description: "List the endpoints you can see, and which are online.",
	Examples:    []string{"mash who", "mash who acme.demo --online", "mash who --capability review --online"},
	Run: func(ctx context.Context, env *cmdcore.Env, p WhoParams) (WhoResult, error) {
		if p.Online && p.Offline {
			return WhoResult{}, cliframe.Usagef("mash who takes --online or --offline, not both").WithHint("mash who --help")
		}
		prefix := ""
		if p.Prefix != "" {
			var err error
			if prefix, err = cmdcore.ParsePrefix(p.Prefix, "mash who --help"); err != nil {
				return WhoResult{}, err
			}
		}
		labels, err := parseLabelFilters(p.Label)
		if err != nil {
			return WhoResult{}, err
		}
		for _, c := range p.Capability {
			if strings.TrimSpace(c) == "" {
				return WhoResult{}, cliframe.Usagef("--capability needs a capability name").WithHint("mash who --help")
			}
		}
		role := strings.ToLower(strings.TrimSpace(p.Role))
		if env == nil {
			return WhoResult{}, cliframe.Failf("internal: mash who has no environment to run in")
		}
		svc, ok := env.Svc.(Directory)
		if !ok || svc == nil {
			return WhoResult{}, cliframe.Failf("internal: mash who has no directory to read")
		}
		all, err := svc.Who(ctx)
		if err != nil {
			return WhoResult{}, err
		}
		r := WhoResult{Endpoints: []WhoEndpoint{}, prefix: prefix, wide: p.Wide}
		switch {
		case p.Online:
			r.only = "online"
		case p.Offline:
			r.only = "offline"
		}
		for _, e := range all {
			if !underPrefix(e.Address, prefix) || p.Online && !e.Online || p.Offline && e.Online || !matchesFilters(e, role, p.Capability, labels) {
				continue
			}
			r.Endpoints = append(r.Endpoints, WhoEndpoint{Address: e.Address, Online: e.Online,
				Role: ptr(e.Role), Host: ptr(e.Host), Description: ptr(e.Description),
				Capability: append([]string{}, e.Capabilities...), Labels: copyLabels(e.Labels)})
		}
		sort.Slice(r.Endpoints, func(i, j int) bool { return r.Endpoints[i].Address < r.Endpoints[j].Address })
		return r, nil
	},
}
