// Package inventory is the message-by-message bookkeeping of AT-011: every message of the proof has a stable identity
// and an expected disposition, the run tallies what it saw, and Compare holds the two together one message at a time.
// A count that balances while one message is lost and another doubled fails, because each message is judged alone.
// The package is pure: it reads no file and starts nothing.
package inventory

import (
	"fmt"
	"sort"
	"strings"
)

// Expect is the disposition a message must end with.
type Expect string

const (
	// Delivered is mail a session was already shown before the upgrade: it must not be shown again.
	Delivered Expect = "delivered"
	// Pending is mail stored or spooled and not yet shown: it must be shown exactly once.
	Pending Expect = "pending"
	// Refused is mail the switched-on release lists as refused with its reason, and never shows.
	Refused Expect = "refused"
)

// ID is the stable identity of a message: its stream sequence, its sender and its nonce. The nonce is the message's
// own unique token: the header nonce of mail the test injected (it is also written in the body), and the token the
// test wrote into the body of mail it sent through the program.
type ID struct {
	Seq    uint64 `json:"seq"`
	Sender string `json:"sender"`
	Nonce  string `json:"nonce"`
}

func (i ID) String() string { return fmt.Sprintf("#%d from %s nonce %s", i.Seq, i.Sender, i.Nonce) }

// Entry is one inventoried message and what must become of it.
type Entry struct {
	ID     ID     `json:"id"`
	Phase  string `json:"phase"` // the step that stored it: "step2", "step3", "step4"
	Label  string `json:"label"`
	Expect Expect `json:"expect"`
	// Reason is the refusal class a Refused message must be listed with.
	Reason string `json:"reason,omitempty"`
}

// Tally is what the run saw: how many times a session was shown each message, and each refusal listing.
type Tally struct {
	shown   map[ID]int
	refused map[uint64][]string // by stream sequence: a refused row has no sender and no body, only its number and class
}

// NewTally returns an empty tally.
func NewTally() *Tally { return &Tally{shown: map[ID]int{}, refused: map[uint64][]string{}} }

// AddShown records that a session was shown the message once.
func (t *Tally) AddShown(id ID) { t.shown[id]++ }

// AddRefused records one listing of the sequence as refused, with the reason the listing gave.
func (t *Tally) AddRefused(seq uint64, reason string) {
	t.refused[seq] = append(t.refused[seq], reason)
}

// Shown is how many times id was shown.
func (t *Tally) Shown(id ID) int { return t.shown[id] }

// Problem is one message that did not end as expected.
type Problem struct {
	ID   ID
	What string
}

func (p Problem) String() string { return p.ID.String() + ": " + p.What }

// Compare holds every entry to the tally, message by message, and returns each deviation, in sequence order. A message
// shown or listed that is not in the inventory is a problem too: it is a message nobody expected.
func Compare(entries []Entry, t *Tally) []Problem {
	var ps []Problem
	known := map[ID]bool{}
	seqs := map[uint64]bool{}
	for _, e := range entries {
		known[e.ID] = true
		seqs[e.ID.Seq] = true
		shown := t.shown[e.ID]
		listed := t.refused[e.ID.Seq]
		add := func(f string, a ...any) {
			ps = append(ps, Problem{e.ID, fmt.Sprintf("%s (%s): ", e.Label, e.Expect) + fmt.Sprintf(f, a...)})
		}
		switch e.Expect {
		case Delivered, Pending:
			switch {
			case shown == 0:
				add("lost: never shown to a session")
			case shown > 1:
				add("doubled: shown %d times", shown)
			}
			if len(listed) > 0 {
				add("listed as refused (%s) though it is not refused", strings.Join(listed, ", "))
			}
		case Refused:
			if shown > 0 {
				add("shown to a session %d times though it is refused", shown)
			}
			switch {
			case len(listed) == 0:
				add("never listed as refused")
			case len(listed) > 1:
				add("listed as refused %d times, want once", len(listed))
			case listed[0] != e.Reason:
				add("listed as refused with the reason %q, want %q", listed[0], e.Reason)
			}
		default:
			add("unknown expected disposition %q", e.Expect)
		}
	}
	for id, n := range t.shown {
		if !known[id] {
			ps = append(ps, Problem{id, fmt.Sprintf("not in the inventory but shown to a session %d times", n)})
		}
	}
	for seq := range t.refused {
		if !seqs[seq] {
			ps = append(ps, Problem{ID{Seq: seq}, "not in the inventory but listed as refused"})
		}
	}
	sort.SliceStable(ps, func(i, j int) bool {
		if ps[i].ID.Seq != ps[j].ID.Seq {
			return ps[i].ID.Seq < ps[j].ID.Seq
		}
		return ps[i].What < ps[j].What
	})
	return ps
}

// Check validates an inventory itself: sequences are unique, every identity is complete, a refused entry names its
// reason and nothing else does.
func Check(entries []Entry) []string {
	var bad []string
	seen := map[uint64]string{}
	nonces := map[string]bool{}
	for _, e := range entries {
		id := e.ID
		if id.Seq == 0 || id.Sender == "" || id.Nonce == "" {
			bad = append(bad, fmt.Sprintf("%s: identity incomplete", e.Label))
		}
		if prev, ok := seen[id.Seq]; ok {
			bad = append(bad, fmt.Sprintf("%s and %s share stream sequence %d", prev, e.Label, id.Seq))
		}
		seen[id.Seq] = e.Label
		if nonces[id.Nonce] {
			bad = append(bad, fmt.Sprintf("%s: nonce %s used twice", e.Label, id.Nonce))
		}
		nonces[id.Nonce] = true
		if (e.Expect == Refused) != (e.Reason != "") {
			bad = append(bad, fmt.Sprintf("%s: a reason goes with a refused entry and only with it", e.Label))
		}
	}
	return bad
}

// Row is one line of a `mashctl endpoint inbox` listing.
type Row struct {
	Seq     uint64
	Sender  string
	Refused bool
	Reason  string
}

// Same holds the listing before the upgrade and the one after to step 7 of AT-011: the same messages by identity (every
// sequence once, none lost, none added), and each message the inventory expects refused marked refused after, with its
// reason. Before the upgrade it must not be marked refused when the previous release did not refuse these shapes
// (prevRefuses false), and must be marked refused when it did (prevRefuses true; its reason then is not compared, the
// old wording may differ). A sender is compared when both listings give one and the message is not refused after: a refused row shows no sender,
// by design, where the earlier release showed whatever the broken header said.
func Same(before, after []Row, entries []Entry, prevRefuses bool) []string {
	var bad []string
	index := func(rows []Row, which string) map[uint64]Row {
		m := map[uint64]Row{}
		for _, r := range rows {
			if _, dup := m[r.Seq]; dup {
				bad = append(bad, fmt.Sprintf("%s listing: #%d appears twice", which, r.Seq))
			}
			m[r.Seq] = r
		}
		return m
	}
	b, a := index(before, "before"), index(after, "after")
	for seq, br := range b {
		ar, ok := a[seq]
		if !ok {
			bad = append(bad, fmt.Sprintf("#%d is listed before the upgrade and not after", seq))
			continue
		}
		if !ar.Refused && br.Sender != "" && ar.Sender != "" && br.Sender != ar.Sender {
			bad = append(bad, fmt.Sprintf("#%d: sender %q before, %q after", seq, br.Sender, ar.Sender))
		}
	}
	for seq := range a {
		if _, ok := b[seq]; !ok {
			bad = append(bad, fmt.Sprintf("#%d is listed after the upgrade and not before", seq))
		}
	}
	for _, e := range entries {
		if e.Expect != Refused {
			if ar, ok := a[e.ID.Seq]; ok && ar.Refused {
				bad = append(bad, fmt.Sprintf("#%d (%s) is marked refused after the upgrade but is not expected refused", e.ID.Seq, e.Label))
			}
			continue
		}
		ar, ok := a[e.ID.Seq]
		switch {
		case !ok:
			bad = append(bad, fmt.Sprintf("#%d (%s) is not listed after the upgrade", e.ID.Seq, e.Label))
		case !ar.Refused:
			bad = append(bad, fmt.Sprintf("#%d (%s) is not marked refused after the upgrade", e.ID.Seq, e.Label))
		case ar.Reason != e.Reason:
			bad = append(bad, fmt.Sprintf("#%d (%s) is marked refused with %q, want %q", e.ID.Seq, e.Label, ar.Reason, e.Reason))
		}
		if br, ok := b[e.ID.Seq]; ok {
			switch {
			case !prevRefuses && br.Refused:
				bad = append(bad, fmt.Sprintf("#%d (%s) is already marked refused before the upgrade", e.ID.Seq, e.Label))
			case prevRefuses && !br.Refused:
				bad = append(bad, fmt.Sprintf("#%d (%s) is not marked refused before the upgrade, and the previous release refuses it", e.ID.Seq, e.Label))
			}
		}
	}
	sort.Strings(bad)
	return bad
}
