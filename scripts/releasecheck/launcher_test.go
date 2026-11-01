package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bogworks/mash/internal/release"
)

// launcherScript stands in for plugin/scripts/mash: __prefetch is answered by prefetch, anything else runs MASH_BIN, and
// it refuses to run unless the gate set CLAUDE_PLUGIN_ROOT to the directory the script is in.
func launcherScript(prefetch string) string {
	return "#!/bin/sh\n[ \"$CLAUDE_PLUGIN_ROOT/scripts/mash\" -ef \"$0\" ] || { echo \"bad CLAUDE_PLUGIN_ROOT $CLAUDE_PLUGIN_ROOT\" >&2; exit 1; }\n" +
		"[ -x \"$MASH_BIN\" ] || exit 1\ncase \"$1\" in __prefetch) " + prefetch + " ;; esac\nexec \"$MASH_BIN\" \"$@\"\n"
}

const launcherMCP = `{"mcpServers": {"mash": {"command": "${CLAUDE_PLUGIN_ROOT}/scripts/mash", "args": ["serve"], "env": {"MASH_PLUGIN_ROOT": "${CLAUDE_PLUGIN_ROOT}"}}}}` + "\n"

const launcherHookCmd = `d=\"${MASH_STATE_DIR:-${XDG_STATE_HOME:-$HOME/.local/state}/mash}\"; mkdir -p \"$d\" 2>/dev/null; l=\"$d/hook.log\"; (: >>\"$l\") 2>/dev/null || l=/dev/null; sh \"${CLAUDE_PLUGIN_ROOT}/scripts/mash\" hook 2>>\"$l\"; exit 0`

func launcherHooks(withPrefetch bool) string {
	pre := ""
	if withPrefetch {
		pre = `"SessionStart": [{"hooks": [{"type": "command", "command": "sh \"${CLAUDE_PLUGIN_ROOT}/scripts/mash\" __prefetch"}]}], `
	}
	return `{"hooks": {` + pre + `"PostToolUse": [{"matcher": "*", "hooks": [{"type": "command", "command": "` + launcherHookCmd + `"}]}], "UserPromptSubmit": [{"hooks": [{"type": "command", "command": "` + launcherHookCmd + `"}]}]}}` + "\n"
}

func launcherPlugin(prefetch string, withPrefetch bool) map[string]string {
	return map[string]string{
		release.PluginMCP:     launcherMCP,
		release.PluginHooks:   launcherHooks(withPrefetch),
		"plugin/scripts/mash": launcherScript(prefetch),
	}
}

func commitFiles(t *testing.T, f *fixture, files map[string]string) {
	t.Helper()
	for name, body := range files {
		write(t, f.repo, name, body)
	}
	commitPlugin(t, f, "x.txt", "x")
}

func TestCheck_launcherFormPlugin(t *testing.T) {
	for _, c := range []struct {
		name     string
		prefetch string
		with     bool
		want     string // empty: passes
	}{
		{"launcher with a silent prefetch", "exit 0", true, ""},
		{"launcher without a prefetch hook (older launcher plugin)", "exit 0", false, ""},
		{"prefetch prints to stdout", "echo downloading; exit 0", true, "stdout"},
		{"prefetch fails", "exit 3", true, "exits 3"},
	} {
		f := newFixture(t)
		commitFiles(t, f, launcherPlugin(c.prefetch, c.with))
		f.setLatencySpec(func(string, serveSpec) latencies { return latencies{Fresh: 1, FreshOK: true} })
		err := f.check()
		switch {
		case c.want == "" && err != nil:
			t.Errorf("%s: %v", c.name, err)
		case c.want != "" && (err == nil || exitCode(err) != 3 || !strings.Contains(err.Error(), c.want) || !strings.Contains(err.Error(), "__prefetch")):
			t.Errorf("%s: want a refusal naming __prefetch and %q, got %v", c.name, c.want, err)
		}
	}
}

// The latency step launches serve through the plugin's launcher, so its spec must carry the materialized plugin dir.
func TestCheck_launcherFormReachesTheLatencyStep(t *testing.T) {
	f := newFixture(t)
	commitFiles(t, f, launcherPlugin("exit 0", true))
	var roots []string
	f.setLatencySpec(func(_ string, s serveSpec) latencies {
		f.measuredMu.Lock()
		roots = append(roots, s.Root)
		f.measuredMu.Unlock()
		return latencies{Fresh: 1, FreshOK: true}
	})
	if err := f.check(); err != nil {
		t.Fatal(err)
	}
	if len(roots) == 0 || roots[0] == "" || filepath.Base(roots[0]) != "plugin" {
		t.Fatalf("latency spec roots %q: want the materialized plugin dir", roots)
	}
}

// A previous release whose plugin runs the launcher is run against the candidate, with the candidate's binary as MASH_BIN.
func TestCheck_previousLauncherPluginRunsAgainstTheCandidate(t *testing.T) {
	f := newFixture(t)
	files := launcherPlugin("exit 0", true)
	prev := map[string]*string{}
	for k, v := range files {
		prev[k] = strp(v)
	}
	withPreviousPlugin(t, f, prev)
	if err := f.check(); err != nil {
		t.Fatal(err)
	}
	// the same previous plugin with a noisy prefetch is the previous release's fault, but still a finding against it
	f = newFixture(t)
	files = launcherPlugin("echo hi", true)
	prev = map[string]*string{}
	for k, v := range files {
		prev[k] = strp(v)
	}
	withPreviousPlugin(t, f, prev)
	err := f.check()
	if err == nil || exitCode(err) != 3 || !strings.Contains(err.Error(), "the previous release's plugin (v0.0.9)") || !strings.Contains(err.Error(), "__prefetch") {
		t.Fatalf("want a refusal naming the previous release's plugin and __prefetch, got %v", err)
	}
}

func TestCheck_unrelatedMCPCommandIsRefused(t *testing.T) {
	f := newFixture(t)
	commitPlugin(t, f, release.PluginMCP, "{\"mcpServers\": {\"mash\": {\"command\": \"node\", \"args\": [\"serve\"]}}}\n")
	if err := f.check(); err == nil || exitCode(err) != 3 || !strings.Contains(err.Error(), `"node"`) {
		t.Errorf("node: %v", err)
	}
}

func TestMaterializePlugin(t *testing.T) {
	f := newFixture(t)
	commitFiles(t, f, launcherPlugin("exit 0", true))
	root, err := materializePlugin(f.repo, f.o.X, filepath.Join(t.TempDir(), "p"))
	if err != nil {
		t.Fatal(err)
	}
	st, err := os.Stat(filepath.Join(root, "scripts", "mash"))
	if err != nil || st.Mode()&0o100 == 0 {
		t.Fatalf("launcher in the copy: %v, %v", st, err)
	}
	if _, err := os.Stat(filepath.Join(root, ".mcp.json")); err != nil {
		t.Error(err)
	}
}
