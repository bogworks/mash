package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

// callSend runs the MCP send tool and returns its text and whether it is an error result.
func (s *session) callSend(t *testing.T, id int, args string) (string, bool) {
	t.Helper()
	s.send(t, `{"jsonrpc":"2.0","id":`+strconv.Itoa(id)+`,"method":"tools/call","params":{"name":"send","arguments":`+args+`}}`)
	r := s.await(t, 10*time.Second, idIs(float64(id)))["result"].(map[string]any)
	return r["content"].([]any)[0].(map[string]any)["text"].(string), r["isError"] == true
}

// answer=now adds the receipt line and records an ask in the state dir; answer_within needs answer=now and a sane
// range; a 21st open ask is refused before anything is sent.
func TestServeSendAnswerNow(t *testing.T) {
	w := newWorld(t, "a", "b")
	state := t.TempDir()
	w.env["a"]["MASH_STATE_DIR"] = state
	s := startServe(t, w.env["a"])
	s.initialize(t)
	n := 20
	call := func(args string) (string, bool) { n++; return s.callSend(t, n, args) }

	out, bad := call(`{"to":"b","text":"plain","kind":"fyi"}`)
	if bad || strings.Contains(out, "will wake you") {
		t.Fatalf("plain: %q %v", out, bad)
	}
	out, bad = call(`{"to":"b","text":"need it","kind":"fyi","answer":"now","answer_within":"30m"}`)
	if bad || !strings.HasPrefix(out, "sent #2 to b") || !strings.HasSuffix(out, "\nwill wake you on the reply") {
		t.Fatalf("answer now: %q %v", out, bad)
	}
	var file string
	_ = filepath.Walk(filepath.Join(state, "asks"), func(p string, fi os.FileInfo, err error) error {
		if err == nil && !fi.IsDir() && strings.HasSuffix(p, ".json") {
			file = p
		}
		return nil
	})
	b, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	var tbl struct {
		Asks []struct {
			RecipientKey string    `json:"recipient_key"`
			Recipient    string    `json:"recipient"`
			ReceiptSeq   uint64    `json:"receipt_seq"`
			SentAt       time.Time `json:"sent_at"`
			Deadline     time.Time `json:"deadline"`
		} `json:"asks"`
	}
	if err := json.Unmarshal(b, &tbl); err != nil || len(tbl.Asks) != 1 {
		t.Fatalf("table %s: %v", b, err)
	}
	a := tbl.Asks[0]
	if a.Recipient != "b" || a.RecipientKey == "" || a.ReceiptSeq != 2 || a.Deadline.Sub(a.SentAt) != 30*time.Minute {
		t.Fatalf("%+v", a)
	}
	for _, args := range []string{
		`{"to":"b","text":"x","kind":"fyi","answer_within":"5m"}`,
		`{"to":"b","text":"x","kind":"fyi","answer":"now","answer_within":"25h"}`,
		`{"to":"b","text":"x","kind":"fyi","answer":"now","answer_within":"nope"}`,
		`{"to":"b","text":"x","kind":"fyi","answer":"soon"}`,
	} {
		if out, bad := call(args); !bad || !strings.Contains(out, "answer") {
			t.Fatalf("%s: %q %v", args, out, bad)
		}
	}
	for i := 0; i < 19; i++ {
		if out, bad := call(`{"to":"b","text":"more","kind":"fyi","answer":"now"}`); bad {
			t.Fatalf("ask %d: %q", i+2, out)
		}
	}
	before, _ := w.cli(t, "b", "inbox")
	if out, bad := call(`{"to":"b","text":"one too many","kind":"fyi","answer":"now"}`); !bad || !strings.Contains(out, "20 answer=now asks are already open") {
		t.Fatalf("21st: %q %v", out, bad)
	}
	if after, _ := w.cli(t, "b", "inbox"); after != before {
		t.Fatal("a refused ask was sent")
	}
}

// The CLI send does not take the new options (ADR 0034): they are message text, and the output is unchanged.
func TestCLISendIgnoresAnswerNow(t *testing.T) {
	w := newWorld(t, "a", "b")
	if out, code := w.cli(t, "b", "send", "a", "--answer", "now", "hi"); code != 0 || out != "sent #1 from b to a\n" {
		t.Fatalf("%d %q", code, out)
	}
	if strings.Contains(sendUsage, "answer") {
		t.Fatal(sendUsage)
	}
}
