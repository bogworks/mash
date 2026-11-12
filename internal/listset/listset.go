// Package listset turns a list set into scoped-key permission templates and per-sender tags, and refuses to produce
// anything the NATS server would not enforce exactly as the lists say.
//
// The contract is mash-messaging-policy draft-2 (REQ-001 to REQ-006, AT-002, AT-004; ADR 0102). A list set is, for one
// NATS account (one range), a shared or closed switch per owner group and verb, and per sending address an allow list
// and a deny list of exact three-part addresses. This package has no file format (DEC-004 is open): ListSet is the
// shape a decoded list set would have, so that bad shapes can be refused.
//
// Validate is pure. Compile validates, builds, and then runs Check on its own output; Check expands every sender's
// tags through its template the way nats-server does and compares what the server would allow with what the lists
// give. Nothing is returned on any error.
package listset

import (
	"fmt"
	"strings"
)

// Verb is one kind of traffic. Mail, prompt and status are publish verbs; heartbeat is a subscribe verb (a sender
// watches the heartbeats it is allowed, and publishes only its own).
type Verb string

// The four verbs. Every owner group with a sender states a switch for each of them.
const (
	Mail      Verb = "mail"
	Prompt    Verb = "prompt"
	Status    Verb = "status"
	Heartbeat Verb = "heartbeat"
)

// Verbs lists the four verbs in the order the package reports them.
var Verbs = []Verb{Mail, Prompt, Status, Heartbeat}

// subject is the subject word of v: agents.<subject>.<range>.<owner>.<name>. Heartbeat is "hb" (SAP).
func (v Verb) subject() string {
	if v == Heartbeat {
		return "hb"
	}
	return string(v)
}

func (v Verb) valid() bool {
	for _, x := range Verbs {
		if v == x {
			return true
		}
	}
	return false
}

// Switch is the setting of one owner group for one verb. Shared turns the owner line on: every sender of the group
// reaches every address of the group. Closed leaves a sender only the addresses on its allow list.
type Switch string

// The two switch values.
const (
	Shared Switch = "shared"
	Closed Switch = "closed"
)

// ListSet is the switches and lists for one account.
type ListSet struct {
	// Range is the account's range part. Every address and group must carry it.
	Range string
	// Switches holds the group-wide switches. A sender's owner group must state all four verbs, each once.
	Switches []SwitchEntry
	// Senders holds one entry per sending address. A sender with empty lists is still listed: it gets a credential
	// that reaches only what its group's shared verbs give.
	Senders []SenderLists
}

// SwitchEntry sets one verb for one owner group. Group is "range.owner". A three-part address in Group is a switch
// for a single sender, which is refused.
type SwitchEntry struct {
	Group  string
	Verb   Verb
	Switch Switch
}

// SenderLists are the exceptions of one sending address, all three-part addresses "range.owner.name".
//
// Allow names the addresses reachable beyond the owner line. Deny names addresses of the sender's own owner group
// taken away from a shared verb; it is refused when the group has no shared verb, when the target is outside the
// sender's owner group, or when the same address is also allowed. An empty list means nobody, never everyone.
type SenderLists struct {
	Sender string
	Allow  []string
	Deny   []string
}

// Problem is one refused entry and the reason.
type Problem struct {
	// Entry names the entry, for example `allow of "foo.c.core": "foo.c.*"`.
	Entry  string
	Reason string
}

func (p Problem) String() string { return p.Entry + ": " + p.Reason }

func joinProblems(ps []Problem) string {
	var b strings.Builder
	for i, p := range ps {
		if i > 0 {
			b.WriteString("; ")
		}
		b.WriteString(p.String())
	}
	return b.String()
}

// ValidationError is the error Validate and Compile return for a list set that is refused. It lists every bad entry.
type ValidationError struct{ Problems []Problem }

func (e *ValidationError) Error() string {
	return fmt.Sprintf("list set refused, %d problem(s): %s", len(e.Problems), joinProblems(e.Problems))
}

// CheckError is the error Check (and so Compile) returns when compiled output would not enforce the lists exactly.
// Each problem names the sender and the verb where it applies.
type CheckError struct{ Problems []Problem }

func (e *CheckError) Error() string {
	return fmt.Sprintf("compiled policy refused, %d problem(s): %s", len(e.Problems), joinProblems(e.Problems))
}
