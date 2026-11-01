package main

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestWhoFiltersCLIAndMCP(t *testing.T) {
	w := newWorld(t, "a", "b")
	if out, code := w.admin(t, "set-meta", "a", "--role", "NATS operator", "--label", "team=acme"); code != 0 {
		t.Fatalf("set-meta: %d %s", code, out)
	}
	base, _ := w.cli(t, "b", "who")
	if !strings.Contains(base, "a\t") || !strings.Contains(base, "b\t") {
		t.Fatalf("who: %q", base)
	}
	if out, code := w.cli(t, "b", "who", "--match", "nats"); code != 0 || !strings.HasPrefix(out, "a\t") || strings.Contains(out, "b\t") {
		t.Fatalf("--match: %d %q", code, out)
	}
	if out, _ := w.cli(t, "b", "who", "--label=team=acme", "--names"); out != "a\n" {
		t.Fatalf("--names: %q", out)
	}
	if out, _ := w.cli(t, "b", "who", "--name", "B", "--offline", "--names"); out != "b\n" {
		t.Fatalf("--name --offline: %q", out)
	}
	out, code := w.cli(t, "b", "who", "--json", "--role", "operator")
	var arr []map[string]any
	if code != 0 || json.Unmarshal([]byte(out), &arr) != nil || len(arr) != 1 || arr[0]["name"] != "a" || arr[0]["labels"].(map[string]any)["team"] != "acme" {
		t.Fatalf("--json: %d %q", code, out)
	}
	if out, code := w.cli(t, "b", "who", "--match", "zzz"); code != 0 || out != "" {
		t.Fatalf("no match text: %d %q", code, out)
	}
	if out, code := w.cli(t, "b", "who", "--match", "zzz", "--json"); code != 0 || strings.TrimSpace(out) != "[]" {
		t.Fatalf("no match json: %d %q", code, out)
	}
	if out, code := w.cli(t, "b", "who", "--bogus"); code != 2 || !strings.Contains(out, "usage: mash who") {
		t.Fatalf("bad flag: %d %q", code, out)
	}

	s := startServe(t, w.env["b"])
	s.initialize(t)
	s.tools(t)
	if got := resultText(s.call(t, 30, "who", `{}`)); !strings.Contains(got, "a\t") || !strings.Contains(got, "b\t") {
		t.Fatalf("no-arg who: %q", got)
	}
	if got := resultText(s.call(t, 31, "who", `{"match":"nats","label":["team=acme"],"online":false}`)); !strings.HasPrefix(got, "a\t") || strings.Contains(got, "b\t") {
		t.Fatalf("filtered who: %q", got)
	}
	if got := resultText(s.call(t, 32, "who", `{"name":"zzz"}`)); got != "" {
		t.Fatalf("no match: %q", got)
	}
	var mj []map[string]any
	if got := resultText(s.call(t, 33, "who", `{"role":"nats","format":"json"}`)); json.Unmarshal([]byte(got), &mj) != nil || len(mj) != 1 || mj[0]["name"] != "a" {
		t.Fatalf("json who: %q", got)
	}
	if r := s.call(t, 34, "who", `{"format":"xml"}`); r["isError"] != true {
		t.Fatalf("bad format: %v", r)
	}
	if r := s.call(t, 35, "who", `{"label":["=x"]}`); r["isError"] != true {
		t.Fatalf("bad label: %v", r)
	}
}
