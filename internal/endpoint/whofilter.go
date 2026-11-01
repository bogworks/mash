package endpoint

import (
	"fmt"
	"strings"
)

// WhoFilter narrows a directory listing. Every set field must hold (they AND). Name, Role and Match are
// case-insensitive substrings, Owner is exact, and each Label is "K" (the key is present) or "K=V" (the value is
// exactly V, case-sensitive). Online and Offline are mutually exclusive.
type WhoFilter struct {
	Match, Name, Owner, Role string
	Labels                   []string
	Online, Offline          bool
}

// ParseLabelSpec splits a label filter into its key and, when the spec has "=", its value.
func ParseLabelSpec(spec string) (key, val string, hasVal bool, err error) {
	key, val, hasVal = strings.Cut(spec, "=")
	if key == "" || hasVal && val == "" {
		return "", "", false, fmt.Errorf("mash: label filter %q is not K or K=V", spec)
	}
	return key, val, hasVal, nil
}

// Validate reports a filter that cannot be applied: both Online and Offline, or a malformed label spec.
func (f WhoFilter) Validate() error {
	if f.Online && f.Offline {
		return fmt.Errorf("mash: online and offline are mutually exclusive")
	}
	for _, l := range f.Labels {
		if _, _, _, err := ParseLabelSpec(l); err != nil {
			return err
		}
	}
	return nil
}

// IsZero reports a filter with nothing set.
func (f WhoFilter) IsZero() bool {
	return f.Match == "" && f.Name == "" && f.Owner == "" && f.Role == "" && len(f.Labels) == 0 && !f.Online && !f.Offline
}

// Apply returns the presences that pass every filter, in the input order. A malformed label spec matches nothing,
// as do Online and Offline together; call Validate first to report them.
func (f WhoFilter) Apply(ps []Presence) []Presence {
	out := make([]Presence, 0, len(ps))
	for _, p := range ps {
		if f.matches(p) {
			out = append(out, p)
		}
	}
	return out
}

func containsFold(s, lowerSub string) bool { return strings.Contains(strings.ToLower(s), lowerSub) }

func (f WhoFilter) matches(p Presence) bool {
	r := p.Record
	if f.Online && (!p.Online || f.Offline) || f.Offline && p.Online {
		return false
	}
	if f.Owner != "" && r.Owner != f.Owner {
		return false
	}
	if f.Name != "" && !containsFold(r.Name, strings.ToLower(f.Name)) {
		return false
	}
	if f.Role != "" && !containsFold(r.Role, strings.ToLower(f.Role)) {
		return false
	}
	for _, l := range f.Labels {
		k, v, hasVal, err := ParseLabelSpec(l)
		if err != nil {
			return false
		}
		got, ok := r.Labels[k]
		if !ok || hasVal && got != v {
			return false
		}
	}
	if f.Match != "" {
		m := strings.ToLower(f.Match)
		hit := containsFold(r.Name, m) || containsFold(r.Role, m) || containsFold(r.Note, m)
		for k, v := range r.Labels {
			hit = hit || containsFold(k, m) || containsFold(v, m)
		}
		if !hit {
			return false
		}
	}
	return true
}
