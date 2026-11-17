package endpoint

import (
	"fmt"
	"slices"
	"strings"

	"github.com/bogworks/mash/internal/sap"
	"github.com/bogworks/mash/internal/store"
)

// WhoFilter narrows a directory listing. Every set field must hold (they AND). Name, Role and Match are
// case-insensitive substrings, Owner is exact, and each Label is "K" (the key is present) or "K=V" (the value is
// exactly V, case-sensitive). Each Cap is an exact capability word (they AND). Match also searches the description and capabilities.
// A Name or Match that is a full address (owner.group.instance or owner_group_instance) matches that endpoint exactly
// instead; otherwise it is a substring of the dotted address. Online and Offline are mutually exclusive.
type WhoFilter struct {
	Match, Name, Owner, Role string
	Labels, Caps             []string
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
	for _, c := range f.Caps {
		if !store.ValidCap(c) {
			return fmt.Errorf("mash: capability filter %q: a letter, then up to 31 of a-z 0-9 _ . -", c)
		}
	}
	return nil
}

// IsZero reports a filter with nothing set.
func (f WhoFilter) IsZero() bool {
	return f.Match == "" && f.Name == "" && f.Owner == "" && f.Role == "" && len(f.Labels) == 0 && len(f.Caps) == 0 && !f.Online && !f.Offline
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

// identityFlat is the flat directory key of arg when arg is a full address, written dotted (owner.group.instance) or flat
// (owner_group_instance), case-insensitively; ok is false for anything else, which filters as a substring of the
// address.
func identityFlat(arg string) (flat string, ok bool) {
	a := strings.ToLower(arg)
	if id, err := sap.ParseAddress(a); err == nil {
		return id.Flat(), true
	}
	if id, err := sap.AddressFromFlat(a); err == nil {
		return id.Flat(), true
	}
	return "", false
}

// flatOf and dottedOf are a record's flat key and dotted address.
func flatOf(r store.Record) string   { return r.Owner + "_" + r.Group + "_" + r.Instance }
func dottedOf(r store.Record) string { return r.Owner + "." + r.Group + "." + r.Instance }

func (f WhoFilter) matches(p Presence) bool {
	r := p.Record
	if f.Online && (!p.Online || f.Offline) || f.Offline && p.Online {
		return false
	}
	if f.Owner != "" && r.Owner != f.Owner {
		return false
	}
	if f.Name != "" {
		if flat, ok := identityFlat(f.Name); ok {
			if flatOf(r) != flat {
				return false
			}
		} else if !containsFold(dottedOf(r), strings.ToLower(f.Name)) {
			return false
		}
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
	for _, c := range f.Caps {
		if !slices.Contains(r.Caps, c) {
			return false
		}
	}
	if f.Match != "" {
		m := strings.ToLower(f.Match)
		hit := containsFold(dottedOf(r), m) || containsFold(r.Role, m) || containsFold(r.Note, m) || containsFold(r.Description, m)
		for _, c := range r.Caps {
			hit = hit || containsFold(c, m)
		}
		for k, v := range r.Labels {
			hit = hit || containsFold(k, m) || containsFold(v, m)
		}
		// --match is the union of the exact identity and the substring search, so a record that merely
		// mentions the identity text still appears.
		if flat, ok := identityFlat(f.Match); ok && flatOf(r) == flat {
			hit = true
		}
		if !hit {
			return false
		}
	}
	return true
}
