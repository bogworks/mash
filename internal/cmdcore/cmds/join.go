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
// the home folder starts with ~); CredsPath is the same file's absolute path.
type JoinOutcome struct {
	Endpoint  string // owner.group.name
	Servers   []string
	Creds     string
	CredsPath string
	Finished  bool // an earlier join here stopped part way and this run finished it
}

// Joiner is the service Join needs from Env.Svc: it installs the join file into the configuration folder.
type Joiner interface {
	Join(ctx context.Context, req JoinRequest) (JoinOutcome, error)
}

// JoinParams are the arguments of mash join. It has no MCP tool.
type JoinParams struct {
	File    string `json:"file" arg:"file" desc:"The join file whoever runs the mesh gave you."`
	Key     string `json:"key,omitempty" flag:"--key" value:"path" desc:"Your own seed file, when the join file was made for a public key you supplied."`
	Replace bool   `json:"replace,omitempty" flag:"--replace" desc:"Replace the creds already installed here for the same address. It replaces nothing else: not the server, not the certificate authority."`
	DryRun  bool   `json:"dry_run,omitempty" flag:"--dry-run" desc:"Print what would change, and change nothing."`
}

// JoinResult is the endpoint joined as. DryRun is always present: false on a real run, true for a run that changed nothing. The
// servers and the creds file are the fuller answer (--detail).
type JoinResult struct {
	Endpoint string `json:"endpoint"`
	DryRun   bool   `json:"dry_run"`

	servers   []string
	creds     string // as shown, for the text
	credsPath string // absolute, for --detail --json
	finished  bool   // this run finished a half-done join
}

// JoinDetail is join's fuller object: the endpoint, the servers and the creds file's absolute path.
type JoinDetail struct {
	Endpoint string   `json:"endpoint"`
	Servers  []string `json:"servers"`
	Creds    string   `json:"creds"`
	DryRun   bool     `json:"dry_run"`
}

// Text is "Joined as <address>" or, for --dry-run, "Would join as <address>"
func (r JoinResult) Text(cmdcore.Opts) string {
	if r.DryRun {
		return "Would join as " + r.Endpoint
	}
	if r.finished {
		return "Joined as " + r.Endpoint + " (finished an earlier join)"
	}
	return "Joined as " + r.Endpoint
}

// DetailValue is the fuller object, which contains the usual one.
func (r JoinResult) DetailValue() any {
	return JoinDetail{Endpoint: r.Endpoint, Servers: append([]string{}, r.servers...), Creds: r.credsPath, DryRun: r.DryRun}
}

// DetailText is the 'servers:' and 'creds:' lines.
func (r JoinResult) DetailText(cmdcore.Opts) string {
	return "servers: " + strings.Join(r.servers, ", ") + "\ncreds:   " + r.creds
}

// Join is the join command.
var Join = cmdcore.Command[JoinParams, JoinResult]{
	Name:        "join",
	Group:       "Setup",
	NoMCP:       true,
	Summary:     "Install an endpoint's credentials on this machine from a join file.",
	Description: "Install an endpoint's credentials on this machine from a join file.",
	Globals:     cmdcore.GlobalsLocal,
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
		return JoinResult{Endpoint: o.Endpoint, DryRun: p.DryRun, servers: append([]string{}, o.Servers...), creds: o.Creds, credsPath: o.CredsPath, finished: o.Finished}, nil
	},
}
