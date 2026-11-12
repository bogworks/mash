package ctl

import (
	"testing"

	"github.com/bogworks/mash/internal/config"
)

func TestNewFormRequested(t *testing.T) {
	for args, want := range map[string]bool{
		"provision x --user U":               false,
		"provision x --agent a --user U":     true,
		"deprovision a.o.x":                  true,
		"deprovision a_o_x":                  true,
		"deprovision x":                      false,
		"deprovision x --agent a":            true,
		"deprovision x --user U":             false,
		"list":                               false,
		"suspend a.o.x":                      false,
		"provision":                          false,
		"provision a.o.x --alias o/n":        true,
		"provision x --note --agent":         false, // a value, not a flag
		"deprovision x --note a --no-revoke": false,
	} {
		if got := NewFormRequested(fields(args)); got != want {
			t.Errorf("%q: got %v want %v", args, got, want)
		}
	}
}

func TestParseNewForm(t *testing.T) {
	cfg := config.Config{Owner: "own", AgentToken: "ops"}
	rest, nf, err := parseNewForm(cfg, fields("provision recv --agent acme --owner t1 --user U"))
	if err != nil || nf == nil || nf.id.String() != "acme.t1.recv" || len(rest) != 4 || rest[2] != "--user" {
		t.Fatalf("%v %+v %v", rest, nf, err)
	}
	_, nf, err = parseNewForm(cfg, fields("provision recv --agent acme"))
	if err != nil || nf.id.String() != "acme.own.recv" {
		t.Fatalf("default owner: %+v %v", nf, err)
	}
	_, nf, err = parseNewForm(cfg, fields("deprovision acme_t1_recv"))
	if err != nil || nf.id.String() != "acme.t1.recv" {
		t.Fatalf("flat: %+v %v", nf, err)
	}
	_, nf, err = parseNewForm(cfg, fields("provision recv --user U"))
	if err != nil || nf != nil {
		t.Fatalf("legacy: %+v %v", nf, err)
	}
	for _, bad := range []string{
		"provision recv --agent",
		"provision recv --agent a --agent b",
		"provision recv --owner o",
		"provision recv --alias o/n",
		"provision a.o.x --agent a",
		"deprovision x --agent a --alias o/n",
		"provision x --agent cc",
		"provision x --agent a --alias nope",
		"provision a.b x",
	} {
		if _, _, err := parseNewForm(cfg, fields(bad)); err == nil {
			t.Errorf("%q: no error", bad)
		} else if !isUsage(err) {
			t.Errorf("%q: not a usage error: %v", bad, err)
		}
	}
	// Setting off: a configuration refusal, not a usage error.
	_, _, err = parseNewForm(config.Config{Owner: "own"}, fields("provision recv --agent acme"))
	if err == nil || isUsage(err) {
		t.Fatalf("setting off: %v", err)
	}
	// Setting off: a bare flat key to deprovision is the admin's directory key and keeps its legacy-key path (as
	// suspend, resume and suspended take it); the dotted form, a three-token flag and provision still need the setting.
	off := config.Config{Owner: "own"}
	if rest, nf, err := parseNewForm(off, fields("deprovision acme_t1_recv --drained-to 3 --suspension ID")); err != nil || nf != nil || len(rest) != 6 {
		t.Fatalf("flat key, setting off: %v %v %v", rest, nf, err)
	}
	for _, s := range []string{"deprovision acme.t1.recv", "deprovision recv --agent acme", "provision acme_t1_recv --user U"} {
		if _, _, err := parseNewForm(off, fields(s)); err == nil || isUsage(err) {
			t.Errorf("%q, setting off: %v", s, err)
		}
	}
}

func fields(s string) []string {
	var out []string
	cur := ""
	for _, r := range s + " " {
		if r == ' ' {
			if cur != "" {
				out = append(out, cur)
			}
			cur = ""
			continue
		}
		cur += string(r)
	}
	return out
}
