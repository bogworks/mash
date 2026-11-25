package cmds_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/bogworks/mash/internal/cliframe"
	"github.com/bogworks/mash/internal/cmdcore"
	"github.com/bogworks/mash/internal/cmdcore/cmds"
)

// These goldens pin the text of mash who as main printed it at the S2 capture (2f9a36f8): the CLI table, --wide, the
// MCP text and the MCP structuredContent. The directories are fixed in memory; the texts are copied from the capture.

func golden(t *testing.T, name string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func entries(online map[string]bool, order ...string) *fakeDir {
	d := &fakeDir{}
	for _, a := range order {
		d.entries = append(d.entries, cmds.WhoEntry{Address: a, Online: online[a]})
	}
	return d
}

func TestWhoGoldens(t *testing.T) {
	cases := []struct {
		name string
		dir  *fakeDir
		wide bool
	}{
		{"who_two_online", entries(map[string]bool{"o.dev.alice": true, "o.dev.bob": true}, "o.dev.bob", "o.dev.alice"), false},
		{"who_badsig", entries(map[string]bool{"o.dev.alice": true, "o.dev.bob": true}, "o.dev.mallory", "o.dev.alice", "o.dev.bob"), true},
		{"who_impostor", entries(map[string]bool{"o.dev.alice": true, "o.dev.bob": true, "o.dev.dora": true},
			"o.dev.victor", "o.dev.trent", "o.dev.dora", "o.dev.bob", "o.dev.alice"), false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			code, out, errs := runWho(tc.dir, cliframe.Global{})
			if code != 0 || errs != "" || out != golden(t, tc.name+".cli.golden") {
				t.Errorf("cli: %d %q %q", code, out, errs)
			}
			if tc.wide {
				code, out, errs = runWho(tc.dir, cliframe.Global{}, "--wide")
				if code != 0 || errs != "" || out != golden(t, tc.name+".wide.golden") {
					t.Errorf("wide: %d %q %q", code, out, errs)
				}
			}
			text, structured, err := cmdcore.MCPTool(cmds.Who, whoEnv(tc.dir)).CallStructured(t.Context(), json.RawMessage(`{}`))
			if err != nil {
				t.Fatal(err)
			}
			if text != golden(t, tc.name+".mcp_text.golden") {
				t.Errorf("mcp text: %q", text)
			}
			b, err := json.Marshal(structured)
			if err != nil {
				t.Fatal(err)
			}
			if string(b) != golden(t, tc.name+".structured.golden") {
				t.Errorf("structuredContent: %s", b)
			}
		})
	}
}
