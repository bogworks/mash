package policyfile

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func loadText(t *testing.T, text string) *Doc {
	t.Helper()
	p := filepath.Join(t.TempDir(), "policy.yaml")
	if err := os.WriteFile(p, []byte(text), 0o644); err != nil {
		t.Fatal(err)
	}
	return load(t, p)
}

// refused asserts err is the flow-style refusal naming the line, and that the doc still holds orig.
func refused(t *testing.T, d *Doc, orig string, err error, line string) {
	t.Helper()
	if err == nil || !strings.Contains(err.Error(), ": line "+line+": ") || !strings.Contains(err.Error(), "is written in flow style ({...}); edit it by hand or rewrite it in block style") {
		t.Fatalf("want a flow-style refusal at line %s, got %v", line, err)
	}
	if b, _ := d.Bytes(); string(b) != orig {
		t.Fatalf("doc changed:\n%s", b)
	}
}

func TestFlowEditsAreRefused(t *testing.T) {
	const inline = "default: allow\nendpoints: {a: {}, b: {}}\nrules: []\n"
	d := loadText(t, inline)
	_, err := d.RemoveEndpoint("a")
	refused(t, d, inline, err, "2")
	refused(t, d, inline, d.SetLabels("a", map[string]string{"net": "x"}, nil), "2")
	refused(t, d, inline, d.AddEndpoint("c", nil), "2")

	// a flow entry and flow labels inside a flow endpoints map
	const nested = "default: allow\nendpoints: {a: {labels: {net: prod}}, b: {labels: {net: dev}}}\nrules: []\n"
	d = loadText(t, nested)
	refused(t, d, nested, d.SetLabels("a", map[string]string{"tier": "1"}, nil), "2")
	refused(t, d, nested, d.SetLabels("a", nil, []string{"net"}), "2")
	_, err = d.RemoveEndpoint("b")
	refused(t, d, nested, err, "2")

	// two endpoints on one line of a multi-line flow map
	const multi = "default: allow\nendpoints: {\n  a: {labels: {net: prod}}, b: {},\n  c: {}\n}\nrules: []\n"
	d = loadText(t, multi)
	_, err = d.RemoveEndpoint("b")
	refused(t, d, multi, err, "2")

	// a flow top level
	const top = "{default: allow, endpoints: {a: {}}, rules: []}\n"
	d = loadText(t, top)
	_, err = d.RemoveEndpoint("a")
	refused(t, d, top, err, "1")
}

func TestBlockStyleStillEdits(t *testing.T) {
	// a flow entry and flow labels on their own lines of a block map are replaced node by node
	const text = "default: allow\nendpoints:\n  a: {labels: {net: prod}}  # keep\n  b:\n    labels: {net: dev, tier: \"1\"}\n  c: {}\nrules: []\n"
	d := loadText(t, text)
	if err := d.SetLabels("a", map[string]string{"tier": "2"}, nil); err != nil {
		t.Fatal(err)
	}
	if err := d.SetLabels("b", nil, []string{"net"}); err != nil {
		t.Fatal(err)
	}
	if _, err := d.RemoveEndpoint("c"); err != nil {
		t.Fatal(err)
	}
	b, _ := d.Bytes()
	want := "default: allow\nendpoints:\n  a: {labels: {net: prod, tier: \"2\"}}  # keep\n  b:\n    labels: {tier: \"1\"}\nrules: []\n"
	if string(b) != want {
		t.Fatalf("%q", b)
	}
}

func TestPostConditionCatchesFaultySplice(t *testing.T) {
	const text = "default: allow\nendpoints:\n  a:\n    labels: {net: prod}\n  b:\n    labels: {net: dev}\nrules: []\n"
	defer func() { afterSplice = nil }()
	replace := func(old, new string) func([]byte) []byte {
		return func(b []byte) []byte { return []byte(strings.Replace(string(b), old, new, 1)) }
	}
	corruptions := map[string]func([]byte) []byte{
		"other endpoint lost":  replace("  b:\n    labels: {net: dev}\n", ""),
		"other label changed":  replace("net: dev", "net: DEV"),
		"rest of file changed": replace("default: allow", "default: deny"),
		"extra endpoint":       replace("  b:\n", "  bb: {}\n  b:\n"),
		"result is not a YAML": func(b []byte) []byte { return append(b, "\n  : : [\n"...) },
	}
	for name, corrupt := range corruptions {
		afterSplice = corrupt
		d := loadText(t, text)
		ops := map[string]func() error{
			"add":    func() error { return d.AddEndpoint("c", map[string]string{"net": "x"}) },
			"set":    func() error { return d.SetLabels("a", map[string]string{"tier": "1"}, nil) },
			"remove": func() error { _, err := d.RemoveEndpoint("a"); return err },
		}
		for op, run := range ops {
			err := run()
			if err == nil || !strings.Contains(err.Error(), "internal error") {
				t.Fatalf("%s/%s: want an internal error, got %v", name, op, err)
			}
			if b, _ := d.Bytes(); string(b) != text {
				t.Fatalf("%s/%s: doc changed:\n%s", name, op, b)
			}
		}
	}
}

type labelsByName = map[string]map[string]string

func TestEditsAreIntendedOrRefused(t *testing.T) {
	files := map[string]string{
		"flow-inline":   "default: allow\nendpoints: {a: {labels: {net: prod}}, b: {}}\nrules: []\n",
		"flow-multi":    "default: allow\nendpoints: {\n  a: {labels: {net: prod}},\n  b: {}\n}\nrules: []\n",
		"flow-top":      "{default: allow, endpoints: {a: {}}, rules: []}\n",
		"empty-flow":    "default: allow\nendpoints: {}\nrules: []\n",
		"null":          "default: allow\nendpoints:\nrules: []\n",
		"none":          "default: allow\nrules: []\n",
		"no-newline":    "default: allow\nendpoints:\n  a:\n    labels:\n      net: prod\n  b: {}",
		"mixed":         "default: allow\nendpoints:\n  a: {labels: {net: prod, tier: \"1\"}}  # c\n  b:\n    labels: {net: dev}\n  # note\n  c:\n    labels:\n      net: x   # z\n      team: y\n  d:   # none\nrules: []\n",
		"multiline-q":   "default: allow\nendpoints:\n  a:\n    labels:\n      team: \"p\n        q\"\n  b: {}\nrules: []\n",
		"multiline-p":   "default: allow\nendpoints:\n  a:\n    labels:\n      team: p\n        q\n  b: {}\nrules: []\n",
		"anchors":       "default: allow\nendpoints:\n  a: &x\n    labels: {net: p}\n  b: *x\nrules: []\n",
		"flow-comment":  "default: allow\nendpoints:\n  a: {labels: {net: p,\n    # }\n    tier: \"1\"}}\n  b: {}\nrules: []\n",
		"labels-tilde":  "default: allow\nendpoints:\n  a:\n    labels: ~\n  b: {}\nrules: []\n",
		"blank-between": "default: allow\nendpoints:\n  a:\n    labels: {net: p}\n\n  # between\n\n  b:\n    labels:\n      net: q\n\nrules: []\n",
	}
	type opcase struct {
		name string
		run  func(d *Doc) error
		want func(m labelsByName)
	}
	ops := []opcase{
		{"add", func(d *Doc) error { return d.AddEndpoint("zz", map[string]string{"net": "n"}) },
			func(m labelsByName) { m["zz"] = map[string]string{"net": "n"} }},
		{"add-bare", func(d *Doc) error { return d.AddEndpoint("zz", nil) },
			func(m labelsByName) { m["zz"] = map[string]string{} }},
		{"set-a", func(d *Doc) error {
			return d.SetLabels("a", map[string]string{"net": "new", "k": "v"}, []string{"tier"})
		},
			func(m labelsByName) {
				delete(m["a"], "tier")
				m["a"]["net"], m["a"]["k"] = "new", "v"
			}},
		{"unset-all-a", func(d *Doc) error { return d.SetLabels("a", nil, []string{"net", "tier", "team"}) },
			func(m labelsByName) { m["a"] = map[string]string{} }},
		{"set-b", func(d *Doc) error { return d.SetLabels("b", map[string]string{"net": "z"}, nil) },
			func(m labelsByName) { m["b"]["net"] = "z" }},
		{"set-d", func(d *Doc) error { return d.SetLabels("d", map[string]string{"net": "z"}, nil) },
			func(m labelsByName) { m["d"]["net"] = "z" }},
		{"remove-a", func(d *Doc) error { _, err := d.RemoveEndpoint("a"); return err },
			func(m labelsByName) { delete(m, "a") }},
		{"remove-b", func(d *Doc) error { _, err := d.RemoveEndpoint("b"); return err },
			func(m labelsByName) { delete(m, "b") }},
		{"remove-c", func(d *Doc) error { _, err := d.RemoveEndpoint("c"); return err },
			func(m labelsByName) { delete(m, "c") }},
	}
	handled := 0
	for fname, text := range files {
		for _, op := range ops {
			t.Run(fname+"/"+op.name, func(t *testing.T) {
				d := loadText(t, text)
				before, err := parseSnap([]byte(text))
				if err != nil {
					t.Fatal(err)
				}
				err = op.run(d)
				b, _ := d.Bytes()
				if err != nil {
					if strings.Contains(err.Error(), "internal error") {
						t.Fatalf("shape reached the post-condition instead of being refused up front: %v", err)
					}
					if string(b) != text {
						t.Fatalf("errored but changed the doc:\n%s", b)
					}
					return
				}
				handled++
				want := before.endpoints
				op.want(want)
				got, err := parseSnap(b)
				if err != nil {
					t.Fatalf("result does not parse: %v\n%s", err, b)
				}
				if !reflect.DeepEqual(got.endpoints, want) {
					t.Fatalf("got %v want %v\n%s", got.endpoints, want, b)
				}
				if !reflect.DeepEqual(got.rest, before.rest) {
					t.Fatalf("rest changed\n%s", b)
				}
			})
		}
	}
	if handled == 0 {
		t.Fatal("nothing was handled")
	}
}
