package release

import (
	"os"
	"path/filepath"
	"sort"
	"testing"

	"gopkg.in/yaml.v3"
)

// actionlint reads .github/actionlint.yaml from the repository root by itself. It reports a self-hosted runner label as
// unknown unless that file lists it, so every custom label a workflow names in runs-on must be declared there.

// runsOnLabels adds every string in a runs-on value (a string, or a list of them) to set; self-hosted is built in.
func runsOnLabels(v any, set map[string]bool) {
	switch r := v.(type) {
	case string:
		set[r] = true
	case []any:
		for _, e := range r {
			runsOnLabels(e, set)
		}
	}
}

// collectRunsOn walks a decoded YAML value and gathers the labels of every runs-on key, job level or matrix include.
func collectRunsOn(v any, set map[string]bool) {
	switch n := v.(type) {
	case map[string]any:
		for k, e := range n {
			if k == "runs-on" {
				runsOnLabels(e, set)
			} else {
				collectRunsOn(e, set)
			}
		}
	case []any:
		for _, e := range n {
			collectRunsOn(e, set)
		}
	}
}

func TestActionlintConfig_declaresEveryCustomLabel(t *testing.T) {
	files, err := filepath.Glob("../../.github/workflows/*.yml")
	if err != nil || len(files) == 0 {
		t.Fatalf("no workflows: %v", err)
	}
	used := map[string]bool{}
	for _, f := range files {
		raw, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		var doc any
		if err := yaml.Unmarshal(raw, &doc); err != nil {
			t.Fatalf("%s: not valid YAML: %v", f, err)
		}
		collectRunsOn(doc, used)
	}
	delete(used, "self-hosted")

	raw, err := os.ReadFile("../../.github/actionlint.yaml")
	if err != nil {
		t.Fatalf("actionlint config: %v", err)
	}
	var cfg struct {
		SelfHostedRunner struct {
			Labels []string `yaml:"labels"`
		} `yaml:"self-hosted-runner"`
	}
	if err := yaml.Unmarshal(raw, &cfg); err != nil {
		t.Fatalf(".github/actionlint.yaml: not valid YAML: %v", err)
	}
	declared := map[string]bool{}
	for _, l := range cfg.SelfHostedRunner.Labels {
		declared[l] = true
	}

	var missing []string
	for l := range used {
		if !declared[l] {
			missing = append(missing, l)
		}
	}
	sort.Strings(missing)
	if len(missing) != 0 {
		t.Errorf("runs-on labels not declared under self-hosted-runner.labels in .github/actionlint.yaml: %v", missing)
	}
}
