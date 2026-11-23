package release

import (
	"fmt"
	"os"
	"sort"
	"strings"
	"testing"
)

// ci_table_test pins docs/ci-table.md (rule 21, ADR 0160): for each job the table names, the events the workflow runs it on, read from the workflow's
// triggers and the job's if, must equal the table's. A job's if that this file does not understand fails the test, so a new condition cannot slip past the table.

// eventsOf returns the sorted events a job of file runs on.
func eventsOf(t *testing.T, file, jobName string) []string {
	t.Helper()
	w := load(t, file)
	j, ok := w.Jobs[jobName]
	if !ok {
		t.Fatalf("%s has no job %s", file, jobName)
	}
	on, _ := w.On.(map[string]any)
	base := map[string]bool{}
	if _, ok := on["pull_request"]; ok {
		base["pr"], base["draft"] = true, true
	}
	if p, ok := on["push"].(map[string]any); ok {
		if _, tags := p["tags"]; tags {
			base["tag"] = true
		}
		if _, br := p["branches"]; br {
			base["main"] = true
		}
	}
	if _, ok := on["schedule"]; ok {
		base["nightly"] = true
	}
	if _, ok := on["workflow_dispatch"]; ok && file == "release.yml" {
		base["tag"] = true // the dry run is a tag-path rehearsal
	}
	cond := strings.Join(strings.Fields(j.If), " ")
	// the conditions this table knows; the classifier output and cancellation guards do not change the event set
	cond = strings.ReplaceAll(cond, "!cancelled() && needs.changes.outputs.code != 'false'", "")
	cond = strings.ReplaceAll(cond, "${{ }}", "")
	switch {
	case strings.Trim(cond, "${} &") == "" || cond == "always()":
		// every event the workflow triggers on
	case strings.Contains(cond, "github.event_name == 'pull_request' && !startsWith(github.head_ref, 'mergify/')"):
		base = map[string]bool{"pr": base["pr"]}
	case strings.Contains(cond, "(github.event_name != 'pull_request' || startsWith(github.head_ref, 'mergify/'))"):
		delete(base, "pr")
	case strings.Contains(cond, "github.ref == 'refs/heads/main'"):
		// the nightly job: schedule or dispatch on main only; the event set stays {nightly}
	default:
		t.Fatalf("%s/%s has an if this table test does not understand: %q (extend eventsOf and docs/ci-table.md)", file, jobName, j.If)
	}
	var out []string
	for e := range base {
		out = append(out, e)
	}
	sort.Strings(out)
	return out
}

func TestCITable_matchesTheWorkflows(t *testing.T) {
	b, err := os.ReadFile("../../docs/ci-table.md")
	if err != nil {
		t.Fatal(err)
	}
	checkName := map[string]string{"test": "test (linux)", "test-macos": "test (macos)", "test-eph": "test (linux, eph)"}
	rows := 0
	seen := map[string]bool{}
	for _, ln := range strings.Split(string(b), "\n") {
		if !strings.HasPrefix(ln, "| ") || strings.HasPrefix(ln, "| File") || strings.HasPrefix(ln, "|---") {
			continue
		}
		c := strings.Split(ln, "|")
		if len(c) < 5 {
			continue
		}
		file, name, ev := strings.TrimSpace(c[1]), strings.TrimSpace(c[2]), strings.TrimSpace(c[3])
		rows++
		job := name
		for k, v := range checkName {
			if v == name {
				job = k
			}
		}
		seen[file+"/"+job] = true
		want := strings.Join(strings.Split(strings.ReplaceAll(ev, " ", ""), ","), ",")
		if got := strings.Join(eventsOf(t, file, job), ","); got != sortedCSV(want) {
			t.Errorf("%s job %s: workflow runs it on %q, docs/ci-table.md says %q", file, name, got, want)
		}
	}
	if rows == 0 {
		t.Fatal("docs/ci-table.md has no rows")
	}
	// every job of the three workflows is in the table: nothing runs that the table does not say
	for _, f := range []string{"ci.yml", "release.yml", "nightly.yml"} {
		if f == "release.yml" {
			continue // release.yml's other jobs are the release itself (tag only), pinned by workflows_test.go
		}
		for jn := range load(t, f).Jobs {
			if !seen[f+"/"+jn] {
				t.Errorf("%s job %s is not in docs/ci-table.md", f, jn)
			}
		}
	}
	// the rule itself: no macOS and no eph job per change
	for jn := range load(t, "ci.yml").Jobs {
		if strings.Contains(jn, "macos") || strings.Contains(jn, "eph") {
			t.Errorf("ci.yml job %s: macOS and eph never run per change (rule 21)", jn)
		}
	}
	if !strings.Contains(string(b), fmt.Sprint("macOS runs on tagged releases only")) {
		t.Error("docs/ci-table.md lost the macOS rule line")
	}
}

func sortedCSV(s string) string {
	p := strings.Split(s, ",")
	sort.Strings(p)
	return strings.Join(p, ",")
}
