package main

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestSendArgsError(t *testing.T) {
	cases := []struct {
		name string
		args string
		want []string // substrings of the error; nil means no error
	}{
		{"wrong name", `{"to":"x","body":"hi"}`, []string{"`text`", "got: body"}},
		{"two wrong names sorted", `{"to":"x","message":"hi","body":"y"}`, []string{"`text`", "got: body, message"}},
		{"empty text", `{"to":"x","text":""}`, []string{"`text`", "got: no text"}},
		{"ok", `{"to":"x","text":"hi"}`, nil},
		{"id is accepted with text", `{"to":"x","text":"hi","id":"job-7"}`, nil},
		{"id is a known key, not reported", `{"to":"x","id":"job-7","body":"hi"}`, []string{"`text`", "got: body"}},
	}
	for _, c := range cases {
		err := sendArgsError(json.RawMessage(c.args))
		if c.want == nil {
			if err != nil {
				t.Errorf("%s: unexpected error %v", c.name, err)
			}
			continue
		}
		if err == nil {
			t.Errorf("%s: want an error", c.name)
			continue
		}
		for _, w := range c.want {
			if !strings.Contains(err.Error(), w) {
				t.Errorf("%s: error %q lacks %q", c.name, err, w)
			}
		}
	}
}

// An id sent without text is a known argument: it is not listed among the unknown keys, and the error says so.
func TestSendArgsErrorDoesNotCallIDUnknown(t *testing.T) {
	err := sendArgsError(json.RawMessage(`{"to":"x","id":"job-7"}`))
	if err == nil || strings.Contains(err.Error(), "got: id") || !strings.Contains(err.Error(), "got: no text") {
		t.Fatalf("id reported as unknown: %v", err)
	}
}
