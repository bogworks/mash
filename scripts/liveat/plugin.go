package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"time"
)

// The plugin journey of AT-001. Claude Code starts the plugin's MCP server from plugin/.mcp.json (the launcher,
// `scripts/mash serve`, with that file's env) and runs the commands of plugin/hooks/hooks.json on UserPromptSubmit and
// PostToolUse with the hook JSON on stdin. The harness has no Claude Code session, so it plays that one role and
// nothing else: it starts the same commands the same way and speaks the MCP stdio protocol and the hook protocol itself.
// Nothing here is faked: the launcher, the serve, the hook and the mail are the real ones.

// pluginLaunch is what the plugin's own files declare, read from them and not written out here.
type pluginLaunch struct {
	Root      string            // the plugin directory
	Launcher  string            // the MCP server command, with ${CLAUDE_PLUGIN_ROOT} expanded
	Args      []string          // its arguments
	Env       map[string]string // its env, expanded
	HookCmds  map[string]string // event -> the hook command string, unexpanded (the shell expands it)
	HookMatch map[string]string // event -> matcher
}

// loadPluginLaunch reads <root>/.mcp.json and <root>/hooks/hooks.json.
func loadPluginLaunch(root string) (pluginLaunch, error) {
	pl := pluginLaunch{Root: root, Env: map[string]string{}, HookCmds: map[string]string{}, HookMatch: map[string]string{}}
	b, err := os.ReadFile(filepath.Join(root, ".mcp.json"))
	if err != nil {
		return pl, err
	}
	var mcp struct {
		Servers map[string]struct {
			Command string            `json:"command"`
			Args    []string          `json:"args"`
			Env     map[string]string `json:"env"`
		} `json:"mcpServers"`
	}
	if err := json.Unmarshal(b, &mcp); err != nil {
		return pl, fmt.Errorf(".mcp.json: %w", err)
	}
	srv, ok := mcp.Servers["mash"]
	if !ok {
		return pl, fmt.Errorf(".mcp.json declares no mash server")
	}
	expand := func(s string) string { return strings.ReplaceAll(s, "${CLAUDE_PLUGIN_ROOT}", root) }
	pl.Launcher, pl.Args = expand(srv.Command), srv.Args
	for k, v := range srv.Env {
		pl.Env[k] = expand(v)
	}
	hb, err := os.ReadFile(filepath.Join(root, "hooks", "hooks.json"))
	if err != nil {
		return pl, err
	}
	hs, err := parseHooks(hb)
	if err != nil {
		return pl, err
	}
	for ev, h := range hs {
		pl.HookCmds[ev], pl.HookMatch[ev] = h[0], h[1]
	}
	return pl, nil
}

// parseHooks reads hooks.json into event -> {command, matcher} (the first command of the event).
func parseHooks(b []byte) (map[string][2]string, error) {
	var doc struct {
		Hooks map[string][]struct {
			Matcher string `json:"matcher"`
			Hooks   []struct {
				Type    string `json:"type"`
				Command string `json:"command"`
			} `json:"hooks"`
		} `json:"hooks"`
	}
	if err := json.Unmarshal(b, &doc); err != nil {
		return nil, fmt.Errorf("hooks.json: %w", err)
	}
	out := map[string][2]string{}
	for ev, groups := range doc.Hooks {
		for _, g := range groups {
			for _, h := range g.Hooks {
				if h.Type == "command" && h.Command != "" {
					if _, dup := out[ev]; !dup {
						out[ev] = [2]string{h.Command, g.Matcher}
					}
				}
			}
		}
	}
	return out, nil
}

// checkDeclared reports what the plugin's files fail to declare that the journey needs.
func (pl pluginLaunch) checkDeclared() []string {
	var bad []string
	if !strings.HasSuffix(pl.Launcher, "/scripts/mash") || len(pl.Args) != 1 || pl.Args[0] != "serve" {
		bad = append(bad, fmt.Sprintf(".mcp.json runs %q %v, want <plugin>/scripts/mash serve", pl.Launcher, pl.Args))
	}
	if pl.Env["MASH_SPOOL_FROM_START"] != "1" || pl.Env["MASH_PLUGIN_ROOT"] != pl.Root {
		bad = append(bad, ".mcp.json env lacks MASH_SPOOL_FROM_START=1 or MASH_PLUGIN_ROOT=<plugin>")
	}
	for _, ev := range []string{"UserPromptSubmit", "PostToolUse"} {
		c := pl.HookCmds[ev]
		if c == "" {
			bad = append(bad, "hooks.json declares no command for "+ev)
		} else if !strings.Contains(c, `"${CLAUDE_PLUGIN_ROOT}/scripts/mash" hook`) {
			bad = append(bad, "the "+ev+" command does not run the launcher's hook")
		}
	}
	if pl.HookMatch["PostToolUse"] != "*" {
		bad = append(bad, "the PostToolUse hook's matcher is not *")
	}
	return bad
}

// pluginEnv is the environment Claude Code gives the plugin's server and hooks, for one session: the .mcp.json env,
// CLAUDE_PLUGIN_ROOT and CLAUDE_PLUGIN_DATA (a scratch directory that is not made, so a download would show), the
// session id, MASH_BIN so the launcher execs the harness's build, and a release base that holds nothing.
func (a *AT) pluginEnv(pl pluginLaunch, name string) (env []string, dataDir string) {
	dataDir = filepath.Join(a.Dir, "plugin-data-"+name)
	for k, v := range pl.Env {
		env = append(env, k+"="+v)
	}
	env = append(env,
		"CLAUDE_PLUGIN_ROOT="+pl.Root,
		"CLAUDE_PLUGIN_DATA="+dataDir,
		"CLAUDE_CODE_SESSION_ID=liveat-"+name,
		"MASH_BIN="+a.R.Mash,
		"MASH_RELEASE_BASE=file://"+filepath.Join(a.Dir, "no-release"),
	)
	return env, dataDir
}

// plugSession is one session started the plugin's way.
type plugSession struct {
	Name    string
	P       *Proc
	Env     []string // the session's whole extra environment (also the hooks' and the launcher CLI's)
	Data    string
	Session string
}

// StartPluginSession starts `sh <launcher> serve` as .mcp.json declares, with the plugin env and the session's
// MASH_ENDPOINT, and takes it through the MCP handshake.
func (a *AT) StartPluginSession(pl pluginLaunch, h Home, group, name string) (*plugSession, error) {
	pe, data := a.pluginEnv(pl, name)
	extra := a.SessionEnv(h, group, name, pe...)
	args := append([]string{"sh", pl.Launcher}, pl.Args...)
	p, err := a.R.StartProc("serve-"+name, args, a.Env(h, extra...))
	s := &plugSession{Name: name, P: p, Env: extra, Data: data, Session: "liveat-" + name}
	if err != nil {
		return s, err
	}
	return s, p.Handshake(20 * time.Second)
}

// Launcher runs `sh <launcher> args...` with the session's environment: the plugin's own entry point for a CLI call.
func (a *AT) Launcher(pl pluginLaunch, label string, h Home, extra []string, args ...string) Result {
	return a.R.Runner.RunIn(h.Dir, label, append([]string{"sh", pl.Launcher}, args...), a.Env(h, extra...), "")
}

var callSeq atomic.Int64

func init() { callSeq.Store(100) }

// Call sends tools/call and waits for its reply: the text of the first content item and whether the call is an error.
func (p *Proc) Call(tool string, args map[string]any, d time.Duration) (string, bool, error) {
	id := int(callSeq.Add(1))
	b, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": id, "method": "tools/call", "params": map[string]any{"name": tool, "arguments": args}})
	if err := p.Send(string(b)); err != nil {
		return "", false, err
	}
	if !p.WaitFor(d, func(o, _ string) bool { _, _, ok := toolReply(o, id); return ok }) {
		return "", false, fmt.Errorf("no reply to tools/call %s", tool)
	}
	text, isErr, _ := toolReply(p.Stdout(), id)
	return text, isErr, nil
}

// toolReply finds the reply with the id in an MCP stdout stream (one JSON document per line).
func toolReply(stdout string, id int) (text string, isErr, found bool) {
	for _, l := range strings.Split(stdout, "\n") {
		var m struct {
			ID     *float64 `json:"id"`
			Result *struct {
				Content []struct {
					Text string `json:"text"`
				} `json:"content"`
				IsError bool `json:"isError"`
			} `json:"result"`
			Error *json.RawMessage `json:"error"`
		}
		if json.Unmarshal([]byte(strings.TrimSpace(l)), &m) != nil || m.ID == nil || int(*m.ID) != id {
			continue
		}
		if m.Error != nil {
			return "", true, true
		}
		if m.Result == nil {
			continue
		}
		if len(m.Result.Content) > 0 {
			text = m.Result.Content[0].Text
		}
		return text, m.Result.IsError, true
	}
	return "", false, false
}

// hookInput is the JSON Claude Code puts on a hook's stdin for event.
func hookInput(event, session, cwd string) string {
	m := map[string]any{"session_id": session, "cwd": cwd, "hook_event_name": event}
	switch event {
	case "UserPromptSubmit":
		m["prompt"] = "check mash"
	case "PostToolUse":
		m["tool_name"] = "Bash"
		m["tool_input"] = map[string]any{"command": "true"}
		m["tool_response"] = map[string]any{"stdout": "", "stderr": ""}
	}
	b, _ := json.Marshal(m)
	return string(b)
}

// hookContext is the additionalContext of a hook's stdout for event: "" when the hook printed nothing, an error when
// it printed anything that is not that document.
func hookContext(stdout, event string) (string, error) {
	if strings.TrimSpace(stdout) == "" {
		return "", nil
	}
	var o struct {
		Specific struct {
			Event   string `json:"hookEventName"`
			Context string `json:"additionalContext"`
		} `json:"hookSpecificOutput"`
	}
	if err := json.Unmarshal([]byte(strings.TrimSpace(stdout)), &o); err != nil {
		return "", fmt.Errorf("hook stdout is not a hook output document")
	}
	if o.Specific.Event != event {
		return "", fmt.Errorf("hookEventName %q, want %q", o.Specific.Event, event)
	}
	return o.Specific.Context, nil
}

// runHook runs the hooks.json command for event under `sh -c`, as Claude Code does, with the hook JSON on stdin.
func (a *AT) runHook(pl pluginLaunch, label string, h Home, s *plugSession, event string) (string, Result, error) {
	r := a.R.Runner.RunIn(h.Dir, label, []string{"sh", "-c", pl.HookCmds[event]}, a.Env(h, s.Env...), hookInput(event, s.Session, h.Dir))
	ctx, err := hookContext(r.Stdout, event)
	return ctx, r, err
}

// pluginMail sends text from one session to the other with the sender's MCP `send` tool; the recipient's hook (event)
// must show it as "mash mail" context with the sender's verified trust label; then the recipient's MCP `inbox` and
// `read` tools must show it too.
func (a *AT) pluginMail(pl pluginLaunch, name string, h Home, from, to *plugSession, event, text string) {
	toAddr := "alice.dev." + to.Name
	fromAddr := "alice.dev." + from.Name
	out, isErr, err := from.P.Call("send", map[string]any{"to": toAddr, "text": text}, 20*time.Second)
	a.C.Check("mail "+name+": the "+from.Name+" session's MCP send tool succeeds", err == nil && !isErr && strings.HasPrefix(out, "sent #"), redact(out)+errSuffix(err))
	m := sentRE.FindStringSubmatch(out)
	if m == nil {
		a.C.Check("mail "+name+": the send receipt names the message number", false, "no '#N' in the receipt")
		return
	}
	deadline := time.Now().Add(20 * time.Second)
	var ctx string
	var herr error
	var hr Result
	for {
		ctx, hr, herr = a.runHook(pl, "hook-"+event+"-"+name, h, to, event)
		if herr != nil || strings.Contains(ctx, text) || time.Now().After(deadline) {
			break
		}
		time.Sleep(500 * time.Millisecond)
	}
	a.C.Check("mail "+name+": the "+event+" hook command from hooks.json exits 0", hr.Code == 0 && herr == nil, exitDetail(hr)+errSuffix(herr))
	a.C.Check("mail "+name+": the "+event+" hook shows it as mash mail context with the text", strings.Contains(ctx, "mash mail for ") && strings.Contains(ctx, text), "")
	a.C.Check("mail "+name+": the hook shows the sender's verified trust label ("+fromAddr+" [verified])", strings.Contains(ctx, "from "+fromAddr+" [verified]"), "")
	inbox, isErr, err := to.P.Call("inbox", map[string]any{}, 20*time.Second)
	a.C.Check("mail "+name+": cross-check, the recipient's MCP inbox tool lists it from "+fromAddr+" as verified", err == nil && !isErr && strings.Contains(inbox, fromAddr) && strings.Contains(inbox, "verified"), errSuffix(err))
	rd, isErr, err := to.P.Call("read", map[string]any{"seq": m[1]}, 20*time.Second)
	a.C.Check("mail "+name+": cross-check, the recipient's MCP read tool shows the text, verified", err == nil && !isErr && strings.Contains(rd, text) && strings.Contains(rd, "[verified]"), errSuffix(err))
}

func errSuffix(err error) string {
	if err == nil {
		return ""
	}
	return " " + redact(err.Error())
}

// launcherExecsBuild asserts that the process the launcher started is the harness's built mash (the launcher resolved
// $MASH_BIN and exec'd it), where the OS lets the harness see that.
func (a *AT) launcherExecsBuild(name string, s *plugSession) {
	exe, err := os.Readlink(fmt.Sprintf("/proc/%d/exe", s.P.PID))
	if err != nil {
		return // no /proc here: nothing to read, and no claim made
	}
	want, werr := filepath.EvalSymlinks(a.R.Mash)
	got, gerr := filepath.EvalSymlinks(exe)
	a.C.Check("the "+name+" session's process, started by the launcher, is the harness's built mash (the launcher resolved MASH_BIN)", werr == nil && gerr == nil && got == want, "")
}

// noDownload asserts that nothing was fetched or installed: no launcher message in any session's stderr or hook.log,
// and no bin directory under any CLAUDE_PLUGIN_DATA.
func (a *AT) noDownload(ss ...*plugSession) {
	var bad []string
	for _, s := range ss {
		if strings.Contains(s.P.Stderr(), "mash launcher:") {
			bad = append(bad, s.Name+" serve stderr has a launcher message")
		}
		if _, err := os.Stat(filepath.Join(s.Data, "bin")); err == nil {
			bad = append(bad, s.Name+" CLAUDE_PLUGIN_DATA has a bin directory")
		}
		if b, err := os.ReadFile(filepath.Join(stateDirOf(s.Env), "hook.log")); err == nil {
			if strings.Contains(string(b), "mash launcher:") {
				bad = append(bad, s.Name+" hook.log has a launcher message")
			}
			if k := scanBytes(b); len(k) > 0 {
				bad = append(bad, s.Name+" hook.log holds a "+strings.Join(k, ","))
			}
		}
	}
	a.C.Check("no download or install happened: the launcher resolved MASH_BIN (no launcher message in serve stderr or hook.log, no bin directory in CLAUDE_PLUGIN_DATA) and hook.log holds no seed or JWT", len(bad) == 0, strings.Join(bad, "; "))
}

// stateDirOf is MASH_STATE_DIR in env (the last assignment wins).
func stateDirOf(env []string) string {
	d := ""
	for _, kv := range env {
		if v, ok := strings.CutPrefix(kv, "MASH_STATE_DIR="); ok {
			d = v
		}
	}
	return d
}
