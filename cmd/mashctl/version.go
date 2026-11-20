package main

import (
	"fmt"
	"io"
	"runtime/debug"
	"strings"

	"github.com/bogworks/mash/internal/cliframe"
	"github.com/bogworks/mash/internal/config"
	"github.com/bogworks/mash/internal/endpoint"
)

// commit is set by -ldflags "-X main.commit=<sha>"; otherwise the VCS revision stamped by go build is used.
var commit string

// readBuildInfo is debug.ReadBuildInfo, replaceable in tests.
var readBuildInfo = debug.ReadBuildInfo

const versionUsage = `usage: mashctl version [--json]
  --json  print the version as JSON
Example:
  mashctl version
`

// shortCommit is the first 7 characters of a revision, with a "+dirty" mark kept; "unknown" when there is none.
func shortCommit(rev string) string {
	if rev == "" {
		return "unknown"
	}
	base, dirty := strings.CutSuffix(rev, "+dirty")
	if len(base) > 7 {
		base = base[:7]
	}
	if dirty {
		base += "+dirty"
	}
	return base
}

// runVersion is `mashctl version`.
func runVersion(args []string, stdout, stderr io.Writer) int {
	asJSON := false
	for _, a := range args {
		switch {
		case a == "-h" || a == "--help":
			fmt.Fprint(stdout, versionUsage)
			return 0
		case a == "--json":
			asJSON = true
		default:
			return cliframe.Report(stdout, stderr, cliframe.Usagef("unknown %s", config.RejectedArg(a)).WithHint("mashctl version --help"), jsonRequested(args))
		}
	}
	bi, _ := readBuildInfo()
	version, rev := endpoint.CurrentVersion(), shortCommit(endpoint.BuildRevision(commit, bi))
	if asJSON {
		writeJSON(stdout, struct {
			Version string `json:"version"`
			Commit  string `json:"commit"`
		}{version, rev})
		return 0
	}
	fmt.Fprintf(stdout, "mashctl v%s (%s)\n", version, rev)
	return 0
}
