package policy

import (
	"fmt"
	"strings"
	"testing"
)

func TestNullListItemRefused(t *testing.T) {
	const rule = "  - {name: r, action: allow, from: {}, to: {}}\n"
	const test = "  - {from: a, to: a, expect: allow}\n"
	const eps = "endpoints:\n  a: {}\n"
	for _, tc := range []struct {
		name, src string
		line      int
	}{
		{"empty dash under rules", eps + "rules:\n  -\n", 4},
		{"empty dash at end of file", "rules:\n-", 2},
		{"under tests", eps + "tests:\n  - ~\n", 4},
		{"middle of list", eps + "rules:\n" + rule + "  - ~\n" + rule, 5},
		{"tilde", eps + "rules:\n  - ~\n", 4},
		{"null", eps + "rules:\n  - null\n", 4},
		{"Null", eps + "rules:\n  - Null\n", 4},
		{"NULL", eps + "tests:\n" + test + "  - NULL\n", 5},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := Parse([]byte(tc.src))
			want := fmt.Sprintf("line %d: a list item is null; remove it or give it a value", tc.line)
			if err == nil || !strings.Contains(err.Error(), want) {
				t.Fatalf("err %v, want %q", err, want)
			}
		})
	}
	if _, err := Parse([]byte(eps + "rules:\n" + rule + "tests:\n" + test)); err != nil {
		t.Fatalf("file with no null item: %v", err)
	}
}
