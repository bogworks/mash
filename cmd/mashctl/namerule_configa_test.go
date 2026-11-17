package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bogworks/mash/internal/sap"
)

// Configuration A of the naming plan's negative tests, at the mashctl entry point of REQ-016: a rule file that refuses
// the group "retired" and the instance "mash". These tests are permanent; none may be skipped or weakened.
// The full table is in internal/endpoint/namerule_configa_test.go.
//
// An address is owner.group.instance; the rule matches its three tokens. `provision` is the entry point mashctl has
// that makes an endpoint, and the rule is applied before the directory is read.
const (
	configAMashctlRule = `{"version":1,"rules":[` +
		`{"id":"no-retired","match":{"group":{"is":"retired"}},"message":"the group retired is closed"},` +
		`{"id":"no-mash","match":{"instance":{"is":"mash"}},"message":"the instance mash is reserved"}]}`
	configAMashctlEmpty = `{"version":1,"rules":[]}`
)

func writeRule(t *testing.T, dir, name, body string) string {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

// provisionOf runs `provision` for the address, with a user key minted for it.
func (h *fleetEnv) provisionOf(addr string) (string, string, int) {
	h.t.Helper()
	a, err := parseAddr(addr)
	if err != nil {
		h.t.Fatal(err)
	}
	_, user := h.e.Mint(h.t, a)
	return h.run("provision", addr, "--user", user, "--account", h.e.Account)
}

func TestConfigAMashctlProvision(t *testing.T) {
	h := newFleetEnv(t)
	h.seed(ad("early"))
	h.env["MASH_NAME_RULE_FILE"] = writeRule(t, h.dir, "name-rule.json", configAMashctlRule)
	recsBefore := h.records()
	cases := []struct {
		name, addr string
		want       []string // every substring the refusal's text must carry
	}{
		{"group refused", "o.retired.fresh", []string{`name rule "no-retired"`, "the group retired is closed"}},
		{"instance refused", "o.g.mash", []string{`name rule "no-mash"`, "the instance mash is reserved"}},
		{"flat key, group refused", "o_retired_fresh", []string{`name rule "no-retired"`}},
		{"record exists, instance refused", "o.g.early", []string{"already"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if c.name == "record exists, instance refused" { // a control: the rule does not refuse it, the directory does
				_, errb, code := h.provisionOf(c.addr)
				if code == 0 || strings.Contains(errb, "name rule") {
					t.Fatalf("%d %q", code, errb)
				}
				return
			}
			out, errb, code := h.provisionOf(c.addr)
			if code == 0 {
				t.Fatalf("a refused provision succeeded: %q %q", out, errb)
			}
			for _, w := range c.want {
				if !strings.Contains(errb, w) {
					t.Fatalf("the refusal lacks %q: %q", w, errb)
				}
			}
			if strings.Contains(errb, "already") || strings.Contains(errb, "not found") {
				t.Fatalf("the directory was read before the rule: %q", errb)
			}
		})
	}
	if got := h.records(); strings.Join(got, ",") != strings.Join(recsBefore, ",") {
		t.Errorf("a refused provision changed the records: %v -> %v", recsBefore, got)
	}
}

// A deployment that sets name_rule_required but names no file: every mashctl verb that loads the configuration refuses
// to start, from the environment and from config.json alike.
func TestConfigAMashctlRequiredWithoutFileRefusesToStart(t *testing.T) {
	verbs := [][]string{
		{"list"}, {"init"}, {"limits"}, {"provision", "o.g.x", "--user", "U", "--account", "A"},
		{"label", "o.g.x", "k=v"}, {"retire", "o.g.x"},
		{"adopt", "--server", "nats://127.0.0.1:1", "--operator-key", "/x", "--account-key", "/x", "--sys-creds", "/x", "--admin-creds", "/x"},
	}
	for _, how := range []string{"environment", "config.json"} {
		for _, args := range verbs {
			dir := t.TempDir()
			env := map[string]string{"HOME": t.TempDir(), "MASH_URL": "nats://127.0.0.1:1", "MASH_OWNER": "o", "MASH_CONFIG_DIR": dir}
			if how == "environment" {
				env["MASH_NAME_RULE_REQUIRED"] = "true"
			} else {
				writeRule(t, dir, "config.json", `{"name_rule_required": true}`)
			}
			var out, errb bytes.Buffer
			code := runEnv(args, func(k string) string { return env[k] }, &out, &errb)
			if code == 0 || !strings.Contains(errb.String(), "a name rule is required") || !strings.Contains(errb.String(), "MASH_NAME_RULE_FILE") {
				t.Errorf("%s %v: code %d stdout %q stderr %q", how, args, code, out.String(), errb.String())
			}
		}
	}
	// A path that is set but empty, or that cannot be read, is an error and not "no rule".
	absent := filepath.Join(t.TempDir(), "absent", "name-rule.json") // under a fresh temp dir, never created
	for name, c := range map[string]struct{ val, want string }{
		"empty":   {"", "MASH_NAME_RULE_FILE is set but empty"},
		"missing": {absent, absent},
	} {
		env := map[string]string{"HOME": t.TempDir(), "MASH_URL": "nats://127.0.0.1:1", "MASH_OWNER": "o"}
		lookup := func(k string) (string, bool) {
			if k == "MASH_NAME_RULE_FILE" {
				return c.val, true // set, possibly empty: the lookup says so, as the process environment does
			}
			return env[k], env[k] != ""
		}
		var out, errb bytes.Buffer
		code := run([]string{"list"}, func(k string) string { return env[k] }, lookup, &out, &errb)
		if code == 0 || !strings.Contains(errb.String(), c.want) {
			t.Errorf("%s rule file: %d %q %q", name, code, out.String(), errb.String())
		}
	}
}

// With a rule file, name_rule_required lets mashctl start; the file decides what is refused.
func TestConfigAMashctlRequiredWithFile(t *testing.T) {
	h := newFleetEnv(t)
	h.env["MASH_NAME_RULE_REQUIRED"] = "true"
	h.env["MASH_NAME_RULE_FILE"] = writeRule(t, h.dir, "name-rule.json", configAMashctlRule)
	if _, errb, code := h.provisionOf("o.retired.fresh"); code == 0 || !strings.Contains(errb, `name rule "no-retired"`) {
		t.Fatalf("required with configuration A: %d %q", code, errb)
	}
	// The control: the same requirement with a file that refuses nothing starts and provisions.
	h.env["MASH_NAME_RULE_FILE"] = writeRule(t, h.dir, "permissive.json", configAMashctlEmpty)
	if out, errb, code := h.provisionOf("o.retired.fresh"); code != 0 {
		t.Fatalf("required with a permissive file: %d %q %q", code, out, errb)
	}
}

// The rule is read from MASH_NAME_RULE_FILE, and the environment wins over name_rule_file in config.json, in both
// directions: a permissive config.json does not weaken an environment that names configuration A, and a
// configuration-A config.json does not override a permissive environment.
func TestConfigAMashctlRuleFileEnvironmentWinsOverConfigJSON(t *testing.T) {
	h := newFleetEnv(t)
	a := writeRule(t, h.dir, "rule-a.json", configAMashctlRule)
	permissive := writeRule(t, h.dir, "rule-none.json", configAMashctlEmpty)

	// config.json names the permissive file; the environment names configuration A: refused.
	writeRule(t, h.dir, "config.json", `{"name_rule_file":"`+permissive+`"}`)
	h.env["MASH_NAME_RULE_FILE"] = a
	if _, errb, code := h.provisionOf("o.retired.fresh"); code == 0 || !strings.Contains(errb, `name rule "no-retired"`) {
		t.Fatalf("environment configuration A over a permissive config.json: %d %q", code, errb)
	}
	if _, ok := h.record(sap.Address{Owner: "o", Group: "retired", Instance: "fresh"}); ok {
		t.Error("a refused provision made a record")
	}

	// config.json alone names configuration A: refused (the file key is read).
	delete(h.env, "MASH_NAME_RULE_FILE")
	writeRule(t, h.dir, "config.json", `{"name_rule_file":"`+a+`"}`)
	if _, errb, code := h.provisionOf("o.retired.fresh"); code == 0 || !strings.Contains(errb, `name rule "no-retired"`) {
		t.Fatalf("configuration A from config.json alone: %d %q", code, errb)
	}

	// config.json names configuration A; the environment names the permissive file: the environment wins, provision succeeds.
	h.env["MASH_NAME_RULE_FILE"] = permissive
	if out, errb, code := h.provisionOf("o.retired.fresh"); code != 0 {
		t.Fatalf("a permissive environment over configuration A in config.json: %d %q %q", code, out, errb)
	}
}
