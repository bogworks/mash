package policy

import (
	"strings"
	"testing"
)

func errTexts(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

func TestDenyRuleSelectingNobodyIsAnError(t *testing.T) {
	src := "default: allow\nendpoints:\n  a: {labels: {zone: red}}\n  b: {labels: {zone: red}}\nrules:\n  - {name: block-typo, action: deny, from: {zoen: red}, to: {}}\n"
	_, err := Parse([]byte(src))
	for _, want := range []string{"rule block-typo (line 6)", "from selector {zoen=red} matches no endpoint", "set allow_empty: true on the rule if the group is intentionally empty"} {
		if !strings.Contains(errTexts(err), want) {
			t.Errorf("error lacks %q: %v", want, err)
		}
	}
	// a to selector too, with the zone hint kept
	_, err = Parse([]byte("endpoints:\n  a: {labels: {zone: red}}\nrules:\n  - {name: r, action: deny, from: {}, to: {zone: ghost}}\n"))
	if !strings.Contains(errTexts(err), "to selector {zone=ghost} matches no endpoint (zone ghost has no endpoints)") {
		t.Errorf("%v", err)
	}
}

func TestAllowRuleSelectingNobodyStaysAWarning(t *testing.T) {
	f := mustParse(t, "endpoints:\n  a: {}\nrules:\n  - {name: r, action: allow, from: {nosuch: v}, to: {}}\n")
	if w := f.Check().Warnings; len(w) != 1 || !strings.Contains(w[0].Message, "matches no endpoint") {
		t.Errorf("%+v", w)
	}
}

func TestAllowEmptyDowngradesADenyToAWarning(t *testing.T) {
	src := "default: allow\nendpoints:\n  a: {labels: {zone: red}}\nrules:\n  - {name: later, action: deny, from: {zone: blue}, to: {}, allow_empty: true}\n"
	f := mustParse(t, src)
	r := f.Check()
	if len(r.Errors) != 0 || len(r.Warnings) != 1 || !strings.Contains(r.Warnings[0].Message, "{zone=blue} matches no endpoint (zone blue has no endpoints)") {
		t.Fatalf("%+v", r)
	}
	if !f.Rules[0].AllowEmpty {
		t.Error("allow_empty not parsed")
	}
	// it changes no permission: the compiled output is the same as for a file without the rule
	c1, err := Compile(f, "o")
	if err != nil {
		t.Fatal(err)
	}
	f2 := mustParse(t, "default: allow\nendpoints:\n  a: {labels: {zone: red}}\n")
	c2, err := Compile(f2, "o")
	if err != nil {
		t.Fatal(err)
	}
	j1, _ := c1.JSON()
	j2, _ := c2.JSON()
	if string(j1) != string(j2) {
		t.Errorf("allow_empty changed the compiled output:\n%s\n%s", j1, j2)
	}
}

func TestZoneLabelIsAtMost32Characters(t *testing.T) {
	zone := func(n int) string { return "z" + strings.Repeat("a", n-1) }
	src := func(z string) string { return "endpoints:\n  a: {labels: {zone: " + z + "}}\n" }
	if _, err := Parse([]byte(src(zone(32)))); err != nil {
		t.Errorf("32 characters refused: %v", err)
	}
	_, err := Parse([]byte(src(zone(33))))
	if err == nil || !strings.Contains(err.Error(), "is the zone, which is one subject token: lower-case letters, digits and single hyphens, starting with a letter, 1-32 characters") {
		t.Errorf("33 characters: %v", err)
	}
	// an endpoint name keeps its 48
	if _, err := Parse([]byte("endpoints:\n  " + zone(48) + ": {}\n")); err != nil {
		t.Errorf("48-character name refused: %v", err)
	}
}
