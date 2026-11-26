package release

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

type mergify struct {
	QueueRules []struct {
		Name            string   `yaml:"name"`
		QueueConditions []any    `yaml:"queue_conditions"`
		MergeConditions []string `yaml:"merge_conditions"`
		MergeMethod     string   `yaml:"merge_method"`
	} `yaml:"queue_rules"`
	PriorityRules []struct {
		Name       string `yaml:"name"`
		Conditions []any  `yaml:"conditions"`
		Priority   int    `yaml:"priority"`
	} `yaml:"priority_rules"`
	PullRequestRules []struct {
		Name       string                    `yaml:"name"`
		Conditions []string                  `yaml:"conditions"`
		Actions    map[string]map[string]any `yaml:"actions"`
	} `yaml:"pull_request_rules"`
	MergeQueue struct {
		Mode              string `yaml:"mode"`
		MaxParallelChecks int    `yaml:"max_parallel_checks"`
	} `yaml:"merge_queue"`
	MergeProtections []struct {
		Name              string `yaml:"name"`
		If                []any  `yaml:"if"`
		SuccessConditions []any  `yaml:"success_conditions"`
	} `yaml:"merge_protections"`
	Protections struct {
		AutoMerge []string `yaml:"auto_merge_conditions"`
	} `yaml:"merge_protections_settings"`
}

// split separates queue conditions into plain strings and `or:` groups (each a list of strings).
func splitConditions(t *testing.T, conds []any) (plain []string, ors [][]string) {
	t.Helper()
	for _, c := range conds {
		switch v := c.(type) {
		case string:
			plain = append(plain, v)
		case map[string]any:
			list, ok := v["or"].([]any)
			if !ok || len(v) != 1 {
				t.Fatalf("unsupported queue condition map %v", v)
			}
			var g []string
			for _, m := range list {
				ms, ok := m.(string)
				if !ok {
					t.Fatalf("or member %v is not a string", m)
				}
				g = append(g, ms)
			}
			ors = append(ors, g)
		default:
			t.Fatalf("unsupported queue condition %v", c)
		}
	}
	return
}

// queueRule returns the queue rule of that name (the docs queue is first, the default queue second).
func queueRule(t *testing.T, m mergify, name string) (q struct {
	Name            string   `yaml:"name"`
	QueueConditions []any    `yaml:"queue_conditions"`
	MergeConditions []string `yaml:"merge_conditions"`
	MergeMethod     string   `yaml:"merge_method"`
}) {
	t.Helper()
	for _, r := range m.QueueRules {
		if r.Name == name {
			return r
		}
	}
	t.Fatalf("no queue rule named %q", name)
	return
}

func loadMergify(t *testing.T) mergify {
	t.Helper()
	b, err := os.ReadFile("../../.mergify.yml")
	if err != nil {
		t.Fatal(err)
	}
	var m mergify
	if err := yaml.Unmarshal(b, &m); err != nil {
		t.Fatalf(".mergify.yml: %v", err)
	}
	return m
}

// Every check the queue waits for is one ci.yml runs (or codex-review, the review watcher's status): a name CI never
// publishes can never be satisfied, and the queue times out on every pull request.
func TestMergify_waitsOnlyForChecksThatExist(t *testing.T) {
	// A check's name is the job's name: when set, else its key (a matrix job's name is not one a queue condition names).
	names := map[string]string{}                                              // check name -> the workflow file that runs it
	for _, f := range []string{"ci.yml", "adr-lint.yml", "label-reset.yml"} { // the checks the queue waits for
		for key, j := range load(t, f).Jobs {
			n := j.Name
			if n == "" {
				n = key
			}
			names[n] = f
		}
	}
	// The queue reads a check only if its workflow runs for the pull request: label-reset is a pull_request_target one.
	if on, _ := load(t, "label-reset.yml").On.(map[string]any); on["pull_request_target"] == nil {
		t.Error("label-reset.yml must trigger on pull_request_target")
	}
	check := regexp.MustCompile(`^check-success = (.+)$`)
	m := loadMergify(t)
	if len(m.QueueRules) != 2 || m.QueueRules[0].Name != "docs" || m.QueueRules[1].Name != "default" {
		t.Fatalf("want the docs queue first and the default queue second, got %d queue rules", len(m.QueueRules))
	}
	q := queueRule(t, m, "default")
	queuePlain, _ := splitConditions(t, q.QueueConditions)
	for _, c := range append(append([]string{}, queuePlain...), q.MergeConditions...) {
		mm := check.FindStringSubmatch(c)
		// codex-review, lead-review and medium-dispositions are commit statuses posted outside the workflows (scripts/review-pr,
		// scripts/lead-approve), so no job carries their name.
		if mm == nil || mm[1] == "codex-review" || mm[1] == "lead-review" || mm[1] == "medium-dispositions" {
			continue
		}
		name := strings.TrimPrefix(mm[1], actionsApp)
		if _, ok := names[name]; !ok {
			t.Errorf(".mergify.yml waits for %q, which no job (by its name:, or its key when it has none) in ci.yml, adr-lint.yml or label-reset.yml is called", name)
		}
	}
	for _, c := range []struct{ check, file string }{{"adr-lint", "adr-lint.yml"}, {"label-reset", "label-reset.yml"}, {"pr-check", "ci.yml"}} {
		if names[c.check] != c.file {
			t.Errorf("check %q is run by %q, want %q", c.check, names[c.check], c.file)
		}
	}
	for _, want := range []string{"check-success = codex-review", "check-success = @github-actions/pr-check", "check-success = @github-actions/adr-lint", "check-success = @github-actions/label-reset", "check-success = medium-dispositions", "label != hold", "-draft", "-conflict"} {
		if !strings.Contains(strings.Join(queuePlain, "\n"), want) {
			t.Errorf("queue_conditions lack %q", want)
		}
	}
	if strings.Join(q.MergeConditions, "|") != "check-success = "+actionsApp+"pr-check" {
		t.Errorf("merge_conditions = %v, want @github-actions/pr-check on the queue branch's run", q.MergeConditions)
	}
	if q.MergeMethod != "fast-forward" || m.MergeQueue.Mode != "serial" || m.MergeQueue.MaxParallelChecks != 1 {
		t.Errorf("main must only move to a tested commit: merge_method %q, mode %q, max_parallel_checks %d",
			q.MergeMethod, m.MergeQueue.Mode, m.MergeQueue.MaxParallelChecks)
	}
	if !strings.Contains(strings.Join(m.Protections.AutoMerge, "\n"), "label != hold") {
		t.Error("auto_merge_conditions must honour the hold label")
	}
}

// A pull request enters the queue by itself (ADR 0177): queue_conditions hold no lead-review status and no approval, and
// the only or group is the exception-path one (ADR 0182); the hold label stays the brake in queue_conditions and
// auto_merge_conditions.
func TestMergify_entryNeedsNoApproval(t *testing.T) {
	m := loadMergify(t)
	plain, ors := splitConditions(t, queueRule(t, m, "default").QueueConditions)
	// the exception-path group, and the size group beside it (a pull request GitHub cannot list whole needs the label too)
	if len(ors) != 3 || len(ors[0]) != 2 || ors[0][1] != "label = lead-approved" || !strings.HasPrefix(ors[0][0], "-files ~= ") ||
		len(ors[1]) != 2 || ors[1][0] != "#files < 3000" || ors[1][1] != "label = lead-approved" {
		t.Errorf("queue_conditions or groups = %v, want the exception-path group and the #files < 3000 group (ADR 0182), then the lint group", ors)
	}
	wantLint(t, ors, 2, "default")
	for _, c := range plain {
		for _, banned := range []string{"lead-review", "approved-reviews-by", "lead-approved"} {
			if strings.Contains(c, banned) {
				t.Errorf("queue_conditions contain %q: no lead approval is needed to queue (ADR 0177)", c)
			}
		}
		if strings.HasPrefix(c, "author = ") {
			t.Errorf("queue_conditions contain %q: authorship proves nothing", c)
		}
	}
	if !contains(plain, "label != hold") {
		t.Error("queue_conditions must honour the hold label")
	}
	if !strings.Contains(strings.Join(m.Protections.AutoMerge, "\n"), "label != hold") {
		t.Error("auto_merge_conditions must honour the hold label")
	}
}

// The lead-approved and decision-confirmed labels must not outlive the code they covered: a workflow removes both on
// every push, on open (labels preset at open do not count) and on reopen. It runs on
// pull_request_target (a token that can edit labels on any pull request), so it must hold only pull-requests: write and contents: read and
// never check out or run the pull request's code, and a failure must fail the job (no swallowed errors).
func TestWorkflows_labelResetRemovesApprovalLabels(t *testing.T) {
	if _, err := os.Stat("../../.github/workflows/lead-approved-reset.yml"); err == nil {
		t.Error("lead-approved-reset.yml still exists: it was renamed to label-reset.yml")
	}
	raw, err := os.ReadFile("../../.github/workflows/label-reset.yml")
	if err != nil {
		t.Fatal(err)
	}
	w := load(t, "label-reset.yml")
	on, ok := w.On.(map[string]any)
	if !ok || len(on) != 1 {
		t.Fatalf("on = %v, want only pull_request_target", w.On)
	}
	prt, ok := on["pull_request_target"].(map[string]any)
	if !ok {
		t.Fatalf("on = %v, want pull_request_target", w.On)
	}
	var types []string
	for _, x := range prt["types"].([]any) {
		types = append(types, x.(string))
	}
	sort.Strings(types)
	if strings.Join(types, ",") != "opened,reopened,synchronize" {
		t.Errorf("pull_request_target types = %v, want [opened reopened synchronize]", types)
	}
	// contents: read is for gh pr view and the fetch of rule A, checks: read for the old head's label-reset run (ADR 0178);
	// all are read or label-edit scopes, no write to code.
	wantPerms := map[string]string{"pull-requests": "write", "contents": "read", "checks": "read"}
	if !reflect.DeepEqual(map[string]string(w.Permissions), wantPerms) {
		t.Errorf("workflow permissions = %v, want exactly %v", w.Permissions, wantPerms)
	}
	if len(w.Jobs) != 1 || w.Jobs["label-reset"].Name != "label-reset" {
		t.Errorf("jobs = %v, want the single job label-reset named label-reset (the check name)", w.Jobs)
	}
	for id, j := range w.Jobs {
		if j.Permissions != nil && !reflect.DeepEqual(map[string]string(j.Permissions), wantPerms) {
			t.Errorf("job %s permissions = %v, want exactly %v", id, j.Permissions, wantPerms)
		}
	}
	text := string(raw)
	// opened resets too: labels preset at open must not count, so there is no early exit for it.
	if strings.Contains(text, `"opened"`) || strings.Contains(text, "exit 0") || strings.Contains(text, "ACTION") {
		t.Error("label-reset.yml special-cases opened or exits early: labels preset at open must be removed like any other")
	}
	// The one checkout is the BASE commit (the trusted checker of rule A, ADR 0178); the pull request's code is never checked out.
	for _, st := range w.Jobs["label-reset"].Steps {
		if strings.HasPrefix(st.Uses, "actions/checkout@") {
			if st.With["ref"] != "${{ github.event.pull_request.base.sha }}" || fmt.Sprint(st.With["persist-credentials"]) != "false" {
				t.Errorf("label-reset.yml checks out %v (persist-credentials %v); only the base sha, with no persisted credentials, may be checked out", st.With["ref"], st.With["persist-credentials"])
			}
			if _, unsafe := st.With["allow-unsafe-pr-checkout"]; unsafe || st.With["repository"] != nil {
				t.Error("label-reset.yml checks out another repository or sets allow-unsafe-pr-checkout")
			}
		}
	}
	for _, l := range []string{"lead-approved", "decision-confirmed"} {
		if !strings.Contains(text, l) {
			t.Errorf("label-reset.yml does not name the %s label", l)
		}
	}
	if !strings.Contains(text, `--remove-label "$label"`) {
		t.Error("label-reset.yml does not remove labels with gh pr edit --remove-label")
	}
	// After the removals the labels are read again and the job fails if either is still set (re-applied concurrently, or
	// the removal did not take).
	removeAt := strings.Index(text, `--remove-label "$label"`)
	if n := strings.Count(text, "gh pr view"); n < 2 {
		t.Errorf("label-reset.yml has %d gh pr view calls, want a second read of the labels after the removals", n)
	} else if second := strings.LastIndex(text, "gh pr view"); second < removeAt {
		t.Error("label-reset.yml re-reads the labels before the removals, not after")
	} else if after := text[second:]; !strings.Contains(after, "exit 1") || !strings.Contains(after, "$label") {
		t.Error("label-reset.yml does not fail (exit 1, naming the label) when a label is still present after the removals")
	}
	if strings.Contains(text, "|| true") || strings.Contains(text, "continue-on-error") {
		t.Error("label-reset.yml swallows a failure (|| true or continue-on-error): an API or token failure must fail the job")
	}
}

// A maintainer's approval must not carry over to code an outside author pushes afterwards: a rule dismisses approvals on
// every push by anyone but the maintainer.
func TestMergify_outsiderApprovalsDieOnPush(t *testing.T) {
	m := loadMergify(t)
	found := false
	for _, r := range m.PullRequestRules {
		d, ok := r.Actions["dismiss_reviews"]
		if !ok {
			continue
		}
		if d["approved"] != true || d["when"] != "synchronize" {
			t.Errorf("rule %q: dismiss_reviews approved=%v when=%v, want true and synchronize", r.Name, d["approved"], d["when"])
			continue
		}
		// The maintainer's login is read from the rule itself, so no login is written in this test.
		excluded := false
		for _, c := range r.Conditions {
			if l, ok := strings.CutPrefix(c, "-author = "); ok && strings.TrimSpace(l) != "" {
				excluded = true
			}
		}
		if !excluded {
			t.Errorf("rule %q does not exclude the maintainer's own pull requests with `-author = <login>` (conditions %v)", r.Name, r.Conditions)
		}
		found = true
	}
	if !found {
		t.Error("no pull_request_rule dismisses approvals on synchronize")
	}
	doc, err := os.ReadFile("../../docs/branch-protection.md")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(doc), "Dismiss stale pull request approvals") {
		t.Error("docs/branch-protection.md does not mention 'Dismiss stale pull request approvals'")
	}
}

// The ruleset requires `Mergify Merge Protections`, which is neutral unless a merge_protections rule exists. The rule
// carries only `label != hold` (ADR 0177), the same text as queue_conditions, and must not apply to queue drafts.
func TestMergify_mergeProtectionsMirrorsQueueEntry(t *testing.T) {
	m := loadMergify(t)
	if len(m.MergeProtections) != 1 {
		t.Fatalf("want exactly one merge_protections rule, got %d", len(m.MergeProtections))
	}
	r := m.MergeProtections[0]
	if r.Name != "not held before merge" {
		t.Errorf("rule name = %q, want %q", r.Name, "not held before merge")
	}
	qPlain, _ := splitConditions(t, queueRule(t, m, "default").QueueConditions)
	plain, ors := splitConditions(t, r.SuccessConditions)
	// Only the label condition: Mergify applies these to the queue draft's commit as merge conditions, so a check-success
	// condition (adr-lint is skipped on drafts) would deadlock the queue.
	if len(ors) != 0 || strings.Join(plain, "|") != "label != hold" {
		t.Fatalf("success_conditions = %v, want exactly [label != hold]", r.SuccessConditions)
	}
	if !contains(qPlain, "label != hold") {
		t.Error("queue_conditions lack `label != hold`, so the rule has drifted from the queue")
	}
}

// evalCondition evaluates a Mergify condition tree against pull request attributes: a list is an AND, `or:` an OR, and
// a string is `[-]attr (~=|!=|=) value`, where a leading `-` negates it.
func evalCondition(t *testing.T, c any, attrs map[string]string) bool {
	t.Helper()
	switch v := c.(type) {
	case []any:
		for _, x := range v {
			if !evalCondition(t, x, attrs) {
				return false
			}
		}
		return true
	case map[string]any:
		list, ok := v["or"].([]any)
		if !ok || len(v) != 1 {
			t.Fatalf("unsupported condition map %v", v)
		}
		for _, x := range list {
			if evalCondition(t, x, attrs) {
				return true
			}
		}
		return false
	case string:
		if cm := regexp.MustCompile(`^#files (>=|<) ([0-9]+)$`).FindStringSubmatch(v); cm != nil {
			n, _ := strconv.Atoi(cm[2])
			count := 0
			for _, f := range strings.Split(attrs["files"], "\n") {
				if f != "" {
					count++
				}
			}
			if cm[1] == ">=" {
				return count >= n
			}
			return count < n
		}
		m := regexp.MustCompile(`^(-?)([a-z-]+) (~=|!=|=) (.+)$`).FindStringSubmatch(v)
		if m == nil {
			t.Fatalf("unsupported condition %q", v)
		}
		var res bool
		switch m[3] {
		case "~=":
			if m[2] == "files" { // a list attribute: true when ANY changed file matches (so `-files ~=` means none does)
				for _, f := range strings.Split(attrs["files"], "\n") {
					if f != "" && fileRegexMatches(t, m[4], f) {
						res = true
					}
				}
				break
			}
			res = regexp.MustCompile(m[4]).MatchString(attrs[m[2]])
		case "=":
			res = attrs[m[2]] == m[4]
		case "!=":
			res = attrs[m[2]] != m[4]
		}
		return res != (m[1] == "-")
	}
	t.Fatalf("unsupported condition %v", c)
	return false
}

// The queue-draft exemption must need both the head ref and Mergify's bot as author: a head ref alone is chosen by any
// same-repository writer and would let them switch the rule off for their own pull request.
func TestMergify_mergeProtectionsSkipsQueueDrafts(t *testing.T) {
	r := loadMergify(t).MergeProtections[0]
	for _, c := range []struct {
		head, author string
		active       bool
	}{
		{"mergify/merge-queue/abc", "mergify[bot]", false},
		{"mergify/merge-queue/abc", "randybias", true},
		{"pr/ctl/x", "mergify[bot]", true},
		{"pr/ctl/x", "randybias", true},
	} {
		attrs := map[string]string{"base": "main", "head": c.head, "author": c.author, "files": "internal/x.go"}
		if got := evalCondition(t, r.If, attrs); got != c.active {
			t.Errorf("head %q author %q: rule active = %v, want %v", c.head, c.author, got, c.active)
		}
	}
}

// A merge_protections rule must hold no check conditions at all: Mergify applies them to the queue draft's commit as
// merge conditions, where adr-lint is skipped and codex-review is not posted, so the queue refuses every pull request
// (the #424 deadlock). Checks belong in queue_conditions.
func TestMergify_mergeProtectionsHoldNoCheckConditions(t *testing.T) {
	for _, r := range loadMergify(t).MergeProtections {
		plain, ors := splitConditions(t, r.SuccessConditions)
		all := append([]string{}, plain...)
		for _, g := range ors {
			all = append(all, g...)
		}
		for _, c := range all {
			if strings.HasPrefix(c, "check-") {
				t.Errorf("merge_protections rule %q has the check condition %q; keep checks in queue_conditions", r.Name, c)
			}
		}
	}
}

func contains(l []string, s string) bool {
	for _, x := range l {
		if x == s {
			return true
		}
	}
	return false
}

const actionsApp = "@github-actions/"

// A plain `check-success = <name>` matches a status or check run by whoever posted it, so a commit status named
// pr-check, adr-lint or label-reset could be forged by anyone who can write statuses. Every condition naming a
// GitHub Actions job must be qualified with the app; codex-review and lead-review are commit statuses and stay plain.
func TestMergify_actionsChecksAreAppQualified(t *testing.T) {
	jobs := map[string]bool{"pr-check": true, "adr-lint": true, "label-reset": true}
	files, err := filepath.Glob(filepath.Join(repoRoot(t), ".github", "workflows", "*.yml"))
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range files {
		for key, j := range load(t, filepath.Base(f)).Jobs {
			jobs[key] = true
			if j.Name != "" {
				jobs[j.Name] = true
			}
		}
	}
	q := queueRule(t, loadMergify(t), "default")
	plain, _ := splitConditions(t, q.QueueConditions)
	check := regexp.MustCompile(`^check-success = (.+)$`)
	seen := map[string]bool{}
	for _, c := range append(append([]string{}, plain...), q.MergeConditions...) {
		mm := check.FindStringSubmatch(c)
		if mm == nil {
			continue
		}
		seen[mm[1]] = true
		if mm[1] == "codex-review" || mm[1] == "lead-review" || mm[1] == "medium-dispositions" {
			continue
		}
		if jobs[mm[1]] {
			t.Errorf("%q names a GitHub Actions job without the app: write check-success = %s%s", c, actionsApp, mm[1])
		} else if !strings.HasPrefix(mm[1], actionsApp) {
			t.Errorf("%q is neither a known status (codex-review, lead-review, medium-dispositions) nor app-qualified", c)
		}
	}
	for _, s := range []string{"codex-review", "medium-dispositions"} {
		if !seen[s] {
			t.Errorf("queue_conditions lack the plain `check-success = %s`", s)
		}
	}
}

// codeRegexMatches evaluates the code regex (the text of the docs queue's `-files ~=` condition) on one path. Mergify's regular
// expressions are Python's; Go's RE2 has no lookahead, and the expression has exactly one, in its first alternative,
// `^(?!(.*\.md$|docs/))`: a path is code unless it is a Markdown file or is under docs/ (rules 20 and 21), which is evaluated here as
// that sentence. The second alternative is plain RE2: the data files under docs/ that the release tooling reads (rule 20, 0.8.1).
func codeRegexMatches(t *testing.T, re, path string) bool {
	t.Helper()
	const lookahead = `^(?!(.*\.md$|docs/))`
	rest, ok := strings.CutPrefix(re, lookahead+"|")
	if !ok {
		t.Fatalf("the code regex must start with %s|: %.80s", lookahead, re)
	}
	if !strings.HasSuffix(path, ".md") && !strings.HasPrefix(path, "docs/") {
		return true
	}
	return regexp.MustCompile(rest).MatchString(path)
}

// fileRegexMatches evaluates a `files ~=` regex on one path: the code regex by codeRegexMatches, any other (the exception
// paths regex) as plain RE2, since it has no lookahead.
func fileRegexMatches(t *testing.T, re, path string) bool {
	t.Helper()
	if strings.HasPrefix(re, "^(?!") {
		return codeRegexMatches(t, re, path)
	}
	return regexp.MustCompile(re).MatchString(path)
}

// The docs queue's code regex, as written in .mergify.yml.
func docsQueueRegex(t *testing.T, m mergify) string {
	t.Helper()
	plain, _ := splitConditions(t, queueRule(t, m, "docs").QueueConditions)
	for _, c := range plain {
		if re, ok := strings.CutPrefix(c, "-files ~= "); ok {
			return re
		}
	}
	t.Fatal("the docs queue has no `-files ~=` condition")
	return ""
}

// The docs queue is first, holds exactly the conditions a docs-only pull request needs (the docs lint in pr-check, adr-lint, label-reset (so an exception path's label is the one for this head),
// and the usual blockers), none of the review or approval ones, and merges the same way as the default queue.
func TestMergify_docsQueue(t *testing.T) {
	m := loadMergify(t)
	if len(m.QueueRules) != 2 || m.QueueRules[0].Name != "docs" {
		t.Fatalf("the docs queue must be the first of two queue rules: %v", m.QueueRules)
	}
	d := queueRule(t, m, "docs")
	plain, ors := splitConditions(t, d.QueueConditions)
	if len(ors) != 2 || len(ors[0]) != 2 || ors[0][1] != "label = lead-approved" || !strings.HasPrefix(ors[0][0], "-files ~= ") {
		t.Errorf("the docs queue or groups = %v, want the exception-path group (ADR 0182), then the lint group", ors)
	}
	wantLint(t, ors, 1, "docs")
	re := docsQueueRegex(t, m)
	want := []string{"base = main", "-draft", "-conflict", "label != hold", `#files >= 1`, `#files < 300`, "-files ~= " + re,
		"check-success = @github-actions/pr-check", "check-success = @github-actions/adr-lint", "check-success = @github-actions/label-reset", "check-success = medium-dispositions"}
	got := append([]string{}, plain...)
	sort.Strings(got)
	sort.Strings(want)
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("docs queue conditions:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
	for _, c := range plain {
		for _, banned := range []string{"codex-review", "lead-review", "approved-reviews-by", "lead-approved", "security-review", "opus-review"} {
			if strings.Contains(c, banned) {
				t.Errorf("the docs queue requires %q: a docs-only change needs only the docs lint", c)
			}
		}
	}
	def := queueRule(t, m, "default")
	if strings.Join(d.MergeConditions, "|") != strings.Join(def.MergeConditions, "|") || d.MergeMethod != "fast-forward" {
		t.Errorf("docs queue merge: conditions %v method %q, want the default queue's (%v) and fast-forward", d.MergeConditions, d.MergeMethod, def.MergeConditions)
	}
	// One source: the merge_protections rule's `if` carries the same regex text as the docs queue.
	var found bool
	for _, c := range m.MergeProtections[0].If {
		if mm, ok := c.(map[string]any); ok {
			if list, ok := mm["or"].([]any); ok {
				for _, e := range list {
					if s, ok := e.(string); ok && s == "files ~= "+re {
						found = true
					}
				}
			}
		}
	}
	if !found {
		t.Error("merge_protections' if has no `files ~=` condition with the docs queue's regex text")
	}
}

// The code regex and scripts/ci-changes agree on every path: a pull request touching any path the classifier calls code never
// matches the docs queue (and keeps the merge protection rule), and a docs-only one always does.
func TestMergify_docsRegexAgreesWithTheClassifier(t *testing.T) {
	re := docsQueueRegex(t, loadMergify(t))
	paths := []string{
		"docs/a.md", "docs/decisions/0099-x.md", "README.md", "docs/guide/x.md", "docs/images/a.svg", "docs/notes.txt", "docs/a/b/c.md",
		"CHANGELOG.md", "changelog.d/a.fixed.md", "CONTRIBUTING.md", "AGENTS.md", ".github/workflows/ci.yml", ".github/PULL_REQUEST_TEMPLATE.md",
		".mergify.yml", ".claude-plugin/plugin.json", "plugin/README.md", "scripts/gate", "scripts/doclint/main.go", "scripts/x/README.md",
		"cmd/mash/main.go", "cmd/mash/NOTES.md", "internal/x.go", "internal/foo/README.md", "ci/x.md", "spec/rules.json", "spec/README.md",
		"go.mod", "go.sum", "tools/go.mod", "main.go", "docs/x/main.go", "internal/policy/testdata/a.md", "docs/x/testdata/b.md", "testdata/z.md",
		"docs/contracts/conformance-vectors.json", "docs/conformance.md", "docs/branch-protection.md", "docs/reference/mash-send.md",
		"docs/review/policy.json", "docs/review/in-session-loop.md", "docs/allowed_signers", "docs/skipped-releases.txt", "docs/release-cutover",
		"docs/images/a.PNG", "docs/images/a.png", "docs/images/b.JPEG", "docs/images/c.gif", "docs/images/d.pdf", "docs/fonts/e.WOFF2", "Makefile",
		"LICENSE", "docs", "docs.md", "xdocs/a.md", "mash-setup/skills/setup/SKILL.md", "mash-setup/.claude-plugin/plugin.json", "AGENTS.md", "CLAUDE.md", "docs/guide/AGENTS.md", "docs/x/CLAUDE.md", "docs/agents.md", "docs/MYAGENTS.md", "docs/agents-notes.md",
		"docs/x/GEMINI.md", "docs/x/claude.md", "docs/anywhere/SKILL.md", "docs/skills/notes.md", "docs/commands/x.md", "docs/agents/INSTALL.md", "docs/hooks/x.md",
		"docs/prompts/a.md", "briefings/a.md", "plugins/x/README.md", "docs/.claude-plugin/a.md", "docs/x/briefing.tmpl", ".cursorrules", ".windsurfrules",
		"Mash-Setup/x.md", "docs/Mash-Setup/y.md", "mash-setup/a.md", "docs/evidence/2026-11-02-heartbeat-fanout/run.sh", "docs/x/data.json", "docs/images/a.svg", "docs/demo/two-sessions.cast", "docs/a.txt", "docs/b.rst", "docs/evidence/x/timeline.log", "docs/x/Makefile", "README.md", "docs/install.md", "SECURITY.md", "CONTRIBUTING.md", "docs/Skills/Uppercase.md", "x/AGENTS.MD",
		"docs/specs/2026-10-21-mash-design.md", "docs/security/threat-model.md", "a.txt",
	}
	if out, err := exec.Command("git", "-C", "../..", "ls-files").Output(); err == nil {
		all := strings.Split(strings.TrimSpace(string(out)), "\n")
		for i := 0; i < len(all); i += 13 {
			paths = append(paths, all[i])
		}
	}
	for _, p := range paths {
		classifierDocs := ciChanges(t, "pull_request", "f/x", p) == "code=false"
		regexDocs := !codeRegexMatches(t, re, p)
		if classifierDocs != regexDocs {
			t.Errorf("%q: scripts/ci-changes docs-only=%v, the docs queue regex docs-only=%v", p, classifierDocs, regexDocs)
		}
	}
}

// A docs-only pull request is served first: the first priority rule carries the docs queue's regex text and the highest
// priority of all, so its couple-of-minutes batch never waits behind a code batch that is only queued.
func TestMergify_docsOnlyPriorityIsHighest(t *testing.T) {
	m := loadMergify(t)
	re := docsQueueRegex(t, m)
	if len(m.PriorityRules) == 0 || m.PriorityRules[0].Name != "docs-only" {
		t.Fatalf("the first priority rule must be docs-only: %+v", m.PriorityRules)
	}
	r := m.PriorityRules[0]
	plain, _ := splitConditions(t, r.Conditions)
	if !contains(plain, "-files ~= "+re) {
		t.Errorf("docs-only priority conditions %v lack the docs queue's regex text", plain)
	}
	for _, o := range m.PriorityRules[1:] {
		if o.Priority >= r.Priority {
			t.Errorf("priority rule %q (%d) is not below docs-only (%d)", o.Name, o.Priority, r.Priority)
		}
	}
}

// A pull request with 300 or more changed files is never docs-only for the queue (GitHub returns at most 3000 files and Mergify
// cannot see past that, codex H1 on the docs queue): the docs queue and the priority rule require fewer than 300, and the merge protection
// rule stays active from 300 up.
func TestMergify_aHugePullRequestIsNeverDocsOnly(t *testing.T) {
	m := loadMergify(t)
	var docs []string
	for i := 0; i < 400; i++ {
		docs = append(docs, "docs/guide/page"+strconv.Itoa(i)+".md")
	}
	attrs := map[string]string{"base": "main", "head": "pr/x/y", "author": "randybias", "files": strings.Join(docs, "\n")}
	if !evalCondition(t, m.MergeProtections[0].If, attrs) {
		t.Error("the merge protection rule must stay active for 400 docs files")
	}
	d := queueRule(t, m, "docs")
	plain, _ := splitConditions(t, d.QueueConditions)
	var fileConds []any // the conditions about the changed files (the rest are checks and labels, which this test does not model)
	for _, c := range plain {
		if strings.HasPrefix(c, "#files") || strings.HasPrefix(c, "-files") {
			fileConds = append(fileConds, c)
		}
	}
	if evalCondition(t, fileConds, map[string]string{"files": strings.Join(docs, "\n")}) {
		t.Error("the docs queue must not admit 400 files")
	}
	if !evalCondition(t, fileConds, map[string]string{"files": "docs/a.md\ndocs/b.md"}) {
		t.Error("the docs queue must admit two docs files")
	}
	small := map[string]string{"base": "main", "head": "pr/x/y", "author": "randybias", "files": "docs/a.md"}
	if evalCondition(t, m.MergeProtections[0].If, small) {
		t.Error("the merge protection rule must be inactive for one docs file")
	}
}

// exceptionGroup returns the exception-path regex text of a queue rule: the `-files ~=` member of its one or group, whose
// other member is `label = lead-approved`.
func exceptionGroup(t *testing.T, m mergify, queue string) string {
	t.Helper()
	_, ors := splitConditions(t, queueRule(t, m, queue).QueueConditions)
	var group []string
	for _, o := range ors {
		if len(o) == 2 && strings.HasPrefix(o[0], "-files ~= ") {
			if group != nil {
				t.Fatalf("%s queue has more than one `-files ~=` or group: %v", queue, ors)
			}
			group = o
		}
	}
	if group == nil || group[1] != "label = lead-approved" {
		t.Fatalf("%s queue or groups = %v, want one group [-files ~= <regex>, label = lead-approved]", queue, ors)
	}
	return strings.TrimPrefix(group[0], "-files ~= ")
}

// The exception-path regex (ADR 0182) is one source written in two queues: the text must be identical.
func TestMergify_exceptionRegexIsTheSameInBothQueues(t *testing.T) {
	m := loadMergify(t)
	d, def := exceptionGroup(t, m, "docs"), exceptionGroup(t, m, "default")
	if d != def {
		t.Errorf("exception-path regex differs:\ndocs:    %s\ndefault: %s", d, def)
	}
	if _, err := regexp.Compile(def); err != nil {
		t.Errorf("exception-path regex does not compile in RE2: %v", err)
	}
}

func TestMergify_exceptionPaths(t *testing.T) {
	re := exceptionGroup(t, loadMergify(t), "default")
	for _, p := range []string{".github/workflows/ci.yml", ".mergify.yml", "scripts/ci-changes", "scripts/releasecheck/main.go",
		"scripts/release-check", "internal/release/checks.go", "spec/mash.json", "docs/decisions/0001-x.md",
		"contracts/0.5.5-build-plan.md", "internal/policy/x.go", "internal/policyserve/a.go", "internal/policyapply/b.go",
		"internal/requesterreg/x.go", "internal/issue/request.go", "internal/reconcile/x.go", "internal/listset/check.go",
		"scripts/build-twice", "scripts/adrlint/main.go", "scripts/doclint/main.go", "scripts/changelog/main.go", "scripts/review-pr"} {
		if !fileRegexMatches(t, re, p) {
			t.Errorf("%q must be an exception path", p)
		}
	}
	for _, p := range []string{"cmd/mash/main.go", "internal/endpoint/mail.go", "internal/listsetx/x.go", "internal/issuex/x.go",
		"README.md", "docs/guide/quickstart.md", "scripts/mash-local-nats-setup.sh", "internal/sap/address.go"} {
		if fileRegexMatches(t, re, p) {
			t.Errorf("%q must not be an exception path", p)
		}
	}
}

// Every engine package scripts/releasecheck/unlinked.go refuses (policyPrefix and policyNamedElsewhere) is an exception
// path, so a new engine package added there without the regex fails here.
func TestMergify_exceptionRegexCoversTheEngineList(t *testing.T) {
	re := exceptionGroup(t, loadMergify(t), "default")
	b, err := os.ReadFile(filepath.Join(repoRoot(t), "scripts", "releasecheck", "unlinked.go"))
	if err != nil {
		t.Fatal(err)
	}
	src := string(b)
	pm := regexp.MustCompile(`(?m)^const policyPrefix = "([^"]+)"`).FindStringSubmatch(src)
	lm := regexp.MustCompile(`(?s)var policyNamedElsewhere = \[\]string\{(.*?)\n\}`).FindStringSubmatch(src)
	if pm == nil || lm == nil {
		t.Fatal("scripts/releasecheck/unlinked.go: policyPrefix or policyNamedElsewhere not found")
	}
	pkgs := []string{pm[1], pm[1] + "x/a.go", pm[1] + "/sub/a.go"}
	named := regexp.MustCompile(`"([^"]+)"`).FindAllStringSubmatch(lm[1], -1)
	if len(named) == 0 {
		t.Fatal("policyNamedElsewhere has no entries")
	}
	for _, n := range named {
		pkgs = append(pkgs, n[1]+"/x.go", n[1]+"/sub/x.go")
	}
	for _, p := range pkgs {
		if !fileRegexMatches(t, re, p) {
			t.Errorf("%q is in the order-16 engine list of scripts/releasecheck/unlinked.go but is not an exception path: update the regex in .mergify.yml (both queues)", p)
		}
	}
}

// A pull request touching an exception path enters a queue only with `lead-approved`; every other one needs no label.
func TestMergify_exceptionPathsNeedTheLabelToQueue(t *testing.T) {
	m := loadMergify(t)
	for _, queue := range []string{"default", "docs"} {
		var conds []any // the file and label conditions; checks and the rest are not modelled
		for _, c := range queueRule(t, m, queue).QueueConditions {
			if s, ok := c.(string); ok && !(strings.HasPrefix(s, "#files") || strings.HasPrefix(s, "-files")) {
				continue
			}
			if isCheckGroup(c) {
				continue
			}
			conds = append(conds, c)
		}
		for _, c := range []struct {
			name, files, labels string
			eligible            bool
		}{
			{"exception path, no label", "docs/decisions/0001-x.md", "", false},
			{"exception path, label", "docs/decisions/0001-x.md", "lead-approved", true},
			{"exception path among others, no label", "docs/a.md\ndocs/decisions/0001-x.md", "", false},
			{"ordinary docs, no label", "docs/a.md", "", true},
		} {
			if queue == "default" {
				c.files = strings.ReplaceAll(c.files, "docs/a.md", "cmd/mash/main.go")
			}
			attrs := map[string]string{"files": c.files, "label": c.labels}
			// the docs queue only takes docs-only pull requests, so a code file is modelled by the default queue alone
			if got := evalCondition(t, conds, attrs); got != c.eligible {
				t.Errorf("%s queue, %s: eligible = %v, want %v", queue, c.name, got, c.eligible)
			}
		}
	}
	// A code pull request on an exception path (.mergify.yml) and an ordinary code one, in the default queue.
	var conds []any
	for _, c := range queueRule(t, m, "default").QueueConditions {
		if _, ok := c.(map[string]any); ok && !isCheckGroup(c) {
			conds = append(conds, c)
		}
	}
	for _, c := range []struct {
		files, labels string
		eligible      bool
	}{{".mergify.yml", "", false}, {".mergify.yml", "lead-approved", true}, {"cmd/mash/main.go", "", true},
		{"scripts/buildrelease/main.go", "", false}, {"scripts/buildrelease/main.go", "lead-approved", true},
		// GitHub lists at most 3000 files: a pull request that large may hide an exception path
		{strings.Repeat("cmd/mash/x.go\n", 2999) + "cmd/mash/y.go", "", false}, {strings.Repeat("cmd/mash/x.go\n", 2998) + "cmd/mash/y.go", "", true}} {
		if got := evalCondition(t, conds, map[string]string{"files": c.files, "label": c.labels}); got != c.eligible {
			t.Errorf("default queue, files %q labels %q: eligible = %v, want %v", c.files, c.labels, got, c.eligible)
		}
	}
}

// Every script a workflow runs from the pull request itself is an exception path: a change to it can make its own check pass.
// The shipped assets (install.sh, the mash-* scripts) are product, not CI, and stay out.
func TestMergify_exceptionRegexCoversTheScriptsWorkflowsRun(t *testing.T) {
	re := exceptionGroup(t, loadMergify(t), "default")
	files, err := filepath.Glob(filepath.Join(repoRoot(t), ".github", "workflows", "*.yml"))
	if err != nil || len(files) == 0 {
		t.Fatalf("no workflows found: %v", err)
	}
	ref := regexp.MustCompile(`scripts/([A-Za-z0-9_-]+)`)
	for _, f := range files {
		b, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		for _, m := range ref.FindAllStringSubmatch(string(b), -1) {
			name := m[1]
			if name == "install" || strings.HasPrefix(name, "mash-") {
				continue
			}
			if !fileRegexMatches(t, re, "scripts/"+name) {
				t.Errorf("%s runs scripts/%s, which is not an exception path: add it to the regex in .mergify.yml (both queues)", filepath.Base(f), name)
			}
		}
	}
}

// lint is a queue condition of both queues (mash#1782). A docs-only pull request skips the job at job level and the check reports as
// skipped, which `check-success` does not accept (Mergify: success only), so the group is success or skipped, app-qualified.
func wantLint(t *testing.T, ors [][]string, i int, queue string) {
	t.Helper()
	if len(ors) <= i || len(ors[i]) != 2 || ors[i][0] != "check-success = "+actionsApp+"lint" || ors[i][1] != "check-skipped = "+actionsApp+"lint" {
		t.Errorf("%s queue or groups = %v, want [check-success = %slint, check-skipped = %slint] at %d", queue, ors, actionsApp, actionsApp, i)
	}
	if n := load(t, "lint.yml").Jobs["lint"].Name; n != "lint" {
		t.Errorf("lint.yml job lint is named %q, want the check name lint", n)
	}
}

// isCheckGroup reports whether a queue condition is an or group of check conditions (the lint group), which the file and label model skips.
func isCheckGroup(c any) bool {
	m, ok := c.(map[string]any)
	if !ok {
		return false
	}
	list, _ := m["or"].([]any)
	for _, x := range list {
		if s, ok := x.(string); ok && strings.HasPrefix(s, "check-") {
			return true
		}
	}
	return false
}
