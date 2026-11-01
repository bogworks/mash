package endpoint

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
)

// The instructions name the tag Claude Code actually puts on mash's channel events: source="plugin:<plugin>:<server>",
// from the plugin's name and its MCP server's key (a live session's events read <channel source="plugin:mash:mash">).
// Derived from the plugin's own manifests, so renaming either keeps the instructions honest.
func TestInstructionsNameTheChannelTagClaudeCodeEmits(t *testing.T) {
	var manifest struct {
		Name string `json:"name"`
	}
	b, err := os.ReadFile("../../plugin/.claude-plugin/plugin.json")
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(b, &manifest); err != nil || manifest.Name == "" {
		t.Fatalf("plugin.json: %v %q", err, manifest.Name)
	}
	var mcp struct {
		Servers map[string]json.RawMessage `json:"mcpServers"`
	}
	if b, err = os.ReadFile("../../plugin/.mcp.json"); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(b, &mcp); err != nil || len(mcp.Servers) != 1 {
		t.Fatalf(".mcp.json: %v, %d servers (want exactly one)", err, len(mcp.Servers))
	}
	var server string
	for k := range mcp.Servers {
		server = k
	}
	want := `<channel source="plugin:` + manifest.Name + `:` + server + `">`
	got := Instructions("a", "o", "")
	if !strings.Contains(got, want) {
		t.Fatalf("instructions do not name %s:\n%s", want, got)
	}
	if strings.Contains(got, `source="`+server+`"`) {
		t.Fatalf("instructions still name the bare server tag source=%q", server)
	}
}
