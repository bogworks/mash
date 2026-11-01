package main

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Live (opt-in, MASH_LIVE_CLAUDE=1; it spends one short Claude turn): a real Claude Code is the MCP client of a real
// `mash serve` on a scratch endpoint of a private NATS server, and the endpoint's lease must come to carry mcp_seen,
// which only an answered ping writes. This is the receipt that Claude Code answers server-initiated pings. The
// installed Claude Code is used as found (no version is pinned).
func TestLiveClaudeAnswersServePings(t *testing.T) {
	if os.Getenv("MASH_LIVE_CLAUDE") != "1" {
		t.Skip("set MASH_LIVE_CLAUDE=1 to run against the installed Claude Code")
	}
	claude, err := exec.LookPath("claude")
	if err != nil {
		t.Skip("no claude on PATH")
	}
	w := newWorld(t, "scratch")
	kv := leaseKV(t, w)
	dir := t.TempDir()
	bin := filepath.Join(dir, "mash")
	if out, err := exec.Command("go", "build", "-o", bin, ".").CombinedOutput(); err != nil {
		t.Fatalf("build: %v %s", err, out)
	}
	cfg, _ := json.Marshal(map[string]any{"mcpServers": map[string]any{
		"mash": map[string]any{"command": bin, "args": []string{"serve"}, "env": w.env["scratch"]}}})
	cfgPath := filepath.Join(dir, "mcp.json")
	if err := os.WriteFile(cfgPath, cfg, 0o600); err != nil {
		t.Fatal(err)
	}
	// The turn outlives one lease refresh (30 s), so the refresh after the first answered ping is written.
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, claude, "-p", "Run the shell command `sleep 45`, then reply with the single word done.",
		"--mcp-config", cfgPath, "--strict-mcp-config", "--allowedTools", "Bash(sleep:*)")
	var env []string
	for _, kv := range os.Environ() { // the session's own mash identity must not leak into the scratch serve
		if !strings.HasPrefix(kv, "MASH_") {
			env = append(env, kv)
		}
	}
	cmd.Env = env
	var out strings.Builder
	cmd.Stdout, cmd.Stderr = &out, &out
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	var seen string
	tick := time.NewTicker(time.Second)
	defer tick.Stop()
	for seen == "" {
		select {
		case err := <-done:
			t.Fatalf("claude exited (%v) before the lease carried mcp_seen:\n%s", err, out.String())
		case <-tick.C:
			if en, err := kv.Get(context.Background(), "scratch"); err == nil {
				var v map[string]any
				_ = json.Unmarshal(en.Value(), &v)
				seen, _ = v["mcp_seen"].(string)
				if seen != "" {
					t.Logf("lease: %s", en.Value())
				}
			}
		}
	}
	at, err := time.Parse(time.RFC3339, seen)
	if err != nil || time.Since(at) > time.Minute {
		t.Fatalf("mcp_seen %q is not a fresh time", seen)
	}
	v, _ := exec.Command(claude, "--version").Output()
	t.Logf("Claude Code %s answered serve's ping; mcp_seen %s", strings.TrimSpace(string(v)), seen)
	cancel()
	<-done
}
