package natsfixture

import (
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

const fixturePath = "github.com/bogworks/mash/internal/natsfixture"

func moduleRoot(t *testing.T) string {
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

// TestNoReleaseCodeImportsFixture: only _test.go files may import this package.
func TestNoReleaseCodeImportsFixture(t *testing.T) {
	root := moduleRoot(t)
	own := filepath.Join(root, "internal", "natsfixture")
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if path == own || d.Name() == ".git" {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		f, perr := parser.ParseFile(token.NewFileSet(), path, nil, parser.ImportsOnly)
		if perr != nil {
			return nil // not this test's concern; the build reports it
		}
		for _, imp := range f.Imports {
			if p, _ := strconv.Unquote(imp.Path.Value); p == fixturePath {
				t.Errorf("%s imports %s; it is test fixtures only", path, fixturePath)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

// TestReleaseBinariesDoNotDependOnFixture: the dependency closure of each release binary excludes this package.
func TestReleaseBinariesDoNotDependOnFixture(t *testing.T) {
	goBin, err := exec.LookPath("go")
	if err != nil {
		t.Skip("go tool not available")
	}
	root := moduleRoot(t)
	for _, pkg := range []string{"./cmd/mash", "./cmd/mashctl"} {
		cmd := exec.Command(goBin, "list", "-deps", pkg)
		cmd.Dir = root
		out, err := cmd.Output()
		if err != nil {
			t.Fatalf("go list -deps %s: %v", pkg, err)
		}
		for _, dep := range strings.Fields(string(out)) {
			if dep == fixturePath {
				t.Errorf("%s depends on %s; it is test fixtures only", pkg, fixturePath)
			}
		}
	}
}
