package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

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
	full := endpoint.CurrentCommit()
	if full == "" {
		full = "unknown"
	}
	if sc["version"] != version || sc["commit"] != full || sc["plugin"] != plugin || len(sc) != 3 {
		t.Errorf("structuredContent %v, want version %q commit %q plugin %q", sc, version, full, plugin)
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
