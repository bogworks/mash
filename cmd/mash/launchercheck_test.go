package main

import (
	"strings"
	"testing"

	"github.com/bogworks/mash/internal/cmdcore/cmds"
)

// A test never depends on the environment it runs in: no launcher row unless the test sets one.
func init() { launcherGetenv = func(string) string { return "" } }

func injectLauncher(t *testing.T, v string) {
	t.Helper()
	old := launcherGetenv
	launcherGetenv = func(k string) string {
		if k == launchedFromEnv {
			return v
		}
		return ""
	}
	t.Cleanup(func() { launcherGetenv = old })
}

func TestLauncherCheck(t *testing.T) {
	for _, tc := range []struct {
		env, state, detail string
		shown              bool
	}{
		{"", "", "", false},
		{"managed", cmds.CheckOK, "pinned mash", true},
		{"override", cmds.CheckOK, "MASH_BIN", true},
		{"path", cmds.CheckWarn, "mash from PATH, not the pinned one", true},
		{"other", cmds.CheckWarn, "the launcher said an unknown value: other", true},
	} {
		injectLauncher(t, tc.env)
		c, shown := launcherCheck()
		if shown != tc.shown || (shown && (c.Name != "launcher" || c.State != tc.state || c.Detail != tc.detail || c.Fix != "")) {
			t.Errorf("%q: %+v shown=%v", tc.env, c, shown)
		}
	}
}

// The launcher row is in doctor only when the launcher set MASH_LAUNCHED_FROM, and sits after claude.
func TestDoctorLauncherRow(t *testing.T) {
	env := claudeDoctorEnv(t)
	injectClaude(t, "2.1.320", nil)
	if r := runMash(env, "doctor"); strings.Contains(r.out, "launcher") {
		t.Fatalf("row without the variable: %s", r.out)
	}
	injectLauncher(t, "managed")
	r := runMash(env, "doctor")
	if r.code != 0 || !strings.Contains(r.out, "claude    ok    2.1.320\nlauncher  ok    pinned mash\ncreds     ok\n") {
		t.Fatalf("managed: %+v", r)
	}
	injectLauncher(t, "path")
	r = runMash(env, "doctor")
	if r.code != 0 || !strings.Contains(r.out, "launcher  warn  mash from PATH, not the pinned one\ncreds     ok\n") || !strings.HasSuffix(r.out, "\n\n1 warning.\n") {
		t.Fatalf("path: %+v", r)
	}
	if s := runMash(env, "doctor", "--strict"); s.code != 1 {
		t.Fatalf("path, strict: %+v", s)
	}
	if j := runMash(env, "doctor", "--json"); !strings.Contains(j.out, `{"name":"launcher","state":"warn","detail":"mash from PATH, not the pinned one","fix":null}`) {
		t.Fatalf("json: %s", j.out)
	}
}
