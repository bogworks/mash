package main

import (
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
