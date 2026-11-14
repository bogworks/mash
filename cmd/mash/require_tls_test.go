package main

import (
	"bytes"
	"context"
	"github.com/bogworks/mash/internal/config"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/bogworks/mash/internal/endpoint"
)

// requireTLSEnv is an endpoint configuration whose server is a plain remote URL and which sets MASH_REQUIRE_TLS.
func requireTLSEnv(t *testing.T) map[string]string {
	t.Helper()
	return map[string]string{"HOME": t.TempDir(), "MASH_NAME": "a", "MASH_OWNER": "o", "MASH_CREDS": "/none",
		"MASH_URL": "nats://u:s3cret@10.255.255.1:4222", "MASH_REQUIRE_TLS": "1"}
}

const refusalHost = `refusing a plain nats:// connection to "10.255.255.1"`

// The hook connects to nothing: under a refused configuration it says why on stderr (hook.log) and exits 0.
func TestHookRefusedConfigSaysSoAndExitsZero(t *testing.T) {
	var out, errb bytes.Buffer
	code := run([]string{"hook"}, envFrom(requireTLSEnv(t)), strings.NewReader(`{"hook_event_name":"UserPromptSubmit"}`), &out, &errb)
	if code != 0 {
		t.Fatalf("hook exited %d", code)
	}
	if out.Len() != 0 {
		t.Fatalf("stdout %q", out.String())
	}
	for _, want := range []string{"mash hook:", "MASH_REQUIRE_TLS", refusalHost, "use tls://"} {
		if !strings.Contains(errb.String(), want) {
			t.Errorf("stderr %q lacks %q", errb.String(), want)
		}
	}
	if strings.Contains(errb.String(), "s3cret") {
		t.Errorf("stderr leaks the password: %q", errb.String())
	}
}

// With the setting off the hook says nothing about TLS.
func TestHookWithoutRequireTLSIsSilent(t *testing.T) {
	env := requireTLSEnv(t)
	env["MASH_REQUIRE_TLS"] = "0"
	var out, errb bytes.Buffer
	if code := run([]string{"hook"}, envFrom(env), strings.NewReader(`{"hook_event_name":"UserPromptSubmit"}`), &out, &errb); code != 0 || errb.Len() != 0 {
		t.Fatalf("exit %d, stderr %q", code, errb.String())
	}
}

// serve reports the refusal as its start error on stderr and in the notice, makes no retry, and keeps answering MCP.
func TestServeRefusesPlainRemoteUnderRequireTLS(t *testing.T) {
	var calls atomic.Int64
	old := openEndpoint
	openEndpoint = func(ctx2 context.Context, cfg config.Config) (*endpoint.Endpoint, error) {
		calls.Add(1)
		return old(ctx2, cfg)
	}
	t.Cleanup(func() { openEndpoint = old })
	s := startServe(t, requireTLSEnv(t))
	s.initialize(t)
	notice := content(s.await(t, 5*time.Second, isChannel))
	for _, want := range []string{"could not start", "MASH_REQUIRE_TLS", refusalHost} {
		if !strings.Contains(notice, want) {
			t.Errorf("notice %q lacks %q", notice, want)
		}
	}
	status := resultText(s.call(t, 7, "status", `{}`))
	if l := statusLine(t, status, "tls: "); !strings.HasPrefix(l, "tls: FAILED - ") || !strings.Contains(l, refusalHost) {
		t.Errorf("status tool tls line %q", l)
	}
	time.Sleep(200 * time.Millisecond)
	if n := calls.Load(); n != 1 {
		t.Fatalf("%d open attempts, want 1: a refusal is permanent", n)
	}
	if e := s.stderr(); !strings.Contains(e, refusalHost) || strings.Contains(e, "s3cret") {
		t.Errorf("stderr %q", e)
	}
}

// The CLI commands refuse before connecting, exit non-zero, and `mash status` shows the refusal.
func TestCLIRefusesPlainRemoteUnderRequireTLS(t *testing.T) {
	for _, cmd := range [][]string{{"inbox"}, {"who"}, {"send", "b", "hi"}} {
		var out, errb bytes.Buffer
		code := run(cmd, envFrom(requireTLSEnv(t)), strings.NewReader(""), &out, &errb)
		got := out.String() + errb.String()
		if code == 0 || !strings.Contains(got, refusalHost) || !strings.Contains(got, "MASH_REQUIRE_TLS") || strings.Contains(got, "s3cret") {
			t.Errorf("%v: exit %d, output %q", cmd, code, got)
		}
	}
	var out, errb bytes.Buffer
	if code := run([]string{"status"}, envFrom(requireTLSEnv(t)), strings.NewReader(""), &out, &errb); code != 0 {
		t.Fatalf("status exited %d", code)
	}
	if l := statusLine(t, out.String(), "tls: "); !strings.HasPrefix(l, "tls: FAILED - ") || !strings.Contains(l, refusalHost) {
		t.Errorf("status tls line %q", l)
	}
}
