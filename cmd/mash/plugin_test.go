package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/bogworks/mash/plugin"
)

func readJSON(t *testing.T, path string, v any) {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(b, v); err != nil {
		t.Fatalf("%s: %v", path, err)
	}
}

func TestPluginManifests(t *testing.T) {
	var mkt struct {
		Name    string `json:"name"`
		Plugins []struct {
			Name   string `json:"name"`
			Source string `json:"source"`
		} `json:"plugins"`
	}
	readJSON(t, "../../.claude-plugin/marketplace.json", &mkt)
	if mkt.Name != "mash" || len(mkt.Plugins) != 1 || mkt.Plugins[0].Name != "mash" || mkt.Plugins[0].Source != "./plugin" {
		t.Fatalf("marketplace %+v", mkt)
	}
	var pl struct {
		Name, Version, License string
	}
	readJSON(t, "../../plugin/.claude-plugin/plugin.json", &pl)
	if pl.Name != "mash" || pl.Version == "" || pl.License != "Apache-2.0" {
		t.Fatalf("plugin %+v", pl)
	}
	var mcp struct {
		MCPServers map[string]struct {
			Command string            `json:"command"`
			Args    []string          `json:"args"`
			Env     map[string]string `json:"env"`
		} `json:"mcpServers"`
	}
	readJSON(t, "../../plugin/.mcp.json", &mcp)
	s, ok := mcp.MCPServers["mash"]
	if !ok || s.Command != "${CLAUDE_PLUGIN_ROOT}/scripts/mash" || len(s.Args) != 1 || s.Args[0] != "serve" {
		t.Fatalf(".mcp.json %+v", mcp)
	}
	// #16: Claude Code drops channel events that arrive before its first prompt, so serve spools from the start.
	// The version tool reads the plugin's own manifest through MASH_PLUGIN_ROOT, which Claude Code expands.
	if len(s.Env) != 2 || s.Env["MASH_SPOOL_FROM_START"] != "1" || s.Env["MASH_PLUGIN_ROOT"] != "${CLAUDE_PLUGIN_ROOT}" {
		t.Fatalf(".mcp.json env %v, want only MASH_SPOOL_FROM_START=1 and MASH_PLUGIN_ROOT=${CLAUDE_PLUGIN_ROOT}", s.Env)
	}
}

func TestMarketplaceOwnerFromAuthor(t *testing.T) {
	dir := t.TempDir()
	if err := exportPlugin(dir, plugin.Files); err != nil {
		t.Fatal(err)
	}
	var m struct{ Owner map[string]string }
	readJSON(t, filepath.Join(dir, ".claude-plugin", "marketplace.json"), &m)
	if m.Owner["name"] != "Bogworks" || m.Owner["url"] != "https://github.com/bogworks" || len(m.Owner) != 2 {
		t.Fatalf("owner = %v", m.Owner)
	}
}

func TestMarketplaceWithoutAuthor(t *testing.T) {
	b, err := marketplaceJSON([]byte(`{"description":"d","version":"1"}`))
	if err != nil {
		t.Fatal(err)
	}
	var m struct{ Owner map[string]string }
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatal(err)
	}
	if m.Owner["name"] != "mash" || len(m.Owner) != 1 {
		t.Fatalf("owner = %v", m.Owner)
	}
}
