// Command repofiles checks that no tracked file under a git checkout is a
// binary executable, holds binary content, or is over the size limit
// (internal/release.CheckTrackedFiles, #1247). CI builds it from the base
// revision and runs it on the pull request's checkout, so a docs-only pull
// request (which skips the test job) still gets the check.
//
//	repofiles --root <dir>
//
// It prints nothing and exits 0 when the tree is clean, prints one line per
// problem on stderr and exits 1 otherwise, and exits 2 on a usage error or
// when <dir> is not a git checkout.
package main

import (
	"flag"
	"fmt"
	"io"
	"os"

	"github.com/bogworks/mash/internal/release"
)

func main() { os.Exit(run(os.Args[1:], os.Stderr)) }

func run(args []string, stderr io.Writer) int {
	fs := flag.NewFlagSet("repofiles", flag.ContinueOnError)
	fs.SetOutput(stderr)
	root := fs.String("root", "", "the git checkout to check")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if *root == "" || fs.NArg() != 0 {
		fmt.Fprintln(stderr, "usage: repofiles --root <dir>")
		return 2
	}
	bad, err := release.CheckTrackedFiles(*root)
	if err != nil {
		fmt.Fprintf(stderr, "repofiles: %v\n", err)
		return 2
	}
	if len(bad) > 0 {
		fmt.Fprintln(stderr, "committed binaries or large files (build outputs belong in .gitignore):")
		for _, p := range bad {
			fmt.Fprintln(stderr, p)
		}
		return 1
	}
	return 0
}
