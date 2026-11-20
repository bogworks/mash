// Package render holds the small text helpers shared by the commands' human output: relative times, aligned tables,
// the terminal width, colour and plural counts.
package render

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
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

func runeLen(s string) int { return len([]rune(s)) }

// Table lays out a header and rows in columns separated by two spaces. The header is upper-cased. No line has trailing
// spaces. When truncateLast is set and a line is wider than width, the last column is cut to fit and ends with "…";
// cuts fall on rune boundaries. A last column that cannot be shown in at least one rune is left whole. The result has no
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
				last = string([]rune(last)[:avail-1]) + "…"
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
