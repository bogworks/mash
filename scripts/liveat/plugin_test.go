package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func repoPlugin(t *testing.T) string {
	t.Helper()
	p, err := filepath.Abs("../../plugin")
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func TestPluginFilesDeclareTheJourney(t *testing.T) {
	pl, err := loadPluginLaunch(repoPlugin(t))
	if err != nil {
		t.Fatal(err)
	}
	if bad := pl.checkDeclared(); len(bad) > 0 {
		t.Fatalf("the repo's plugin does not declare the journey: %v", bad)
	}
	if !strings.HasSuffix(pl.Launcher, "/plugin/scripts/mash") || pl.Env["MASH_PLUGIN_ROOT"] != pl.Root {
		t.Errorf("launcher %q env %v", pl.Launcher, pl.Env)
	}
}

func TestCheckDeclaredRefusesMissingPieces(t *testing.T) {
	pl := pluginLaunch{Root: "/p", Launcher: "/p/scripts/mash", Args: []string{"serve"},
		Env:       map[string]string{"MASH_SPOOL_FROM_START": "1", "MASH_PLUGIN_ROOT": "/p"},
		HookCmds:  map[string]string{"UserPromptSubmit": `sh "${CLAUDE_PLUGIN_ROOT}/scripts/mash" hook`, "PostToolUse": `sh "${CLAUDE_PLUGIN_ROOT}/scripts/mash" hook`},
		HookMatch: map[string]string{"PostToolUse": "*"}}
	if bad := pl.checkDeclared(); len(bad) != 0 {
		t.Fatalf("good declaration refused: %v", bad)
	}
	for name, mut := range map[string]func(*pluginLaunch){
		"no hook":     func(p *pluginLaunch) { delete(p.HookCmds, "PostToolUse") },
		"wrong hook":  func(p *pluginLaunch) { p.HookCmds["UserPromptSubmit"] = "true" },
		"no spool":    func(p *pluginLaunch) { p.Env = map[string]string{"MASH_PLUGIN_ROOT": "/p"} },
		"bare binary": func(p *pluginLaunch) { p.Launcher = "/usr/bin/mash" },
		"matcher":     func(p *pluginLaunch) { p.HookMatch["PostToolUse"] = "Bash" },
	} {
		c := pl
		c.Env, c.HookCmds, c.HookMatch = map[string]string{}, map[string]string{}, map[string]string{}
		for k, v := range pl.Env {
			c.Env[k] = v
		}
		for k, v := range pl.HookCmds {
			c.HookCmds[k] = v
		}
		for k, v := range pl.HookMatch {
			c.HookMatch[k] = v
		}
		mut(&c)
		if len(c.checkDeclared()) == 0 {
			t.Errorf("%s: accepted", name)
		}
	}
}

func TestToolReply(t *testing.T) {
	out := `{"jsonrpc":"2.0","id":1,"result":{}}` + "\n" +
		`{"jsonrpc":"2.0","method":"notifications/claude/channel","params":{"content":"x"}}` + "\n" +
		`{"jsonrpc":"2.0","id":101,"result":{"content":[{"type":"text","text":"sent #4 to a.b.c"}]}}` + "\n" +
		`{"jsonrpc":"2.0","id":102,"result":{"content":[{"type":"text","text":"nope"}],"isError":true}}` + "\n" +
		`{"jsonrpc":"2.0","id":103,"error":{"code":-32601,"message":"x"}}` + "\n"
	if txt, isErr, ok := toolReply(out, 101); !ok || isErr || txt != "sent #4 to a.b.c" {
		t.Errorf("101: %q %v %v", txt, isErr, ok)
	}
	if _, isErr, ok := toolReply(out, 102); !ok || !isErr {
		t.Errorf("102: isErr %v ok %v", isErr, ok)
	}
	if _, isErr, ok := toolReply(out, 103); !ok || !isErr {
		t.Errorf("103 (a JSON-RPC error) must be an error: isErr %v ok %v", isErr, ok)
	}
	if _, _, ok := toolReply(out, 104); ok {
		t.Error("104 found")
	}
}

func TestHookContext(t *testing.T) {
	good := `{"hookSpecificOutput":{"hookEventName":"PostToolUse","additionalContext":"mash mail for x"}}` + "\n"
	if c, err := hookContext(good, "PostToolUse"); err != nil || c != "mash mail for x" {
		t.Errorf("%q %v", c, err)
	}
	if c, err := hookContext("", "PostToolUse"); err != nil || c != "" {
		t.Errorf("empty: %q %v", c, err)
	}
	if _, err := hookContext(good, "UserPromptSubmit"); err == nil {
		t.Error("a wrong event name was accepted")
	}
	if _, err := hookContext("not json", "PostToolUse"); err == nil {
		t.Error("garbage was accepted")
	}
}

func TestHookInputIsTheShapeClaudeCodeSends(t *testing.T) {
	for _, ev := range []string{"UserPromptSubmit", "PostToolUse"} {
		in := hookInput(ev, "sess", "/w")
		for _, want := range []string{`"hook_event_name":"` + ev + `"`, `"session_id":"sess"`} {
			if !strings.Contains(in, want) {
				t.Errorf("%s input lacks %s: %s", ev, want, in)
			}
		}
	}
	if !strings.Contains(hookInput("PostToolUse", "s", "/w"), `"tool_name"`) {
		t.Error("PostToolUse input has no tool_name")
	}
}

// The real launcher, with MASH_BIN set, execs that binary and neither downloads nor makes the data directory.
func TestLauncherExecsMASHBINWithoutDownload(t *testing.T) {
	dir := t.TempDir()
	fake := filepath.Join(dir, "fakemash")
	if err := os.WriteFile(fake, []byte("#!/bin/sh\necho \"fake-mash $*\"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	data := filepath.Join(dir, "data")
	cmd := exec.Command("sh", filepath.Join(repoPlugin(t), "scripts", "mash"), "serve")
	cmd.Env = []string{"PATH=" + toolPath(), "HOME=" + dir, "MASH_BIN=" + fake, "CLAUDE_PLUGIN_DATA=" + data, "MASH_RELEASE_BASE=file://" + filepath.Join(dir, "none")}
	out, err := cmd.CombinedOutput()
	if err != nil || strings.TrimSpace(string(out)) != "fake-mash serve" {
		t.Fatalf("%v: %q", err, out)
	}
	if _, err := os.Stat(data); err == nil {
		t.Error("the data directory was made")
	}
}
