package policy

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"gopkg.in/yaml.v3"
)

// Fuzz targets for the policy file parser and compiler. The class they guard is GHSA-cp58: a YAML key the decoder
// drops without a word (null, ~, a merge key) made a deny rule keyed by it vanish. Plain `go test` runs only the
// seeds below; CI and CONTRIBUTING.md say how to fuzz for longer.

// fuzzBudget bounds the work one input may cost.
const fuzzBudget = 2 * time.Second

func nastySeeds() []string {
	nest := func(depth int) string {
		return strings.Repeat("a: {", depth) + strings.Repeat("}", depth)
	}
	// A small billion-laughs: each level holds eight references to the level below.
	bomb := "l0: &l0 [x, x, x, x, x, x, x, x]\n"
	for i := 1; i <= 6; i++ {
		p := "*l" + string(rune('0'+i-1))
		bomb += "l" + string(rune('0'+i)) + ": &l" + string(rune('0'+i)) + " [" + strings.Repeat(p+", ", 7) + p + "]\n"
	}
	return []string{
		"endpoints:\n  null: {}\n", "endpoints:\n  Null: {}\n", "endpoints:\n  NULL: {}\n", "endpoints:\n  ~: {}\n",
		"endpoints:\n  \"\": {}\n", "endpoints:\n  : {}\n", "endpoints:\n  123: {}\n", "endpoints:\n  0x1f: {}\n",
		"endpoints:\n  true: {}\n", "endpoints:\n  yes: {}\n", "endpoints:\n  <<: {}\n", "endpoints:\n  \"null\": {}\n",
		"endpoints:\n  a: &a {labels: {team: x}}\n  b:\n    <<: *a\n",
		"endpoints:\n  a: &a {labels: {team: x}}\n  b: *a\n",
		"x: &a {team: x}\nendpoints:\n  a:\n    labels: *a\nrules:\n  - {name: r, action: deny, from: *a, to: {}}\n",
		"endpoints: &e\n  a: {}\nrules: *e\n",
		bomb, bomb + "endpoints:\n  a:\n    labels: {team: x}\n",
		"rules: [" + strings.Repeat("{name: r, action: allow, from: {}, to: {}}, ", 500) + "]\n",
		"endpoints: {" + strings.Repeat("a: {}, ", 300) + "}\n",
		nest(200), "endpoints:\n  " + nest(200) + "\n",
		"default: allow\ndefault: deny\n", "endpoints:\n  a: {}\n  a: {}\n",
		"endpoints:\n  a:\n    labels: {k: v, k: w}\n",
		"endpoints:\n  a: !!binary aGVsbG8=\n", "endpoints:\n  !!str 5: {}\n", "endpoints:\n  a: !custom {}\n",
		"default: !!str allow\n", "endpoints:\n  a:\n    labels: {team: !!str 5}\n",
		"default: allow\n---\ndefault: deny\n", "---\n---\n", "...\n",
		"\xef\xbb\xbfdefault: allow\nendpoints:\n  a: {}\n",
		"default: allow\r\nendpoints:\r\n  a: {}\r\nrules: []\r\n",
		"endpoints:\n\ta: {}\n", "endpoints:\n  a:\t{}\n",
		"default: \xff\xfe\n", "endpoints:\n  a:\n    labels: {team: \"\\ud800\"}\n", "default: \"\\udc00\"\n",
		"default: allow\x00\n", "\x00", "endpoints:\n  a\x00: {}\n",
		"default: " + strings.Repeat("a", 64<<10) + "\n",
		"endpoints:\n  a:\n    labels: {team: " + strings.Repeat("x", 64<<10) + "}\n",
		"endpoints:\n  " + strings.Repeat("a", 64<<10) + ": {}\n",
		"endpoints:\n  \u0430dmin: {}\n  admin: {}\n", "endpoints:\n  \u0430: {}\n",
		"endpoints:\n  a:\n    labels: {zone: \u0430}\n  b:\n    labels: {zone: a}\n",
		"", "\n", "[]", "{}", "null", "~", "5", "- a\n", "? [a]\n: b\n", "! a\n",
		"subject_label: team\nendpoints:\n  a:\n    labels: {team: x}\n  b: {}\n",
		"default: deny\nendpoints:\n  a: {labels: {team: x}}\n  b: {labels: {team: y}}\nrules:\n  - {name: r, action: allow, from: {team: x}, to: {team: y}}\n  - {name: d, action: deny, from: {team: x}, to: {team: y}}\ntests:\n  - {from: a, to: b, expect: deny}\n",
	}
}

// addPolicySeeds seeds f with the testdata policies, the minimal valid policies and the nasty seeds; owner, when
// given, is the second fuzz argument of each.
func addPolicySeeds(f *testing.F, owner ...string) {
	add := func(b []byte) {
		args := []any{b}
		for _, o := range owner {
			args = append(args, o)
		}
		f.Add(args...)
	}
	files, _ := filepath.Glob(filepath.Join("testdata", "*.yaml"))
	for _, p := range files {
		b, err := os.ReadFile(p)
		if err != nil {
			f.Fatal(err)
		}
		add(b)
	}
	for _, s := range []string{
		"endpoints:\n  a: {}\n",
		"default: deny\nendpoints:\n  a:\n    labels: {team: x}\n  b:\n    labels: {team: y}\nrules:\n  - {name: r, action: allow, from: {team: x}, to: {team: y}}\n",
		"endpoints:\n  a: {labels: {zone: z}}\nrules:\n  - {name: r, action: deny, from: {}, to: {}}\ntests:\n  - {from: a, to: a, expect: deny}\n",
	} {
		add([]byte(s))
	}
	for _, s := range nastySeeds() {
		add([]byte(s))
	}
}

// deref follows an alias to the node it names.
func deref(n *yaml.Node) *yaml.Node {
	for n != nil && n.Kind == yaml.AliasNode {
		n = n.Alias
	}
	return n
}

// items counts the items of a sequence.
func items(v *yaml.Node) int {
	if v.Kind == yaml.SequenceNode {
		return len(v.Content)
	}
	return 0
}

// sourceCounts decodes data independently of Parse and returns the endpoint key texts and the rule and test item
// counts of the top mapping.
func sourceCounts(data []byte) (endpoints []string, rules, tests int, ok bool) {
	var root yaml.Node
	if err := yaml.NewDecoder(strings.NewReader(string(data))).Decode(&root); err != nil {
		return nil, 0, 0, false
	}
	top := deref(&root)
	if top.Kind == yaml.DocumentNode && len(top.Content) == 1 {
		top = deref(top.Content[0])
	}
	if top.Kind != yaml.MappingNode {
		return nil, 0, 0, false
	}
	for i := 0; i+1 < len(top.Content); i += 2 {
		v := deref(top.Content[i+1])
		switch top.Content[i].Value {
		case "endpoints":
			if v.Kind == yaml.MappingNode {
				for j := 0; j+1 < len(v.Content); j += 2 {
					endpoints = append(endpoints, v.Content[j].Value)
				}
			}
		case "rules":
			rules = items(v)
		case "tests":
			tests = items(v)
		}
	}
	return endpoints, rules, tests, true
}

// bounded fails the test when the input takes longer than fuzzBudget; defer it first.
func bounded(t *testing.T, size int, start time.Time) {
	if d := time.Since(start); d > fuzzBudget {
		t.Fatalf("input of %d bytes took %v (limit %v)", size, d, fuzzBudget)
	}
}

func FuzzParse(f *testing.F) {
	addPolicySeeds(f)
	f.Fuzz(func(t *testing.T, data []byte) {
		defer bounded(t, len(data), time.Now())
		file, err := Parse(data)
		if err != nil {
			if err.Error() == "" {
				t.Fatal("Parse returned an empty error")
			}
			return
		}

		// Key-drop invariant (GHSA-cp58): nothing the source declares may vanish in decoding.
		eps, nRules, nTests, ok := sourceCounts(data)
		if !ok {
			t.Fatal("Parse accepted what an independent decode could not read as a mapping")
		}
		if len(file.Endpoints) != len(eps) {
			t.Fatalf("source declares %d endpoint keys %q, Parse kept %d", len(eps), eps, len(file.Endpoints))
		}
		for _, e := range eps {
			if _, ok := file.Endpoints[e]; !ok {
				t.Fatalf("endpoint key %q is in the source and missing from the parsed file", e)
			}
		}
		if len(file.Rules) != nRules {
			t.Fatalf("source has %d rules, Parse kept %d", nRules, len(file.Rules))
		}
		if len(file.Tests) != nTests {
			t.Fatalf("source has %d tests, Parse kept %d", nTests, len(file.Tests))
		}
		if file.Default != Allow && file.Default != Deny {
			t.Fatalf("default is %q", file.Default)
		}
		if r := file.Check(); len(r.Errors) != 0 {
			t.Fatalf("Parse succeeded but Check reports %v", r.Errors)
		}

		c, err := Compile(file, "o")
		if err != nil {
			t.Fatalf("Compile of a parsed file failed: %v", err)
		}
		if len(c.Endpoints) != len(file.Endpoints) {
			t.Fatalf("compiled %d endpoints, file declares %d", len(c.Endpoints), len(file.Endpoints))
		}
		for name := range file.Endpoints {
			if _, ok := c.endpoint(name); !ok {
				t.Fatalf("declared endpoint %q is not compiled", name)
			}
			if _, err := c.Effective(name); err != nil {
				t.Fatalf("Effective(%q): %v", name, err)
			}
		}
		_ = c.Matrix()
		j, err := c.JSON()
		if err != nil {
			t.Fatalf("JSON: %v", err)
		}
		if !json.Valid(j) {
			t.Fatalf("JSON output is not valid JSON: %q", j)
		}
		RunTests(file)

		// Explain agrees with the rules and with the compiled recipients: a deny rule that matches a pair makes it
		// deny, and a pair is allowed exactly when the sender's compiled recipients list the recipient.
		names := file.names()
		if len(names) > 6 {
			names = names[:6]
		}
		for _, a := range names {
			ce, _ := c.endpoint(a)
			for _, b := range names {
				x, err := Explain(file, a, b)
				if err != nil {
					t.Fatalf("Explain(%q,%q): %v", a, b, err)
				}
				al, bl := file.effectiveLabels(a), file.effectiveLabels(b)
				for _, r := range file.Rules {
					if r.Action == Deny && r.From.matches(al) && r.To.matches(bl) && x.Allowed {
						t.Fatalf("deny rule %q matches %q -> %q but Explain allows it", r.Name, a, b)
					}
				}
				var listed bool
				for _, rc := range ce.Recipients {
					listed = listed || rc == b
				}
				if listed != x.Allowed {
					t.Fatalf("%q -> %q: Explain allowed=%v, compiled recipients say %v", a, b, x.Allowed, listed)
				}
			}
		}
	})
}

func FuzzCheckCompile(f *testing.F) {
	addPolicySeeds(f, "o")
	f.Fuzz(func(t *testing.T, data []byte, owner string) {
		defer bounded(t, len(data), time.Now())
		file, err := Parse(data)
		if err != nil {
			return
		}
		file.Check()
		c, err := Compile(file, owner)
		if err != nil {
			if c != nil {
				t.Fatal("Compile returned both a result and an error")
			}
			return
		}
		for _, e := range c.Endpoints {
			if _, err := c.Effective(e.Name); err != nil {
				t.Fatalf("Effective(%q): %v", e.Name, err)
			}
		}
	})
}
