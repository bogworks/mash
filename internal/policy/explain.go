package policy

import (
	"fmt"
	"strings"
)

// Explanation says whether one sender may mail one recipient and which rule decided.
type Explanation struct {
	Allowed bool
	// Rule is the deciding rule's name; "" when the default decided. Any matching deny beats any allow; among
	// several rules of the deciding kind the first by name decides and Text names the others.
	Rule string
	// Via is "allow rule", "deny rule" or "default".
	Via  string
	Text string
}

// Explain answers why from may or may not mail to. Both must be declared endpoints.
func Explain(f *File, from, to string) (Explanation, error) {
	if err := f.checked(); err != nil {
		return Explanation{}, err
	}
	for _, n := range []string{from, to} {
		if _, ok := f.Endpoints[n]; !ok {
			return Explanation{}, fmt.Errorf("policy: %q is not a declared endpoint", n)
		}
	}
	return explain(f, from, to), nil
}

func explain(f *File, from, to string) Explanation {
	d := f.decide(from, to)
	e := Explanation{Allowed: d.allowed}
	verb := "cannot mail"
	if d.allowed {
		verb = "can mail"
	}
	head := from + " " + verb + " " + to + ": "
	if d.allowRules == nil && d.denyRules == nil {
		e.Via = "default"
		e.Text = head + "no rule matched; default " + f.defaultName()
		return e
	}
	deciding, others, via, other := d.allowRules, d.denyRules, "allow", ""
	if !d.allowed {
		deciding, others, via = d.denyRules, d.allowRules, "deny"
	}
	e.Rule = deciding[0]
	e.Via = via + " rule"
	r := f.rule(e.Rule)
	e.Text = fmt.Sprintf("%srule %s (%s), selected by from %s and to %s", head, e.Rule, via, r.From, r.To)
	if len(deciding) > 1 {
		other = "also " + via + " by " + strings.Join(deciding[1:], ", ")
		e.Text += "; " + other
	}
	if len(others) > 0 {
		kind := "allow"
		if via == "allow" {
			kind = "deny"
		}
		e.Text += fmt.Sprintf("; %s rules also matched: %s", kind, strings.Join(others, ", "))
		if !d.allowed {
			e.Text += " (a deny beats any allow)"
		}
	}
	return e
}

func (f *File) rule(name string) Rule {
	for _, r := range f.Rules {
		if r.Name == name {
			return r
		}
	}
	return Rule{}
}

// TestResult is the outcome of one tests entry.
type TestResult struct {
	Test
	// Got is "allow" or "deny" as the rules decide, or "error" when the test could not be evaluated.
	Got  string
	Pass bool
	Explanation
}

// RunTests evaluates the file's tests section against its rules. A policy with no tests returns none. A file that
// does not validate yields one failing result per test with Got "error" and the problem in Text.
func RunTests(f *File) []TestResult {
	if len(f.Tests) == 0 {
		return nil
	}
	err := f.checked()
	out := make([]TestResult, len(f.Tests))
	for i, t := range f.Tests {
		if err != nil {
			out[i] = TestResult{Test: t, Got: "error", Explanation: Explanation{Text: err.Error()}}
			continue
		}
		x := explain(f, t.From, t.To)
		got := verdict(x.Allowed)
		out[i] = TestResult{Test: t, Got: got, Pass: got == t.Expect, Explanation: x}
	}
	return out
}
