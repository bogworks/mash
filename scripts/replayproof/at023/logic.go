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
	// ReplyToolOK is whether the session's reply tool call came back as a success.
	ReplyToolOK bool `json:"reply_tool_ok"`
	// ExpectedText is the text the session was told to reply with; the caller must receive exactly this.
	ExpectedText string `json:"expected_text,omitempty"`
	// StreamComplete is whether the reply stream ended with its final (end) chunk.
	StreamComplete bool `json:"stream_complete"`
}

// The texts the session replies with, so the assertion can compare what the caller received to them exactly.
const (
	baselineAnswer = "the date is today"
	replayAnswer   = "answer to the replayed prompt"
)

// StreamEnded reports whether the last chunk is the end of the stream (an error chunk is not a completed stream).
func StreamEnded(chunks []Chunk) bool {
	return len(chunks) > 0 && chunks[len(chunks)-1].Kind == "end"
}

// AnswerProblem returns "" when r is an accepted prompt that the session saw, answered through a successful reply
// tool call, whose caller received exactly the expected text, and whose reply stream completed. Otherwise it returns
// a sentence naming what is missing.
func AnswerProblem(r Replay) string {
	var p []string
	if r.Outcome != "accepted" {
		p = append(p, "not accepted ("+r.Outcome+")")
	}
	if !r.SessionSaw {
		p = append(p, "the session never saw the prompt")
	}
	if !r.ReplyToolOK {
		p = append(p, "the reply tool call did not succeed")
	}
	if r.ExpectedText == "" {
		p = append(p, "no expected answer text recorded")
	} else if r.ReplyText != r.ExpectedText {
		p = append(p, fmt.Sprintf("the caller received %q, expected %q", r.ReplyText, r.ExpectedText))
	}
	if !r.StreamComplete {
		p = append(p, "the reply stream did not complete")
	}
	return strings.Join(p, "; ")
}

// Summary is a replay in one line, for the comparison.
func (r Replay) Summary() string {
	return fmt.Sprintf("%s; session saw the prompt: %v; reply tool ok: %v; answer back to the caller: %q (expected %q); stream complete: %v",
		r.Outcome, r.SessionSaw, r.ReplyToolOK, r.ReplyText, r.ExpectedText, r.StreamComplete)
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
	// VerifiedBy says how the entry was checked: "launcher" (the issue exists and carries the label, confirmed
	// with gh on the host that prepared the run), "not verified" (the URL's form is fine but nothing confirmed the issue;
	// the assertion fails), or "" when no URL is given or its form is wrong.
	VerifiedBy string `json:"verified_by,omitempty"`
}

// NewKnownIssue says in its status whether the link is given.
func NewKnownIssue(url string) KnownIssue {
	if strings.TrimSpace(url) == "" {
		return KnownIssue{Status: "EMPTY: no known-0.6.0 entry is linked yet; REQ-022 needs one before the gate"}
	}
	return KnownIssue{URL: strings.TrimSpace(url), Status: "linked"}
}

// KnownIssueFormProblem returns "" when url is exactly https://github.com/<repo>/issues/<positive integer>, otherwise
// a sentence saying which form is expected. Only the form is checked here; KnownIssueCheck also needs the host's confirmation.
func KnownIssueFormProblem(url, repo string) string {
	prefix := "https://github.com/" + repo + "/issues/"
	n := strings.TrimPrefix(url, prefix)
	ok := n != url && n != "" && n[0] != '0'
	for _, c := range n {
		if c < '0' || c > '9' {
			ok = false
		}
	}
	if ok {
		return ""
	}
	return "expected exactly " + prefix + "<positive integer>, got " + fmt.Sprintf("%q", url)
}

// KnownIssueCheck sets ki.VerifiedBy from the verified text (what the host that prepared the run confirmed with gh) and
// returns the assertion's pass flag and detail. A pass needs both a URL of the right form and non-empty verified text:
// a URL that only looks like an issue URL never passes.
func KnownIssueCheck(ki *KnownIssue, repo, verified string) (bool, string) {
	if ki.URL == "" {
		return false, ki.Status + " " + ki.URL
	}
	if p := KnownIssueFormProblem(ki.URL, repo); p != "" {
		return false, p
	}
	if v := strings.TrimSpace(verified); v != "" {
		ki.VerifiedBy = "launcher"
		return true, ki.Status + " " + ki.URL + " (verified by the launcher: " + v + ")"
	}
	ki.VerifiedBy = "not verified"
	return false, ki.Status + " " + ki.URL + " (not verified: the issue and its known-<version> label were not confirmed; " +
		"run scripts/replayproof/at023.sh, which confirms them with gh on the host, or in a VM guest have the host that " +
		"prepared the run confirm with `gh issue view <n> -R <repo> --json state,labels` and pass the result as -known-issue-verified)"
}
