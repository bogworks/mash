package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bogworks/mash/internal/policy"
)

// policyFiles copies the policy package's golden files into a temp dir and returns it.
func policyFiles(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	for _, n := range []string{"solo.yaml", "enterprise.yaml"} {
		b, err := os.ReadFile(filepath.Join("..", "..", "internal", "policy", "testdata", n))
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, n), b, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func write(t *testing.T, dir, name, body string) string {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

// policyRun runs mashctl with only the given environment: no server, no admin credentials.
func policyRun(t *testing.T, env map[string]string, args ...string) (string, string, int) {
	t.Helper()
	if env == nil {
		env = map[string]string{}
	}
	if env["HOME"] == "" {
		env["HOME"] = t.TempDir()
	}
	var out, errb bytes.Buffer
	code := runEnv(args, func(k string) string { return env[k] }, &out, &errb)
	return out.String(), errb.String(), code
}

func TestPolicyCheck(t *testing.T) {
	dir := policyFiles(t)
	out, errb, code := policyRun(t, nil, "policy", "check", "--policy", filepath.Join(dir, "solo.yaml"))
	if code != 0 || out != "check: OK - 3 endpoints, 1 rules\n" || errb != "" {
		t.Fatalf("solo: %d %q %q", code, out, errb)
	}
	// MASH_POLICY names the file when the flag is absent
	out, _, code = policyRun(t, map[string]string{"MASH_POLICY": filepath.Join(dir, "enterprise.yaml")}, "policy", "check")
	if code != 0 || !strings.HasPrefix(out, "check: OK - ") {
		t.Fatalf("env: %d %q", code, out)
	}
	// the flag beats the environment
	out, _, code = policyRun(t, map[string]string{"MASH_POLICY": filepath.Join(dir, "missing.yaml")}, "policy", "check", "--policy="+filepath.Join(dir, "solo.yaml"))
	if code != 0 || !strings.Contains(out, "3 endpoints") {
		t.Fatalf("flag over env: %d %q", code, out)
	}
	// <config dir>/policy.yaml is the default
	cfgDir := t.TempDir()
	os.Rename(filepath.Join(dir, "solo.yaml"), filepath.Join(cfgDir, "policy.yaml"))
	out, _, code = policyRun(t, map[string]string{"MASH_CONFIG_DIR": cfgDir}, "policy", "check")
	if code != 0 || !strings.Contains(out, "3 endpoints") {
		t.Fatalf("config dir: %d %q", code, out)
	}
	// a missing file
	_, errb, code = policyRun(t, map[string]string{"MASH_CONFIG_DIR": t.TempDir()}, "policy", "check")
	if code != 1 || !strings.Contains(errb, "policy.yaml") {
		t.Fatalf("missing: %d %q", code, errb)
	}
}

func TestPolicyCheckBrokenAndWarnings(t *testing.T) {
	dir := t.TempDir()
	broken := write(t, dir, "broken.yaml", "default: allow\nendpoints:\n  a:\n    labels: {net: x}\nrules:\n  - name: r\n    action: maybe\n    from: {}\n    to: {}\nbogus: 1\n")
	out, errb, code := policyRun(t, nil, "policy", "check", "--policy", broken)
	if code != 1 || !strings.Contains(out+errb, "line 10") {
		t.Fatalf("unknown field: %d %q %q", code, out, errb)
	}
	invalid := write(t, dir, "invalid.yaml", "default: sometimes\nendpoints:\n  a: {}\nrules:\n  - name: r\n    action: maybe\n    from: {}\n    to: {}\n")
	out, _, code = policyRun(t, nil, "policy", "check", "--policy", invalid)
	if code != 1 || strings.Count(out, "error: ") != 2 || !strings.Contains(out, "error: default: ") || !strings.Contains(out, "maybe") {
		t.Fatalf("invalid: %d %q", code, out)
	}
	warn := write(t, dir, "warn.yaml", "default: allow\nendpoints:\n  a:\n    labels: {net: x}\nrules:\n  - name: ghost\n    action: deny\n    from: {nosuch: label}\n    to: {}\n    allow_empty: true\n")
	out, _, code = policyRun(t, nil, "policy", "check", "--policy", warn)
	if code != 0 || !strings.HasPrefix(out, "warning: ") || !strings.Contains(out, "ghost") || !strings.HasSuffix(out, "check: OK - 1 endpoints, 1 rules\n") {
		t.Fatalf("warning: %d %q", code, out)
	}
	// without allow_empty the same deny rule is an error naming the way out
	noEmpty := write(t, dir, "noempty.yaml", "default: allow\nendpoints:\n  a:\n    labels: {net: x}\nrules:\n  - name: ghost\n    action: deny\n    from: {nosuch: label}\n    to: {}\n")
	out, _, code = policyRun(t, nil, "policy", "check", "--policy", noEmpty)
	if code != 1 || strings.Count(out, "error: ") != 1 || !strings.Contains(out, "rule ghost (line 6)") || !strings.Contains(out, "set allow_empty: true on the rule if the group is intentionally empty") {
		t.Fatalf("deny selecting nobody: %d %q", code, out)
	}
}

func TestPolicyTest(t *testing.T) {
	dir := policyFiles(t)
	out, _, code := policyRun(t, nil, "policy", "test", "--policy", filepath.Join(dir, "solo.yaml"))
	want := "PASS coder->reviewer expect allow\nPASS coder->deploy-bot expect deny\nPASS deploy-bot->coder expect allow\n"
	if code != 0 || out != want {
		t.Fatalf("solo: %d %q", code, out)
	}
	bad := write(t, dir, "bad.yaml", "default: allow\nendpoints:\n  a: {}\n  b: {}\nrules:\n  - {name: no-b, action: deny, from: {}, to: {}}\ntests:\n  - {from: a, to: b, expect: allow}\n  - {from: a, to: b, expect: deny}\n")
	out, _, code = policyRun(t, nil, "policy", "test", "--policy", bad)
	x, _ := policy.Explain(mustParse(t, bad), "a", "b")
	wantFail := "FAIL a->b expect allow got deny: " + x.Text + "\nPASS a->b expect deny\n"
	if code != 1 || out != wantFail {
		t.Fatalf("fail: %d %q want %q", code, out, wantFail)
	}
	none := write(t, dir, "none.yaml", "default: allow\nendpoints:\n  a: {}\n")
	out, _, code = policyRun(t, nil, "policy", "test", "--policy", none)
	if code != 0 || out != "test: no tests in the file\n" {
		t.Fatalf("none: %d %q", code, out)
	}
}

func mustParse(t *testing.T, path string) *policy.File {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	f, err := policy.Parse(b)
	if err != nil {
		t.Fatal(err)
	}
	return f
}

// A policy file that carries a zone is refused by check, naming ADR 0091 and what to remove.
func TestPolicyCheckRefusesAZone(t *testing.T) {
	p := write(t, t.TempDir(), "zoned.yaml", "subject_label: site\nendpoints:\n  a:\n    labels: {zone: x}\n")
	env := map[string]string{"MASH_OWNER": "acme"}
	for _, args := range [][]string{{"policy", "check", "--policy", p}, {"policy", "test", "--policy", p}} {
		out, errb, code := policyRun(t, env, args...)
		if code == 0 || !strings.Contains(out+errb, "ADR 0091") || !strings.Contains(out+errb, "remove") {
			t.Fatalf("%v: %d %q %q", args, code, out, errb)
		}
	}
}

func TestPolicyUsageAndRouting(t *testing.T) {
	// an unknown verb, a bad argument or no verb is the usage error, exit 2, and the usage lists every verb
	for _, args := range [][]string{{"policy"}, {"policy", "frobnicate"}, {"policy", "explain", "a"}, {"policy", "diff"}, {"policy", "show", "extra"}, {"policy", "check", "--nope"}, {"policy", "check", "extra"}, {"policy", "check", "--policy"}} {
		out, errb, code := policyRun(t, nil, args...)
		if code != 2 || out != "" || !strings.Contains(errb, "usage: mashctl policy check") {
			t.Fatalf("%v: %d %q %q", args, code, out, errb)
		}
	}
	verbs := []string{"policy check", "policy test", "policy apply", "policy drift", "policy repair", "policy show", "policy explain", "policy diff"}
	_, errb, _ := policyRun(t, nil, "policy")
	for _, v := range verbs {
		if !strings.Contains(errb, "mashctl "+v) {
			t.Fatalf("usage lacks %s:\n%s", v, errb)
		}
	}
	out, _, code := policyRun(t, nil, "help")
	if code != 0 {
		t.Fatal(code)
	}
	for _, v := range []string{"policy check", "policy test", "policy drift", "policy repair", "policy show", "policy explain", "policy diff"} {
		if !strings.Contains(out, "mashctl "+v) {
			t.Fatalf("help lacks %s:\n%s", v, out)
		}
	}
}
