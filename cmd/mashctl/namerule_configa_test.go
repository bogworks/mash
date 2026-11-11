package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Configuration A of the naming plan's negative tests (steps 1.10 and 3.1), at the mashctl entry points of REQ-016:
// a rule file that refuses the agent tokens cc and mash. These tests are permanent; none may be skipped or weakened.
// The full table is in internal/endpoint/namerule_configa_test.go.
//
// mashctl provisions legacy endpoints only, so what `add` and `provision` create is always the identity
// (cc, owner, name): a bare name is refused by the rule no-cc, whatever the name. A three-token identity (a configured
// agent_token, whether cc, mash or an ordinary token) is refused by mashctl itself (errAgentTokenSet, its fail-closed
// refusal while it cannot act on one), before the rule is reached; the flat and dotted spellings are refused by the name
// syntax. There is therefore no allowed control at provision under configuration A: every form mashctl can express is
// refused, and the control is a rule file that refuses none of them (TestConfigAMashctlRequiredWithFile).
const (
	configAMashctlRule = `{"version":1,"rules":[` +
		`{"id":"no-cc","match":{"agent":{"is":"cc"}},"message":"the agent token cc is retired"},` +
		`{"id":"no-mash","match":{"agent":{"is":"mash"}},"message":"the agent token mash is reserved"}]}`
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

// assertNothingCreated fails if a refused add or provision left anything behind.
func (h *addEnv) assertNothingCreated(names ...string) {
	h.t.Helper()
	for _, n := range names {
		if _, err := os.Stat(h.creds(n)); err == nil {
			h.t.Errorf("a refused command wrote the creds of %q", n)
		}
	}
}

func TestConfigAMashctlAdd(t *testing.T) {
	h := newAddEnv(t, addPolicy)
	fakeKeyRevoked(t, false, nil)
	// An endpoint added before the rule came: adding its name again is refused by the rule, before the directory is
	// read ("already provisioned" would mean it was).
	if _, errb, code := h.run("add", "early", "--zone", "green"); code != 0 {
		t.Fatalf("add before the rule: %d %q", code, errb)
	}
	h.env["MASH_NAME_RULE_FILE"] = writeRule(t, h.dir, "name-rule.json", configAMashctlRule)
	before, _ := os.ReadFile(h.policy)
	recsBefore := h.records()

	cases := []struct {
		name string
		args []string
		env  map[string]string
		want []string // every substring the refusal's text must carry
	}{
		{"bare legacy name, no record", []string{"add", "fresh", "--zone", "green"}, nil,
			[]string{`name rule "no-cc"`, "the agent token cc is retired", "provision"}},
		{"bare legacy name, record exists", []string{"add", "early", "--zone", "green"}, nil,
			[]string{`name rule "no-cc"`, "the agent token cc is retired"}},
		{"bare name that is a reserved agent token", []string{"add", "mash", "--zone", "green"}, nil,
			[]string{`name rule "no-cc"`, "the agent token cc is retired"}},
		{"three-token flat key with agent cc", []string{"add", "cc_acme_gone", "--zone", "green"}, nil, []string{"endpoint name"}},
		{"three-token flat key with agent mash", []string{"add", "mash_acme_gone", "--zone", "green"}, nil, []string{"endpoint name"}},
		{"dotted display form with agent cc", []string{"add", "cc.acme.gone", "--zone", "green"}, nil, []string{"endpoint name"}},
		{"configured agent_token cc", []string{"add", "fresh", "--zone", "green"}, map[string]string{"MASH_NAME": "fresh", "MASH_AGENT_TOKEN": "cc"}, []string{refusal}},
		{"configured agent_token mash", []string{"add", "fresh", "--zone", "green"}, map[string]string{"MASH_NAME": "fresh", "MASH_AGENT_TOKEN": "mash"}, []string{refusal}},
		{"configured agent_token with an ordinary token", []string{"add", "fresh", "--zone", "green"}, map[string]string{"MASH_NAME": "fresh", "MASH_AGENT_TOKEN": "claude"}, []string{refusal}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			for k, v := range c.env {
				h.env[k] = v
				defer delete(h.env, k)
			}
			out, errb, code := h.run(c.args...)
			if code == 0 {
				t.Fatalf("a refused add succeeded: %q %q", out, errb)
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
	h.assertNothingCreated("fresh", "mash", "cc_acme_gone", "mash_acme_gone", "cc.acme.gone")
	if after, _ := os.ReadFile(h.policy); !bytes.Equal(before, after) {
		t.Error("a refused add changed the policy")
	}
	if got := h.records(); strings.Join(got, ",") != strings.Join(recsBefore, ",") {
		t.Errorf("a refused add changed the records: %v -> %v", recsBefore, got)
	}
}

// `mashctl provision` is the same entry point through the other verb: the rule is applied through ctl.Run's admin.
func TestConfigAMashctlProvision(t *testing.T) {
	h := newAddEnv(t, addPolicy)
	h.env["MASH_NAME_RULE_FILE"] = writeRule(t, h.dir, "name-rule.json", configAMashctlRule)
	recsBefore := h.records()
	for _, name := range []string{"fresh", "mash", "gone"} {
		_, user := h.e.Mint(t, name)
		_, errb, code := h.run("provision", name, "--user", user, "--account", h.e.Account)
		if code == 0 || !strings.Contains(errb, `name rule "no-cc"`) || !strings.Contains(errb, "the agent token cc is retired") {
			t.Errorf("provision %q: %d %q", name, code, errb)
		}
	}
	if got := h.records(); strings.Join(got, ",") != strings.Join(recsBefore, ",") {
		t.Errorf("a refused provision changed the records: %v -> %v", recsBefore, got)
	}
}

// A deployment that sets name_rule_required but names no file: every mashctl verb that loads the configuration refuses
// to start, from the environment and from config.json alike.
func TestConfigAMashctlRequiredWithoutFileRefusesToStart(t *testing.T) {
	verbs := [][]string{
		{"list"}, {"init"}, {"limits"}, {"provision", "x", "--user", "U", "--account", "A"},
		{"policy", "check"}, {"policy", "show"}, {"policy", "apply", "--dry-run"}, {"policy", "drift"},
		{"add", "x"}, {"label", "x", "k=v"}, {"retire", "x"}, {"reissue", "x"},
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
	h := newAddEnv(t, addPolicy)
	fakeKeyRevoked(t, false, nil)
	h.env["MASH_NAME_RULE_REQUIRED"] = "true"
	h.env["MASH_NAME_RULE_FILE"] = writeRule(t, h.dir, "name-rule.json", configAMashctlRule)
	if _, errb, code := h.run("add", "fresh", "--zone", "green"); code == 0 || !strings.Contains(errb, `name rule "no-cc"`) {
		t.Fatalf("required with configuration A: %d %q", code, errb)
	}
	// The control: the same requirement with a file that refuses nothing starts and adds.
	h.env["MASH_NAME_RULE_FILE"] = writeRule(t, h.dir, "permissive.json", configAMashctlEmpty)
	if out, errb, code := h.run("add", "fresh", "--zone", "green"); code != 0 {
		t.Fatalf("required with a permissive file: %d %q %q", code, out, errb)
	}
}

// The rule is read from MASH_NAME_RULE_FILE, and the environment wins over name_rule_file in config.json, in both
// directions: a permissive config.json does not weaken an environment that names configuration A, and a
// configuration-A config.json does not override a permissive environment.
func TestConfigAMashctlRuleFileEnvironmentWinsOverConfigJSON(t *testing.T) {
	h := newAddEnv(t, addPolicy)
	fakeKeyRevoked(t, false, nil)
	a := writeRule(t, h.dir, "rule-a.json", configAMashctlRule)
	permissive := writeRule(t, h.dir, "rule-none.json", configAMashctlEmpty)

	// config.json names the permissive file; the environment names configuration A: refused.
	writeRule(t, h.dir, "config.json", `{"name_rule_file":"`+permissive+`"}`)
	h.env["MASH_NAME_RULE_FILE"] = a
	if _, errb, code := h.run("add", "fresh", "--zone", "green"); code == 0 || !strings.Contains(errb, `name rule "no-cc"`) {
		t.Fatalf("environment configuration A over a permissive config.json: %d %q", code, errb)
	}
	h.assertNothingCreated("fresh")

	// config.json alone names configuration A: refused (the file key is read).
	delete(h.env, "MASH_NAME_RULE_FILE")
	writeRule(t, h.dir, "config.json", `{"name_rule_file":"`+a+`"}`)
	if _, errb, code := h.run("add", "fresh", "--zone", "green"); code == 0 || !strings.Contains(errb, `name rule "no-cc"`) {
		t.Fatalf("configuration A from config.json alone: %d %q", code, errb)
	}

	// config.json names configuration A; the environment names the permissive file: the environment wins, add succeeds.
	h.env["MASH_NAME_RULE_FILE"] = permissive
	if out, errb, code := h.run("add", "fresh", "--zone", "green"); code != 0 {
		t.Fatalf("a permissive environment over configuration A in config.json: %d %q %q", code, out, errb)
	}
}
