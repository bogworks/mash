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

// A run line may mention the pull request's checkout directory pr/ only as data, in one of the explicit forms below.
// This is an allow-list: each allowed form is cut out of the line, and if what is left still mentions pr as a path
// component, the line fails. So `git -C pr fetch && pr/bin/tool` and `make -C pr` are refused, not just known-bad words.

// prDir is the pull request checkout as a path: pr, "pr", $GITHUB_WORKSPACE/pr, "$GITHUB_WORKSPACE/pr" (no trailing path).
const prDir = `(?:"?\$\{?GITHUB_WORKSPACE\}?/pr"?|"?pr"?)`

var (
	// mentionsPR finds pr as a path component anywhere: pr/x, ./pr, /pr, cd pr, -C pr, PATH=pr/bin, "pr", (cd pr; ...).
	mentionsPR = regexp.MustCompile(`(^|[\s"'=:(;&|/])pr($|[\s"'/;&|):])`)

	// allowedPRUses are the data-only uses; the matched text is removed before mentionsPR is applied to the rest.
	allowedPRUses = []*regexp.Regexp{
		// gh pr <subcommand>: the GitHub CLI's pull-request command, whose word pr is not the directory.
		regexp.MustCompile(`\bgh\s+pr\s+(view|list|diff|checks|status)\b`),
		// git -C pr <read-only subcommand>: git only reads or fetches there; never checkout, apply, hook, submodule, config.
		regexp.MustCompile(`\bgit\s+-C\s+` + prDir + `\s+(fetch|diff|log|rev-parse|show)\b`),
		// the trusted checker (built from trusted/) given the checkout as its --root argument: data to the checker.
		regexp.MustCompile(`^\s*"?\$RUNNER_TEMP/adrlint"?\s+(docs|pr)\s+--root\s+` + prDir + `(\s|$)`),
	}
)

// execFromPRLine reports whether one run line (comment stripped) mentions pr/ other than by an allowed data-only use.
func execFromPRLine(code string) bool {
	for _, re := range allowedPRUses {
		code = re.ReplaceAllString(code, " ")
	}
	return mentionsPR.MatchString(code)
}

// runLines splits a run script into logical lines: comments stripped, backslash continuations joined.
func runLines(run string) []string {
	var out []string
	cur := ""
	for _, line := range strings.Split(run, "\n") {
		code := stripComment(line)
		if strings.HasSuffix(strings.TrimRight(code, " \t"), "\\") {
			cur += strings.TrimSuffix(strings.TrimRight(code, " \t"), "\\") + " "
			continue
		}
		out = append(out, cur+code)
		cur = ""
	}
	if cur != "" {
		out = append(out, cur)
	}
	return out
}

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
	// A working-directory anywhere in the file (a job's defaults.run included, which the structs do not model) under pr/.
	for n, line := range strings.Split(text, "\n") {
		if code := stripComment(line); strings.Contains(code, "working-directory") && mentionsPR.MatchString(code[strings.Index(code, "working-directory")+len("working-directory"):]) {
			out = append(out, fmt.Sprintf("%s:%d: working-directory in pr/", name, n+1))
		}
	}
	for id, j := range w.Jobs {
		for i, s := range j.Steps {
			where := fmt.Sprintf("%s job %s step %d", name, id, i+1)
			if strings.HasPrefix(s.Uses, "./pr") {
				out = append(out, where+": uses a local action from pr/")
			}
			if wd := strings.TrimSuffix(s.WorkingDirectory, "/"); mentionsPR.MatchString(wd) || wd == "pr" || strings.HasPrefix(wd, "pr/") {
				out = append(out, where+": working-directory in pr/")
			}
			for _, line := range runLines(s.Run) {
				if execFromPRLine(line) {
					out = append(out, fmt.Sprintf("%s: run line uses pr/ other than as data: %s", where, strings.TrimSpace(line)))
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
		`git -C pr log -1 --format=%H`, `git -C pr rev-parse HEAD`, `git -C pr show HEAD:README.md`,
		`"$RUNNER_TEMP/adrlint" pr --root "$GITHUB_WORKSPACE/pr" --files "$RUNNER_TEMP/files"`,
		`labels=$(gh pr view "$PR" --json labels)`,
		`# ./pr/run.sh is never run`,
	} {
		if v := execFromPRViolations("x.yml", run(ok)); len(v) > 0 {
			t.Errorf("%q wrongly refused: %v", ok, v)
		}
	}
	for _, bad := range []string{
		`./pr/scripts/x.sh`, `bash pr/x.sh`, `sh pr/x.sh`, `source pr/env`, `. pr/env`, `go run ./pr/cmd/x`,
		`cd pr`, `cd "$GITHUB_WORKSPACE/pr" && make`, `"$GITHUB_WORKSPACE/pr/bin/x"`, `bash "$GITHUB_WORKSPACE/pr/x.sh"`,
		`pr/bin/tool`, `pr/bin/tool --flag`, `PATH=pr/bin:$PATH tool`, `PATH="$GITHUB_WORKSPACE/pr/bin:$PATH" tool`,
		`make -C pr`, `make -C "$GITHUB_WORKSPACE/pr" test`, `bash -c 'cd pr && ./x'`, `(cd pr; ./x)`,
		`git -C pr checkout main`, `git -C pr apply x.patch`, `git -C pr submodule update`, `git -C pr config alias.x '!pr/x'`,
		`git -C pr hook run pre-commit`, `git -C pr fetch origin && pr/bin/tool`, `git -C pr -c alias.s='!./x' s`,
		`"$RUNNER_TEMP/adrlint" docs --root pr && pr/bin/tool`, `./adrlint --root pr`, `"$RUNNER_TEMP/other" --root "$GITHUB_WORKSPACE/pr"`,
		"git -C pr \\\n  fetch x && make -C pr",
	} {
		if v := execFromPRViolations("x.yml", run(bad)); len(v) == 0 {
			t.Errorf("%q not refused", bad)
		}
	}
	for _, bad := range []string{
		"jobs:\n  a:\n    defaults:\n      run:\n        working-directory: pr\n    steps:\n      - run: make\n",
		"jobs:\n  a:\n    steps:\n      - working-directory: pr\n        run: make\n",
		"jobs:\n  a:\n    steps:\n      - working-directory: pr/sub\n        run: make\n",
		"jobs:\n  a:\n    steps:\n      - working-directory: ${{ github.workspace }}/pr\n        run: make\n",
		"jobs:\n  a:\n    steps:\n      - uses: ./pr/.github/actions/x\n",
		"jobs:\n  a:\n    steps:\n      - uses: ./pr\n",
	} {
		if v := execFromPRViolations("x.yml", bad); len(v) == 0 {
			t.Errorf("%q not refused", bad)
		}
	}
}

// A label event must never cancel a run for the same head: a cancelled run reports no success and Mergify waits on it
// forever. The group names the head sha (a new head is a new group) and nothing is cancelled in progress.
func TestADRLint_concurrencyNeverCancelsTheSameHead(t *testing.T) {
	var w struct {
		Concurrency struct {
			Group  string `yaml:"group"`
			Cancel any    `yaml:"cancel-in-progress"`
			Queue  string `yaml:"queue"`
		} `yaml:"concurrency"`
	}
	if err := yaml.Unmarshal([]byte(readWorkflow(t, adrLintWorkflow)), &w); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(w.Concurrency.Group, prHeadRef) {
		t.Errorf("concurrency group %q lacks the head sha, so a label event on one head could cancel another run", w.Concurrency.Group)
	}
	if isTrue(w.Concurrency.Cancel) {
		t.Errorf("cancel-in-progress is %v: a label event would cancel the run for the same head", w.Concurrency.Cancel)
	}
	if w.Concurrency.Queue != "max" {
		t.Errorf("concurrency queue is %q, want max: the default single queue cancels a pending run when another event arrives in the group", w.Concurrency.Queue)
	}
}
