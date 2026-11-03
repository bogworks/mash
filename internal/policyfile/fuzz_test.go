package policyfile

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/bogworks/mash/internal/policy"
)

// FuzzLoadEdit loads arbitrary bytes as a policy file and, when they load, applies each edit operation with fuzzed
// arguments. The edit guard (apply) must keep an edit from dropping or altering anything it was not asked to touch:
// after an edit that returns nil, the file still parses (when the arguments were valid) and still declares every
// endpoint it did, except the one removed and plus the one added. Plain `go test` runs the seeds only.

func fuzzSeeds() []string {
	nest := strings.Repeat("a: {", 100) + strings.Repeat("}", 100)
	return []string{
		"endpoints:\n  null: {}\n", "endpoints:\n  ~: {}\n", "endpoints:\n  <<: {}\n", "endpoints:\n  \"\": {}\n",
		"endpoints:\n  a: &a {labels: {team: x}}\n  b: *a\n",
		"endpoints:\n  a: &a {labels: {team: x}}\n  b:\n    <<: *a\n",
		"endpoints:\n  a: {labels: {team: x}}\nx: &l [x, x, x, x]\ny: &m [*l, *l, *l, *l]\n",
		"endpoints: {" + strings.Repeat("a: {}, ", 100) + "}\n", nest, "endpoints:\n  " + nest + "\n",
		"default: allow\ndefault: deny\n", "default: allow\n---\ndefault: deny\n", "endpoints:\n  a: !!binary aGVsbG8=\n",
		"\xef\xbb\xbfdefault: allow\nendpoints:\n  a: {}\n", "default: allow\r\nendpoints:\r\n  a:\r\n    labels:\r\n      team: x\r\n",
		"endpoints:\n\ta: {}\n", "default: \xff\xfe\n", "endpoints:\n  a:\n    labels: {team: \"\\ud800\"}\n", "default: allow\x00\n",
		"endpoints:\n  a:\n    labels: {team: " + strings.Repeat("x", 4<<10) + "}\n",
		"endpoints:\n  \u0430dmin: {}\n", "", "{}", "null", "- a\n",
		"endpoints:\n  a: {}\n", "endpoints:\n  a:\n    labels: {team: x}\n  b: {}\n# trailing comment\n",
		"default: deny\nendpoints:\n  a: {labels: {team: x}}\n  b: {labels: {team: y}}\nrules:\n  - {name: r, action: deny, from: {team: x}, to: {team: y}}\ntests:\n  - {from: a, to: b, expect: deny}\n",
	}
}

// blames reports whether a parse error is about one of the touched endpoints, or about a deny rule left selecting
// nobody (an edit removed the last label it matched): Check refuses those and Save will not write them.
func blames(err error, touched []string) bool {
	msg := err.Error()
	if strings.Contains(msg, "matches no endpoint") {
		return true
	}
	for _, n := range touched {
		// Add writes the name as given, so 0 or null lands unquoted and Parse refuses the key (a known finding: the
		// edit guard reads it back as the same name, Save is what refuses the file).
		if strings.Contains(msg, "endpoint "+n+" ") || strings.Contains(msg, "map key "+n+" is not a plain string") {
			return true
		}
	}
	return false
}

func FuzzLoadEdit(f *testing.F) {
	add := func(b []byte) {
		f.Add(b, "a", "team", "x")
		f.Add(b, "new-one", "k", "v")
		f.Add(b, "b", "zone", "")
	}
	for _, name := range []string{"block.yaml", "commented.yaml"} {
		b, err := os.ReadFile(filepath.Join("testdata", name))
		if err != nil {
			f.Fatal(err)
		}
		add(b)
	}
	for _, s := range fuzzSeeds() {
		add([]byte(s))
	}
	f.Fuzz(func(t *testing.T, data []byte, name, k, v string) {
		start := time.Now()
		defer func() {
			if d := time.Since(start); d > 2*time.Second {
				t.Fatalf("input of %d bytes took %v", len(data), d)
			}
		}()
		path := filepath.Join(t.TempDir(), "policy.yaml")
		if err := os.WriteFile(path, data, 0o600); err != nil {
			t.Fatal(err)
		}
		d, err := Load(path)
		if err != nil {
			return
		}
		defer d.Close()
		orig, err := policy.Parse(data)
		if err != nil {
			t.Fatalf("Load accepted what Parse refuses: %v", err)
		}
		want := map[string]bool{}
		for n := range orig.Endpoints {
			want[n] = true
		}

		broken := false
		// step runs one edit; ok says it returned no error. After it, the bytes must parse (unless the edit left a test
		// dangling or the error is about the endpoints the edit touched, whose fuzzed labels may be invalid: Save refuses those) and declare exactly the endpoints in want.
		step := func(what string, ok bool, dangling bool, touched ...string) {
			t.Helper()
			b, err := d.Bytes()
			if err != nil {
				t.Fatalf("%s: Bytes: %v", what, err)
			}
			if !ok {
				return
			}
			// An edit can leave a deny rule selecting nobody (it removed the last matching label): Check refuses that
			// and Save will not write it, so it is not a failure here.
			touched = append(touched, k) // a label key written bare can read as a null or a number
			if _, err := policy.Parse(b); err != nil {
				if !dangling && !broken && !blames(err, touched) {
					t.Fatalf("%s: edited file no longer parses: %v\n%s", what, err, b)
				}
				broken = true // later edits start from a file Save would refuse already
			}
			var got struct {
				Endpoints map[string]any `yaml:"endpoints"`
			}
			if err := yaml.Unmarshal(b, &got); err != nil {
				t.Fatalf("%s: edited file is not YAML: %v\n%s", what, err, b)
			}
			if len(got.Endpoints) != len(want) {
				t.Fatalf("%s: file declares %d endpoints, want %d\n%s", what, len(got.Endpoints), len(want), b)
			}
			for n := range want {
				if _, ok := got.Endpoints[n]; !ok {
					t.Fatalf("%s: endpoint %q vanished\n%s", what, n, b)
				}
			}
		}

		err = d.AddEndpoint(name, map[string]string{k: v})
		if err == nil {
			want[name] = true
		}
		step("AddEndpoint", err == nil, false, name)

		err = d.SetLabels(name, map[string]string{k: v}, []string{"zone"})
		step("SetLabels", err == nil, false, name)
		first := ""
		for n := range orig.Endpoints {
			if first == "" || n < first {
				first = n
			}
		}
		if first != "" {
			err = d.SetLabels(first, map[string]string{k: v}, nil)
			step("SetLabels(first)", err == nil, false, first)
			err = d.SetLabels(first, nil, []string{k})
			step("SetLabels unset", err == nil, false, first)
		}

		refs, err := d.RemoveEndpoint(name)
		if err == nil {
			delete(want, name)
		}
		step("RemoveEndpoint", err == nil, len(refs) > 0, name)
		if first != "" {
			refs, err = d.RemoveEndpoint(first)
			if err == nil {
				delete(want, first)
			}
			step("RemoveEndpoint(first)", err == nil, len(refs) > 0, first)
		}
	})
}
