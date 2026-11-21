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
	const key = "endpoint delete Cancelled everywhere, then a later step failed"
	oc := confOutCase{Case: "Cancelled everywhere, then a later step failed", Stream: "stderr", Exit: 1,
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

// The case "Messages" of `endpoint inbox` cannot arise (see confUnchecked). This pins the nearest world's output and
// the --json shape of a message that names no sender.
func TestConfInboxMessagesObstacle(t *testing.T) {
	r := confInboxMessagesRun(t)
	want := "2 messages for acme.demo.bob, 1 not delivered.\n\n" +
		"NUMBER  FROM   WHEN    TRUST     DELIVERED  KIND  SIZE\n" +
		"7       alice  1m ago  verified  yes        task  48 B\n" +
		"8       ?      1m ago  absent    no         task  61 B\n"
	if r.ran.code != 0 || r.ran.out != want || r.ran.errs != "" {
		t.Errorf("%d %q %q", r.ran.code, r.ran.out, r.ran.errs)
	}
	j := confInboxMessagesRun(t, "--json")
	var doc struct {
		Messages []map[string]any `json:"messages"`
	}
	if err := json.Unmarshal([]byte(j.ran.out), &doc); err != nil || len(doc.Messages) != 2 || doc.Messages[1]["from"] != "?" || !strings.Contains(j.ran.out, `"from": "?"`) {
		t.Errorf("%v %s", err, j.ran.out)
	}
}
