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
	names, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	code := map[string]bool{}
	for _, name := range names {
		if strings.HasSuffix(name, "_test.go") {
			continue
		}
		f, err := parser.ParseFile(fset, name, nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		for _, d := range f.Decls {
			gd, ok := d.(*ast.GenDecl)
			if !ok || gd.Tok != token.CONST {
				continue
			}
			// A Trust constant is written as `Name Trust = "v"`, as `Name = Trust("v")`, or as a bare name that repeats the
			// previous spec of its group (the type and the value both carry over). Each form is collected.
			var lastType ast.Expr
			var lastValues []ast.Expr
			for _, sp := range gd.Specs {
				vs := sp.(*ast.ValueSpec)
				typ, values := vs.Type, vs.Values
				if typ == nil && len(values) == 0 { // an implicit repetition of the previous spec
					typ, values = lastType, lastValues
				} else {
					lastType, lastValues = typ, values
				}
				isTrust := func(e ast.Expr) bool { id, ok := e.(*ast.Ident); return ok && id.Name == "Trust" }
				for _, v := range values {
					var lit *ast.BasicLit
					switch x := v.(type) {
					case *ast.BasicLit:
						if isTrust(typ) {
							lit = x
						}
					case *ast.CallExpr: // Trust("new")
						if len(x.Args) == 1 && isTrust(x.Fun) {
							lit, _ = x.Args[0].(*ast.BasicLit)
						}
					}
					if lit != nil && lit.Kind == token.STRING {
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
