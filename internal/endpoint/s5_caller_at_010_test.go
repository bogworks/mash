package endpoint

// AT-010 (REQ-010) under amendment 5 (A) and N9: the caller check. Discovery, presence and the prompt sender stay in
// mash, so no file imports the SDK's caller or host package. The import bans hold as ever: .../caller only in the
// constructor package (there is none), and .../host nowhere, tests included. The construction count is at most one
// (zero today). The mutation test removes mash's one verify step in discovery and AT-015's negative cases must then
// show the regression.
//
// The mutation mechanism: the verify step is called through the S5 switch (s5Switch, s5hook.go), by both
// discoveries, and the test swaps the switch's verify for a step that accepts every registration. The mutated build still builds and
// starts (it is this same test binary), and the cases that fail closed today then accept the forged answers.
//
// The release check (scripts/releasecheck/unlinked.go) also refuses the SDK's caller and host packages in the built
// 0.5.x binaries; this test is the source-level check.

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/bogworks/mash/internal/sap"
)

const s5SDK = "github.com/bogworks/synadia-agents.go"

// s5SDKImports walks the Go files under root. constructorDir is the one directory (relative to root, slash form) that
// may import the SDK's caller package, "" for none. It returns a line for each ban broken, and the number of calls in
// the constructor's non-test files that build a caller (a New function of the imported package).
func s5SDKImports(t *testing.T, root, constructorDir string) (violations []string, constructions int) {
	t.Helper()
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if d.Name() == ".git" {
				return fs.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") {
			return nil
		}
		fset := token.NewFileSet()
		f, err := parser.ParseFile(fset, path, nil, parser.SkipObjectResolution)
		if err != nil {
			return nil // a file that does not parse is not an import of anything
		}
		rel, _ := filepath.Rel(root, path)
		dir, isTest := filepath.ToSlash(filepath.Dir(rel)), strings.HasSuffix(path, "_test.go")
		for _, im := range f.Imports {
			ip, _ := strconv.Unquote(im.Path.Value)
			switch {
			case ip == s5SDK+"/host" || strings.HasPrefix(ip, s5SDK+"/host/"):
				violations = append(violations, rel+" imports "+ip+" (the host package is banned everywhere)")
			case ip == s5SDK+"/caller" || strings.HasPrefix(ip, s5SDK+"/caller/"):
				if constructorDir == "" || dir != constructorDir {
					violations = append(violations, rel+" imports "+ip+" outside the constructor package")
					continue
				}
				if isTest {
					continue
				}
				local := "caller"
				if im.Name != nil {
					local = im.Name.Name
				}
				ast.Inspect(f, func(n ast.Node) bool {
					if c, ok := n.(*ast.CallExpr); ok {
						if sel, ok := c.Fun.(*ast.SelectorExpr); ok {
							if x, ok := sel.X.(*ast.Ident); ok && x.Name == local && strings.HasPrefix(sel.Sel.Name, "New") {
								constructions++
							}
						}
					}
					return true
				})
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return violations, constructions
}

// TestS5AT010_ImportBansAndConstructionCount: the repository holds no import of the SDK's caller or host and no
// construction site (the count is at most one). Fixtures show the check sees a broken ban: a caller import outside the
// constructor package, a host import in a test file, and a second construction site.
func TestS5AT010_ImportBansAndConstructionCount(t *testing.T) {
	violations, n := s5SDKImports(t, filepath.Join("..", ".."), "")
	if len(violations) != 0 || n != 0 {
		t.Fatalf("the repository breaks the import bans or builds a caller: %q (%d constructions)", violations, n)
	}

	write := func(root, rel, src string) {
		t.Helper()
		p := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(src), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	root := t.TempDir()
	write(root, "elsewhere/x.go", "package x\nimport _ \""+s5SDK+"/caller\"\n")
	write(root, "elsewhere/y_test.go", "package x\nimport _ \""+s5SDK+"/host\"\n")
	write(root, "ctor/c.go", "package ctor\nimport \""+s5SDK+"/caller\"\nfunc a() { caller.New() }\nfunc b() { caller.NewOther() }\n")
	write(root, "ctor/c_test.go", "package ctor\nimport \""+s5SDK+"/caller\"\nfunc c() { caller.New() }\n")
	violations, n = s5SDKImports(t, root, "ctor")
	if len(violations) != 2 || n != 2 {
		t.Fatalf("fixtures: %q, %d constructions; want the caller import outside the constructor and the host import in a test, and 2 constructions (a second site fails the count of one)", violations, n)
	}
	violations, n = s5SDKImports(t, root, "")
	if len(violations) != 4 || n != 0 {
		t.Fatalf("fixtures with no constructor package: %q, %d constructions; want all four imports refused", violations, n)
	}
}

// TestS5AT010_MutationRemovingTheVerifyStepIsCaught: with mash's one verify step taken out, AT-015's negative cases
// each show the regression: the forged or mismatched answer is accepted (it is not counted as not verified), where it
// is refused today. The mutated discovery runs and finishes: an error from it is not the regression and fails this
// test. The cases that name bob are also seen to attribute bob, and the recipient discovery of prompt vouches for the
// forged answer's subject.
func TestS5AT010_MutationRemovingTheVerifyStepIsCaught(t *testing.T) {
	needS5Caller(t, "AT-010 mutation")
	z := newS5Fix(t)
	genuine := z.genuineRegistration(t, z.bob)
	refused := z.refused(t, genuine)
	env, _ := z.env(t, z.alice, s5Inputs{})
	ep := newS5Main(t, env).(*s5Main).ep

	// today: every case is refused and counted, by both discoveries
	for _, f := range refused {
		t.Run("today/"+f.Name, func(t *testing.T) {
			got, _ := s5RunNegative(t, z, f)
			if got.Err != nil || len(got.Accepted) != 0 || got.NotVerified != 1 {
				t.Fatalf("unmutated: %+v, want the answer refused and counted once", got)
			}
			if ep.judgeAnswerS5(s5Switch, f.Data, s5PromptSubjectOf(t, f.Data)) {
				t.Fatalf("unmutated: the recipient discovery vouched for the %s answer", f.Name)
			}
		})
	}

	mutated := s5On()
	mutated.verify = func(reg registration, _ *dirView) (string, bool) {
		a, _, ok := sap.AddressFromSubject(reg.Subject)
		return a.Flat(), ok
	}
	setS5Hook(t, mutated)

	for _, f := range refused {
		t.Run("mutated/"+f.Name, func(t *testing.T) {
			got, _ := s5RunNegative(t, z, f)
			if got.Err != nil {
				t.Fatalf("the mutated discovery failed (a crash or an error is not the regression): %v", got.Err)
			}
			if got.NotVerified != 0 || got.Total != 0 {
				t.Fatalf("mutated: the %s answer was still refused and counted (%d, total %d): the mutation was not caught", f.Name, got.NotVerified, got.Total)
			}
			if f.Name != "subject" && (len(got.Accepted) != 1 || got.Accepted[0].Address != z.bob) {
				t.Fatalf("mutated: the %s answer was not attributed to bob: %+v", f.Name, got.Accepted)
			}
			if !ep.judgeAnswerS5(s5Switch, f.Data, s5PromptSubjectOf(t, f.Data)) {
				t.Fatalf("mutated: the recipient discovery did not vouch for the %s answer", f.Name)
			}
		})
	}
}
