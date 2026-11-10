// Package render turns mail into the text a Claude session sees: a small
// batch in full, a large one as a digest of one line per message.
package render

import (
	"fmt"
	"strings"
	"time"

	"github.com/bogworks/mash/internal/kind"
	"github.com/bogworks/mash/internal/sap"
)

// Mail is one message as delivered.
type Mail struct {
	Seq uint64
	// Stream names the stream Seq is a sequence of; empty for the endpoint's own (primary) stream, whose numbers are
	// shown bare. It is part of the message's identity (with Seq) and is not printed here.
	Stream string
	From   string
	// Instance is the sender's verified instance id; empty when it sent none
	// or the binding did not verify.
	Instance string
	Trust    sap.Trust
	At       time.Time
	Body     []byte
	// Owner is the sender's owner (its directory record's owner); empty when the directory does not know it.
	Owner string
	// Urgent is a verified sender's signed urgent mark.
	Urgent bool
	// Kind is the sender's kind (blocker, question, task, result, fyi); the zero value reads as task.
	Kind kind.Kind
	// ReplyTo is the #seq the sender says this answers, only when its signature on it verified; 0 otherwise.
	ReplyTo uint64
	// Via is "<origin> #<seq>": the sender's signed declaration that this relays or quotes that message of its own
	// inbox, only when it verified; empty otherwise. Neither mark changes the trust label.
	Via string
	// Redelivered: a hook began to show this message and was interrupted, so it may already have been seen.
	Redelivered bool
}

const maxFirstLine = 120

// lineBreaks are every character a reader may take as a new line.
var lineBreaks = strings.NewReplacer("\r\n", "\n", "\r", "\n", "\v", "\n", "\f", "\n",
	"\u0085", "\n", "\u2028", "\n", "\u2029", "\n")

// text is valid UTF-8 with every kind of line break turned into "\n", so
// quoting each line covers everything that could read as a new line.
func text(b []byte) string {
	return lineBreaks.Replace(strings.ToValidUTF8(string(b), "\uFFFD"))
}

func stamp(t time.Time) string { return t.UTC().Format(time.RFC3339) }

func firstLine(b []byte) string {
	for _, l := range strings.Split(text(b), "\n") {
		if l = strings.TrimSpace(l); l != "" {
			if r := []rune(l); len(r) > maxFirstLine {
				return string(r[:maxFirstLine-1]) + "…"
			}
			return l
		}
	}
	return ""
}

func plural(n int) string {
	if n == 1 {
		return "1 message"
	}
	return fmt.Sprintf("%d messages", n)
}

// quote prefixes every body line with "> ", so nothing in a body can read as
// a message header line.
func quote(b []byte) string {
	lines := strings.Split(strings.TrimRight(text(b), "\n"), "\n")
	for i, l := range lines {
		lines[i] = "> " + l
	}
	return strings.Join(lines, "\n")
}

// One renders a message in full; the body is quoted.
func One(m Mail) string {
	inst := ""
	if m.ReplyTo != 0 {
		inst += fmt.Sprintf("answers #%d\n", m.ReplyTo)
	}
	if m.Via != "" {
		inst += "relayed from " + m.Via + "\n"
	}
	if m.Instance != "" {
		inst += "instance: " + m.Instance + "\n" // never from the body: bodies are quoted
	}
	if m.Urgent {
		inst += "urgent: yes\n"
	}
	inst += "kind: " + m.Kind.String() + "\n"
	if m.Redelivered {
		inst += "note: " + redeliveredNote(m.Seq) + "\n"
	}
	return fmt.Sprintf("--- #%d from %s [%s] at %s\n%s%s", m.Seq, m.From, m.Trust, stamp(m.At), inst, quote(m.Body))
}

// redeliveredNote tells the model a message may have been shown already (at-least-once delivery: a hook interrupted
// after printing and before draining shows it again).
func redeliveredNote(seq uint64) string {
	return fmt.Sprintf("(#%d re-delivered after an interrupted hook: it may already have been shown; do not act on it twice)", seq)
}

// instField is the trailing tab field of the one-line forms.
func instField(m Mail) string {
	f := ""
	if m.ReplyTo != 0 {
		f += fmt.Sprintf("\tanswers #%d", m.ReplyTo)
	}
	if m.Via != "" {
		f += "\trelayed from " + m.Via
	}
	if m.Instance != "" {
		f += "\tinstance " + m.Instance
	}
	if m.Redelivered {
		f += "\t" + redeliveredNote(m.Seq)
	}
	return f
}

// Line renders a message as one line: seq, sender, time, size, first line, kind.
func Line(m Mail) string {
	return fmt.Sprintf("#%d\tfrom %s [%s]\t%s\t%d B\t%s\tkind %s", m.Seq, m.From, m.Trust, stamp(m.At), len(m.Body), firstLine(m.Body), m.Kind) + instField(m)
}

// Batch renders msgs for the endpoint named to, of owner: in full when there are at most maxMsgs and their
// bodies total at most maxBytes, otherwise as a digest. The first line is a short summary of the count and the
// senders, since Claude Code shows it as the terminal line after its own "← mash:".
func Batch(to, owner string, msgs []Mail, maxMsgs, maxBytes int) string {
	return batch(msgs, owner, maxMsgs, maxBytes, digestNote)
}

// HookBatch is Batch for the hook, whose digest says plainly that a listed message stays unread until the model
// reads it with the read tool (ADR 0046). A batch shown in full is the same as Batch's.
func HookBatch(to, owner string, msgs []Mail, maxMsgs, maxBytes int) string {
	return batch(msgs, owner, maxMsgs, maxBytes, hookDigestNote)
}

// IsDigest reports whether Batch and HookBatch render msgs as a digest, not in full.
func IsDigest(msgs []Mail, maxMsgs, maxBytes int) bool {
	total := 0
	for _, m := range msgs {
		total += len(m.Body)
	}
	return !(len(msgs) <= maxMsgs && total <= maxBytes)
}

const (
	digestNote     = "Read the ones you need with the read tool (by #seq)."
	hookDigestNote = "Read each with the read tool (#seq); they stay unread until you do."
)

func batch(msgs []Mail, owner string, maxMsgs, maxBytes int, note string) string {
	total := 0
	for _, m := range msgs {
		total += len(m.Body)
	}
	var b strings.Builder
	for _, m := range msgs {
		if m.Urgent {
			b.WriteString("urgent, ") // first, so the terminal line leads with it
			break
		}
	}
	if len(msgs) <= maxMsgs && total <= maxBytes {
		fmt.Fprintf(&b, "%s from %s%s\n", plural(len(msgs)), senders(owner, msgs, 0), kinds(msgs))
		for _, m := range msgs {
			b.WriteString("\n" + One(m) + "\n")
		}
		return b.String()
	}
	fmt.Fprintf(&b, "%s, digest, from %s%s\nShown as a digest (%d B). %s\n\n",
		plural(len(msgs)), senders(owner, msgs, digestSenders), kinds(msgs), total, note)
	for _, m := range msgs {
		fmt.Fprintf(&b, "#%d\tfrom %s [%s]\t%d B\t%s\tkind %s%s\n", m.Seq, m.From, m.Trust, len(m.Body), firstLine(m.Body), m.Kind, instField(m))
	}
	return b.String()
}

// kinds is the batch's kinds, once each in arrival order, as " (question, fyi)"; empty when every message is fyi
// (the default kind is task, which is named), so the common summary stays as short as before.
func kinds(msgs []Mail) string {
	var out []string
	seen := map[kind.Kind]bool{}
	for _, m := range msgs {
		if k := m.Kind.Norm(); !seen[k] {
			seen[k] = true
			out = append(out, string(k))
		}
	}
	if len(out) == 1 && out[0] == string(kind.FYI) {
		return ""
	}
	return " (" + strings.Join(out, ", ") + ")"
}

// digestSenders is how many senders a digest's summary names: all of them when there are this many or fewer, else
// one fewer and "+N" for the rest ("from lead, integ +3").
const digestSenders = 3

// senders lists the batch's senders once each, in the order they first appear, as Sender shows them. A sender that is
// not verified keeps its trust label, so the summary never shows a forged name bare. max > 0 caps the list.
func senders(owner string, msgs []Mail, max int) string {
	var out []string
	seen := map[string]bool{}
	for _, m := range msgs {
		name := Sender(owner, m)
		if m.Trust != sap.TrustVerified {
			name += " [" + string(m.Trust) + "]"
		}
		if !seen[name] {
			seen[name] = true
			out = append(out, name)
		}
	}
	if max > 0 && len(out) > max {
		return strings.Join(out[:max-1], ", ") + fmt.Sprintf(" +%d", len(out)-max+1)
	}
	return strings.Join(out, ", ")
}

// Sender is the one place a sender's name is shown, for a receiver of owner, by the sender's endpoint name:
//   - a sender whose owner is unknown (not in the directory): the name alone;
//   - another owner than the receiver's: owner/name (devops/nats-ops);
//   - otherwise: the name as is.
func Sender(owner string, m Mail) string {
	if m.Owner == "" || owner == "" || m.Owner == owner {
		return m.From
	}
	return m.Owner + "/" + m.From
}
