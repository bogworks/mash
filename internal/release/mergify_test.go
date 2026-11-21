package release

import (
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
		// codex-review and lead-review are commit statuses posted outside the workflows (scripts/review-pr,
		// scripts/lead-approve), so no job carries their name.
		if mm == nil || mm[1] == "codex-review" || mm[1] == "lead-review" {
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
	for _, want := range []string{"check-success = codex-review", "check-success = @github-actions/pr-check", "check-success = @github-actions/adr-lint", "check-success = @github-actions/label-reset", "label != hold", "-draft", "-conflict"} {
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

// The maintainer's login authors agent pull requests too, so authorship proves nothing: entry to the queue needs a
// recorded approval or the lead-approved label (which label-reset.yml removes on every push), and the hold label
// stays the brake in both places.
func TestMergify_entryNeedsApprovalOrLabel(t *testing.T) {
	m := loadMergify(t)
	plain, ors := splitConditions(t, queueRule(t, m, "default").QueueConditions)
	if len(ors) != 1 {
		t.Fatalf("want exactly one or group in queue_conditions, got %d", len(ors))
	}
	for _, c := range append(append([]string{}, plain...), ors[0]...) {
		if strings.HasPrefix(c, "author = ") {
			t.Errorf("queue_conditions contain %q: the maintainer's login authors agent pull requests, so authorship proves nothing", c)
		}
	}
	for _, c := range ors[0] {
		if strings.Contains(c, "check-success") {
			t.Errorf("or member %q lets a bare check satisfy the group", c)
		}
	}
	got := append([]string{}, ors[0]...)
	sort.Strings(got)
	want := []string{"approved-reviews-by = randybias", "label = lead-approved"}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Errorf("entry or group = %v, want exactly %v", got, want)
	}
	if !strings.Contains(strings.Join(plain, "\n"), "label != hold") {
		t.Error("queue_conditions must honour the hold label")
	}
	if !strings.Contains(strings.Join(m.Protections.AutoMerge, "\n"), "label != hold") {
		t.Error("auto_merge_conditions must honour the hold label")
	}
}

// The lead's review is a commit status on the exact head (ADR 0116). It must be a plain queue_conditions entry: not in
// merge_protections (the queue draft's commit has no such status, the #424 deadlock) and not inside the or group (a
// bare check inside an or is banned, ADR 0078). During the migration week it is required beside the approval gate.
func TestMergify_queueNeedsLeadReviewStatus(t *testing.T) {
	m := loadMergify(t)
	plain, ors := splitConditions(t, queueRule(t, m, "default").QueueConditions)
	if !contains(plain, "check-success = lead-review") {
		t.Errorf("queue_conditions lack the plain entry `check-success = lead-review`: %v", plain)
	}
	for _, g := range ors {
		for _, c := range g {
			if strings.Contains(c, "lead-review") {
				t.Errorf("or member %q: lead-review must be a plain entry during the migration week", c)
			}
		}
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
	// contents: read is for gh pr view (it may need it on this repository); both are read or label-edit scopes, no write to code.
	wantPerms := map[string]string{"pull-requests": "write", "contents": "read"}
	if !reflect.DeepEqual(w.Permissions, wantPerms) {
		t.Errorf("workflow permissions = %v, want exactly %v", w.Permissions, wantPerms)
	}
	if len(w.Jobs) != 1 || w.Jobs["label-reset"].Name != "label-reset" {
		t.Errorf("jobs = %v, want the single job label-reset named label-reset (the check name)", w.Jobs)
	}
	for id, j := range w.Jobs {
		if j.Permissions != nil && !reflect.DeepEqual(j.Permissions, wantPerms) {
			t.Errorf("job %s permissions = %v, want exactly %v", id, j.Permissions, wantPerms)
		}
	}
	text := string(raw)
	// opened resets too: labels preset at open must not count, so there is no early exit for it.
	if strings.Contains(text, `"opened"`) || strings.Contains(text, "exit 0") || strings.Contains(text, "ACTION") {
		t.Error("label-reset.yml special-cases opened or exits early: labels preset at open must be removed like any other")
	}
	if strings.Contains(text, "actions/checkout") {
		t.Error("label-reset.yml checks out code; a pull_request_target workflow must never run the pull request's code")
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
// every push by anyone but the maintainers the queue's or group names.
func TestMergify_outsiderApprovalsDieOnPush(t *testing.T) {
	m := loadMergify(t)
	_, ors := splitConditions(t, queueRule(t, m, "default").QueueConditions)
	if len(ors) != 1 {
		t.Fatalf("want exactly one or group in queue_conditions, got %d", len(ors))
	}
	var logins []string
	for _, c := range ors[0] {
		if strings.HasPrefix(c, "approved-reviews-by = ") {
			logins = append(logins, strings.TrimPrefix(c, "approved-reviews-by = "))
		}
	}
	if len(logins) == 0 {
		t.Fatalf("or group %v has no approved-reviews-by member", ors[0])
	}
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
		have := strings.Join(r.Conditions, "\n")
		for _, l := range logins {
			if !strings.Contains(have, "-author = "+l) {
				t.Errorf("rule %q does not exclude maintainer %q (conditions %v)", r.Name, l, r.Conditions)
			}
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

// The ruleset requires `Mergify Merge Protections`, which is neutral unless a merge_protections rule exists, so a pull
// request green on CI but lacking codex-review and an approval could be merged through the API. The rule mirrors the
// queue's entry conditions (compared as parsed strings so they cannot drift) and must not apply to queue drafts.
func TestMergify_mergeProtectionsMirrorsQueueEntry(t *testing.T) {
	m := loadMergify(t)
	if len(m.MergeProtections) != 1 {
		t.Fatalf("want exactly one merge_protections rule, got %d", len(m.MergeProtections))
	}
	r := m.MergeProtections[0]
	qPlain, qOrs := splitConditions(t, queueRule(t, m, "default").QueueConditions)
	plain, ors := splitConditions(t, r.SuccessConditions)
	// Only label conditions: Mergify applies these to the queue draft's commit as merge conditions, so a check-success
	// condition (adr-lint is skipped on drafts) would deadlock the queue.
	if len(plain) != 1 || len(ors) != 1 {
		t.Fatalf("success_conditions = %v, want only `label != hold` and one approval or group", r.SuccessConditions)
	}
	for _, want := range []string{"label != hold"} {
		if !contains(plain, want) {
			t.Errorf("success_conditions lack %q", want)
		}
		if !contains(qPlain, want) {
			t.Errorf("queue_conditions lack %q, so the rule has drifted from the queue", want)
		}
	}
	// The approver list mirrors queue_conditions deliberately, so changing maintainers is one edit (the same login in
	// both lists) and this test catches drift between them.
	got := append([]string{}, ors[0]...)
	sort.Strings(got)
	if len(qOrs) != 1 {
		t.Fatalf("want one or group in queue_conditions, got %d", len(qOrs))
	}
	q := append([]string{}, qOrs[0]...)
	sort.Strings(q)
	if strings.Join(got, "|") != strings.Join(q, "|") {
		t.Errorf("approval group = %v, queue_conditions require %v", got, q)
	}
	// The expected approver is derived from queue_conditions, not hard-coded here, so this comparison needs no edit when
	// the maintainer changes (TestMergify_entryNeedsApprovalOrLabel pins the maintainer deliberately).
	var logins []string
	for _, c := range q {
		if l, ok := strings.CutPrefix(c, "approved-reviews-by = "); ok {
			logins = append(logins, l)
		}
	}
	if len(logins) == 0 || strings.TrimSpace(logins[0]) == "" {
		t.Fatalf("queue_conditions name no approver login: %v", q)
	}
	for _, l := range logins {
		if !contains(got, "approved-reviews-by = "+l) {
			t.Errorf("approval group %v lacks approver %q from queue_conditions", got, l)
		}
	}
	if !contains(got, "label = lead-approved") {
		t.Errorf("approval group %v lacks label = lead-approved", got)
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
					if f != "" && codeRegexMatches(t, m[4], f) {
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
		if mm[1] == "codex-review" || mm[1] == "lead-review" {
			continue
		}
		if jobs[mm[1]] {
			t.Errorf("%q names a GitHub Actions job without the app: write check-success = %s%s", c, actionsApp, mm[1])
		} else if !strings.HasPrefix(mm[1], actionsApp) {
			t.Errorf("%q is neither a known status (codex-review, lead-review) nor app-qualified", c)
		}
	}
	for _, s := range []string{"codex-review", "lead-review"} {
		if !seen[s] {
			t.Errorf("queue_conditions lack the plain `check-success = %s`", s)
		}
	}
}

// codeRegexMatches evaluates the code regex (the text of the docs queue's `-files ~=` condition) on one path. Mergify's regular
// expressions are Python's; Go's RE2 has no lookahead, and the expression has exactly one, in its first alternative
// (`^(?!(docs/|.*\.md$))`: not under docs/ and not a Markdown file), which is evaluated here as that sentence. Every other
// alternative is plain RE2.
func codeRegexMatches(t *testing.T, re, path string) bool {
	t.Helper()
	const lookahead = `^(?!(.*\.md$|docs/.*\.(txt|rst|adoc|csv|log)$))`
	rest, ok := strings.CutPrefix(re, lookahead+"|")
	if !ok {
		t.Fatalf("the code regex must start with %s: %.60s", lookahead, re)
	}
	// the lookahead: not Markdown anywhere, and not a plain-text file under docs/
	plainText := false
	if strings.HasPrefix(path, "docs/") {
		for _, e := range []string{".txt", ".rst", ".adoc", ".csv", ".log"} {
			plainText = plainText || strings.HasSuffix(path, e)
		}
	}
	if !strings.HasSuffix(path, ".md") && !plainText {
		return true
	}
	return regexp.MustCompile(rest).MatchString(path)
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

// The docs queue is first, holds exactly the conditions a docs-only pull request needs (the docs lint in pr-check, adr-lint,
// and the usual blockers), none of the review or approval ones, and merges the same way as the default queue.
func TestMergify_docsQueue(t *testing.T) {
	m := loadMergify(t)
	if len(m.QueueRules) != 2 || m.QueueRules[0].Name != "docs" {
		t.Fatalf("the docs queue must be the first of two queue rules: %v", m.QueueRules)
	}
	d := queueRule(t, m, "docs")
	plain, ors := splitConditions(t, d.QueueConditions)
	if len(ors) != 0 {
		t.Errorf("the docs queue has or groups: %v (no approval or label)", ors)
	}
	re := docsQueueRegex(t, m)
	want := []string{"base = main", "-draft", "-conflict", "label != hold", `#files >= 1`, `#files < 300`, "-files ~= " + re,
		"check-success = @github-actions/pr-check", "check-success = @github-actions/adr-lint"}
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
// matches the docs queue (and keeps the approval rule), and a docs-only one always does.
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
// cannot see past that, codex H1 on the docs queue): the docs queue and the priority rule require fewer than 300, and the approval
// rule stays active from 300 up.
func TestMergify_aHugePullRequestIsNeverDocsOnly(t *testing.T) {
	m := loadMergify(t)
	var docs []string
	for i := 0; i < 400; i++ {
		docs = append(docs, "docs/guide/page"+strconv.Itoa(i)+".md")
	}
	attrs := map[string]string{"base": "main", "head": "pr/x/y", "author": "randybias", "files": strings.Join(docs, "\n")}
	if !evalCondition(t, m.MergeProtections[0].If, attrs) {
		t.Error("the approval rule must stay active for 400 docs files")
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
		t.Error("the approval rule must be inactive for one docs file")
	}
}
