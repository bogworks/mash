package main

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/bogworks/mash/internal/endpoint"
)

// shellSession is the session object of status --detail --json run at a shell, as raw JSON ("null" at no session).
func shellSession(t *testing.T, w *world) (string, string) {
	t.Helper()
	out, _ := w.cli(t, "a", "status", "--detail", "--json")
	var m struct {
		Ready      bool            `json:"ready"`
		ReasonCode *string         `json:"reason_code"`
		Session    json.RawMessage `json:"session"`
	}
	if err := json.Unmarshal([]byte(out), &m); err != nil {
		t.Fatalf("status --detail --json: %q: %v", out, err)
	}
	return string(m.Session), out
}

// toolSession is the session the status tool says (detail: true) in the same JSON words as the shell's object.
func toolSession(t *testing.T, s *session, id int) string {
	t.Helper()
	out := resultText(s.call(t, id, "status", `{"detail":true}`))
	field := func(label string) string {
		_, rest, _ := strings.Cut(out, label)
		line, _, _ := strings.Cut(rest, "\n")
		return strings.TrimSpace(line)
	}
	holds := map[string]string{"yes": "true", "no": "false"}[field("holds endpoint:")]
	return `{"holds_endpoint":` + holds + `,"hooks":"` + field("hooks:") + `","storage":"` + field("storage:") + `"}`
}

// The status tool in a session and mash status at its shell say the same about the hooks: not_run until a hook runs,
// degraded once serve proved none does, ran again after a hook; the doctor tool's delivery row follows.
func TestStatusDetailHookStatesAgreeAtShellAndInSession(t *testing.T) {
	w, s := hooklessWorld(t, 150*time.Millisecond)
	sp := spoolFor(w, "a")
	const notRun = `{"holds_endpoint":true,"hooks":"not_run","storage":"ok"}`
	eventually(t, 10*time.Second, "shell sees the session", func() bool { got, _ := shellSession(t, w); return got == notRun })
	if got := toolSession(t, s, 30); got != notRun {
		t.Errorf("tool: %s", got)
	}
	if out := resultText(s.call(t, 31, "doctor", `{}`)); !strings.Contains(out, "delivery  warn  hooks have not run in this session yet; they run with\n                the first prompt or tool call\n") {
		t.Errorf("doctor: %s", out)
	}
	time.Sleep(endpoint.HookGrace + 100*time.Millisecond)
	toolSession(t, s, 32) // a second tool call, well after the first: no hook ran, so serve records degraded
	const degraded = `{"holds_endpoint":true,"hooks":"degraded","storage":"ok"}`
	if got := toolSession(t, s, 33); got != degraded {
		t.Errorf("tool: %s", got)
	}
	eventually(t, 10*time.Second, "shell sees degraded", func() bool { got, _ := shellSession(t, w); return got == degraded })
	if out := resultText(s.call(t, 34, "doctor", `{}`)); !strings.Contains(out, "delivery  FAIL  hooks are not delivering mail to this session;\n                restart the session after reinstalling\n") {
		t.Errorf("doctor: %s", out)
	}
	// a hook runs: both say ran at once, with no wait for serve
	if err := sp.Touch(); err != nil {
		t.Fatal(err)
	}
	const ran = `{"holds_endpoint":true,"hooks":"ran","storage":"ok"}`
	if got, _ := shellSession(t, w); got != ran {
		t.Errorf("shell after a hook: %s", got)
	}
	if got := toolSession(t, s, 35); got != ran {
		t.Errorf("tool after a hook: %s", got)
	}
}

// At a shell with no session on the machine, the session is null.
func TestStatusDetailNoSessionAtShell(t *testing.T) {
	w := newWorld(t, "a", "b")
	if got, out := shellSession(t, w); got != "null" {
		t.Errorf("session %s in %s", got, out)
	}
}

var hookRanID = 9000

// hookRan records a hook run in the spool of session s (endpoint name) and waits until the status tool says so.
func hookRan(t *testing.T, w *world, s *session, name string) {
	t.Helper()
	sp := spoolFor(w, name)
	eventually(t, 10*time.Second, "the session sees a hook run", func() bool {
		if sp.Touch() != nil {
			return false
		}
		hookRanID++
		return strings.Contains(resultText(s.call(t, hookRanID, "status", `{"detail":true}`)), "hooks:          ran")
	})
}

// The delivery row's two hook texts are the specification's: a warning with no hint while hooks have not run (so
// doctor --strict fails until the first hook runs: there is no grace timer), and a failure with the hint to reinstall.
func TestDoctorDeliveryRowHookTexts(t *testing.T) {
	w, s := hooklessWorld(t, 150*time.Millisecond)
	eventually(t, 10*time.Second, "shell sees the session", func() bool { got, _ := shellSession(t, w); return strings.Contains(got, "not_run") })
	const warn = "delivery  warn  hooks have not run in this session yet; they run with\n                the first prompt or tool call\n"
	if out := resultText(s.call(t, 80, "doctor", `{}`)); !strings.Contains(out, warn) || strings.Contains(out, "hint:") {
		t.Errorf("not yet run: %q", out) // no hint line after it
	}
	strict := s.call(t, 81, "doctor", `{"strict":true}`)
	if strict["isError"] != true {
		t.Errorf("a just-started session passed --strict: %v", strict)
	}
	sc, _ := s.call(t, 82, "doctor", `{}`)["structuredContent"].(map[string]any)
	if row := deliveryRow(t, sc); row["detail"] != "hooks have not run in this session yet; they run with the first prompt or tool call" || row["fix"] != nil {
		t.Errorf("row: %v", row)
	}
	time.Sleep(endpoint.HookGrace + 100*time.Millisecond)
	toolSession(t, s, 83)
	toolSession(t, s, 84) // two calls well apart, no hook: degraded
	const fail = "delivery  FAIL  hooks are not delivering mail to this session;\n                restart the session after reinstalling\n"
	if out := resultText(s.call(t, 85, "doctor", `{}`)); !strings.Contains(out, fail) || !strings.HasSuffix(out, "\nhint: claude plugin install mash@mash") {
		t.Errorf("failing: %q", out)
	}
	sc, _ = s.call(t, 86, "doctor", `{}`)["structuredContent"].(map[string]any)
	if row := deliveryRow(t, sc); row["detail"] != "hooks are not delivering mail to this session; restart the session after reinstalling" || row["fix"] != "claude plugin install mash@mash" {
		t.Errorf("row: %v", row)
	}
}
