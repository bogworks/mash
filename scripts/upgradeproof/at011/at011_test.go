package main

import (
	"reflect"
	"testing"
)

func TestPageStepsAndOrder(t *testing.T) {
	page := "# Compatibility\n\n## Upgrading from 0.5.10\n\n1. Run `mash version` and `mash doctor`.\n2. Install both programs.\n3. Update the plugin.\n4. Restart each session.\n5. Run `mash doctor` again.\n\n## Other\n\n1. not this\n"
	steps := pageSteps(page)
	if len(steps) != 5 {
		t.Fatalf("steps = %q", steps)
	}
	order, unknown := orderFrom(steps)
	if len(unknown) != 0 || !reflect.DeepEqual(order, fallbackOrder) {
		t.Fatalf("order %v unknown %v", order, unknown)
	}
	if got := pageSteps("# Upgrading\n\nSome prose, no list.\n"); len(got) != 0 {
		t.Fatalf("prose is not steps: %q", got)
	}
}

func TestParseHook(t *testing.T) {
	out := `{"hookSpecificOutput":{"additionalContext":"--- #7 from acme.demo.bob [verified] at 2026-11-26T10:00:00Z\ninstance:x\n> hello [tok:d01]\n--- #9 from acme.demo.bob [verified] at t\n> two [tok:p01]","hookEventName":"PostToolUse"}}`
	got := parseHook(out)
	want := []shown{{7, "acme.demo.bob", "d01"}, {9, "acme.demo.bob", "p01"}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %+v", got)
	}
	if len(parseHook("")) != 0 {
		t.Fatal("empty output shows nothing")
	}
}

func TestRowsFrom(t *testing.T) {
	rows, err := rowsFrom(`{"address":"a","total":3,"messages":[{"number":1,"from":"acme.demo.bob","trust":"verified"},{"number":2,"trust":"refused","reason":"time not in UTC"},{"number":3,"from":"x","trust":"forged"}]}`)
	if err != nil || len(rows) != 3 {
		t.Fatalf("%v %v", rows, err)
	}
	if rows[0].Refused || !rows[1].Refused || rows[1].Reason != "time not in UTC" || rows[2].Refused {
		t.Fatalf("%+v", rows)
	}
	if _, err := rowsFrom("no json here"); err == nil {
		t.Fatal("text is not a listing")
	}
}

func TestSumsLine(t *testing.T) {
	h := "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	if d, ok := sumsLine(h+"  mash-linux-amd64\n"+h+" *x\n", "x"); !ok || d != h {
		t.Fatal("binary-mode line")
	}
	if _, ok := sumsLine(h+"  mash-linux-amd64\n", "mashctl-linux-amd64"); ok {
		t.Fatal("other name")
	}
}
