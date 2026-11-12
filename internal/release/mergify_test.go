package release

import (
	"os"
	"reflect"
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
		if _, ok := names[mm[1]]; !ok {
			t.Errorf(".mergify.yml waits for %q, which no job (by its name:, or its key when it has none) in ci.yml, adr-lint.yml or label-reset.yml is called", mm[1])
		}
	}
	for _, c := range []struct{ check, file string }{{"adr-lint", "adr-lint.yml"}, {"label-reset", "label-reset.yml"}, {"pr-check", "ci.yml"}} {
		if names[c.check] != c.file {
			t.Errorf("check %q is run by %q, want %q", c.check, names[c.check], c.file)
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

// The ruleset requires `Mergify Merge Protections`, which is neutral unless a merge_protections rule exists, so a pull
// request green on CI but lacking codex-review and an approval could be merged through the API. The rule mirrors the
// queue's entry conditions (compared as parsed strings so they cannot drift) and must not apply to queue drafts.
func TestMergify_mergeProtectionsMirrorsQueueEntry(t *testing.T) {
	m := loadMergify(t)
	if len(m.MergeProtections) != 1 {
		t.Fatalf("want exactly one merge_protections rule, got %d", len(m.MergeProtections))
	}
	r := m.MergeProtections[0]
	qPlain, qOrs := splitConditions(t, m.QueueRules[0].QueueConditions)
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
		m := regexp.MustCompile(`^(-?)([a-z-]+) (~=|!=|=) (.+)$`).FindStringSubmatch(v)
		if m == nil {
			t.Fatalf("unsupported condition %q", v)
		}
		var res bool
		switch m[3] {
		case "~=":
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
		attrs := map[string]string{"base": "main", "head": c.head, "author": c.author}
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
