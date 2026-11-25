package main

import (
	"fmt"
	"io"
	"slices"
	"strings"

	"github.com/bogworks/mash/internal/admin"
	"github.com/bogworks/mash/internal/cliframe"
	"github.com/bogworks/mash/internal/config"
	"github.com/bogworks/mash/internal/store"
)

const endpointSetUsage = `usage: mashctl endpoint set <address> key=value... [--dry-run] [--json]
  address    the endpoint, as owner.group.name
  key=value  one or more of: role, description, host, note, capability,
             label.<name>
             capability is a list, capability=review,deploy; it replaces
             the whole list
  --dry-run  print what would change, and change nothing
  --json     print the result as JSON
Example:
  mashctl endpoint set acme.demo.bob role=reviewer label.team=blue
`

const endpointUnsetUsage = `usage: mashctl endpoint unset <address> key... [--dry-run] [--json]
  address    the endpoint, as owner.group.name
  key        one or more of: role, description, host, note, capability,
             label.<name>
             capability removes every capability; to remove one, set the
             list again without it
  --dry-run  print what would change, and change nothing
  --json     print the result as JSON
Example:
  mashctl endpoint unset acme.demo.bob host label.team
`

// metaKeys are the keys of `endpoint set` and `endpoint unset`, besides label.<name>.
const metaKeys = "role, description, host, note, capability, label.<name>"

// metaPair is one key=value of `endpoint set`.
type metaPair struct{ key, value string }

// metaEdit is what `endpoint set` or `endpoint unset` was asked to do.
type metaEdit struct {
	set   []metaPair // set
	unset []string   // unset
}

// metaKeyOK reports whether k is a key of the record that set and unset take.
func metaKeyOK(k string) bool {
	switch k {
	case "role", "description", "host", "note", "capability":
		return true
	}
	name, ok := strings.CutPrefix(k, "label.")
	return ok && store.ValidLabelKey(name)
}

// parseMetaPairs reads the key=value arguments of `endpoint set`. A value or key that is wrong is never echoed.
func parseMetaPairs(args []string) ([]metaPair, error) {
	var out []metaPair
	seen := map[string]bool{}
	for i, a := range args {
		k, v, ok := strings.Cut(a, "=")
		if !ok {
			return nil, cliframe.Usagef("argument %d is not key=value (the value is not shown)", i+2).WithHint("mashctl endpoint set --help")
		}
		if !metaKeyOK(k) {
			return nil, cliframe.Usagef("argument %d has a key that cannot be set (the key is not shown): the keys are %s; a label name is a letter, then up to 31 of a-z 0-9 _ . -", i+2, metaKeys).WithHint("mashctl endpoint set --help")
		}
		if v == "" {
			return nil, cliframe.Usagef("%s has no value", k).WithHint("to remove it: mashctl endpoint unset ADDRESS " + k)
		}
		if k == "capability" {
			for _, c := range strings.Split(v, ",") {
				if !store.ValidCap(c) {
					return nil, cliframe.Usagef("capability needs names separated by commas, each a letter, then up to 31 of a-z 0-9 _ . - (the value is not shown)").WithHint("mashctl endpoint set --help")
				}
			}
		}
		if seen[k] {
			return nil, cliframe.Usagef("%s is given twice", k).WithHint("mashctl endpoint set --help")
		}
		seen[k] = true
		out = append(out, metaPair{k, v})
	}
	return out, nil
}

// parseMetaKeys reads the key arguments of `endpoint unset`.
func parseMetaKeys(args []string) ([]string, error) {
	var out []string
	for i, a := range args {
		if strings.Contains(a, "=") {
			return nil, cliframe.Usagef("unset takes keys, not key=value: argument %d has a value (the value is not shown)", i+2).WithHint("mashctl endpoint unset --help")
		}
		if !metaKeyOK(a) {
			return nil, cliframe.Usagef("argument %d is not a key of the record (the key is not shown): the keys are %s", i+2, metaKeys).WithHint("mashctl endpoint unset --help")
		}
		if slices.Contains(out, a) {
			return nil, cliframe.Usagef("%s is given twice", a).WithHint("mashctl endpoint unset --help")
		}
		out = append(out, a)
	}
	return out, nil
}

var endpointSetSpec = epSpec{
	name: "set", usage: endpointSetUsage, flags: []string{"--dry-run"}, min: 2, max: -1, need: "the endpoint address and at least one key=value",
	check: func(in *epInput) error {
		if err := checkAddress(in); err != nil {
			return err
		}
		pairs, err := parseMetaPairs(in.pos[1:])
		in.edit.set = pairs
		return err
	},
}

var endpointUnsetSpec = epSpec{
	name: "unset", usage: endpointUnsetUsage, flags: []string{"--dry-run"}, min: 2, max: -1, need: "the endpoint address and at least one key",
	check: func(in *epInput) error {
		if err := checkAddress(in); err != nil {
			return err
		}
		keys, err := parseMetaKeys(in.pos[1:])
		in.edit.unset = keys
		return err
	},
}

// capList is the sorted capabilities of a comma-separated value, without repeats.
func capList(v string) []string {
	out := strings.Split(v, ",")
	slices.Sort(out)
	return slices.Compact(out)
}

// metaChange is the update an edit makes to a record, the words that say it, and the warnings it gives.
type metaChange struct {
	update   admin.MetaUpdate
	what     []string
	warnings []string
}

func (c metaChange) empty() bool { return len(c.what) == 0 }

// plan works out what the edit changes on rec: only what differs from the record. It checks the labels the endpoint
// would end with, as the directory checks them.
func (e metaEdit) plan(rec store.Record) (metaChange, error) {
	var c metaChange
	u := &c.update
	next := map[string]string{}
	for k, v := range rec.Labels {
		next[k] = v
	}
	field := func(key string, p **string, old, v string) {
		if old != v {
			s := v
			*p = &s
			if v == "" {
				c.what = append(c.what, key)
			} else {
				c.what = append(c.what, key+"="+v)
			}
		}
	}
	for _, p := range e.set {
		switch p.key {
		case "role":
			field("role", &u.Role, rec.Role, p.value)
		case "host":
			field("host", &u.Host, rec.Host, p.value)
		case "description":
			field("description", &u.Description, rec.Description, p.value)
		case "note":
			field("note", &u.Note, rec.Note, p.value)
		case "capability":
			want := capList(p.value)
			have := append([]string(nil), rec.Caps...)
			slices.Sort(have)
			if !slices.Equal(have, want) {
				u.ClearCaps = true
				u.AddCaps = want
				c.what = append(c.what, "capability="+strings.Join(want, ","))
			}
		default:
			name := strings.TrimPrefix(p.key, "label.")
			if old, has := next[name]; !has || old != p.value {
				if u.Labels == nil {
					u.Labels = map[string]string{}
				}
				u.Labels[name] = p.value
				c.what = append(c.what, p.key+"="+p.value)
			}
			next[name] = p.value
		}
	}
	for _, k := range e.unset {
		switch k {
		case "role":
			if rec.Role != "" {
				field("role", &u.Role, rec.Role, "")
			}
		case "host":
			if rec.Host != "" {
				field("host", &u.Host, rec.Host, "")
			}
		case "note":
			if rec.Note != "" {
				field("note", &u.Note, rec.Note, "")
			}
		case "description":
			if rec.Description != "" {
				field("description", &u.Description, rec.Description, "")
			}
		case "capability":
			if len(rec.Caps) > 0 {
				u.ClearCaps = true
				c.what = append(c.what, "capability")
			}
		default:
			name := strings.TrimPrefix(k, "label.")
			if _, has := next[name]; !has {
				c.warnings = append(c.warnings, fmt.Sprintf("%s is not set", k))
				continue
			}
			delete(next, name)
			if u.Labels == nil {
				u.Labels = map[string]string{}
			}
			u.Labels[name] = ""
			c.what = append(c.what, k)
		}
	}
	if err := store.ValidLabels(next); err != nil {
		return c, failErr(err)
	}
	return c, nil
}

// setResultJSON is the JSON of `endpoint set`.
type setResultJSON struct {
	Address string         `json:"address"`
	Set     map[string]any `json:"set"`
	DryRun  bool           `json:"dry_run"`
}

// unsetResultJSON is the JSON of `endpoint unset`.
type unsetResultJSON struct {
	Address string   `json:"address"`
	Unset   []string `json:"unset"`
	DryRun  bool     `json:"dry_run"`
}

// runEndpointSet is `mashctl endpoint set`.
func runEndpointSet(args []string, g cliframe.Global, env func(string) string, lookup config.Lookup, stdout, stderr io.Writer) int {
	return runEp(endpointSetSpec, g, args, env, lookup, stdout, stderr, func(rt *epRuntime) error {
		set := map[string]any{}
		for _, p := range rt.in.edit.set {
			if p.key == "capability" {
				set["capability"] = capList(p.value)
				continue
			}
			set[p.key] = p.value
		}
		return applyMetaEdit(rt, setResultJSON{Address: rt.in.addr.String(), Set: set})
	})
}

// runEndpointUnset is `mashctl endpoint unset`.
func runEndpointUnset(args []string, g cliframe.Global, env func(string) string, lookup config.Lookup, stdout, stderr io.Writer) int {
	return runEp(endpointUnsetSpec, g, args, env, lookup, stdout, stderr, func(rt *epRuntime) error {
		return applyMetaEdit(rt, unsetResultJSON{Address: rt.in.addr.String(), Unset: rt.in.edit.unset})
	})
}

// applyMetaEdit is the body of set and unset: it reads the record, works out the change, and with --dry-run says it,
// else writes it. result is the JSON to print.
func applyMetaEdit(rt *epRuntime, result any) error {
	addr := rt.in.addr
	rec, err := rt.loadRecord(addr)
	if err != nil {
		return err
	}
	c, err := rt.in.edit.plan(rec)
	if err != nil {
		return err
	}
	for _, w := range c.warnings {
		fmt.Fprintf(rt.stderr, "warning: %s\n", w)
	}
	dry := rt.in.flags["--dry-run"]
	if !dry && !c.empty() {
		if _, err := rt.adm.SetMeta(rt.ctx, addr, c.update); err != nil {
			return failErr(err)
		}
	}
	if rt.in.json {
		switch r := result.(type) {
		case setResultJSON:
			r.DryRun = dry
			writeJSON(rt.stdout, r)
		case unsetResultJSON:
			r.DryRun = dry
			writeJSON(rt.stdout, r)
		}
		return nil
	}
	switch {
	case c.empty():
		fmt.Fprintf(rt.stdout, "Nothing to change on endpoint %s\n", addr)
	case dry:
		fmt.Fprintf(rt.stdout, "Would update endpoint %s: %s\nNothing was changed.\n", addr, config.Clean(strings.Join(c.what, ", ")))
	default:
		fmt.Fprintf(rt.stdout, "Updated endpoint %s\n", addr)
	}
	return nil
}
