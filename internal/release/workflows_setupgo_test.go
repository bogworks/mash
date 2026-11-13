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

// No workflow may use the GitHub cache on a self-hosted runner: not actions/cache (or its restore and save halves), and
// not the built-in cache of any actions/setup-* step (a `cache` other than false, or `cache-dependency-path`). To allow
// one, add "<workflow file>" -> "<job id>" -> reason below. An entry needs a reason: say why the job needs the cache.
var cacheAllowlist = map[string]map[string]string{}

func isActionsCache(uses string) bool {
	return strings.HasPrefix(uses, "actions/cache@") || strings.HasPrefix(uses, "actions/cache/restore@") || strings.HasPrefix(uses, "actions/cache/save@")
}

// githubCacheViolations lists each step in one workflow's text that uses the GitHub cache, skipping allowlisted jobs.
func githubCacheViolations(name, text string, allow map[string]map[string]string) []string {
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
		if reason, ok := allow[name][id]; ok && reason != "" {
			continue
		}
		for i, s := range j.Steps {
			if isActionsCache(s.Uses) {
				out = append(out, fmt.Sprintf("%s job %s step %d: %s uses the GitHub cache", name, id, i+1, s.Uses))
			}
			if !strings.HasPrefix(s.Uses, "actions/setup-") {
				continue
			}
			if _, set := s.With["cache-dependency-path"]; set {
				out = append(out, fmt.Sprintf("%s job %s step %d: %s sets cache-dependency-path", name, id, i+1, s.Uses))
			}
			if c, set := s.With["cache"]; set {
				if b, isBool := c.(bool); isBool && !b {
					continue
				}
				if str, isStr := c.(string); isStr && str == "false" {
					continue
				}
				out = append(out, fmt.Sprintf("%s job %s step %d: %s enables its built-in cache (cache: %v)", name, id, i+1, s.Uses, c))
			}
		}
	}
	return out
}

func TestWorkflows_noGitHubCacheOnSelfHostedRunners(t *testing.T) {
	files, err := filepath.Glob("../../.github/workflows/*.yml")
	if err != nil || len(files) == 0 {
		t.Fatalf("no workflows: %v", err)
	}
	for _, f := range files {
		raw, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		for _, v := range githubCacheViolations(filepath.Base(f), string(raw), cacheAllowlist) {
			t.Error(v)
		}
	}
}

func TestGitHubCacheGuard_reportsAndAllowlists(t *testing.T) {
	step := func(uses, with string) string {
		return "jobs:\n  a:\n    steps:\n      - uses: " + uses + "@0123456789012345678901234567890123456789\n        with:\n" + with
	}
	bad := map[string]string{
		"actions/cache":         step("actions/cache", "          path: x\n          key: k\n"),
		"actions/cache/restore": step("actions/cache/restore", "          path: x\n          key: k\n"),
		"actions/cache/save":    step("actions/cache/save", "          path: x\n          key: k\n"),
		"setup-node cache":      step("actions/setup-node", "          cache: npm\n"),
		"cache-dependency-path": step("actions/setup-python", "          cache-dependency-path: r.txt\n"),
	}
	for name, text := range bad {
		if v := githubCacheViolations("bad.yml", text, nil); len(v) == 0 {
			t.Errorf("%s: the finder accepted it", name)
		}
		if v := githubCacheViolations("bad.yml", text, map[string]map[string]string{"bad.yml": {"a": "needs it for X"}}); len(v) != 0 {
			t.Errorf("%s: an allowlisted job was reported: %v", name, v)
		}
		if v := githubCacheViolations("bad.yml", text, map[string]map[string]string{"bad.yml": {"a": ""}}); len(v) == 0 {
			t.Errorf("%s: an allowlist entry without a reason was honoured", name)
		}
	}
	if v := githubCacheViolations("ok.yml", step("actions/setup-go", "          cache: false\n"), nil); len(v) != 0 {
		t.Errorf("cache: false was reported: %v", v)
	}
}
