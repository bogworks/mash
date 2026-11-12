package ledger

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestFoldCollapsesRunsAndKeepsGenerationsApart(t *testing.T) {
	p := filepath.Join(t.TempDir(), File)
	l := New(p)
	for _, s := range [][]uint64{{1, 2, 3}, {5}, {4}, {7, 8}} {
		if err := l.Note("g1", Presented, s...); err != nil {
			t.Fatal(err)
		}
	}
	if err := l.Note("g2", Presented, 1, 2); err != nil {
		t.Fatal(err)
	}
	st, err := Read(p)
	if err != nil {
		t.Fatal(err)
	}
	g1 := st.Gens["g1"]
	if want := (Ranges{{1, 5}, {7, 8}}); !equal(g1.P, want) {
		t.Fatalf("g1 presented %v, want %v", g1.P, want)
	}
	if want := (Ranges{{1, 2}}); !equal(st.Gens["g2"].P, want) {
		t.Fatalf("g2 presented %v", st.Gens["g2"].P)
	}
	if n, _, _, _ := st.Total(); n != 9 {
		t.Fatalf("total presented %d, want 9", n)
	}
}

func equal(a, b Ranges) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func TestWritingTheSameSequenceTwiceIsHarmless(t *testing.T) {
	p := filepath.Join(t.TempDir(), File)
	l := New(p)
	for range 2 {
		if err := l.Note("g", Presented, 4, 5); err != nil {
			t.Fatal(err)
		}
	}
	st, _ := Read(p)
	if n, _, _, _ := st.Total(); n != 2 {
		t.Fatalf("presented %d after a repeated write, want 2", n)
	}
}

func TestTornLastLineIsSkippedCountedAndClosed(t *testing.T) {
	p := filepath.Join(t.TempDir(), File)
	l := New(p)
	if err := l.Note("g", Presented, 1, 2); err != nil {
		t.Fatal(err)
	}
	f, err := os.OpenFile(p, os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	f.WriteString(`{"gen":"g","t":"2026-11-12T00:00:00Z","kind":"pres`) // a crash mid-write
	f.Close()
	st, _ := Read(p)
	if st.Torn != 1 {
		t.Fatalf("torn %d, want 1", st.Torn)
	}
	if n, _, _, _ := st.Total(); n != 2 {
		t.Fatalf("presented %d, want 2", n)
	}
	// A new writer (a restarted serve) must not glue its first line to the torn one.
	if err := New(p).Note("g", Presented, 3); err != nil {
		t.Fatal(err)
	}
	st, _ = Read(p)
	if n, _, _, _ := st.Total(); n != 3 {
		t.Fatalf("presented %d after the next write, want 3 (the torn line swallowed it)", n)
	}
	if st.Torn != 1 {
		t.Fatalf("torn %d, want still 1", st.Torn)
	}
}

func TestKindsAreCountedApartAndChannelIsNotPresented(t *testing.T) {
	p := filepath.Join(t.TempDir(), File)
	l := New(p)
	l.Note("g", Presented, 1)
	l.Note("g", Refused, 2)
	l.Note("g", Channel, 3, 4)
	l.NoteDuplicate("g", 5, 1) // repeats a presented message
	l.NoteDuplicate("g", 6, 0) // repeats nothing known
	st, _ := Read(p)
	pr, rf, du, ch := st.Total()
	if pr != 1 || rf != 1 || du != 2 || ch != 2 {
		t.Fatalf("counts %d %d %d %d", pr, rf, du, ch)
	}
	g := st.Gens["g"]
	if g.P.Has(3) || g.P.Has(4) {
		t.Fatal("a channel-acked message counted as presented")
	}
	acc := g.Accounted()
	if !acc.Has(5) {
		t.Fatal("a duplicate of a presented message is not accounted")
	}
	if acc.Has(6) {
		t.Fatal("a duplicate that names no original was accounted: a loss could hide as a duplicate")
	}
}

func TestDuplicateOfAnUnpresentedMessageExplainsNothing(t *testing.T) {
	p := filepath.Join(t.TempDir(), File)
	l := New(p)
	l.Note("g", Presented, 1)
	l.NoteDuplicate("g", 3, 2) // 2 is not in the ledger
	st, _ := Read(p)
	if st.Gens["g"].Accounted().Has(3) {
		t.Fatal("accounted a duplicate of a sequence the ledger does not hold")
	}
}

func TestLost(t *testing.T) {
	g := &Gen{}
	for _, s := range [][2]uint64{{3, 5}, {8, 9}} {
		g.P.Add(s[0], s[1])
	}
	cases := []struct {
		first        uint64
		lost, before uint64
	}{
		{1, 0, 0},
		{2, 0, 1},  // below the base: nothing claimed
		{4, 0, 2},  // 1..2 before the ledger
		{10, 2, 2}, // 6 and 7 are gaps
	}
	for _, c := range cases {
		lost, before := g.Lost(c.first)
		if lost != c.lost || before != c.before {
			t.Errorf("Lost(%d) = %d, %d; want %d, %d", c.first, lost, before, c.lost, c.before)
		}
	}
	// a ledger that covers everything
	all := &Gen{}
	all.P.Add(1, 20)
	if lost, before := all.Lost(21); lost != 0 || before != 0 {
		t.Errorf("full ledger: lost %d before %d", lost, before)
	}
}

func TestCompactionSnapshotRoundTrips(t *testing.T) {
	p := filepath.Join(t.TempDir(), File)
	l := New(p)
	l.Note("a", Presented, 1, 2, 3, 5)
	l.Note("a", Refused, 4)
	l.Note("a", Channel, 9)
	l.NoteDuplicate("a", 6, 1)
	l.NoteDuplicate("a", 7, 0)
	l.Note("b", Presented, 1)
	before, _ := Read(p)
	if err := compact(p); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(p)
	if n := strings.Count(string(b), "\n"); n != 2 {
		t.Fatalf("%d lines after compaction, want one per generation:\n%s", n, b)
	}
	fi, _ := os.Stat(p)
	if fi.Mode().Perm() != 0o600 {
		t.Fatalf("mode %v", fi.Mode())
	}
	after, _ := Read(p)
	for _, id := range []string{"a", "b"} {
		x, y := before.Gens[id], after.Gens[id]
		if !equal(x.P, y.P) || !equal(x.R, y.R) || !equal(x.C, y.C) {
			t.Fatalf("%s changed: %+v vs %+v", id, x, y)
		}
		if !equal(x.Accounted(), y.Accounted()) {
			t.Fatalf("%s accounted changed: %v vs %v", id, x.Accounted(), y.Accounted())
		}
	}
	a1, a2 := before.Summarize(), after.Summarize()
	if a1.Presented != a2.Presented || a1.Refused != a2.Refused || a1.Duplicate != a2.Duplicate || a1.Channel != a2.Channel {
		t.Fatalf("counts changed: %+v vs %+v", a1, a2)
	}
	if !a2.First.Equal(a1.First) {
		t.Fatalf("first record time %v became %v", a1.First, a2.First)
	}
	// further writes after a compaction still fold
	l.Note("a", Presented, 10)
	st, _ := Read(p)
	if !st.Gens["a"].P.Has(10) {
		t.Fatal("a write after compaction was lost")
	}
}

func TestWriteCompactsPastTheSizeLimit(t *testing.T) {
	old := compactAt
	compactAt = 2000
	t.Cleanup(func() { compactAt = old })
	p := filepath.Join(t.TempDir(), File)
	l := New(p)
	for i := uint64(1); i < 200; i += 2 { // alternating: the ranges never collapse
		if err := l.Note("g", Presented, i); err != nil {
			t.Fatal(err)
		}
	}
	b, _ := os.ReadFile(p)
	if n := strings.Count(string(b), "\n"); n >= 100 {
		t.Fatalf("%d lines: never compacted", n)
	}
	st, _ := Read(p)
	if n, _, _, _ := st.Total(); n != 100 {
		t.Fatalf("presented %d after compaction, want 100", n)
	}
}

func TestSummaryText(t *testing.T) {
	first := time.Date(2026, 11, 12, 1, 2, 3, 0, time.UTC)
	s := Summary{Presented: 4, Refused: 1, Duplicate: 2, Channel: 3, First: first}
	got := s.Text("loss: none known", "")
	want := "delivery: 4 presented, 1 refused, 2 duplicate, 3 channel-acked (not proven) since 2026-11-12T01:02:03Z; loss: none known. " + Note
	if got != want {
		t.Fatalf("got  %q\nwant %q", got, want)
	}
	if got := s.Text("", "disk full"); !strings.Contains(got, "LEDGER WRITE FAILED: disk full (mail is still acked)") {
		t.Fatalf("no write failure in %q", got)
	}
}

func TestNilAndUnwritableLedger(t *testing.T) {
	var l *Ledger
	if err := l.Note("g", Presented, 1); err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	blocker := filepath.Join(dir, "file")
	os.WriteFile(blocker, nil, 0o600)
	if err := New(filepath.Join(blocker, File)).Note("g", Presented, 1); err == nil {
		t.Fatal("a write under a regular file succeeded")
	}
}

func TestLossText(t *testing.T) {
	gap := &Gen{}
	gap.P.Add(3, 5)
	gap.P.Add(8, 9)
	full := &Gen{}
	full.P.Add(1, 20)
	cases := []struct {
		name string
		in   []StreamState
		want string
	}{
		{"no stream state", nil, "loss: not checked (the inbox could not be read)"},
		{"covers everything", []StreamState{{Gen: full, FirstSeq: 21}}, "loss: none known"},
		{"nothing evicted", []StreamState{{Gen: full, FirstSeq: 1}}, "loss: none known"},
		{"a gap below FirstSeq", []StreamState{{Gen: gap, FirstSeq: 10}},
			"loss: at least 2 unproven (evicted from the inbox with no record that they were handed to the host); at least 2 more may be lost before the ledger started"},
		{"only before the ledger", []StreamState{{Gen: gap, FirstSeq: 4}}, "loss: none known; at least 2 more may be lost before the ledger started"},
		{"no record for the generation", []StreamState{{FirstSeq: 6}}, "loss: none known; at least 5 more may be lost before the ledger started"},
	}
	for _, c := range cases {
		if got := LossText(c.in); got != c.want {
			t.Errorf("%s:\n got %q\nwant %q", c.name, got, c.want)
		}
	}
}
