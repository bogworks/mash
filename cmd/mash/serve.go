package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/randybias/mash/internal/config"
	"github.com/randybias/mash/internal/endpoint"
	"github.com/randybias/mash/internal/mcp"
	"github.com/randybias/mash/internal/render"
)

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
	openCtx, openCancel := context.WithTimeout(ctx, 20*time.Second)
	ep, err := endpoint.Open(openCtx, cfg)
	openCancel()
	if err != nil {
		fmt.Fprintln(stderr, err)
		return degraded(endpoint.ErrorInstructions(cfg.Name, err), true)
	}
	defer ep.Close()

	policy, _ := os.ReadFile(filepath.Join(cfg.Dir, "policy.md"))
	srv := mcp.New(stdin, stdout, "mash", endpoint.Version,
		endpoint.Instructions(cfg.Name, cfg.Owner, string(policy)), tools(ep))

	go func() {
		select {
		case <-srv.ChannelReady():
		case <-ctx.Done():
			return
		}
		if err := ep.StartService(ctx, func(p endpoint.PromptEvent) {
			text := fmt.Sprintf("mash: live prompt from %s [%s] (request_id %s). The caller is waiting: answer with the reply tool.\n\n%s",
				p.From, p.Trust, p.RequestID, p.Text)
			_ = srv.Notify(text, map[string]string{"request_id": p.RequestID, "from": p.From, "kind": "prompt"})
		}); err != nil {
			fmt.Fprintln(stderr, err)
			_ = srv.Notify(endpoint.ErrorInstructions(cfg.Name, err), map[string]string{"error": "true"})
		}
		if err := ep.Run(ctx, srv.Notify); err != nil {
			fmt.Fprintln(stderr, err)
			_ = srv.Notify(fmt.Sprintf("mash: endpoint %s stopped: %v. Mail is off until this session restarts.", cfg.Name, err),
				map[string]string{"error": "true"})
		}
	}()
	_ = srv.Run(ctx)
	return 0
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

func tools(ep *endpoint.Endpoint) []mcp.Tool {
	call := func(f func(context.Context, json.RawMessage) (string, error)) func(context.Context, json.RawMessage) (string, error) {
		return func(ctx context.Context, args json.RawMessage) (string, error) {
			ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
			defer cancel()
			return f(ctx, args)
		}
	}
	return []mcp.Tool{
		{Name: "send", Description: "Mail another mash endpoint by name. Returns the delivery receipt (#seq in their inbox).",
			Schema: schema(map[string]any{"to": str("endpoint name"), "text": str("message body (markdown)"),
				"in_reply_to": num("optional #seq of the message this answers")}, "to", "text"),
			Call: call(func(ctx context.Context, raw json.RawMessage) (string, error) {
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
			Call: call(func(ctx context.Context, raw json.RawMessage) (string, error) {
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
			Call: call(func(ctx context.Context, raw json.RawMessage) (string, error) {
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
			Call: call(func(context.Context, json.RawMessage) (string, error) {
				return whoText(ep.Who()), nil
			})},
		{Name: "reply", Description: "Answer a live prompt by its request_id. done=false streams a partial answer.",
			Schema: schema(map[string]any{"request_id": str("from the prompt event"), "text": str("your answer"),
				"done": flag("end the answer (default true)")}, "request_id", "text"),
			Call: call(func(_ context.Context, raw json.RawMessage) (string, error) {
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
