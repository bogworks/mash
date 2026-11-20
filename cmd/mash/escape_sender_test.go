package main

import (
	"strings"
	"testing"

	"github.com/bogworks/mash/internal/config"
	"github.com/bogworks/mash/internal/endpoint"
	"github.com/bogworks/mash/internal/sap"
	"github.com/bogworks/mash/internal/store"
)

// hasRawControl reports a control byte other than "\n" and "\t": C0, DEL, or a C1 character.
func hasRawControl(s string) bool {
	for _, r := range s {
		if r != '\n' && r != '\t' && (r < 0x20 || (r >= 0x7f && r <= 0x9f)) {
			return true
		}
	}
	return false
}

func TestPromptNoticeEscapesControls(t *testing.T) {
	text := "a\tb\nc\x1b[31mred\x1b]0;title\x07 back\x08space \u009b31m nul\x00 end 世界 \U0001F600"
	got, _ := promptNotice(endpoint.PromptEvent{RequestID: "r\x1b1", From: "x\x1b[2J", Trust: sap.Trust("v\x07"), Text: text})
	if hasRawControl(got) {
		t.Fatalf("raw control byte in %q", got)
	}
	for _, want := range []string{`\x1b[31m`, `\x1b]0;title\x07`, `\x08`, `\x9b31m`, `\x00`, "a\tb\nc", "世界 \U0001F600"} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in %q", want, got)
		}
	}
}

func TestPromptNoticeNeverCuts(t *testing.T) {
	if PromptTextMax != endpoint.MaxPromptBytes {
		t.Fatal("the notice limit is not the endpoint's limit")
	}
	at := strings.Repeat("a", PromptTextMax)
	if got, ok := promptNotice(endpoint.PromptEvent{Text: at}); !ok || strings.Contains(got, "truncated") || !strings.HasSuffix(got, at) {
		t.Fatal("a prompt exactly at the limit was cut")
	}
	// Control-byte-heavy text at the limit: every byte becomes 4 characters, nothing is cut, no raw control remains.
	ctl := strings.Repeat("\x1b", PromptTextMax)
	got, ok := promptNotice(endpoint.PromptEvent{Text: ctl})
	if !ok || strings.Contains(got, "truncated") || hasRawControl(got) || strings.Count(got, `\x1b`) != PromptTextMax {
		t.Fatalf("control-heavy text at the limit: ok=%v", ok)
	}
	// Past the guard the notice is refused whole, never cut (the endpoint refuses such a prompt long before).
	if _, ok := promptNotice(endpoint.PromptEvent{Text: strings.Repeat("\x1b", promptNoticeMax/4+1)}); ok {
		t.Fatal("text escaping past the guard was shown")
	}
}

// The worst case of EscapeText is 4 output bytes per input byte (`\xNN`), so the limit's worst notice is 4x, under the
// 8x guard, and the guard cannot trip for an accepted prompt.
func TestPromptNoticeWorstCaseArithmetic(t *testing.T) {
	for _, b := range []byte{0x00, 0x1b, 0x7f, 0xff} {
		if n := len(config.EscapeText(string([]byte{b}))); n != 4 {
			t.Errorf("byte %#x escapes to %d bytes, want 4", b, n)
		}
	}
	if got := len(config.EscapeText(strings.Repeat("\x80", 100))); got != 400 { // invalid UTF-8
		t.Errorf("invalid bytes: %d", got)
	}
	if got := len(config.EscapeText(strings.Repeat("\u009b", 100))); got != 400 { // 2 bytes -> 4
		t.Errorf("C1: %d", got)
	}
	if worst := 4 * PromptTextMax; worst >= promptNoticeMax || promptNoticeMax != 8*PromptTextMax {
		t.Fatalf("worst %d vs guard %d", worst, promptNoticeMax)
	}
}

func hostileRecord() store.Record {
	bad := func(s string) string { return s + "\x1b[31m\x07\u009b" }
	return store.Record{Owner: "acme", Group: "team1", Instance: "ops1", Agent: "claude-code", Account: "A", User: "U",
		MailSubject: tAddr("ops1").Subjects().Mail, Created: "2026-01-01T00:00:00Z",
		Note: bad("note"), Role: bad("role"), Host: bad("host"), Description: bad("desc"),
		Caps: []string{bad("cap")}, Labels: map[string]string{bad("lk"): bad("lv")}}
}
