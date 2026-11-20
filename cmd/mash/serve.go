package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net"
	"os"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"

	"github.com/bogworks/mash/internal/cmdcore"
	"github.com/bogworks/mash/internal/config"
	"github.com/bogworks/mash/internal/endpoint"
	"github.com/bogworks/mash/internal/host"
	"github.com/bogworks/mash/internal/mcp"
	"github.com/bogworks/mash/internal/sap"
	"github.com/bogworks/mash/internal/spool"
)

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

	// A held lease whose holder is an earlier serve of this same session (ADR 0092) that was not taken over (the
	// compare-and-set lost to a write made since) is retried after sameSessionRetry, up to sameSessionTries times in a
	// row, then every leaseRetry.
	sameSessionRetry = time.Second
	sameSessionTries = 5

	// A serve superseded by a newer serve of its session exits when its parent changes; it looks every parentWatchEvery.
	parentWatchEvery = time.Second
	getppid          = os.Getppid // replaceable in tests

	acquireAttempts atomic.Int64 // Opens started by the retry loop
	acquiredCount   atomic.Int64 // retry Opens that took the lease

	beforeAttemptHook func()
	afterOpenHook     func()
)

// applyRetryTiming sets the retry timers from the configured values that are set; the package variables stay the
// test knobs, and an unset key leaves them alone.
func applyRetryTiming(cfg config.Config) {
	if cfg.LeaseRetry > 0 {
		leaseRetry = cfg.LeaseRetry
	}
	if cfg.TransientBudget > 0 {
		transientBudget = cfg.TransientBudget
	}
	if cfg.TransientMax > 0 {
		transientMax = cfg.TransientMax
	}
}

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
	// superseded: a newer mash serve of the same session took the lease from this one; it names that serve. This
	// server is inactive: it answers the tools with that and exits when its client closes it or its parent changes.
	superseded *endpoint.LeaseLostError
	// lost: another session took the lease from this one after it started. Serve ends right after; status and doctor
	// say so in the meantime.
	lost *endpoint.LeaseLostError
}

// supersededText is what a superseded serve says: the newer serve of the same session handles the mail.
func supersededText(name string, l *endpoint.LeaseLostError) string {
	return fmt.Sprintf("mash: this server was superseded by a newer mash serve of the same session (pid %s, host %s); "+
		"mail for endpoint %s is handled there, and this server is inactive. Use the mash tools of the newer server.",
		l.PID, l.Host, name)
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
	case s.superseded != nil:
		return nil, errors.New(supersededText(name, s.superseded))
	case s.lost != nil:
		return nil, fmt.Errorf("mash: this session lost this endpoint (%s) to a newer one; mail is off for this session", name)
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
	applyRetryTiming(cfg)
	ctx, cancel := context.WithCancel(parent)
	defer cancel()

	// Without a usable identity, still answer MCP (the plugin may be enabled in every session) with only the
	// tools that need no endpoint, version, status and doctor, and say why.
	src := &statusSource{cfg: cfg, cfgErr: cfgErr, pluginRoot: pluginRoot}
	reportSettings(src, stderr)
	degraded := func(instr string, notify bool) int {
		srv := mcp.New(stdin, stdout, "mash", endpoint.CurrentVersion(), instr, infoTools(src))
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
		return degraded(endpoint.ErrorInstructions(cfg.DisplayName(), cfgErr), true)
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
			return degraded(endpoint.ErrorInstructions(cfg.DisplayName(), perr), true)
		}
	}
	// The first Open runs before the MCP server exists, as it always has: it is bounded by openTimeout, and a
	// stdin EOF during it is seen once it returns. A held lease is waited out, and a transient failure (a
	// restarted session meeting a broker blip) is retried the same way; any other failure is terminal.
	// Recorded before the first attempt: a resumed copy's hook can run while it is still in progress (ADR 0043).
	// Removed on every exit, and at once when the first attempt binds (the bind also removes it).
	clearWaiter, noteHeld := noteWaiting(cfg)
	defer clearWaiter()
	startPPID := getppid()
	openCtx, openCancel := context.WithTimeout(ctx, openBound(cfg))
	ep, err := openEndpoint(openCtx, cfg)
	openCancel()
	if ep != nil {
		clearWaiter()
	}
	var held *endpoint.LeaseHeldError
	var startErr error // a transient first failure, retried
	if errors.As(err, &held) {
		noteHeld(held)
	}
	if err != nil && !errors.As(err, &held) {
		if !transient(err) {
			fmt.Fprintln(stderr, err)
			src.state = &atomic.Pointer[sessionState]{}
			src.state.Store(&sessionState{failed: err})
			return degraded(endpoint.ErrorInstructions(cfg.DisplayName(), err), true)
		}
		startErr = err
		fmt.Fprintf(stderr, "%v; retrying (a transient error does not end the wait)\n", err)
	}
	var state atomic.Pointer[sessionState]
	instr := ""
	instr = endpoint.Instructions(cfg.DisplayName(), cfg.Owner, string(policy))
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
			if ep, ok = acquire(ctx, cfg, srv, &state, held, startErr, stderr, notify, noteHeld); !ok {
				return
			}
			clearWaiter() // this serve now holds the lease
		}
		closeEP := sync.OnceFunc(ep.Close)
		defer closeEP()
		select {
		case <-srv.ChannelReady():
		case <-ctx.Done():
			return
		}
		if waited {
			notify(fmt.Sprintf("mash: endpoint %s is online; mail is on for this session.", cfg.DisplayName()))
		}
		go pingClient(ctx, srv, ep)
		go warnLeaseRollup(ctx, ep, stderr)
		// Hook delivery: once the plugin's hook has run in this session, mail goes through the spool (endpoint/hook.go).
		// Without a spool, mail stays on the channel.
		// A host that takes no channel delivery is hooked from the start, and without a spool its mail is held
		// unacked in the inbox: the channel would ack what the host never sees.
		noChannel := adapter.SpoolsFromStart()
		// MASH_SPOOL_REQUIRED is the fail-closed delivery knob, read here and nowhere else: mail is never delivered
		// by the channel, so what the spool cannot take is held unacked in the inbox and a spool failure is reported.
		failClosed := cfg.SpoolRequired
		if noChannel || failClosed {
			ep.NoChannel()
		}
		if failClosed {
			ep.ReportSpoolFaults(func(msg string) {
				fmt.Fprintf(stderr, "mash: spool: %s\n", msg)
				notify("mash: spool: " + msg + ".")
			})
		}
		sdir, err := endpoint.SpoolDir(cfg)
		if err == nil {
			err = ep.SetSpool(spool.Spool{Dir: sdir}, adapter.OwnerToken(os.Getenv))
		}
		if err != nil {
			if failClosed {
				ep.NoteSpoolFault(err)
				go retrySpool(ctx, ep, func() (spool.Spool, error) {
					d, err := endpoint.SpoolDir(cfg)
					return spool.Spool{Dir: d}, err
				}, adapter.OwnerToken(os.Getenv))
			} else if cfg.SpoolFromStart && !noChannel {
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
		} else if noChannel || cfg.SpoolFromStart || failClosed {
			// Claude Code drops channel events that arrive before its first user message, and its first hook runs
			// with that message: only with a spool can early mail wait for a hook, so a failed SetSpool keeps the
			// channel (#16).
			ep.SpoolFromStart()
		}
		startSvc := func() {
			if err := ep.StartService(ctx, func(p endpoint.PromptEvent) {
				text, ok := promptNotice(p)
				if !ok { // unreachable under endpoint.MaxPromptBytes; answer the caller rather than show a cut notice
					_ = ep.Reply(p.RequestID, "mash: the prompt was too large to show and was not delivered", true)
					return
				}
				_ = srv.Notify(text, map[string]string{"request_id": p.RequestID, "from": p.From, "kind": "prompt"})
			}); err != nil {
				fmt.Fprintln(stderr, err)
				_ = srv.Notify(endpoint.ErrorInstructions(cfg.DisplayName(), err), map[string]string{"error": "true"})
			}
		}
		// After a takeover from a live serve of this session the SAP service is registered only once the grace has
		// passed and the lease is confirmed held, so the earlier serve and this one never answer prompts together.
		// stopSvc stops the service and bars a later start; a start in flight finishes first (svcMu).
		var svcMu sync.Mutex
		svcStopped := false
		svcCtx, svcCancel := context.WithCancel(ctx)
		svcDone := make(chan struct{})
		stopSvc := func() {
			svcMu.Lock()
			svcStopped = true
			svcCancel()
			ep.StopService()
			svcMu.Unlock()
		}
		defer func() { stopSvc(); <-svcDone }() // runs before closeEP
		if ep.TakeoverPending() {
			go func() {
				defer close(svcDone)
				if ep.AwaitTakeover(svcCtx) != nil {
					return // Run reports a lease loss; nothing is registered
				}
				svcMu.Lock()
				defer svcMu.Unlock()
				if !svcStopped {
					startSvc()
				}
			}()
		} else {
			close(svcDone)
			startSvc()
		}
		if err := ep.Run(ctx, adapter.Waker(srv.Notify)); err != nil && ctx.Err() == nil {
			var lostTo *endpoint.LeaseLostError
			if errors.As(err, &lostTo) && lostTo.SameSession {
				// A newer serve of this same session holds the lease (ADR 0092): this one has stopped consuming and
				// refreshing. It stays up, inactive and labelled, until its client closes it or its parent changes.
				stopSvc()
				state.Store(&sessionState{superseded: lostTo})
				closeEP()
				fmt.Fprintf(stderr, "%v; superseded by a newer mash serve of this session (pid %s); this server is inactive\n", err, lostTo.PID)
				told := make(chan struct{})
				go func() {
					_ = srv.Notify(supersededText(cfg.DisplayName(), lostTo), map[string]string{"error": "true"})
					close(told)
				}()
				select {
				case <-told:
				case <-time.After(2 * time.Second):
				}
				t := time.NewTicker(parentWatchEvery)
				defer t.Stop()
				for {
					select {
					case <-ctx.Done():
						return
					case <-t.C:
						if getppid() != startPPID {
							cancel()
							return
						}
					}
				}
			}
			// The lease is gone (another session holds the name): stop the SAP
			// service first, so the two never split prompts or heartbeat as
			// one, then tell Claude, but never let a blocked stdout hold up
			// the shutdown. Lease loss after a start stays terminal.
			lost.Store(true)
			var lostErr *endpoint.LeaseLostError
			if errors.As(err, &lostErr) {
				state.Store(&sessionState{lost: lostErr})
			}
			stopSvc()
			fmt.Fprintln(stderr, err)
			told := make(chan struct{})
			go func() {
				_ = srv.Notify(fmt.Sprintf("mash: endpoint %s stopped: %v. Mail is off for this session.", cfg.DisplayName(), err),
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
	held *endpoint.LeaseHeldError, startErr error, stderr io.Writer, notify func(string), noteHeld func(*endpoint.LeaseHeldError)) (*endpoint.Endpoint, bool) {
	type result struct {
		ep  *endpoint.Endpoint
		err error
	}
	results := make(chan result, 1)
	ready := srv.ChannelReady()
	sameTries := 0
	// heldDelay is the wait before the next attempt against holder h: short for an earlier serve of this same session
	// that was not taken over, a few times in a row, else leaseRetry.
	heldDelay := func(h *endpoint.LeaseHeldError) time.Duration {
		if h == nil || !h.SameSession {
			sameTries = 0
			return leaseRetry
		}
		if sameTries < sameSessionTries {
			sameTries++
			return sameSessionRetry
		}
		return leaseRetry
	}
	first := leaseRetry
	if held != nil {
		first = heldDelay(held)
	}
	timer := time.NewTimer(first)
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
			// The holder is read again on every attempt: what the tools, status, the hook notice and Claude are told
			// is the holder now, not the one the first attempt found.
			changed := held == nil || held.PID != h.PID || held.Host != h.Host
			held, lastTransient = h, nil
			state.Store(&sessionState{waiting: h})
			noteHeld(h)
			transientSince, backoff = time.Time{}, leaseRetry
			timer.Reset(heldDelay(h))
			if changed && heldSent && ready == nil {
				notify(h.Error() + "; mail is off for this session. mash is retrying every " + leaseRetry.String() + ".")
			}
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
				notify(endpoint.ErrorInstructions(cfg.DisplayName(), r.err))
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
				notify(endpoint.ErrorInstructions(cfg.DisplayName(), failed))
				return nil, false
			}
			if !heldSent {
				heldSent = true
				if held != nil {
					notify(held.Error() + "; mail is off for this session. mash is retrying every " + leaseRetry.String() + ".")
				} else {
					notify(fmt.Sprintf("mash: endpoint %s could not reach its server (%v); mail is off for this session. mash is retrying.",
						cfg.DisplayName(), lastTransient))
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

func tools(state *atomic.Pointer[sessionState], src *statusSource) []mcp.Tool {
	name := src.cfg.DisplayName()
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
	svc := &mcpSvc{buildVersion: buildVersion{pluginRoot: src.pluginRoot}, state: state, name: name, callTimeout: callTimeout, wake: wakeOf(src.cfg)}
	table := newCommands(func(context.Context) (*cmdcore.Env, error) { return &cmdcore.Env{Svc: svc}, nil })
	send, _ := table.Lookup("send")
	who, _ := table.Lookup("who")
	inbox, _ := table.Lookup("inbox")
	read, _ := table.Lookup("read")
	readTool := read.Tool(table.Env).MCP()
	readTool.AfterWrite = svc.readAck
	return append([]mcp.Tool{
		send.Tool(table.Env).MCP(),
		inbox.Tool(table.Env).MCP(),
		readTool,
		who.Tool(table.Env).MCP(),
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

// PromptTextMax is the most bytes of a live prompt's text. It is the endpoint's limit, the one number: the service
// refuses a longer prompt to its caller before the session hears of it, so the notice below never cuts.
const PromptTextMax = endpoint.MaxPromptBytes

// promptNoticeMax bounds the escaped text. Escaping turns one byte into at most 4 characters (`\xNN`), so a prompt
// within PromptTextMax cannot exceed 4x; 8x is the guard. The guard cannot trip with that limit; if it ever did, the
// prompt is refused as a whole rather than cut.
const promptNoticeMax = 8 * PromptTextMax

// promptNotice is the channel text for a live prompt, and false if the escaped text is over promptNoticeMax. Everything
// the sender controls is shown with control characters escaped (the text keeps "\n" and "\t"). The text is never cut.
func promptNotice(p endpoint.PromptEvent) (string, bool) {
	body := config.EscapeText(p.Text)
	if len(body) > promptNoticeMax {
		return "", false
	}
	return fmt.Sprintf("mash: live prompt from %s [%s] (request_id %s). The caller is waiting: answer with the reply tool.\n\n%s",
		config.Clean(sap.Display(p.From)), config.Clean(string(p.Trust)), config.Clean(p.RequestID), body), true
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
// removes the record, and the function that records who holds the lease (the spool's lease-wait state, which a hook of
// this session turns into a notice, ADR 0092); the removal removes that state too. A spool that cannot be written costs
// only these.
func noteWaiting(cfg config.Config) (clear func(), noteHeld func(*endpoint.LeaseHeldError)) {
	dir, err := endpoint.SpoolDir(cfg)
	if err != nil {
		return func() {}, func(*endpoint.LeaseHeldError) {}
	}
	s := spool.Spool{Dir: dir}
	ppid := os.Getppid()
	session := os.Getenv("CLAUDE_CODE_SESSION_ID")
	if s.Init() != nil {
		return func() {}, func(*endpoint.LeaseHeldError) {}
	}
	if unlock, err := s.Lock(); err == nil {
		_ = s.NoteWaiter(spool.WaiterRecord{PID: ppid, Session: session})
		unlock()
	}
	// instance tells this serve's state from another serve's (the files are shared, and a pid can be reused or, in a test,
	// shared): the state is removed only if it still names this serve.
	var rb [8]byte
	_, _ = rand.Read(rb[:])
	instance := hex.EncodeToString(rb[:])
	return func() {
			if unlock, err := s.Lock(); err == nil {
				_ = s.ClearWaiter(ppid)
				_ = s.ClearWaitState(os.Getpid(), instance)
				unlock()
			}
		}, func(h *endpoint.LeaseHeldError) {
			if unlock, err := s.Lock(); err == nil {
				_ = s.NoteWaitState(spool.WaitState{HolderPID: h.PID, HolderHost: h.Host, HolderSince: h.Since,
					ServePID: os.Getpid(), Instance: instance, ParentPID: ppid, Session: session, Updated: time.Now().UTC()})
				unlock()
			}
		}
}

// leaseRollupWarned makes the lease bucket warning appear at most once per process.
var leaseRollupWarned sync.Once

// warnLeaseRollup prints one stderr line when the lease bucket still allows rollup writes. It runs beside the start,
// never delays it, and says nothing when the check cannot be made.
func warnLeaseRollup(ctx context.Context, ep *endpoint.Endpoint, stderr io.Writer) {
	if ep.LeaseRollupWarning(ctx) {
		leaseRollupWarned.Do(func() { fmt.Fprintln(stderr, endpoint.LeaseRollupStartWarning) })
	}
}

// spoolRetryEvery is how often a spool that failed at start is tried again under MASH_SPOOL_REQUIRED.
const spoolRetryEvery = 5 * time.Second

// retrySpool binds the spool in the background once it works, until ctx ends. The held mail was never acked, so the
// inbox redelivers it and it is spooled from the first message.
func retrySpool(ctx context.Context, ep *endpoint.Endpoint, mk func() (spool.Spool, error), owner string) {
	t := time.NewTicker(spoolRetryEvery)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
		s, err := mk()
		if err == nil {
			err = ep.BindSpool(ctx, s, owner, true)
		}
		if err == nil {
			return
		}
		if ctx.Err() != nil {
			return
		}
		ep.NoteSpoolFault(err)
	}
}
