package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"testing"
)

func structuredTool() Tool {
	return Tool{Name: "st", Description: "st", Schema: map[string]any{"type": "object"},
		CallStructured: func(_ context.Context, args json.RawMessage) (string, any, error) {
			var a struct{ Mode string }
			_ = json.Unmarshal(args, &a)
			switch a.Mode {
			case "fail":
				return "", map[string]any{"x": 1}, errors.New("it failed")
			case "nil":
				return "plain", nil, nil
			}
			return "text out", map[string]any{"count": float64(2), "names": []any{"a", "b"}}, nil
		}}
}

func TestStructuredContent(t *testing.T) {
	obj := map[string]any{"count": float64(2), "names": []any{"a", "b"}}
	cases := []struct {
		name, request, mode, negotiated string
		wantStructured                  any // nil: the key must be absent
		wantText                        string
		wantErr                         bool
	}{
		{"2025-06-18 carries object", "2025-06-18", "ok", "2025-06-18", obj, "text out", false},
		{"2025-03-26 text only", "2025-03-26", "ok", "2025-03-26", nil, "text out", false},
		{"2024-11-05 text only", "2024-11-05", "ok", "2024-11-05", nil, "text out", false},
		{"unknown newer negotiates to latest", "2099-01-01", "ok", "2025-06-18", obj, "text out", false},
		{"2026-07-28 negotiates to latest", "2026-07-28", "ok", "2025-06-18", obj, "text out", false},
		{"nil result emits none", "2025-06-18", "nil", "2025-06-18", nil, "plain", false},
		{"error is text only", "2025-06-18", "fail", "2025-06-18", nil, "it failed", true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			h := start(t, []Tool{structuredTool()})
			h.send(t, `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"`+c.request+`","capabilities":{},"clientInfo":{"name":"x","version":"1"}}}`)
			got := h.next(t)["result"].(map[string]any)["protocolVersion"]
			if got != c.negotiated {
				t.Fatalf("negotiated %v, want %s", got, c.negotiated)
			}
			h.send(t, `{"jsonrpc":"2.0","method":"notifications/initialized"}`)
			h.send(t, `{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"st","arguments":{"mode":"`+c.mode+`"}}}`)
			res := h.next(t)["result"].(map[string]any)
			if txt := res["content"].([]any)[0].(map[string]any)["text"]; txt != c.wantText {
				t.Fatalf("text %v, want %q", txt, c.wantText)
			}
			if (res["isError"] == true) != c.wantErr {
				t.Fatalf("isError %v, want %v", res["isError"], c.wantErr)
			}
			sc, has := res["structuredContent"]
			if c.wantStructured == nil {
				if has {
					t.Fatalf("unexpected structuredContent %v", sc)
				}
			} else if !reflect.DeepEqual(sc, c.wantStructured) {
				t.Fatalf("structuredContent %v, want %v", sc, c.wantStructured)
			}
		})
	}
}

func TestStructuredBeforeInitializeIsTextOnly(t *testing.T) {
	h := start(t, []Tool{structuredTool()})
	h.send(t, `{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"st","arguments":{}}}`)
	if _, has := h.next(t)["result"].(map[string]any)["structuredContent"]; has {
		t.Fatal("structuredContent before a negotiated revision")
	}
}

func TestToolsListDeclaresNoOutputSchemaAndOldCallUnchanged(t *testing.T) {
	h := start(t, []Tool{structuredTool(), echoTool()})
	h.initialize(t)
	h.send(t, `{"jsonrpc":"2.0","id":2,"method":"tools/list"}`)
	for _, tl := range h.next(t)["result"].(map[string]any)["tools"].([]any) {
		if _, has := tl.(map[string]any)["outputSchema"]; has {
			t.Fatalf("outputSchema declared: %v", tl)
		}
	}
	h.send(t, `{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"echo","arguments":{"text":"hi"}}}`)
	res := h.next(t)["result"].(map[string]any)
	if _, has := res["structuredContent"]; has || res["content"].([]any)[0].(map[string]any)["text"] != "echo: hi" {
		t.Fatalf("old-style Call changed: %v", res)
	}
}
