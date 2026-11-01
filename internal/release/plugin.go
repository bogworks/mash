package release

import (
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// PluginManifest is the path of the Claude Code plugin manifest, relative to the repository root.
const PluginManifest = "plugin/.claude-plugin/plugin.json"

// CheckPluginVersion refuses a plugin manifest whose version is not the release version (N.N.N, no leading v).
// Claude Code updates an installed plugin only when its manifest version changes.
func CheckPluginVersion(manifest []byte, version string) error {
	var m struct {
		Version string `json:"version"`
	}
	if err := json.Unmarshal(manifest, &m); err != nil {
		return fmt.Errorf("%s is unreadable: %w", PluginManifest, err)
	}
	if m.Version == "" {
		return fmt.Errorf("%s has no version, the release is %s", PluginManifest, version)
	}
	if m.Version != version {
		return fmt.Errorf("%s says %s, the release is %s", PluginManifest, m.Version, version)
	}
	return nil
}

// PluginHooks and PluginMCP are the paths of the plugin's hook and MCP server definitions, relative to the repository root.
const (
	PluginHooks = "plugin/hooks/hooks.json"
	PluginMCP   = "plugin/.mcp.json"
)

// HookCommand is one command of the plugin's hooks.json, with the event that runs it and the "matcher" of its hook
// group (empty when the group has none).
type HookCommand struct{ Event, Matcher, Command string }

// HookCommands lists the commands hooks.json runs, events in name order, commands in file order, each with its group's matcher.
func HookCommands(hooks []byte) ([]HookCommand, error) {
	var h struct {
		Hooks map[string][]struct {
			Matcher string `json:"matcher"`
			Hooks   []struct {
				Type    string `json:"type"`
				Command string `json:"command"`
			} `json:"hooks"`
		} `json:"hooks"`
	}
	if err := json.Unmarshal(hooks, &h); err != nil {
		return nil, fmt.Errorf("%s is unreadable: %w", PluginHooks, err)
	}
	events := make([]string, 0, len(h.Hooks))
	for e := range h.Hooks {
		events = append(events, e)
	}
	sort.Strings(events)
	var out []HookCommand
	for _, e := range events {
		for _, g := range h.Hooks[e] {
			for _, c := range g.Hooks {
				if c.Type == "command" && c.Command != "" {
					out = append(out, HookCommand{e, g.Matcher, c.Command})
				}
			}
		}
	}
	return out, nil
}

// LauncherCommand is the MCP command of a plugin that runs mash through its bundled launcher script.
const LauncherCommand = "${CLAUDE_PLUGIN_ROOT}/scripts/mash"

// launcherHookRE matches the launcher invoked with the hook subcommand, with or without quotes around its path.
var launcherHookRE = regexp.MustCompile(`scripts/mash"? hook`)

// IsMashHook reports whether a hook command runs `mash hook`: directly, or through the plugin's launcher script.
func IsMashHook(cmd string) bool {
	return strings.Contains(cmd, "mash hook") || launcherHookRE.MatchString(cmd)
}

// IsLauncherCommand reports whether an MCP server command is the plugin's launcher script.
func IsLauncherCommand(cmd string) bool { return cmd == LauncherCommand }

// AcceptedMCPCommand reports whether an MCP server command is one the gate can check: bare `mash` (the binary on PATH)
// or the plugin's launcher script.
func AcceptedMCPCommand(cmd string) bool { return cmd == "mash" || IsLauncherCommand(cmd) }

// CheckHookMatchers refuses hooks whose matchers stop the plugin's hooks from running on what they exist for: PostToolUse
// must match every tool ("*" or no matcher), UserPromptSubmit takes no matcher, and each must run a `mash hook` command.
func CheckHookMatchers(hooks []HookCommand) error {
	for _, e := range []string{"PostToolUse", "UserPromptSubmit"} {
		found := false
		for _, h := range hooks {
			if h.Event != e {
				continue
			}
			if IsMashHook(h.Command) {
				found = true
			}
			if (e == "PostToolUse" && h.Matcher != "" && h.Matcher != "*") || (e == "UserPromptSubmit" && h.Matcher != "") {
				return fmt.Errorf("%s: the %s hook has matcher %q, so it does not run on every %s", PluginHooks, e, h.Matcher, map[string]string{"PostToolUse": "tool call", "UserPromptSubmit": "prompt"}[e])
			}
		}
		if !found {
			return fmt.Errorf("%s: %s has no `mash hook` command", PluginHooks, e)
		}
	}
	return nil
}

// MCPCommand is the command and arguments of the plugin's "mash" MCP server.
func MCPCommand(mcp []byte) (command string, args []string, err error) {
	command, args, _, err = MCPServer(mcp)
	return command, args, err
}

// MCPServer is MCPCommand and the server's env block too: the plugin launches serve with it (MASH_SPOOL_FROM_START),
// so a check of serve's behaviour must run it the same way.
func MCPServer(mcp []byte) (command string, args []string, env map[string]string, err error) {
	var m struct {
		Servers map[string]struct {
			Command string            `json:"command"`
			Args    []string          `json:"args"`
			Env     map[string]string `json:"env"`
		} `json:"mcpServers"`
	}
	if err := json.Unmarshal(mcp, &m); err != nil {
		return "", nil, nil, fmt.Errorf("%s is unreadable: %w", PluginMCP, err)
	}
	s, ok := m.Servers["mash"]
	if !ok || s.Command == "" {
		return "", nil, nil, fmt.Errorf("%s has no mash server command", PluginMCP)
	}
	return s.Command, s.Args, s.Env, nil
}

var tagRE = regexp.MustCompile(`^v([0-9]+)\.([0-9]+)\.([0-9]+)$`)

// PreviousTag is the highest vN.N.N tag strictly below version (N.N.N), ignoring every other tag; false when none.
func PreviousTag(tags []string, version string) (string, bool) {
	key := tagKey
	cur, ok := key("v" + version)
	if !ok {
		return "", false
	}
	best, found := "", false
	var bk [3]int
	for _, t := range tags {
		k, ok := key(t)
		if !ok || !less(k, cur) || (found && !less(bk, k)) {
			continue
		}
		best, bk, found = t, k, true
	}
	return best, found
}

// HookSince is the first mash version that has `mash hook`; a release older than this answers it "unknown command", which
// the plugin's tolerant hook wrapper exists to absorb. It is cmd/mash's pluginMinMash (plugin_hook_test.go).
const HookSince = "0.2.0"

// tagKey parses vN.N.N.
func tagKey(s string) ([3]int, bool) {
	m := tagRE.FindStringSubmatch(s)
	if m == nil {
		return [3]int{}, false
	}
	var k [3]int
	for i := range k {
		k[i], _ = strconv.Atoi(m[i+1])
	}
	return k, true
}

// TagBelow reports whether the vN.N.N tag is numerically below version (N.N.N); false when either does not parse.
func TagBelow(tag, version string) bool {
	a, ok := tagKey(tag)
	b, ok2 := tagKey("v" + version)
	return ok && ok2 && less(a, b)
}

func less(a, b [3]int) bool {
	for i := range a {
		if a[i] != b[i] {
			return a[i] < b[i]
		}
	}
	return false
}
