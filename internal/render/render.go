// Package render turns mail into the text a Claude session sees: a small
// batch in full, a large one as a digest of one line per message.
package render

import (
	"fmt"
	"strings"
	"time"

	"github.com/randybias/mash/internal/sap"
)

// Mail is one message as delivered.
type Mail struct {
	Seq   uint64
	From  string
	Trust sap.Trust
	At    time.Time
	Body  []byte
}

const maxFirstLine = 120

func text(b []byte) string { return strings.ToValidUTF8(string(b), "�") }

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
	return fmt.Sprintf("--- #%d from %s [%s] at %s\n%s", m.Seq, m.From, m.Trust, stamp(m.At), quote(m.Body))
}

// Line renders a message as one line: seq, sender, time, size, first line.
func Line(m Mail) string {
	return fmt.Sprintf("#%d\tfrom %s [%s]\t%s\t%d B\t%s", m.Seq, m.From, m.Trust, stamp(m.At), len(m.Body), firstLine(m.Body))
}

// Batch renders msgs for endpoint to: in full when there are at most maxMsgs
// and their bodies total at most maxBytes, otherwise as a digest.
func Batch(to string, msgs []Mail, maxMsgs, maxBytes int) string {
	total := 0
	for _, m := range msgs {
		total += len(m.Body)
	}
	var b strings.Builder
	if len(msgs) <= maxMsgs && total <= maxBytes {
		fmt.Fprintf(&b, "mash: %s for %s\n", plural(len(msgs)), to)
		for _, m := range msgs {
			b.WriteString("\n" + One(m) + "\n")
		}
		return b.String()
	}
	fmt.Fprintf(&b, "mash: %s for %s (%d B), shown as a digest. Read the ones you need with the read tool (by #seq).\n\n",
		plural(len(msgs)), to, total)
	for _, m := range msgs {
		fmt.Fprintf(&b, "#%d\tfrom %s [%s]\t%d B\t%s\n", m.Seq, m.From, m.Trust, len(m.Body), firstLine(m.Body))
	}
	return b.String()
}
