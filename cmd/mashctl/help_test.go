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
	{"adopt"}, {"audit"}, {"issued"}, {"issued", "list"}, {"issued", "check"}, {"version"},
	{"endpoint"}, {"init"}, {"limits"},
	{"endpoint", "list"}, {"endpoint", "show"}, {"endpoint", "set"}, {"endpoint", "unset"}, {"endpoint", "suspend"}, {"endpoint", "resume"},
	{"endpoint", "inbox"}, {"endpoint", "permissions"}, {"endpoint", "create"}, {"endpoint", "reissue"}, {"endpoint", "delete"},
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
					"MASH_SERVER": "nats://127.0.0.1:1",
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

// Help is only the first argument after the verb (after the sub-verb for issued): a value, a note or a
// label that contains the words is never help.
func TestHelpRequestedOnlyFirstArgument(t *testing.T) {
	yes := []struct {
		verb string
		args []string
	}{
		{"endpoint", []string{"-h"}}, {"limits", []string{"-h"}},
		{"issued", []string{"check", "--help"}}, {"issued", []string{"list", "-h"}}, {"issued", []string{"-h"}},
		{"version", []string{"--help"}},
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
		{"endpoint", []string{"n", "-h"}}, {"limits", []string{"n", "--help"}},
		{"issued", []string{"check", "n", "--help"}},
	}
	for _, c := range no {
		if helpRequested(c.verb, c.args) {
			t.Errorf("%s %v: taken as help", c.verb, c.args)
		}
	}
}

func TestVerbUsageFallsBackToFull(t *testing.T) {
	if got := verbUsage([]string{"init"}); !strings.Contains(got, "mashctl init") || strings.Contains(got, "mashctl limits") {
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

// Each command of the endpoint noun that takes its arguments through epSpec has a help of lines under 80 columns that
// starts with its usage line, and the noun's help lists it.
func TestEndpointSubcommandHelp(t *testing.T) {
	specs := []epSpec{endpointListSpec, endpointShowSpec, endpointSetSpec, endpointUnsetSpec, endpointSuspendSpec, endpointResumeSpec, endpointInboxSpec, endpointPermissionsSpec}
	env := func(string) string { return "" }
	var noun bytes.Buffer
	if rc := runEnv([]string{"endpoint", "--help"}, env, &noun, &noun); rc != 0 {
		t.Fatalf("endpoint --help: rc %d", rc)
	}
	for _, l := range strings.Split(noun.String(), "\n") {
		if len(l) >= 80 {
			t.Errorf("endpoint --help: line of %d columns: %q", len(l), l)
		}
	}
	for _, s := range specs {
		var out, errb bytes.Buffer
		if rc := runEnv([]string{"endpoint", s.name, "--help"}, env, &out, &errb); rc != 0 || errb.Len() != 0 {
			t.Fatalf("endpoint %s --help: rc %d stderr %q", s.name, rc, errb.String())
		}
		if !strings.HasPrefix(out.String(), "usage: mashctl endpoint "+s.name) || !strings.Contains(out.String(), "\nExample:\n") {
			t.Errorf("endpoint %s --help: %q", s.name, out.String())
		}
		for _, l := range strings.Split(out.String(), "\n") {
			if len(l) >= 80 {
				t.Errorf("endpoint %s --help: line of %d columns: %q", s.name, len(l), l)
			}
		}
		if !strings.Contains(noun.String(), "\n  "+s.name+" ") {
			t.Errorf("endpoint --help does not list %s", s.name)
		}
		// every option the command takes is in its help
		for _, f := range append(append([]string{"--json"}, s.flags...), s.values...) {
			if !strings.Contains(out.String(), f) {
				t.Errorf("endpoint %s --help does not show %s", s.name, f)
			}
		}
	}
}
