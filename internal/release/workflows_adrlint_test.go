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

// adr-lint.yml checks out a pull request's code (a fork's too) in a pull_request_target run, which actions/checkout v7
// refuses unless allow-unsafe-pr-checkout is set. This is the one deliberate exception. These guards pin its safe shape:
// exactly one checkout sets it, in the step that fetches the PR head sha into pr/ with no persisted credentials; no
// step in any other workflow sets it; and no run step of adr-lint.yml executes anything from pr/ (it is data only).

const adrLintWorkflow = "adr-lint.yml"

// prHeadRef is the ref expression that names the pull request's head commit.
const prHeadRef = "${{ github.event.pull_request.head.sha }}"

// execFromPR finds a command that runs something from pr/ (a script, a binary, a Go package, a directory change into
// it, a sourced file), or a command word that is a path under pr/. Passing pr/ as data (git -C pr, --root .../pr) is fine.
var execFromPR = regexp.MustCompile(
	`\./pr(/|\s|$)` +
		`|\b(bash|sh|zsh|source|exec|python3?|node)\s+"?(\$\{?GITHUB_WORKSPACE\}?/)?pr/` +
		`|(^|[;&|(]\s*)\.\s+"?(\$\{?GITHUB_WORKSPACE\}?/)?pr/` +
		`|\bgo\s+(run|build|test|generate|install|vet)\b[^\n]*\s\./pr\b` +
		`|\bcd\s+"?(\$\{?GITHUB_WORKSPACE\}?/)?pr(/|"|\s|$)` +
		`|^\s*"?\$\{?GITHUB_WORKSPACE\}?/pr/`)

func isTrue(v any) bool {
	switch b := v.(type) {
	case bool:
		return b
	case string:
		return strings.TrimSpace(b) == "true"
	}
	return false
}

func readWorkflow(t *testing.T, name string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("../../.github/workflows", name))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// unsafeCheckoutViolations lists what is wrong with the allow-unsafe-pr-checkout use in one workflow file's text.
// wantOne is true for adr-lint.yml, where exactly one safe step must set it, and false for every other file, where none may.
func unsafeCheckoutViolations(name, text string, wantOne bool) []string {
	var w workflow
	if err := yaml.Unmarshal([]byte(text), &w); err != nil {
		return []string{fmt.Sprintf("%s: not valid YAML: %v", name, err)}
	}
	var out []string
	n := 0
	for id, j := range w.Jobs {
		for i, s := range j.Steps {
			if _, set := s.With["allow-unsafe-pr-checkout"]; !set {
				continue
			}
			n++
			where := fmt.Sprintf("%s job %s step %d", name, id, i+1)
			if !wantOne {
				out = append(out, where+": sets allow-unsafe-pr-checkout; only "+adrLintWorkflow+" may")
				continue
			}
			if !strings.HasPrefix(s.Uses, "actions/checkout@") {
				out = append(out, where+": allow-unsafe-pr-checkout on a step that is not actions/checkout")
			}
			if !isTrue(s.With["allow-unsafe-pr-checkout"]) {
				out = append(out, where+": allow-unsafe-pr-checkout is not true")
			}
			if p, _ := s.With["path"].(string); p != "pr" {
				out = append(out, fmt.Sprintf("%s: allow-unsafe-pr-checkout step has path %q, want pr", where, p))
			}
			if r, _ := s.With["ref"].(string); r != prHeadRef {
				out = append(out, fmt.Sprintf("%s: allow-unsafe-pr-checkout step has ref %q, want the pull request head sha %s", where, r, prHeadRef))
			}
			if pc, ok := s.With["persist-credentials"]; !ok || fmt.Sprint(pc) != "false" {
				out = append(out, where+": allow-unsafe-pr-checkout step must set persist-credentials: false")
			}
		}
	}
	if wantOne && n != 1 {
		out = append(out, fmt.Sprintf("%s: %d steps set allow-unsafe-pr-checkout, want exactly 1", name, n))
	}
	return out
}

// execFromPRViolations lists the steps of one workflow's text that would execute something from the pr/ checkout.
func execFromPRViolations(name, text string) []string {
	var w workflow
	if err := yaml.Unmarshal([]byte(text), &w); err != nil {
		return []string{fmt.Sprintf("%s: not valid YAML: %v", name, err)}
	}
	var out []string
	for id, j := range w.Jobs {
		for i, s := range j.Steps {
			where := fmt.Sprintf("%s job %s step %d", name, id, i+1)
			if strings.HasPrefix(s.Uses, "./pr") {
				out = append(out, where+": uses a local action from pr/")
			}
			if s.Run == "" {
				continue
			}
			if wd := strings.TrimSuffix(s.WorkingDirectory, "/"); wd == "pr" || strings.HasPrefix(wd, "pr/") ||
				strings.HasSuffix(wd, "/pr") || strings.Contains(wd, "/pr/") {
				out = append(out, where+": runs a script with working-directory in pr/")
			}
			for _, line := range strings.Split(s.Run, "\n") {
				if code := stripComment(line); execFromPR.MatchString(code) {
					out = append(out, fmt.Sprintf("%s: run line executes from pr/: %s", where, strings.TrimSpace(code)))
				}
			}
		}
	}
	return out
}

func TestADRLint_unsafeCheckoutIsTheOneDataOnlyPullRequestCheckout(t *testing.T) {
	var text string
	for name := range all(t) {
		b := readWorkflow(t, name)
		if name == adrLintWorkflow {
			text = b
			continue
		}
		if v := unsafeCheckoutViolations(name, b, false); len(v) > 0 {
			t.Errorf("%s", strings.Join(v, "\n"))
		}
	}
	if text == "" {
		t.Fatal("no " + adrLintWorkflow)
	}
	if v := unsafeCheckoutViolations(adrLintWorkflow, text, true); len(v) > 0 {
		t.Errorf("%s", strings.Join(v, "\n"))
	}
}

func TestADRLint_noRunStepExecutesFromThePullRequestCheckout(t *testing.T) {
	if v := execFromPRViolations(adrLintWorkflow, readWorkflow(t, adrLintWorkflow)); len(v) > 0 {
		t.Errorf("%s", strings.Join(v, "\n"))
	}
}

func TestADRLint_unsafeCheckoutGuardsRefuseBadText(t *testing.T) {
	const good = `
jobs:
  a:
    steps:
      - uses: actions/checkout@abc
        with:
          ref: ${{ github.event.pull_request.head.sha }}
          path: pr
          persist-credentials: false
          allow-unsafe-pr-checkout: true
`
	step := func(with string) string {
		return "jobs:\n  a:\n    steps:\n      - uses: actions/checkout@abc\n        with:\n" + with
	}
	const head = "          ref: ${{ github.event.pull_request.head.sha }}\n"
	cases := []struct {
		name    string
		text    string
		wantOne bool
		want    string // substring of a violation; empty means none
	}{
		{"good", good, true, ""},
		{"another file sets it", good, false, "only adr-lint.yml may"},
		{"none in adr-lint", step(head + "          path: pr\n          persist-credentials: false\n"), true, "want exactly 1"},
		{"two in adr-lint", good + "      - uses: actions/checkout@abc\n        with:\n          path: x\n          allow-unsafe-pr-checkout: true\n", true, "want exactly 1"},
		{"wrong path", step(head + "          path: trusted\n          persist-credentials: false\n          allow-unsafe-pr-checkout: true\n"), true, "want pr"},
		{"base ref", step("          ref: ${{ github.event.pull_request.base.sha }}\n          path: pr\n          persist-credentials: false\n          allow-unsafe-pr-checkout: true\n"), true, "head sha"},
		{"credentials persisted", step(head + "          path: pr\n          allow-unsafe-pr-checkout: true\n"), true, "persist-credentials: false"},
		{"set to false", step(head + "          path: pr\n          persist-credentials: false\n          allow-unsafe-pr-checkout: false\n"), true, "is not true"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			v := strings.Join(unsafeCheckoutViolations("x.yml", c.text, c.wantOne), "\n")
			if c.want == "" && v != "" {
				t.Fatalf("unexpected violations: %s", v)
			}
			if c.want != "" && !strings.Contains(v, c.want) {
				t.Fatalf("violations %q lack %q", v, c.want)
			}
		})
	}
}

func TestADRLint_execGuardRefusesRunningFromPR(t *testing.T) {
	run := func(line string) string {
		return "jobs:\n  a:\n    steps:\n      - run: |\n          " + line + "\n"
	}
	for _, ok := range []string{
		`git -C pr fetch --no-tags "$GITHUB_WORKSPACE/trusted" x`,
		`"$RUNNER_TEMP/adrlint" docs --root "$GITHUB_WORKSPACE/pr" --base origin/main`,
		`git -C "$GITHUB_WORKSPACE/pr" diff --name-only a...HEAD > "$RUNNER_TEMP/files"`,
		`# ./pr/run.sh is never run`,
	} {
		if v := execFromPRViolations("x.yml", run(ok)); len(v) > 0 {
			t.Errorf("%q wrongly refused: %v", ok, v)
		}
	}
	for _, bad := range []string{
		`./pr/scripts/x.sh`, `bash pr/x.sh`, `sh pr/x.sh`, `source pr/env`, `. pr/env`, `go run ./pr/cmd/x`,
		`cd pr`, `cd "$GITHUB_WORKSPACE/pr" && make`, `"$GITHUB_WORKSPACE/pr/bin/x"`, `bash "$GITHUB_WORKSPACE/pr/x.sh"`,
	} {
		if v := execFromPRViolations("x.yml", run(bad)); len(v) == 0 {
			t.Errorf("%q not refused", bad)
		}
	}
	wd := "jobs:\n  a:\n    steps:\n      - working-directory: pr\n        run: make\n"
	if v := execFromPRViolations("x.yml", wd); len(v) == 0 {
		t.Error("working-directory pr not refused")
	}
}
