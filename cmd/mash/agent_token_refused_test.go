package main

import (
	"bytes"
	"context"
	"strings"
	"testing"
	"time"

	"github.com/bogworks/mash/internal/config"
	"github.com/bogworks/mash/internal/endpoint"
	"github.com/bogworks/mash/internal/spool"
)

const adminAgentTokenRefusal = "mash: agent_token is set but mash admin cannot act on a three-token identity yet; unset agent_token (MASH_AGENT_TOKEN)"

func agentTokenEnv(t *testing.T) map[string]string {
	return map[string]string{
		"HOME": t.TempDir(), "MASH_NAME": "a", "MASH_OWNER": "o", "MASH_URL": "nats://127.0.0.1:1",
		"MASH_CREDS": "/nonexistent", "MASH_AGENT_TOKEN": "ag",
	}
}

// mash admin acts on the legacy identity (naming plan 1.6), so it still refuses with agent_token set.
func TestAgentTokenRefusedByAdmin(t *testing.T) {
	for _, args := range [][]string{{"admin", "whatever"}, {"admin"}} {
		t.Run(strings.Join(args, "_"), func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
			defer cancel()
			var out, errb bytes.Buffer
			code := runCtx(ctx, args, envFrom(agentTokenEnv(t)), strings.NewReader(""), &out, &errb)
			if code == 0 || !strings.Contains(errb.String(), adminAgentTokenRefusal) {
				t.Fatalf("code %d stderr %q", code, errb.String())
			}
		})
	}
}

// The verbs wired in step 2c no longer refuse: with creds that do not exist they fail at the credentials, like a legacy
// endpoint does, and hook is a silent no-op with no spool.
func TestAgentTokenVerbsAreNotRefused(t *testing.T) {
	for _, args := range [][]string{{"send", "b", "hi"}, {"read", "1"}, {"inbox"}, {"who"}, {"prompt", "b", "hi"}} {
		t.Run(strings.Join(args, "_"), func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
			defer cancel()
			var out, errb bytes.Buffer
			code := runCtx(ctx, args, envFrom(agentTokenEnv(t)), strings.NewReader(""), &out, &errb)
			if code == 0 || strings.Contains(errb.String(), "cannot run a three-token identity") || !strings.Contains(errb.String(), "credentials") {
				t.Fatalf("code %d stdout %q stderr %q", code, out.String(), errb.String())
			}
		})
	}
	var out, errb bytes.Buffer
	env := agentTokenEnv(t)
	env["MASH_STATE_DIR"] = t.TempDir()
	if code := runCtx(context.Background(), []string{"hook"}, envFrom(env), strings.NewReader(`{"hook_event_name":"PostToolUse"}`), &out, &errb); code != 0 || out.String() != "" {
		t.Fatalf("hook: code %d stdout %q stderr %q", code, out.String(), errb.String())
	}
}

// serve opens as the three-token identity: it no longer prints the refusal, and fails (here, at the credentials).
func TestAgentTokenServeOpensTheIdentity(t *testing.T) {
	env := agentTokenEnv(t)
	env["MASH_STATE_DIR"] = t.TempDir()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	var out, errb bytes.Buffer
	_ = runCtx(ctx, []string{"serve"}, envFrom(env), strings.NewReader(""), &out, &errb)
	if strings.Contains(errb.String(), "cannot run a three-token identity") || !strings.Contains(errb.String(), "credentials") {
		t.Fatalf("stderr %q", errb.String())
	}
}

// A new-form endpoint's spool is keyed by its flat identity, never by the bare name a legacy endpoint of the same name
// token would use.
func TestAgentTokenSpoolIsKeyedByFlatIdentity(t *testing.T) {
	cfg := mustLoadAgentTokenCfg(t)
	cfg.StateDir = "/s"
	d, err := endpoint.SpoolDir(cfg)
	if err != nil || d != spool.Dir("/s", "o", "ag_o_a") || d == spool.Dir("/s", cfg.Owner, cfg.Name) {
		t.Fatalf("spool dir %q %v", d, err)
	}
}

// status, plugin and version are unaffected.
func TestAgentTokenLeavesStatusPluginVersionWorking(t *testing.T) {
	for _, args := range [][]string{{"status"}, {"version"}, {"plugin", "--help"}, {"help"}} {
		var out, errb bytes.Buffer
		code := runCtx(context.Background(), args, envFrom(agentTokenEnv(t)), strings.NewReader(""), &out, &errb)
		if strings.Contains(errb.String(), adminAgentTokenRefusal) {
			t.Errorf("%v refused: %q", args, errb.String())
		}
		if args[0] == "status" || args[0] == "version" || args[0] == "help" {
			if code != 0 {
				t.Errorf("%v exit %d: %s", args, code, errb.String())
			}
		}
	}
}

func mustLoadAgentTokenCfg(t *testing.T) config.Config {
	t.Helper()
	env := agentTokenEnv(t)
	cfg, err := config.Load(config.LookupFromFunc(envFrom(env)), env["HOME"])
	if err != nil || cfg.AgentToken != "ag" {
		t.Fatalf("config: %+v %v", cfg, err)
	}
	return cfg
}

// MASH_AGENT_TOKEN present but empty is a config error; hook must refuse it, not treat it as a silent no-op.
func TestEmptyAgentTokenRefusedByHookAndAdmin(t *testing.T) {
	for _, args := range [][]string{{"hook"}, {"admin", "x"}} {
		t.Run(strings.Join(args, "_"), func(t *testing.T) {
			env := agentTokenEnv(t)
			delete(env, "MASH_AGENT_TOKEN")
			lookup := func(k string) (string, bool) {
				if k == "MASH_AGENT_TOKEN" {
					return "", true
				}
				return env[k], env[k] != ""
			}
			var out, errb bytes.Buffer
			code := runLookup(context.Background(), args, envFrom(env), lookup, strings.NewReader(`{"hook_event_name":"PostToolUse"}`), &out, &errb)
			if code == 0 || !strings.Contains(errb.String(), "MASH_AGENT_TOKEN") || strings.Contains(out.String(), "additionalContext") {
				t.Fatalf("code %d stdout %q stderr %q", code, out.String(), errb.String())
			}
			// Unset: unchanged, no refusal naming the token.
			errb.Reset()
			runLookup(context.Background(), args, envFrom(env), config.LookupFromFunc(envFrom(env)), strings.NewReader(`{"hook_event_name":"PostToolUse"}`), &out, &errb)
			if strings.Contains(errb.String(), "MASH_AGENT_TOKEN") {
				t.Fatalf("unset token refused: %q", errb.String())
			}
		})
	}
}
