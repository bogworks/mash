package release

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// The rule: CI does no work on a docs-only pull request, and every check name still reports. The heavy jobs skip at job
// level on the changes job's code output; no workflow-level paths filter exists, because a required check that never
// reports stalls the merge queue. scripts/ci-changes is the classifier; docs/branch-protection.md states the rule.

// heavyIf is the job-level condition of every heavy job in ci.yml that gates (test, reproducible): skipped only
// when changes answered code=false, run when changes failed (an empty output), and never run after a cancellation.
const heavyIf = "${{ !cancelled() && needs.changes.outputs.code != 'false' }}"

// reproIf: heavyIf, and not on an ordinary pull request push: it runs on a queue draft and on a push.
const reproIf = "${{ !cancelled() && needs.changes.outputs.code != 'false' && (github.event_name != 'pull_request' || startsWith(github.head_ref, 'mergify/')) }}"

// Every workflow may filter by branch or tag, but none by path: the checks pr-check, adr-lint, label-reset, test (linux),
// reproducible, lint, govulncheck and codeql all come from these files, and a check a path filter keeps from running never
// reports.
func TestDocsOnly_noWorkflowLevelPathFilter(t *testing.T) {
	for name, w := range all(t) {
		on, ok := w.On.(map[string]any)
		if !ok {
			continue
		}
		for event, cfg := range on {
			m, _ := cfg.(map[string]any)
			for _, k := range []string{"paths", "paths-ignore"} {
				if _, has := m[k]; has {
					t.Errorf("%s: on.%s.%s: a workflow-level path filter can keep a required check from ever reporting; skip at job level instead", name, event, k)
				}
			}
		}
	}
}

// Each heavy job needs changes and is skipped by the changes output; every cheap or required job is not.
func TestDocsOnly_heavyJobsSkipOnTheChangesOutput(t *testing.T) {
	heavy := map[string][]string{
		"ci.yml":       {"test", "reproducible", "conformance"},
		"lint.yml":     {"lint"},
		"security.yml": {"govulncheck", "codeql"},
	}
	isHeavy := map[string]bool{}
	for f, jobs := range heavy {
		w := load(t, f)
		for _, jn := range jobs {
			isHeavy[f+"/"+jn] = true
			j, ok := w.Jobs[jn]
			if !ok {
				t.Errorf("%s has no job %s", f, jn)
				continue
			}
			if !strings.Contains(fmt.Sprint(j.Needs), "changes") {
				t.Errorf("%s/%s does not need changes: %v", f, jn, j.Needs)
			}
			if !strings.Contains(j.If, "needs.changes.outputs.code") {
				t.Errorf("%s/%s has no job-level if on needs.changes.outputs.code: %q", f, jn, j.If)
			}
			if strings.Contains(j.If, "== 'true'") && !strings.Contains(j.If, "!= 'false'") {
				t.Errorf("%s/%s skips unless code is exactly true, so a failed changes job would skip real work: %q", f, jn, j.If)
			}
		}
	}
	if got := load(t, "ci.yml").Jobs["test"].If; got != heavyIf {
		t.Errorf("ci.yml/test if = %q, want %q", got, heavyIf)
	}
	if got := load(t, "ci.yml").Jobs["reproducible"].If; got != reproIf {
		t.Errorf("ci.yml/reproducible if = %q, want %q", got, reproIf)
	}
	// Rule 21: no macOS or eph leg runs per change; they live in release.yml (tag) and nightly.yml (schedule on main).
	for _, jn := range []string{"test-eph", "test-macos"} {
		if _, ok := load(t, "ci.yml").Jobs[jn]; ok {
			t.Errorf("ci.yml has job %s: macOS runs on tags only (release.yml) and eph nightly (nightly.yml), never per change", jn)
		}
	}
	// The cheap and required jobs never mention the classifier's verdict: adr-numbers runs on every event, adr-lint and
	// label-reset on every pull request (including a queue draft's own rules), and pr-check always() and only reads it.
	for f, w := range all(t) {
		for jn, j := range w.Jobs {
			if isHeavy[f+"/"+jn] || jn == "changes" || jn == "pr-check" {
				continue
			}
			if strings.Contains(j.If, "needs.changes") || strings.Contains(fmt.Sprint(j.Needs), "changes") {
				t.Errorf("%s/%s depends on changes but is not a listed heavy job", f, jn)
			}
		}
	}
	ci := load(t, "ci.yml")
	if j := ci.Jobs["adr-numbers"]; j.If != "" || j.Needs != nil {
		t.Errorf("adr-numbers must run on every event: %+v", j)
	}
	if got := ci.Jobs["pr-check"].If; got != "always()" {
		t.Errorf("pr-check if = %q, want always()", got)
	}
	if got := load(t, "adr-lint.yml").Jobs["adr-lint"].If; strings.Contains(got, "code") {
		t.Errorf("adr-lint must not depend on the classifier: %q", got)
	}
}

// All three changes jobs run the one classifier on the diff against the base, with the head ref, and write its output.
func TestDocsOnly_everyChangesJobRunsTheSharedClassifier(t *testing.T) {
	for _, f := range []string{"ci.yml", "lint.yml", "security.yml"} {
		j, ok := load(t, f).Jobs["changes"]
		if !ok {
			t.Errorf("%s has no changes job", f)
			continue
		}
		if j.Outputs["code"] != "${{ steps.diff.outputs.code }}" {
			t.Errorf("%s changes outputs %v", f, j.Outputs)
		}
		var run, ref, event string
		for _, st := range j.Steps {
			if st.ID == "diff" {
				run, ref, event = st.Run, st.Env["HEAD_REF"], st.Env["EVENT"]
			}
		}
		for _, want := range []string{"git diff --no-renames --name-only", "bash scripts/ci-changes", `>> "$GITHUB_OUTPUT"`, "set -euo pipefail", `git show "$BASE:scripts/ci-changes"`, `.github/`} {
			if !strings.Contains(run, want) {
				t.Errorf("%s changes diff step lacks %q:\n%s", f, want, run)
			}
		}
		if ref != "${{ github.head_ref }}" || event != "${{ github.event_name }}" {
			t.Errorf("%s changes diff step env: EVENT %q HEAD_REF %q", f, event, ref)
		}
	}
}

// ciChanges runs scripts/ci-changes as the workflows do: paths on stdin, EVENT and HEAD_REF in the environment.
func ciChanges(t *testing.T, event, headRef string, files ...string) string {
	t.Helper()
	cmd := exec.Command("bash", "scripts/ci-changes")
	cmd.Dir = "../.."
	cmd.Env = append(os.Environ(), "EVENT="+event, "HEAD_REF="+headRef)
	cmd.Stdin = strings.NewReader(strings.Join(files, "\n") + "\n")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("%s %q %q: %v %s", event, headRef, files, err, out)
	}
	return strings.TrimSpace(string(out))
}

func TestDocsOnly_classifier(t *testing.T) {
	const pr = "pull_request"
	for _, c := range []struct {
		name, event, ref string
		files            []string
		code             string
	}{
		{"docs and markdown only", pr, "f/x", []string{"docs/faq.md", "README.md", "CONTRIBUTING.md"}, "false"},
		{"plain text under docs", pr, "f/x", []string{"docs/plans/p.txt", "docs/a.rst", "docs/b.adoc", "docs/c.csv", "docs/evidence/x/timeline.log"}, "false"},
		{"an svg under docs is active material (documentation, rules 20 and 21)", pr, "f/x", []string{"docs/images/a.svg"}, "false"},
		{"a cast under docs (documentation, rules 20 and 21)", pr, "f/x", []string{"docs/demo/two-sessions.cast"}, "false"},
		{"a shell script under docs (security-review F1) (documentation, rules 20 and 21)", pr, "f/x", []string{"docs/evidence/2026-11-02-heartbeat-fanout/run.sh"}, "false"},
		{"a python script under docs (documentation, rules 20 and 21)", pr, "f/x", []string{"docs/evidence/x/harness/drv.py"}, "false"},
		{"a data file under docs (documentation, rules 20 and 21)", pr, "f/x", []string{"docs/x/data.json", "docs/x/conf.yaml", "docs/x/c.toml"}, "false"},
		{"a file without an extension under docs (documentation, rules 20 and 21)", pr, "f/x", []string{"docs/x/Makefile"}, "false"},
		{"an ADR only", pr, "f/x", []string{"docs/decisions/0099-x.md", "docs/decisions/INDEX.md"}, "false"},
		{"docs and a Go file", pr, "f/x", []string{"docs/a.md", "cmd/mash/cli.go"}, "true"},
		{"a Go file only", pr, "f/x", []string{"internal/x.go"}, "true"},
		{"scripts only", pr, "f/x", []string{"scripts/gate"}, "true"},
		{"a script beside docs", pr, "f/x", []string{"docs/a.md", "scripts/ci-changes"}, "true"},
		{"a workflow", pr, "f/x", []string{".github/workflows/ci.yml"}, "true"},
		{"markdown under .github (documentation, rules 20 and 21)", pr, "f/x", []string{".github/PULL_REQUEST_TEMPLATE.md"}, "false"},
		{"mergify config", pr, "f/x", []string{".mergify.yml"}, "true"},
		{"go.mod", pr, "f/x", []string{"docs/a.md", "go.mod"}, "true"},
		{"go.sum", pr, "f/x", []string{"go.sum"}, "true"},
		{"markdown under internal (documentation, rules 20 and 21)", pr, "f/x", []string{"internal/foo/README.md"}, "false"},
		{"markdown under cmd (documentation, rules 20 and 21)", pr, "f/x", []string{"cmd/mash/NOTES.md"}, "false"},
		{"markdown under scripts (documentation, rules 20 and 21)", pr, "f/x", []string{"scripts/scrubcheck/inventory.md"}, "false"},
		{"the plugin package (documentation, rules 20 and 21)", pr, "f/x", []string{"plugin/README.md"}, "false"},
		{"test data (documentation, rules 20 and 21)", pr, "f/x", []string{"internal/policy/testdata/a.md", "docs/x/testdata/b.md"}, "false"},
		{"a conformance vector (documentation, rules 20 and 21)", pr, "f/x", []string{"docs/contracts/conformance-vectors.json", "docs/conformance.md"}, "false"},
		{"a change-log fragment (documentation, rules 20 and 21)", pr, "f/x", []string{"changelog.d/a.fixed.md"}, "false"},
		{"the change log (documentation, rules 20 and 21)", pr, "f/x", []string{"CHANGELOG.md"}, "false"},
		{"the branch-protection doc (documentation, rules 20 and 21)", pr, "f/x", []string{"docs/branch-protection.md"}, "false"},
		{"a generated reference page (documentation, rules 20 and 21)", pr, "f/x", []string{"docs/reference/mash-send.md"}, "false"},
		{"the spec", pr, "f/x", []string{"spec/mash.json"}, "true"},
		{"review docs beside the policy (documentation, rules 20 and 21)", pr, "f/x", []string{"docs/review/in-session-loop.md"}, "false"},
		{"a binary asset under docs (documentation, rules 20 and 21)", pr, "f/x", []string{"docs/images/a.PNG"}, "false"},
		{"a Go file under docs (documentation, rules 20 and 21)", pr, "f/x", []string{"docs/evidence/x/main.go"}, "false"},
		{"a rename out of scripts into docs (documentation, rules 20 and 21)", pr, "f/x", []string{"scripts/notes.md", "docs/notes.md"}, "false"},
		{"a file the classifier does not know", pr, "f/x", []string{"Makefile"}, "true"},
		{"a setup skill (documentation, rules 20 and 21)", pr, "f/x", []string{"mash-setup/skills/setup/SKILL.md"}, "false"},
		{"the setup plugin manifest", pr, "f/x", []string{"mash-setup/.claude-plugin/plugin.json"}, "true"},
		{"agent instructions at the root (documentation, rules 20 and 21)", pr, "f/x", []string{"AGENTS.md"}, "false"},
		{"agent instructions in a folder (documentation, rules 20 and 21)", pr, "f/x", []string{"docs/guide/AGENTS.md", "docs/a.md"}, "false"},
		{"a briefing file in a folder (documentation, rules 20 and 21)", pr, "f/x", []string{"docs/x/CLAUDE.md"}, "false"},
		{"a GEMINI file (documentation, rules 20 and 21)", pr, "f/x", []string{"docs/x/GEMINI.md"}, "false"},
		{"a lower-case instruction file (documentation, rules 20 and 21)", pr, "f/x", []string{"docs/x/claude.md"}, "false"},
		{"any SKILL.md (documentation, rules 20 and 21)", pr, "f/x", []string{"docs/anywhere/SKILL.md"}, "false"},
		{"a skills directory (documentation, rules 20 and 21)", pr, "f/x", []string{"docs/skills/notes.md"}, "false"},
		{"a commands directory (documentation, rules 20 and 21)", pr, "f/x", []string{"docs/commands/x.md"}, "false"},
		{"an agents directory (documentation, rules 20 and 21)", pr, "f/x", []string{"docs/agents/INSTALL.md"}, "false"},
		{"a hooks directory (documentation, rules 20 and 21)", pr, "f/x", []string{"docs/hooks/x.md"}, "false"},
		{"a prompts directory (documentation, rules 20 and 21)", pr, "f/x", []string{"docs/prompts/a.md"}, "false"},
		{"a briefings directory (documentation, rules 20 and 21)", pr, "f/x", []string{"briefings/a.md"}, "false"},
		{"a plugins directory (documentation, rules 20 and 21)", pr, "f/x", []string{"plugins/x/README.md"}, "false"},
		{"a .claude-plugin directory (documentation, rules 20 and 21)", pr, "f/x", []string{"docs/.claude-plugin/a.md"}, "false"},
		{"a template (documentation, rules 20 and 21)", pr, "f/x", []string{"docs/x/briefing.tmpl"}, "false"},
		{"a cursor rules file", pr, "f/x", []string{".cursorrules"}, "true"},
		{"README stays docs", pr, "f/x", []string{"README.md", "docs/install.md", "SECURITY.md", "CONTRIBUTING.md"}, "false"},
		{"a file merely named like one", pr, "f/x", []string{"docs/MYAGENTS.md", "docs/agents-notes.md"}, "false"},
		{"a lower-case agents file is the instruction file (documentation, rules 20 and 21)", pr, "f/x", []string{"docs/agents.md"}, "false"},
		{"a quoted path (git escapes it)", pr, "f/x", []string{`"docs/\303\251.md"`}, "true"},
		{"no paths at all", pr, "f/x", nil, "true"},
		// A push to main and a queue draft are classified by the paths they add, like a pull request (a docs-only
		// change runs only the docs lint, on the pull request, in the queue and on main). A schedule and a merge group are code.
		{"a push to main, docs only", "push", "", []string{"docs/faq.md"}, "false"},
		{"a push to main with code", "push", "", []string{"docs/faq.md", "internal/x.go"}, "true"},
		{"a push to main touching a workflow", "push", "", []string{".github/workflows/ci.yml"}, "true"},
		{"a push to main with no paths", "push", "", nil, "true"},
		{"a schedule", "schedule", "", nil, "true"},
		{"a merge group, docs only", "merge_group", "", []string{"docs/faq.md"}, "true"},
		{"a queue draft whose batch is docs-only", pr, "mergify/merge-queue/0123abcd", []string{"docs/faq.md", "README.md"}, "false"},
		{"a queue draft, ADRs only", pr, "mergify/merge-queue/0123abcd", []string{"docs/decisions/0099-x.md"}, "false"},
		{"a queue draft whose batch has one code pull request", pr, "mergify/merge-queue/0123abcd", []string{"docs/faq.md", "internal/x.go"}, "true"},
		{"a queue draft touching the mergify config", pr, "mergify/merge-queue/0123abcd", []string{"docs/faq.md", ".mergify.yml"}, "true"},
		{"a queue draft touching the spec", pr, "mergify/merge-queue/0123abcd", []string{"docs/faq.md", "spec/rules.json"}, "true"},
		{"a queue draft with no paths", pr, "mergify/merge-queue/0123abcd", nil, "true"},
		{"a branch that only resembles the queue's", pr, "feature/mergify/merge-queue/x", []string{"docs/faq.md"}, "false"},
		{"every Markdown file is documentation, whatever its name or place", pr, "f/x", []string{"README.md", "CHANGELOG.md", "AGENTS.md", "mash-setup/skills/setup/SKILL.md", "plugins/x/README.md", "docs/images/stack.svg"}, "false"},
		{"the release signers are data the release tooling reads: code", pr, "f/x", []string{"docs/allowed_signers"}, "true"},
		{"the review policy is data the gate reads: code", pr, "f/x", []string{"docs/review/policy.json"}, "true"},
		{"the skipped-releases list is data: code", pr, "f/x", []string{"docs/skipped-releases.txt"}, "true"},
		{"the release cutover version is data: code", pr, "f/x", []string{"docs/release-cutover"}, "true"},
		{"review pages beside the policy stay documentation", pr, "f/x", []string{"docs/review/in-session-loop.md", "docs/images/stack.svg"}, "false"},
		{"a hook script in a skills directory stays code", pr, "f/x", []string{"docs/a.md", "mash-setup/skills/setup/run.sh"}, "true"},
		{"a manifest in a plugin directory stays code", pr, "f/x", []string{"mash-setup/.claude-plugin/plugin.json"}, "true"},
		{"a template outside docs stays code", pr, "f/x", []string{"briefings/a.tmpl"}, "true"},
	} {
		if got := ciChanges(t, c.event, c.ref, c.files...); got != "code="+c.code {
			t.Errorf("%s: %s, want code=%s", c.name, got, c.code)
		}
	}
}

// conformance is advisory and gates nothing, so a docs-only pull request skips it like the other heavy jobs (the
// review watcher waits only on pr-check); a Mergify draft never runs it.
func TestDocsOnly_conformanceSkipsOnDocsOnly(t *testing.T) {
	j, ok := load(t, "ci.yml").Jobs["conformance"]
	if !ok {
		t.Fatal("ci.yml has no conformance job")
	}
	if !strings.Contains(fmt.Sprint(j.Needs), "changes") {
		t.Errorf("conformance does not need changes: %v", j.Needs)
	}
	if want := "github.event_name == 'pull_request' && !startsWith(github.head_ref, 'mergify/') && needs.changes.outputs.code != 'false'"; j.If != want {
		t.Errorf("conformance if %q; want %q", j.If, want)
	}
}

// diffStepScripts returns each workflow's changes/diff step run script, keyed by workflow file.
func diffStepScripts(t *testing.T) map[string]string {
	t.Helper()
	out := map[string]string{}
	for _, f := range []string{"ci.yml", "lint.yml", "security.yml"} {
		for _, st := range load(t, f).Jobs["changes"].Steps {
			if st.ID == "diff" {
				out[f] = st.Run
			}
		}
		if out[f] == "" {
			t.Fatalf("%s: no changes/diff step", f)
		}
	}
	return out
}

func git(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-c", "user.name=t", "-c", "user.email=t@example.com", "-c", "commit.gpgsign=false"}, args...)...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v %s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

func writeRepoFile(t *testing.T, dir, rel, content string) {
	t.Helper()
	p := filepath.Join(dir, rel)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0o755); err != nil {
		t.Fatal(err)
	}
}

// The diff step's behaviour, run for real: the classifier comes from the base revision, and a diff touching .github/ or
// scripts/ci-changes is always code.
func TestDocsOnly_diffStepHardening(t *testing.T) {
	real, err := os.ReadFile("../../scripts/ci-changes")
	if err != nil {
		t.Fatal(err)
	}
	const rewritten = "#!/usr/bin/env bash\necho code=false\n"
	type change struct{ path, content string }
	for _, c := range []struct {
		name       string
		event      string
		baseHasCls bool
		changes    []change
		want       string
	}{
		{"docs only", "pull_request", true, []change{{"docs/a.md", "x\n"}}, "code=false"},
		{"rewritten classifier plus Go", "pull_request", true, []change{{"scripts/ci-changes", rewritten}, {"internal/x.go", "package x\n"}}, "code=true"},
		{"rewritten classifier plus docs", "pull_request", true, []change{{"scripts/ci-changes", rewritten}, {"docs/a.md", "x\n"}}, "code=true"},
		{"workflow change", "pull_request", true, []change{{".github/workflows/x.yml", "on: push\n"}}, "code=true"},
		{"workflow change beside docs", "pull_request", true, []change{{".github/workflows/x.yml", "on: push\n"}, {"docs/a.md", "x\n"}}, "code=true"},
		{"base without the classifier", "pull_request", false, []change{{"docs/a.md", "x\n"}}, "code=true"},
		{"push, docs only", "push", true, []change{{"docs/a.md", "x\n"}}, "code=false"},
		{"push rewriting the classifier", "push", true, []change{{"scripts/ci-changes", rewritten}, {"docs/a.md", "x\n"}}, "code=true"},
		{"push touching a workflow", "push", true, []change{{".github/workflows/x.yml", "on: push\n"}, {"docs/a.md", "x\n"}}, "code=true"},
		{"push with code", "push", true, []change{{"internal/x.go", "package x\n"}, {"docs/a.md", "x\n"}}, "code=true"},
		{"push whose previous head is not in the checkout", "push-nobefore", true, []change{{"docs/a.md", "x\n"}}, "code=true"},
	} {
		for wf, script := range diffStepScripts(t) {
			t.Run(c.name+"/"+wf, func(t *testing.T) {
				dir := t.TempDir()
				git(t, dir, "init", "-q")
				writeRepoFile(t, dir, "README.txt", "base\n")
				if c.baseHasCls {
					writeRepoFile(t, dir, "scripts/ci-changes", string(real))
				}
				git(t, dir, "add", "-A")
				git(t, dir, "commit", "-q", "-m", "base")
				base := git(t, dir, "rev-parse", "HEAD")
				for _, ch := range c.changes {
					writeRepoFile(t, dir, ch.path, ch.content)
				}
				git(t, dir, "add", "-A")
				git(t, dir, "commit", "-q", "-m", "pr")
				outFile := filepath.Join(t.TempDir(), "output")
				runnerTemp := t.TempDir()
				cmd := exec.Command("bash", "-c", script)
				cmd.Dir = dir
				event, before := c.event, base
				if event == "push-nobefore" {
					event, before = "push", "0000000000000000000000000000000000000000"
				}
				cmd.Env = append(os.Environ(), "EVENT="+event, "BASE="+base, "BEFORE="+before, "HEAD_REF=x", "GITHUB_OUTPUT="+outFile, "RUNNER_TEMP="+runnerTemp)
				if out, err := cmd.CombinedOutput(); err != nil {
					t.Fatalf("%v: %s", err, out)
				}
				got, err := os.ReadFile(outFile)
				if err != nil {
					t.Fatal(err)
				}
				if strings.TrimSpace(string(got)) != c.want || strings.Count(string(got), "code=") != 1 {
					t.Errorf("output %q, want exactly %s", got, c.want)
				}
			})
		}
	}
}

// A push to main that is the merge queue's fast-forward of a commit its draft already tested runs nothing again: the changes job
// answers code=false when a Mergify draft's ci run on that SHA has a passed pr-check. It needs actions: read for the lookup, asks only
// about runs of the same SHA from a mergify/ branch, and any failed lookup falls through to the path classifier.
func TestDocsOnly_pushOfAQueueTestedCommitRunsNothingAgain(t *testing.T) {
	j := load(t, "ci.yml").Jobs["changes"]
	if j.Permissions["actions"] != "read" || j.Permissions["contents"] != "read" || len(j.Permissions) != 2 {
		t.Errorf("changes permissions = %v, want contents: read and actions: read only", j.Permissions)
	}
	var run string
	for _, st := range j.Steps {
		if st.ID == "diff" {
			run = st.Run
		}
	}
	for _, want := range []string{
		"head_sha=$GITHUB_SHA&event=pull_request",
		`startswith("mergify/")`,
		`.name == "pr-check" and .conclusion == "success"`,
		"|| true",
	} {
		if !strings.Contains(run, want) {
			t.Errorf("changes/diff step lacks %q", want)
		}
	}
	// The release gate must still find the draft's passed runs behind this push's skipped ones (evaluate ignores skipped runs).
	mk := func(id int64, name, conclusion string) CheckRun {
		r := CheckRun{ID: id, Name: name, HeadSHA: "x", Status: "completed", Conclusion: conclusion}
		r.App.Slug = CIApp
		return r
	}
	runs := []CheckRun{mk(1, "test (linux)", "success"), mk(2, "reproducible", "success"), mk(3, "test (linux)", "skipped"), mk(4, "reproducible", "skipped")}
	if st, err := evaluate(runs, "x"); err != nil || st != ciPassed {
		t.Errorf("a draft's passed runs behind a push's skipped runs: state %v, err %v; want passed", st, err)
	}
}
