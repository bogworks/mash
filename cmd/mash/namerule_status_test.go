package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bogworks/mash/internal/config"
	"github.com/bogworks/mash/internal/namerule"
)

const statusRuleBody = `{"version":1,"rules":[{"id":"no-cc","match":{"agent":{"is":"cc"}},"message":"retired"},{"id":"no-x","match":{"name":{"is":"x"}},"message":"x"}]}`

func TestStatusShowsNameRuleNone(t *testing.T) {
	src := &statusSource{cfg: config.Config{Name: "a"}, cli: true, getenv: func(string) string { return "" }}
	if l := statusLine(t, statusText(src), "name rule: "); l != "name rule: none set" {
		t.Fatalf("got %q", l)
	}
}

func TestStatusShowsNameRuleLoaded(t *testing.T) {
	p := filepath.Join(t.TempDir(), "rule.json")
	if err := os.WriteFile(p, []byte(statusRuleBody), 0o600); err != nil {
		t.Fatal(err)
	}
	r, err := namerule.Load(p)
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256([]byte(statusRuleBody))
	want := "name rule: " + p + " sha256:" + hex.EncodeToString(sum[:])[:12] + " (2 rules)"
	src := &statusSource{cfg: config.Config{Name: "a", NameRule: r, NameRulePath: p}, cli: true, getenv: func(string) string { return "" }}
	if l := statusLine(t, statusText(src), "name rule: "); l != want {
		t.Fatalf("got %q, want %q", l, want)
	}
	// `mash status` from a shell shows the same line.
	var out bytes.Buffer
	statusCmd(src.cfg, nil, func(string) string { return "" }, &out)
	if !strings.Contains(out.String(), want+"\n") {
		t.Fatalf("mash status output lacks %q:\n%s", want, out.String())
	}
}

func TestStatusNameRuleOmittedOnConfigError(t *testing.T) {
	src := &statusSource{cfg: config.Config{Name: "a"}, cfgErr: errors.New("mash: name rule file x: broken"), cli: true, getenv: func(string) string { return "" }}
	out := statusText(src)
	if strings.Contains(out, "name rule: none set") {
		t.Fatalf("a failed load reported as no rule:\n%s", out)
	}
	if !strings.Contains(out, "name rule file x: broken") {
		t.Fatalf("the config error is not shown:\n%s", out)
	}
}

// (f) `mash admin provision` applies the rule through the same configuration.
func TestAdminProvisionAppliesTheNameRule(t *testing.T) {
	w := newWorld(t, "a")
	p := filepath.Join(t.TempDir(), "rule.json")
	if err := os.WriteFile(p, []byte(`{"version":1,"rules":[{"id":"no-c","match":{"name":{"is":"c"}},"message":"c is retired"}]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	r, err := namerule.Load(p)
	if err != nil {
		t.Fatal(err)
	}
	cfg := config.Config{InsecureLoopback: true, URL: w.e.URL, Owner: w.e.Owner, AdminCreds: w.e.AdminCreds, NameRule: r, NameRulePath: p}
	run := func(name string) (string, int) {
		_, u := w.e.Mint(t, name)
		var out, errb bytes.Buffer
		code := adminCmd(cfg, []string{"provision", name, "--user", u, "--account", w.e.Account}, &out, &errb)
		return out.String() + errb.String(), code
	}
	if out, code := run("c"); code == 0 || !strings.Contains(out, `name rule "no-c"`) || !strings.Contains(out, "c is retired") {
		t.Fatalf("refused name: %d %s", code, out)
	}
	if out, code := run("d"); code != 0 {
		t.Fatalf("allowed name: %d %s", code, out)
	}
}
