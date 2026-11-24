package sap

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
)

// A spool written by one release is read by another, so the set of Trust strings the code can write must match the
// set the spool golden records (the v0.5.0 labels). The spool test checks golden against the code's list; this
// reads the constants out of the source, so a new Trust constant, or a dropped one, fails here and is a decision
// instead of an accident.
func TestCompatTrustConstantsMatchSpoolGolden(t *testing.T) {
	fset := token.NewFileSet()
	pkgs, err := parser.ParseDir(fset, ".", func(fi os.FileInfo) bool { return !strings.HasSuffix(fi.Name(), "_test.go") }, 0)
	if err != nil {
		t.Fatal(err)
	}
	code := map[string]bool{}
	for _, pkg := range pkgs {
		for _, f := range pkg.Files {
			for _, d := range f.Decls {
				gd, ok := d.(*ast.GenDecl)
				if !ok || gd.Tok != token.CONST {
					continue
				}
				// Only a spec that states both the type and a string value counts; every Trust constant does.
				for _, sp := range gd.Specs {
					vs := sp.(*ast.ValueSpec)
					id, ok := vs.Type.(*ast.Ident)
					if !ok || id.Name != "Trust" {
						continue
					}
					for _, v := range vs.Values {
						if lit, ok := v.(*ast.BasicLit); ok && lit.Kind == token.STRING {
							s, err := strconv.Unquote(lit.Value)
							if err != nil {
								t.Fatal(err)
							}
							code[s] = true
						}
					}
				}
			}
		}
	}
	b, err := os.ReadFile(filepath.Join("..", "spool", "testdata", "trust_labels.golden"))
	if err != nil {
		t.Fatal(err)
	}
	golden := map[string]bool{}
	for _, l := range strings.Split(strings.TrimSpace(string(b)), "\n") {
		golden[l] = true
	}
	keys := func(m map[string]bool) []string {
		var k []string
		for s := range m {
			k = append(k, s)
		}
		slices.Sort(k)
		return k
	}
	if len(code) == 0 || !slices.Equal(keys(code), keys(golden)) {
		t.Fatalf("Trust constants in the code and labels in spool/testdata/trust_labels.golden differ\n code:   %q\n golden: %q", keys(code), keys(golden))
	}
}
