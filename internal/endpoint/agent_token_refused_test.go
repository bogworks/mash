package endpoint

import (
	"context"
	"strings"
	"testing"

	"github.com/bogworks/mash/internal/config"
)

// With agent_token set the endpoint opens as its three-token identity (naming plan step 2c): there is no refusal any
// more. With nothing to connect to, the open gets as far as the credentials, which is where a legacy open fails too.
func TestOpenWithAgentTokenIsNoLongerRefused(t *testing.T) {
	cfg := config.Config{Name: "a", Owner: "o", URL: "nats://127.0.0.1:1", AgentToken: "ag", Creds: "/nonexistent"}
	for name, open := range map[string]func() error{
		"OpenClient":     func() error { _, err := OpenClient(context.Background(), cfg); return err },
		"OpenClientWith": func() error { _, err := OpenClientWith(context.Background(), cfg, ClientOptions{}); return err },
		"Open":           func() error { _, err := Open(context.Background(), cfg); return err },
	} {
		err := open()
		if err == nil || strings.Contains(err.Error(), "cannot run a three-token identity") || !strings.Contains(err.Error(), "credentials") {
			t.Errorf("%s: got %v", name, err)
		}
	}
}

// An agent_token that makes no valid identity fails before any connection, naming the rule.
func TestOpenWithInvalidIdentityFailsBeforeConnecting(t *testing.T) {
	for _, tok := range []string{"cc", "Bad Token"} {
		cfg := config.Config{Name: "a", Owner: "o", URL: "nats://127.0.0.1:1", AgentToken: tok, Creds: "/nonexistent"}
		_, err := Open(context.Background(), cfg)
		if err == nil || strings.Contains(err.Error(), "credentials") {
			t.Errorf("agent_token %q: got %v", tok, err)
		}
	}
}

func TestStateLocation(t *testing.T) {
	legacy := config.Config{Name: "a", Owner: "o", StateDir: "/s"}
	if o, n, err := StateLocation(legacy); err != nil || o != "o" || n != "a" {
		t.Fatalf("legacy: %q %q %v", o, n, err)
	}
	nf := config.Config{Name: "a", Owner: "o", AgentToken: "ag", StateDir: "/s"}
	if o, n, err := StateLocation(nf); err != nil || o != "o" || n != "ag_o_a" {
		t.Fatalf("new-form: %q %q %v", o, n, err)
	}
	if d, err := SpoolDir(nf); err != nil || d != "/s/spool/o/ag_o_a" {
		t.Fatalf("spool dir: %q %v", d, err)
	}
	if d, err := SpoolDir(legacy); err != nil || d != "/s/spool/o/a" {
		t.Fatalf("legacy spool dir: %q %v", d, err)
	}
	if _, _, err := StateLocation(config.Config{Name: "a", Owner: "o", AgentToken: "cc"}); err == nil {
		t.Fatal("invalid identity accepted")
	}
}
