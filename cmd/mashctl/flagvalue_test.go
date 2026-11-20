package main

import (
	"strings"
	"testing"
)

func TestFlagValueDash(t *testing.T) {
	cases := []struct {
		name   string
		args   []string
		parse  func([]string) error
		errHas string // empty: accepted
	}{
		{"audit --compare --json", []string{"--compare", "--json"}, func(a []string) error { _, err := parseAuditArgs(a); return err }, `--compare needs a value; "--json" looks like a flag`},
		{"audit --compare -", []string{"--compare", "-"}, func(a []string) error { _, err := parseAuditArgs(a); return err }, ""},
		{"endpoint inbox --after -3", []string{"x.y.z", "--after", "-3"}, func(a []string) error { _, err := parseEpInput(endpointInboxSpec, a); return err }, `looks like a flag`},
		{"init --config --dry-run", []string{"--config", "--dry-run"}, func(a []string) error { _, err := parseEpInput(initSpec, a); return err }, `--config needs a value`},
	}
	for _, c := range cases {
		err := c.parse(c.args)
		switch {
		case c.errHas == "" && err != nil:
			t.Errorf("%s: refused: %v", c.name, err)
		case c.errHas != "" && (err == nil || !strings.Contains(err.Error(), c.errHas)):
			t.Errorf("%s: err %v, want %q", c.name, err, c.errHas)
		}
		if err != nil && strings.Contains(err.Error(), "hunter2") {
			t.Errorf("%s: the value is echoed: %v", c.name, err)
		}
	}
}
