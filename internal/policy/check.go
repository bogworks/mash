package policy

import (
	"fmt"
	"sort"
	"strings"

	"github.com/bogworks/mash/internal/config"
	"github.com/bogworks/mash/internal/store"
)

// Diagnostic is one finding of Check; Where says what it is about (and the source line when known).
type Diagnostic struct {
	Where, Message string
}

func (d Diagnostic) String() string {
	if d.Where == "" {
		return d.Message
	}
	return d.Where + ": " + d.Message
}

// Report is the result of Check. A file with Errors must not be compiled.
type Report struct {
	Errors, Warnings []Diagnostic
}

// Check validates the file without compiling it.
//
// Errors: a default other than allow or deny; an endpoint name that is not config.ValidName; labels that break the
// directory's rules (at most store.MaxLabels (8), key a lowercase letter then up to 31 of a-z 0-9 _ . -, value non-empty
// printable text of at most 64 characters with no tab or newline); a rule with an empty or repeated name, an action
// other than allow or deny, a missing from or to (write {} for everyone), or a selector with an invalid key or
// value; a test naming an endpoint the file does not declare or expecting something other than allow or deny.
//
// Warnings: a rule whose from or to selector matches no declared endpoint (a misspelled label matches nothing and
// the pair silently falls to the default); under default deny, an endpoint that can neither send nor receive.
// Labels no rule uses are not reported. Warnings are only computed for a file without errors.
func (f *File) Check() Report {
	var r Report
	errf := func(where, format string, a ...any) {
		r.Errors = append(r.Errors, Diagnostic{where, fmt.Sprintf(format, a...)})
	}

	if f.Default != "" && f.Default != Allow && f.Default != Deny {
		errf("default", "%q is not allow or deny", f.Default)
	}

	names := f.names()
	for _, n := range names {
		where := "endpoint " + n + lineSuffix(f.lines.endpoints[n])
		if !config.ValidName(n) {
			errf(where, "name must be lower-case letters, digits and single hyphens, starting with a letter, 1-48 characters")
		}
		labels := f.Endpoints[n].Labels
		if err := store.ValidLabels(labels); err != nil {
			errf(where, "labels: %s", strings.TrimPrefix(err.Error(), "mash: "))
		}
		for _, k := range sortedKeys(labels) {
			if labels[k] == "" {
				errf(where, "label %s has an empty value", k)
			}
		}
	}

	seen := map[string]int{}
	selectorsOK := make([]bool, len(f.Rules))
	for i, rule := range f.Rules {
		where := fmt.Sprintf("rule %d", i+1)
		if rule.Name != "" {
			where = "rule " + rule.Name
		}
		where += lineSuffix(at(f.lines.rules, i))
		bad := false
		if strings.TrimSpace(rule.Name) == "" || rule.Name != strings.TrimSpace(rule.Name) {
			errf(where, "name must be non-empty, without leading or trailing space")
		} else {
			if err := store.ValidMeta("name", rule.Name); err != nil {
				errf(where, "%s", strings.TrimPrefix(err.Error(), "mash: "))
			}
			if prev, dup := seen[rule.Name]; dup {
				errf(where, "name already used by rule %d", prev)
			}
			seen[rule.Name] = i + 1
		}
		if rule.Action != Allow && rule.Action != Deny {
			errf(where, "action %q is not allow or deny", rule.Action)
		}
		for _, sel := range []struct {
			name string
			s    Selector
		}{{"from", rule.From}, {"to", rule.To}} {
			if sel.s == nil {
				errf(where, "%s is missing (write {} to select every endpoint)", sel.name)
				bad = true
				continue
			}
			for _, k := range sortedKeys(sel.s) {
				if !store.ValidLabelKey(k) {
					errf(where, "%s: label key %q: a letter, then up to 31 of a-z 0-9 _ . -", sel.name, k)
					bad = true
				}
				if v := sel.s[k]; v == "" || store.ValidMeta("value", v) != nil {
					errf(where, "%s: label %s needs a non-empty printable value of at most %d characters", sel.name, k, store.MaxMetaLen)
					bad = true
				}
			}
		}
		selectorsOK[i] = !bad
	}

	for i, t := range f.Tests {
		where := fmt.Sprintf("test %d%s", i+1, lineSuffix(at(f.lines.tests, i)))
		for _, e := range []struct{ field, name string }{{"from", t.From}, {"to", t.To}} {
			if _, ok := f.Endpoints[e.name]; !ok {
				errf(where, "%s %q is not a declared endpoint", e.field, e.name)
			}
		}
		if t.Expect != Allow && t.Expect != Deny {
			errf(where, "expect %q is not allow or deny", t.Expect)
		}
	}

	if len(r.Errors) > 0 {
		return r
	}

	for i, rule := range f.Rules {
		if !selectorsOK[i] {
			continue
		}
		where := "rule " + rule.Name + lineSuffix(at(f.lines.rules, i))
		for _, sel := range []struct {
			name string
			s    Selector
		}{{"from", rule.From}, {"to", rule.To}} {
			matched := false
			for _, n := range names {
				if sel.s.matches(f.Endpoints[n].Labels) {
					matched = true
					break
				}
			}
			if !matched {
				r.Warnings = append(r.Warnings, Diagnostic{where, fmt.Sprintf("%s selector %s matches no endpoint", sel.name, sel.s)})
			}
		}
	}
	if !f.defaultAllow() {
		for _, n := range names {
			if f.touches(n, names) {
				continue
			}
			r.Warnings = append(r.Warnings, Diagnostic{"endpoint " + n + lineSuffix(f.lines.endpoints[n]),
				"default is deny and no allow rule lets it send or receive, so it can never mail or be mailed"})
		}
	}
	sort.SliceStable(r.Warnings, func(i, j int) bool { return r.Warnings[i].Where < r.Warnings[j].Where })
	return r
}

// touches reports whether n sends to or receives from anyone (itself included) under the rules.
func (f *File) touches(n string, names []string) bool {
	for _, o := range names {
		if f.decide(n, o).allowed || f.decide(o, n).allowed {
			return true
		}
	}
	return false
}

func lineSuffix(line int) string {
	if line == 0 {
		return ""
	}
	return fmt.Sprintf(" (line %d)", line)
}

func at(l []int, i int) int {
	if i < len(l) {
		return l[i]
	}
	return 0
}

func sortedKeys(m map[string]string) []string {
	k := make([]string, 0, len(m))
	for key := range m {
		k = append(k, key)
	}
	sort.Strings(k)
	return k
}
