package main

import (
	"bytes"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/bogworks/mash/internal/testenv"
)

// Mash must be fully usable with only NATS, its SAP and its runtime dependencies: no Claude or other agent-orchestration
// config. These tests prove it through the built binary, run with a scrubbed environment.

var foreignTools = []string{"range", "herdr", "claude"}

// forbiddenWords splits a comma-separated word list: trimmed, lowercased, empty entries dropped.
func forbiddenWords(list string) []string {
	var ws []string
	for _, w := range strings.Split(list, ",") {
		if w = strings.ToLower(strings.TrimSpace(w)); w != "" {
			ws = append(ws, w)
		}
	}
	return ws
}

// allForeignTools is the neutral list plus the words in MASH_FORBIDDEN_WORDS (set by the maintainers, so the names
// they stand for need not appear in the tree).
func allForeignTools() []string {
	return append(append([]string{}, foreignTools...), forbiddenWords(os.Getenv("MASH_FORBIDDEN_WORDS"))...)
}

// scrubbedEnv is the whole environment of a standalone subprocess: an empty PATH directory, an empty HOME, and the
// MASH_* variables the test sets, nothing else.
func scrubbedEnv(t *testing.T, mash map[string]string) []string {
	t.Helper()
	pathDir := t.TempDir() // empty: nothing resolves on it
	env := []string{"PATH=" + pathDir, "HOME=" + t.TempDir()}
	for k, v := range mash {
		if !strings.HasPrefix(k, "MASH_") {
			t.Fatalf("scrubbedEnv: %s is not a MASH_ variable", k)
		}
		env = append(env, k+"="+v)
	}
	return env
}

func assertToolsAbsent(t *testing.T, env []string) {
	t.Helper()
	var path string
	for _, kv := range env {
		if v, ok := strings.CutPrefix(kv, "PATH="); ok {
			path = v
		}
		for _, bad := range []string{"RANGE_", "HERDR_", "CLAUDE"} {
			if strings.HasPrefix(kv, bad) {
				t.Fatalf("environment leaks %s", kv)
			}
		}
	}
	for _, tool := range allForeignTools() {
		if _, err := os.Stat(filepath.Join(path, tool)); err == nil {
			t.Fatalf("%s is resolvable on the scrubbed PATH %s", tool, path)
		}
	}
}

func mashRun(t *testing.T, bin string, env []string, stdin string, args ...string) (string, int) {
	t.Helper()
	cmd := exec.Command(bin, args...)
	cmd.Env = env
	cmd.Stdin = strings.NewReader(stdin)
	var out bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &out
	err := cmd.Run()
	code := 0
	if ee, ok := err.(*exec.ExitError); ok {
		code = ee.ExitCode()
	} else if err != nil {
		t.Fatalf("mash %v: %v", args, err)
	}
	return out.String(), code
}

func mustRun(t *testing.T, bin string, env []string, args ...string) string {
	t.Helper()
	out, code := mashRun(t, bin, env, "", args...)
	if code != 0 {
		t.Fatalf("mash %s: exit %d\n%s", strings.Join(args, " "), code, out)
	}
	return out
}

// TestStandaloneOnlyNATS drives the real binary, from provisioning to a reply, with nothing but NATS reachable.
func TestStandaloneOnlyNATS(t *testing.T) {
	bin := mashBinary(t)
	e := testenv.Start(t)

	base := map[string]string{"MASH_URL": e.URL, "MASH_OWNER": e.Owner}
	adminEnv := scrubbedEnv(t, map[string]string{"MASH_URL": e.URL, "MASH_OWNER": e.Owner, "MASH_ADMIN_CREDS": e.AdminCreds, "MASH_INSECURE_LOOPBACK": "1"})
	assertToolsAbsent(t, adminEnv)
	mustRun(t, bin, adminEnv, "admin", "init")
	envs := map[string][]string{}
	for _, n := range []string{"alpha", "beta"} {
		creds, user, err := e.MintEndpoint(n)
		if err != nil {
			t.Fatal(err)
		}
		out := mustRun(t, bin, adminEnv, "admin", "provision", n, "--user", user, "--account", e.Account,
			"--role", "tester-"+n, "--host", "standalone")
		if !strings.Contains(out, "provisioned "+n) {
			t.Fatalf("provision %s: %s", n, out)
		}
		m := map[string]string{"MASH_NAME": n, "MASH_CREDS": creds}
		for k, v := range base {
			m[k] = v
		}
		envs[n] = scrubbedEnv(t, m)
		assertToolsAbsent(t, envs[n])
	}

	out := mustRun(t, bin, envs["alpha"], "send", "beta", "hello", "from", "alpha")
	if !strings.Contains(out, "sent #1 from alpha to beta") {
		t.Fatalf("send: %s", out)
	}
	inbox := mustRun(t, bin, envs["beta"], "inbox")
	if !strings.Contains(inbox, "alpha") || !strings.Contains(inbox, "hello from alpha") {
		t.Fatalf("inbox: %s", inbox)
	}
	read := mustRun(t, bin, envs["beta"], "read", "1")
	if !strings.Contains(read, "[verified]") || !strings.Contains(read, "alpha") || !strings.Contains(read, "hello from alpha") {
		t.Fatalf("read lacks a verified sender: %s", read)
	}

	who := mustRun(t, bin, envs["alpha"], "who", "--long")
	for _, want := range []string{"alpha", "beta", "tester-alpha", "tester-beta"} {
		if !strings.Contains(who, want) {
			t.Fatalf("who lacks %q: %s", want, who)
		}
	}

	// The reply round trip: beta answers, alpha reads it, verified.
	out = mustRun(t, bin, envs["beta"], "send", "alpha", "reply", "from", "beta")
	if !strings.Contains(out, "sent #1 from beta to alpha") {
		t.Fatalf("reply send: %s", out)
	}
	read = mustRun(t, bin, envs["alpha"], "read", "1")
	if !strings.Contains(read, "[verified]") || !strings.Contains(read, "beta") || !strings.Contains(read, "reply from beta") {
		t.Fatalf("reply read: %s", read)
	}
}

// TestStandaloneEnvOnlyConfig runs with no config.json and no fleet config: the environment alone configures mash.
func TestStandaloneEnvOnlyConfig(t *testing.T) {
	bin := mashBinary(t)
	e := testenv.Start(t)
	adminEnv := scrubbedEnv(t, map[string]string{"MASH_URL": e.URL, "MASH_OWNER": e.Owner, "MASH_ADMIN_CREDS": e.AdminCreds, "MASH_INSECURE_LOOPBACK": "1"})
	mustRun(t, bin, adminEnv, "admin", "init")
	creds, user, err := e.MintEndpoint("solo")
	if err != nil {
		t.Fatal(err)
	}
	mustRun(t, bin, adminEnv, "admin", "provision", "solo", "--user", user, "--account", e.Account)

	env := scrubbedEnv(t, map[string]string{"MASH_URL": e.URL, "MASH_OWNER": e.Owner, "MASH_NAME": "solo", "MASH_CREDS": creds})
	assertToolsAbsent(t, env)
	for _, kv := range env {
		if v, ok := strings.CutPrefix(kv, "HOME="); ok {
			if _, err := os.Stat(filepath.Join(v, ".config")); err == nil {
				t.Fatalf("HOME %s already has a config directory", v)
			}
		}
	}
	if out := mustRun(t, bin, env, "send", "solo", "note", "to", "self"); !strings.Contains(out, "sent #1 from solo to solo") {
		t.Fatalf("send: %s", out)
	}
	if out := mustRun(t, bin, env, "who"); !strings.Contains(out, "solo") {
		t.Fatalf("who: %s", out)
	}
	// Without the environment and with no config.json, mash says what is missing, rather than reaching for a tool.
	bare := scrubbedEnv(t, nil)
	if out, code := mashRun(t, bin, bare, "", "who"); code == 0 || !strings.Contains(out, "MASH_NAME") {
		t.Fatalf("who with no configuration: exit %d, %s", code, out)
	}
}

// TestNoForeignToolInSource is a simple, honest check: no non-test Go file under cmd/ or internal/ runs, looks up or
// imports a foreign tool or claude.
func TestNoForeignToolInSource(t *testing.T) {
	root := filepath.Join("..", "..")
	fset := token.NewFileSet()
	checked := 0
	for _, top := range []string{"cmd", "internal"} {
		err := filepath.WalkDir(filepath.Join(root, top), func(p string, d fs.DirEntry, err error) error {
			if err != nil || d.IsDir() || !strings.HasSuffix(p, ".go") || strings.HasSuffix(p, "_test.go") {
				return err
			}
			f, err := parser.ParseFile(fset, p, nil, 0)
			if err != nil {
				return err
			}
			checked++
			for _, imp := range f.Imports {
				path, _ := strconv.Unquote(imp.Path.Value)
				if hasForeign(path) {
					t.Errorf("%s imports %s", p, path)
				}
			}
			ast.Inspect(f, func(n ast.Node) bool {
				call, ok := n.(*ast.CallExpr)
				if !ok {
					return true
				}
				sel, ok := call.Fun.(*ast.SelectorExpr)
				if !ok {
					return true
				}
				switch sel.Sel.Name {
				case "Command", "CommandContext", "LookPath", "StartProcess":
					for _, a := range call.Args {
						if lit, ok := a.(*ast.BasicLit); ok && lit.Kind == token.STRING {
							if s, _ := strconv.Unquote(lit.Value); hasForeign(filepath.Base(s)) {
								t.Errorf("%s: %s(%s) names a foreign tool", p, sel.Sel.Name, lit.Value)
							}
						}
					}
				}
				return true
			})
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	if checked < 20 {
		t.Fatalf("only %d source files checked: wrong root?", checked)
	}
}

func hasForeign(s string) bool {
	return hasForeignIn(s, forbiddenWords(os.Getenv("MASH_FORBIDDEN_WORDS")))
}

// hasForeignIn reports whether s is a foreign tool: the neutral list (a whole name or a last path element), or any of
// extra as a whole path component.
func hasForeignIn(s string, extra []string) bool {
	s = strings.ToLower(s)
	for _, tool := range foreignTools {
		if s == tool || strings.HasSuffix(s, "/"+tool) {
			return true
		}
	}
	for _, w := range extra {
		for _, c := range strings.Split(s, "/") {
			if c == w {
				return true
			}
		}
	}
	return strings.Contains(s, "herdr")
}

func TestForbiddenWordsHelpers(t *testing.T) {
	if got := forbiddenWords(" Zebra, ,quokka,,"); len(got) != 2 || got[0] != "zebra" || got[1] != "quokka" {
		t.Errorf("forbiddenWords = %q", got)
	}
	if got := forbiddenWords(""); len(got) != 0 {
		t.Errorf("empty list gave %q", got)
	}
	x := []string{"zebra"}
	for s, want := range map[string]bool{"zebra": true, "example.com/Zebra/pkg": true, "/usr/bin/zebra": true, "zebras": false, "pkg/okapi": false} {
		if got := hasForeignIn(s, x); got != want {
			t.Errorf("hasForeignIn(%q) = %v, want %v", s, got, want)
		}
	}
	if hasForeignIn("zebra", nil) {
		t.Error("no extra words must not match")
	}
}
