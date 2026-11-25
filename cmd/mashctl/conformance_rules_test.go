package main

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestConfIsDescription(t *testing.T) {
	for text, want := range map[string]bool{
		"(the same JSON as --json: this command prints data,\n for pasting into a NATS configuration)": true,
		"  (words)\n":    true,
		"()":             true,
		"(a) and (b)":    false, // the first bracket closes before the end: not one pair around the whole text
		"(a (nested) b)": true,
		"(open":          false,
		"close)":         false,
		"x (words)":      false,
		"(words) x":      false,
		"":               false,
		"(":              false,
	} {
		if got := confIsDescription(text); got != want {
			t.Errorf("%q: %v, want %v", text, got, want)
		}
	}
}

// The --json shape of a message that names no sender (the case "Messages" of `endpoint inbox`).
func TestConfInboxMessagesJSON(t *testing.T) {
	atTerminal(t) // expects the terminal form of --json (#1043)
	j := confInboxMessagesRun(t, "--json")
	var doc struct {
		Messages []map[string]any `json:"messages"`
	}
	if err := json.Unmarshal([]byte(j.ran.out), &doc); err != nil || len(doc.Messages) != 2 || doc.Messages[1]["from"] != "?" || !strings.Contains(j.ran.out, `"from": "?"`) {
		t.Errorf("%v %s", err, j.ran.out)
	}
}
