package main

import (
	"fmt"
	"io"
	"runtime/debug"
	"strings"

	"github.com/bogworks/mash/internal/cliframe"
	"github.com/bogworks/mash/internal/endpoint"
)

// commit is set by -ldflags "-X main.commit=<sha>"; otherwise the VCS revision stamped by go build is used.
var commit string

// readBuildInfo is debug.ReadBuildInfo, replaceable in tests.
var readBuildInfo = debug.ReadBuildInfo

const versionUsage = `Usage: mashctl version [--json]
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
			if strings.HasPrefix(a, "-") && a != "-" {
				return cliframe.Report(stdout, stderr, unknownFlag(a), jsonRequested(args))
			}
			return cliframe.Report(stdout, stderr, cliframe.Usagef("unknown argument").WithHint("mashctl version --help"), jsonRequested(args))
		}
	}
	bi, _ := readBuildInfo()
	version, full := endpoint.CurrentVersion(), endpoint.BuildRevision(commit, bi)
	if asJSON {
		if full == "" {
			full = "unknown"
		}
		// The JSON carries the whole commit (a pin for a tool that needs it exactly); the text shows seven characters.
		writeJSON(stdout, struct {
			Version string  `json:"version"`
			Commit  string  `json:"commit"`
			Plugin  *string `json:"plugin"` // mashctl has no plugin
		}{Version: version, Commit: full})
		return 0
	}
	fmt.Fprintf(stdout, "mashctl v%s (%s)\n", version, shortCommit(full))
	return 0
}
