// Package kind is a message's kind: what the sender wants of the recipient, which decides whether it wakes an idle
// session. A kind is advisory, a sender's claim: it only picks wake or wait, never trust.
package kind

import (
	"fmt"
	"strings"
	"unicode/utf8"
)

// Kind is a mail kind. The zero value is Task: a send with no kind wakes the recipient; fyi and result are opt-ins.
type Kind string

const (
	Blocker  Kind = "blocker"
	Question Kind = "question"
	Task     Kind = "task"
	Result   Kind = "result"
	FYI      Kind = "fyi"
)

// All lists the kinds, waking ones first.
var All = []Kind{Blocker, Question, Task, Result, FYI}

// Names is the kinds as a "blocker|question|..." list, for usage and error text.
func Names() string {
	s := make([]string, len(All))
	for i, k := range All {
		s[i] = string(k)
	}
	return strings.Join(s, "|")
}

// Parse reads a kind, case-insensitively; empty is Task. Anything else is an error naming the allowed kinds.
func Parse(s string) (Kind, error) {
	s = strings.ToLower(strings.TrimSpace(s))
	if s == "" {
		return Task, nil
	}
	for _, k := range All {
		if string(k) == s {
			return k, nil
		}
	}
	return Task, fmt.Errorf("unknown kind %q (one of %s)", s, Names())
}

// Of is the kind a received header value names: a missing or unknown one is Task, so an old sender's mail wakes, as
// before kinds. Only an explicit fyi or result does not wake. Of never returns the zero Kind.
func Of(s string) Kind {
	if strings.TrimSpace(s) == "" {
		return Task
	}
	k, err := Parse(s)
	if err != nil {
		return Task
	}
	return k
}

// Norm is k with the zero value made Task.
func (k Kind) Norm() Kind {
	if k == "" {
		return Task
	}
	return k
}

func (k Kind) String() string { return string(k.Norm()) }

// Wakes reports whether mail of this kind wakes an idle recipient: blocker, question and task do; result and fyi wait
// for the recipient's next turn.
func (k Kind) Wakes() bool {
	switch k.Norm() {
	case Blocker, Question, Task:
		return true
	}
	return false
}

// ShortReply is the longest reply, in characters, that Tag downgrades.
const ShortReply = 280

// Tag applies the auto-tag rule: a reply (inReplyTo set) of kind question whose body has no '?' and is at most
// ShortReply characters asks nothing, so it goes as fyi. It tags, never refuses, and never downgrades a blocker or a
// task, so an unkinded reply (task) is untouched: a task is an instruction and needs no '?' ("Please deploy the fix") (codex H1 on #75).
func Tag(k Kind, inReplyTo uint64, body string) (Kind, bool) {
	k = k.Norm()
	if inReplyTo > 0 && k == Question && !strings.Contains(body, "?") && utf8.RuneCountInString(body) <= ShortReply {
		return FYI, true
	}
	return k, false
}

// TaggedNote is the sender's output when Tag downgraded.
const TaggedNote = "(tagged fyi: short reply with no question)"
