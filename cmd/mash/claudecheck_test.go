package main

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"strings"
	"testing"

	"github.com/bogworks/mash/internal/host"
)

// injectClaude makes the claude row see a claude that prints out (or fails with err); it undoes itself after the test.
func injectClaude(t *testing.T, out string, err error) {
	t.Helper()
	oldLook, oldRun := claudeLookPath, claudeVersionOutput
	claudeLookPath = func() (string, error) { return "/fake/claude", nil }
	claudeVersionOutput = func(context.Context, string) (string, error) { return out, err }
	t.Cleanup(func() { claudeLookPath, claudeVersionOutput = oldLook, oldRun })
}

func TestParseVersion(t *testing.T) {
	for in, want := range map[string]struct {
		v  [3]int
		ok bool
	}{
		"2.1.320 (Claude Code)": {[3]int{2, 1, 320}, true},
		"v2.1.0":                {[3]int{2, 1, 0}, true},
		"claude 10.20.30\n":     {[3]int{10, 20, 30}, true},
		"garbage":               {[3]int{}, false},
		"2.1":                   {[3]int{}, false},
		"":                      {[3]int{}, false},
	} {
		if v, ok := parseVersion(in); v != want.v || ok != want.ok {
			t.Errorf("parseVersion(%q) = %v, %v", in, v, ok)
		}
	}
}

func TestClaudeCheckCases(t *testing.T) {
	for _, tc := range []struct {
		name, out string
		err       error
		missing   bool
		state     string
		detail    string
	}{
		{"recent enough", "2.1.320 (Claude Code)\n", nil, false, "ok", "2.1.320"},
		{"at the minimum", "2.1.312 (Claude Code)", nil, false, "ok", "2.1.312"},
		{"older", "2.1.300 (Claude Code)", nil, false, "fail", "2.1.300 is below the tested " + host.MinClaudeCode},
		{"older minor", "2.0.999", nil, false, "fail", "2.0.999 is below the tested " + host.MinClaudeCode},
		{"newer", "2.9.0 (Claude Code)", nil, false, "warn", "2.9.0 is above the tested " + host.TestedClaudeSeries},
		{"next series", "2.2.0", nil, false, "warn", "2.2.0 is above the tested " + host.TestedClaudeSeries},
		{"later major", "3.0.0", nil, false, "warn", "3.0.0 is above the tested " + host.TestedClaudeSeries},
		{"none on PATH", "", nil, true, "skip", "no claude command found"},
		{"run fails", "", errors.New("boom"), false, "warn", "the version of claude could not be read"},
		{"nothing parseable", "hello", nil, false, "warn", "the version of claude could not be read"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			injectClaude(t, tc.out, tc.err)
			if tc.missing {
				claudeLookPath = func() (string, error) { return "", exec.ErrNotFound }
			}
			c := claudeCheck(context.Background())
			if c.Name != "claude" || c.State != tc.state || c.Detail != tc.detail || c.Fix != "" {
				t.Fatalf("%+v", c)
			}
		})
	}
}

// The two literal bounds are stated in the doctor's words only through the constants, and the compatibility page says them too.
func TestCompatibilityPageStatesClaudeBounds(t *testing.T) {
	b, err := os.ReadFile("../../docs/compatibility.md")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{host.MinClaudeCode, host.TestedClaudeSeries} {
		if !strings.Contains(string(b), want) {
			t.Errorf("docs/compatibility.md does not contain %q", want)
		}
	}
}

func claudeDoctorEnv(t *testing.T) map[string]string {
	w := tlsWorld(t, "a")
	return strictEnv(w, "a", nil)
}

func TestDoctorClaudeRow(t *testing.T) {
	for _, tc := range []struct {
		name, out string
		row       string
		code      int
		strict    int
		summary   string
	}{
		{"ok", "2.1.320 (Claude Code)", "claude    ok    2.1.320\n", 0, 0, "No problems."},
		{"fail", "2.1.300", "claude    FAIL  2.1.300 is below the tested 2.1.312\n", 1, 1, "1 problem."},
		{"warn", "2.9.0", "claude    warn  2.9.0 is above the tested 2.1\n", 0, 1, "1 warning."},
		{"unreadable", "nope", "claude    warn  the version of claude could not be read\n", 0, 1, "1 warning."},
	} {
		t.Run(tc.name, func(t *testing.T) {
			env := claudeDoctorEnv(t)
			injectClaude(t, tc.out, nil)
			r := runMash(env, "doctor")
			if r.code != tc.code || r.errs != "" || !strings.Contains(r.out, "config    ok\n"+tc.row+"creds     ok\n") || !strings.HasSuffix(r.out, "\n\n"+tc.summary+"\n") {
				t.Fatalf("doctor: %+v", r)
			}
			if s := runMash(env, "doctor", "--strict"); s.code != tc.strict {
				t.Fatalf("doctor --strict: %+v", s)
			}
			j := runMash(env, "doctor", "--json")
			if j.code != tc.code || !strings.Contains(j.out, `{"name":"claude","state":"`) {
				t.Fatalf("doctor --json: %+v", j)
			}
		})
	}
}

func TestDoctorClaudeRowJSONAndSkip(t *testing.T) {
	env := claudeDoctorEnv(t)
	injectClaude(t, "2.1.300 (Claude Code)", nil)
	j := runMash(env, "doctor", "--json")
	if !strings.Contains(j.out, `{"name":"claude","state":"fail","detail":"2.1.300 is below the tested 2.1.312","fix":null}`) {
		t.Fatalf("%s", j.out)
	}
	claudeLookPath = func() (string, error) { return "", exec.ErrNotFound }
	j = runMash(env, "doctor", "--json")
	if !strings.Contains(j.out, `{"name":"claude","state":"skip","detail":"no claude command found","fix":null}`) || j.code != 0 {
		t.Fatalf("%+v", j)
	}
}
