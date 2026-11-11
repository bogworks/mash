package release

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// On the self-hosted runners actions/setup-go spends minutes restoring and saving the GitHub cache, far more than a
// cold build costs. Every setup-go step must therefore set `cache: false` literally (the YAML boolean or the string
// "false"); a missing key, true and an expression all fail.

// setupGoCacheViolations lists each actions/setup-go step in one workflow's text whose with.cache is not false.
func setupGoCacheViolations(name, text string) []string {
	var w struct {
		Jobs map[string]struct {
			Steps []struct {
				Uses string         `yaml:"uses"`
				With map[string]any `yaml:"with"`
			} `yaml:"steps"`
		} `yaml:"jobs"`
	}
	if err := yaml.Unmarshal([]byte(text), &w); err != nil {
		return []string{fmt.Sprintf("%s: not valid YAML: %v", name, err)}
	}
	var out []string
	for id, j := range w.Jobs {
		for i, s := range j.Steps {
			if !strings.HasPrefix(s.Uses, "actions/setup-go@") {
				continue
			}
			c, set := s.With["cache"]
			if b, isBool := c.(bool); set && isBool && !b {
				continue
			}
			if str, isStr := c.(string); set && isStr && str == "false" {
				continue
			}
			out = append(out, fmt.Sprintf("%s job %s step %d: actions/setup-go has with.cache %v (set: %v), want the literal false", name, id, i+1, c, set))
		}
	}
	return out
}

func TestWorkflows_setupGoNeverUsesTheGitHubCache(t *testing.T) {
	files, err := filepath.Glob("../../.github/workflows/*.yml")
	if err != nil || len(files) == 0 {
		t.Fatalf("no workflows: %v", err)
	}
	for _, f := range files {
		raw, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		for _, v := range setupGoCacheViolations(filepath.Base(f), string(raw)) {
			t.Error(v)
		}
	}
}

func TestSetupGoCacheGuard_catchesEachViolation(t *testing.T) {
	const uses = "      - uses: actions/setup-go@0123456789012345678901234567890123456789\n"
	step := func(with string) string {
		return "jobs:\n  a:\n    steps:\n" + uses + "        with:\n          go-version-file: go.mod\n" + with
	}
	for name, text := range map[string]string{
		"false":        step("          cache: false\n"),
		"string false": step("          cache: \"false\"\n"),
	} {
		if v := setupGoCacheViolations("ok.yml", text); len(v) != 0 {
			t.Errorf("%s: a compliant step was refused: %v", name, v)
		}
	}
	for name, text := range map[string]string{
		"a missing cache": step(""),
		"cache true":      step("          cache: true\n"),
		"an expression":   step("          cache: ${{ matrix.os != 'macos' }}\n"),
		"a string true":   step("          cache: \"true\"\n"),
		"no with block":   "jobs:\n  a:\n    steps:\n" + uses,
	} {
		if v := setupGoCacheViolations("bad.yml", text); len(v) == 0 {
			t.Errorf("%s: the guard accepted it", name)
		}
	}
}
