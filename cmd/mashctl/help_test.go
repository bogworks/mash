package main

import (
	"bytes"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/bogworks/mash/internal/ctl"
)

// helpVerbs is every verb run dispatches; each takes -h and --help, prints its usage and changes nothing.
var helpVerbs = [][]string{
	{"adopt"}, {"audit"}, {"inbox"}, {"inbox", "inspect"}, {"issued"}, {"issued", "list"}, {"issued", "check"}, {"version"}, {"label"}, {"unlabel"},
	{"retire"}, {"init"}, {"limits"}, {"permissions"}, {"provision"},
	{"set-meta"}, {"suspend"}, {"resume"}, {"suspended"}, {"deprovision"}, {"list"},
}

func TestVerbHelpPrintsUsageAndChangesNothing(t *testing.T) {
	for _, v := range helpVerbs {
		for _, flag := range []string{"--help", "-h"} {
			name := strings.Join(v, " ") + " " + flag
			t.Run(name, func(t *testing.T) {
				home := t.TempDir()
				vars := map[string]string{
					"HOME": home, "XDG_CONFIG_HOME": filepath.Join(home, "c"),
					"XDG_DATA_HOME": filepath.Join(home, "d"), "XDG_STATE_HOME": filepath.Join(home, "s"),
					"MASH_URL": "nats://127.0.0.1:1",
				}
				env := func(k string) string { return vars[k] }
				lookup := func(k string) (string, bool) { x, ok := vars[k]; return x, ok }
				args := append(append([]string{}, v...), flag)
				var out, errb bytes.Buffer
				start := time.Now()
				rc := run(args, env, lookup, &out, &errb)
				if rc != 0 {
					t.Fatalf("rc = %d, want 0; stderr %q", rc, errb.String())
				}
				if time.Since(start) > 2*time.Second {
					t.Errorf("took %v; help must not touch the network", time.Since(start))
				}
				if out.Len() == 0 || !strings.Contains(out.String(), v[0]) {
					t.Errorf("stdout %q does not mention %q", out.String(), v[0])
				}
				if errb.Len() != 0 {
					t.Errorf("stderr = %q, want empty", errb.String())
				}
				var found []string
				_ = filepath.WalkDir(home, func(p string, d fs.DirEntry, err error) error {
					if p != home {
						found = append(found, p)
					}
					return nil
				})
				if len(found) != 0 {
					t.Errorf("help wrote %v", found)
				}
			})
		}
	}
}

func TestVerbUsagePicksOwnLines(t *testing.T) {
	cases := []struct {
		args      []string
		want, not string
	}{
		{[]string{"issued", "check"}, "mashctl issued check", "mashctl issued list"},
		{[]string{"issued", "list"}, "mashctl issued list", "mashctl issued check"},
		{[]string{"inbox", "inspect"}, "mashctl inbox inspect", ""},
		{[]string{"unlabel"}, "mashctl unlabel", "mashctl label"},
		{[]string{"label"}, "mashctl label", "mashctl unlabel"},
		{[]string{"version"}, "usage: mashctl version", ""},
	}
	for _, c := range cases {
		var out, errb bytes.Buffer
		env := func(string) string { return "" }
		lookup := func(string) (string, bool) { return "", false }
		if rc := run(append(append([]string{}, c.args...), "--help"), env, lookup, &out, &errb); rc != 0 {
			t.Errorf("%v: rc %d", c.args, rc)
		}
		got := out.String()
		if !strings.Contains(got, c.want) || (c.not != "" && strings.Contains(got, c.not)) {
			t.Errorf("%v: stdout %q, want %q and not %q", c.args, got, c.want, c.not)
		}
	}
}

// Help is only the first argument after the verb (after the sub-verb for issued and inbox): a value, a note or a
// label that contains the words is never help.
func TestHelpRequestedOnlyFirstArgument(t *testing.T) {
	yes := []struct {
		verb string
		args []string
	}{
		{"label", []string{"--help"}}, {"set-meta", []string{"-h"}},
		{"retire", []string{"-h"}}, {"suspend", []string{"-h"}},
		{"issued", []string{"check", "--help"}}, {"issued", []string{"list", "-h"}}, {"issued", []string{"-h"}},
		{"inbox", []string{"inspect", "-h"}}, {"version", []string{"--help"}},
	}
	for _, c := range yes {
		if !helpRequested(c.verb, c.args) {
			t.Errorf("%s %v: not help, want help", c.verb, c.args)
		}
	}
	no := []struct {
		verb string
		args []string
	}{
		{"label", []string{"n", "--label", "help"}},
		{"label", []string{"n", "role=-h"}}, {"label", []string{"n", "k=--help"}}, {"label", []string{"n", "k", "--help"}},
		{"set-meta", []string{"n", "note", "--help"}}, {"set-meta", []string{"n", "--help"}},
		{"retire", []string{"n", "-h"}}, {"suspend", []string{"n", "--help"}},
		{"unlabel", []string{"n", "-h"}}, {"issued", []string{"check", "n", "--help"}}, {"inbox", []string{"inspect", "n", "-h"}},
	}
	for _, c := range no {
		if helpRequested(c.verb, c.args) {
			t.Errorf("%s %v: taken as help", c.verb, c.args)
		}
	}
}

func TestVerbUsageFallsBackToFull(t *testing.T) {
	if got := verbUsage([]string{"init"}); !strings.Contains(got, "mashctl init") || strings.Contains(got, "mashctl list") {
		t.Errorf("init usage = %q", got)
	}
	if got := verbUsage([]string{"nosuchverb"}); got != ctl.Usage("mashctl") {
		t.Error("unmatched verb did not fall back to the full usage")
	}
}

func TestInitHelpCreatesNoConfigDir(t *testing.T) {
	home := t.TempDir()
	vars := map[string]string{"HOME": home, "XDG_CONFIG_HOME": filepath.Join(home, "cfg")}
	env := func(k string) string { return vars[k] }
	lookup := func(k string) (string, bool) { x, ok := vars[k]; return x, ok }
	var out, errb bytes.Buffer
	if rc := run([]string{"init", "--help"}, env, lookup, &out, &errb); rc != 0 {
		t.Fatalf("rc = %d", rc)
	}
	if _, err := os.Stat(filepath.Join(home, "cfg")); !os.IsNotExist(err) {
		t.Errorf("config dir exists after init --help: %v", err)
	}
}
