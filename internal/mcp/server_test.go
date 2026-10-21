package mcp

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"
	"testing"
	"time"
)

type harness struct {
	in     *io.PipeWriter
	lines  chan map[string]any
	s      *Server
	cancel context.CancelFunc
}

func start(t *testing.T, tools []Tool) *harness {
	t.Helper()
	inR, inW := io.Pipe()
	outR, outW := io.Pipe()
	s := New(inR, outW, "mash", "0.1.0", "be nice", tools)
	ctx, cancel := context.WithCancel(context.Background())
	go func() { _ = s.Run(ctx); outW.Close() }()
	h := &harness{in: inW, lines: make(chan map[string]any, 1000), s: s, cancel: cancel}
	go func() {
		sc := bufio.NewScanner(outR)
		sc.Buffer(make([]byte, 1<<20), 1<<24)
		for sc.Scan() {
			var m map[string]any
			if err := json.Unmarshal(sc.Bytes(), &m); err != nil {
				t.Errorf("unparseable line %q: %v", sc.Text(), err)
				continue
			}
			h.lines <- m
		}
		close(h.lines)
	}()
	t.Cleanup(func() { inW.Close(); cancel() })
	return h
}

func (h *harness) send(t *testing.T, msg string) {
	t.Helper()
	if _, err := io.WriteString(h.in, msg+"\n"); err != nil {
		t.Fatal(err)
	}
}

func (h *harness) next(t *testing.T) map[string]any {
	t.Helper()
	select {
	case m := <-h.lines:
		return m
	case <-time.After(3 * time.Second):
		t.Fatal("no output")
	}
	return nil
}

func (h *harness) initialize(t *testing.T) map[string]any {
	h.send(t, `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"claude-code","version":"x"}}}`)
	r := h.next(t)
	h.send(t, `{"jsonrpc":"2.0","method":"notifications/initialized"}`)
	return r
}

func TestInitializeEchoesVersionAndChannelCapability(t *testing.T) {
	h := start(t, nil)
	r := h.initialize(t)
	res := r["result"].(map[string]any)
	if res["protocolVersion"] != "2025-06-18" || res["instructions"] != "be nice" {
		t.Fatalf("result %v", res)
	}
	caps := res["capabilities"].(map[string]any)
	if _, ok := caps["experimental"].(map[string]any)["claude/channel"]; !ok {
		t.Fatalf("no claude/channel capability: %v", caps)
	}
	if _, ok := caps["tools"]; !ok {
		t.Fatalf("no tools capability: %v", caps)
	}
	if res["serverInfo"].(map[string]any)["name"] != "mash" {
		t.Fatalf("serverInfo %v", res["serverInfo"])
	}
	select {
	case <-h.s.Initialized():
	case <-time.After(time.Second):
		t.Fatal("initialized not signalled")
	}
}

func echoTool() Tool {
	return Tool{Name: "echo", Description: "echo", Schema: map[string]any{"type": "object",
		"properties": map[string]any{"text": map[string]any{"type": "string"}}, "required": []string{"text"}},
		Call: func(_ context.Context, args json.RawMessage) (string, error) {
			var a struct{ Text string }
			if err := json.Unmarshal(args, &a); err != nil {
				return "", err
			}
			if a.Text == "fail" {
				return "", errors.New("it failed")
			}
			return "echo: " + a.Text, nil
		}}
}

func TestToolsListAndCall(t *testing.T) {
	h := start(t, []Tool{echoTool()})
	h.initialize(t)
	h.send(t, `{"jsonrpc":"2.0","id":2,"method":"tools/list"}`)
	r := h.next(t)
	tools := r["result"].(map[string]any)["tools"].([]any)
	if len(tools) != 1 || tools[0].(map[string]any)["name"] != "echo" || tools[0].(map[string]any)["inputSchema"] == nil {
		t.Fatalf("tools %v", tools)
	}
	h.send(t, `{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"echo","arguments":{"text":"hi"}}}`)
	r = h.next(t)
	res := r["result"].(map[string]any)
	c := res["content"].([]any)[0].(map[string]any)
	if r["id"].(float64) != 3 || c["type"] != "text" || c["text"] != "echo: hi" || res["isError"] == true {
		t.Fatalf("call %v", r)
	}
}

func TestToolErrorIsResultNotRPCError(t *testing.T) {
	h := start(t, []Tool{echoTool()})
	h.initialize(t)
	h.send(t, `{"jsonrpc":"2.0","id":4,"method":"tools/call","params":{"name":"echo","arguments":{"text":"fail"}}}`)
	r := h.next(t)
	if r["error"] != nil {
		t.Fatalf("tool failure must be a result: %v", r)
	}
	res := r["result"].(map[string]any)
	if res["isError"] != true || !strings.Contains(res["content"].([]any)[0].(map[string]any)["text"].(string), "it failed") {
		t.Fatalf("result %v", res)
	}
	h.send(t, `{"jsonrpc":"2.0","id":5,"method":"tools/call","params":{"name":"nope","arguments":{}}}`)
	if r := h.next(t); r["result"].(map[string]any)["isError"] != true {
		t.Fatalf("unknown tool: %v", r)
	}
}

func TestNotifyAfterInitialized(t *testing.T) {
	h := start(t, nil)
	h.initialize(t)
	<-h.s.Initialized()
	if err := h.s.Notify("you have mail", map[string]string{"count": "1"}); err != nil {
		t.Fatal(err)
	}
	r := h.next(t)
	p := r["params"].(map[string]any)
	if r["method"] != "notifications/claude/channel" || p["content"] != "you have mail" ||
		p["meta"].(map[string]any)["count"] != "1" || r["id"] != nil {
		t.Fatalf("notification %v", r)
	}
}

func TestPing(t *testing.T) {
	h := start(t, nil)
	h.send(t, `{"jsonrpc":"2.0","id":"p1","method":"ping"}`)
	r := h.next(t)
	if r["id"] != "p1" || r["result"] == nil {
		t.Fatalf("ping %v", r)
	}
}

func TestUnknownMethod(t *testing.T) {
	h := start(t, nil)
	h.send(t, `{"jsonrpc":"2.0","id":9,"method":"resources/list"}`)
	r := h.next(t)
	if r["error"].(map[string]any)["code"].(float64) != -32601 {
		t.Fatalf("unknown method %v", r)
	}
	h.send(t, `{"jsonrpc":"2.0","method":"notifications/cancelled","params":{}}`)
	h.send(t, `not json at all`)
	h.send(t, `{"jsonrpc":"2.0","id":10,"method":"ping"}`)
	if r := h.next(t); r["id"].(float64) != 10 {
		t.Fatalf("server stopped after bad input: %v", r)
	}
}

func TestConcurrentWritesAreLineAtomic(t *testing.T) {
	h := start(t, nil)
	h.initialize(t)
	var wg sync.WaitGroup
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_ = h.s.Notify(fmt.Sprintf("n%d %s", i, strings.Repeat("x", 5000)), nil)
		}(i)
	}
	wg.Wait()
	for i := 0; i < 100; i++ {
		if m := h.next(t); m["method"] != "notifications/claude/channel" {
			t.Fatalf("line %d: %v", i, m)
		}
	}
}

func TestRunEndsWhenInputCloses(t *testing.T) {
	inR, inW := io.Pipe()
	s := New(inR, io.Discard, "mash", "0", "", nil)
	done := make(chan error, 1)
	go func() { done <- s.Run(context.Background()) }()
	inW.Close()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Run did not return on EOF")
	}
}

func TestChannelReadyWaitsForToolsList(t *testing.T) {
	h := start(t, nil)
	h.initialize(t)
	select {
	case <-h.s.ChannelReady():
		t.Fatal("channel ready before tools/list: events sent now are dropped by Claude Code")
	case <-time.After(300 * time.Millisecond):
	}
	h.send(t, `{"jsonrpc":"2.0","id":2,"method":"tools/list"}`)
	h.next(t)
	select {
	case <-h.s.ChannelReady():
	case <-time.After(2 * time.Second):
		t.Fatal("channel never ready after tools/list")
	}
}

func TestChannelReadyFallback(t *testing.T) {
	old := ChannelFallback
	ChannelFallback = 200 * time.Millisecond
	t.Cleanup(func() { ChannelFallback = old })
	h := start(t, nil)
	h.initialize(t)
	select {
	case <-h.s.ChannelReady():
	case <-time.After(2 * time.Second):
		t.Fatal("no fallback without tools/list")
	}
}
