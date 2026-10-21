// Package mcp is a minimal Model Context Protocol server over stdio, enough to
// be a Claude Code channel: initialize (declaring claude/channel), tools, ping,
// and notifications/claude/channel. Messages are newline-delimited JSON-RPC.
package mcp

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"sync"
)

// Tool is one MCP tool. A Call error is returned to Claude as an error result.
type Tool struct {
	Name        string
	Description string
	Schema      map[string]any
	Call        func(ctx context.Context, args json.RawMessage) (string, error)
}

// Server serves one stdio connection.
type Server struct {
	in           io.Reader
	out          io.Writer
	name         string
	version      string
	instructions string
	tools        map[string]Tool
	order        []string

	wmu   sync.Mutex
	once  sync.Once
	ready chan struct{}
}

// New returns a server; Run starts it.
func New(in io.Reader, out io.Writer, name, version, instructions string, tools []Tool) *Server {
	s := &Server{in: in, out: out, name: name, version: version, instructions: instructions,
		tools: map[string]Tool{}, ready: make(chan struct{})}
	for _, t := range tools {
		s.tools[t.Name] = t
		s.order = append(s.order, t.Name)
	}
	return s
}

// Initialized is closed once the client has sent notifications/initialized.
func (s *Server) Initialized() <-chan struct{} { return s.ready }

type request struct {
	ID     json.RawMessage `json:"id,omitempty"`
	Method string          `json:"method"`
	Params json.RawMessage `json:"params,omitempty"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

func (s *Server) write(v any) error {
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	s.wmu.Lock()
	defer s.wmu.Unlock()
	_, err = s.out.Write(append(b, '\n'))
	return err
}

func (s *Server) result(id json.RawMessage, r any) error {
	return s.write(map[string]any{"jsonrpc": "2.0", "id": id, "result": r})
}

func (s *Server) fail(id json.RawMessage, code int, msg string) error {
	return s.write(map[string]any{"jsonrpc": "2.0", "id": id, "error": rpcError{code, msg}})
}

// Notify pushes a channel event into the session.
func (s *Server) Notify(content string, meta map[string]string) error {
	params := map[string]any{"content": content}
	if meta != nil {
		params["meta"] = meta
	}
	return s.write(map[string]any{"jsonrpc": "2.0", "method": "notifications/claude/channel", "params": params})
}

// Run serves until the input ends or ctx is cancelled.
func (s *Server) Run(ctx context.Context) error {
	lines := make(chan []byte)
	errc := make(chan error, 1)
	go func() {
		sc := bufio.NewScanner(s.in)
		sc.Buffer(make([]byte, 64<<10), 16<<20)
		for sc.Scan() {
			lines <- append([]byte(nil), sc.Bytes()...)
		}
		errc <- sc.Err()
		close(lines)
	}()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case line, ok := <-lines:
			if !ok {
				return <-errc
			}
			s.handle(ctx, line)
		}
	}
}

func (s *Server) handle(ctx context.Context, line []byte) {
	var req request
	if err := json.Unmarshal(line, &req); err != nil || req.Method == "" {
		return // not a request we can answer
	}
	isCall := len(req.ID) > 0 && string(req.ID) != "null"
	switch req.Method {
	case "initialize":
		var p struct {
			ProtocolVersion string `json:"protocolVersion"`
		}
		_ = json.Unmarshal(req.Params, &p)
		_ = s.result(req.ID, map[string]any{
			"protocolVersion": p.ProtocolVersion,
			"serverInfo":      map[string]string{"name": s.name, "version": s.version},
			"capabilities": map[string]any{
				"tools":        map[string]any{},
				"experimental": map[string]any{"claude/channel": map[string]any{}},
			},
			"instructions": s.instructions,
		})
	case "notifications/initialized":
		s.once.Do(func() { close(s.ready) })
	case "ping":
		_ = s.result(req.ID, map[string]any{})
	case "tools/list":
		list := make([]map[string]any, 0, len(s.order))
		for _, n := range s.order {
			t := s.tools[n]
			list = append(list, map[string]any{"name": t.Name, "description": t.Description, "inputSchema": t.Schema})
		}
		_ = s.result(req.ID, map[string]any{"tools": list})
	case "tools/call":
		var p struct {
			Name      string          `json:"name"`
			Arguments json.RawMessage `json:"arguments"`
		}
		_ = json.Unmarshal(req.Params, &p)
		go s.call(ctx, req.ID, p.Name, p.Arguments)
	default:
		if isCall {
			_ = s.fail(req.ID, -32601, "method not supported: "+req.Method)
		}
	}
}

func (s *Server) call(ctx context.Context, id json.RawMessage, name string, args json.RawMessage) {
	text := func(t string, isErr bool) {
		r := map[string]any{"content": []map[string]string{{"type": "text", "text": t}}}
		if isErr {
			r["isError"] = true
		}
		_ = s.result(id, r)
	}
	t, ok := s.tools[name]
	if !ok {
		text("unknown tool "+name, true)
		return
	}
	if len(args) == 0 {
		args = json.RawMessage("{}")
	}
	out, err := t.Call(ctx, args)
	if err != nil {
		text(err.Error(), true)
		return
	}
	text(out, false)
}
