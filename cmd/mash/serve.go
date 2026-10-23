package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"time"

	"github.com/randybias/mash/internal/config"
	"github.com/randybias/mash/internal/endpoint"
	"github.com/randybias/mash/internal/mcp"
	"github.com/randybias/mash/internal/render"
)

// Retry timing for a session that starts while its endpoint's lease is held. Variables so tests can shorten
// them; the hooks are test barriers and nil in production.
var (
	leaseRetry  = 10 * time.Second // between attempts to take a held lease
	openTimeout = 20 * time.Second // bound on one Open (nats' own dial is not bounded by it)

	acquireAttempts atomic.Int64 // Opens started by the retry loop
	acquiredCount   atomic.Int64 // retry Opens that took the lease

	beforeAttemptHook func()
	afterOpenHook     func()
)

// sessionState is what the tools see: the live endpoint, the holder being waited out, or the error that ended
// the wait. It is replaced whole, never mutated.
type sessionState struct {
	ep      *endpoint.Endpoint
	waiting *endpoint.LeaseHeldError
	failed  error
}

// usable is the endpoint a tool call may use, or the reason it cannot.
func (s *sessionState) usable(name string) (*endpoint.Endpoint, error) {
	switch {
	case s.ep != nil:
		return s.ep, nil
	case s.failed != nil:
		return nil, errors.New(endpoint.ErrorInstructions(name, s.failed))
	default:
		return nil, fmt.Errorf("mash is waiting for endpoint %s (held by %s pid %s since %s); mail is off until it is free",
			name, s.waiting.Host, s.waiting.PID, s.waiting.Since)
	}
}

// waitingLine is appended to the normal instructions while the name is held.
const waitingLine = "\n\nmash is waiting for this endpoint's name to become free; mail is off until it does. " +
	"mash retries on its own and will tell you when mail is on."

// serve runs the endpoint manager for one Claude session until stdin closes.
func serve(parent context.Context, cfg config.Config, cfgErr error, stdin io.Reader, stdout, stderr io.Writer) int {
	ctx, cancel := context.WithCancel(parent)
	defer cancel()

	// Without a usable identity, still answer MCP (the plugin may be enabled
	// in every session) but expose no tools, and say why.
	degraded := func(instr string, notify bool) int {
		srv := mcp.New(stdin, stdout, "mash", endpoint.Version, instr, nil)
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
	// The first Open runs before the MCP server exists, as it always has: it is bounded by openTimeout, and a
	// stdin EOF during it is seen once it returns. Only a held lease is waited out; any other failure is
	// terminal and unchanged.
	openCtx, openCancel := context.WithTimeout(ctx, openTimeout)
	ep, err := endpoint.Open(openCtx, cfg)
	openCancel()
	var held *endpoint.LeaseHeldError
	if err != nil && !errors.As(err, &held) {
		fmt.Fprintln(stderr, err)
		return degraded(endpoint.ErrorInstructions(cfg.Name, err), true)
	}
	var state atomic.Pointer[sessionState]
	instr := ""
	policy, _ := os.ReadFile(filepath.Join(cfg.Dir, "policy.md"))
	instr = endpoint.Instructions(cfg.Name, cfg.Owner, string(policy))
	if ep != nil {
		state.Store(&sessionState{ep: ep})
	} else {
		state.Store(&sessionState{waiting: held})
		instr += waitingLine
	}
	srv := mcp.New(stdin, stdout, "mash", endpoint.Version, instr, tools(&state, cfg.Name))

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
		if ep == nil {
			var ok bool
			if ep, ok = acquire(ctx, cfg, srv, &state, held, stderr, notify); !ok {
				return
			}
		}
		defer ep.Close()
		select {
		case <-srv.ChannelReady():
		case <-ctx.Done():
			return
		}
		if waited := held != nil; waited {
			notify(fmt.Sprintf("mash: endpoint %s is online; mail is on for this session.", cfg.Name))
		}
		if err := ep.StartService(ctx, func(p endpoint.PromptEvent) {
			text := fmt.Sprintf("mash: live prompt from %s [%s] (request_id %s). The caller is waiting: answer with the reply tool.\n\n%s",
				p.From, p.Trust, p.RequestID, p.Text)
			_ = srv.Notify(text, map[string]string{"request_id": p.RequestID, "from": p.From, "kind": "prompt"})
		}); err != nil {
			fmt.Fprintln(stderr, err)
			_ = srv.Notify(endpoint.ErrorInstructions(cfg.Name, err), map[string]string{"error": "true"})
		}
		if err := ep.Run(ctx, srv.Notify); err != nil && ctx.Err() == nil {
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

// acquire retries the lease take every leaseRetry until it succeeds, ctx ends or a non-held error stops it.
// It sends the held notice once Claude Code is ready, and the terminal notice on a non-held failure. It
// returns the endpoint it now owns; on false the caller has nothing to close.
func acquire(ctx context.Context, cfg config.Config, srv *mcp.Server, state *atomic.Pointer[sessionState],
	held *endpoint.LeaseHeldError, stderr io.Writer, notify func(string)) (*endpoint.Endpoint, bool) {
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
			held = h
			state.Store(&sessionState{waiting: h})
			timer.Reset(leaseRetry)
		default: // a non-held failure ends the wait: terminal for this session
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
				notify(held.Error() + "; mail is off for this session. mash is retrying every " + leaseRetry.String() + ".")
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
				c, cancel := context.WithTimeout(ctx, openTimeout)
				defer cancel()
				ep, err := endpoint.Open(c, cfg)
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

func tools(state *atomic.Pointer[sessionState], name string) []mcp.Tool {
	// Each handler loads the state once and uses the endpoint it finds, so the tools are registered once and
	// work on whichever endpoint the session ends up with.
	call := func(f func(context.Context, *endpoint.Endpoint, json.RawMessage) (string, error)) func(context.Context, json.RawMessage) (string, error) {
		return func(ctx context.Context, args json.RawMessage) (string, error) {
			ep, err := state.Load().usable(name)
			if err != nil {
				return "", err
			}
			ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
			defer cancel()
			return f(ctx, ep, args)
		}
	}
	return []mcp.Tool{
		{Name: "send", Description: "Mail another mash endpoint by name. Returns the delivery receipt (#seq in their inbox).",
			Schema: schema(map[string]any{"to": str("endpoint name"), "text": str("message body (markdown)"),
				"in_reply_to": num("optional #seq of the message this answers")}, "to", "text"),
			Call: call(func(ctx context.Context, ep *endpoint.Endpoint, raw json.RawMessage) (string, error) {
				var a struct {
					To        string `json:"to"`
					Text      string `json:"text"`
					InReplyTo uint64 `json:"in_reply_to"`
				}
				if err := json.Unmarshal(raw, &a); err != nil {
					return "", err
				}
				seq, err := ep.Send(ctx, a.To, a.Text, a.InReplyTo)
				if err != nil {
					return "", err
				}
				return fmt.Sprintf("sent #%d to %s", seq, a.To), nil
			})},
		{Name: "read", Description: "Read one message from your inbox by #seq.",
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
		{Name: "who", Description: "List mash endpoints and whether each is online.",
			Schema: schema(map[string]any{}),
			Call: call(func(_ context.Context, ep *endpoint.Endpoint, _ json.RawMessage) (string, error) {
				return whoText(ep.Who()), nil
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
	}
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

func whoText(ps []endpoint.Presence) string {
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
		b.WriteString("\n")
	}
	return strings.TrimRight(b.String(), "\n")
}
