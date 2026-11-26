package config

import "testing"

func TestDisplayName(t *testing.T) {
	for _, c := range []struct {
		name string
		cfg  Config
		want string
	}{
		{"three tokens", Config{Name: "build-bot", Owner: "dev", Group: "acme"}, "dev.acme.build-bot"},
		{"no group falls back to the name", Config{Name: "team-a", Owner: "acme"}, "team-a"},
		{"invalid tokens fall back to the name", Config{Name: "Bad", Owner: "x", Group: "bogworks"}, "Bad"},
		{"unset", Config{}, ""},
	} {
		if got := c.cfg.DisplayName(); got != c.want {
			t.Errorf("%s: DisplayName() = %q, want %q", c.name, got, c.want)
		}
	}
}
