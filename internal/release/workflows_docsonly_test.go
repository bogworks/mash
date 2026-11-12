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

// heavyIf is the job-level condition of every heavy job in ci.yml that gates (test, test-macos, reproducible): skipped only
// when changes answered code=false, run when changes failed (an empty output), and never run after a cancellation.
const heavyIf = "${{ !cancelled() && (github.event_name == 'push' || needs.changes.outputs.code != 'false') }}"

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
		"ci.yml":       {"test", "test-macos", "reproducible"},
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
	for _, jn := range []string{"test", "test-macos", "reproducible"} {
		if got := load(t, "ci.yml").Jobs[jn].If; got != heavyIf {
			t.Errorf("ci.yml/%s if = %q, want %q", jn, got, heavyIf)
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
		for _, want := range []string{"git diff --no-renames --name-only", "bash scripts/ci-changes", `>> "$GITHUB_OUTPUT"`, "set -euo pipefail"} {
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
		{"a non-markdown file under docs", pr, "f/x", []string{"docs/plans/p.txt", "docs/images/a.svg", "docs/demo/two-sessions.cast"}, "false"},
		{"an ADR only", pr, "f/x", []string{"docs/decisions/0099-x.md", "docs/decisions/INDEX.md"}, "false"},
		{"docs and a Go file", pr, "f/x", []string{"docs/a.md", "cmd/mash/cli.go"}, "true"},
		{"a Go file only", pr, "f/x", []string{"internal/x.go"}, "true"},
		{"scripts only", pr, "f/x", []string{"scripts/gate"}, "true"},
		{"a script beside docs", pr, "f/x", []string{"docs/a.md", "scripts/ci-changes"}, "true"},
		{"a workflow", pr, "f/x", []string{".github/workflows/ci.yml"}, "true"},
		{"markdown under .github", pr, "f/x", []string{".github/PULL_REQUEST_TEMPLATE.md"}, "true"},
		{"mergify config", pr, "f/x", []string{".mergify.yml"}, "true"},
		{"go.mod", pr, "f/x", []string{"docs/a.md", "go.mod"}, "true"},
		{"go.sum", pr, "f/x", []string{"go.sum"}, "true"},
		{"markdown under internal", pr, "f/x", []string{"internal/foo/README.md"}, "true"},
		{"markdown under cmd", pr, "f/x", []string{"cmd/mash/NOTES.md"}, "true"},
		{"markdown under scripts", pr, "f/x", []string{"scripts/scrubcheck/inventory.md"}, "true"},
		{"the plugin package", pr, "f/x", []string{"plugin/README.md"}, "true"},
		{"test data", pr, "f/x", []string{"internal/policy/testdata/a.md", "docs/x/testdata/b.md"}, "true"},
		{"a conformance vector", pr, "f/x", []string{"docs/contracts/conformance-vectors.json", "docs/conformance.md"}, "true"},
		{"a change-log fragment", pr, "f/x", []string{"changelog.d/a.fixed.md"}, "true"},
		{"the change log", pr, "f/x", []string{"CHANGELOG.md"}, "true"},
		{"the audit golden doc", pr, "f/x", []string{"docs/audit.md"}, "true"},
		{"the branch-protection doc", pr, "f/x", []string{"docs/branch-protection.md"}, "true"},
		{"the review policy", pr, "f/x", []string{"docs/review/policy.json"}, "true"},
		{"review docs beside the policy", pr, "f/x", []string{"docs/review/in-session-loop.md"}, "true"},
		{"the release signers", pr, "f/x", []string{"docs/allowed_signers"}, "true"},
		{"the skipped-releases list", pr, "f/x", []string{"docs/skipped-releases.txt"}, "true"},
		{"a binary asset under docs", pr, "f/x", []string{"docs/images/a.PNG"}, "true"},
		{"a Go file under docs", pr, "f/x", []string{"docs/evidence/x/main.go"}, "true"},
		{"a rename out of scripts into docs", pr, "f/x", []string{"scripts/notes.md", "docs/notes.md"}, "true"},
		{"a file the classifier does not know", pr, "f/x", []string{"Makefile"}, "true"},
		{"a quoted path (git escapes it)", pr, "f/x", []string{`"docs/\303\251.md"`}, "true"},
		{"no paths at all", pr, "f/x", nil, "true"},
		// Never docs-only: pushes, schedules, merge groups, and the merge queue's drafts.
		{"a push to main, docs only", "push", "", []string{"docs/faq.md"}, "true"},
		{"a schedule", "schedule", "", nil, "true"},
		{"a merge group, docs only", "merge_group", "", []string{"docs/faq.md"}, "true"},
		{"a queue draft whose diff looks docs-only", pr, "mergify/merge-queue/0123abcd", []string{"docs/faq.md", "README.md"}, "true"},
		{"a queue draft, ADRs only", pr, "mergify/merge-queue/0123abcd", []string{"docs/decisions/0099-x.md"}, "true"},
		{"a branch that only resembles the queue's", pr, "feature/mergify/merge-queue/x", []string{"docs/faq.md"}, "false"},
	} {
		if got := ciChanges(t, c.event, c.ref, c.files...); got != "code="+c.code {
			t.Errorf("%s: %s, want code=%s", c.name, got, c.code)
		}
	}
}

// The classifier's docs-only paths are exactly the ones no test, build or tool reads: every repository file a Go test or
// the build reads from the docs areas must classify as code. This pins the list in scripts/ci-changes against the files
// that exist, so a rename of one of them does not silently turn it docs-only.
func TestDocsOnly_filesReadByTestsAreCode(t *testing.T) {
	for _, p := range []string{
		"docs/audit.md",                // cmd/mashctl audit tests
		"docs/branch-protection.md",    // internal/release mergify_test.go
		"docs/review/policy.json",      // scripts/reviewgate tests, scripts/gate
		"docs/allowed_signers",         // scripts/releasecheck (release signing)
		"docs/skipped-releases.txt",    // scripts/releasecheck
		"CHANGELOG.md",                 // scripts/changelog tests
		"scripts/scrubcheck/words.txt", // scripts/scrubcheck tests
	} {
		if _, err := os.Stat(filepath.Join("../..", p)); err != nil {
			t.Errorf("%s no longer exists: update scripts/ci-changes and this list: %v", p, err)
			continue
		}
		if got := ciChanges(t, "pull_request", "f/x", p); got != "code=true" {
			t.Errorf("%s is read by a test or tool but classifies as %s", p, got)
		}
	}
}

// conformance runs on every pull request, docs-only included: the review watcher waits for it and may treat a skipped
// job as not done. It has no changes dependency and no code-based condition.
func TestDocsOnly_conformanceIsNeverSkippedOnCode(t *testing.T) {
	j, ok := load(t, "ci.yml").Jobs["conformance"]
	if !ok {
		t.Fatal("ci.yml has no conformance job")
	}
	if strings.Contains(fmt.Sprint(j.Needs), "changes") {
		t.Errorf("conformance needs changes: %v", j.Needs)
	}
	if strings.Contains(j.If, "needs.changes") || strings.Contains(j.If, "code") {
		t.Errorf("conformance if %q depends on the changes output", j.If)
	}
	if want := "github.event_name == 'pull_request' && !startsWith(github.head_ref, 'mergify/')"; j.If != want {
		t.Errorf("conformance if %q; want %q", j.If, want)
	}
}
