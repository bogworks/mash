package main

import (
	"bytes"
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

func TestVerbHelpAndStrayArguments(t *testing.T) {
	env := func(string) string { return "" }
	for _, verb := range []string{"status", "version", "who", "read", "inbox", "send", "prompt"} {
		for _, h := range []string{"--help", "-h"} {
			var out, errb bytes.Buffer
			if code := run([]string{verb, h}, env, strings.NewReader(""), &out, &errb); code != 0 || !strings.HasPrefix(out.String(), "usage: mash "+verb) || errb.Len() != 0 {
				t.Errorf("%s %s: %d %q %q", verb, h, code, out.String(), errb.String())
			}
		}
	}
	for _, verb := range []string{"status", "version"} {
		var out, errb bytes.Buffer
		if code := run([]string{verb, "--bogus"}, env, strings.NewReader(""), &out, &errb); code != 2 || out.Len() != 0 || !strings.Contains(errb.String(), "usage: mash "+verb) {
			t.Errorf("%s --bogus: %d %q %q", verb, code, out.String(), errb.String())
		}
	}
}
