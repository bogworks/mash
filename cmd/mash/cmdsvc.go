package main

import (
	"context"
	"encoding/json"
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
	"github.com/bogworks/mash/internal/render"
	"github.com/bogworks/mash/internal/sap"
)

// commandEntries is the one command table: the commands defined on cmdcore, served by the command line (cli) and as
// MCP tools (serve) from this list. Add a command here and both doors have it.
var commandEntries = []cmdcore.Entry{
	cmdcore.Register(cmds.Send),
	cmdcore.Register(cmds.Version),
	cmdcore.Register(cmds.Inbox),
	cmdcore.Register(cmds.Read),
	cmdcore.Register(cmds.Who),
	cmdcore.Register(cmds.Status),
	cmdcore.Register(cmds.Doctor),
	cmdcore.Register(cmds.Join),
	cmdcore.Register(cmds.KeyCreate),
}

// helpFrame is what --help prints after a command's own options. The descriptions are the specification's.
var helpFrame = cmdcore.Frame{Tool: "mash", Globals: []cmdcore.HelpOption{
	{Flag: "--endpoint", Value: "address", Desc: "Which endpoint to act as, as owner.group.name. Not needed for the everyday and setup commands when only one is installed here. mash serve and mash hook always need it said: a session becomes an endpoint only because someone chose that."},
	{Flag: "--creds", Value: "path", Desc: "The creds file to sign in with. Default: <config folder>/<address>.creds."},
	{Flag: "--server", Value: "url", Desc: "The server address. May be given more than once."},
	{Flag: "--ca", Value: "path", Desc: "The certificate authority's certificate, when the server uses its own."},
	{Flag: "--config-dir", Value: "path", Desc: "The configuration folder. Default: ~/.config/mash."},
	{Flag: cliframe.DetailFlag, Desc: "Print the fuller answer: after the usual line, more as 'label: value' lines; with --json, the fuller object."},
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
	// the whole revision: --json and the tool carry it; the text form shortens it (cmds.VersionResult.Text)
	commit = endpoint.CurrentCommit()
	if commit == "" {
		commit = "unknown"
	}
	return endpoint.CurrentVersion(), commit, pluginVersion(b.pluginRoot)
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
		var rs *endpoint.RecipientSuspendedError
		if errors.As(err, &rs) {
			return cmds.SendReceipt{}, cmds.ErrRecipientSuspended
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
	var cm *endpoint.CredsMissingError
	switch {
	case errors.As(err, &cm):
		return cmdcore.NoCredentials(cfg.DisplayName(), config.TildeIn(cm.Path, cfg.Home))
	case endpoint.IsAuthFailure(err):
		return framed(cfg.DisplayName(), err)
	case transient(err) || strings.Contains(err.Error(), "no servers available"):
		return cmdcore.ServerNotAnswering(config.RedactURL(cfg.URL))
	}
	return err
}

// framed words err the way the rules do when it is a refused sign-in: error ADDRESS cannot sign in, hint mash doctor.
// It is the one helper every command and tool that talks to the server uses (open, read, inbox, who, send); any other
// error is returned as it is. addr is the endpoint's own address.
func framed(addr string, err error) error {
	if err != nil && endpoint.IsAuthFailure(err) {
		return cmdcore.NotSignedIn(addr)
	}
	return err
}

// cliSvc is the service the command line's commands run against. It opens the endpoint when a send needs it, so a
// usage error and --help come first and version needs no endpoint.
type cliSvc struct {
	buildVersion
	cfg    config.Config
	cfgErr error // what loading the configuration failed with: status and doctor report it
	stderr io.Writer
}

// Health is the checks of status and doctor. They report a configuration that did not load, so they run with it.
func (s *cliSvc) Health(ctx context.Context) (cmds.HealthReport, error) {
	return (&healthSvc{cfg: s.cfg, cfgErr: s.cfgErr}).Health(ctx)
}

func (s *cliSvc) Send(ctx context.Context, to, text string, o cmds.SendOptions) (cmds.SendReceipt, error) {
	cfg := s.cfg
	if err := checkSend(o); err != nil { // before the endpoint: a bad key is a usage error with or without one
		return cmds.SendReceipt{}, err.WithHint("mash send --help")
	}
	if cfg.Inert() {
		return cmds.SendReceipt{}, cmdcore.NoEndpoint(cmdcore.NoneInstalled, "send", nil)
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

// whoEntries is the directory as the who command reads it: the dotted address, whether it is online, and the role, host,
// description, capabilities and labels where recorded. Only the endpoints of owner (the caller's own) are kept: who
// never lists another owner's, whatever the account holds. The command sorts and filters.
func whoEntries(ps []endpoint.Presence, owner string) []cmds.WhoEntry {
	out := make([]cmds.WhoEntry, 0, len(ps))
	for _, p := range ps {
		r := p.Record
		if r.Owner != owner {
			continue
		}
		out = append(out, cmds.WhoEntry{Address: r.Owner + "." + r.Group + "." + r.Instance, Online: p.Online,
			Role: r.Role, Host: r.Host, Description: r.Description, Capabilities: r.Caps, Labels: r.Labels})
	}
	return out
}

// Who reads the whole directory and which endpoints answer a presence probe, in one open: over a slow link every
// dependent request is a round trip.
func (s *cliSvc) Who(ctx context.Context) ([]cmds.WhoEntry, error) {
	cfg := s.cfg
	if cfg.Inert() {
		return nil, cmdcore.NoEndpoint(cmdcore.NoneInstalled, "who", nil)
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
		return nil, refreshFailure(cfg, err)
	}
	return whoEntries(ep.Who(), cfg.Owner), nil
}

// refreshFailure words a failure of the wait for who is online. A wait that ran out is not a failure to connect: the
// server answered, the endpoints did not, so it is "the directory could not be read" (hint mash doctor), exit 1.
// Anything else is worded as a failure to open is.
func refreshFailure(cfg config.Config, err error) error {
	if !errors.Is(err, context.DeadlineExceeded) {
		if f := openFailure(cfg, err); f != err {
			return f
		}
		return cliframe.Failf("the directory could not be read: %s", err).WithHint("mash doctor")
	}
	return cliframe.Failf("the directory could not be read: the endpoints did not answer in time").WithHint("mash doctor")
}

// wakeSettings are the wake settings mash inbox reports against: wake_count (0 = off) and wake_age (zero = off; the text
// is as it was written).
type wakeSettings struct {
	count int
	age   time.Duration
	text  string
}

func wakeOf(cfg config.Config) wakeSettings {
	return wakeSettings{count: cfg.WakeCount, age: cfg.WakeAge, text: cfg.WakeAgeText}
}

// plainFail is err as one failure line: the "mash: " the endpoint package puts in front of its messages is the tool's
// name, which "error:" already stands for.
func plainFail(err error) error {
	var ce *cliframe.Error
	if errors.As(err, &ce) {
		return err
	}
	return cliframe.Failf("%s", strings.TrimPrefix(err.Error(), "mash: "))
}

// mailItem is a message as the commands read it: the sender's full address, the body safe to print.
func mailItem(m render.Mail, floor uint64, waiting bool) cmds.MailItem {
	it := cmds.MailItem{Number: int(m.Seq), From: sap.Display(m.From), Sent: m.At, New: waiting && m.Seq > floor, Urgent: m.Urgent,
		Kind: m.Kind.String(), Trust: string(m.Trust), Size: len(m.Body), ReplyTo: int(m.ReplyTo), Body: render.Plain(m.Body)}
	if origin, seq, ok := strings.Cut(m.Via, " #"); ok {
		it.Relay = sap.Display(origin) + " #" + seq
	}
	return it
}

// inboxOn is the inbox command's read of ep. The counts come from the server's consumer (what has not reached the
// session), not from the messages listed: a listing is only a window. A message is new when it is above the consumer's
// acknowledged floor. How many waiting messages are urgent is counted over the newest endpoint.UrgentLook of them.
func inboxOn(ctx context.Context, ep *endpoint.Endpoint, q cmds.InboxQuery, w wakeSettings) (cmds.InboxView, error) {
	r, err := ep.ReadInbox(ctx, endpoint.InboxQuery{OnlyNew: q.OnlyNew, After: uint64(q.After), Limit: q.Limit})
	if err != nil {
		return cmds.InboxView{}, plainFail(err)
	}
	st := r.State
	v := cmds.InboxView{Endpoint: ep.Display(), Total: st.Total, Waiting: st.Waiting, NewUrgent: r.NewUrgent, OldestWaiting: st.OldestWaiting,
		WakeCount: w.count, WakeAge: w.age, WakeAgeText: w.text, Messages: make([]cmds.MailItem, 0, len(r.Mail))}
	for _, m := range r.Mail {
		v.Messages = append(v.Messages, mailItem(m, st.AckFloor, st.Waiting > 0))
	}
	return v, nil
}

// messageOn is the read command's read of ep: the message, or none when the inbox has no such one.
func messageOn(ctx context.Context, ep *endpoint.Endpoint, number int) (cmds.ReadView, error) {
	m, err := ep.ReadRef(ctx, endpoint.MsgRef{Seq: uint64(number)})
	var nm *endpoint.NoMessageError
	if errors.As(err, &nm) {
		return cmds.ReadView{Endpoint: ep.Display()}, nil
	}
	var rf *endpoint.RefusedMessageError
	if errors.As(err, &rf) {
		return cmds.ReadView{}, cmdcore.RefusedMessage(int(rf.Seq))
	}
	if err != nil {
		return cmds.ReadView{}, plainFail(err)
	}
	it := mailItem(m, 0, false)
	return cmds.ReadView{Endpoint: ep.Display(), Message: &it}, nil
}

// openMailbox opens the endpoint for a command that reads its inbox, within the command's deadline, and words a refusal
// the way the rules do. The caller closes the endpoint and cancels the returned context.
func (s *cliSvc) openMailbox(ctx context.Context) (context.Context, *endpoint.Endpoint, func(), error) {
	cfg := s.cfg
	if cfg.Inert() {
		return nil, nil, nil, cmdcore.NoEndpoint(cmdcore.NoneInstalled, "inbox", nil)
	}
	ctx, cancel := context.WithTimeout(ctx, cliDeadline(cfg))
	openCtx, openCancel := context.WithTimeout(ctx, cfg.OpenTimeout)
	ep, err := endpoint.OpenClientWith(openCtx, cfg, endpoint.ClientOptions{Inbox: true})
	openCancel()
	if err != nil {
		cancel()
		var se *cmds.SignInError
		if f := openFailure(cfg, err); errors.As(f, &se) {
			return nil, nil, nil, cmdcore.NotSignedIn(se.Addr)
		} else {
			return nil, nil, nil, f
		}
	}
	return ctx, ep, func() { ep.Close(); cancel() }, nil
}

// Inbox lists the inbox of the endpoint, in one open: the first wave of reads also fetches the inbox stream.
func (s *cliSvc) Inbox(ctx context.Context, q cmds.InboxQuery) (cmds.InboxView, error) {
	ctx, ep, done, err := s.openMailbox(ctx)
	if err != nil {
		return cmds.InboxView{}, err
	}
	defer done()
	c, cc := context.WithTimeout(ctx, s.cfg.CallTimeout)
	defer cc()
	return inboxOn(c, ep, q, wakeOf(s.cfg))
}

// Message reads one message. With no session online nothing can mark it read: at a shell it stays new (see mcpSvc).
func (s *cliSvc) Message(ctx context.Context, number int) (cmds.ReadView, error) {
	ctx, ep, done, err := s.openMailbox(ctx)
	if err != nil {
		return cmds.ReadView{}, err
	}
	defer done()
	c, cc := context.WithTimeout(ctx, s.cfg.CallTimeout)
	defer cc()
	return messageOn(c, ep, number)
}

// mcpSvc is the service the MCP tools run against: the session's endpoint, which may not be usable yet.
type mcpSvc struct {
	buildVersion
	state       *atomic.Pointer[sessionState]
	name        string
	callTimeout time.Duration
	wake        wakeSettings
}

func (s *mcpSvc) Inbox(ctx context.Context, q cmds.InboxQuery) (cmds.InboxView, error) {
	ep, err := s.state.Load().usable(s.name)
	if err != nil {
		return cmds.InboxView{}, err
	}
	ctx, cancel := context.WithTimeout(ctx, s.callTimeout)
	defer cancel()
	return inboxOn(ctx, ep, q, s.wake)
}

// Message reads one message. The tool's AfterWrite (see readAck) acknowledges it once the result is written.
func (s *mcpSvc) Message(ctx context.Context, number int) (cmds.ReadView, error) {
	ep, err := s.state.Load().usable(s.name)
	if err != nil {
		return cmds.ReadView{}, err
	}
	ctx, cancel := context.WithTimeout(ctx, s.callTimeout)
	defer cancel()
	return messageOn(ctx, ep, number)
}

// readAck is the read tool's AfterWrite: only after the result is written to the client does the model have the body,
// and that counts as seen, so serve acks the message, for one a hook listed in a digest (any mode, ADR 0046) and for one
// waiting in a session proven hookless (no hook will show it). Everywhere else the message stays held for a hook.
func (s *mcpSvc) readAck(raw json.RawMessage) {
	var a struct {
		Number int `json:"number"`
	}
	if json.Unmarshal(raw, &a) != nil || a.Number < 1 {
		return
	}
	if ep, err := s.state.Load().usable(s.name); err == nil {
		ep.MarkRead(endpoint.MsgRef{Seq: uint64(a.Number)})
	}
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
	return whoEntries(ps, ep.Address().Owner), nil
}
