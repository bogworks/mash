package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

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
	if len(lines) != 3 {
		t.Fatalf("want 3 lines, got %q", out)
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
