package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

// liveWindow is the replay window of a live prompt (SAP section 13.7): a header's ts verifies for 30 seconds.
const liveWindow = 30 * time.Second

// windowMargin is kept free at the end of the window: a replay sent inside it could reach the endpoint after the
// window shut, and then would say nothing about a restart.
const windowMargin = 2 * time.Second

// Window says where a moment lies in the live window that starts at a message's own timestamp.
type Window struct {
	Elapsed   time.Duration `json:"elapsed_ns"`
	Remaining time.Duration `json:"remaining_ns"`
	Inside    bool          `json:"inside"`
}

// InWindow reports whether sent is inside the 30-second live window of a message stamped ts, with windowMargin to spare.
// A moment before ts is not inside the window either (the stamp was in the future).
func InWindow(ts, sent time.Time) Window {
	el := sent.Sub(ts)
	return Window{Elapsed: el, Remaining: liveWindow - el, Inside: el >= 0 && el <= liveWindow-windowMargin}
}

// Chunk is one message that came back on the reply inbox.
type Chunk struct {
	Kind string `json:"kind"` // "ack", "working", "response", "error", "end", "other"
	Text string `json:"text,omitempty"`
}

// ParseChunk reads one reply message: its body, and its Nats-Service-Error-Code and Nats-Service-Error headers.
func ParseChunk(data []byte, errCode, errText string, hasHeaders bool) Chunk {
	if errCode != "" {
		return Chunk{Kind: "error", Text: strings.TrimSpace(errCode + " " + errText)}
	}
	if len(data) == 0 && !hasHeaders {
		return Chunk{Kind: "end"}
	}
	var c struct {
		Type string          `json:"type"`
		Data json.RawMessage `json:"data"`
	}
	if json.Unmarshal(data, &c) != nil {
		return Chunk{Kind: "other"}
	}
	var s string
	if json.Unmarshal(c.Data, &s) != nil {
		s = string(c.Data)
	}
	switch {
	case c.Type == "status" && s == "ack":
		return Chunk{Kind: "ack"}
	case c.Type == "status":
		return Chunk{Kind: "working", Text: s}
	case c.Type == "response":
		return Chunk{Kind: "response", Text: s}
	}
	return Chunk{Kind: "other"}
}

// Outcome is what the first chunk back says about a prompt: "accepted" (the endpoint acknowledged it), "refused <code> <text>"
// (it answered with an error), or "no reply" (nothing came back).
func Outcome(chunks []Chunk) string {
	for _, c := range chunks {
		switch c.Kind {
		case "ack", "working", "response":
			return "accepted"
		case "error":
			return "refused " + c.Text
		case "end":
			return "ended without an answer"
		}
	}
	return "no reply"
}

// Replay is what one replay of the captured prompt did.
type Replay struct {
	SentAt     time.Time `json:"sent_at"`
	Outcome    string    `json:"outcome"`
	Chunks     []Chunk   `json:"chunks"`
	SessionSaw bool      `json:"session_saw_prompt"` // the endpoint's session got a prompt event
	ReplyText  string    `json:"reply_text,omitempty"`
}

// Summary is a replay in one line, for the comparison.
func (r Replay) Summary() string {
	return fmt.Sprintf("%s; session saw the prompt: %v; answer back to the caller: %q", r.Outcome, r.SessionSaw, r.ReplyText)
}

// Result is the part of a build's run that the two builds are compared on. Times and ids are not in it.
type Result struct {
	Baseline string `json:"baseline"`           // the original prompt's outcome and answer
	Control  string `json:"control_no_restart"` // the replay before the restart
	Replay   string `json:"replay_after_restart"`
}

// Diff lists the fields in which two results differ, one line each; it is empty when they are equal.
func Diff(a, b Result) []string {
	var d []string
	for _, f := range []struct{ name, x, y string }{
		{"baseline", a.Baseline, b.Baseline}, {"control_no_restart", a.Control, b.Control}, {"replay_after_restart", a.Replay, b.Replay},
	} {
		if f.x != f.y {
			d = append(d, fmt.Sprintf("%s: oracle %q, candidate %q", f.name, f.x, f.y))
		}
	}
	return d
}

// ParseSums reads a SHA256SUMS file ("<hex>  <name>" per line) into name -> digest.
func ParseSums(text string) (map[string]string, error) {
	m := map[string]string{}
	sc := bufio.NewScanner(strings.NewReader(text))
	for sc.Scan() {
		f := strings.Fields(sc.Text())
		if len(f) == 0 {
			continue
		}
		if len(f) != 2 || len(f[0]) != 64 {
			return nil, fmt.Errorf("SHA256SUMS: unreadable line %q", sc.Text())
		}
		m[strings.TrimPrefix(f[1], "*")] = f[0]
	}
	return m, sc.Err()
}

// KnownIssue is the record's field for the known-issues entry REQ-022 asks for.
type KnownIssue struct {
	URL    string `json:"url"`
	Status string `json:"status"`
}

// NewKnownIssue says in its status whether the link is given.
func NewKnownIssue(url string) KnownIssue {
	if strings.TrimSpace(url) == "" {
		return KnownIssue{Status: "EMPTY: no known-0.6.0 entry is linked yet; REQ-022 needs one before the gate"}
	}
	return KnownIssue{URL: strings.TrimSpace(url), Status: "linked"}
}
