package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bogworks/mash/internal/admin"
	"github.com/bogworks/mash/internal/testenv"
)

// Configuration A of the naming plan's negative tests (steps 1.10 and 3.1), at the `mash` entry points of REQ-016:
// a rule file that refuses the agent tokens cc and mash. These tests are permanent; none may be skipped or weakened.
// The full table, for every entry point, is in internal/endpoint/namerule_configa_test.go: this file proves that the
// mash commands (start through serve and the CLI, send, prompt, provision through `mash admin`) reach it through the
// same configuration, and that a deployment which requires a rule and supplies none does not start.
//
// Layers, as in the endpoint table: a bare legacy name is the identity (cc, owner, name) and is refused by the rule
// no-cc; a three-token identity with agent mash by no-mash (it stands for the plain name inside a deployment); a
// three-token identity with agent cc (flat or dotted spelling) by sap, which never builds it while the legacy form
// exists, so the command says the key is not an endpoint key; an ordinary agent token is the allowed control.
const (
	configAMashRule = `{"version":1,"rules":[` +
		`{"id":"no-cc","match":{"agent":{"is":"cc"}},"message":"the agent token cc is retired"},` +
		`{"id":"no-mash","match":{"agent":{"is":"mash"}},"message":"the agent token mash is reserved"}]}`
	configAMashEmpty = `{"version":1,"rules":[]}`
)

func configARuleFile(t *testing.T, dir, name, body string) string {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

type configAMashWorld struct {
	e                                  *testenv.Env
	rule                               string
	legacy, mashSender, control, peerE map[string]string // environments
}

func newConfigAMashWorld(t *testing.T) *configAMashWorld {
	t.Helper()
	w := newWorld(t, "a", "legacy")
	e := w.e
	_, control := provisionNewForm(t, e, "claude", "acme", "me")
	_, peer := provisionNewForm(t, e, "claude", "acme", "peer")
	_, mashEnv := provisionNewForm(t, e, "mash", "acme", "me")
	_, _ = provisionNewForm(t, e, "mash", "acme", "peer")
	rule := configARuleFile(t, t.TempDir(), "name-rule.json", configAMashRule)
	with := func(m map[string]string) map[string]string {
		c := map[string]string{}
		for k, v := range m {
			c[k] = v
		}
		c["MASH_NAME_RULE_FILE"] = rule
		return c
	}
	return &configAMashWorld{e: e, rule: rule, legacy: with(w.env["a"]), mashSender: with(mashEnv), control: with(control), peerE: with(peer)}
}

func configARun(env map[string]string, args ...string) (string, int) {
	var out, errb strings.Builder
	code := run(args, envFrom(env), strings.NewReader(""), &out, &errb)
	return out.String() + errb.String(), code
}

// start: the endpoint's own identity, through every command that opens it. A refused identity is refused by the rule
// before anything is read, the command tells whoever ran it, and the control opens.
func TestConfigAMashStart(t *testing.T) {
	w := newConfigAMashWorld(t)
	for _, c := range []struct {
		name string
		env  map[string]string
		want string
	}{
		{"bare legacy name", w.legacy, "no-cc"},
		{"three-token identity with agent mash", w.mashSender, "no-mash"},
		{"control: ordinary agent token", w.control, ""},
	} {
		for _, args := range [][]string{{"inbox"}, {"who"}, {"read", "1"}, {"send", "claude_acme_peer", "hi"}, {"prompt", "claude_acme_peer", "hi"}} {
			t.Run(c.name+"/"+args[0], func(t *testing.T) {
				out, code := configARun(c.env, args...)
				if c.want == "" {
					if strings.Contains(out, "name rule") {
						t.Fatalf("the control was refused: %d %s", code, out)
					}
					return
				}
				if code == 0 || !strings.Contains(out, `name rule "`+c.want+`"`) || !strings.Contains(out, "start") {
					t.Fatalf("a refused identity: %d %s", code, out)
				}
				if strings.Contains(out, "not provisioned") || strings.Contains(out, "no endpoint named") {
					t.Fatalf("the directory was read before the rule: %s", out)
				}
			})
		}
	}
	if out, code := configARun(w.control, "inbox"); code != 0 {
		t.Fatalf("the control inbox: %d %s", code, out)
	}
}

// serve (the MCP server, what a session runs): a refused identity does not start. serve stays up so the session can
// ask why: it exposes only status and version, and says the rule and its message; the control starts with its tools.
func TestConfigAMashServeStart(t *testing.T) {
	w := newConfigAMashWorld(t)
	for _, c := range []struct {
		name string
		env  map[string]string
		want string
	}{
		{"bare legacy name", w.legacy, "no-cc"},
		{"three-token identity with agent mash", w.mashSender, "no-mash"},
	} {
		t.Run(c.name, func(t *testing.T) {
			s := startServe(t, c.env)
			instr := s.initialize(t)["result"].(map[string]any)["instructions"].(string)
			if !strings.Contains(instr, `name rule "`+c.want+`"`) {
				t.Fatalf("the session is not told the rule: %q", instr)
			}
			if names := toolNames(t, s); len(names) != 2 || !names["status"] || !names["version"] {
				t.Fatalf("a refused identity exposes %v, want status and version only", names)
			}
			if !strings.Contains(s.stderr(), `name rule "`+c.want+`"`) {
				t.Fatalf("stderr does not name the rule: %q", s.stderr())
			}
		})
	}
	t.Run("control: ordinary agent token", func(t *testing.T) {
		s := startServe(t, w.control)
		instr := s.initialize(t)["result"].(map[string]any)["instructions"].(string)
		if strings.Contains(instr, "name rule") {
			t.Fatalf("the control was refused: %q", instr)
		}
		if names := toolNames(t, s); !names["send"] || !names["inbox"] {
			t.Fatalf("the control exposes %v", names)
		}
	})
}

// send and prompt: the addressed identity in every form, from an allowed sender. The sender is told, and nothing is
// dropped silently: no refused recipient has mail afterwards.
func TestConfigAMashSendAndPrompt(t *testing.T) {
	w := newConfigAMashWorld(t)
	forms := []struct {
		name, key, want string // want: "no-cc", "no-mash", "sap" (not an endpoint key) or ""
	}{
		{"bare legacy name, no record", "gone", "no-cc"},
		{"bare legacy name, record exists", "legacy", "no-cc"},
		{"three-token flat key with agent cc", "cc_acme_gone", "sap"},
		{"dotted display form with agent cc", "cc.acme.gone", "sap"},
		{"three-token identity with agent mash, no record", "mash_acme_gone", "no-mash"},
		{"three-token identity with agent mash, record exists", "mash_acme_peer", "no-mash"},
		{"control: ordinary agent token", "claude_acme_peer", ""},
	}
	for _, verb := range []string{"send", "prompt"} {
		for _, f := range forms {
			if verb == "prompt" && f.want == "" {
				continue // a live prompt needs a live service: the control is TestConfigAPrompt in internal/endpoint
			}
			t.Run(verb+"/"+f.name, func(t *testing.T) {
				out, code := configARun(w.control, verb, f.key, "hi")
				switch f.want {
				case "sap":
					if code == 0 || strings.Contains(out, "name rule") || !strings.Contains(out, "is not an endpoint key") {
						t.Fatalf("%d %s", code, out)
					}
				case "":
					if code != 0 || strings.Contains(out, "name rule") {
						t.Fatalf("the control was refused: %d %s", code, out)
					}
				default:
					if code == 0 || !strings.Contains(out, `name rule "`+f.want+`"`) || !strings.Contains(out, verb+": the recipient") {
						t.Fatalf("%d %s", code, out)
					}
				}
			})
		}
	}
	// Nothing reached a refused recipient: the stream of mash_acme_peer holds no message.
	if n := pendingOf(t, w.e, "inbox-mash_acme_peer"); n != 0 {
		t.Errorf("a refused recipient has %d messages", n)
	}
	if n := pendingOf(t, w.e, "inbox-legacy"); n != 0 {
		t.Errorf("a refused legacy recipient has %d messages", n)
	}
	if n := pendingOf(t, w.e, "inbox-claude_acme_peer"); n != 1 {
		t.Errorf("the control recipient has %d messages, want 1", n)
	}
}

// provision through `mash admin`: the same configuration, the rule first, nothing created. A three-token identity is
// refused by mash admin itself (errAdminAgentToken, its fail-closed refusal) before the rule is reached.
func TestConfigAMashAdminProvision(t *testing.T) {
	w := newConfigAMashWorld(t)
	admEnv := func(extra map[string]string) map[string]string {
		m := map[string]string{"HOME": t.TempDir(), "MASH_URL": w.e.URL, "MASH_OWNER": w.e.Owner, "MASH_ADMIN_CREDS": w.e.AdminCreds,
			"MASH_INSECURE_LOOPBACK": "1", "MASH_NAME_RULE_FILE": w.rule}
		for k, v := range extra {
			m[k] = v
		}
		return m
	}
	for _, name := range []string{"fresh", "legacy", "mash", "gone"} { // "legacy" exists
		_, user := w.e.Mint(t, name)
		out, code := configARun(admEnv(nil), "admin", "provision", name, "--user", user, "--account", w.e.Account)
		if code == 0 || !strings.Contains(out, `name rule "no-cc"`) || !strings.Contains(out, "the agent token cc is retired") {
			t.Errorf("admin provision %q: %d %s", name, code, out)
		}
		if strings.Contains(out, "already") {
			t.Errorf("the directory was read before the rule: %s", out)
		}
	}
	for _, name := range []string{"cc_acme_gone", "mash_acme_gone", "cc.acme.gone"} {
		_, user := w.e.Mint(t, "x"+strings.NewReplacer("_", "", ".", "").Replace(name))
		out, code := configARun(admEnv(nil), "admin", "provision", name, "--user", user, "--account", w.e.Account)
		if code == 0 || strings.Contains(out, "name rule") || !strings.Contains(out, "endpoint name") {
			t.Errorf("admin provision %q: %d %s", name, code, out)
		}
	}
	for _, tok := range []string{"cc", "mash", "claude"} {
		_, user := w.e.Mint(t, "fresh")
		out, code := configARun(admEnv(map[string]string{"MASH_NAME": "fresh", "MASH_AGENT_TOKEN": tok}), "admin", "provision", "fresh", "--user", user, "--account", w.e.Account)
		if code == 0 || !strings.Contains(out, errAdminAgentToken.Error()) {
			t.Errorf("agent_token %q: %d %s", tok, code, out)
		}
	}
	adm, err := admin.New(w.e.Conn(t, w.e.AdminCreds), w.e.Owner)
	if err != nil {
		t.Fatal(err)
	}
	recs, err := adm.List(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range recs {
		if r.Name == "fresh" || r.Name == "mash" || r.Name == "gone" {
			t.Errorf("a refused provision wrote the record %q", r.Name)
		}
	}
}

// A deployment that sets name_rule_required but supplies no file does not start, at every mash entry point and from
// the environment and from config.json alike.
func TestConfigAMashRequiredWithoutFileRefusesToStart(t *testing.T) {
	w := newConfigAMashWorld(t)
	for _, how := range []string{"environment", "config.json"} {
		env := func() map[string]string {
			m := map[string]string{}
			for k, v := range w.control {
				m[k] = v
			}
			delete(m, "MASH_NAME_RULE_FILE")
			if how == "environment" {
				m["MASH_NAME_RULE_REQUIRED"] = "true"
			} else {
				dir := t.TempDir()
				configARuleFile(t, dir, "config.json", `{"name_rule_required": true}`)
				m["MASH_CONFIG_DIR"] = dir
			}
			return m
		}
		const want = "a name rule is required"
		for _, args := range [][]string{{"inbox"}, {"who"}, {"read", "1"}, {"send", "claude_acme_peer", "hi"}, {"prompt", "claude_acme_peer", "hi"},
			{"admin", "provision", "x", "--user", "U", "--account", "A"}} {
			t.Run(how+"/"+strings.Join(args[:min(2, len(args))], " "), func(t *testing.T) {
				e := env()
				if args[0] == "admin" {
					e["MASH_ADMIN_CREDS"] = w.e.AdminCreds
					delete(e, "MASH_AGENT_TOKEN")
				}
				out, code := configARun(e, args...)
				if code == 0 || !strings.Contains(out, want) {
					t.Fatalf("%d %s", code, out)
				}
			})
		}
		t.Run(how+"/serve", func(t *testing.T) { // serve stays up to say why; it exposes status and version only
			s := startServe(t, env())
			instr := s.initialize(t)["result"].(map[string]any)["instructions"].(string)
			if !strings.Contains(instr, want) {
				t.Fatalf("the session is not told: %q", instr)
			}
			if names := toolNames(t, s); len(names) != 2 || !names["status"] || !names["version"] {
				t.Fatalf("serve without its required rule exposes %v", names)
			}
		})
		t.Run(how+"/status", func(t *testing.T) { // a report: the error is a line of it, never "none set"
			out, _ := configARun(env(), "status")
			if !strings.Contains(out, want) || strings.Contains(out, "name rule: none set") {
				t.Fatalf("%s", out)
			}
		})
		t.Run(how+"/hook", func(t *testing.T) { // a hook never fails a session: it is a no-op and delivers nothing
			out, code := configARun(env(), "hook")
			if code != 0 || strings.Contains(out, "additionalContext") {
				t.Fatalf("%d %s", code, out)
			}
		})
	}
	// A path that is set but empty, or that cannot be read, is an error and not "no rule".
	absent := filepath.Join(t.TempDir(), "absent", "name-rule.json") // under a fresh temp dir, never created
	for name, c := range map[string]struct{ val, want string }{
		"empty":   {"", "MASH_NAME_RULE_FILE is set but empty"},
		"missing": {absent, absent},
	} {
		e := map[string]string{}
		for k, v := range w.control {
			e[k] = v
		}
		delete(e, "MASH_NAME_RULE_FILE")
		lookup := func(k string) (string, bool) {
			if k == "MASH_NAME_RULE_FILE" {
				return c.val, true // set, possibly empty: the lookup says so, as the process environment does
			}
			return e[k], e[k] != ""
		}
		var out, errb strings.Builder
		code := runLookup(context.Background(), []string{"inbox"}, envFrom(e), lookup, strings.NewReader(""), &out, &errb)
		if code == 0 || !strings.Contains(errb.String(), c.want) {
			t.Errorf("%s rule file: %d %q %q", name, code, out.String(), errb.String())
		}
	}
}

// The rule is read from MASH_NAME_RULE_FILE, and the environment wins over name_rule_file in config.json, in both
// directions.
func TestConfigAMashRuleFileEnvironmentWinsOverConfigJSON(t *testing.T) {
	w := newConfigAMashWorld(t)
	dir := t.TempDir()
	permissive := configARuleFile(t, dir, "rule-none.json", configAMashEmpty)
	a := w.rule
	base := func(ruleEnv string) map[string]string {
		m := map[string]string{}
		for k, v := range w.legacy { // the legacy endpoint a: only a permissive rule lets it start
			m[k] = v
		}
		delete(m, "MASH_NAME_RULE_FILE")
		m["MASH_CONFIG_DIR"] = dir
		if ruleEnv != "" {
			m["MASH_NAME_RULE_FILE"] = ruleEnv
		}
		return m
	}
	// config.json names the permissive file; the environment names configuration A: refused, and status shows A.
	configARuleFile(t, dir, "config.json", `{"name_rule_file":"`+permissive+`"}`)
	if out, code := configARun(base(a), "inbox"); code == 0 || !strings.Contains(out, `name rule "no-cc"`) {
		t.Fatalf("environment configuration A over a permissive config.json: %d %s", code, out)
	}
	if out, _ := configARun(base(a), "status"); !strings.Contains(out, "name rule: "+a+" ") {
		t.Fatalf("status does not show the environment's file:\n%s", out)
	}
	// config.json alone names configuration A: refused.
	configARuleFile(t, dir, "config.json", `{"name_rule_file":"`+a+`"}`)
	if out, code := configARun(base(""), "inbox"); code == 0 || !strings.Contains(out, `name rule "no-cc"`) {
		t.Fatalf("configuration A from config.json alone: %d %s", code, out)
	}
	// config.json names configuration A; the environment names the permissive file: the environment wins.
	if out, code := configARun(base(permissive), "inbox"); code != 0 {
		t.Fatalf("a permissive environment over configuration A in config.json: %d %s", code, out)
	}
	if out, _ := configARun(base(permissive), "status"); !strings.Contains(out, "name rule: "+permissive+" ") {
		t.Fatalf("status does not show the environment's file:\n%s", out)
	}
}
