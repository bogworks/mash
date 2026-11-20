package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/bogworks/mash/internal/endpoint"
)

// versionReply runs the `version` tool in a serve whose env has MASH_PLUGIN_ROOT set to root (unset when root is nil).
func versionReply(t *testing.T, w *world, root *string) map[string]any {
	t.Helper()
	env := map[string]string{}
	for k, v := range w.env["a"] {
		env[k] = v
	}
	if root != nil {
		env["MASH_PLUGIN_ROOT"] = *root
	}
	s := startServe(t, env)
	s.initialize(t)
	found := false
	for _, tl := range s.tools(t) {
		if tl.(map[string]any)["name"] == "version" {
			found = true
		}
	}
	if !found {
		t.Fatal("version is not in tools/list")
	}
	return s.call(t, 30, "version", `{}`)
}

func pluginRoot(t *testing.T, manifest string) string {
	t.Helper()
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, ".claude-plugin"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, ".claude-plugin", "plugin.json"), []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}
	return root
}

// checkVersion checks the version tool's reply: the one line `mash version` prints, and the object that is its structured
// result, with the plugin's version when there is one.
func checkVersion(t *testing.T, r map[string]any, plugin string) {
	t.Helper()
	if r["isError"] == true {
		t.Fatalf("version tool errored: %v", r)
	}
	version, commit := endpoint.CurrentVersion(), shortCommit(endpoint.CurrentCommit())
	if want := "mash v" + version + " (" + commit + ")"; resultText(r) != want {
		t.Errorf("text %q, want %q", resultText(r), want)
	}
	sc, _ := r["structuredContent"].(map[string]any)
	if sc == nil {
		t.Fatalf("no structuredContent in %v", r)
	}
	if sc["version"] != version || sc["commit"] != commit || sc["plugin"] != plugin || len(sc) != 3 {
		t.Errorf("structuredContent %v, want version %q commit %q plugin %q", sc, version, commit, plugin)
	}
}

func TestVersionTool(t *testing.T) {
	w := newWorld(t, "a")
	good := pluginRoot(t, `{"name":"mash","version":"9.9.9"}`)
	bad := pluginRoot(t, `{not json`)
	noVer := pluginRoot(t, `{"name":"mash"}`)
	empty := ""
	literal := "${CLAUDE_PLUGIN_ROOT}"
	missing := filepath.Join(t.TempDir(), "nope")
	for _, c := range []struct {
		name   string
		root   *string
		plugin string
	}{
		{"good", &good, "9.9.9"},
		{"unset", nil, ""},
		{"empty", &empty, ""},
		{"literal", &literal, ""},
		{"bad json", &bad, ""},
		{"no version", &noVer, ""},
		{"missing file", &missing, ""},
	} {
		t.Run(c.name, func(t *testing.T) {
			checkVersion(t, versionReply(t, w, c.root), c.plugin)
		})
	}
}

// The version tool takes no arguments, and answers with no endpoint (an inert session) too.
func TestVersionToolTakesNoArgumentsAndNeedsNoEndpoint(t *testing.T) {
	s := startServe(t, map[string]string{"HOME": t.TempDir()})
	s.initialize(t)
	checkVersion(t, s.call(t, 30, "version", `{}`), "")
	if r := s.call(t, 31, "version", `{"x":1}`); r["isError"] != true || !strings.Contains(resultText(r), "unknown argument x") {
		t.Errorf("an argument: %v", r)
	}
}

func TestClientInfoShownByStatus(t *testing.T) {
	w := newWorld(t, "a")
	for _, c := range []struct {
		name, info, status string
	}{
		{"claude code", `{"name":"claude-code","version":"2.1.316"}`, "client: Claude Code 2.1.316"},
		{"old claude code", `{"name":"claude-code","version":"2.1.311"}`, "client: WARN - Claude Code 2.1.311 is older than 2.1.312, the oldest version mash has a passing live receipt on. mash does not refuse it. Next: update Claude Code"},
		{"other client", `{"name":"pi","version":"0.1"}`, "client: pi 0.1"},
		{"none", ``, "client: unknown (no initialize seen)"},
	} {
		t.Run(c.name, func(t *testing.T) {
			s := startServe(t, w.env["a"])
			params := `"protocolVersion":"2025-06-18"`
			if c.info != "" {
				params += `,"clientInfo":` + c.info
			}
			s.send(t, `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{`+params+`}}`)
			s.await(t, 5*time.Second, idIs(1))
			s.send(t, `{"jsonrpc":"2.0","method":"notifications/initialized"}`)
			if out := statusOf(t, s, 4); !strings.Contains(out, "\n"+c.status) {
				t.Errorf("status tool: want line %q in:\n%s", c.status, out)
			}
		})
	}
}

func TestClientStatusLine(t *testing.T) {
	for _, c := range []struct {
		name, version string
		warn          bool
	}{
		{"claude-code", "2.1.311", true},
		{"claude-code", "2.1.312", false},
		{"claude-code", "2.1.316", false},
		{"claude-code", "2.2.0", false},
		{"claude-code", "1.9.99", true},
		{"claude-code", "garbage", false},
		{"claude-code", "", false},
		{"other", "0.0.1", false},
	} {
		got := strings.HasPrefix(clientStatusLine(c.name, c.version), "client: WARN")
		if got != c.warn {
			t.Errorf("%s %q: warn=%v, want %v", c.name, c.version, got, c.warn)
		}
	}
}

func TestDriftLine(t *testing.T) {
	for _, c := range []struct {
		plugin, binary string
		warn           bool
	}{
		{"0.3.1", "0.3.0", false},
		{"0.2.10", "0.3.0", true},
		{"0.4.0", "0.3.2", true},
		{"1.0.0", "0.3.0", true},
		{"", "0.3.0", false},
		{"garbage", "0.3.0", false},
		{"0.3.0", "0.0.0-dev", true},
	} {
		l := driftLine(c.plugin, c.binary)
		if (l != "") != c.warn {
			t.Errorf("plugin %q binary %q: got %q, want warn=%v", c.plugin, c.binary, l, c.warn)
		}
		if l != "" && !strings.HasPrefix(l, "plugin: WARN - plugin "+c.plugin+" and binary "+c.binary+" differ in major or minor. Next: update both") {
			t.Errorf("drift line %q", l)
		}
	}
}

func TestPluginVersionRead(t *testing.T) {
	if v := pluginVersion(pluginRoot(t, `{"version":"0.2.10"}`)); v != "0.2.10" {
		t.Errorf("got %q", v)
	}
	for _, r := range []string{"", "${CLAUDE_PLUGIN_ROOT}", pluginRoot(t, `{not json`), pluginRoot(t, `{}`)} {
		if v := pluginVersion(r); v != "" {
			t.Errorf("%q: got %q", r, v)
		}
	}
}
