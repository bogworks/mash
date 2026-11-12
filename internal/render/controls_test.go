package render

import (
	"strings"
	"testing"
)

func TestTextShowsControlCharacters(t *testing.T) {
	cases := []struct{ name, in, want string }{
		{"csi colour", "\x1b[31mred\x1b[0m", `\x1b[31mred\x1b[0m`},
		{"osc title", "\x1b]0;title\a", `\x1b]0;title\x07`},
		{"backspace", "a\bb", `a\x08b`},
		{"bell", "ding\a", `ding\x07`},
		{"del", "a\x7fb", `a\x7fb`},
		{"nul", "a\x00b", `a\x00b`},
		{"c1 csi", "\u009b31m", `\x9b31m`},
		{"c1 osc", "\u009d0;x\u009c", `\x9d0;x\x9c`},
		{"newline kept", "a\nb", "a\nb"},
		{"tab kept", "a\tb", "a\tb"},
		{"crlf is one newline", "a\r\nb", "a\nb"},
		{"cr, vt, ff, nel become newlines", "a\rb\vc\fd\u0085e", "a\nb\nc\nd\ne"},
		{"unicode line separators", "a b c", "a\nb\nc"},
		{"plain utf-8", "héllo 日本語 🎉 ok", "héllo 日本語 🎉 ok"},
		{"invalid utf-8", "ok\xff!", "ok�!"},
		{"just above c1 stays", " ¡", " ¡"},
	}
	for _, c := range cases {
		if got := text([]byte(c.in)); got != c.want {
			t.Errorf("%s: text(%q) = %q, want %q", c.name, c.in, got, c.want)
		}
	}
}

func rawControl(s string) (rune, bool) {
	for _, r := range s {
		if r != '\n' && r != '\t' && (r < 0x20 || (r >= 0x7f && r <= 0x9f)) {
			return r, true
		}
	}
	return 0, false
}

func TestQuotedOutputHasNoRawControl(t *testing.T) {
	body := "hi\x1b[31m\x1b]0;t\a\b\x7f\u009b1m\nnext\tline"
	m := mail(1, "bob", body)
	outs := map[string]string{
		"One":       One(m),
		"Line":      Line(m),
		"Batch":     Batch("me", "acme", []Mail{m}, 5, 10000),
		"Digest":    Batch("me", "acme", []Mail{m, m}, 1, 10000),
		"HookBatch": HookBatch("me", "acme", []Mail{m, m}, 1, 10000),
	}
	for name, out := range outs {
		if r, bad := rawControl(out); bad {
			t.Errorf("%s contains raw control %U: %q", name, r, out)
		}
		if !strings.Contains(out, `\x1b`) {
			t.Errorf("%s does not show the escape: %q", name, out)
		}
	}
	if !strings.Contains(One(m), "> next\tline") {
		t.Errorf("newline and tab not preserved: %q", One(m))
	}
}
