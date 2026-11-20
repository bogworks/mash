// Package render holds the small text helpers shared by the commands' human output: relative times, aligned tables,
// the terminal width, colour and plural counts.
package render

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
	"unicode"
)

// Opts are the output options a command's Text method receives.
type Opts struct {
	Verbose bool
	NoColor bool
	Width   int  // terminal columns; 0 means 80
	TTY     bool // stdout is a terminal
}

// RelTime renders t relative to now: "10s ago", "1m ago", "3h ago", "2d ago", and the date (2026-11-19) once t is more
// than 30 days old. A t at or after now reads "0s ago".
func RelTime(now, t time.Time) string {
	d := now.Sub(t)
	if d < 0 {
		d = 0
	}
	switch {
	case d < time.Second:
		return "just now"
	case d < time.Minute:
		return fmt.Sprintf("%ds ago", int(d/time.Second))
	}
	hours := int(d / time.Hour)
	switch {
	case hours < 1:
		return fmt.Sprintf("%dm ago", int(d/time.Minute))
	case hours < 24:
		return fmt.Sprintf("%dh ago", hours)
	case hours/24 <= 30:
		return fmt.Sprintf("%dd ago", hours/24)
	}
	return t.Format("2006-01-02")
}

// wide lists the code point ranges a terminal draws two columns wide (the East Asian Wide and Fullwidth ranges of
// Unicode, and the emoji blocks). golang.org/x/text/width is not a dependency of this module, so the table is kept here.
var wide = [][2]rune{
	{0x1100, 0x115F}, {0x231A, 0x231B}, {0x2329, 0x232A}, {0x23E9, 0x23EC}, {0x23F0, 0x23F0}, {0x23F3, 0x23F3},
	{0x25FD, 0x25FE}, {0x2614, 0x2615}, {0x2648, 0x2653}, {0x267F, 0x267F}, {0x2693, 0x2693}, {0x26A1, 0x26A1},
	{0x26AA, 0x26AB}, {0x26BD, 0x26BE}, {0x26C4, 0x26C5}, {0x26CE, 0x26CE}, {0x26D4, 0x26D4}, {0x26EA, 0x26EA},
	{0x26F2, 0x26F3}, {0x26F5, 0x26F5}, {0x26FA, 0x26FA}, {0x26FD, 0x26FD}, {0x2705, 0x2705}, {0x270A, 0x270B},
	{0x2728, 0x2728}, {0x274C, 0x274C}, {0x274E, 0x274E}, {0x2753, 0x2755}, {0x2757, 0x2757}, {0x2795, 0x2797},
	{0x27B0, 0x27B0}, {0x27BF, 0x27BF}, {0x2B1B, 0x2B1C}, {0x2B50, 0x2B50}, {0x2B55, 0x2B55}, {0x2E80, 0x303E},
	{0x3041, 0x33FF}, {0x3400, 0x4DBF}, {0x4E00, 0x9FFF}, {0xA000, 0xA4CF}, {0xA960, 0xA97F}, {0xAC00, 0xD7A3},
	{0xF900, 0xFAFF}, {0xFE10, 0xFE19}, {0xFE30, 0xFE6F}, {0xFF00, 0xFF60}, {0xFFE0, 0xFFE6}, {0x1F300, 0x1F64F},
	{0x1F680, 0x1F6FF}, {0x1F900, 0x1F9FF}, {0x20000, 0x3FFFD},
}

func runeWidth(r rune) int {
	switch {
	case r == 0 || unicode.In(r, unicode.Mn, unicode.Me, unicode.Cf):
		return 0
	}
	for _, w := range wide {
		if r >= w[0] && r <= w[1] {
			return 2
		}
	}
	return 1
}

// DisplayWidth is the number of terminal columns s takes: east-asian wide characters and emoji count 2, combining marks
// and zero-width characters 0, everything else 1.
func DisplayWidth(s string) int {
	n := 0
	for _, r := range s {
		n += runeWidth(r)
	}
	return n
}

// cut returns the longest prefix of s that is at most cols columns wide.
func cut(s string, cols int) string {
	n := 0
	for i, r := range s {
		n += runeWidth(r)
		if n > cols {
			return s[:i]
		}
	}
	return s
}

func runeLen(s string) int { return DisplayWidth(s) }

// Table lays out a header and rows in columns separated by two spaces. The header is upper-cased. No line has trailing
// spaces. When truncateLast is set and a line is wider than width, the last column is cut to fit and ends with "…";
// widths are display columns (see DisplayWidth) and cuts fall on rune boundaries. A last column that cannot be shown in at least one rune is left whole. The result has no
// final newline.
func Table(cols []string, rows [][]string, width int, truncateLast bool) string {
	n := len(cols)
	if n == 0 {
		return ""
	}
	head := make([]string, n)
	for i, c := range cols {
		head[i] = strings.ToUpper(c)
	}
	all := append([][]string{head}, rows...)
	w := make([]int, n)
	for _, r := range all {
		for i := 0; i < n && i < len(r); i++ {
			if l := runeLen(r[i]); l > w[i] {
				w[i] = l
			}
		}
	}
	prefix := 0
	for i := 0; i < n-1; i++ {
		prefix += w[i] + 2
	}
	lines := make([]string, 0, len(all))
	for _, r := range all {
		cells := make([]string, n)
		copy(cells, r)
		var b strings.Builder
		for i := 0; i < n-1; i++ {
			b.WriteString(cells[i])
			b.WriteString(strings.Repeat(" ", w[i]-runeLen(cells[i])+2))
		}
		last := cells[n-1]
		if truncateLast && width > 0 {
			if avail := width - prefix; avail >= 1 && runeLen(last) > avail {
				last = cut(last, avail-1) + "…"
			}
		}
		b.WriteString(last)
		lines = append(lines, strings.TrimRight(b.String(), " "))
	}
	return strings.Join(lines, "\n")
}

// Width is the number of columns to lay out for: COLUMNS when it is a positive number and out is a terminal, otherwise
// 80. env looks up an environment variable.
func Width(env func(string) string, isTTY bool) int {
	if !isTTY {
		return 80
	}
	if n, err := strconv.Atoi(strings.TrimSpace(env("COLUMNS"))); err == nil && n > 0 {
		return n
	}
	return 80
}

// Color wraps text in an ANSI colour for state, but only on a terminal with colour on: o.TTY, not o.NoColor, and NO_COLOR
// unset or empty. "online" and "ok" are green, "FAIL" and "fail" red, "warn" yellow; any other state is plain.
func Color(o Opts, state, text string) string {
	if !o.TTY || o.NoColor || os.Getenv("NO_COLOR") != "" {
		return text
	}
	var code string
	switch state {
	case "online", "ok":
		code = "32"
	case "FAIL", "fail":
		code = "31"
	case "warn":
		code = "33"
	default:
		return text
	}
	return "\x1b[" + code + "m" + text + "\x1b[0m"
}

// Plural renders a count with its noun: "1 message", "3 messages". The plural adds "s".
func Plural(n int, noun string) string {
	if n == 1 {
		return "1 " + noun
	}
	return fmt.Sprintf("%d %ss", n, noun)
}

const day = 24 * time.Hour

// Age renders how long something has waited, for a line of text and a JSON value: "10s", "5m", "1h 12m", "2d 3h". It
// shows the two largest units that are not zero (seconds under a minute, minutes under an hour, hours and minutes under
// a day, days and hours after), and rounds down. A negative d reads "0s".
func Age(d time.Duration) string {
	if d < 0 {
		d = 0
	}
	two := func(big int, bigU string, small int, smallU string) string {
		if small == 0 {
			return fmt.Sprintf("%d%s", big, bigU)
		}
		return fmt.Sprintf("%d%s %d%s", big, bigU, small, smallU)
	}
	switch {
	case d < time.Minute:
		return fmt.Sprintf("%ds", int(d/time.Second))
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d/time.Minute))
	case d < day:
		return two(int(d/time.Hour), "h", int(d%time.Hour/time.Minute), "m")
	}
	return two(int(d/day), "d", int(d%day/time.Hour), "h")
}

// Cut shortens s to at most cols display columns, ending in "…" when it was cut (the ellipsis is within cols). A cols
// below 1 returns s whole.
func Cut(s string, cols int) string {
	if cols < 1 || DisplayWidth(s) <= cols {
		return s
	}
	return cut(s, cols-1) + "…"
}
