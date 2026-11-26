package main

// The x-json of a command (the sample object of its --json) and the --json cases of its x-output are two statements of
// one shape in spec/mash.json and spec/mashctl.json. They are written by hand, so they drift. This test compares them
// for every command that has both: the same keys at every level of nesting (for a list, the keys of its first
// element) and, for each key, null or not null. Values are not compared, since the two examples differ.
//
// A command with several --json cases whose shapes differ on purpose (an error, a dry run) matches if x-json is
// the shape of any one of them.

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

type xjsonCmd struct {
	Name     string
	Commands []xjsonCmd
	Metadata []struct {
		Name  string
		Value json.RawMessage
	}
}

// xjsonShape reduces a decoded JSON value to its shape: an object to a map of its keys' shapes, a list to the shape
// of its first element (nil when empty), null to "null" and any other value to "value".
func xjsonShape(v any) any {
	switch x := v.(type) {
	case map[string]any:
		m := map[string]any{}
		for k, e := range x {
			m[k] = xjsonShape(e)
		}
		return m
	case []any:
		if len(x) == 0 {
			return []any{}
		}
		return []any{xjsonShape(x[0])}
	case nil:
		return "null"
	}
	return "value"
}

// xjsonDiff lists the differences between two shapes, each as a path and what differs.
func xjsonDiff(path string, a, b any) []string {
	switch x := a.(type) {
	case map[string]any:
		y, ok := b.(map[string]any)
		if !ok {
			return []string{fmt.Sprintf("%s: x-json has an object, the case has %s", path, xjsonKind(b))}
		}
		var out []string
		keys := map[string]bool{}
		for k := range x {
			keys[k] = true
		}
		for k := range y {
			keys[k] = true
		}
		var sorted []string
		for k := range keys {
			sorted = append(sorted, k)
		}
		sort.Strings(sorted)
		for _, k := range sorted {
			xa, inA := x[k]
			yb, inB := y[k]
			switch {
			case !inA:
				out = append(out, fmt.Sprintf("%s.%s: only in the case", path, k))
			case !inB:
				out = append(out, fmt.Sprintf("%s.%s: only in x-json", path, k))
			default:
				out = append(out, xjsonDiff(path+"."+k, xa, yb)...)
			}
		}
		return out
	case []any:
		y, ok := b.([]any)
		if !ok {
			return []string{fmt.Sprintf("%s: x-json has a list, the case has %s", path, xjsonKind(b))}
		}
		if len(x) == 1 && len(y) == 1 {
			return xjsonDiff(path+"[0]", x[0], y[0])
		}
		return nil // an empty list shows no element to compare
	}
	if a != b {
		return []string{fmt.Sprintf("%s: x-json is %v, the case is %s", path, a, xjsonKind(b))}
	}
	return nil
}

func xjsonKind(v any) string {
	switch v := v.(type) {
	case map[string]any:
		return "an object"
	case []any:
		return "a list"
	case string:
		return v
	}
	return fmt.Sprint(v)
}

func TestXJSONAgreesWithXOutput(t *testing.T) {
	for _, file := range []string{"mash.json", "mashctl.json"} {
		b, err := os.ReadFile(filepath.Join("..", "..", "spec", file))
		if err != nil {
			t.Fatal(err)
		}
		var doc struct{ Command xjsonCmd }
		if err := json.Unmarshal(b, &doc); err != nil {
			t.Fatalf("spec/%s: %v", file, err)
		}
		compared := 0
		var walk func(path string, c xjsonCmd)
		walk = func(path string, c xjsonCmd) {
			var xj json.RawMessage
			var cases []struct{ Case, Text string }
			for _, m := range c.Metadata {
				switch m.Name {
				case "x-json":
					xj = m.Value
				case "x-output":
					if err := json.Unmarshal(m.Value, &cases); err != nil {
						t.Errorf("spec/%s: %s: x-output: %v", file, path, err)
					}
				}
			}
			if xj != nil {
				var want any
				if err := json.Unmarshal(xj, &want); err != nil {
					t.Errorf("spec/%s: %s: x-json: %v", file, path, err)
				}
				wantShape := xjsonShape(want)
				var tried []string
				matched, sawCase := false, false
				for _, oc := range cases {
					var got map[string]any
					if json.Unmarshal([]byte(oc.Text), &got) != nil || got == nil {
						continue
					}
					sawCase = true
					diff := xjsonDiff("", wantShape, xjsonShape(got))
					if len(diff) == 0 {
						matched = true
						break
					}
					tried = append(tried, fmt.Sprintf("  against the case %q:\n    %s", oc.Case, strings.Join(diff, "\n    ")))
				}
				if sawCase {
					compared++
					if !matched {
						t.Errorf("spec/%s: %q: x-json and the --json x-output differ (keys are listed with a leading dot):\n%s", file, path, strings.Join(tried, "\n"))
					}
				}
			}
			for _, s := range c.Commands {
				walk(strings.TrimSpace(path+" "+s.Name), s)
			}
		}
		for _, c := range doc.Command.Commands {
			walk(c.Name, c)
		}
		if compared == 0 {
			t.Errorf("spec/%s: no command had both an x-json and a --json case: the test checks nothing", file)
		}
	}
}
