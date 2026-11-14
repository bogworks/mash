package main

import (
	"bytes"
	"encoding/json"
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
	broken := write(t, dir, "broken.yaml", "default: allow\nendpoints:\n  a:\n    labels: {zone: x}\nrules:\n  - name: r\n    action: maybe\n    from: {}\n    to: {}\nbogus: 1\n")
	out, errb, code := policyRun(t, nil, "policy", "check", "--policy", broken)
	if code != 1 || !strings.Contains(out+errb, "line 10") {
		t.Fatalf("unknown field: %d %q %q", code, out, errb)
	}
	invalid := write(t, dir, "invalid.yaml", "default: sometimes\nendpoints:\n  a: {}\nrules:\n  - name: r\n    action: maybe\n    from: {}\n    to: {}\n")
	out, _, code = policyRun(t, nil, "policy", "check", "--policy", invalid)
	if code != 1 || strings.Count(out, "error: ") != 2 || !strings.Contains(out, "error: default: ") || !strings.Contains(out, "maybe") {
		t.Fatalf("invalid: %d %q", code, out)
	}
	warn := write(t, dir, "warn.yaml", "default: allow\nendpoints:\n  a:\n    labels: {zone: x}\nrules:\n  - name: ghost\n    action: deny\n    from: {nosuch: label}\n    to: {}\n    allow_empty: true\n")
	out, _, code = policyRun(t, nil, "policy", "check", "--policy", warn)
	if code != 0 || !strings.HasPrefix(out, "warning: ") || !strings.Contains(out, "ghost") || !strings.HasSuffix(out, "check: OK - 1 endpoints, 1 rules\n") {
		t.Fatalf("warning: %d %q", code, out)
	}
	// without allow_empty the same deny rule is an error naming the way out
	noEmpty := write(t, dir, "noempty.yaml", "default: allow\nendpoints:\n  a:\n    labels: {zone: x}\nrules:\n  - name: ghost\n    action: deny\n    from: {nosuch: label}\n    to: {}\n")
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

func TestPolicyExplain(t *testing.T) {
	dir := policyFiles(t)
	solo := filepath.Join(dir, "solo.yaml")
	f := mustParse(t, solo)
	for _, c := range [][2]string{{"coder", "reviewer"}, {"coder", "deploy-bot"}} {
		x, _ := policy.Explain(f, c[0], c[1])
		out, _, code := policyRun(t, nil, "policy", "explain", c[0], c[1], "--policy", solo)
		if code != 0 || out != x.Text+"\n"+coverageNote+"\n" {
			t.Fatalf("%v: %d %q", c, code, out)
		}
	}
	if x, _ := policy.Explain(f, "coder", "deploy-bot"); x.Allowed {
		t.Fatal("test premise: coder->deploy-bot should be denied")
	}
	_, errb, code := policyRun(t, nil, "policy", "explain", "coder", "nobody", "--policy", solo)
	if code != 2 || !strings.Contains(errb, `"nobody" is not a declared endpoint`) {
		t.Fatalf("unknown: %d %q", code, errb)
	}
	_, errb, code = policyRun(t, nil, "policy", "explain", "coder", "--policy", solo)
	if code != 2 || !strings.Contains(errb, "usage: mashctl policy") {
		t.Fatalf("one arg: %d %q", code, errb)
	}
}

func TestPolicyShow(t *testing.T) {
	dir := policyFiles(t)
	for _, n := range []string{"solo.yaml", "enterprise.yaml"} {
		p := filepath.Join(dir, n)
		c, err := policy.Compile(mustParse(t, p), "acme")
		if err != nil {
			t.Fatal(err)
		}
		env := map[string]string{"MASH_OWNER": "acme"}
		out, errb, code := policyRun(t, env, "policy", "show", "--policy", p)
		if code != 0 || errb != "" || !strings.HasPrefix(out, c.Matrix()) || !strings.Contains(out, "\nzones:\n") ||
			!strings.Contains(out, "wildcard zones") || !strings.Contains(out, "tags per endpoint") ||
			!strings.HasSuffix(out, "\n"+coverageNote+"\n") {
			t.Fatalf("%s text: %d %q %q", n, code, out, errb)
		}
		js, _ := c.JSON()
		out, errb, code = policyRun(t, env, "policy", "show", "--json", "--policy", p)
		if code != 0 || out != string(js) || errb != "" {
			t.Fatalf("%s json: %d %q %q", n, code, out, errb)
		}
		var doc struct{ Covers, Open []string }
		if err := json.Unmarshal([]byte(out), &doc); err != nil || strings.Join(doc.Covers, ",") != "mail" ||
			strings.Join(doc.Open, ",") != "prompt,status" || strings.Contains(out, "note:") {
			t.Fatalf("%s json fields: %v %+v", n, err, doc)
		}
	}
}

func TestPolicyShowNeedsOwner(t *testing.T) {
	p := filepath.Join(policyFiles(t), "solo.yaml")
	out, errb, code := policyRun(t, nil, "policy", "show", "--policy", p)
	if code != 1 || out != "" || !strings.Contains(errb, "no owner") || !strings.Contains(errb, "MASH_OWNER") {
		t.Fatalf("%d %q %q", code, out, errb)
	}
	// the other verbs do not need one
	if _, _, code := policyRun(t, nil, "policy", "check", "--policy", p); code != 0 {
		t.Fatal("check needs no owner")
	}
}

func TestPolicyShowBudget(t *testing.T) {
	p := filepath.Join(policyFiles(t), "enterprise.yaml")
	env := map[string]string{"MASH_OWNER": "acme"}
	out, errb, code := policyRun(t, env, "policy", "show", "--budget", "--policy", p)
	if code != 0 || errb != "" || !strings.Contains(out, "budget (default server limit): account JWT ") || !strings.Contains(out, "worst connect line ") {
		t.Fatalf("%d %q %q", code, out, errb)
	}
	// with --json the budget line must not corrupt the JSON on stdout
	out, errb, code = policyRun(t, env, "policy", "show", "--json", "--budget", "--policy", p)
	if code != 0 || !strings.HasPrefix(out, "{") || strings.Contains(out, "budget (") || !strings.Contains(errb, "budget (default server limit): account JWT") {
		t.Fatalf("json: %d %q %q", code, out, errb)
	}
	// over budget: a hub that may mail 170 peers one by one needs a tag for each, which overflows its user JWT
	var b strings.Builder
	b.WriteString("default: deny\nendpoints:\n  hub:\n    labels: {role: hub}\n")
	var rules strings.Builder
	rules.WriteString("rules:\n")
	for i := 0; i < 170; i++ {
		id := "p" + strings.Repeat("x", 1) + string(rune('a'+i/26/26%26)) + string(rune('a'+i/26%26)) + string(rune('a'+i%26))
		b.WriteString("  " + id + ":\n    labels: {id: " + id + "}\n")
		rules.WriteString("  - {name: r-" + id + ", action: allow, from: {role: hub}, to: {id: " + id + "}}\n")
	}
	big := write(t, t.TempDir(), "big.yaml", b.String()+rules.String())
	out, errb, code = policyRun(t, env, "policy", "show", "--budget", "--policy", big)
	if code != 1 || !strings.Contains(out, "budget (default server limit): account JWT") || !strings.Contains(errb, `sender "hub"`) || !strings.Contains(errb, "allows 4,096") {
		t.Fatalf("over budget: %d out-tail %q err %q", code, out[max(0, len(out)-200):], errb)
	}
}

func TestPolicyUsageAndRouting(t *testing.T) {
	for _, args := range [][]string{{"policy"}, {"policy", "frobnicate"}, {"policy", "check", "--nope"}, {"policy", "check", "extra"}, {"policy", "show", "--policy"}} {
		out, errb, code := policyRun(t, nil, args...)
		if code != 2 || out != "" || !strings.Contains(errb, "usage: mashctl policy check") {
			t.Fatalf("%v: %d %q %q", args, code, out, errb)
		}
	}
	out, _, code := policyRun(t, nil, "help")
	if code != 0 {
		t.Fatal(code)
	}
	for _, v := range []string{"policy check", "policy test", "policy explain", "policy show"} {
		if !strings.Contains(out, "mashctl "+v) {
			t.Fatalf("usage lacks %s:\n%s", v, out)
		}
	}
}
