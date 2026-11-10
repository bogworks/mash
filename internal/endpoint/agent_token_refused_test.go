package endpoint

import (
	"context"
	"strings"
	"testing"

	"github.com/bogworks/mash/internal/config"
)

func TestOpenRefusesAgentTokenBeforeConnecting(t *testing.T) {
	cfg := config.Config{Name: "a", Owner: "o", URL: "nats://127.0.0.1:1", AgentToken: "ag", Creds: "/nonexistent"}
	for name, open := range map[string]func() error{
		"OpenClient":     func() error { _, err := OpenClient(context.Background(), cfg); return err },
		"OpenClientWith": func() error { _, err := OpenClientWith(context.Background(), cfg, ClientOptions{}); return err },
		"Open":           func() error { _, err := Open(context.Background(), cfg); return err },
	} {
		err := open()
		if err == nil || !strings.Contains(err.Error(), "agent_token is set but this build cannot run a three-token identity yet") {
			t.Errorf("%s: got %v", name, err)
		}
	}
}
