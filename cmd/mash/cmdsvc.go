package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync/atomic"
	"time"

	"github.com/bogworks/mash/internal/cliframe"
	"github.com/bogworks/mash/internal/cmdcore"
	"github.com/bogworks/mash/internal/cmdcore/cmds"
	"github.com/bogworks/mash/internal/config"
	"github.com/bogworks/mash/internal/endpoint"
	"github.com/bogworks/mash/internal/kind"
	"github.com/bogworks/mash/internal/sap"
)

// commandEntries is the one command table: the commands defined on cmdcore, served by the command line (cli) and as
// MCP tools (serve) from this list. Add a command here and both doors have it.
var commandEntries = []cmdcore.Entry{
	cmdcore.Register(cmds.Send),
	cmdcore.Register(cmds.Version),
	cmdcore.Register(cmds.Who),
}

// helpFrame is what --help prints after a command's own options. The descriptions are the specification's.
var helpFrame = cmdcore.Frame{Tool: "mash", Globals: []cmdcore.HelpOption{
	{Flag: "--endpoint", Value: "address", Desc: "Which endpoint to act as, as owner.group.name. Not needed for the everyday and setup commands when only one is installed here. mash serve and mash hook always need it said: a session becomes an endpoint only because someone chose that."},
	{Flag: "--creds", Value: "path", Desc: "The creds file to sign in with. Default: <config folder>/<address>.creds."},
	{Flag: "--server", Value: "url", Desc: "The server address. May be given more than once."},
	{Flag: "--ca", Value: "path", Desc: "The certificate authority's certificate, when the server uses its own."},
	{Flag: "--config-dir", Value: "path", Desc: "The configuration folder. Default: ~/.config/mash."},
	{Flag: "--verbose", Short: "-v", Desc: "After the usual output, print more about what was done, as 'label: value' lines."},
	{Flag: "--json", Desc: "Print the result as JSON: the same object the matching MCP tool returns."},
	{Flag: "--no-color", Desc: "Never use color. Color is used only on a terminal, and NO_COLOR is honored."},
	{Flag: "--help", Short: "-h", Desc: "Show help for the command."},
}}

// newCommands is the command table with the Env its commands run in.
func newCommands(env cmdcore.EnvFunc) *cmdcore.Registry {
	return cmdcore.NewRegistry(env, commandEntries...).WithFrame(helpFrame)
}

// commandVerb reports whether verb is a command of the table.
func commandVerb(verb string) bool {
	_, ok := newCommands(nil).Lookup(verb)
	return ok
}

// shortCommit is the commit as `mash version` shows it: the first 7 characters of a full revision, "+dirty" kept, and
// "unknown" when the build has none.
func shortCommit(c string) string {
	if c == "" {
		return "unknown"
	}
	rev, dirty := strings.CutSuffix(c, "+dirty")
	if len(rev) > 7 {
		rev = rev[:7]
	}
	if dirty {
		rev += "+dirty"
	}
	return rev
}

// buildVersion is cmds.VersionInfo for a process whose plugin root is pluginRoot ("" when not run by the plugin).
type buildVersion struct{ pluginRoot string }

func (b buildVersion) Version() (version, commit, plugin string) {
	return endpoint.CurrentVersion(), shortCommit(endpoint.CurrentCommit()), pluginVersion(b.pluginRoot)
}

// sendOn sends as ep: the checks the send tool and the command line share (the numbers name messages of ep's own
// inbox, a short reply to a question is tagged fyi, the id is checked), then the send itself.
func sendOn(ctx context.Context, ep *endpoint.Endpoint, to, text string, o cmds.SendOptions) (cmds.SendReceipt, error) {
	if err := checkSend(o); err != nil {
		return cmds.SendReceipt{}, err
	}
	k, err := kind.Parse(o.Kind)
	if err != nil {
		return cmds.SendReceipt{}, err
	}
	inReplyTo, via := uint64(o.ReplyTo), uint64(o.Relay)
	k, retagged := kind.Tag(k, inReplyTo, text)
	res, err := ep.SendDetailed(ctx, to, text, endpoint.SendOptions{InReplyTo: inReplyTo, Via: via, Urgent: o.Urgent, Kind: k, ID: o.ID})
	if err != nil {
		var ne *endpoint.NoEndpointError
		if errors.As(err, &ne) {
			return cmds.SendReceipt{}, cmds.ErrNoSuchEndpoint
		}
		return cmds.SendReceipt{}, err
	}
	return cmds.SendReceipt{Number: int(res.Seq), Duplicate: res.Duplicate, Retagged: retagged, To: sap.Display(sap.KeyOf(to)), Kind: string(k)}, nil
}

// checkSend refuses what no endpoint would accept, as a usage error.
func checkSend(o cmds.SendOptions) *cliframe.Error {
	if o.ID != "" {
		if err := endpoint.ValidateSendID(o.ID); err != nil {
			return cliframe.Usagef("%s", strings.TrimPrefix(err.Error(), "mash: "))
		}
	}
	return nil
}

// openFailure words a failure to open the endpoint the way the rules do: the server refusing the credentials, and the
// server not answering, each in its own form. Any other error is returned as it is.
func openFailure(cfg config.Config, err error) error {
	switch {
	case endpoint.IsAuthFailure(err):
		return &cmds.SignInError{Addr: cfg.DisplayName()}
	case transient(err) || strings.Contains(err.Error(), "no servers available"):
		return cmdcore.ServerNotAnswering(config.RedactURL(cfg.URL))
	}
	return err
}

// cliSvc is the service the command line's commands run against. It opens the endpoint when a send needs it, so a
// usage error and --help come first and version needs no endpoint.
type cliSvc struct {
	buildVersion
	cfg    config.Config
	stderr io.Writer
}

func (s *cliSvc) Send(ctx context.Context, to, text string, o cmds.SendOptions) (cmds.SendReceipt, error) {
	cfg := s.cfg
	if err := checkSend(o); err != nil { // before the endpoint: a bad key is a usage error with or without one
		return cmds.SendReceipt{}, err.WithHint("mash send --help")
	}
	if cfg.Inert() {
		return cmds.SendReceipt{}, cliframe.Failf("no endpoint: set MASH_ENDPOINT (owner.group.name), --endpoint, or install credentials for one endpoint").WithHint("mash status")
	}
	ctx, cancel := context.WithTimeout(ctx, cliDeadline(cfg))
	defer cancel()
	openCtx, openCancel := context.WithTimeout(ctx, cfg.OpenTimeout)
	// The first wave of reads also fetches the recipient's record: over a slow link every dependent request is a round trip.
	ep, err := endpoint.OpenClientWith(openCtx, cfg, endpoint.ClientOptions{Lease: cfg.LaunchToken != "", Records: []string{sap.KeyOf(to)}})
	openCancel()
	if err != nil {
		return cmds.SendReceipt{}, openFailure(cfg, err)
	}
	defer ep.Close()
	// A send from a launched session's shell names the session's instance; adopted after the body is read, so
	// a slow stdin can't leave it signing for a serve that has since gone.
	// A session's shell outlives its serve's lease in two windows: right after a (re)start, before the new serve
	// takes the lease, and after a crash, while the dead serve's lease (another launch's token) has not yet
	// expired. Refusing there lost a resumed session's replies. So the mail goes out
	// under the client's own instance, and the warning says it cannot be bound to the session; a reader that
	// binds acks to the session's instance then rejects it, exactly as before this adoption existed.
	ac, acc := context.WithTimeout(ctx, cfg.CallTimeout)
	if err := ep.AdoptLaunchInstance(ac); err != nil {
		fmt.Fprintf(s.stderr, "%v; sending under this command's own instance\n", err)
	}
	acc()
	c, cc := context.WithTimeout(ctx, cfg.CallTimeout)
	defer cc()
	return sendOn(c, ep, to, text, o)
}

// whoEntries is the directory as the who command reads it: the dotted address, whether it is online, and the role, host
// and description where recorded. The command sorts and filters.
func whoEntries(ps []endpoint.Presence) []cmds.WhoEntry {
	out := make([]cmds.WhoEntry, 0, len(ps))
	for _, p := range ps {
		r := p.Record
		out = append(out, cmds.WhoEntry{Address: r.Owner + "." + r.Group + "." + r.Instance, Online: p.Online,
			Role: r.Role, Host: r.Host, Description: r.Description})
	}
	return out
}

// Who reads the whole directory and which endpoints answer a presence probe, in one open: over a slow link every
// dependent request is a round trip.
func (s *cliSvc) Who(ctx context.Context) ([]cmds.WhoEntry, error) {
	cfg := s.cfg
	if cfg.Inert() {
		return nil, cliframe.Failf("no endpoint: set MASH_ENDPOINT (owner.group.name), --endpoint, or install credentials for one endpoint").WithHint("mash status")
	}
	ctx, cancel := context.WithTimeout(ctx, cliDeadline(cfg))
	defer cancel()
	openCtx, openCancel := context.WithTimeout(ctx, cfg.OpenTimeout)
	ep, err := endpoint.OpenClientWith(openCtx, cfg, endpoint.ClientOptions{FullDirectory: true, Presence: true})
	openCancel()
	if err != nil {
		return nil, openFailure(cfg, err)
	}
	defer ep.Close()
	c, cc := context.WithTimeout(ctx, cfg.CallTimeout)
	defer cc()
	if err := ep.Refresh(c); err != nil {
		return nil, openFailure(cfg, err)
	}
	return whoEntries(ep.Who()), nil
}

// mcpSvc is the service the MCP tools run against: the session's endpoint, which may not be usable yet.
type mcpSvc struct {
	buildVersion
	state       *atomic.Pointer[sessionState]
	name        string
	callTimeout time.Duration
}

func (s *mcpSvc) Send(ctx context.Context, to, text string, o cmds.SendOptions) (cmds.SendReceipt, error) {
	ep, err := s.state.Load().usable(s.name)
	if err != nil {
		return cmds.SendReceipt{}, err
	}
	ctx, cancel := context.WithTimeout(ctx, s.callTimeout)
	defer cancel()
	return sendOn(ctx, ep, to, text, o)
}

// Who asks every endpoint now whether it is there (the answer is reused for 30 seconds), then reads the directory.
func (s *mcpSvc) Who(ctx context.Context) ([]cmds.WhoEntry, error) {
	ep, err := s.state.Load().usable(s.name)
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(ctx, s.callTimeout)
	defer cancel()
	ps, err := ep.WhoFresh(ctx)
	if err != nil {
		return nil, fmt.Errorf("who: asking who is online: %w", err)
	}
	return whoEntries(ps), nil
}
