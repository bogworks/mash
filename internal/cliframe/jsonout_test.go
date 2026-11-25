package cliframe

import (
	"bytes"
	"errors"
	"io"
	"testing"
)

// forceTerminal makes IsTerminal answer on for the rest of the test.
func forceTerminal(t *testing.T, on bool) {
	t.Helper()
	old := IsTerminal
	IsTerminal = func(io.Writer) bool { return on }
	t.Cleanup(func() { IsTerminal = old })
}

func TestWriteJSONPolicy(t *testing.T) {
	v := struct {
		A string   `json:"a"`
		B []string `json:"b"`
	}{"x<&>", []string{"p", "q"}}
	for _, tc := range []struct {
		name string
		tty  bool
		want string
	}{
		{"pipe or file: one compact line", false, `{"a":"x<&>","b":["p","q"]}` + "\n"},
		{"terminal: indented", true, "{\n  \"a\": \"x<&>\",\n  \"b\": [\n    \"p\",\n    \"q\"\n  ]\n}\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			forceTerminal(t, tc.tty)
			var b bytes.Buffer
			if err := WriteJSON(&b, v); err != nil {
				t.Fatal(err)
			}
			if b.String() != tc.want {
				t.Errorf("got %q, want %q", b.String(), tc.want)
			}
		})
	}
}

func TestWriteJSONBufferIsCompact(t *testing.T) {
	// The real IsTerminal: a buffer is never a terminal.
	var b bytes.Buffer
	if err := WriteJSON(&b, map[string]int{"n": 1}); err != nil || b.String() != "{\"n\":1}\n" {
		t.Errorf("got %q, %v", b.String(), err)
	}
}

func TestReportJSONPolicy(t *testing.T) {
	err := Usagef("bad flag").WithHint("see --help")
	for _, tc := range []struct {
		name string
		tty  bool
		want string
	}{
		{"pipe or file", false, `{"error":"bad flag","hint":"see --help"}` + "\n"},
		{"terminal", true, "{\n  \"error\": \"bad flag\",\n  \"hint\": \"see --help\"\n}\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			forceTerminal(t, tc.tty)
			var out, errb bytes.Buffer
			if code := Report(&out, &errb, err, true); code != 2 {
				t.Errorf("code %d", code)
			}
			if out.String() != tc.want || errb.Len() != 0 {
				t.Errorf("stdout %q (want %q), stderr %q", out.String(), tc.want, errb.String())
			}
		})
	}
	t.Run("plain error, no hint", func(t *testing.T) {
		forceTerminal(t, true)
		var out, errb bytes.Buffer
		Report(&out, &errb, errors.New("boom"), true)
		if want := "{\n  \"error\": \"boom\"\n}\n"; out.String() != want {
			t.Errorf("got %q, want %q", out.String(), want)
		}
	})
}
