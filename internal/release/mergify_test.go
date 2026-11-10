package release

import (
	"os"
	"regexp"
	"sort"
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
	PullRequestRules []struct {
		Name       string                    `yaml:"name"`
		Conditions []string                  `yaml:"conditions"`
		Actions    map[string]map[string]any `yaml:"actions"`
	} `yaml:"pull_request_rules"`
	MergeQueue struct {
		Mode              string `yaml:"mode"`
		MaxParallelChecks int    `yaml:"max_parallel_checks"`
	} `yaml:"merge_queue"`
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
	jobs := load(t, "ci.yml").Jobs
	for _, f := range []string{"adr-lint.yml", "label-reset.yml"} { // their jobs are checks the queue waits for too
		for name, j := range load(t, f).Jobs {
			jobs[name] = j
		}
	}
	check := regexp.MustCompile(`^check-success = (.+)$`)
	m := loadMergify(t)
	if len(m.QueueRules) != 1 {
		t.Fatalf("want one queue rule, got %d", len(m.QueueRules))
	}
	q := m.QueueRules[0]
	queuePlain, _ := splitConditions(t, q.QueueConditions)
	for _, c := range append(append([]string{}, queuePlain...), q.MergeConditions...) {
		mm := check.FindStringSubmatch(c)
		if mm == nil || mm[1] == "codex-review" {
			continue
		}
		if _, ok := jobs[mm[1]]; !ok {
			t.Errorf(".mergify.yml waits for %q, which no workflow the queue reads (ci.yml, adr-lint.yml, label-reset.yml) runs", mm[1])
		}
	}
	for _, want := range []string{"check-success = codex-review", "check-success = pr-check", "check-success = adr-lint", "check-success = label-reset", "label != hold", "-draft", "-conflict"} {
		if !strings.Contains(strings.Join(queuePlain, "\n"), want) {
			t.Errorf("queue_conditions lack %q", want)
		}
	}
	if strings.Join(q.MergeConditions, "|") != "check-success = pr-check" {
		t.Errorf("merge_conditions = %v, want pr-check on the queue branch's run", q.MergeConditions)
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
	plain, ors := splitConditions(t, m.QueueRules[0].QueueConditions)
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

// The lead-approved and decision-confirmed labels must not outlive the code they covered: a workflow removes both on
// every push (and runs on open and reopen, so the label-reset check exists on every head). It runs on
// pull_request_target (a token that can edit labels on any pull request), so it must hold only pull-requests: write and
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
	if len(w.Permissions) != 1 || w.Permissions["pull-requests"] != "write" {
		t.Errorf("workflow permissions = %v, want pull-requests: write only", w.Permissions)
	}
	if len(w.Jobs) != 1 || w.Jobs["label-reset"].Name != "label-reset" {
		t.Errorf("jobs = %v, want the single job label-reset named label-reset (the check name)", w.Jobs)
	}
	for id, j := range w.Jobs {
		if j.Permissions != nil && (len(j.Permissions) != 1 || j.Permissions["pull-requests"] != "write") {
			t.Errorf("job %s permissions = %v, want pull-requests: write only", id, j.Permissions)
		}
	}
	text := string(raw)
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
	_, ors := splitConditions(t, m.QueueRules[0].QueueConditions)
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
