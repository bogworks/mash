package release

import (
	"encoding/json"
	"os"
	"regexp"
	"strings"
	"testing"
)

func TestCheckPluginVersion(t *testing.T) {
	cases := []struct {
		name, manifest, want string // want: "" passes, else a substring of the error
	}{
		{"match", `{"name":"mash","version":"0.2.2"}`, ""},
		{"pin version is not the release", `{"version":"0.2.2-pin.1"}`, "says 0.2.2-pin.1, the release is 0.2.2"},
		{"mismatch", `{"version":"0.1.0"}`, "plugin/.claude-plugin/plugin.json says 0.1.0, the release is 0.2.2"},
		{"missing version", `{"name":"mash"}`, "no version"},
		{"empty version", `{"version":""}`, "no version"},
		{"non-string version", `{"version":2}`, "plugin/.claude-plugin/plugin.json"},
		{"bad json", `{not json`, "plugin/.claude-plugin/plugin.json"},
		{"empty", ``, "plugin/.claude-plugin/plugin.json"},
	}
	for _, c := range cases {
		err := CheckPluginVersion([]byte(c.manifest), "0.2.2")
		switch {
		case c.want == "" && err != nil:
			t.Errorf("%s: %v", c.name, err)
		case c.want != "" && (err == nil || !strings.Contains(err.Error(), c.want)):
			t.Errorf("%s: got %v, want %q", c.name, err, c.want)
		}
	}
}

// Claude Code updates an installed plugin only when its manifest version changes, so it must be a real semver.
func TestRepo_pluginManifestHasASemverVersion(t *testing.T) {
	b, err := os.ReadFile("../../plugin/.claude-plugin/plugin.json")
	if err != nil {
		t.Fatal(err)
	}
	var m struct {
		Version string `json:"version"`
	}
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatal(err)
	}
	if !regexp.MustCompile(`^[0-9]+\.[0-9]+\.[0-9]+(-pin\.[0-9]+)?$`).MatchString(m.Version) {
		t.Fatalf("plugin.json version %q is not N.N.N or N.N.N-pin.N", m.Version)
	}
}

func TestHookCommands(t *testing.T) {
	got, err := HookCommands([]byte(`{"hooks":{"UserPromptSubmit":[{"hooks":[{"type":"command","command":"b"}]}],
		"PostToolUse":[{"matcher":"*","hooks":[{"type":"command","command":"a1"},{"type":"command","command":"a2"}]}]}}`))
	want := []HookCommand{{"PostToolUse", "*", "a1"}, {"PostToolUse", "*", "a2"}, {"UserPromptSubmit", "", "b"}}
	if err != nil || len(got) != len(want) {
		t.Fatalf("%v %v", got, err)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("%d: %v, want %v", i, got[i], want[i])
		}
	}
	if _, err := HookCommands([]byte(`{nope`)); err == nil {
		t.Error("bad json passed")
	}
}

func TestCheckHookMatchers(t *testing.T) {
	const cmd = "mash hook; exit 0"
	good := []HookCommand{{"PostToolUse", "*", cmd}, {"UserPromptSubmit", "", cmd}}
	if err := CheckHookMatchers(good); err != nil {
		t.Errorf("good: %v", err)
	}
	if err := CheckHookMatchers([]HookCommand{{"PostToolUse", "", cmd}, {"UserPromptSubmit", "", cmd}}); err != nil {
		t.Errorf("an absent PostToolUse matcher matches every tool: %v", err)
	}
	for _, c := range []struct {
		name string
		hs   []HookCommand
		want []string
	}{
		{"PostToolUse Bash", []HookCommand{{"PostToolUse", "Bash", cmd}, {"UserPromptSubmit", "", cmd}}, []string{"PostToolUse", `"Bash"`}},
		{"UserPromptSubmit matcher", []HookCommand{{"PostToolUse", "*", cmd}, {"UserPromptSubmit", "x", cmd}}, []string{"UserPromptSubmit", `"x"`}},
		{"no UserPromptSubmit", good[:1], []string{"UserPromptSubmit", "no `mash hook`"}},
		{"no PostToolUse", good[1:], []string{"PostToolUse", "no `mash hook`"}},
		{"unrelated launcher subcommand", []HookCommand{{"PostToolUse", "*", `sh "${CLAUDE_PLUGIN_ROOT}/scripts/mash" __prefetch`}, {"UserPromptSubmit", "", cmd}}, []string{"PostToolUse", "no `mash hook`"}},
		{"not mash hook", []HookCommand{{"PostToolUse", "*", "true"}, {"UserPromptSubmit", "", cmd}}, []string{"PostToolUse", "no `mash hook`"}},
	} {
		err := CheckHookMatchers(c.hs)
		if err == nil {
			t.Errorf("%s: passed", c.name)
			continue
		}
		for _, w := range c.want {
			if !strings.Contains(err.Error(), w) {
				t.Errorf("%s: %v lacks %q", c.name, err, w)
			}
		}
	}
}

func TestMCPCommand(t *testing.T) {
	c, a, err := MCPCommand([]byte(`{"mcpServers":{"mash":{"command":"mash","args":["serve"]}}}`))
	if err != nil || c != "mash" || len(a) != 1 || a[0] != "serve" {
		t.Fatalf("%q %v %v", c, a, err)
	}
	for _, bad := range []string{`{nope`, `{}`, `{"mcpServers":{"other":{"command":"x"}}}`} {
		if _, _, err := MCPCommand([]byte(bad)); err == nil {
			t.Errorf("%s passed", bad)
		}
	}
}

func TestMCPServer_carriesTheEnvBlock(t *testing.T) {
	_, _, env, err := MCPServer([]byte(`{"mcpServers":{"mash":{"command":"mash","args":["serve"],"env":{"MASH_SPOOL_FROM_START":"1"}}}}`))
	if err != nil || len(env) != 1 || env["MASH_SPOOL_FROM_START"] != "1" {
		t.Fatalf("%v %v", env, err)
	}
	if _, _, env, err := MCPServer([]byte(`{"mcpServers":{"mash":{"command":"mash"}}}`)); err != nil || len(env) != 0 {
		t.Fatalf("%v %v", env, err)
	}
}

func TestPreviousTag(t *testing.T) {
	tags := []string{"v0.1.2", "v0.2.0", "v0.10.0", "v0.2.1", "v0.2.2", "v0.3.0-rc1", "latest", "v1.0", "0.2.9"}
	cases := []struct {
		version, want string // want "" is none
	}{
		{"0.2.2", "v0.2.1"}, {"0.2.0", "v0.1.2"}, {"0.1.2", ""}, {"0.10.0", "v0.2.2"}, {"0.10.1", "v0.10.0"}, {"0.3.0", "v0.2.2"}, {"bad", ""},
	}
	for _, c := range cases {
		got, ok := PreviousTag(tags, c.version)
		if got != c.want || ok != (c.want != "") {
			t.Errorf("%s: %q %v, want %q", c.version, got, ok, c.want)
		}
	}
	if got, ok := PreviousTag(nil, "0.2.2"); ok || got != "" {
		t.Errorf("no tags: %q %v", got, ok)
	}
}

func TestCheckHookMatchers_launcherForm(t *testing.T) {
	for _, cmd := range []string{
		`sh "${CLAUDE_PLUGIN_ROOT}/scripts/mash" hook 2>>"$l"; exit 0`,
		`sh ${CLAUDE_PLUGIN_ROOT}/scripts/mash hook`,
		`${CLAUDE_PLUGIN_ROOT}/scripts/mash hook`,
		`mash hook 2>/dev/null; exit 0`,
	} {
		hs := []HookCommand{{"PostToolUse", "*", cmd}, {"UserPromptSubmit", "", cmd}, {"SessionStart", "", `sh "${CLAUDE_PLUGIN_ROOT}/scripts/mash" __prefetch`}}
		if err := CheckHookMatchers(hs); err != nil {
			t.Errorf("%q: %v", cmd, err)
		}
		if !IsMashHook(cmd) {
			t.Errorf("IsMashHook(%q) = false", cmd)
		}
	}
	if IsMashHook(`sh "${CLAUDE_PLUGIN_ROOT}/scripts/mash" __prefetch`) || IsMashHook("node hook") {
		t.Error("an unrelated command is a mash hook")
	}
}

func TestAcceptedMCPCommand(t *testing.T) {
	for cmd, want := range map[string]bool{"mash": true, "${CLAUDE_PLUGIN_ROOT}/scripts/mash": true, "node": false, "/usr/bin/mash": false, "": false} {
		if got := AcceptedMCPCommand(cmd); got != want {
			t.Errorf("AcceptedMCPCommand(%q) = %v, want %v", cmd, got, want)
		}
	}
	if !IsLauncherCommand(LauncherCommand) || IsLauncherCommand("mash") {
		t.Error("IsLauncherCommand")
	}
}
