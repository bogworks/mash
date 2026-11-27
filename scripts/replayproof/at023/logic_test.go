package main

import (
	"strings"
	"testing"
	"time"
)

func TestInWindow(t *testing.T) {
	ts := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	for _, c := range []struct {
		after  time.Duration
		inside bool
	}{
		{-time.Second, false}, {0, true}, {5 * time.Second, true}, {28 * time.Second, true},
		{28*time.Second + time.Millisecond, false}, {30 * time.Second, false}, {45 * time.Second, false},
	} {
		w := InWindow(ts, ts.Add(c.after))
		if w.Inside != c.inside {
			t.Errorf("after %v: inside %v, want %v", c.after, w.Inside, c.inside)
		}
		if w.Elapsed != c.after || w.Remaining != liveWindow-c.after {
			t.Errorf("after %v: elapsed %v remaining %v", c.after, w.Elapsed, w.Remaining)
		}
	}
}

func TestParseChunkAndOutcome(t *testing.T) {
	ack := ParseChunk([]byte(`{"type":"status","data":"ack"}`), "", "", false)
	resp := ParseChunk([]byte(`{"type":"response","data":"noon"}`), "", "", false)
	end := ParseChunk(nil, "", "", false)
	refused := ParseChunk(nil, "401", "a signed Agent-Sender header is required", true)
	for _, c := range []struct {
		name   string
		chunks []Chunk
		want   string
	}{
		{"ack", []Chunk{ack, resp, end}, "accepted"},
		{"refused", []Chunk{refused, end}, "refused 401 a signed Agent-Sender header is required"},
		{"nothing", nil, "no reply"},
		{"end only", []Chunk{end}, "ended without an answer"},
	} {
		if got := Outcome(c.chunks); got != c.want {
			t.Errorf("%s: %q, want %q", c.name, got, c.want)
		}
	}
	if resp.Text != "noon" || ack.Kind != "ack" || end.Kind != "end" || refused.Kind != "error" {
		t.Errorf("chunks: %+v %+v %+v %+v", ack, resp, end, refused)
	}
}

func TestDiff(t *testing.T) {
	a := Result{Baseline: "x", Control: "y", Replay: "z"}
	if d := Diff(a, a); len(d) != 0 {
		t.Errorf("equal results differ: %v", d)
	}
	b := a
	b.Replay = "refused 401"
	if d := Diff(a, b); len(d) != 1 {
		t.Errorf("one field differs, got %v", d)
	}
}

func TestParseSumsAndKnownIssue(t *testing.T) {
	h := "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	m, err := ParseSums(h + "  mash-linux-amd64\n\n" + h + " *other\n")
	if err != nil || m["mash-linux-amd64"] != h || m["other"] != h {
		t.Fatalf("%v %v", m, err)
	}
	if _, err := ParseSums("short  x\n"); err == nil {
		t.Error("a short digest was accepted")
	}
	if k := NewKnownIssue(" "); k.URL != "" || k.Status[:5] != "EMPTY" {
		t.Errorf("empty: %+v", k)
	}
	if k := NewKnownIssue("https://example.org/1"); k.URL == "" || k.Status != "linked" {
		t.Errorf("given: %+v", k)
	}
}

func TestKnownIssueFormProblem(t *testing.T) {
	for _, c := range []struct {
		url  string
		good bool
	}{
		{"https://github.com/acme/mash/issues/1829", true},
		{"https://github.com/zeta/mash/issues/1829", false},
		{"https://github.com/acme/mash/pull/1829", false},
		{"https://github.com/acme/mash/issues/1829x", false},
		{"https://github.com/acme/mash/issues/1829/", false},
		{"https://github.com/acme/mash/issues/0", false},
		{"https://github.com/acme/mash/issues/", false},
		{"", false},
	} {
		if got := KnownIssueFormProblem(c.url, "acme/mash") == ""; got != c.good {
			t.Errorf("%q: good %v, want %v", c.url, got, c.good)
		}
	}
}

func TestAnswerProblem(t *testing.T) {
	good := Replay{Outcome: "accepted", SessionSaw: true, ReplyToolOK: true, ReplyText: replayAnswer, ExpectedText: replayAnswer,
		StreamComplete: true, Chunks: []Chunk{{Kind: "ack"}, {Kind: "response", Text: replayAnswer}, {Kind: "end"}}}
	if p := AnswerProblem(good); p != "" {
		t.Fatalf("a complete answer was refused: %s", p)
	}
	for _, c := range []struct {
		name string
		edit func(*Replay)
		want string
	}{
		{"reply tool failed", func(r *Replay) { r.ReplyToolOK = false }, "reply tool"},
		{"no answer", func(r *Replay) { r.ReplyText = "" }, "received"},
		{"wrong text", func(r *Replay) { r.ReplyText = "something else" }, "received"},
		{"extra text", func(r *Replay) { r.ReplyText += "!" }, "received"},
		{"stream incomplete", func(r *Replay) { r.StreamComplete = false }, "stream"},
		{"no expected text", func(r *Replay) { r.ExpectedText = "" }, "expected answer"},
		{"session never saw it", func(r *Replay) { r.SessionSaw = false }, "never saw"},
		{"refused", func(r *Replay) { r.Outcome = "refused 401" }, "not accepted"},
	} {
		r := good
		c.edit(&r)
		if p := AnswerProblem(r); p == "" || !strings.Contains(p, c.want) {
			t.Errorf("%s: problem %q, want it to mention %q", c.name, p, c.want)
		}
	}
	// Both builds failing the same way must still fail: the check is on each build, not on their equality.
	bad := good
	bad.ReplyToolOK = false
	if len(Diff(Result{Replay: bad.Summary()}, Result{Replay: bad.Summary()})) != 0 || AnswerProblem(bad) == "" {
		t.Error("an identical failure on both builds passed")
	}
}

func TestStreamEnded(t *testing.T) {
	for _, c := range []struct {
		chunks []Chunk
		want   bool
	}{
		{nil, false}, {[]Chunk{{Kind: "ack"}}, false}, {[]Chunk{{Kind: "ack"}, {Kind: "error"}}, false},
		{[]Chunk{{Kind: "ack"}, {Kind: "response"}, {Kind: "end"}}, true},
	} {
		if got := StreamEnded(c.chunks); got != c.want {
			t.Errorf("%v: %v, want %v", c.chunks, got, c.want)
		}
	}
}

func TestKnownIssueCheck(t *testing.T) {
	url := "https://github.com/acme/mash/issues/1829"
	for _, c := range []struct {
		name, url, verified, by string
		pass                    bool
		detail                  string
	}{
		{"launcher verified", url, "open, label known-0.6.0", "launcher", true, "verified by the launcher"},
		{"form only fails", url, "", "not verified", false, "-known-issue-verified"},
		{"blank verified fails", url, "  ", "not verified", false, "not verified"},
		{"gh result text passes", url, "state OPEN, labels known-0.6.0", "launcher", true, "state OPEN"},
		{"bad form even if verified", "https://github.com/zeta/mash/issues/1", "open, label known-0.6.0", "", false, "expected exactly"},
		{"no url", "", "open", "", false, "EMPTY"},
	} {
		ki := NewKnownIssue(c.url)
		pass, detail := KnownIssueCheck(&ki, "acme/mash", c.verified)
		if pass != c.pass || ki.VerifiedBy != c.by || !strings.Contains(detail, c.detail) {
			t.Errorf("%s: pass %v by %q detail %q", c.name, pass, ki.VerifiedBy, detail)
		}
	}
}

func TestOwnerToken(t *testing.T) {
	for _, ok := range []string{"acme", "zeta", "a-b_9"} {
		if !ownerRE.MatchString(ok) {
			t.Errorf("%q should be accepted", ok)
		}
	}
	for _, bad := range []string{"", "a.b", "a b", "a>", "a*", "a\n"} {
		if ownerRE.MatchString(bad) {
			t.Errorf("%q should be refused", bad)
		}
	}
}

func TestRefusedAfterRestartProblem(t *testing.T) {
	good := "refused 401 this endpoint restarted after the prompt was signed: send the prompt again"
	for _, c := range []struct {
		name string
		r    Replay
		ok   bool
	}{
		{"refused, right text, no session", Replay{Outcome: good}, true},
		{"accepted", Replay{Outcome: "accepted", SessionSaw: true, ReplyToolOK: true}, false},
		{"refused, other text", Replay{Outcome: "refused 401 bad signature"}, false},
		{"refused, other status", Replay{Outcome: "refused 403 this endpoint " + RestartedText}, false},
		{"refused, right text, session saw it", Replay{Outcome: good, SessionSaw: true}, false},
		{"refused, right text, reply ok", Replay{Outcome: good, ReplyToolOK: true}, false},
	} {
		if p := RefusedAfterRestartProblem(c.r); (p == "") != c.ok {
			t.Errorf("%s: problem %q, want ok=%v", c.name, p, c.ok)
		}
	}
}

func TestOnlyReplayDiffers(t *testing.T) {
	a := Result{Baseline: "x", Control: "y", Replay: "z"}
	b := a
	b.Replay = "refused 401"
	if !OnlyReplayDiffers(Diff(a, b)) {
		t.Error("replay-only difference not recognised")
	}
	if OnlyReplayDiffers(Diff(a, a)) {
		t.Error("equal results accepted")
	}
	b.Control = "other"
	if OnlyReplayDiffers(Diff(a, b)) {
		t.Error("two differences accepted")
	}
	c := a
	c.Baseline = "q"
	if OnlyReplayDiffers(Diff(a, c)) {
		t.Error("baseline-only difference accepted")
	}
}
