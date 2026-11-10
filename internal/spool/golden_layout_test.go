package spool

import (
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"testing"
)

// Characterisation tests for naming plan 1.4a: for a single stream the on-disk spool layout (file names, marks, the
// Bind marker) and Waiting's result are what they were before the spool was keyed by (stream, sequence). These tests
// were written and passed before the refactor; only the helpers below changed with the method signatures.

func gShown(t *testing.T, s Spool, seqs ...uint64) {
	t.Helper()
	refs := make([]Ref, len(seqs))
	for i, q := range seqs {
		refs[i] = Ref{Seq: q}
	}
	if err := s.MarkShown(refs...); err != nil {
		t.Fatal(err)
	}
}
func gListed(t *testing.T, s Spool, seq uint64) {
	t.Helper()
	if err := s.Listed(Ref{Seq: seq}); err != nil {
		t.Fatal(err)
	}
}
func gDrained(t *testing.T, s Spool, seq uint64) {
	t.Helper()
	if err := s.Drained(Ref{Seq: seq}); err != nil {
		t.Fatal(err)
	}
}
func gAcked(t *testing.T, s Spool, seq uint64) {
	t.Helper()
	if err := s.Acked(Ref{Seq: seq}); err != nil {
		t.Fatal(err)
	}
}
func gIsDrained(s Spool, seq uint64) bool { return s.IsDrained(Ref{Seq: seq}) }
func gIsListed(s Spool, seq uint64) bool  { return s.IsListed(Ref{Seq: seq}) }
func gHas(s Spool, seq uint64) bool       { return s.Has(Ref{Seq: seq}) }

// layout lists every file under the spool dir, relative, sorted: the on-disk shape.
func layout(t *testing.T, s Spool) []string {
	t.Helper()
	var out []string
	err := filepath.WalkDir(s.Dir, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(s.Dir, p)
		if rel == "." {
			return nil
		}
		if d.IsDir() {
			rel += "/"
		}
		out = append(out, rel)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	sort.Strings(out)
	return out
}

func TestGoldenSingleStreamLayout(t *testing.T) {
	s := newSpool(t)
	for _, seq := range []uint64{3, 4, 5, 6} {
		if err := s.Put(Entry{Seq: seq, From: "b", Body: []byte("x")}); err != nil {
			t.Fatal(err)
		}
	}
	want := []string{"3.json", "4.json", "5.json", "6.json", "drained/", "listed/", "shown/"}
	if got := layout(t, s); !reflect.DeepEqual(got, want) {
		t.Fatalf("after Put: %v, want %v", got, want)
	}
	gShown(t, s, 3, 4, 5)
	gListed(t, s, 5)
	want = []string{"3.json", "4.json", "5.json", "6.json", "drained/", "listed/", "listed/5", "shown/", "shown/3", "shown/4"}
	if got := layout(t, s); !reflect.DeepEqual(got, want) {
		t.Fatalf("after MarkShown and Listed: %v, want %v", got, want)
	}
	gDrained(t, s, 3)
	want = []string{"4.json", "5.json", "6.json", "drained/", "drained/3", "listed/", "listed/5", "shown/", "shown/4"}
	if got := layout(t, s); !reflect.DeepEqual(got, want) {
		t.Fatalf("after Drained: %v, want %v", got, want)
	}
	if !gIsDrained(s, 3) || gIsDrained(s, 4) || !gIsListed(s, 5) || gIsListed(s, 4) || gHas(s, 3) || !gHas(s, 4) {
		t.Fatal("marks do not answer as before")
	}
	gAcked(t, s, 3)
	gDrained(t, s, 5)
	want = []string{"4.json", "6.json", "drained/", "drained/5", "listed/", "shown/", "shown/4"}
	if got := layout(t, s); !reflect.DeepEqual(got, want) {
		t.Fatalf("after Acked and Drained: %v, want %v", got, want)
	}
	if err := s.Bind("name@t1"); err != nil {
		t.Fatal(err)
	}
	if got := layout(t, s); !reflect.DeepEqual(got, []string{".stream", "drained/", "listed/", "shown/"}) {
		t.Fatalf("a first Bind drops everything and writes the marker: %v", got)
	}
	if b, _ := os.ReadFile(filepath.Join(s.Dir, ".stream")); string(b) != "name@t1" {
		t.Fatalf(".stream = %q", b)
	}
}

func TestGoldenSingleStreamBindKeepsOnSameStreamAndDropsOnNew(t *testing.T) {
	s := newSpool(t)
	if err := s.Bind("n@1"); err != nil {
		t.Fatal(err)
	}
	if err := s.Put(Entry{Seq: 2, Body: []byte("x")}); err != nil {
		t.Fatal(err)
	}
	gDrained(t, s, 1)
	if err := s.Bind("n@1"); err != nil {
		t.Fatal(err)
	}
	if !gHas(s, 2) || !gIsDrained(s, 1) {
		t.Fatal("Bind of the same stream changed something")
	}
	if err := s.Bind("n@2"); err != nil {
		t.Fatal(err)
	}
	if gHas(s, 2) || gIsDrained(s, 1) {
		t.Fatal("Bind of a new stream kept the old stream's file or tombstone")
	}
}

func TestGoldenSingleStreamWaiting(t *testing.T) {
	s := newSpool(t)
	for _, seq := range []uint64{10, 2, 7} {
		if err := s.Put(Entry{Seq: seq, From: "b", Trust: "verified", Body: []byte("hi")}); err != nil {
			t.Fatal(err)
		}
	}
	gShown(t, s, 7)
	gListed(t, s, 2)
	es, err := s.Waiting()
	if err != nil {
		t.Fatal(err)
	}
	var seqs []uint64
	for _, e := range es {
		seqs = append(seqs, e.Seq)
	}
	if !reflect.DeepEqual(seqs, []uint64{2, 7, 10}) {
		t.Fatalf("order %v", seqs)
	}
	if !es[0].Listed || es[0].Redelivered || es[1].Listed || !es[1].Redelivered || es[2].Listed || es[2].Redelivered {
		t.Fatalf("flags %+v", es)
	}
	b, _ := os.ReadFile(filepath.Join(s.Dir, "7.json"))
	if string(b) != `{"seq":7,"from":"b","trust":"verified","at":"0001-01-01T00:00:00Z","body":"aGk="}` {
		t.Fatalf("entry bytes %s", b)
	}
	// ClearWaiting keeps tombstones and shown marks, drops files and listed marks.
	gDrained(t, s, 2)
	if err := s.ClearWaiting(); err != nil {
		t.Fatal(err)
	}
	want := []string{"drained/", "drained/2", "listed/", "shown/", "shown/7"}
	if got := layout(t, s); !reflect.DeepEqual(got, want) {
		t.Fatalf("after ClearWaiting: %v, want %v", got, want)
	}
}
