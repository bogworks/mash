package main

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/bogworks/mash/internal/release"
)

// pluginMinMash is the oldest mash that has every command the plugin runs.
//
// Several sessions of one user share one Claude Code config dir, so the installed plugin at version N runs against
// mash binaries N and N-1. Changing the plugin's commands breaks sessions whose binary is N-1: a new command may ship
// only once the previous release has it. The golden below and pluginMinMash are updated deliberately, together.
const pluginMinMash = "0.2.0"

// pluginCommandsGolden is every command line the plugin runs: the hook commands (SessionStart prefetch, PostToolUse, UserPromptSubmit) with their matchers in brackets and the MCP server.
var pluginCommandsGolden = []string{
	`PostToolUse [*]: ` + hookCmd,
	`SessionStart []: ` + prefetchCmd,
	`UserPromptSubmit []: ` + hookCmd,
	`mcp: ${CLAUDE_PLUGIN_ROOT}/scripts/mash serve`,
}

const prefetchCmd = `sh "${CLAUDE_PLUGIN_ROOT}/scripts/mash" __prefetch`

const hookCmd = `d="${XDG_STATE_HOME:-$HOME/.local/state}/mash"; mkdir -p "$d" 2>/dev/null; l="$d/hook.log"; (: >>"$l") 2>/dev/null || l=/dev/null; sh "${CLAUDE_PLUGIN_ROOT}/scripts/mash" hook 2>>"$l"; exit 0`

func pluginHooks(t *testing.T) []release.HookCommand {
	t.Helper()
	b, err := os.ReadFile("../../" + release.PluginHooks)
	if err != nil {
		t.Fatal(err)
	}
	hs, err := release.HookCommands(b)
	if err != nil {
		t.Fatal(err)
	}
	return hs
}

func TestPluginCommandsAreFrozen(t *testing.T) {
	var got []string
	for _, h := range pluginHooks(t) {
		got = append(got, h.Event+" ["+h.Matcher+"]: "+h.Command)
	}
	b, err := os.ReadFile("../../" + release.PluginMCP)
	if err != nil {
		t.Fatal(err)
	}
	c, args, err := release.MCPCommand(b)
	if err != nil {
		t.Fatal(err)
	}
	got = append(got, "mcp: "+strings.Join(append([]string{c}, args...), " "))
	if !reflect.DeepEqual(got, pluginCommandsGolden) {
		t.Fatalf("the plugin's commands changed (sessions on mash %s or older break; see pluginMinMash):\n got %q\nwant %q", pluginMinMash, got, pluginCommandsGolden)
	}
}

// runShellHook runs a hook command through sh with a PATH of only dir (plus the system's), returning stdout, stderr and the exit code.
func runShellHook(t *testing.T, command, pathDir string, env ...string) (stdout, stderr string, code int) {
	t.Helper()
	cmd := exec.Command("sh", "-c", command)
	root, err := filepath.Abs("../../plugin")
	if err != nil {
		t.Fatal(err)
	}
	cmd.Env = append([]string{"PATH=" + pathDir + ":/usr/bin:/bin", "HOME=" + t.TempDir(), "CLAUDE_PLUGIN_ROOT=" + root}, env...)
	cmd.Stdin = strings.NewReader(`{"hook_event_name":"PostToolUse"}`)
	var o, e bytes.Buffer
	cmd.Stdout, cmd.Stderr = &o, &e
	err = cmd.Run()
	if ee, ok := err.(*exec.ExitError); ok {
		code = ee.ExitCode()
	} else if err != nil {
		t.Fatal(err)
	}
	return o.String(), e.String(), code
}

func fakeMash(t *testing.T, script string) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "mash"), []byte("#!/bin/sh\n"+script+"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestPluginHook_neverBlocksAndLogsStderr(t *testing.T) {
	for _, h := range pluginHooks(t) {
		if h.Event == "SessionStart" {
			continue // the prefetch hook is covered by TestPluginPrefetchHook_neverFails
		}
		t.Run(h.Event+"/an old binary that exits 2 with usage", func(t *testing.T) {
			state := t.TempDir()
			dir := fakeMash(t, "echo 'usage: mash <command>' >&2; exit 2")
			out, errOut, code := runShellHook(t, h.Command, dir, "XDG_STATE_HOME="+state)
			if code != 0 || out != "" || errOut != "" {
				t.Fatalf("exit %d stdout %q stderr %q, want 0 and nothing", code, out, errOut)
			}
			log, err := os.ReadFile(filepath.Join(state, "mash", "hook.log"))
			if err != nil || !strings.Contains(string(log), "usage: mash <command>") {
				t.Fatalf("hook.log %q, %v", log, err)
			}
		})
		t.Run(h.Event+"/context passes through", func(t *testing.T) {
			dir := fakeMash(t, "cat >/dev/null; printf CTX")
			out, _, code := runShellHook(t, h.Command, dir, "XDG_STATE_HOME="+t.TempDir())
			if code != 0 || out != "CTX" {
				t.Fatalf("exit %d stdout %q", code, out)
			}
		})
		t.Run(h.Event+"/stdin reaches mash", func(t *testing.T) {
			dir := fakeMash(t, "cat")
			out, _, _ := runShellHook(t, h.Command, dir, "XDG_STATE_HOME="+t.TempDir())
			if out != `{"hook_event_name":"PostToolUse"}` {
				t.Fatalf("stdout %q", out)
			}
		})
		t.Run(h.Event+"/an unwritable state dir", func(t *testing.T) {
			f := filepath.Join(t.TempDir(), "file")
			if err := os.WriteFile(f, nil, 0o600); err != nil {
				t.Fatal(err)
			}
			dir := fakeMash(t, "printf CTX; echo oops >&2; exit 2")
			for _, state := range []string{f, filepath.Join(f, "sub"), "/proc/nope"} { // a file in the way, below a file, a read-only tree
				out, errOut, code := runShellHook(t, h.Command, dir, "XDG_STATE_HOME="+state)
				if code != 0 || out != "CTX" || errOut != "" {
					t.Errorf("%s: exit %d stdout %q stderr %q, want 0, CTX, nothing", state, code, out, errOut)
				}
			}
		})
		t.Run(h.Event+"/no mash on PATH", func(t *testing.T) {
			out, errOut, code := runShellHook(t, h.Command, t.TempDir(), "XDG_STATE_HOME="+t.TempDir())
			if code != 0 || out != "" || errOut != "" {
				t.Fatalf("exit %d stdout %q stderr %q", code, out, errOut)
			}
		})
		t.Run(h.Event+"/the real binary with an empty spool", func(t *testing.T) {
			bin := mashBinary(t)
			out, _, code := runShellHook(t, h.Command, filepath.Dir(bin), "XDG_STATE_HOME="+t.TempDir())
			if code != 0 || out != "" {
				t.Fatalf("exit %d stdout %q", code, out)
			}
		})
	}
}

// The prefetch hook never writes to stdout, never fails, and does not need an exec bit or a pinned release.
func TestPluginPrefetchHook_neverFails(t *testing.T) {
	out, errOut, code := runShellHook(t, prefetchCmd, t.TempDir(), "XDG_STATE_HOME="+t.TempDir())
	if code != 0 || out != "" {
		t.Fatalf("exit %d stdout %q stderr %q", code, out, errOut)
	}
}
