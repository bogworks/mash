package policy

import (
	"slices"
	"strings"
	"testing"
)

func TestImplicitDefaultZoneSelectors(t *testing.T) {
	f := mustParse(t, `
default: allow
endpoints:
  a: {}
  b: {labels: {zone: prod}}
rules:
  - {name: no-default, action: deny, from: {}, to: {zone: default}}
`)
	got := recipients(t, f)
	if !slices.Equal(got["a"], []string{"b"}) || !slices.Equal(got["b"], []string{"b"}) {
		t.Fatalf("a must not be mailable by anyone: %v", got)
	}
	e, err := Explain(f, "b", "a")
	if err != nil || e.Allowed || e.Rule != "no-default" {
		t.Fatalf("explain %+v %v", e, err)
	}
	// Stored labels are untouched.
	if _, ok := f.Endpoints["a"].Labels["zone"]; ok {
		t.Fatal("implicit label written into the file")
	}
	c, _ := Compile(f, "o")
	for _, ce := range c.Endpoints {
		if ce.Name == "a" && len(ce.Labels) != 0 {
			t.Fatalf("compiled labels %v", ce.Labels)
		}
	}

	// Implicit and explicit default behave identically, and the empty selector still matches both.
	for _, labels := range []string{"{}", "{labels: {zone: default}}"} {
		g := mustParse(t, "default: deny\nendpoints:\n  a: "+labels+"\n  b: {labels: {zone: prod}}\nrules:\n"+
			"  - {name: r, action: allow, from: {zone: default}, to: {}}\n  - {name: s, action: allow, from: {}, to: {zone: default}}\n")
		got := recipients(t, g)
		if !slices.Equal(got["a"], []string{"a", "b"}) || !slices.Equal(got["b"], []string{"a"}) {
			t.Errorf("%s: %v", labels, got)
		}
		if w := g.Check().Warnings; len(w) != 0 {
			t.Errorf("%s: unexpected warnings %+v", labels, w)
		}
	}

	// A zone selector with no endpoint in it still warns.
	h := mustParse(t, "endpoints:\n  b: {labels: {zone: prod}}\nrules:\n  - {name: r, action: deny, from: {}, to: {zone: default}}\n")
	if w := h.Check().Warnings; len(w) != 1 || !strings.Contains(w[0].Message, "matches no endpoint") {
		t.Errorf("warnings %+v", w)
	}

	// Another subject label.
	k := mustParse(t, `
subject_label: tier
default: allow
endpoints:
  a: {}
  b: {labels: {tier: gold}}
rules:
  - {name: r, action: deny, from: {}, to: {tier: default}}
`)
	if e, _ := Explain(k, "b", "a"); e.Allowed || e.Rule != "r" {
		t.Fatalf("tier: %+v", e)
	}
	if w := k.Check().Warnings; len(w) != 0 {
		t.Errorf("tier warnings %+v", w)
	}
}
