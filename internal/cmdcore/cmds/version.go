package cmds

import (
	"context"
	"strings"

	"github.com/bogworks/mash/internal/cliframe"
	"github.com/bogworks/mash/internal/cmdcore"
)

// VersionInfo is the service Version needs from Env.Svc.
type VersionInfo interface {
	Version() (version, commit, plugin string)
}

// VersionParams has no arguments.
type VersionParams struct{}

// VersionResult is the build. Plugin is filled in when run by the plugin.
type VersionResult struct {
	Version string `json:"version"`
	Commit  string `json:"commit"`
	Plugin  string `json:"plugin"`
}

// ShortCommit is the commit as the text form shows it: the first 7 characters of a revision, "+dirty" kept.
func ShortCommit(c string) string {
	rev, dirty := strings.CutSuffix(c, "+dirty")
	if len(rev) > 7 {
		rev = rev[:7]
	}
	if dirty {
		rev += "+dirty"
	}
	return rev
}

// Text is "mash v<version> (<commit>)", the commit short. The JSON form carries the whole commit.
func (r VersionResult) Text(cmdcore.Opts) string {
	return "mash v" + r.Version + " (" + ShortCommit(r.Commit) + ")"
}

// Version is the version command.
var Version = cmdcore.Command[VersionParams, VersionResult]{
	Name:        "version",
	Group:       "Everyday",
	Summary:     "Print the version.",
	Description: "Print the version.",
	Examples:    []string{"mash version"},
	Run: func(_ context.Context, env *cmdcore.Env, _ VersionParams) (VersionResult, error) {
		if env == nil {
			return VersionResult{}, cliframe.Failf("internal: mash version has no environment to run in")
		}
		svc, ok := env.Svc.(VersionInfo)
		if !ok || svc == nil {
			return VersionResult{}, cliframe.Failf("internal: mash version has no build information")
		}
		v, c, p := svc.Version()
		return VersionResult{Version: v, Commit: c, Plugin: p}, nil
	},
}
