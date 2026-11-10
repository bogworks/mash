package release

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// ADR 0079: CI and release jobs run on self-hosted runners only, picked by two literal label lists. This guard reads
// every workflow file and fails on a job-level runs-on that is anything else than [self-hosted, rh-linux] or
// [self-hosted, rh-macos] (a reusable-workflow caller job has none), on a runs-on with a fallback (||), on a matrix-driven
// runs-on (${{ matrix.<key> }}) unless every value that matrix supplies is one of those two lists, on a GitHub-hosted
// label (ubuntu-*, macos-*, windows-*) anywhere outside a comment, and on a host name.

// linuxLabels and macosLabels are fmt.Sprint forms of the only two allowed runs-on lists.
const (
	linuxLabels = "[self-hosted rh-linux]"
	macosLabels = "[self-hosted rh-macos]"
)

// hostedLabel finds a GitHub-hosted runner label (ubuntu-*, macos-*, windows-*) that does not continue a longer word,
// so an asset name such as mash-windows-amd64 is not one.
var hostedLabel = regexp.MustCompile(`(^|[^A-Za-z0-9_-])(ubuntu|macos|windows)-[A-Za-z0-9.]+`)

// hostNames are machine names no workflow may carry.
var hostNames = []string{"bf1", "hermes1"}

var matrixRunsOn = regexp.MustCompile(`^\$\{\{\s*matrix\.([A-Za-z0-9_-]+)\s*\}\}$`)

// stripComment drops a whole-line or trailing YAML comment (a # at the start or after a space).
func stripComment(line string) string {
	if strings.HasPrefix(strings.TrimSpace(line), "#") {
		return ""
	}
	if i := strings.Index(line, " #"); i >= 0 {
		return line[:i]
	}
	return line
}

func allowedLabels(v any) bool {
	l, ok := v.([]any)
	if !ok {
		return false
	}
	for _, e := range l {
		if _, isStr := e.(string); !isStr {
			return false
		}
	}
	s := fmt.Sprint(v)
	return s == linuxLabels || s == macosLabels
}

// selfHostedViolations lists what is wrong with one workflow file's text; nothing means it complies.
func selfHostedViolations(name, text string) []string {
	var out []string
	for n, line := range strings.Split(text, "\n") {
		code := stripComment(line)
		if m := hostedLabel.FindString(code); m != "" {
			out = append(out, fmt.Sprintf("%s:%d names a GitHub-hosted label %q outside a comment", name, n+1, strings.TrimLeft(m, " \t\"'[,:")))
		}
		for _, h := range hostNames {
			if strings.Contains(strings.ToLower(code), h) {
				out = append(out, fmt.Sprintf("%s:%d names the host %q", name, n+1, h))
			}
		}
	}
	var w struct {
		Jobs map[string]struct {
			Uses     string `yaml:"uses"`
			RunsOn   any    `yaml:"runs-on"`
			Strategy struct {
				Matrix map[string]any `yaml:"matrix"`
			} `yaml:"strategy"`
		} `yaml:"jobs"`
	}
	if err := yaml.Unmarshal([]byte(text), &w); err != nil {
		return append(out, fmt.Sprintf("%s: not valid YAML: %v", name, err))
	}
	for id, j := range w.Jobs {
		if j.Uses != "" && j.RunsOn == nil {
			continue // a reusable-workflow caller job: the called workflow picks its runners
		}
		switch r := j.RunsOn.(type) {
		case []any:
			if !allowedLabels(r) {
				out = append(out, fmt.Sprintf("%s job %s: runs-on %v is not %s or %s", name, id, r, linuxLabels, macosLabels))
			}
		case string:
			if strings.Contains(r, "||") {
				out = append(out, fmt.Sprintf("%s job %s: runs-on %q has a fallback (||)", name, id, r))
			}
			m := matrixRunsOn.FindStringSubmatch(strings.TrimSpace(r))
			if m == nil {
				out = append(out, fmt.Sprintf("%s job %s: runs-on %q is not a list of the allowed labels or a matrix key", name, id, r))
				break
			}
			out = append(out, matrixValueViolations(name, id, m[1], j.Strategy.Matrix)...)
		default:
			out = append(out, fmt.Sprintf("%s job %s: runs-on is %v, want %s or %s", name, id, j.RunsOn, linuxLabels, macosLabels))
		}
	}
	return out
}

// matrixValueViolations checks every value the matrix supplies for key: the axis list itself and each include entry.
func matrixValueViolations(name, id, key string, matrix map[string]any) []string {
	var out []string
	check := func(where string, v any) {
		if !allowedLabels(v) {
			out = append(out, fmt.Sprintf("%s job %s: matrix %s value %v is not %s or %s", name, id, where, v, linuxLabels, macosLabels))
		}
	}
	supplied := false
	if axis, ok := matrix[key]; ok {
		supplied = true
		vals, isList := axis.([]any)
		if !isList {
			check(key, axis)
		}
		for _, v := range vals {
			check(key, v)
		}
	}
	includes, _ := matrix["include"].([]any)
	for i, e := range includes {
		entry, _ := e.(map[string]any)
		v, ok := entry[key]
		if !ok {
			if _, axis := matrix[key]; !axis {
				out = append(out, fmt.Sprintf("%s job %s: matrix include entry %d has no %s, so runs-on is empty for it", name, id, i, key))
			}
			continue
		}
		supplied = true
		check(fmt.Sprintf("include[%d].%s", i, key), v)
	}
	if !supplied {
		out = append(out, fmt.Sprintf("%s job %s: matrix supplies no %s", name, id, key))
	}
	return out
}

func TestWorkflows_onlySelfHostedRunners(t *testing.T) {
	files, err := filepath.Glob("../../.github/workflows/*.yml")
	if err != nil || len(files) == 0 {
		t.Fatalf("no workflows: %v", err)
	}
	for _, f := range files {
		raw, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		for _, v := range selfHostedViolations(filepath.Base(f), string(raw)) {
			t.Error(v)
		}
	}
}

func TestSelfHostedGuard_catchesEachViolation(t *testing.T) {
	const ok = `jobs:
  a:
    runs-on: [self-hosted, rh-linux]
    steps:
      - run: echo hi # ubuntu-latest in a comment is fine
  # macos-latest in a comment is fine
  b:
    runs-on: ${{ matrix.runs-on }}
    strategy:
      matrix:
        include:
          - {os: linux, runs-on: [self-hosted, rh-linux]}
          - {os: macos, runs-on: [self-hosted, rh-macos]}
  c:
    uses: o/r/.github/workflows/w.yml@v1
`
	if v := selfHostedViolations("ok.yml", ok); len(v) != 0 {
		t.Fatalf("a compliant workflow was refused: %v", v)
	}
	const lin = "jobs:\n  a:\n    runs-on: [self-hosted, rh-linux]\n"
	for name, c := range map[string]struct{ text, want string }{
		"a hosted runs-on":          {"jobs:\n  a:\n    runs-on: ubuntu-latest\n", "ubuntu-latest"},
		"a macos runs-on":           {"jobs:\n  a:\n    runs-on: macos-14\n", "macos-14"},
		"a windows runs-on":         {"jobs:\n  a:\n    runs-on: windows-2022\n", "windows-2022"},
		"a fallback":                {"jobs:\n  a:\n    runs-on: ${{ vars.X || 'y' }}\n", "fallback"},
		"a variable":                {"jobs:\n  a:\n    runs-on: ${{ vars.RUNNER_LINUX }}\n", "not a list"},
		"a bare self-hosted":        {"jobs:\n  a:\n    runs-on: self-hosted\n", "not a list"},
		"a wrong list":              {"jobs:\n  a:\n    runs-on: [self-hosted, linux]\n", "is not"},
		"an extra label":            {"jobs:\n  a:\n    runs-on: [self-hosted, rh-linux, gpu]\n", "is not"},
		"the macOS OS label":        {"jobs:\n  a:\n    runs-on: [self-hosted, macOS, rh-macos]\n", "is not"},
		"a reordered list":          {"jobs:\n  a:\n    runs-on: [rh-linux, self-hosted]\n", "is not"},
		"a group mapping":           {"jobs:\n  a:\n    runs-on: {group: g}\n", "want"},
		"no runs-on":                {"jobs:\n  a:\n    steps: []\n", "want"},
		"a matrix key never set":    {"jobs:\n  a:\n    runs-on: ${{ matrix.r }}\n    strategy:\n      matrix:\n        os: [linux]\n", "supplies no r"},
		"a matrix hosted value":     {"jobs:\n  a:\n    runs-on: ${{ matrix.r }}\n    strategy:\n      matrix:\n        include:\n          - {r: [self-hosted, rh-linux]}\n          - {r: ubuntu-latest}\n", "is not"},
		"a matrix axis wrong value": {"jobs:\n  a:\n    runs-on: ${{ matrix.r }}\n    strategy:\n      matrix:\n        r: [[self-hosted, rh-linux], [self-hosted, other]]\n", "is not"},
		"a matrix entry missing it": {"jobs:\n  a:\n    runs-on: ${{ matrix.r }}\n    strategy:\n      matrix:\n        include:\n          - {r: [self-hosted, rh-linux]}\n          - {os: x}\n", "has no r"},
		"a matrix expression":       {"jobs:\n  a:\n    runs-on: ${{ matrix.r || 'x' }}\n", "fallback"},
		"a label outside a comment": {lin + "    steps:\n      - run: echo ubuntu-22.04\n", "ubuntu-22.04"},
		"a host name":               {lin + "    steps:\n      - run: ssh bf1\n", "bf1"},
	} {
		v := selfHostedViolations("bad.yml", c.text)
		if len(v) == 0 || !strings.Contains(strings.Join(v, "\n"), c.want) {
			t.Errorf("%s: the guard reported %v, want a violation mentioning %q", name, v, c.want)
		}
	}
}
