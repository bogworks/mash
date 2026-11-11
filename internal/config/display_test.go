package config

import "testing"

func TestDisplayName(t *testing.T) {
	for _, c := range []struct {
		name string
		cfg  Config
		want string
	}{
		{"three tokens", Config{Name: "sap-go-sdk", Owner: "x", AgentToken: "bogworks"}, "bogworks.x.sap-go-sdk"},
		{"legacy", Config{Name: "team-a", Owner: "acme"}, "team-a"},
		{"invalid three tokens fall back to the name", Config{Name: "Bad", Owner: "x", AgentToken: "bogworks"}, "Bad"},
		{"unset", Config{}, ""},
	} {
		if got := c.cfg.DisplayName(); got != c.want {
			t.Errorf("%s: DisplayName() = %q, want %q", c.name, got, c.want)
		}
	}
}
