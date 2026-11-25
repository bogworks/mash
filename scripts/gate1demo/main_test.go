package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func write(t *testing.T, dir, name, body string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func tool(next string) string {
	return `{"command":{"name":"mashx","commands":[{"name":"init","metadata":[
	{"name":"x-output","value":[{"case":"Set up","text":"old text","stream":"stdout","exit":0}]},
	{"name":"x-output-next","value":` + next + `}]}]}}`
}

const emptyTool = `{"command":{"name":"mashctl"}}`
const rules = `{"rules":[{"rule":"Old rule","detail":"old detail"}]}`

func fixture(t *testing.T, next, nextFile string) string {
	dir := t.TempDir()
	write(t, dir, "mash.json", tool(next))
	write(t, dir, "mashctl.json", emptyTool)
	write(t, dir, "rules.json", rules)
	const info = `"info":{"version":"0.5.9-spec.0"}`
	switch {
	case nextFile == "" || nextFile == "{}":
		nextFile = "{" + info + "}"
	case !strings.Contains(nextFile, `"info"`):
		nextFile = "{" + info + "," + strings.TrimPrefix(nextFile, "{")
	}
	write(t, dir, "next.json", nextFile)
	return dir
}

func TestCommandReplaces(t *testing.T) {
	dir := fixture(t, `[{"case":"New","text":"new text","stream":"stderr","exit":1,"issue":"#1055","replaces":"Set up","note":"a note"}]`, "")
	out, err := render(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, w := range []string{"## mashx init / New (#1055)", "Today:", "old text", "0.5.9:", "new text", "stream: stderr, exit: 1", "Note: a note"} {
		if !strings.Contains(out, w) {
			t.Errorf("missing %q in\n%s", w, out)
		}
	}
	if strings.Index(out, "old text") > strings.Index(out, "new text") {
		t.Error("Today must precede 0.5.9")
	}
}

func TestReplacesMissing(t *testing.T) {
	dir := fixture(t, `[{"case":"New","text":"x","replaces":"Nope"}]`, "")
	out, err := render(dir)
	if err == nil || out != "" || !strings.Contains(err.Error(), "mashx init") || !strings.Contains(err.Error(), `"Nope"`) {
		t.Fatalf("want error naming command and target, got %v\n%s", err, out)
	}
}

func TestRuleReplacesMissing(t *testing.T) {
	dir := fixture(t, `[]`, `{"rules_next":[{"rule":"New rule","detail":"d","replaces":"Nope"}]}`)
	out, err := render(dir)
	if err == nil || out != "" || !strings.Contains(err.Error(), "New rule") || !strings.Contains(err.Error(), `"Nope"`) {
		t.Fatalf("want error naming rule and target, got %v\n%s", err, out)
	}
}

func TestMissingNextJSON(t *testing.T) {
	dir := fixture(t, `[]`, "")
	if err := os.Remove(filepath.Join(dir, "next.json")); err != nil {
		t.Fatal(err)
	}
	if out, err := render(dir); err == nil || out != "" {
		t.Fatalf("want error, got %v\n%s", err, out)
	}
}

func TestItemReplacesToday(t *testing.T) {
	dir := fixture(t, `[]`, `{"items":[{"id":"flag","case":"c","text":"new flag text","replaces":"old flag text","stream":"stderr","exit":2}]}`)
	out, err := render(dir)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "Today:\n\n```\nold flag text\n```\n\n0.5.9:") {
		t.Errorf("Today block wrong:\n%s", out)
	}
	if strings.Count(out, "stream:") != 1 {
		t.Errorf("Today must not repeat stream/exit:\n%s", out)
	}
}

func TestItemsAndRules(t *testing.T) {
	dir := fixture(t, `[]`, `{"items":[{"id":"inst","where":"install.sh","case":"c","text":"hello","stream":"stdout","exit":0,"issue":"#9"}],
	"rules_next":[{"rule":"New rule","detail":"new detail","replaces":"Old rule"}]}`)
	out, err := render(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, w := range []string{"## inst [install.sh] (#9)", "hello", "## rule: New rule", "old detail", "new detail"} {
		if !strings.Contains(out, w) {
			t.Errorf("missing %q in\n%s", w, out)
		}
	}
}

func TestWidth(t *testing.T) {
	l := func(n int) string { return strings.Repeat("a", n) }
	dir := fixture(t, `[{"case":"A","text":"`+l(69)+`"},{"case":"B","text":"`+l(75)+`"},{"case":"C","text":"`+l(80)+`"}]`, "")
	out, err := render(dir)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out, "width: 69") || !strings.Contains(out, "width: 75 columns") || !strings.Contains(out, "width: 80 columns") {
		t.Errorf("flagging wrong:\n%s", out)
	}
	dir = fixture(t, `[{"case":"D","text":"ok\n`+l(81)+`"}]`, "")
	if out, err := render(dir); err == nil || out != "" || !strings.Contains(err.Error(), "81 columns") {
		t.Errorf("want width error, got %v", err)
	}
}

func TestWrappedNoteRefused(t *testing.T) {
	const msg = "the text is wrapped for display (the note says so); show the program's own lines, which the width rule measures"
	for _, note := range []string{"One line in the program; wrapped here", "Shown WRAPPED HERE for display"} {
		dir := fixture(t, `[{"case":"A","text":"short","note":"`+note+`"}]`, "")
		if out, err := render(dir); err == nil || out != "" || !strings.Contains(err.Error(), msg) {
			t.Errorf("note %q: want wrapped refusal, got %v", note, err)
		}
	}
	// An ordinary case is unaffected.
	if _, err := render(fixture(t, `[{"case":"A","text":"short","note":"a plain note"}]`, "")); err != nil {
		t.Error(err)
	}
	// A normal case with an 81-column line is still refused by width, not as wrapped.
	dir := fixture(t, `[{"case":"A","text":"`+strings.Repeat("a", 81)+`","note":"a plain note"}]`, "")
	if _, err := render(dir); err == nil || !strings.Contains(err.Error(), "81 columns") || strings.Contains(err.Error(), "wrapped") {
		t.Errorf("want width error only, got %v", err)
	}
}

func TestSecretsRefused(t *testing.T) {
	bad := []string{
		"SU" + strings.Repeat("A", 40),
		"SAAB" + strings.Repeat("2", 40),
		"-----BEGIN PRIVATE KEY-----",
		"nkey: " + strings.Repeat("x", 30),
		"seed=" + strings.Repeat("y", 30),
		"eyJhbGciOiJIUzI1NiJ9.abc",
		"MASH_SETUP_TOKEN=abc",
	}
	for _, s := range bad {
		for name, dir := range map[string]string{
			"command": fixture(t, `[{"case":"A","text":"`+s+`"}]`, ""),
			"item":    fixture(t, `[]`, `{"items":[{"id":"i","case":"c","text":"`+s+`"}]}`),
			"rule":    fixture(t, `[]`, `{"rules_next":[{"rule":"r","detail":"`+s+`"}]}`),
		} {
			out, err := render(dir)
			if err == nil || out != "" {
				t.Errorf("%s %q: not refused", name, s)
			}
		}
	}
	// The clean cases do not trip it.
	if _, err := render(fixture(t, `[{"case":"A","text":"seed phrase and SUnshine"}]`, "")); err != nil {
		t.Error(err)
	}
}

func TestDeterministic(t *testing.T) {
	dir := fixture(t, `[{"case":"A","text":"a"},{"case":"B","text":"b"}]`, `{"items":[{"id":"i","case":"c","text":"t"}]}`)
	a, _ := render(dir)
	b, _ := render(dir)
	if a == "" || a != b {
		t.Error("not deterministic")
	}
	if strings.Index(a, "A (") > strings.Index(a, "mashx init / B") && strings.Contains(a, "/ A") {
		t.Error("order")
	}
}

// A planned change to a script is written in x-output-next on its command in install.json, and shows in the demo.
func TestScriptSpecNext(t *testing.T) {
	dir := fixture(t, `[]`, "")
	write(t, dir, "install.json", `{"command":{"name":"install.sh","metadata":[{"name":"x-output","value":[{"case":"Old","text":"old line"}]},
		{"name":"x-output-next","value":[{"case":"New","text":"new line","stream":"stderr","exit":1,"issue":"#9","replaces":"Old"}]}]}}`)
	out, err := render(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, w := range []string{"install.sh / New (#9)", "old line", "new line"} {
		if !strings.Contains(out, w) {
			t.Errorf("missing %q in\n%s", w, out)
		}
	}
}

func TestRealSpec(t *testing.T) {
	out, err := render("../../spec")
	if err != nil {
		t.Fatal(err)
	}
	for _, w := range []string{"signing-lost-both [mashctl endpoint create / reissue (strict signing)]"} {
		if !strings.Contains(out, w) {
			t.Errorf("missing %q in\n%s", w, out)
		}
	}
}

func TestHeadingTakesVersionFromFile(t *testing.T) {
	dir := fixture(t, `[]`, `{"info":{"version":"1.2.3-spec.4"}}`)
	out, err := render(dir)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(out, "# Gate 1 demo: planned 1.2.3 output\n") {
		t.Errorf("heading does not name the version from the file:\n%s", out)
	}
}

func TestMalformedVersionRefused(t *testing.T) {
	for _, next := range []string{
		`{"items":[]}`,
		`{"info":{"version":""},"items":[]}`,
		`{"info":{"version":"0.5.9"}}`,
		`{"info":{"version":"v0.5.9-spec.0"}}`,
		`{"info":{"version":"0.5-spec.0"}}`,
		`{"info":{"version":"0.5.9-spec."}}`,
	} {
		dir := fixture(t, `[]`, next)
		write(t, dir, "next.json", next)
		out, err := render(dir)
		if err == nil || !strings.Contains(err.Error(), "info.version") {
			t.Errorf("%s: want a version refusal, got err=%v", next, err)
		}
		if out != "" {
			t.Errorf("%s: refused render must give no output", next)
		}
	}
}

func TestBracketedCaseIsDescription(t *testing.T) {
	dir := fixture(t, `[{"case":"Quiet","text":"(no warning line;\nthe result line is as before)","stream":"stderr","exit":0}]`, "")
	out, err := render(dir)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "Description (not output): no warning line;\nthe result line is as before\n") {
		t.Errorf("missing description line in\n%s", out)
	}
	for _, w := range []string{"stream: stderr", "```\n(no warning", "```\nno warning"} {
		if strings.Contains(out, w) {
			t.Errorf("description must not render as output (%q) in\n%s", w, out)
		}
	}
}
