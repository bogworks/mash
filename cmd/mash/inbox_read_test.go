package main

import (
	"encoding/json"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/bogworks/mash/internal/endpoint"
)

var agoRE = regexp.MustCompile(`just now|\d+[smhd] ago`)

// timeless is text with its relative times and the age of the oldest waiting message taken out, which tick between two
// calls.
func timeless(s string) string { return agoRE.ReplaceAllString(s, "T") }

func structuredOf(t *testing.T, r map[string]any) map[string]any {
	t.Helper()
	sc, ok := r["structuredContent"].(map[string]any)
	if !ok {
		t.Fatalf("no structuredContent in %v", r)
	}
	delete(sc, "oldest_waiting")
	return sc
}

func jsonOf(t *testing.T, s string) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal([]byte(s), &m); err != nil {
		t.Fatalf("%q: %v", s, err)
	}
	delete(m, "oldest_waiting")
	return m
}

// Both doors give the same inbox and the same message: the tool's text is the command line's, and its structured result
// is --json. The command line reads a message and leaves it new; the read tool, once the session is proven hookless
// (no hook will show it), acknowledges it, and the inbox then says read.
func TestInboxAndReadDoorsOnARealEndpoint(t *testing.T) {
	w, s := hooklessWorld(t, 150*time.Millisecond)
	e := sendAndWaitSpooled(t, w, "the body\nsecond line")
	num := itoa(int(e.Seq))

	cli, code := w.cli(t, "a", "inbox")
	if code != 0 || !strings.HasPrefix(cli, "1 message for o.dev.a, 1 new, 0 urgent.\n\nNUMBER  FROM  WHEN") || !strings.Contains(cli, " new    no      task  the body\n") {
		t.Fatalf("cli inbox: %d %q", code, cli)
	}
	cliJSON, _ := w.cli(t, "a", "inbox", "--json")
	r := s.call(t, 20, "inbox", `{}`)
	if timeless(resultText(r)) != timeless(cli[:len(cli)-1]) {
		t.Errorf("inbox text: tool %q, cli %q", resultText(r), cli)
	}
	if got, want := structuredOf(t, r), jsonOf(t, cliJSON); !jsonEqual(got, want) {
		t.Errorf("inbox object: tool %v, cli %v", got, want)
	}
	wide, _ := w.cli(t, "a", "inbox", "--wide")
	rw := s.call(t, 21, "inbox", `{"wide":true}`)
	if timeless(resultText(rw)) != timeless(wide[:len(wide)-1]) || !strings.Contains(wide, "o.dev.b") || !strings.Contains(wide, "verified") {
		t.Errorf("wide: tool %q, cli %q", resultText(rw), wide)
	}

	// the command line shows the message and cannot mark it
	rd, code := w.cli(t, "a", "read", num)
	if code != 0 || !strings.HasSuffix(rd, "\n\n> the body\n> second line\n") || !strings.Contains(rd, "from:     o.dev.b\n") {
		t.Fatalf("cli read: %d %q", code, rd)
	}
	if out, _ := w.cli(t, "a", "inbox", "--new"); !strings.HasPrefix(out, "1 message for o.dev.a, 1 new") || unreadOf(t, w, "a") != 1 {
		t.Fatalf("a read at a shell left the message %q, unread %d", out, unreadOf(t, w, "a"))
	}
	cliRead, _ := w.cli(t, "a", "read", num, "--json")

	// the read tool: its first call proves nothing yet; after the grace the next one acknowledges
	rr := s.call(t, 22, "read", `{"number":`+num+`}`)
	if got, want := structuredOf(t, rr), jsonOf(t, cliRead); !jsonEqual(got, want) {
		t.Errorf("read object: tool %v, cli %v", got, want)
	}
	if timeless(resultText(rr)) != timeless(strings.TrimSuffix(rd, "\n")) {
		t.Errorf("read text: tool %q, cli %q", resultText(rr), rd)
	}
	time.Sleep(endpoint.HookGrace + 100*time.Millisecond)
	s.call(t, 23, "read", `{"number":`+num+`}`)
	eventually(t, 10*time.Second, "the read tool acknowledged it", func() bool { return unreadOf(t, w, "a") == 0 })
	out, _ := w.cli(t, "a", "inbox")
	if !strings.HasPrefix(out, "1 message for o.dev.a, 0 new, 0 urgent.\n\n") || !strings.Contains(out, " read   no      task  the body\n") {
		t.Errorf("inbox after the read tool: %q", out)
	}
	if out, _ := w.cli(t, "a", "inbox", "--new"); !strings.HasPrefix(out, "1 message for o.dev.a, 0 new, 0 urgent.\n") || strings.Contains(out, "NUMBER") {
		t.Errorf("inbox --new after the read tool: %q", out)
	}

	// no such message, in both doors
	out, code = w.cli(t, "a", "read", "99")
	if code != 1 || out != "error: o.dev.a has no message 99\nhint: mash inbox\n" {
		t.Errorf("cli read 99: %d %q", code, out)
	}
	if r := s.call(t, 24, "read", `{"number":99}`); r["isError"] != true || resultText(r) != "error: o.dev.a has no message 99\nhint: mash inbox" {
		t.Errorf("tool read 99: %v", r)
	}
	// the old names are refused, and the limit is the same in both doors
	if r := s.call(t, 25, "read", `{"seq":1}`); r["isError"] != true || !strings.Contains(resultText(r), "unknown argument seq") {
		t.Errorf("tool read seq: %v", r)
	}
	if r := s.call(t, 26, "inbox", `{"limit":101}`); r["isError"] != true || resultText(r) != "error: limit must be a whole number from 1 to 100" {
		t.Errorf("tool inbox limit 101: %v", r)
	}
}

func jsonEqual(a, b map[string]any) bool {
	x, _ := json.Marshal(a)
	y, _ := json.Marshal(b)
	return string(x) == string(y)
}

// The wake settings reach the inbox: the count line appears above wake_count, 0 turns it off, and the counts are the
// consumer's, whatever --limit lists.
func TestInboxWakeSettingsFromConfig(t *testing.T) {
	w := newWorld(t, "a", "b")
	for i := 0; i < 3; i++ {
		if out, code := w.cli(t, "b", "send", tTo("a"), "m"+itoa(i)); code != 0 {
			t.Fatalf("send: %d %s", code, out)
		}
	}
	w.env["a"]["MASH_WAKE_COUNT"] = "2"
	out, code := w.cli(t, "a", "inbox", "--limit", "1")
	if code != 0 || !strings.HasPrefix(out, "3 messages for o.dev.a, 3 new, 0 urgent.\nMore than 2 are waiting.\n\nNUMBER  ") || strings.Count(out, "\n") != 5 {
		t.Fatalf("inbox: %d %q", code, out)
	}
	js, _ := w.cli(t, "a", "inbox", "--json")
	m := jsonOf(t, js)
	if m["wake_count"] != float64(2) || m["over_wake_count"] != true || m["wake_age"] != "1h" || m["over_wake_age"] != false || m["total"] != float64(3) || m["new"] != float64(3) {
		t.Fatalf("object: %s", js)
	}
	w.env["a"]["MASH_WAKE_COUNT"] = "0"
	w.env["a"]["MASH_WAKE_AGE"] = "0"
	out, _ = w.cli(t, "a", "inbox")
	js, _ = w.cli(t, "a", "inbox", "--json")
	m = jsonOf(t, js)
	if strings.Contains(out, "More than") || m["wake_count"] != float64(0) || m["wake_age"] != "0" || m["over_wake_count"] != false {
		t.Fatalf("off: %q %s", out, js)
	}
	w.env["a"]["MASH_WAKE_COUNT"] = "many"
	if out, code := w.cli(t, "a", "inbox"); code != 1 || !strings.HasPrefix(out, "error: MASH_WAKE_COUNT \"many\" is not a whole number from 0 to 1000") {
		t.Fatalf("bad setting: %d %q", code, out)
	}
	delete(w.env["a"], "MASH_WAKE_COUNT")
	w.env["a"]["MASH_WAKE_AGE"] = "30s"
	if out, code := w.cli(t, "a", "inbox"); code != 1 || !strings.Contains(out, "MASH_WAKE_AGE \"30s\"") {
		t.Fatalf("bad age: %d %q", code, out)
	}
}

// An urgent message counts as urgent, is marked in the table and in the object, and a read at a shell shows it as such.
func TestInboxUrgentAndKind(t *testing.T) {
	w := newWorld(t, "a", "b")
	for _, args := range [][]string{{"send", tTo("a"), "plain"}, {"send", tTo("a"), "--urgent", "--kind", "blocker", "stuck now"}} {
		if out, code := w.cli(t, "b", args...); code != 0 {
			t.Fatalf("%v: %d %s", args, code, out)
		}
	}
	out, _ := w.cli(t, "a", "inbox")
	if !strings.HasPrefix(out, "2 messages for o.dev.a, 2 new, 1 urgent.\n\n") || !strings.Contains(out, " new    yes     blocker  stuck now\n") || !strings.Contains(out, " new    no      task     plain\n") {
		t.Fatalf("inbox: %q", out)
	}
	js, _ := w.cli(t, "a", "inbox", "--json", "--limit", "1")
	m := jsonOf(t, js)
	msgs := m["messages"].([]any)
	if m["urgent"] != float64(1) || len(msgs) != 1 || msgs[0].(map[string]any)["urgent"] != true || msgs[0].(map[string]any)["kind"] != "blocker" || msgs[0].(map[string]any)["from"] != "o.dev.b" {
		t.Fatalf("object: %s", js)
	}
	rd, _ := w.cli(t, "a", "read", "2")
	if !strings.Contains(rd, "\nkind:     blocker\nurgent:   yes\n") {
		t.Fatalf("read: %q", rd)
	}
}
