package main

import (
	"bytes"
	"context"
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

	"github.com/bogworks/mash/internal/admin"
	"github.com/bogworks/mash/internal/testenv"
)

// Mash must be fully usable with only NATS, its SAP and its runtime dependencies: no Claude or other agent-orchestration
// config. These tests prove it through the built binary, run with a scrubbed environment.

var foreignTools = []string{"claude"}

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
	if _, ok := mash["MASH_DANGEROUSLY_SKIP_NATS_ENCRYPTION"]; !ok {
		// REQ-014: these tests are not about TLS; the override is explicit.
		env = append(env, "MASH_DANGEROUSLY_SKIP_NATS_ENCRYPTION=1")
	}
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
		for _, tool := range allForeignTools() {
			if bad := strings.ToUpper(tool); strings.HasPrefix(kv, bad) {
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
	return withoutPlainWarning(out.String()), code
}

// mashRunSplit is mashRun with standard output and standard error kept apart.
func mashRunSplit(t *testing.T, bin string, env []string, stdin string, args ...string) (stdout, stderr string, code int) {
	t.Helper()
	cmd := exec.Command(bin, args...)
	cmd.Env = env
	cmd.Stdin = strings.NewReader(stdin)
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	err := cmd.Run()
	if ee, ok := err.(*exec.ExitError); ok {
		code = ee.ExitCode()
	} else if err != nil {
		t.Fatalf("mash %v: %v", args, err)
	}
	return out.String(), errb.String(), code
}

// standaloneAdmin opens the admin API on e and creates the buckets: the setup the endpoint verbs of mashctl do.
func standaloneAdmin(t *testing.T, e *testenv.Env) *admin.Admin {
	t.Helper()
	adm, err := admin.New(e.Conn(t, e.AdminCreds), e.Owner)
	if err != nil {
		t.Fatal(err)
	}
	if err := adm.Init(context.Background()); err != nil {
		t.Fatal(err)
	}
	return adm
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

	base := map[string]string{"MASH_SERVER": e.URL}
	adm := standaloneAdmin(t, e)
	envs := map[string][]string{}
	for _, n := range []string{"alpha", "beta"} {
		creds, user, err := e.MintEndpoint(tAddr(n))
		if err != nil {
			t.Fatal(err)
		}
		if _, err := adm.Provision(context.Background(), tAddr(n), user, e.Account, "", admin.Meta{Role: "tester-" + n, Host: "standalone"}); err != nil {
			t.Fatalf("provision %s: %v", n, err)
		}
		m := map[string]string{"MASH_ENDPOINT": e.Owner + "." + testGroup + "." + n, "MASH_CREDS": creds}
		for k, v := range base {
			m[k] = v
		}
		envs[n] = scrubbedEnv(t, m)
		assertToolsAbsent(t, envs[n])
	}

	out := mustRun(t, bin, envs["alpha"], "send", tTo("beta"), "hello from alpha")
	if !strings.Contains(out, "Mail sent (number ") {
		t.Fatalf("send: %s", out)
	}
	inbox := mustRun(t, bin, envs["beta"], "inbox")
	if !strings.Contains(inbox, "alpha") || !strings.Contains(inbox, "hello from alpha") {
		t.Fatalf("inbox: %s", inbox)
	}
	read := mustRun(t, bin, envs["beta"], "read", "1")
	if !strings.Contains(read, "trust:    verified") || !strings.Contains(read, "alpha") || !strings.Contains(read, "hello from alpha") {
		t.Fatalf("read lacks a verified sender: %s", read)
	}

	who := mustRun(t, bin, envs["alpha"], "who", "--wide")
	for _, want := range []string{"alpha", "beta", "tester-alpha", "tester-beta"} {
		if !strings.Contains(who, want) {
			t.Fatalf("who lacks %q: %s", want, who)
		}
	}

	// The reply round trip: beta answers, alpha reads it, verified.
	out = mustRun(t, bin, envs["beta"], "send", tTo("alpha"), "reply from beta")
	if !strings.Contains(out, "Mail sent (number ") {
		t.Fatalf("reply send: %s", out)
	}
	read = mustRun(t, bin, envs["alpha"], "read", "1")
	if !strings.Contains(read, "trust:    verified") || !strings.Contains(read, "beta") || !strings.Contains(read, "reply from beta") {
		t.Fatalf("reply read: %s", read)
	}
}

// TestStandaloneEnvOnlyConfig runs with no config.json and no fleet config: the environment alone configures mash.
func TestStandaloneEnvOnlyConfig(t *testing.T) {
	bin := mashBinary(t)
	e := testenv.Start(t)
	adm := standaloneAdmin(t, e)
	creds, user, err := e.MintEndpoint(tAddr("solo"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := adm.Provision(context.Background(), tAddr("solo"), user, e.Account, "", admin.Meta{}); err != nil {
		t.Fatal(err)
	}

	env := scrubbedEnv(t, map[string]string{"MASH_SERVER": e.URL, "MASH_ENDPOINT": e.Owner + "." + testGroup + ".solo", "MASH_CREDS": creds})
	assertToolsAbsent(t, env)
	for _, kv := range env {
		if v, ok := strings.CutPrefix(kv, "HOME="); ok {
			if _, err := os.Stat(filepath.Join(v, ".config")); err == nil {
				t.Fatalf("HOME %s already has a config directory", v)
			}
		}
	}
	if out := mustRun(t, bin, env, "send", tTo("solo"), "note to self"); !strings.Contains(out, "Mail sent (number ") {
		t.Fatalf("send: %s", out)
	}
	if out := mustRun(t, bin, env, "who"); !strings.Contains(out, "solo") {
		t.Fatalf("who: %s", out)
	}
	// Without the environment and with no config.json, mash says what is missing, rather than reaching for a tool.
	bare := scrubbedEnv(t, nil)
	if out, code := mashRun(t, bin, bare, "", "who"); code != 2 || !strings.Contains(out, "no endpoint is installed here") || strings.Contains(out, "MASH_ENDPOINT") {
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
			for _, msg := range foreignCalls(p, f) {
				t.Error(msg)
			}
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

// claudeCheckFile is the one file that may look up claude and run 2.1.330 (Claude Code) (REQ-017, doctor's claude row).
const claudeCheckFile = "../../cmd/mash/claudecheck.go"

// foreignCalls lists the calls in f that run or look up a foreign tool. In claudeCheckFile only the lookup of
// "claude" (one LookPath) and one Command/CommandContext(ctx, path, "--version") pass, where path is a string parameter
// of the enclosing function (the path the claude lookup returned, handed in); a second of either, a literal or any
// other variable as the command, or any other process call there, is listed.
func foreignCalls(p string, f *ast.File) (bad []string) {
	inClaude := filepath.ToSlash(p) == claudeCheckFile
	lookups, runs := 0, 0
	var stack []ast.Node // the path from the file to the node being visited
	ast.Inspect(f, func(n ast.Node) bool {
		if n == nil {
			stack = stack[:len(stack)-1]
			return true
		}
		stack = append(stack, n)
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
		default:
			return true
		}
		lit := func(e ast.Expr) string {
			if b, ok := e.(*ast.BasicLit); ok && b.Kind == token.STRING {
				s, _ := strconv.Unquote(b.Value)
				return s
			}
			return ""
		}
		// isPathParam: e is an identifier that is a string parameter of the innermost enclosing function.
		isPathParam := func(e ast.Expr) bool {
			id, ok := e.(*ast.Ident)
			if !ok {
				return false
			}
			var ft *ast.FuncType
			for i := len(stack) - 1; i >= 0 && ft == nil; i-- {
				switch fn := stack[i].(type) {
				case *ast.FuncDecl:
					ft = fn.Type
				case *ast.FuncLit:
					ft = fn.Type
				}
			}
			if ft == nil {
				return false
			}
			for _, fld := range ft.Params.List {
				if t, ok := fld.Type.(*ast.Ident); !ok || t.Name != "string" {
					continue
				}
				for _, name := range fld.Names {
					if name.Name == id.Name {
						return true
					}
				}
			}
			return false
		}
		if inClaude {
			last := ""
			if len(call.Args) > 0 {
				last = lit(call.Args[len(call.Args)-1])
			}
			switch {
			case sel.Sel.Name == "LookPath" && len(call.Args) == 1 && last == "claude":
				if lookups++; lookups == 1 {
					return true
				}
			case (sel.Sel.Name == "Command" || sel.Sel.Name == "CommandContext") && last == "--version" && isRun(sel.Sel.Name, call.Args, isPathParam):
				if runs++; runs == 1 {
					return true
				}
			}
			bad = append(bad, p+": "+sel.Sel.Name+" is not the one lookup of claude or the one claude --version")
			return true
		}
		for _, a := range call.Args {
			if s := lit(a); s != "" && hasForeign(filepath.Base(s)) {
				bad = append(bad, p+": "+sel.Sel.Name+"("+strconv.Quote(s)+") names a foreign tool")
			}
		}
		return true
	})
	return bad
}

// isRun reports whether args are (ctx, path, "--version") for CommandContext or (path, "--version") for Command.
func isRun(name string, args []ast.Expr, isPath func(ast.Expr) bool) bool {
	if name == "CommandContext" {
		return len(args) == 3 && isPath(args[1])
	}
	return len(args) == 2 && isPath(args[0])
}

func TestForeignCallsGuardInClaudeCheckFile(t *testing.T) {
	const head = "package main\nimport \"os/exec\"\nfunc f(ctx context.Context, path string, n int) {\n"
	const other = "../../cmd/mash/other.go"
	for _, tc := range []struct {
		name, body, file string
		bad              int
	}{
		{"the lookup and the version run", `exec.LookPath("claude"); exec.CommandContext(ctx, path, "--version")`, claudeCheckFile, 0},
		{"the WaitDelay form", `exec.LookPath("claude"); cmd := exec.CommandContext(ctx, path, "--version"); cmd.WaitDelay = 1; cmd.Output()`, claudeCheckFile, 0},
		{"the plain Command form", `exec.Command(path, "--version")`, claudeCheckFile, 0},
		{"git as a literal", `exec.CommandContext(ctx, "git", "--version")`, claudeCheckFile, 1},
		{"a different variable", `exec.CommandContext(ctx, other, "--version")`, claudeCheckFile, 1},
		{"a non-string parameter", `exec.CommandContext(ctx, n, "--version")`, claudeCheckFile, 1},
		{"a computed command", `exec.CommandContext(ctx, path+"x", "--version")`, claudeCheckFile, 1},
		{"a second run", `exec.LookPath("claude"); exec.CommandContext(ctx, path, "--version"); exec.CommandContext(ctx, path, "--version")`, claudeCheckFile, 1},
		{"another tool", `exec.LookPath("claude"); exec.Command("ssh", "host")`, claudeCheckFile, 1},
		{"another argument", `exec.CommandContext(ctx, path, "mcp", "list")`, claudeCheckFile, 1},
		{"a lookup of another name", `exec.LookPath("git")`, claudeCheckFile, 1},
		{"a start", `os.StartProcess(path, nil, nil)`, claudeCheckFile, 1},
		{"elsewhere claude is refused", `exec.LookPath("claude")`, other, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f, err := parser.ParseFile(token.NewFileSet(), tc.file, head+tc.body+"\n}\n", 0)
			if err != nil {
				t.Fatal(err)
			}
			if got := foreignCalls(tc.file, f); len(got) != tc.bad {
				t.Fatalf("%d calls refused, want %d: %v", len(got), tc.bad, got)
			}
		})
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
	return false
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
