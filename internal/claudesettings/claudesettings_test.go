package claudesettings

import (
	"os"
	"path/filepath"
	"testing"
)

const withMash = `{"channelsEnabled": true, "allowedChannelPlugins": [{"marketplace":"mash","plugin":"mash"}]}`

// layout makes a temp HOME and managed dir; files maps a path relative to them ("home/..." or "managed/...") to content.
func layout(t *testing.T, files map[string]string) (home, managed string) {
	t.Helper()
	root := t.TempDir()
	home, managed = filepath.Join(root, "home"), filepath.Join(root, "managed")
	for rel, body := range files {
		p := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return home, managed
}

func allows(s *Source) bool { return s != nil && s.AllowsMash != nil && *s.AllowsMash }

func TestServerManagedShadowsLocalFile(t *testing.T) {
	home, managed := layout(t, map[string]string{
		"home/.claude/remote-settings.json": `{"permissions": {"deny": ["Bash(curl *)"]}}`,
		"managed/managed-settings.json":     withMash,
	})
	w, shadowed := Effective(Sources(home, "linux", managed))
	if w == nil || w.Name != ServerName {
		t.Fatalf("winner = %+v, want the server-managed source", w)
	}
	if allows(w) {
		t.Fatal("the server source must not allow mash")
	}
	if len(shadowed) != 1 || shadowed[0].Name != FileName || !allows(&shadowed[0]) {
		t.Fatalf("shadowed = %+v, want the local file listing mash", shadowed)
	}
}

func TestMergeOptInUnionsSources(t *testing.T) {
	home, managed := layout(t, map[string]string{
		"home/.claude/remote-settings.json": `{"permissions": {"deny": ["Bash(curl *)"]}, "managedSourcesBehavior": "merge"}`,
		"managed/managed-settings.json":     withMash,
	})
	w, _ := Effective(Sources(home, "linux", managed))
	if !allows(w) || w.ChannelsEnabled == nil || !*w.ChannelsEnabled {
		t.Fatalf("merged = %+v, want mash allowed and channels enabled", w)
	}
}

func TestMergeKeepsHigherAllowlistWhole(t *testing.T) {
	home, managed := layout(t, map[string]string{
		"home/.claude/remote-settings.json": `{"managedSourcesBehavior": "merge", "allowedChannelPlugins": [{"marketplace":"other","plugin":"x"}]}`,
		"managed/managed-settings.json":     withMash,
	})
	if w, _ := Effective(Sources(home, "linux", managed)); allows(w) {
		t.Fatal("a restriction allowlist comes whole from the highest source; the local mash entry must not be added")
	}
}

func TestLocalFileWinsWithoutServerSettings(t *testing.T) {
	home, managed := layout(t, map[string]string{"managed/managed-settings.json": withMash})
	w, shadowed := Effective(Sources(home, "linux", managed))
	if w == nil || w.Name != FileName || !allows(w) {
		t.Fatalf("winner = %+v, want the local file allowing mash", w)
	}
	if len(shadowed) != 1 || shadowed[0].Present {
		t.Fatalf("shadowed = %+v, want only the absent server source", shadowed)
	}
}

func TestDropInsMergeAlphabetically(t *testing.T) {
	home, managed := layout(t, map[string]string{
		"managed/managed-settings.json":             `{"channelsEnabled": true}`,
		"managed/managed-settings.d/10-a.json":      `{"channelsEnabled": false, "allowedChannelPlugins": [{"marketplace":"other","plugin":"x"}]}`,
		"managed/managed-settings.d/20-b.json":      `{"channelsEnabled": true, "allowedChannelPlugins": [{"marketplace":"mash","plugin":"mash"}]}`,
		"managed/managed-settings.d/.hidden.json":   `{"channelsEnabled": false}`,
		"managed/managed-settings.d/30-ignored.txt": `{"channelsEnabled": false}`,
	})
	w, _ := Effective(Sources(home, "linux", managed))
	if !allows(w) {
		t.Fatalf("winner = %+v: lists combine across drop-ins, so mash must be allowed", w)
	}
	if w.ChannelsEnabled == nil || !*w.ChannelsEnabled {
		t.Fatal("the later file's single value (channelsEnabled true) must replace the earlier one")
	}
	// Swap the order of the single value: the later file's false now wins.
	home, managed = layout(t, map[string]string{
		"managed/managed-settings.d/10-a.json": withMash,
		"managed/managed-settings.d/20-b.json": `{"channelsEnabled": false}`,
	})
	w, _ = Effective(Sources(home, "linux", managed))
	if w == nil || w.ChannelsEnabled == nil || *w.ChannelsEnabled || !allows(w) {
		t.Fatalf("winner = %+v, want channelsEnabled false from 20-b and mash still listed", w)
	}
}

func TestUnreadableOrBadJSONIsUnknown(t *testing.T) {
	home, managed := layout(t, map[string]string{
		"managed/managed-settings.json":     withMash,
		"home/.claude/remote-settings.json": `{`,
	})
	srcs := Sources(home, "linux", managed)
	if srcs[0].Err == nil || !srcs[0].Present {
		t.Fatalf("bad JSON: %+v, want Err", srcs[0])
	}
	if os.Geteuid() == 0 {
		t.Skip("root reads mode 000 files")
	}
	p := filepath.Join(managed, "managed-settings.json")
	if err := os.Chmod(p, 0); err != nil {
		t.Fatal(err)
	}
	srcs = Sources(home, "linux", managed)
	if srcs[1].Err == nil || !srcs[1].Present {
		t.Fatalf("mode 000: %+v, want Err", srcs[1])
	}
	Effective(srcs) // must not panic
}

func TestStrippedEnv(t *testing.T) {
	none := func(string) string { return "" }
	home, managed := layout(t, map[string]string{
		"managed/managed-settings.json": `{"env": {"CLAUDE_CODE_MCP_ALLOWLIST_ENV": "1"}}`,
	})
	if on, where := StrippedEnv(home, Sources(home, "linux", managed), none); !on || where != FileName {
		t.Fatalf("managed env: %v %q", on, where)
	}
	home, managed = layout(t, map[string]string{"home/.claude/settings.json": `{"env": {"CLAUDE_CODE_MCP_ALLOWLIST_ENV": "1"}}`})
	if on, _ := StrippedEnv(home, Sources(home, "linux", managed), none); !on {
		t.Fatal("user settings env not seen")
	}
	if on, _ := StrippedEnv(home, nil, func(string) string { return "1" }); !on {
		t.Fatal("process env not seen")
	}
	home, managed = layout(t, nil)
	if on, _ := StrippedEnv(home, Sources(home, "linux", managed), none); on {
		t.Fatal("nothing set must not be stripped")
	}
}
