package cmds

import (
	"context"
	"strings"

	"github.com/bogworks/mash/internal/cliframe"
	"github.com/bogworks/mash/internal/cmdcore"
)

// JoinRequest is what mash join asks its service to do.
type JoinRequest struct {
	File    string // the join file, as the person wrote it
	Key     string // the seed file, or ""
	Replace bool
	DryRun  bool
}

// JoinOutcome is what joining did, or with DryRun would do. Creds is the creds file's path as it is shown (a path under
// the home folder starts with ~).
type JoinOutcome struct {
	Endpoint string // owner.group.name
	Servers  []string
	Creds    string
}

// Joiner is the service Join needs from Env.Svc: it installs the join file into the configuration folder.
type Joiner interface {
	Join(ctx context.Context, req JoinRequest) (JoinOutcome, error)
}

// JoinParams are the arguments of mash join. It has no MCP tool.
type JoinParams struct {
	File    string `json:"file" arg:"file" desc:"The join file whoever runs the mesh gave you."`
	Key     string `json:"key,omitempty" flag:"--key" value:"path" desc:"Your own seed file, when the join file was made for a public key you supplied."`
	Replace bool   `json:"replace,omitempty" flag:"--replace" desc:"Replace credentials already installed here for the same address."`
	DryRun  bool   `json:"dry_run,omitempty" flag:"--dry-run" desc:"Print what would change, and change nothing."`
}

// JoinResult is the endpoint joined as, the servers and the creds file's path. DryRun is true, and only then present,
// for a run that changed nothing.
type JoinResult struct {
	Endpoint string   `json:"endpoint"`
	Servers  []string `json:"servers"`
	Creds    string   `json:"creds"`
	DryRun   bool     `json:"dry_run,omitempty"`
}

// Text is "Joined as <address>." or, for --dry-run, "Would join as <address>."; with --verbose the servers and the creds
// path follow as 'label: value' lines.
func (r JoinResult) Text(o cmdcore.Opts) string {
	s := "Joined as " + r.Endpoint + "."
	if r.DryRun {
		s = "Would join as " + r.Endpoint + "."
	}
	if o.Verbose {
		s += "\nservers: " + strings.Join(r.Servers, ", ") + "\ncreds: " + r.Creds
	}
	return s
}

// Join is the join command.
var Join = cmdcore.Command[JoinParams, JoinResult]{
	Name:        "join",
	Group:       "Setup",
	NoMCP:       true,
	Summary:     "Install an endpoint's credentials on this machine from a join file.",
	Description: "Install an endpoint's credentials on this machine from a join file.",
	Examples:    []string{"mash join acme.demo.bob.join", "mash join acme.demo.bob.join --key bob.seed"},
	Run: func(ctx context.Context, env *cmdcore.Env, p JoinParams) (JoinResult, error) {
		if env == nil {
			return JoinResult{}, cliframe.Failf("internal: mash join has no environment to run in")
		}
		svc, ok := env.Svc.(Joiner)
		if !ok || svc == nil {
			return JoinResult{}, cliframe.Failf("internal: mash join has no configuration folder to install into")
		}
		o, err := svc.Join(ctx, JoinRequest{File: p.File, Key: p.Key, Replace: p.Replace, DryRun: p.DryRun})
		if err != nil {
			return JoinResult{}, err
		}
		return JoinResult{Endpoint: o.Endpoint, Servers: append([]string{}, o.Servers...), Creds: o.Creds, DryRun: p.DryRun}, nil
	},
}
