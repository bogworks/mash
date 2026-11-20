package render

import (
	"strings"
	"testing"
	"time"
)

func TestRelTime(t *testing.T) {
	now := time.Date(2026, 11, 20, 12, 0, 0, 0, time.UTC)
	for _, tc := range []struct {
		ago  time.Duration
		want string
	}{
		{-time.Hour, "just now"}, {0, "just now"}, {8 * time.Second, "8s ago"}, {10 * time.Second, "10s ago"},
		{59 * time.Second, "59s ago"}, {time.Minute, "1m ago"}, {90 * time.Second, "1m ago"},
		{59 * time.Minute, "59m ago"}, {3 * time.Hour, "3h ago"}, {23 * time.Hour, "23h ago"},
		{24 * time.Hour, "1d ago"}, {2*24*time.Hour + time.Hour, "2d ago"}, {30 * 24 * time.Hour, "30d ago"},
		{31 * 24 * time.Hour, "2026-10-20"}, {365 * 24 * time.Hour, "2025-11-20"},
	} {
		if got := RelTime(now, now.Add(-tc.ago)); got != tc.want {
			t.Errorf("RelTime(-%v) = %q, want %q", tc.ago, got, tc.want)
		}
	}
}

func TestTable(t *testing.T) {
	cols := []string{"name", "state", "note"}
	rows := [][]string{{"alice", "online", "hello world"}, {"bob", "away", ""}}
	for _, tc := range []struct {
		name     string
		width    int
		truncate bool
		want     string
	}{
		{"fits", 80, true, "NAME   STATE   NOTE\nalice  online  hello world\nbob    away"},
		{"no truncate", 10, false, "NAME   STATE   NOTE\nalice  online  hello world\nbob    away"},
		{"cut", 24, true, "NAME   STATE   NOTE\nalice  online  hello wo…\nbob    away"},
		{"width 0 means no cut", 0, true, "NAME   STATE   NOTE\nalice  online  hello world\nbob    away"},
		{"no room left", 5, true, "NAME   STATE   NOTE\nalice  online  hello world\nbob    away"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := Table(cols, rows, tc.width, tc.truncate); got != tc.want {
				t.Errorf("got\n%q\nwant\n%q", got, tc.want)
			}
		})
	}
	if got := Table(nil, nil, 80, true); got != "" {
		t.Errorf("empty = %q", got)
	}
	// a multi-byte last column is cut on a rune boundary
	got := Table([]string{"a", "b"}, [][]string{{"x", "héllo wörld"}}, 8, true)
	if want := "A  B\nx  héll…"; got != want {
		t.Errorf("got %q want %q", got, want)
	}
	// short rows are padded
	if got := Table([]string{"a", "b"}, [][]string{{"x"}}, 80, false); got != "A  B\nx" {
		t.Errorf("short row = %q", got)
	}
}

func TestWidth(t *testing.T) {
	env := func(v string) func(string) string { return func(string) string { return v } }
	for _, tc := range []struct {
		cols string
		tty  bool
		want int
	}{{"120", true, 120}, {"abc", true, 80}, {"", true, 80}, {"0", true, 80}, {"-5", true, 80}, {"120", false, 80}} {
		if got := Width(env(tc.cols), tc.tty); got != tc.want {
			t.Errorf("Width(%q,%v) = %d, want %d", tc.cols, tc.tty, got, tc.want)
		}
	}
}

func TestColor(t *testing.T) {
	on := Opts{TTY: true}
	t.Setenv("NO_COLOR", "")
	for _, tc := range []struct {
		o           Opts
		state, want string
	}{
		{on, "online", "\x1b[32mx\x1b[0m"}, {on, "ok", "\x1b[32mx\x1b[0m"}, {on, "FAIL", "\x1b[31mx\x1b[0m"},
		{on, "warn", "\x1b[33mx\x1b[0m"}, {on, "other", "x"},
		{Opts{}, "online", "x"}, {Opts{TTY: true, NoColor: true}, "online", "x"},
	} {
		if got := Color(tc.o, tc.state, "x"); got != tc.want {
			t.Errorf("Color(%+v,%q) = %q, want %q", tc.o, tc.state, got, tc.want)
		}
	}
	t.Setenv("NO_COLOR", "1")
	if got := Color(on, "ok", "x"); got != "x" {
		t.Errorf("NO_COLOR: %q", got)
	}
}

func TestPlural(t *testing.T) {
	for n, want := range map[int]string{0: "0 messages", 1: "1 message", 2: "2 messages"} {
		if got := Plural(n, "message"); got != want {
			t.Errorf("Plural(%d) = %q, want %q", n, got, want)
		}
	}
}

func TestDisplayWidth(t *testing.T) {
	for s, want := range map[string]int{"abc": 3, "": 0, "日本語": 6, "a日b": 4, "é": 1, "\U0001F600": 2, "한국": 4} {
		if got := DisplayWidth(s); got != want {
			t.Errorf("DisplayWidth(%q) = %d, want %d", s, got, want)
		}
	}
}

func TestTableMeasuresDisplayWidth(t *testing.T) {
	got := Table([]string{"name", "note"}, [][]string{{"日本", "x"}, {"ab", "y"}}, 80, false)
	want := "NAME  NOTE\n日本  x\nab    y"
	if got != want {
		t.Errorf("wide cells misaligned:\n%q\nwant\n%q", got, want)
	}
	// A last column of wide characters is cut by columns, not runes: 6 columns of prefix leaves 4, so 3 columns and "…".
	got = Table([]string{"a", "b"}, [][]string{{"x", "日本語日本語"}}, 10, true)
	if l := strings.Split(got, "\n")[1]; DisplayWidth(l) > 10 || !strings.HasSuffix(l, "…") {
		t.Errorf("truncated line %q is %d columns wide", l, DisplayWidth(l))
	}
}
