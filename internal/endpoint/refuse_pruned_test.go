package endpoint

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"testing"

	"github.com/bogworks/mash/internal/config"
)

// prunedEndpoint is an endpoint with a state directory, the stream identity "inbox@now", refusedSeqsKeep 2 (a prune at
// 4 recorded sequences) and the stream's first sequence read from *first.
func prunedEndpoint(t *testing.T, first *uint64) (*Endpoint, config.Config, string, string) {
	t.Helper()
	old := refusedSeqsKeep
	refusedSeqsKeep = 2
	t.Cleanup(func() { refusedSeqsKeep = old })
	cfg := config.Config{Owner: "acme", Group: "demo", Name: "bob", StateDir: t.TempDir()}
	e := &Endpoint{cfg: cfg}
	id := "inbox@now"
	e.refusedStream.Store(&id)
	e.firstSeqFn = func(context.Context) (uint64, error) { return *first, nil }
	dir, err := SpoolDir(cfg)
	if err != nil {
		t.Fatal(err)
	}
	return e, cfg, id, filepath.Join(dir, refusedSeqsFile)
}

func record(t *testing.T, e *Endpoint, seqs ...uint64) {
	t.Helper()
	for _, s := range seqs {
		if _, added, err := e.recordRefusalNew(s); err != nil || !added {
			t.Fatalf("record %d: added=%v err=%v", s, added, err)
		}
	}
}

func readText(t *testing.T, p string) string {
	t.Helper()
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// A prune writes the pruned total as a marker line, and the count is the marker plus the recorded sequences.
func TestRefusedPruneWritesMarker(t *testing.T) {
	first := uint64(1)
	e, cfg, id, p := prunedEndpoint(t, &first)
	record(t, e, 1, 2, 3, 4)
	if got := readText(t, p); got != "stream "+id+"\n1\n2\n3\n4\n" {
		t.Fatalf("before the prune: %q", got)
	}
	first = 3 // the stream dropped 1 and 2
	record(t, e, 5)
	if got, want := readText(t, p), "stream "+id+"\npruned 2\n3\n4\n5\n"; got != want {
		t.Fatalf("after the prune: %q, want %q", got, want)
	}
	if n := RefusedCount(cfg); n != 5 {
		t.Fatalf("count %d, want 5 (2 pruned + 3 recorded)", n)
	}
	// A second prune adds to the marker: 6 appends, 7 prunes 3 and 4.
	first = 5
	record(t, e, 6, 7)
	if got, want := readText(t, p), "stream "+id+"\npruned 4\n5\n6\n7\n"; got != want {
		t.Fatalf("after the second prune: %q, want %q", got, want)
	}
	if n := RefusedCount(cfg); n != 7 {
		t.Fatalf("count %d, want 7", n)
	}
}

// A kill between the prune's rewrite (marker and new sequence already in the file) and the count write leaves the
// count one low; the redelivery and reconcileRefusedCount both repair it exactly.
func TestRefusedCountRepairedAfterKillInPrune(t *testing.T) {
	first := uint64(1)
	e, cfg, id, p := prunedEndpoint(t, &first)
	record(t, e, 1, 2, 3, 4)
	// The killed prune: file rewritten with the marker and seq 5, count still 4.
	if err := writeRefusedSeqs(p, id, 2, []uint64{3, 4, 5}); err != nil {
		t.Fatal(err)
	}
	lower := func() {
		t.Helper()
		dir, _ := SpoolDir(cfg)
		if err := os.WriteFile(filepath.Join(dir, RefusedFile), []byte("4\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		if n := RefusedCount(cfg); n != 4 {
			t.Fatalf("setup: count %d", n)
		}
	}
	lower()
	if n, added, err := e.recordRefusalNew(5); err != nil || added || n != 5 {
		t.Fatalf("redelivery: n=%d added=%v err=%v", n, added, err)
	}
	if n := RefusedCount(cfg); n != 5 {
		t.Fatalf("count after redelivery = %d, want 5", n)
	}
	lower()
	if err := e.reconcileRefusedCount(id); err != nil {
		t.Fatal(err)
	}
	if n := RefusedCount(cfg); n != 5 {
		t.Fatalf("count after reconcile = %d, want 5", n)
	}
	// And the next new refusal counts on from the repaired value.
	if n, added, _ := e.recordRefusalNew(6); !added || n != 6 {
		t.Fatalf("next refusal: n=%d added=%v", n, added)
	}
}

// The reader returns the marker too; readRefusedSeqs ignores it; a stream change resets it.
func TestRefusedReaderAndMarker(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "f.seqs")
	if err := os.WriteFile(p, []byte("stream s1\npruned 7\n3\n4\npruned 9\n4\n5\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	id, seqs, pruned := readRefusedFile(p)
	if id != "s1" || !slices.Equal(seqs, []uint64{3, 4, 5}) || pruned != 9 {
		t.Fatalf("readRefusedFile: %q %v %d (last marker wins)", id, seqs, pruned)
	}
	id, seqs = readRefusedSeqs(p)
	if id != "s1" || !slices.Equal(seqs, []uint64{3, 4, 5}) {
		t.Fatalf("readRefusedSeqs: %q %v", id, seqs)
	}
	// What a reader that does not know the marker does with its line: ParseUint fails and the line is skipped.
	if _, err := strconv.ParseUint("pruned 7", 10, 64); err == nil {
		t.Fatal("the marker parses as a sequence")
	}
	if err := os.WriteFile(p, []byte("stream s1\npruned x\n1\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, seqs, pruned := readRefusedFile(p); pruned != 0 || !slices.Equal(seqs, []uint64{1}) {
		t.Fatalf("malformed marker: %v %d", seqs, pruned)
	}
}

func TestRefusedStreamChangeResetsPruned(t *testing.T) {
	first := uint64(1)
	e, cfg, _, p := prunedEndpoint(t, &first)
	record(t, e, 1, 2, 3, 4)
	first = 3
	record(t, e, 5)
	if _, _, pruned := readRefusedFile(p); pruned != 2 {
		t.Fatalf("setup: pruned %d", pruned)
	}
	other := "inbox@later"
	e.refusedStream.Store(&other)
	if _, err := e.bindRefusalsTo(other); err != nil {
		t.Fatal(err)
	}
	if got := readText(t, p); got != "stream "+other+"\n" {
		t.Fatalf("after the stream change: %q", got)
	}
	if n := RefusedCount(cfg); n != 0 {
		t.Fatalf("count %d after the stream change", n)
	}
}

// A file without the marker (old format) behaves as before: pruned is 0 and the count is repaired to the number of
// recorded sequences.
func TestRefusedOldFormatFile(t *testing.T) {
	first := uint64(1)
	e, cfg, id, p := prunedEndpoint(t, &first)
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte("stream "+id+"\n1\n2\n3\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, seqs, pruned := readRefusedFile(p); pruned != 0 || len(seqs) != 3 {
		t.Fatalf("old file: %v %d", seqs, pruned)
	}
	if n, added, err := e.recordRefusalNew(3); err != nil || added || n != 3 {
		t.Fatalf("redelivery: n=%d added=%v err=%v", n, added, err)
	}
	if n := RefusedCount(cfg); n != 3 {
		t.Fatalf("count %d, want 3", n)
	}
	first = 3
	record(t, e, 4) // 3 recorded < 4: appended, no marker
	if got := readText(t, p); got != "stream "+id+"\n1\n2\n3\n4\n" {
		t.Fatalf("append: %q", got)
	}
	record(t, e, 5) // 4 recorded: pruned (1 and 2 dropped), marker appears
	if got := readText(t, p); got != "stream "+id+"\npruned 2\n3\n4\n5\n" {
		t.Fatalf("prune of an old file: %q", got)
	}
	if n := RefusedCount(cfg); n != 5 {
		t.Fatalf("count %d, want 5", n)
	}
}
