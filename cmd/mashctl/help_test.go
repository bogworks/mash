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
	{"adopt"}, {"audit"}, {"inbox"}, {"inbox", "inspect"}, {"issued"}, {"add"}, {"label"}, {"unlabel"},
	{"retire"}, {"unzone"}, {"reissue"}, {"init"}, {"limits"}, {"permissions"}, {"provision"},
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

func TestHelpRequestedReadsOnlyFlagTokensBeforeDoubleDash(t *testing.T) {
	if helpRequested([]string{"add", "n", "--", "--help"}) {
		t.Error("--help after -- was taken as help")
	}
	if !helpRequested([]string{"add", "n", "--note", "--help"}) {
		t.Error("--help not found")
	}
	if helpRequested([]string{"label", "n", "--label", "help"}) {
		t.Error("the value help was taken as a flag")
	}
}

func TestVerbUsageFallsBackToFull(t *testing.T) {
	if got := verbUsage("init"); !strings.Contains(got, "mashctl init") || strings.Contains(got, "mashctl list") {
		t.Errorf("init usage = %q", got)
	}
	if got := verbUsage("nosuchverb"); got != ctl.Usage("mashctl") {
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
