package main

import (
	"context"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/bogworks/mash/internal/release"
)

// TestP12_ReleaseGuardUnlinkedEveryTarget is acceptance test P-12 (the policy engine is not part of a release; the
// release guard of #1542): no policy package is linked into mash or mashctl in any version, and
// the guard refuses a linked policy package for both programs on every release target.
//
// Already covered elsewhere (unlinked_test.go): the matching rules on fixed listings, and the step against a scratch
// module for linux and darwin imports. Added here: (a) the real tree, through the step's own goListDeps for every
// release.Targets entry, links no refused package; (b) for every target and program, one engine package injected by
// text into a dependency listing is reported by linkedPolicyPackages and described by the step's describe functions.
func TestP12_ReleaseGuardUnlinkedEveryTarget(t *testing.T) {
	// The injected listings are text, so their module name is a fixture, not the real tree's.
	const module = "example.test/mash"
	// 0.6.0: judge the engine and the SDK caller and host packages, not the switch-on packages (REQ-009).
	const version = "0.6.0"
	engine := []string{"internal/policydoc", "internal/listset", "internal/policyapply", "internal/policyview"}

	t.Run("real tree links no refused package", func(t *testing.T) {
		if testing.Short() {
			t.Skip("go list -deps of the real tree for every target is slow")
		}
		if _, err := exec.LookPath("go"); err != nil {
			t.Skip("go is not on PATH")
		}
		// The root is the module that holds this file, found from the file's own location, not the working directory.
		_, self, _, ok := runtime.Caller(0)
		if !ok {
			t.Fatal("cannot locate the test source")
		}
		root := filepath.Join(filepath.Dir(self), "..", "..")
		realModule, err := modulePath(context.Background(), root)
		if err != nil {
			t.Fatal(err)
		}
		for _, tg := range release.Targets {
			// goListDeps lists cmd/mash and cmd/mashctl together, so one listing covers both programs.
			out, err := goListDeps(context.Background(), root, tg.GOOS, tg.GOARCH)
			if err != nil {
				t.Fatalf("%s/%s: %v", tg.GOOS, tg.GOARCH, err)
			}
			for _, prog := range release.Programs {
				if !strings.Contains(out, realModule+"/cmd/"+prog+"\n") {
					t.Errorf("%s/%s: the listing does not contain cmd/%s", tg.GOOS, tg.GOARCH, prog)
				}
			}
			if got := linkedPolicyPackages(realModule, version, out); len(got) != 0 {
				t.Errorf("%s/%s: refused packages are linked: %v", tg.GOOS, tg.GOARCH, got)
			}
		}
	})

	t.Run("a linked engine package is refused for every target and program", func(t *testing.T) {
		for _, tg := range release.Targets {
			target := tg.GOOS + "/" + tg.GOARCH
			for _, prog := range release.Programs {
				for _, pkg := range engine {
					name := target + "/" + prog + "/" + pkg
					listing := "fmt\n" + module + "/internal/policy\n" + module + "/" + pkg + "\n" + module + "/cmd/" + prog + "\n"
					got := linkedPolicyPackages(module, version, listing)
					if len(got) != 1 || got[0] != pkg {
						t.Errorf("%s: got %v, want [%s]", name, got, pkg)
						continue
					}
					links := []targetLinks{{target, got}}
					if s := describeLinked(links); s != pkg+" ("+target+")" {
						t.Errorf("%s: describeLinked = %q", name, s)
					}
					if r := describeReasons(links); !strings.Contains(r, "policy engine") {
						t.Errorf("%s: describeReasons = %q", name, r)
					}
				}
			}
		}
	})
}
