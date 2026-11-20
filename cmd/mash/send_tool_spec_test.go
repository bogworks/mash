package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// The send tool in tools/list carries the description spec/mash-mcp.json gives it, word for word; a flat address is an MCP
// error with {error, hint}; and a reply that asks nothing comes back with "retagged": true.
func TestSendToolServedAsSpecified(t *testing.T) {
	w := newWorld(t, "a", "b")
	b, err := os.ReadFile(filepath.Join("..", "..", "spec", "mash-mcp.json"))
	if err != nil {
		t.Fatal(err)
	}
	var spec struct {
		Tools []struct{ Name, Description string }
	}
	if err := json.Unmarshal(b, &spec); err != nil {
		t.Fatal(err)
	}
	want := ""
	for _, tl := range spec.Tools {
		if tl.Name == "send" {
			want = tl.Description
		}
	}
	if want == "" {
		t.Fatal("no send tool in the specification")
	}
	s := startServe(t, w.env["a"])
	s.initialize(t)
	got := ""
	for _, tl := range s.tools(t) {
		if m := tl.(map[string]any); m["name"] == "send" {
			got, _ = m["description"].(string)
		}
	}
	if got != want {
		t.Fatalf("send description\n%q\nwant\n%q", got, want)
	}
	flat := "o_dev_b"
	r := s.call(t, 31, "send", `{"address":"`+flat+`","message":"hi"}`)
	sc, _ := r["structuredContent"].(map[string]any)
	if r["isError"] != true || sc["error"] != flat+" is not an address; write it as o.dev.b" || sc["hint"] != "mash send --help" {
		t.Fatalf("flat address: %v", r)
	}
	if rr := s.call(t, 32, "send", `{"address":"o.dev.b","message":"first?"}`); rr["isError"] == true {
		t.Fatalf("send: %v", rr)
	}
	// b answers #1 with a short reply marked question: sent as fyi, said so
	sb := startServe(t, w.env["b"])
	sb.initialize(t)
	rr := sb.call(t, 33, "send", `{"address":"o.dev.a","message":"ok","kind":"question","reply_to":1}`)
	sc, _ = rr["structuredContent"].(map[string]any)
	if rr["isError"] == true || sc["retagged"] != true || sc["kind"] != "fyi" || resultText(rr) != "Mail sent as fyi: a reply marked question that asks nothing." {
		t.Fatalf("retagged: %v", rr)
	}
}
