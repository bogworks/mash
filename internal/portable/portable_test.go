package portable

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"
)

// Rule names, as written in baseline.txt.
const (
	rulePersonal = "R1-personal-tokens"
	ruleHomeTmp  = "R2-home-and-tmp"
	ruleOrgURL   = "R3-foreign-org-url"
	ruleTimeout  = "R4-long-timeout"
	ruleShell    = "R5-shell"
)

// longTimeout is the bound above which a literal duration must be a named package-level setting.
const longTimeout = 30 * time.Second

const updateEnv = "MASH_PORTABLE_UPDATE"

var (
	// personalRE matches words that name a person, a machine or a private project. "bogworks" is the public org and is
	// deliberately not here.
	personalRE = regexp.MustCompile(`(?i)randyb|randybias|\brlb\b|homerange|rangehand|plethorax|dev1\b|thaos|bf1\b|devsup|rdev\b|mire's|ms-?wisp`)
	// homePathRE matches a literal that starts at a user's home or at a temp or home directory.
	homePathRE = regexp.MustCompile(`^(~/|/tmp/|/home/|/Users/)`)
	// githubOrgRE captures the org of a github.com URL or an scp-style git@github.com:org remote.
	githubOrgRE = regexp.MustCompile(`github\.com[/:]([A-Za-z0-9_.-]+)`)
)

// allowedOrgs are the GitHub orgs a literal may name. Lower case.
var allowedOrgs = map[string]bool{
	"bogworks": true, // the public org that owns mash
	// No other org is allowed: the nats-io and golang URLs in the tree are import paths, which are not scanned.
}

// homeAllowed are the packages that resolve the user's directories and so may ask for the home directory.
var homeAllowed = []string{"internal/config/", "internal/claudesettings/"}

// scanRoots are the trees scanned for Go and shell sources.
var scanRoots = []string{"cmd", "internal", "scripts"}

// hit is one violation at a position.
type hit struct {
	rule string
	path string // repo-relative, slash-separated
	line int
	text string
}

type key struct{ rule, path string }

func repoRoot(t testing.TB) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("go.mod not found above the test directory")
		}
		dir = parent
	}
}

// skipDir reports whether a directory is outside the scan: VCS and dependency trees, dot-dirs, testdata, and the
// directories of test fixtures and of this package.
func skipDir(rel, name string) bool {
	if strings.HasPrefix(name, ".") && rel != "." {
		return true
	}
	switch name {
	case "node_modules", "testdata":
		return true
	}
	return rel == "internal/testenv" || rel == "internal/portable"
}

func isGoSource(rel string) bool {
	return strings.HasSuffix(rel, ".go") && !strings.HasSuffix(rel, "_test.go")
}

func hasShebang(path string) bool {
	b, err := os.ReadFile(path)
	return err == nil && strings.HasPrefix(string(b), "#!")
}

func isShellFile(abs, rel string) bool {
	if !strings.HasPrefix(rel, "scripts/") {
		return false
	}
	ext := filepath.Ext(rel)
	if ext == ".sh" {
		return true
	}
	return ext == "" && hasShebang(abs)
}

func isWorkflow(rel string) bool {
	return strings.HasPrefix(rel, ".github/workflows/") && (strings.HasSuffix(rel, ".yml") || strings.HasSuffix(rel, ".yaml"))
}

// scan walks the tree and returns every hit.
func scan(t testing.TB, root string) []hit {
	t.Helper()
	var hits []hit
	walk := func(start string, want func(abs, rel string) bool, fn func(abs, rel string)) {
		base := filepath.Join(root, start)
		if _, err := os.Stat(base); err != nil {
			return
		}
		err := filepath.WalkDir(base, func(p string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			rel, _ := filepath.Rel(root, p)
			rel = filepath.ToSlash(rel)
			if d.IsDir() {
				if p != base && skipDir(rel, d.Name()) {
					return filepath.SkipDir
				}
				return nil
			}
			if want(p, rel) {
				fn(p, rel)
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	for _, r := range scanRoots {
		walk(r, func(_, rel string) bool { return isGoSource(rel) }, func(abs, rel string) {
			src, err := os.ReadFile(abs)
			if err != nil {
				t.Fatal(err)
			}
			h, err := scanGo(rel, src)
			if err != nil {
				t.Fatalf("%s: %v", rel, err)
			}
			hits = append(hits, h...)
		})
		walk(r, isShellFile, func(abs, rel string) { hits = append(hits, scanShellFile(t, abs, rel)...) })
	}
	walk(".github/workflows", func(_, rel string) bool { return isWorkflow(rel) }, func(abs, rel string) {
		hits = append(hits, scanShellFile(t, abs, rel)...)
	})
	return hits
}

func scanShellFile(t testing.TB, abs, rel string) []hit {
	b, err := os.ReadFile(abs)
	if err != nil {
		t.Fatal(err)
	}
	return scanShell(rel, string(b))
}

// scanShell applies R1 to the non-comment lines of a shell or workflow file.
func scanShell(rel, text string) []hit {
	var hits []hit
	for i, line := range strings.Split(text, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "#") {
			continue
		}
		if personalRE.MatchString(line) {
			hits = append(hits, hit{ruleShell, rel, i + 1, strings.TrimSpace(line)})
		}
	}
	return hits
}

func homeAllowedIn(rel string) bool {
	for _, p := range homeAllowed {
		if strings.HasPrefix(rel, p) {
			return true
		}
	}
	return false
}

// foreignOrg returns the first org in s that is not allowed, or "".
func foreignOrg(s string) string {
	for _, m := range githubOrgRE.FindAllStringSubmatch(s, -1) {
		if org := strings.ToLower(m[1]); !allowedOrgs[org] {
			return m[1]
		}
	}
	return ""
}

// scanGo parses one Go source file and applies R1 to R4. Comments are never inspected: only string literals and
// calls and binary expressions in the syntax tree.
func scanGo(rel string, src []byte) ([]hit, error) {
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, rel, src, parser.SkipObjectResolution)
	if err != nil {
		return nil, err
	}
	var hits []hit
	add := func(rule string, pos token.Pos, text string) {
		hits = append(hits, hit{rule, rel, fset.Position(pos).Line, text})
	}
	imports := map[*ast.BasicLit]bool{}
	for _, im := range f.Imports {
		imports[im.Path] = true
	}
	// visit inspects n; named is true inside the initialiser of a package-level const or var, where a long duration is
	// a documented default and R4 does not apply.
	visit := func(n ast.Node, named bool) {
		ast.Inspect(n, func(n ast.Node) bool {
			switch x := n.(type) {
			case *ast.BasicLit:
				if x.Kind != token.STRING || imports[x] {
					return true
				}
				s, err := strconv.Unquote(x.Value)
				if err != nil {
					return true
				}
				if personalRE.MatchString(s) {
					add(rulePersonal, x.Pos(), s)
				}
				if homePathRE.MatchString(s) {
					add(ruleHomeTmp, x.Pos(), s)
				}
				if org := foreignOrg(s); org != "" {
					add(ruleOrgURL, x.Pos(), s)
				}
			case *ast.CallExpr:
				name := selector(x.Fun)
				if !homeAllowedIn(rel) {
					if name == "os.UserHomeDir" {
						add(ruleHomeTmp, x.Pos(), name+"()")
					}
					if name == "os.Getenv" && len(x.Args) == 1 && stringLit(x.Args[0]) == "HOME" {
						add(ruleHomeTmp, x.Pos(), `os.Getenv("HOME")`)
					}
				}
				if !named && name == "time.ParseDuration" && len(x.Args) == 1 {
					if d, err := time.ParseDuration(stringLit(x.Args[0])); err == nil && d > longTimeout {
						add(ruleTimeout, x.Pos(), "time.ParseDuration("+strconv.Quote(stringLit(x.Args[0]))+")")
					}
				}
			case *ast.BinaryExpr:
				if !named && x.Op == token.MUL && isLongUnit(x) {
					add(ruleTimeout, x.Pos(), "inline "+selector(unitOperand(x))+" multiple")
				}
			}
			return true
		})
	}
	for _, d := range f.Decls {
		gd, ok := d.(*ast.GenDecl)
		named := ok && (gd.Tok == token.CONST || gd.Tok == token.VAR)
		visit(d, named)
	}
	sort.SliceStable(hits, func(i, j int) bool { return hits[i].line < hits[j].line })
	return hits, nil
}

// selector renders a pkg.Name selector expression, or "" for anything else.
func selector(e ast.Expr) string {
	if s, ok := e.(*ast.SelectorExpr); ok {
		if id, ok := s.X.(*ast.Ident); ok {
			return id.Name + "." + s.Sel.Name
		}
	}
	return ""
}

func stringLit(e ast.Expr) string {
	if b, ok := e.(*ast.BasicLit); ok && b.Kind == token.STRING {
		if s, err := strconv.Unquote(b.Value); err == nil {
			return s
		}
	}
	return ""
}

func unitOperand(x *ast.BinaryExpr) ast.Expr {
	for _, e := range []ast.Expr{x.X, x.Y} {
		if n := selector(e); n == "time.Minute" || n == "time.Hour" {
			return e
		}
	}
	return nil
}

// isLongUnit reports whether x is <integer literal> * time.Minute or time.Hour, in either order.
func isLongUnit(x *ast.BinaryExpr) bool {
	isInt := func(e ast.Expr) bool {
		b, ok := e.(*ast.BasicLit)
		return ok && b.Kind == token.INT
	}
	return unitOperand(x) != nil && (isInt(x.X) || isInt(x.Y))
}

func count(hits []hit) map[key]int {
	m := map[key]int{}
	for _, h := range hits {
		m[key{h.rule, h.path}]++
	}
	return m
}

// baselineRow is one baseline.txt row with the reason comment above its group.
type baselineRow struct {
	key
	n      int
	reason string
}

// parseBaseline reads rows "<rule> <path> <count>", in sorted order, each under a non-empty "# reason" comment.
func parseBaseline(text string) ([]baselineRow, error) {
	var rows []baselineRow
	reason := ""
	for i, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		switch {
		case line == "":
			continue
		case strings.HasPrefix(line, "#"):
			reason = strings.TrimSpace(strings.TrimPrefix(line, "#"))
			continue
		}
		f := strings.Fields(line)
		if len(f) != 3 {
			return nil, fmt.Errorf("baseline.txt:%d: want \"<rule> <path> <count>\", got %q", i+1, line)
		}
		n, err := strconv.Atoi(f[2])
		if err != nil || n < 1 {
			return nil, fmt.Errorf("baseline.txt:%d: count must be a positive integer, got %q", i+1, f[2])
		}
		if reason == "" {
			return nil, fmt.Errorf("baseline.txt:%d: row has no \"# reason\" comment above it", i+1)
		}
		r := baselineRow{key{f[0], f[1]}, n, reason}
		if len(rows) > 0 {
			p := rows[len(rows)-1].key
			if p.rule > r.rule || (p.rule == r.rule && p.path >= r.path) {
				return nil, fmt.Errorf("baseline.txt:%d: rows must be sorted by rule then path, without duplicates", i+1)
			}
		}
		rows = append(rows, r)
	}
	return rows, nil
}

const baselineHeader = `# Portable-code baseline: one row per "<rule> <path> <count>", sorted by rule then path, each group under a
# "# reason" comment. A count can only fall: go test fails when a count rises or sits above what the scan finds.
# Run MASH_PORTABLE_UPDATE=1 go test ./internal/portable/ to lower rows. A new row needs a real reason.
`

func formatBaseline(rows []baselineRow) string {
	var b strings.Builder
	b.WriteString(baselineHeader)
	prev := ""
	for _, r := range rows {
		if r.reason != prev {
			fmt.Fprintf(&b, "\n# %s\n", r.reason)
			prev = r.reason
		}
		fmt.Fprintf(&b, "%s %s %d\n", r.rule, r.path, r.n)
	}
	return b.String()
}

// compare returns the problems between the current counts and the baseline.
func compare(cur map[key]int, rows []baselineRow, hits []hit) (over, stale []string) {
	base := map[key]int{}
	for _, r := range rows {
		base[r.key] = r.n
	}
	for k, n := range cur {
		if n > base[k] {
			over = append(over, fmt.Sprintf("%s %s: %d hit(s), baseline %d", k.rule, k.path, n, base[k]))
			for _, h := range hits {
				if h.rule == k.rule && h.path == k.path {
					over = append(over, fmt.Sprintf("    %s:%d: %s", h.path, h.line, h.text))
				}
			}
		}
	}
	for _, r := range rows {
		switch n := cur[r.key]; {
		case n == 0:
			stale = append(stale, fmt.Sprintf("stale: %s %s has no hits: delete the row", r.rule, r.path))
		case n < r.n:
			stale = append(stale, fmt.Sprintf("stale: %s %s: lower the baseline to %d", r.rule, r.path, n))
		}
	}
	sort.Strings(stale)
	return over, stale
}

func TestPortableCode(t *testing.T) {
	root := repoRoot(t)
	hits := scan(t, root)
	cur := count(hits)
	path := filepath.Join(root, "internal", "portable", "baseline.txt")
	text, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	rows, err := parseBaseline(string(text))
	if err != nil {
		t.Fatal(err)
	}
	over, stale := compare(cur, rows, hits)

	if os.Getenv(updateEnv) == "1" {
		if len(over) > 0 {
			t.Fatalf("%s refuses to raise the baseline; fix these first:\n%s", updateEnv, strings.Join(over, "\n"))
		}
		var out []baselineRow
		for _, r := range rows {
			if n := cur[r.key]; n > 0 {
				r.n = n
				out = append(out, r)
			}
		}
		if err := os.WriteFile(path, []byte(formatBaseline(out)), 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	if len(over) > 0 {
		t.Errorf("new portable-code violations (fix them, or add a baseline row with a reason a stranger installing mash could not hit):\n%s", strings.Join(over, "\n"))
	}
	if len(stale) > 0 {
		t.Errorf("the baseline is above the scan (it can only shrink; run %s=1 go test ./internal/portable/):\n%s", updateEnv, strings.Join(stale, "\n"))
	}
	if len(over) > 0 || len(stale) > 0 {
		t.FailNow()
	}
}

// TestBootstrap prints the current counts as baseline rows when MASH_PORTABLE_INIT names a file to write; it is how the
// initial baseline was populated and does nothing otherwise.
func TestBootstrap(t *testing.T) {
	out := os.Getenv("MASH_PORTABLE_INIT")
	if out == "" {
		t.Skip("MASH_PORTABLE_INIT not set")
	}
	cur := count(scan(t, repoRoot(t)))
	var rows []baselineRow
	for k, n := range cur {
		rows = append(rows, baselineRow{k, n, "baseline 2026-11-01"})
	}
	sort.Slice(rows, func(i, j int) bool {
		if rows[i].rule != rows[j].rule {
			return rows[i].rule < rows[j].rule
		}
		return rows[i].path < rows[j].path
	})
	if err := os.WriteFile(out, []byte(formatBaseline(rows)), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestRegexes(t *testing.T) {
	for _, s := range []string{"randyb", "RandyBias", "my rlb box", "homerange", "rangehand-x", "plethorax", "dev1", "thaos", "bf1", "devsup", "rdev", "mire's", "msWisp", "ms-wisp"} {
		if !personalRE.MatchString(s) {
			t.Errorf("personalRE should match %q", s)
		}
	}
	for _, s := range []string{"bogworks", "github.com/bogworks/mash", "world", "dev10", "rlbx", "ordinary text", "rdevice"} {
		if personalRE.MatchString(s) {
			t.Errorf("personalRE should not match %q", s)
		}
	}
	for _, s := range []string{"~/x", "/tmp/x", "/home/u", "/Users/u"} {
		if !homePathRE.MatchString(s) {
			t.Errorf("homePathRE should match %q", s)
		}
	}
	for _, s := range []string{"/tmp", "a/tmp/x", "/homework", "~user", "/usr/local"} {
		if homePathRE.MatchString(s) {
			t.Errorf("homePathRE should not match %q", s)
		}
	}
	for s, want := range map[string]string{
		"https://github.com/someone/repo": "someone",
		"git@github.com:someone/repo.git": "someone",
		"github.com/bogworks/mash":        "",
		"github.com/nats-io/nats.go":      "nats-io",
		"github.com/Bogworks/mash":        "",
		"no url here":                     "",
	} {
		if got := foreignOrg(s); got != want {
			t.Errorf("foreignOrg(%q) = %q, want %q", s, got, want)
		}
	}
}

func TestGoRules(t *testing.T) {
	tests := []struct {
		name, rel, src string
		want           []string // rule names, in line order
	}{
		{"personal literal", "cmd/x/a.go", `package x
var s = "ask randyb"`, []string{rulePersonal}},
		{"comment is ignored", "cmd/x/a.go", `package x
// randyb /tmp/x 5 * time.Minute
var s = 1`, nil},
		{"home and tmp literals", "cmd/x/a.go", `package x
var a = "~/x"
var b = "/tmp/y"
var c = "/home/z"
var d = "/Users/z"`, []string{ruleHomeTmp, ruleHomeTmp, ruleHomeTmp, ruleHomeTmp}},
		{"home calls", "internal/x/a.go", `package x
import "os"
func f() { os.UserHomeDir(); os.Getenv("HOME"); os.Getenv("PATH") }`, []string{ruleHomeTmp, ruleHomeTmp}},
		{"home calls allowed in config", "internal/config/a.go", `package config
import "os"
func f() { os.UserHomeDir(); os.Getenv("HOME") }`, nil},
		{"foreign org", "cmd/x/a.go", `package x
var u = "https://github.com/other/thing"
var v = "https://github.com/bogworks/mash"`, []string{ruleOrgURL}},
		{"import path is not a literal", "cmd/x/a.go", `package x
import _ "github.com/other/thing"`, nil},
		{"inline minute", "cmd/x/a.go", `package x
import "time"
func f() { _ = 5 * time.Minute; _ = time.Hour * 2; _ = n * time.Minute; _ = 30 * time.Second }`, []string{ruleTimeout, ruleTimeout}},
		{"named package-level is fine", "cmd/x/a.go", `package x
import "time"
const lease = 5 * time.Minute
var grace = 2 * time.Hour
var ttl, _ = time.ParseDuration("10m")`, nil},
		{"long ParseDuration", "cmd/x/a.go", `package x
import "time"
func f() { time.ParseDuration("45s"); time.ParseDuration("30s"); time.ParseDuration("nonsense") }`, []string{ruleTimeout}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			hits, err := scanGo(tc.rel, []byte(tc.src))
			if err != nil {
				t.Fatal(err)
			}
			var got []string
			for _, h := range hits {
				got = append(got, h.rule)
			}
			if strings.Join(got, ",") != strings.Join(tc.want, ",") {
				t.Errorf("rules = %v, want %v (%v)", got, tc.want, hits)
			}
		})
	}
}

func TestShellRule(t *testing.T) {
	text := "#!/bin/sh\n# randyb wrote this\necho ok\nssh rdev ls\n  # rlb\n"
	hits := scanShell("scripts/x.sh", text)
	if len(hits) != 1 || hits[0].line != 4 || hits[0].rule != ruleShell {
		t.Errorf("hits = %v, want one on line 4", hits)
	}
}

func TestBaselineParse(t *testing.T) {
	if _, err := parseBaseline("R1 a.go 1\n"); err == nil {
		t.Error("a row without a reason should be refused")
	}
	if _, err := parseBaseline("# \nR1 a.go 1\n"); err == nil {
		t.Error("an empty reason should be refused")
	}
	if _, err := parseBaseline("# why\nR1 b.go 1\nR1 a.go 1\n"); err == nil {
		t.Error("unsorted rows should be refused")
	}
	if _, err := parseBaseline("# why\nR1 a.go 0\n"); err == nil {
		t.Error("a zero count should be refused")
	}
	rows, err := parseBaseline("# why\nR1 a.go 2\nR2 a.go 1\n")
	if err != nil || len(rows) != 2 {
		t.Fatalf("rows=%v err=%v", rows, err)
	}
	if rt, err := parseBaseline(formatBaseline(rows)); err != nil || len(rt) != 2 {
		t.Errorf("round trip: %v %v", rt, err)
	}
}

func TestCompare(t *testing.T) {
	rows := []baselineRow{{key{"R1", "a.go"}, 2, "r"}, {key{"R1", "b.go"}, 1, "r"}, {key{"R1", "c.go"}, 3, "r"}}
	cur := map[key]int{{"R1", "a.go"}: 3, {"R1", "c.go"}: 2, {"R2", "d.go"}: 1}
	over, stale := compare(cur, rows, nil)
	if len(over) != 2 {
		t.Errorf("over = %v, want a.go and d.go", over)
	}
	if len(stale) != 2 {
		t.Errorf("stale = %v, want b.go (delete) and c.go (lower)", stale)
	}
}
