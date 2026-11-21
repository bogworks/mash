package main

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestConfCutCause(t *testing.T) {
	cut, cause, ok := confCutCause("error: deleted a.b.c, but the lease fence was not removed: the server did not answer\nhint: mashctl x")
	if !ok || cut != "error: deleted a.b.c, but the lease fence was not removed\nhint: mashctl x" || cause != "the server did not answer" {
		t.Errorf("%q %q %v", cut, cause, ok)
	}
	// the cut is at the LAST ": "
	if cut, cause, _ = confCutCause("error: a: b: c: d"); cut != "error: a: b: c" || cause != "d" {
		t.Errorf("%q %q", cut, cause)
	}
	// only the first error line is cut; earlier lines are kept
	if cut, _, _ = confCutCause("warning: x: y\nerror: p: q\nerror: r: s"); cut != "warning: x: y\nerror: p\nerror: r: s" {
		t.Errorf("%q", cut)
	}
	for _, none := range []string{"", "no error line: here", "error: no cause"} {
		if _, _, ok := confCutCause(none); ok {
			t.Errorf("%q: found a cause", none)
		}
	}
}

func TestConfVaryingCauseCompare(t *testing.T) {
	const key = "endpoint delete Cancelled everywhere, then the lease or the inbox could not be removed (0.5.6 as built, the other path)"
	oc := confOutCase{Case: "Cancelled everywhere, then the lease or the inbox could not be removed (0.5.6 as built, the other path)", Stream: "stderr", Exit: 1,
		Text: "error: deleted acme.demo.bob, but the lease fence or the inbox was not removed: the server did not answer\nhint: mashctl endpoint delete acme.demo.bob"}
	run := func(errs string) []string {
		return confOutputProblems("endpoint delete", oc, confOutRun{ran: confRan{errs: errs, code: 1}})
	}
	tail := "\nhint: mashctl endpoint delete acme.demo.bob\n"
	pre := "error: deleted acme.demo.bob, but the lease fence or the inbox was not removed"
	if p := run(pre + ": the server did not answer" + tail); len(p) != 0 {
		t.Errorf("same cause: %v", p)
	}
	if p := run(pre + ": connection lost" + tail); len(p) != 0 {
		t.Errorf("a different cause: %v", p)
	}
	if p := run("error: deleted acme.demo.bob, but something else: the server did not answer" + tail); len(p) == 0 {
		t.Error("a different prefix passed")
	}
	if p := run(pre + ": mash: inbox stream: connection lost" + tail); len(p) != 0 {
		t.Errorf("a cause that contains \": \": %v", p)
	}
	if p := run(pre + ": " + tail); len(p) == 0 {
		t.Error("an empty cause passed")
	}
	if p := run(pre + tail); len(p) == 0 {
		t.Error("no cause passed")
	}
	if p := run(pre + ": x\nhint: mashctl other\n"); len(p) == 0 {
		t.Error("a different hint passed")
	}
	if !confVaryingCause[key] {
		t.Error("the case is not in confVaryingCause")
	}
	// a case not in the set is compared exactly, cause included
	other := oc
	other.Case = "other"
	if p := confOutputProblems("endpoint delete", other, confOutRun{ran: confRan{errs: pre + ": another cause" + tail, code: 1}}); len(p) == 0 {
		t.Error("a case outside confVaryingCause accepted another cause")
	}
}

func TestConfMidCauseCompare(t *testing.T) {
	const name = "Cancelled everywhere, then a later step failed (0.5.6 as built)"
	pre := "error: deleted acme.demo.bob, but PARTIAL: acme_demo_bob's user key "
	mid := " was revoked in the account JWT, so its credentials no longer work, but the removal did not complete: "
	suf := "; run the same command again (revoking again is safe)"
	hint := "\nhint: mashctl endpoint delete acme.demo.bob"
	oc := confOutCase{Case: name, Stream: "stderr", Exit: 1,
		Text: pre + "UBIDWPW3" + mid + "mash: lease: nats: nats: API error: code=400 err_code=10109 description=invalid operation on sealed stream" + suf + hint}
	run := func(errs string, code int) []string {
		return confOutputProblems("endpoint delete", oc, confOutRun{ran: confRan{errs: errs, code: code}})
	}
	if !confMidCause["endpoint delete "+name] {
		t.Fatal("the case is not in confMidCause")
	}
	if p := run(oc.Text+"\n", 1); len(p) != 0 {
		t.Errorf("good text: %v", p)
	}
	if p := run(pre+"K7Q2M9XA"+mid+"mash: lease: another cause: with colons; and semicolons"+suf+hint+"\n", 1); len(p) != 0 {
		t.Errorf("another key id and cause: %v", p)
	}
	for what, errs := range map[string]string{
		"different prefix":  "error: deleted acme.demo.bob, but something else: UBIDWPW3" + mid + "x" + suf + hint + "\n",
		"short key id":      pre + "UBID" + mid + "x" + suf + hint + "\n",
		"lower-case key id": pre + "ubidwpw3" + mid + "x" + suf + hint + "\n",
		"empty cause":       pre + "UBIDWPW3" + mid + suf + hint + "\n",
		"blank cause":       pre + "UBIDWPW3" + mid + "  " + suf + hint + "\n",
		"missing suffix":    pre + "UBIDWPW3" + mid + "x" + hint + "\n",
		"different hint":    pre + "UBIDWPW3" + mid + "x" + suf + "\nhint: mashctl other\n",
		"no hint":           pre + "UBIDWPW3" + mid + "x" + suf + "\n",
		"text after suffix": pre + "UBIDWPW3" + mid + "x" + suf + " now" + hint + "\n",
	} {
		if p := run(errs, 1); len(p) == 0 {
			t.Errorf("%s passed", what)
		}
	}
	if p := run(oc.Text+"\n", 2); len(p) == 0 {
		t.Error("exit 2 passed")
	}
	// the mechanism applies to its case only
	other := oc
	other.Case = "other"
	if p := confOutputProblems("endpoint delete", other, confOutRun{ran: confRan{errs: pre + "K7Q2M9XA" + mid + "x" + suf + hint + "\n", code: 1}}); len(p) == 0 {
		t.Error("a case outside confMidCause accepted another key id and cause")
	}
}

func TestConfIsDescription(t *testing.T) {
	for text, want := range map[string]bool{
		"(the same JSON as --json: this command prints data,\n for pasting into a NATS configuration)": true,
		"  (words)\n": true,
		"()":          true,
		"(a) and (b)": true, // starts and ends in brackets: a description
		"(open":       false,
		"close)":      false,
		"x (words)":   false,
		"(words) x":   false,
		"":            false,
		"(":           false,
	} {
		if got := confIsDescription(text); got != want {
			t.Errorf("%q: %v, want %v", text, got, want)
		}
	}
}

// The --json shape of a message that names no sender (the case "Messages" of `endpoint inbox`).
func TestConfInboxMessagesJSON(t *testing.T) {
	j := confInboxMessagesRun(t, "--json")
	var doc struct {
		Messages []map[string]any `json:"messages"`
	}
	if err := json.Unmarshal([]byte(j.ran.out), &doc); err != nil || len(doc.Messages) != 2 || doc.Messages[1]["from"] != "?" || !strings.Contains(j.ran.out, `"from": "?"`) {
		t.Errorf("%v %s", err, j.ran.out)
	}
}
