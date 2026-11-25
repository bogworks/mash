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

// Rule A (ADR 0178): label-reset.yml and adr-lint.yml reach one verdict with one checker. Each runs `adrlint keep-label`,
// built from the BASE commit, on a push only, on commits fetched as data. Only the fetch step holds the token; the build and
// the verdict run without it, every step but the last of label-reset.yml is gated on synchronize, and a missing verdict is
// "no" (KEEP and PR_KEEP stay empty). The labels are still read live, never from the event payload.
func TestRuleA_bothWorkflowsRunTheSameKeepLabelVerdict(t *testing.T) {
	const onlyPush = "github.event.action == 'synchronize'"
	for _, c := range []struct{ file, job, checker string }{
		{"label-reset.yml", "label-reset", "$RUNNER_TEMP/adrlint"},
		{"adr-lint.yml", "adr-lint", "$RUNNER_TEMP/adrlint"},
	} {
		t.Run(c.file, func(t *testing.T) {
			steps := load(t, c.file).Jobs[c.job].Steps
			byID := map[string]step{}
			for _, st := range steps {
				if st.ID != "" {
					byID[st.ID] = st
				}
			}
			fetch, ok := byID["fetch-keep"]
			if !ok {
				t.Fatal("no step fetch-keep")
			}
			keep, ok := byID["keep"]
			if !ok {
				t.Fatal("no step keep")
			}
			for id, st := range map[string]step{"fetch-keep": fetch, "keep": keep} {
				if !strings.Contains(st.If, onlyPush) {
					t.Errorf("step %s must run only on a push (%s): %q", id, onlyPush, st.If)
				}
			}
			for _, want := range []string{"keep-label", "--base \"$BASE\"", "--before \"$BEFORE\"", "--after \"$AFTER\"", "--pr \"$PR\"", "--before-checks", "--repo trusted", c.checker} {
				if !strings.Contains(keep.Run, want) {
					t.Errorf("the keep step lacks %q:\n%s", want, keep.Run)
				}
			}
			for env, want := range map[string]string{
				"BASE": "${{ github.event.pull_request.base.sha }}", "BEFORE": "${{ github.event.before }}",
				"AFTER": "${{ github.event.pull_request.head.sha }}",
			} {
				if keep.Env[env] != want || fetch.Env[env] != want {
					t.Errorf("%s must come from %s in the fetch and keep steps, got %q and %q", env, want, fetch.Env[env], keep.Env[env])
				}
			}
			if want := "${{ github.event.pull_request.number }}"; keep.Env["PR"] != want {
				t.Errorf("PR must come from %s in the keep step, got %q", want, keep.Env["PR"])
			}
			// The token is in the fetch step only: the verdict step runs the checker on a pull request's objects without it.
			if fetch.Env["GH_TOKEN"] == "" {
				t.Error("the fetch step needs the token (fetch and check runs)")
			}
			for _, st := range steps {
				if st.ID == "keep" || st.ID == "build-keep" {
					if _, has := st.Env["GH_TOKEN"]; has {
						t.Errorf("step %q must not hold the token", st.Name)
					}
				}
			}
			// The old head's check runs are read (the chain), the commits are fetched as data, and nothing swallows an error as a keep.
			for _, want := range []string{"/check-runs", "fetch -q --no-tags", "keep-label: reset", "fetched=yes"} {
				if !strings.Contains(fetch.Run, want) {
					t.Errorf("the fetch step lacks %q:\n%s", want, fetch.Run)
				}
			}
			if !strings.Contains(keep.Run, "keep=no") || !strings.Contains(keep.Run, `echo "keep=$keep" >> "$GITHUB_OUTPUT"`) {
				t.Errorf("the keep step must default to keep=no and write the verdict to its output:\n%s", keep.Run)
			}
			if !strings.Contains(keep.If, "steps.fetch-keep.outputs.fetched == 'yes'") {
				t.Errorf("the keep step must run only after a successful fetch: %q", keep.If)
			}
		})
	}
}

// label-reset.yml: lead-approved always resets; decision-confirmed resets unless the verdict was exactly yes; the removal
// step always runs, so a failed build or fetch above is a reset; the build step is from the base checkout.
func TestRuleA_labelResetKeepsOnlyDecisionConfirmed(t *testing.T) {
	steps := load(t, "label-reset.yml").Jobs["label-reset"].Steps
	var build, rm step
	var checkouts int
	for _, st := range steps {
		switch {
		case st.ID == "build-keep":
			build = st
		case strings.Contains(st.Name, "Remove lead-approved"):
			rm = st
		case strings.HasPrefix(st.Uses, "actions/checkout@"):
			checkouts++
		}
	}
	if checkouts != 1 {
		t.Errorf("label-reset.yml has %d checkouts, want exactly 1 (the base commit)", checkouts)
	}
	if !strings.Contains(build.Run, "cd trusted && go build") || !strings.Contains(build.Run, "./scripts/adrlint") {
		t.Errorf("the checker must be built from the base checkout (trusted/):\n%s", build.Run)
	}
	if !strings.Contains(build.If, "github.event.action == 'synchronize'") {
		t.Errorf("the build step must run only on a push: %q", build.If)
	}
	if !strings.Contains(rm.If, "always()") {
		t.Errorf("the removal step must always run, so an infrastructure failure resets: %q", rm.If)
	}
	if rm.Env["KEEP"] != "${{ steps.keep.outputs.keep }}" {
		t.Errorf("the removal step must read the verdict from steps.keep.outputs.keep, got %q", rm.Env["KEEP"])
	}
	if !strings.Contains(rm.Run, `[ "${KEEP:-no}" = yes ]`) || !strings.Contains(rm.Run, "LABELS=lead-approved") {
		t.Errorf("only a verdict of exactly yes may narrow the removal, and then only to lead-approved:\n%s", rm.Run)
	}
	if !strings.Contains(rm.Env["LABELS"], "lead-approved") || !strings.Contains(rm.Env["LABELS"], "decision-confirmed") {
		t.Errorf("both labels must be on the removal list by default: %q", rm.Env["LABELS"])
	}
	if rm.Env["GH_TOKEN"] == "" {
		t.Error("the removal step needs the token")
	}
}

// adr-lint.yml hands the same verdict to the checker, which counts decision-confirmed on a synchronize run only for
// exactly yes (scripts/adrlint tests), and still reads the labels from the API, never from the event payload.
func TestRuleA_adrLintPassesTheVerdictAndReadsLabelsLive(t *testing.T) {
	var found bool
	for _, st := range load(t, adrLintWorkflow).Jobs["adr-lint"].Steps {
		if strings.Contains(st.Run, "adrlint\" pr ") {
			found = true
			if st.Env["PR_KEEP"] != "${{ steps.keep.outputs.keep }}" {
				t.Errorf("the pr step must get PR_KEEP from the keep step, got %q", st.Env["PR_KEEP"])
			}
			if !strings.Contains(st.Run, "gh pr view") || strings.Contains(fmt.Sprint(st.Env), "labels") {
				t.Errorf("the labels must be read live with gh pr view, not from the event:\n%s %v", st.Run, st.Env)
			}
		}
	}
	if !found {
		t.Fatal("no step runs adrlint pr")
	}
}
