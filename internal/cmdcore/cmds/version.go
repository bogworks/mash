package cmds

import (
	"context"

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

// Text is "mash v<version> (<commit>)".
func (r VersionResult) Text(cmdcore.Opts) string {
	return "mash v" + r.Version + " (" + r.Commit + ")"
}

// Version is the version command.
var Version = cmdcore.Command[VersionParams, VersionResult]{
	Name:    "version",
	Summary: "Print the version.",
	Run: func(_ context.Context, env *cmdcore.Env, _ VersionParams) (VersionResult, error) {
		svc, ok := env.Svc.(VersionInfo)
		if !ok {
			return VersionResult{}, cliframe.Failf("mash version has no build information")
		}
		v, c, p := svc.Version()
		return VersionResult{Version: v, Commit: c, Plugin: p}, nil
	},
}
