package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/bogworks/mash/internal/endpoint"
)

// versionOut runs the `version` tool in a serve whose env has MASH_PLUGIN_ROOT set to root (unset when root is nil).
func versionOut(t *testing.T, w *world, root *string) string {
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
	r := s.call(t, 30, "version", `{}`)
	if r["isError"] == true {
		t.Fatalf("version tool errored: %v", r)
	}
	return resultText(r)
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

func checkVersionLines(t *testing.T, w *world, out, plugin string) {
	t.Helper()
	lines := strings.Split(strings.TrimSpace(out), "\n")
	// binary, plugin, endpoint, client, then a drift warning when the plugin and binary differ.
	if len(lines) < 4 || len(lines) > 5 {
		t.Fatalf("want 4 or 5 lines, got %q", out)
	}
	if lines[3] != "client: unknown (no initialize seen)" {
		t.Errorf("client line %q", lines[3])
	}
	if want := versionLine(endpoint.CurrentVersion(), endpoint.CurrentCommit(), nil); lines[0] != want {
		t.Errorf("binary line %q, want %q", lines[0], want)
	}
	if !strings.HasPrefix(lines[1], plugin) {
		t.Errorf("plugin line %q, want prefix %q", lines[1], plugin)
	}
	if want := "endpoint a (owner " + w.e.Owner + ")"; lines[2] != want {
		t.Errorf("endpoint line %q, want %q", lines[2], want)
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
		prefix string
	}{
		{"good", &good, "plugin 9.9.9 (" + good + ")"},
		{"unset", nil, "plugin unknown (MASH_PLUGIN_ROOT is not set"},
		{"empty", &empty, "plugin unknown (MASH_PLUGIN_ROOT is not set"},
		{"literal", &literal, "plugin unknown (MASH_PLUGIN_ROOT is not expanded"},
		{"bad json", &bad, "plugin unknown ("},
		{"no version", &noVer, "plugin unknown ("},
		{"missing file", &missing, "plugin unknown ("},
	} {
		t.Run(c.name, func(t *testing.T) {
			checkVersionLines(t, w, versionOut(t, w, c.root), c.prefix)
		})
	}
}

func TestClientInfoShownByVersionAndStatus(t *testing.T) {
	w := newWorld(t, "a")
	for _, c := range []struct {
		name, info, version, status string
	}{
		{"claude code", `{"name":"claude-code","version":"2.1.316"}`, "client: Claude Code 2.1.316", "client: Claude Code 2.1.316"},
		{"old claude code", `{"name":"claude-code","version":"2.1.311"}`, "client: Claude Code 2.1.311", "client: WARN - Claude Code 2.1.311 is older than 2.1.312, the oldest version mash has a passing live receipt on. mash does not refuse it. Next: update Claude Code"},
		{"other client", `{"name":"pi","version":"0.1"}`, "client: pi 0.1", "client: pi 0.1"},
		{"none", ``, "client: unknown (no initialize seen)", "client: unknown (no initialize seen)"},
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
			if out := resultText(s.call(t, 3, "version", `{}`)); !strings.Contains(out, "\n"+c.version+"\n") && !strings.HasSuffix(out, "\n"+c.version) {
				t.Errorf("version tool: want line %q in:\n%s", c.version, out)
			}
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
