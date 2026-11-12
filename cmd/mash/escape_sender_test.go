package main

import (
	"fmt"
	"strings"
	"testing"

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
	got := promptNotice(endpoint.PromptEvent{RequestID: "r\x1b1", From: "x\x1b[2J", Trust: sap.Trust("v\x07"), Text: text})
	if hasRawControl(got) {
		t.Fatalf("raw control byte in %q", got)
	}
	for _, want := range []string{`\x1b[31m`, `\x1b]0;title\x07`, `\x08`, `\x9b31m`, `\x00`, "a\tb\nc", "世界 \U0001F600"} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in %q", want, got)
		}
	}
}

func TestPromptNoticeCap(t *testing.T) {
	at := strings.Repeat("a", PromptTextMax)
	if got := promptNotice(endpoint.PromptEvent{Text: at}); strings.Contains(got, "truncated") || !strings.HasSuffix(got, at) {
		t.Fatalf("a prompt exactly at the cap was cut")
	}
	big := strings.Repeat("b\x1b", 50<<10) // 100 KiB
	got := promptNotice(endpoint.PromptEvent{Text: big})
	if hasRawControl(got) {
		t.Fatal("raw control byte in the capped notice")
	}
	if !strings.HasSuffix(got, fmt.Sprintf("[truncated: %d more bytes]", len(big)-PromptTextMax)) {
		t.Fatalf("marker: %q", got[len(got)-60:])
	}
	// The cut lands inside a multi-byte rune: it backs up to the boundary and counts the bytes it dropped.
	cjk := strings.Repeat("世", PromptTextMax) // 3 bytes each; the cap falls mid-rune
	got = promptNotice(endpoint.PromptEvent{Text: cjk})
	kept := PromptTextMax - PromptTextMax%3
	if strings.Contains(got, "�") || strings.Contains(got, `\x`) ||
		!strings.HasSuffix(got, fmt.Sprintf("[truncated: %d more bytes]", len(cjk)-kept)) {
		t.Fatalf("rune boundary cut wrong: %q", got[len(got)-60:])
	}
}

func hostileRecord() store.Record {
	bad := func(s string) string { return s + "\x1b[31m\x07\u009b" }
	return store.Record{Name: "ops1", Owner: "acme", Agent: "cc", AgentToken: "cc", Account: "A", User: "U",
		MailSubject: store.MailSubjectFor("acme", "", "ops1"), Created: "2026-01-01T00:00:00Z",
		Note: bad("note"), Role: bad("role"), Host: bad("host"), Description: bad("desc"),
		Caps: []string{bad("cap")}, Zone: bad("zone"), Labels: map[string]string{bad("lk"): bad("lv")}}
}

func TestWhoTextEscapesRecordFields(t *testing.T) {
	ps := []endpoint.Presence{{Record: hostileRecord()}}
	for _, long := range []bool{false, true} {
		got := whoText(ps, long, nil)
		if hasRawControl(got) {
			t.Fatalf("long=%v raw control in %q", long, got)
		}
		if !strings.Contains(got, `note\x1b[31m\x07\u009b`) {
			t.Fatalf("note not escaped: %q", got)
		}
	}
	long := whoText(ps, true, nil)
	for _, k := range []string{"role", "host", "desc", "cap", "zone", "lk"} {
		if !strings.Contains(long, k+`\x1b[31m`) {
			t.Errorf("%s not shown escaped: %q", k, long)
		}
	}
	// whoRender is what the CLI prints, --names included.
	for _, o := range []whoOutput{{Long: true}, {Names: true}} {
		if out := whoRender(ps, o, false, nil); hasRawControl(out) {
			t.Errorf("whoRender %+v: raw control in %q", o, out)
		}
	}
}

func TestWhoTextCapsAndKeepsUnicode(t *testing.T) {
	r := hostileRecord()
	r.Note = strings.Repeat("世", 1000)
	r.Description = "\U0001F600 日本"
	got := whoText([]endpoint.Presence{{Record: r}}, true, nil)
	if strings.Contains(got, strings.Repeat("世", 257)) || !strings.Contains(got, "...") {
		t.Fatal("note not capped")
	}
	if !strings.Contains(got, "desc \U0001F600 日本") {
		t.Fatalf("unicode changed: %q", got)
	}
}
