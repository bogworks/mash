// Package mcp is a minimal Model Context Protocol server over stdio, enough to
// be a Claude Code channel: initialize (declaring claude/channel), tools, ping,
// and notifications/claude/channel. Messages are newline-delimited JSON-RPC.
package mcp

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"io"
	"strconv"
	"sync"
	"sync/atomic"
	"time"
)

// Tool is one MCP tool. A Call error is returned to Claude as an error result.
type Tool struct {
	Name        string
	Description string
	Schema      map[string]any
	Call        func(ctx context.Context, args json.RawMessage) (string, error)
	// AfterWrite, when set, runs with the call's arguments only after Call succeeded and its result was written to
	// the client without error: for effects that must not happen unless the client received the result.
	AfterWrite func(args json.RawMessage)
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

	// OnToolCall, when set before Run, is called with a tool's name each time a tool call has ended, successful or not.
	OnToolCall func(name string)

	wmu       sync.Mutex
	once      sync.Once
	ready     chan struct{}
	listOnce  sync.Once
	listed    chan struct{}
	chanReady chan struct{}

	pmu     sync.Mutex
	pings   map[string]chan struct{} // outstanding pings to the client, by request id
	pingN   int
	writing atomic.Bool // a ping's write has not returned: the client is not reading stdout

	notified   atomic.Int64 // channel notifications written
	notifiedAt atomic.Int64 // unix nanoseconds of the last one
}

// ErrPingBusy is Ping's answer while an earlier ping is still being written: the client is not draining stdout, so
// a second write would only queue behind the first.
var ErrPingBusy = errors.New("mcp: an earlier ping is still being written")

// Claude Code drops channel events that arrive before it has registered the
// channel, which happens around its first tools/list after initialize. These
// bound the wait.
var (
	ChannelGrace    = 500 * time.Millisecond
	ChannelFallback = 3 * time.Second
)

// supportedVersions are the MCP protocol revisions mash speaks, oldest first;
// the last is the newest. Never add 2026-07-28 or anything newer, and never
// echo a client's version unchecked: Claude Code does not register a channel
// server that negotiates revision 2026-07-28, so every session would silently
// lose its mail wake.
//
// For the same reason mash must keep answering server/discover with
// method-not-found: a successful discover lets Claude Code take the 2026-07-28
// handshake, and channels are lost.
var supportedVersions = []string{"2024-11-05", "2025-03-26", "2025-06-18"}

// negotiateVersion answers the client's requested revision when mash supports
// it, otherwise the newest revision mash supports (also for an empty request).
func negotiateVersion(requested string) string {
	for _, v := range supportedVersions {
		if v == requested {
			return v
		}
	}
	return supportedVersions[len(supportedVersions)-1]
}

// New returns a server; Run starts it.
func New(in io.Reader, out io.Writer, name, version, instructions string, tools []Tool) *Server {
	s := &Server{in: in, out: out, name: name, version: version, instructions: instructions,
		tools: map[string]Tool{}, ready: make(chan struct{}), listed: make(chan struct{}), chanReady: make(chan struct{}),
		pings: map[string]chan struct{}{}}
	grace, fallback := ChannelGrace, ChannelFallback // read once: tests change them
	go func() {
		<-s.ready
		select {
		case <-s.listed:
		case <-time.After(fallback):
		}
		time.Sleep(grace)
		close(s.chanReady)
	}()
	for _, t := range tools {
		s.tools[t.Name] = t
		s.order = append(s.order, t.Name)
	}
	return s
}

// Initialized is closed once the client has sent notifications/initialized.
func (s *Server) Initialized() <-chan struct{} { return s.ready }

// ChannelReady is closed when channel events are expected to be delivered:
// after initialized and the client's first tools/list (plus a short grace),
// or after ChannelFallback without one. Send events only after it closes.
func (s *Server) ChannelReady() <-chan struct{} { return s.chanReady }

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

// Ping sends the client a ping request and waits for its answer until ctx ends. Any answer, a result or an error,
// proves the client is reading this server's output and handling requests, which a notification written to
// stdout cannot (the write returns once the bytes are in the pipe). Only one ping is written at a time: while one
// write has not returned, Ping returns ErrPingBusy.
func (s *Server) Ping(ctx context.Context) error {
	if !s.writing.CompareAndSwap(false, true) {
		return ErrPingBusy
	}
	s.pmu.Lock()
	s.pingN++
	id := "mash-ping-" + strconv.Itoa(s.pingN)
	answered := make(chan struct{})
	s.pings[id] = answered
	s.pmu.Unlock()
	defer func() {
		s.pmu.Lock()
		delete(s.pings, id)
		s.pmu.Unlock()
	}()
	wrote := make(chan error, 1)
	go func() {
		wrote <- s.write(map[string]any{"jsonrpc": "2.0", "id": id, "method": "ping"})
		s.writing.Store(false)
	}()
	select {
	case err := <-wrote:
		if err != nil {
			return err
		}
	case <-ctx.Done():
		return ctx.Err()
	}
	select {
	case <-answered:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// answer routes a response from the client to the ping waiting for it; a response to nothing outstanding (a ping
// already given up on) is dropped.
func (s *Server) answer(id json.RawMessage) {
	var key string
	if json.Unmarshal(id, &key) != nil {
		return
	}
	s.pmu.Lock()
	ch, ok := s.pings[key]
	if ok {
		delete(s.pings, key)
	}
	s.pmu.Unlock()
	if ok {
		close(ch)
	}
}

// Notify pushes a channel event into the session.
func (s *Server) Notify(content string, meta map[string]string) error {
	params := map[string]any{"content": content}
	if meta != nil {
		params["meta"] = meta
	}
	err := s.write(map[string]any{"jsonrpc": "2.0", "method": "notifications/claude/channel", "params": params})
	if err == nil {
		s.notified.Add(1)
		s.notifiedAt.Store(time.Now().UnixNano())
	}
	return err
}

// Notified is how many channel notifications have been written to the client, and when the last was (zero if none).
// A write is not delivery: mash cannot see whether Claude Code takes channel events.
func (s *Server) Notified() (int64, time.Time) {
	n := s.notified.Load()
	if n == 0 {
		return 0, time.Time{}
	}
	return n, time.Unix(0, s.notifiedAt.Load())
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
	if err := json.Unmarshal(line, &req); err != nil {
		return // not a message we can read
	}
	if req.Method == "" {
		if len(req.ID) > 0 && string(req.ID) != "null" {
			s.answer(req.ID) // a response to a request of ours (a ping)
		}
		return
	}
	isCall := len(req.ID) > 0 && string(req.ID) != "null"
	switch req.Method {
	case "initialize":
		var p struct {
			ProtocolVersion string `json:"protocolVersion"`
		}
		_ = json.Unmarshal(req.Params, &p)
		_ = s.result(req.ID, map[string]any{
			"protocolVersion": negotiateVersion(p.ProtocolVersion),
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
		s.listOnce.Do(func() { close(s.listed) })
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
	text := func(t string, isErr bool) error {
		r := map[string]any{"content": []map[string]string{{"type": "text", "text": t}}}
		if isErr {
			r["isError"] = true
		}
		return s.result(id, r)
	}
	t, ok := s.tools[name]
	if !ok {
		_ = text("unknown tool "+name, true)
		return
	}
	if len(args) == 0 {
		args = json.RawMessage("{}")
	}
	out, err := t.Call(ctx, args)
	if s.OnToolCall != nil {
		s.OnToolCall(name)
	}
	if err != nil {
		_ = text(err.Error(), true)
		return
	}
	if text(out, false) == nil && t.AfterWrite != nil {
		t.AfterWrite(args)
	}
}
