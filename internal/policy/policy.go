// Package policy is mash's policy file: who may mail whom, as labels and rules, compiled into each endpoint's
// publish permissions. Everything here is a pure function of the bytes or values the caller passes in: no NATS
// connection, no files, no clock. The file's schema and semantics are described in docs/policy-file.md.
//
// Semantics in one paragraph: a selector is a set of key=value pairs that must all match an endpoint's labels, and
// the empty selector matches every endpoint. A rule allows or denies mail from the endpoints its from selector
// selects to the endpoints its to selector selects. Rules are a union and their order means nothing: a sender may
// address a recipient when some allow rule matches and no deny rule does; when no rule matches the pair, the file's
// default decides. Today a rule covers mail; prompt and status probes stay open (ADR 0055; they join the policy when the maintainer decides). Self-mail
// follows the same rules as any other pair: a sender is its own recipient when the rules (or the default) say so.
package policy

import (
	"fmt"
	"sort"
	"strings"
)

// Actions and defaults.
const (
	Allow = "allow"
	Deny  = "deny"
)

// File is a parsed policy file.
type File struct {
	// Default is "allow" or "deny": what applies to a sender-recipient pair no rule matches. Parse sets "allow" when
	// the file does not say.
	Default   string              `yaml:"default"`
	Endpoints map[string]Endpoint `yaml:"endpoints"`
	Rules     []Rule              `yaml:"rules"`
	Tests     []Test              `yaml:"tests"`

	lines fileLines
}

// fileLines remembers where things were in the source, for diagnostics. Zero when the File was built by hand.
type fileLines struct {
	rules     []int
	tests     []int
	endpoints map[string]int
}

// Endpoint is one declared endpoint. Its labels are claims by whoever holds the account key.
type Endpoint struct {
	Labels map[string]string `yaml:"labels"`
}

// Selector selects endpoints: every pair must match the endpoint's labels. The empty selector {} selects every
// endpoint. A nil Selector (a rule that left the field out) is invalid, so that forgetting a selector can never
// widen a rule to everyone.
type Selector map[string]string

// Rule allows or denies mail from the endpoints From selects to the endpoints To selects.
type Rule struct {
	Name   string   `yaml:"name"`
	Action string   `yaml:"action"`
	From   Selector `yaml:"from"`
	To     Selector `yaml:"to"`
	// Log asks that what a deny rule refuses be recorded. Nothing records yet, so Check refuses a rule that sets it
	// (ADR 0062); the field stays parsed so the refusal is a clear message, not an unknown-field error.
	Log bool `yaml:"log"`
	// AllowEmpty says a from or to selector of this rule may match no endpoint (a group that is empty on purpose, such
	// as a group with no endpoints yet). Without it a deny rule that selects nobody is a Check error. It is carried
	// through the file and has no effect on the compiled permissions.
	AllowEmpty bool `yaml:"allow_empty"`
}

// Test asserts the decision for one sender-recipient pair; apply refuses a file whose tests fail.
type Test struct {
	From   string `yaml:"from"`
	To     string `yaml:"to"`
	Expect string `yaml:"expect"`
}

// matches reports whether every pair of s is among labels. The empty selector matches everything; a label an
// endpoint does not have never matches.
func (s Selector) matches(labels map[string]string) bool {
	for k, v := range s {
		if got, ok := labels[k]; !ok || got != v {
			return false
		}
	}
	return true
}

// String renders the selector as {k=v, k2=v2} with sorted keys.
func (s Selector) String() string {
	keys := make([]string, 0, len(s))
	for k := range s {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	parts := make([]string, len(keys))
	for i, k := range keys {
		parts[i] = k + "=" + s[k]
	}
	return "{" + strings.Join(parts, ", ") + "}"
}

func (f *File) defaultAllow() bool { return f.Default != Deny }

func (f *File) defaultName() string {
	if f.defaultAllow() {
		return Allow
	}
	return Deny
}

// names returns the declared endpoint names, sorted.
func (f *File) names() []string {
	n := make([]string, 0, len(f.Endpoints))
	for k := range f.Endpoints {
		n = append(n, k)
	}
	sort.Strings(n)
	return n
}

// decision is the outcome for one ordered pair: the rules that matched (names sorted) and the verdict.
type decision struct {
	allowRules, denyRules []string
	allowed               bool
}

// decide applies the rules to from -> to. Both must be declared. Any matching deny beats any matching allow; the
// default applies only when no rule matched.
func (f *File) decide(from, to string) decision {
	fl, tl := f.Endpoints[from].Labels, f.Endpoints[to].Labels
	var d decision
	for _, r := range f.Rules {
		if !r.From.matches(fl) || !r.To.matches(tl) {
			continue
		}
		if r.Action == Deny {
			d.denyRules = append(d.denyRules, r.Name)
		} else {
			d.allowRules = append(d.allowRules, r.Name)
		}
	}
	sort.Strings(d.allowRules)
	sort.Strings(d.denyRules)
	switch {
	case len(d.denyRules) > 0:
		d.allowed = false
	case len(d.allowRules) > 0:
		d.allowed = true
	default:
		d.allowed = f.defaultAllow()
	}
	return d
}

// permits is decide(from, to).allowed, given both endpoints' effective labels, without building the explanation.
func (f *File) permits(fl, tl map[string]string) bool {
	allowed := false
	for _, r := range f.Rules {
		if !r.From.matches(fl) || !r.To.matches(tl) {
			continue
		}
		if r.Action == Deny {
			return false
		}
		allowed = true
	}
	if allowed {
		return true
	}
	return f.defaultAllow()
}

// matrix is the decision for every ordered pair of the declared endpoints (in names order), computed once per class:
// endpoints that every rule's from selector and every rule's to selector treat alike are one class, so a file of
// thousands of endpoints and a handful of distinct label sets costs classes squared, not endpoints squared, calls
// of permits.
type matrix struct {
	cls     []int
	classOK [][]bool
}

func (f *File) matrix(names []string) matrix {
	m := matrix{cls: make([]int, len(names))}
	byKey := map[string]int{}
	var reps []map[string]string
	key := make([]byte, 0, 2*len(f.Rules))
	for i, n := range names {
		key = key[:0]
		labels := f.Endpoints[n].Labels
		for _, r := range f.Rules {
			key = append(key, b2c(r.From.matches(labels)), b2c(r.To.matches(labels)))
		}
		c, ok := byKey[string(key)]
		if !ok {
			c = len(reps)
			byKey[string(key)] = c
			reps = append(reps, labels)
		}
		m.cls[i] = c
	}
	m.classOK = make([][]bool, len(reps))
	for i, from := range reps {
		m.classOK[i] = make([]bool, len(reps))
		for j, to := range reps {
			m.classOK[i][j] = f.permits(from, to)
		}
	}
	return m
}

func b2c(b bool) byte {
	if b {
		return 1
	}
	return 0
}

// allowed reports whether names[i] may address names[j].
func (m matrix) allowed(i, j int) bool { return m.classOK[m.cls[i]][m.cls[j]] }

func verdict(allowed bool) string {
	if allowed {
		return Allow
	}
	return Deny
}

// checked returns an error listing the Check errors, or nil.
func (f *File) checked() error {
	r := f.Check()
	if len(r.Errors) == 0 {
		return nil
	}
	msgs := make([]string, len(r.Errors))
	for i, e := range r.Errors {
		msgs[i] = e.String()
	}
	return fmt.Errorf("policy: %s", strings.Join(msgs, "; "))
}
