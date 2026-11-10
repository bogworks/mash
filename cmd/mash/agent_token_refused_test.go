package main

import (
	"bytes"
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/bogworks/mash/internal/config"
	"github.com/bogworks/mash/internal/endpoint"
	"github.com/bogworks/mash/internal/spool"
)

const agentTokenRefusal = "mash: agent_token is set but this build cannot run a three-token identity yet; unset agent_token (MASH_AGENT_TOKEN)"

func agentTokenEnv(t *testing.T) map[string]string {
	return map[string]string{
		"HOME": t.TempDir(), "MASH_NAME": "a", "MASH_OWNER": "o", "MASH_URL": "nats://127.0.0.1:1",
		"MASH_CREDS": "/nonexistent", "MASH_AGENT_TOKEN": "ag",
	}
}

func TestAgentTokenRefusalTextIsShared(t *testing.T) {
	if endpoint.ErrAgentTokenUnsupported.Error() != agentTokenRefusal {
		t.Fatalf("text changed: %q", endpoint.ErrAgentTokenUnsupported)
	}
}

// With agent_token set, every verb that would act on the legacy identity refuses, whether it reaches
// endpoint.Open* (which refuses itself) or not (hook, admin: refused in main).
func TestAgentTokenRefusedByEveryActingVerb(t *testing.T) {
	for _, args := range [][]string{
		{"hook"}, {"admin", "whatever"}, {"admin"}, {"send", "b", "hi"}, {"read", "1"}, {"inbox"}, {"who"}, {"prompt", "b", "hi"},
	} {
		t.Run(strings.Join(args, "_"), func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
			defer cancel()
			var out, errb bytes.Buffer
			code := runCtx(ctx, args, envFrom(agentTokenEnv(t)), strings.NewReader(`{"hook_event_name":"PostToolUse"}`), &out, &errb)
			if code == 0 {
				t.Fatalf("exit 0; stdout %q stderr %q", out.String(), errb.String())
			}
			if !strings.Contains(errb.String(), agentTokenRefusal) {
				t.Fatalf("stderr %q", errb.String())
			}
			if strings.Contains(out.String(), "additionalContext") {
				t.Fatalf("stdout %q", out.String())
			}
		})
	}
}

// serve degrades instead of exiting, but its endpoint comes from endpoint.Open, which refuses with the same text.
func TestAgentTokenRefusedByServeOpen(t *testing.T) {
	cfg := mustLoadAgentTokenCfg(t)
	if _, err := endpoint.Open(context.Background(), cfg); err == nil || err.Error() != agentTokenRefusal {
		t.Fatalf("Open: %v", err)
	}
	if _, err := endpoint.OpenClient(context.Background(), cfg); err == nil || err.Error() != agentTokenRefusal {
		t.Fatalf("OpenClient: %v", err)
	}
}

// status, plugin and version are unaffected.
func TestAgentTokenLeavesStatusPluginVersionWorking(t *testing.T) {
	for _, args := range [][]string{{"status"}, {"version"}, {"plugin", "--help"}, {"help"}} {
		var out, errb bytes.Buffer
		code := runCtx(context.Background(), args, envFrom(agentTokenEnv(t)), strings.NewReader(""), &out, &errb)
		if strings.Contains(errb.String(), agentTokenRefusal) {
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

// serve refuses before any spool or state action: the spool directory is neither created nor touched.
func TestAgentTokenServeDoesNotTouchSpool(t *testing.T) {
	env := agentTokenEnv(t)
	state := t.TempDir()
	env["MASH_STATE_DIR"] = state
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	var out, errb bytes.Buffer
	code := runCtx(ctx, []string{"serve"}, envFrom(env), strings.NewReader(""), &out, &errb)
	_ = code
	if !strings.Contains(errb.String(), agentTokenRefusal) {
		t.Fatalf("stderr %q", errb.String())
	}
	if ents, _ := os.ReadDir(state); len(ents) != 0 {
		t.Fatalf("state dir touched: %v", ents)
	}
	cfg := mustLoadAgentTokenCfg(t)
	if _, err := os.Stat(spool.Dir(state, cfg.Owner, cfg.Name)); err == nil {
		t.Fatal("spool dir exists")
	}
}
