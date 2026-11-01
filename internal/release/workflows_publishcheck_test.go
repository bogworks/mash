package release

import (
	"strings"
	"testing"
)

// publish-check runs on a schedule and on dispatch only, with the token scopes it needs (read the code, write the
// issue) and no more, on the Linux runner variable, and never runs the test suite.
func TestWorkflows_publishCheckIsAScheduledAudit(t *testing.T) {
	t.Parallel()
	w := load(t, "publish-check.yml")
	on, _ := w.On.(map[string]any)
	if len(on) != 2 {
		t.Errorf("publish-check.yml triggers = %v; want only schedule and workflow_dispatch", on)
	}
	for _, ev := range []string{"schedule", "workflow_dispatch"} {
		if _, ok := on[ev]; !ok {
			t.Errorf("publish-check.yml has no %s trigger", ev)
		}
	}
	if len(w.Permissions) != 1 || w.Permissions["contents"] != "read" {
		t.Errorf("publish-check.yml permissions = %v; want contents: read only at workflow level", w.Permissions)
	}
	j, ok := w.Jobs["check"]
	if !ok || len(w.Jobs) != 1 {
		t.Fatalf("publish-check.yml jobs = %d; want the one job check", len(w.Jobs))
	}
	if j.RunsOn != "${{ vars.RUNNER_LINUX || 'ubuntu-latest' }}" {
		t.Errorf("check runs on %q; want ${{ vars.RUNNER_LINUX || 'ubuntu-latest' }}", j.RunsOn)
	}
	if len(j.Permissions) != 2 || j.Permissions["contents"] != "write" || j.Permissions["issues"] != "write" {
		t.Errorf("check permissions = %v; want contents: write (lists its own drafts) and issues: write only", j.Permissions)
	}
	for _, st := range j.Steps {
		if strings.Contains(st.Run, "secrets.") || strings.Contains(st.Run, "matrix.") {
			t.Errorf("step %q uses secrets or a matrix; publish-check audits its own repo with github.token", st.Name)
		}
		for k, v := range st.Env {
			if strings.Contains(v, "secrets.") || strings.Contains(v, "matrix.") {
				t.Errorf("step %q env %s = %q; want no secrets or matrix", st.Name, k, v)
			}
		}
		if strings.Contains(st.Run, "go test") {
			t.Errorf("step %q runs go test; publish-check is an audit, not a test run", st.Name)
		}
	}
}
