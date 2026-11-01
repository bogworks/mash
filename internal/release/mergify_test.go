package release

import (
	"os"
	"regexp"
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
	for name, j := range load(t, "adr-lint.yml").Jobs { // adr-lint.yml's jobs are checks the queue waits for too
		jobs[name] = j
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
			t.Errorf(".mergify.yml waits for %q, which ci.yml does not run", mm[1])
		}
	}
	for _, want := range []string{"check-success = codex-review", "check-success = pr-check", "check-success = adr-lint", "label != hold", "-draft", "-conflict"} {
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

// An outside contributor's pull request must not reach the queue (which runs its code in CI) on
// green checks alone: a maintainer must have written or approved it.
func TestMergify_outsidersNeedAMaintainer(t *testing.T) {
	m := loadMergify(t)
	_, ors := splitConditions(t, m.QueueRules[0].QueueConditions)
	if len(ors) != 1 {
		t.Fatalf("want exactly one or group in queue_conditions, got %d", len(ors))
	}
	if len(ors[0]) < 2 {
		t.Fatalf("or group %v needs at least two members", ors[0])
	}
	authors, approvers := map[string]bool{}, map[string]bool{}
	for _, c := range ors[0] {
		switch {
		case strings.HasPrefix(c, "author = "):
			authors[strings.TrimPrefix(c, "author = ")] = true
		case strings.HasPrefix(c, "approved-reviews-by = "):
			approvers[strings.TrimPrefix(c, "approved-reviews-by = ")] = true
		}
		if strings.Contains(c, "check-success") {
			t.Errorf("or member %q lets a bare check satisfy the group", c)
		}
	}
	if len(authors) == 0 || len(approvers) == 0 {
		t.Fatalf("or group %v needs an author and an approved-reviews-by member", ors[0])
	}
	for a := range authors {
		if !approvers[a] {
			t.Errorf("%q may author but is not an approver", a)
		}
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
		if strings.HasPrefix(c, "author = ") {
			logins = append(logins, strings.TrimPrefix(c, "author = "))
		}
	}
	if len(logins) == 0 {
		t.Fatalf("or group %v has no author member", ors[0])
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
