package policy

import (
	"encoding/json"
	"flag"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

var update = flag.Bool("update", false, "rewrite the golden files in testdata")

// goldenOwner is the owner every golden output is compiled for.
const goldenOwner = "acme"

// explainPairs are the sender-recipient pairs whose explanation each golden file records.
var explainPairs = map[string][][2]string{
	"solo": {
		{"coder", "reviewer"},
		{"coder", "deploy-bot"},
		{"deploy-bot", "deploy-bot"},
	},
	"enterprise": {
		{"web-intake", "billing-agent"},
		{"hr-agent", "hr-agent"},
		{"build-bot", "deploy-tool"},
	},
}

func golden(t *testing.T, name string, got []byte) {
	t.Helper()
	path := filepath.Join("testdata", name)
	if *update {
		if err := os.WriteFile(path, got, 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("%v (run go test ./internal/policy -update)", err)
	}
	if string(want) != string(got) {
		t.Errorf("%s differs from its golden file (run go test ./internal/policy -update to accept)\n--- got\n%s\n--- want\n%s", name, got, want)
	}
}

func TestGolden(t *testing.T) {
	inputs, err := filepath.Glob("testdata/*.yaml")
	if err != nil || len(inputs) < 2 {
		t.Fatalf("golden inputs: %v %v", inputs, err)
	}
	for _, in := range inputs {
		base := strings.TrimSuffix(filepath.Base(in), ".yaml")
		t.Run(base, func(t *testing.T) {
			data, err := os.ReadFile(in)
			if err != nil {
				t.Fatal(err)
			}
			f, err := Parse(data)
			if err != nil {
				t.Fatal(err)
			}
			if r := f.Check(); len(r.Errors) != 0 || len(r.Warnings) != 0 {
				t.Errorf("golden input should be clean: %+v", r)
			}
			for _, tr := range RunTests(f) {
				if !tr.Pass {
					t.Errorf("test %s -> %s: got %s want %s", tr.From, tr.To, tr.Got, tr.Expect)
				}
			}
			c, err := Compile(f, goldenOwner)
			if err != nil {
				t.Fatal(err)
			}
			golden(t, base+".matrix.golden", []byte(c.Matrix()))
			js, err := c.JSON()
			if err != nil {
				t.Fatal(err)
			}
			golden(t, base+".json.golden", js)

			type perms struct {
				Pub     []string `json:"pub"`
				PubDeny []string `json:"pub_deny,omitempty"`
				Sub     []string `json:"sub"`
			}
			type zoneOut struct {
				Wildcards []string `json:"wildcards"`
				Endpoints []string `json:"endpoints"`
				Template  perms    `json:"template"`
			}
			out := struct {
				Zones map[string]zoneOut  `json:"zones"`
				Tags  map[string][]string `json:"tags"`
			}{map[string]zoneOut{}, map[string][]string{}}
			for _, z := range c.Zones {
				out.Zones[z.Name] = zoneOut{Wildcards: z.Wildcards, Endpoints: z.Endpoints, Template: perms{
					Pub:     slices.Sorted(slices.Values([]string(z.Perms.Pub.Allow))),
					PubDeny: slices.Sorted(slices.Values([]string(z.Perms.Pub.Deny))),
					Sub:     slices.Sorted(slices.Values([]string(z.Perms.Sub.Allow))),
				}}
			}
			for _, e := range c.Endpoints {
				out.Tags[e.Name] = e.Tags
			}
			pj, err := json.MarshalIndent(out, "", "  ")
			if err != nil {
				t.Fatal(err)
			}
			golden(t, base+".perms.golden", append(pj, '\n'))

			var ex strings.Builder
			for _, p := range explainPairs[base] {
				x, err := Explain(f, p[0], p[1])
				if err != nil {
					t.Fatal(err)
				}
				ex.WriteString(x.Text + "\n")
			}
			golden(t, base+".explain.golden", []byte(ex.String()))
		})
	}
}
