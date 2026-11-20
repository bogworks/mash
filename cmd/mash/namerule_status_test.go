package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bogworks/mash/internal/config"
	"github.com/bogworks/mash/internal/namerule"
)

const statusRuleBody = `{"version":1,"rules":[{"id":"no-cc","match":{"group":{"is":"cc"}},"message":"retired"},{"id":"no-x","match":{"instance":{"is":"x"}},"message":"x"}]}`

// (f) `mash admin provision` applies the rule through the same configuration.
func TestAdminProvisionAppliesTheNameRule(t *testing.T) {
	w := newWorld(t, "a")
	p := filepath.Join(t.TempDir(), "rule.json")
	if err := os.WriteFile(p, []byte(`{"version":1,"rules":[{"id":"no-c","match":{"instance":{"is":"c"}},"message":"c is retired"}]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	r, err := namerule.Load(p)
	if err != nil {
		t.Fatal(err)
	}
	cfg := config.Config{InsecurePlaintext: true, URL: w.e.URL, Owner: w.e.Owner, AdminCreds: w.e.AdminCreds, NameRule: r, NameRulePath: p}
	run := func(name string) (string, int) {
		_, u := w.e.Mint(t, tAddr(name))
		var out, errb bytes.Buffer
		code := adminCmd(cfg, []string{"provision", tTo(name), "--user", u, "--account", w.e.Account}, &out, &errb)
		return out.String() + errb.String(), code
	}
	if out, code := run("c"); code == 0 || !strings.Contains(out, `name rule "no-c"`) || !strings.Contains(out, "c is retired") {
		t.Fatalf("refused name: %d %s", code, out)
	}
	if out, code := run("d"); code != 0 {
		t.Fatalf("allowed name: %d %s", code, out)
	}
}
