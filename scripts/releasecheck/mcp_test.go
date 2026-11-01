package main

import (
	"strings"
	"testing"
)

func TestCheckInitializeResponse(t *testing.T) {
	good := `{"id":1,"jsonrpc":"2.0","result":{"protocolVersion":"2025-06-18","serverInfo":{"name":"mash"}}}`
	if err := checkInitializeResponse(good + "\n"); err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct{ name, line, want string }{
		{"bad JSON", `not json`, "bad JSON"},
		{"wrong id", strings.Replace(good, `"id":1`, `"id":2`, 1), "id"},
		{"an error", `{"id":1,"error":{"code":-1}}`, "error"},
		{"no result", `{"id":1}`, "protocolVersion"},
		{"empty protocolVersion", strings.Replace(good, "2025-06-18", "", 1), "protocolVersion"},
		{"another server", strings.Replace(good, `"mash"`, `"other"`, 1), "not mash"},
	} {
		if err := checkInitializeResponse(c.line); err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: want %q, got %v", c.name, c.want, err)
		}
	}
}

func TestOnlyUnknownHook(t *testing.T) {
	if !onlyUnknownHook("mash: unknown command \"hook\"\n\n") || onlyUnknownHook("mash: unknown command \"foo\"\n") || onlyUnknownHook("x\nmash: unknown command \"hook\"\n") {
		t.Fatal("onlyUnknownHook")
	}
}
