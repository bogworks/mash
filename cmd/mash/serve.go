package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"

	"github.com/bogworks/mash/internal/config"
	"github.com/bogworks/mash/internal/endpoint"
	"github.com/bogworks/mash/internal/host"
	"github.com/bogworks/mash/internal/kind"
	"github.com/bogworks/mash/internal/mcp"
	"github.com/bogworks/mash/internal/render"
	"github.com/bogworks/mash/internal/spool"
)

// sendArgsError explains a send call that has no text: it names the parameter and lists the argument keys
// received that the tool does not know, so a model that guessed `body` or `message` can correct itself.
// It returns nil when text is present.
func sendArgsError(raw json.RawMessage) error {
	var args map[string]json.RawMessage
	_ = json.Unmarshal(raw, &args)
	if t, ok := args["text"]; ok {
		var s string
		if json.Unmarshal(t, &s) == nil && s != "" {
			return nil
		}
	}
	var unknown []string
	for k := range args {
		switch k {
		case "to", "text", "in_reply_to", "urgent", "kind", "id":
		default:
			unknown = append(unknown, k)
		}
	}
	sort.Strings(unknown)
	got := "no text"
	if len(unknown) > 0 {
		got = strings.Join(unknown, ", ")
	}
	return fmt.Errorf("send needs `text` (the message body); got: %s", got)
}

// Retry timing for a session that starts while its endpoint's lease is held. Variables so tests can shorten
// them; the hooks are test barriers and nil in production.
var (
	leaseRetry  = 10 * time.Second // between attempts to take a held lease
	openTimeout time.Duration      // a test's bound on one Open; zero uses the configured one (nats' own dial is not bounded by it)

	// A transient failure (a timeout, no responders, a dropped connection) while waiting is retried with a backoff
	// that doubles from leaseRetry up to transientMax; failures in a row for longer than transientBudget end the
	// wait like any other error.
	transientMax    = 2 * time.Minute
	transientBudget = 15 * time.Minute

	openEndpoint = endpoint.Open // replaceable in tests
	// serve pings its MCP client every mcpPingEvery, waiting up to mcpPingWait for the answer; an answered ping is
	// recorded in the lease (mcp_seen): the client is alive and reading serve's output, read with no Claude turn. It
	// does not show that channel events are taken (endpoint.leaseValue).
	mcpPingEvery = 30 * time.Second
	mcpPingWait  = 10 * time.Second

	acquireAttempts atomic.Int64 // Opens started by the retry loop
	acquiredCount   atomic.Int64 // retry Opens that took the lease

	beforeAttemptHook func()
	afterOpenHook     func()
)

// openBound is the bound on one Open: the configured open timeout, unless a test set one.
func openBound(cfg config.Config) time.Duration {
	if openTimeout > 0 {
		return openTimeout
	}
	return cfg.OpenTimeout
}

// sessionState is what the tools see: the live endpoint, the holder being waited out, the transient error being
// retried, or the error that ended the wait. It is replaced whole, never mutated.
type sessionState struct {
	ep       *endpoint.Endpoint
	waiting  *endpoint.LeaseHeldError
	retrying error
	failed   error
}

// usable is the endpoint a tool call may use, or the reason it cannot.
func (s *sessionState) usable(name string) (*endpoint.Endpoint, error) {
	switch {
	case s.ep != nil:
		return s.ep, nil
	case s.failed != nil:
		return nil, errors.New(endpoint.ErrorInstructions(name, s.failed))
	case s.retrying != nil:
		return nil, fmt.Errorf("mash could not reach the server for endpoint %s (%v) and is retrying; mail is off until it connects",
			name, s.retrying)
	default:
		return nil, fmt.Errorf("mash is waiting for endpoint %s (held by %s pid %s since %s); mail is off until it is free",
			name, s.waiting.Host, s.waiting.PID, s.waiting.Since)
	}
}

// waitingLine is appended to the normal instructions while the name is held.
const waitingLine = "\n\nmash is waiting for this endpoint's name to become free; mail is off until it does. " +
	"mash retries on its own and will tell you when mail is on."

// retryingLine is appended to the normal instructions while a transient first failure is retried.
const retryingLine = "\n\nmash could not reach its server yet and is retrying; mail is off until it connects. " +
	"mash retries on its own and will tell you when mail is on."

// serve runs the endpoint manager for one Claude session until stdin closes.
func serve(parent context.Context, cfg config.Config, cfgErr error, adapter host.Adapter, pluginRoot string, stdin io.Reader, stdout, stderr io.Writer) int {
	ctx, cancel := context.WithCancel(parent)
	defer cancel()

	// Without a usable identity, still answer MCP (the plugin may be enabled in every session) with only the
	// tools that need no endpoint, version and status, and say why.
	src := &statusSource{cfg: cfg, cfgErr: cfgErr, pluginRoot: pluginRoot, srv: &atomic.Pointer[mcp.Server]{}}
	reportSettings(src, stderr)
	degraded := func(instr string, notify bool) int {
		srv := mcp.New(stdin, stdout, "mash", endpoint.CurrentVersion(), instr, infoTools(src))
		src.srv.Store(srv)
		if notify {
			go func() {
				select {
				case <-srv.ChannelReady():
					_ = srv.Notify(instr, map[string]string{"error": "true"})
				case <-ctx.Done():
				}
			}()
		}
		_ = srv.Run(ctx)
		return 0
	}
	if cfgErr != nil {
		fmt.Fprintln(stderr, cfgErr)
		return degraded(endpoint.ErrorInstructions(cfg.Name, cfgErr), true)
	}
	if cfg.Inert() {
		return degraded(endpoint.InertInstructions, false)
	}
	// A policy.md whose path is absent is an empty policy (a dangling symlink is present, so not absent); one that exists but cannot be read is a configuration failure, never
	// a silent drop of the operator's restrictions.
	var policy []byte
	if cfg.Dir != "" {
		path := cfg.Find("policy.md")
		var perr error
		if policy, perr = os.ReadFile(path); perr != nil && !(errors.Is(perr, fs.ErrNotExist) && config.Absent(path)) {
			perr = fmt.Errorf("mash: reading %s: %w", path, perr)
			src.cfgErr = perr
			fmt.Fprintln(stderr, perr)
			return degraded(endpoint.ErrorInstructions(cfg.Name, perr), true)
		}
	}
	// The first Open runs before the MCP server exists, as it always has: it is bounded by openTimeout, and a
	// stdin EOF during it is seen once it returns. A held lease is waited out, and a transient failure (a
	// restarted session meeting a broker blip) is retried the same way; any other failure is terminal.
	// Recorded before the first attempt: a resumed copy's hook can run while it is still in progress (ADR 0043).
	// Removed on every exit, and at once when the first attempt binds (the bind also removes it, spool.OwnRecord).
	clearWaiter := noteWaiting(cfg)
	defer clearWaiter()
	openCtx, openCancel := context.WithTimeout(ctx, openBound(cfg))
	ep, err := openEndpoint(openCtx, cfg)
	openCancel()
	if ep != nil {
		clearWaiter()
	}
	var held *endpoint.LeaseHeldError
	var startErr error // a transient first failure, retried
	if err != nil && !errors.As(err, &held) {
		if !transient(err) {
			fmt.Fprintln(stderr, err)
			src.state = &atomic.Pointer[sessionState]{}
			src.state.Store(&sessionState{failed: err})
			return degraded(endpoint.ErrorInstructions(cfg.Name, err), true)
		}
		startErr = err
		fmt.Fprintf(stderr, "%v; retrying (a transient error does not end the wait)\n", err)
	}
	var state atomic.Pointer[sessionState]
	instr := ""
	instr = endpoint.Instructions(cfg.Name, cfg.Owner, string(policy))
	switch {
	case ep != nil:
		state.Store(&sessionState{ep: ep})
	case held != nil:
		state.Store(&sessionState{waiting: held})
		instr += waitingLine
	default:
		state.Store(&sessionState{retrying: startErr})
		instr += retryingLine
	}
	src.state = &state
	srv := mcp.New(stdin, stdout, "mash", endpoint.CurrentVersion(), instr, tools(&state, src))
	src.srv.Store(srv)
	// Each ended tool call is evidence for the hookless detector (endpoint/hookless.go): a hook runs after every
	// successful tool call, so two calls well apart with no hook between them mean the hooks do not run.
	srv.OnToolCall = func(string) {
		if st := state.Load(); st != nil && st.ep != nil {
			st.ep.ToolCalled(time.Now())
		}
	}

	// One worker owns the endpoint from acquisition to Close, and is joined before serve returns: every
	// successful Open is closed by it, even when cancellation wins the race with publication. It is also the
	// only sender of notices, and sends none before Claude Code can take channel events.
	var lost atomic.Bool
	done := make(chan struct{})
	go func() {
		defer close(done)
		notify := func(text string) {
			_ = srv.Notify(text, nil)
		}
		waited := ep == nil
		if waited {
			var ok bool
			if ep, ok = acquire(ctx, cfg, srv, &state, held, startErr, stderr, notify); !ok {
				return
			}
			clearWaiter() // this serve now holds the lease
		}
		defer ep.Close()
		select {
		case <-srv.ChannelReady():
		case <-ctx.Done():
			return
		}
		if waited {
			notify(fmt.Sprintf("mash: endpoint %s is online; mail is on for this session.", cfg.Name))
		}
		go pingClient(ctx, srv, ep)
		// Hook delivery: once the plugin's hook has run in this session, mail goes through the spool (endpoint/hook.go).
		// Without a spool, mail stays on the channel.
		// A host that takes no channel delivery is hooked from the start, and without a spool its mail is held
		// unacked in the inbox: the channel would ack what the host never sees.
		noChannel := adapter.SpoolsFromStart()
		if noChannel {
			ep.NoChannel()
		}
		boundAt := time.Now() // before the bind, so a hook run once this session owns the spool is never missed
		if err := ep.SetSpool(spool.Spool{Dir: spool.Dir(cfg.StateHome, cfg.Owner, cfg.Name)}, adapter.OwnerToken(os.Getenv)); err != nil {
			if cfg.SpoolFromStart && !noChannel {
				// Claude drops channel events before its first prompt, yet the channel acks: without a spool, hold
				// the mail unacked and tell the model to use inbox/read (#16).
				ep.NoChannel()
				fmt.Fprintf(stderr, "mash: spool: %v; mail is held in the inbox, unacked (MASH_SPOOL_FROM_START): use the inbox and read tools\n", err)
				notify(fmt.Sprintf("mash: spool: %v; mail is held in the inbox, unacked (MASH_SPOOL_FROM_START): use the inbox and read tools.", err))
			} else if noChannel {
				fmt.Fprintf(stderr, "mash: spool: %v; mail is held in the inbox, unacked, because host %s takes no channel delivery\n", err, adapter.Name())
			} else {
				fmt.Fprintf(stderr, "mash: spool: %v; mail stays on the channel\n", err)
			}
		} else if src.owned.Store(&boundAt); noChannel || cfg.SpoolFromStart {
			// Claude Code drops channel events that arrive before its first user message, and its first hook runs
			// with that message: only with a spool can early mail wait for a hook, so a failed SetSpool keeps the
			// channel (#16).
			ep.SpoolFromStart()
		}
		if err := ep.StartService(ctx, func(p endpoint.PromptEvent) {
			text := fmt.Sprintf("mash: live prompt from %s [%s] (request_id %s). The caller is waiting: answer with the reply tool.\n\n%s",
				p.From, p.Trust, p.RequestID, p.Text)
			_ = srv.Notify(text, map[string]string{"request_id": p.RequestID, "from": p.From, "kind": "prompt"})
		}); err != nil {
			fmt.Fprintln(stderr, err)
			_ = srv.Notify(endpoint.ErrorInstructions(cfg.Name, err), map[string]string{"error": "true"})
		}
		if err := ep.Run(ctx, adapter.Waker(srv.Notify)); err != nil && ctx.Err() == nil {
			// The lease is gone (another session holds the name): stop the SAP
			// service first, so the two never split prompts or heartbeat as
			// one, then tell Claude, but never let a blocked stdout hold up
			// the shutdown. Lease loss after a start stays terminal.
			lost.Store(true)
			ep.StopService()
			fmt.Fprintln(stderr, err)
			told := make(chan struct{})
			go func() {
				_ = srv.Notify(fmt.Sprintf("mash: endpoint %s stopped: %v. Mail is off for this session.", cfg.Name, err),
					map[string]string{"error": "true"})
				close(told)
			}()
			select {
			case <-told:
			case <-time.After(2 * time.Second):
			}
			cancel()
		}
	}()
	_ = srv.Run(ctx)
	cancel()
	<-done
	if lost.Load() {
		return 1
	}
	return 0
}

// acquire retries the Open every leaseRetry (backing off after transient failures) until it succeeds, ctx ends
// or a permanent error stops it. It starts from the first Open's failure: a held lease, or else startErr, a
// transient error. It sends the waiting notice once Claude Code is ready, and the terminal notice on a permanent
// failure. It returns the endpoint it now owns; on false the caller has nothing to close.
func acquire(ctx context.Context, cfg config.Config, srv *mcp.Server, state *atomic.Pointer[sessionState],
	held *endpoint.LeaseHeldError, startErr error, stderr io.Writer, notify func(string)) (*endpoint.Endpoint, bool) {
	type result struct {
		ep  *endpoint.Endpoint
		err error
	}
	results := make(chan result, 1)
	ready := srv.ChannelReady()
	timer := time.NewTimer(leaseRetry)
	defer timer.Stop()
	inflight, heldSent := false, false
	var failed error
	var transientSince time.Time // the first of the transient failures in a row; zero when the last was not one
	backoff := leaseRetry
	lastTransient := startErr // the transient error being retried while no holder is known
	if startErr != nil {
		transientSince = time.Now()
	}
	drain := func() { // a started attempt is always received, and a success always closed
		if inflight {
			if r := <-results; r.ep != nil {
				r.ep.Close()
			}
		}
	}
	// process handles a finished attempt. stop is true when the loop must return (ep non-nil on success).
	process := func(r result) (ep *endpoint.Endpoint, stop bool) {
		inflight = false
		var h *endpoint.LeaseHeldError
		switch {
		case r.err == nil:
			acquiredCount.Add(1)
			state.Store(&sessionState{ep: r.ep})
			return r.ep, true
		case ctx.Err() != nil:
			return nil, true // cancellation is never "held"
		case errors.As(r.err, &h):
			held, lastTransient = h, nil
			state.Store(&sessionState{waiting: h})
			transientSince, backoff = time.Time{}, leaseRetry
			timer.Reset(leaseRetry)
		case transient(r.err) && (transientSince.IsZero() || time.Since(transientSince) < transientBudget):
			if transientSince.IsZero() {
				transientSince = time.Now()
				fmt.Fprintf(stderr, "%v; retrying (a transient error does not end the wait)\n", r.err)
			}
			if held == nil {
				lastTransient = r.err
				state.Store(&sessionState{retrying: r.err})
			}
			backoff = min(backoff*2, transientMax)
			timer.Reset(backoff)
		default: // a permanent failure, or transient ones past transientBudget, ends the wait: terminal for this session
			fmt.Fprintln(stderr, r.err)
			failed = r.err
			state.Store(&sessionState{failed: r.err})
			if ready == nil { // Claude is already listening: say so now
				notify(endpoint.ErrorInstructions(cfg.Name, r.err))
				return nil, true
			}
		}
		return nil, false
	}
	for {
		select {
		case <-ready:
			ready = nil
			// An attempt that finished before readiness is settled first, so a success is never announced as
			// "held" and a terminal failure is told at once.
			select {
			case r := <-results:
				if ep, stop := process(r); stop {
					return ep, ep != nil
				}
			default:
			}
			if failed != nil {
				notify(endpoint.ErrorInstructions(cfg.Name, failed))
				return nil, false
			}
			if !heldSent {
				heldSent = true
				if held != nil {
					notify(held.Error() + "; mail is off for this session. mash is retrying every " + leaseRetry.String() + ".")
				} else {
					notify(fmt.Sprintf("mash: endpoint %s could not reach its server (%v); mail is off for this session. mash is retrying.",
						cfg.Name, lastTransient))
				}
			}
		case <-timer.C:
			if inflight || failed != nil {
				continue
			}
			inflight = true
			acquireAttempts.Add(1)
			go func() {
				if beforeAttemptHook != nil {
					beforeAttemptHook()
				}
				c, cancel := context.WithTimeout(ctx, openBound(cfg))
				defer cancel()
				ep, err := openEndpoint(c, cfg)
				if ep != nil && afterOpenHook != nil {
					afterOpenHook()
				}
				results <- result{ep, err}
			}()
		case r := <-results:
			if ep, stop := process(r); stop {
				return ep, ep != nil
			}
		case <-ctx.Done():
			drain()
			return nil, false
		}
	}
}

func schema(props map[string]any, required ...string) map[string]any {
	s := map[string]any{"type": "object", "properties": props}
	if len(required) > 0 {
		s["required"] = required
	}
	return s
}

func str(desc string) map[string]any  { return map[string]any{"type": "string", "description": desc} }
func num(desc string) map[string]any  { return map[string]any{"type": "integer", "description": desc} }
func flag(desc string) map[string]any { return map[string]any{"type": "boolean", "description": desc} }

// kindProp is the send tool's `kind` parameter: a string enum of the kinds.
func kindProp() map[string]any {
	names := make([]string, len(kind.All))
	for i, k := range kind.All {
		names[i] = string(k)
	}
	return map[string]any{"type": "string", "enum": names,
		"description": "optional, default task: blocker|question|task wake an idle recipient; result|fyi (opt-in) wait for its next turn"}
}

func tools(state *atomic.Pointer[sessionState], src *statusSource) []mcp.Tool {
	name := src.cfg.Name
	callTimeout := src.cfg.CallTimeout
	// Each handler loads the state once and uses the endpoint it finds, so the tools are registered once and
	// work on whichever endpoint the session ends up with.
	call := func(f func(context.Context, *endpoint.Endpoint, json.RawMessage) (string, error)) func(context.Context, json.RawMessage) (string, error) {
		return func(ctx context.Context, args json.RawMessage) (string, error) {
			ep, err := state.Load().usable(name)
			if err != nil {
				return "", err
			}
			ctx, cancel := context.WithTimeout(ctx, callTimeout)
			defer cancel()
			return f(ctx, ep, args)
		}
	}
	return append([]mcp.Tool{
		{Name: "send", Description: "Mail another mash endpoint by name. Returns the delivery receipt (#seq in their inbox).",
			Schema: schema(map[string]any{"to": str("endpoint name"), "text": str("message body (markdown)"),
				"in_reply_to": num("optional #seq of the message this answers"),
				"urgent":      flag("optional: wake the recipient at once instead of after its idle wait; for what cannot wait"),
				"kind":        kindProp(),
				"id": str("optional idempotency key (1-128 printable ASCII characters, no spaces). A send repeated with the same id to the same " +
					"recipient within the stream's duplicate window (2 minutes) is stored once and reports a duplicate, so a retry after a timeout cannot deliver twice; " +
					"after the window a repeated id is a new message")}, "to", "text"),
			Call: call(func(ctx context.Context, ep *endpoint.Endpoint, raw json.RawMessage) (string, error) {
				var a struct {
					To        string `json:"to"`
					Text      string `json:"text"`
					InReplyTo uint64 `json:"in_reply_to"`
					Urgent    bool   `json:"urgent"`
					Kind      string `json:"kind"`
					ID        string `json:"id"`
				}
				if err := json.Unmarshal(raw, &a); err != nil {
					return "", err
				}
				if a.Text == "" {
					return "", sendArgsError(raw)
				}
				k, err := kind.Parse(a.Kind)
				if err != nil {
					return "", err
				}
				k, tagged := kind.Tag(k, a.InReplyTo, a.Text)
				res, err := ep.SendDetailed(ctx, a.To, a.Text, endpoint.SendOptions{InReplyTo: a.InReplyTo, Urgent: a.Urgent, Kind: k, ID: a.ID})
				if err != nil {
					return "", err
				}
				seq := res.Seq
				if res.Duplicate {
					return fmt.Sprintf("sent #%d to %s (duplicate of id %q: already delivered, nothing new stored)", seq, a.To, a.ID), nil
				}
				if tagged {
					return fmt.Sprintf("sent #%d to %s %s", seq, a.To, kind.TaggedNote), nil
				}
				return fmt.Sprintf("sent #%d to %s", seq, a.To), nil
			})},
		{Name: "read", Description: "Read one message from your inbox by #seq.",
			// Only after the result is written to the client has the model the body. Only in a session proven
			// hookless (no hook will show it) does that count as seen: serve acks it. Everywhere else the message
			// stays held for a hook.
			AfterWrite: func(raw json.RawMessage) {
				var a struct {
					Seq uint64 `json:"seq"`
				}
				if json.Unmarshal(raw, &a) == nil {
					if ep, err := state.Load().usable(name); err == nil {
						ep.MarkRead(a.Seq)
					}
				}
			},
			Schema: schema(map[string]any{"seq": num("message #seq")}, "seq"),
			Call: call(func(ctx context.Context, ep *endpoint.Endpoint, raw json.RawMessage) (string, error) {
				var a struct {
					Seq uint64 `json:"seq"`
				}
				if err := json.Unmarshal(raw, &a); err != nil {
					return "", err
				}
				m, err := ep.Read(ctx, a.Seq)
				if err != nil {
					return "", err
				}
				return render.One(m), nil
			})},
		{Name: "inbox", Description: "List recent messages in your inbox (newest last), one line each.",
			Schema: schema(map[string]any{"limit": num("how many (default 20, max 100)")}),
			Call: call(func(ctx context.Context, ep *endpoint.Endpoint, raw json.RawMessage) (string, error) {
				var a struct {
					Limit int `json:"limit"`
				}
				_ = json.Unmarshal(raw, &a)
				ms, err := ep.Inbox(ctx, clampLimit(a.Limit))
				if err != nil {
					return "", err
				}
				return inboxText(ms), nil
			})},
		{Name: "who", Description: "List mash endpoints and whether each is online, with their role, host and labels when recorded.",
			Schema: schema(map[string]any{}),
			Call: call(func(ctx context.Context, ep *endpoint.Endpoint, _ json.RawMessage) (string, error) {
				return whoText(ep.Who(), true, liveLeases(ctx, ep)), nil
			})},
		{Name: "reply", Description: "Answer a live prompt by its request_id. done=false streams a partial answer.",
			Schema: schema(map[string]any{"request_id": str("from the prompt event"), "text": str("your answer"),
				"done": flag("end the answer (default true)")}, "request_id", "text"),
			Call: call(func(_ context.Context, ep *endpoint.Endpoint, raw json.RawMessage) (string, error) {
				var a struct {
					RequestID string `json:"request_id"`
					Text      string `json:"text"`
					Done      *bool  `json:"done"`
				}
				if err := json.Unmarshal(raw, &a); err != nil {
					return "", err
				}
				done := a.Done == nil || *a.Done
				if err := ep.Reply(a.RequestID, a.Text, done); err != nil {
					return "", err
				}
				if done {
					return "answered", nil
				}
				return "partial answer sent", nil
			})},
	}, infoTools(src)...)
}

// versionText is the version tool's output: the binary line (the one `mash version` prints), the plugin's version and
// the endpoint. It needs no endpoint, so it answers while the name is held, and it never fails: a plugin version it
// cannot find is "unknown" with the reason.
func versionText(name, owner, pluginRoot string) string {
	ep := fmt.Sprintf("endpoint %s (owner %s)", name, owner)
	if name == "" {
		ep = "endpoint not set (no MASH_NAME)"
	}
	return strings.Join([]string{
		versionLine(endpoint.CurrentVersion(), endpoint.CurrentCommit(), nil),
		pluginLine(pluginRoot),
		ep,
	}, "\n")
}

// pluginLine is "plugin <version> (<root>)" from <root>/.claude-plugin/plugin.json, or "plugin unknown (<reason>)".
// root is $MASH_PLUGIN_ROOT, which the plugin's .mcp.json sets to ${CLAUDE_PLUGIN_ROOT}.
func pluginLine(root string) string {
	unknown := func(format string, a ...any) string { return "plugin unknown (" + fmt.Sprintf(format, a...) + ")" }
	if root == "" {
		return unknown("MASH_PLUGIN_ROOT is not set: an older plugin, or serve run by hand")
	}
	if strings.Contains(root, "${") {
		return unknown("MASH_PLUGIN_ROOT is not expanded: %s", root)
	}
	b, err := os.ReadFile(filepath.Join(root, ".claude-plugin", "plugin.json"))
	if err != nil {
		return unknown("%v", err)
	}
	var m struct {
		Version string `json:"version"`
	}
	if err := json.Unmarshal(b, &m); err != nil {
		return unknown("plugin.json is not valid JSON: %v", err)
	}
	if strings.TrimSpace(m.Version) == "" {
		return unknown("plugin.json has no version")
	}
	return fmt.Sprintf("plugin %s (%s)", m.Version, root)
}

func clampLimit(n int) int {
	switch {
	case n <= 0:
		return 20
	case n > 100:
		return 100
	}
	return n
}

func inboxText(ms []render.Mail) string {
	if len(ms) == 0 {
		return "inbox empty"
	}
	lines := make([]string, len(ms))
	for i, m := range ms {
		lines[i] = render.Line(m)
	}
	return strings.Join(lines, "\n")
}

// liveLeases is the live lease info whoText can show: this endpoint's own, the only lease its permissions read.
func liveLeases(ctx context.Context, ep *endpoint.Endpoint) map[string]endpoint.LeaseInfo {
	c, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if li, ok := ep.OwnLease(c); ok {
		return map[string]endpoint.LeaseInfo{ep.Name(): li}
	}
	return nil
}

// whoText renders the directory with presence. The default lines are parsed by other tools (a launcher): never change
// them. long appends the optional directory metadata and, from live, the lease's host and version.
func whoText(ps []endpoint.Presence, long bool, live map[string]endpoint.LeaseInfo) string {
	if len(ps) == 0 {
		return "no endpoints"
	}
	var b strings.Builder
	for _, p := range ps {
		state := "offline"
		if p.Online {
			state = "online"
		}
		fmt.Fprintf(&b, "%s\t%s", p.Record.Name, state)
		if !p.LastSeen.IsZero() {
			fmt.Fprintf(&b, "\tseen %s ago", time.Since(p.LastSeen).Round(time.Second))
		}
		if p.Record.Note != "" {
			fmt.Fprintf(&b, "\t%s", p.Record.Note)
		}
		if long {
			r := p.Record
			for _, f := range []struct{ k, v string }{{"role", r.Role}, {"host", r.Host}} {
				if f.v != "" {
					fmt.Fprintf(&b, "\t%s %s", f.k, f.v)
				}
			}
			lk := make([]string, 0, len(r.Labels))
			for k := range r.Labels {
				lk = append(lk, k)
			}
			sort.Strings(lk)
			for _, k := range lk {
				fmt.Fprintf(&b, "\tlabel.%s=%s", k, r.Labels[k])
			}
			if li, ok := live[r.Name]; ok {
				if li.Host != "" {
					fmt.Fprintf(&b, "\tlease host %s", li.Host)
				}
				if li.Version != "" {
					fmt.Fprintf(&b, "\tlease version %s", li.Version)
				}
				if li.Host != "" && r.Host != "" && li.Host != r.Host {
					fmt.Fprintf(&b, "\tlease host %s differs from directory host %s", li.Host, r.Host)
				}
			}
		}
		b.WriteString("\n")
	}
	return strings.TrimRight(b.String(), "\n")
}

// transient reports whether err is a failure that may fix itself (the broker is slow, restarting or briefly
// unreachable), as opposed to one that will not (credentials, permissions, a missing directory record, bad config).
// Only the kinds listed here are transient: anything unrecognised stays terminal.
func transient(err error) bool {
	// jetstream.ErrNoStreamResponse is a KV write's form of no responders: the client turns ErrNoResponders on a
	// publish into it, so a lease Create during a broker outage fails with it (codex H1 on #8).
	for _, t := range []error{nats.ErrTimeout, nats.ErrNoResponders, jetstream.ErrNoStreamResponse, nats.ErrConnectionClosed,
		nats.ErrNoServers, nats.ErrConnectionReconnecting, nats.ErrDisconnected, context.DeadlineExceeded, io.EOF,
		io.ErrUnexpectedEOF, syscall.ECONNREFUSED, syscall.ECONNRESET, syscall.EPIPE} {
		if errors.Is(err, t) {
			return true
		}
	}
	var ne net.Error
	return errors.As(err, &ne) && ne.Timeout()
}

// pingClient pings the MCP client at once and then every mcpPingEvery until ctx ends, recording each answered ping
// on ep. An unanswered or still-pending ping records nothing, so mcp_seen ages.
func pingClient(ctx context.Context, srv *mcp.Server, ep *endpoint.Endpoint) {
	t := time.NewTicker(mcpPingEvery)
	defer t.Stop()
	for {
		c, cancel := context.WithTimeout(ctx, mcpPingWait)
		if srv.Ping(c) == nil {
			ep.SetMCPSeen(time.Now())
		}
		cancel()
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

// noteWaiting records this serve in its endpoint's spool as waiting for the lease (spool.NoteWaiter), so a hook of the
// same session that descends from this process is told it is another launch (ADR 0043). It returns the function that
// removes the record. A spool that cannot be written costs only that detection.
func noteWaiting(cfg config.Config) func() {
	s := spool.Spool{Dir: spool.Dir(cfg.StateHome, cfg.Owner, cfg.Name)}
	ppid := os.Getppid()
	if s.Init() != nil {
		return func() {}
	}
	if unlock, err := s.Lock(); err == nil {
		_ = s.NoteWaiter(spool.WaiterRecord{PID: ppid, Session: os.Getenv("CLAUDE_CODE_SESSION_ID")})
		unlock()
	}
	return func() {
		if unlock, err := s.Lock(); err == nil {
			_ = s.ClearWaiter(ppid)
			unlock()
		}
	}
}
