// Command buildrelease builds mash exactly as a release does and prints "<sha256>  <path>".
//
//	buildrelease -src <clone> -out <file> -goos linux -goarch arm64 -version 0.1.0 -commit <X>
package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"

	"github.com/bogworks/mash/internal/release"
)

func main() {
	if err := run(os.Args[1:], os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, "buildrelease:", err)
		os.Exit(1)
	}
}

func run(args []string, stdout io.Writer) error {
	fs := flag.NewFlagSet("buildrelease", flag.ContinueOnError)
	src := fs.String("src", ".", "source directory (a clone at the candidate commit)")
	out := fs.String("out", "", "output file (required)")
	goos := fs.String("goos", "", "target GOOS (default: the host's)")
	goarch := fs.String("goarch", "", "target GOARCH (default: the host's)")
	version := fs.String("version", "", "release version, without the leading v (required)")
	commit := fs.String("commit", "", "candidate commit (required)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *out == "" || *version == "" || *commit == "" {
		return fmt.Errorf("-out, -version and -commit are required")
	}
	if err := release.CheckSource(*src, *commit, *out); err != nil {
		return err
	}
	sum, err := release.Build(context.Background(), *src, *out, *goos, *goarch, *version, *commit)
	if err != nil {
		return err
	}
	_, err = fmt.Fprintf(stdout, "%s  %s\n", sum, *out)
	return err
}
