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
	Version string  `json:"version"`
	Commit  string  `json:"commit"`
	Plugin  *string `json:"plugin"` // null when not run by the plugin
}

// Text is "mash v<version> (<commit>)", the commit shortened to its first seven characters ("+dirty" kept); --json and the
// tool carry the whole revision.
func (r VersionResult) Text(cmdcore.Opts) string {
	rev, dirty := strings.CutSuffix(r.Commit, "+dirty")
	if len(rev) > 7 {
		rev = rev[:7]
	}
	if dirty {
		rev += "+dirty"
	}
	return "mash v" + r.Version + " (" + rev + ")"
}

// Version is the version command.
var Version = cmdcore.Command[VersionParams, VersionResult]{
	Name:           "version",
	Group:          "Everyday",
	Summary:        "Print the version. It can also be run as --version.",
	Description:    "Print the version. It can also be run as --version.",
	Globals:        cmdcore.GlobalsJSON,
	Examples:       []string{"mash version", "mash --version"},
	MCPDescription: "Print the version.",
	Run: func(_ context.Context, env *cmdcore.Env, _ VersionParams) (VersionResult, error) {
		if env == nil {
			return VersionResult{}, cliframe.Failf("internal: mash version has no environment to run in")
		}
		svc, ok := env.Svc.(VersionInfo)
		if !ok || svc == nil {
			return VersionResult{}, cliframe.Failf("internal: mash version has no build information")
		}
		v, c, p := svc.Version()
		res := VersionResult{Version: v, Commit: c}
		if p != "" {
			res.Plugin = &p
		}
		return res, nil
	},
}
