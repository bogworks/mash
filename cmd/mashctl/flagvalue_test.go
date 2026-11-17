package main

import (
	"strings"
	"testing"
)

func TestFlagValueDash(t *testing.T) {
	envf := func(string) string { return "" }
	cases := []struct {
		name   string
		args   []string
		parse  func([]string) error
		errHas string // empty: accepted
	}{
		{"servers --sys-creds", []string{"--servers", "--sys-creds", "f"}, func(a []string) error { _, _, err := extractServerFlags(a, envf); return err }, `--servers needs a value; "--sys-creds" looks like a flag`},
		{"sys-creds -", []string{"--sys-creds", "-"}, func(a []string) error { _, _, err := extractServerFlags(a, envf); return err }, ""},
		{"servers=-1 inline passes the helper", []string{"--sys-creds=-x"}, func(a []string) error { _, _, err := extractServerFlags(a, envf); return err }, ""},
		{"audit --compare --json", []string{"--compare", "--json"}, func(a []string) error { _, err := parseAuditArgs(a); return err }, `--compare needs a value; "--json" looks like a flag`},
		{"audit --compare -", []string{"--compare", "-"}, func(a []string) error { _, err := parseAuditArgs(a); return err }, ""},
		{"issued check --servers --json", []string{"--servers", "--json"}, func(a []string) error { _, err := parseIssuedArgs("check", a); return err }, `--servers needs a value`},
		{"inbox --from -3", []string{"--from", "-3"}, func(a []string) error { _, err := parseInboxInspectArgs(a); return err }, `looks like a flag`},
		{"adopt --owner --mint-admin", []string{"--owner", "--mint-admin"}, func(a []string) error { _, err := parseAdoptArgs(a); return err }, `--owner needs a value`},
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
