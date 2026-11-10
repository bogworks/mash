package spool

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

// Naming plan 1.4a: two streams of one endpoint number from 1 each, so the spool keys every file and mark by
// (stream, sequence).

func TestTwoStreamsWithTheSameSequenceAreDistinct(t *testing.T) {
	s := newSpool(t)
	own, old := Ref{Seq: 5}, Ref{Stream: "legacy", Seq: 5}
	for _, e := range []Entry{{Seq: 5, Body: []byte("own")}, {Stream: "legacy", Seq: 5, Body: []byte("old")}} {
		if err := s.Put(e); err != nil {
			t.Fatal(err)
		}
	}
	want := []string{"5.json", "drained/", "legacy@5.json", "listed/", "shown/"}
	if got := layout(t, s); !reflect.DeepEqual(got, want) {
		t.Fatalf("layout %v, want %v", got, want)
	}
	if !s.Has(own) || !s.Has(old) {
		t.Fatal("Has")
	}
	if err := s.MarkShown(old); err != nil {
		t.Fatal(err)
	}
	if err := s.Listed(own); err != nil {
		t.Fatal(err)
	}
	w, err := s.Waiting()
	if err != nil || len(w) != 2 {
		t.Fatalf("%v %v", w, err)
	}
	if w[0].Stream != "" || string(w[0].Body) != "own" || !w[0].Listed || w[0].Redelivered {
		t.Fatalf("own %+v", w[0])
	}
	if w[1].Stream != "legacy" || string(w[1].Body) != "old" || w[1].Listed || !w[1].Redelivered {
		t.Fatalf("legacy %+v", w[1])
	}
	if err := s.Drained(old); err != nil {
		t.Fatal(err)
	}
	if !s.IsDrained(old) || s.IsDrained(own) || s.Has(old) || !s.Has(own) || !s.IsListed(own) || s.IsListed(old) {
		t.Fatal("draining one stream's 5 touched the other's")
	}
	if err := s.Acked(old); err != nil {
		t.Fatal(err)
	}
	if got := layout(t, s); !reflect.DeepEqual(got, []string{"5.json", "drained/", "listed/", "listed/5", "shown/"}) {
		t.Fatalf("after Acked: %v", got)
	}
}

func TestBindPerStreamKeepsTheOtherStreamsMarks(t *testing.T) {
	s := newSpool(t)
	if err := s.Bind("own@1"); err != nil {
		t.Fatal(err)
	}
	if err := s.BindStream("legacy", "old@1"); err != nil {
		t.Fatal(err)
	}
	own, old := Ref{Seq: 3}, Ref{Stream: "legacy", Seq: 3}
	for _, r := range []Ref{own, old} {
		if err := s.Put(Entry{Stream: r.Stream, Seq: r.Seq, Body: []byte("x")}); err != nil {
			t.Fatal(err)
		}
		if err := s.MarkShown(r); err != nil {
			t.Fatal(err)
		}
		if err := s.Listed(Ref{Stream: r.Stream, Seq: r.Seq + 1}); err != nil {
			t.Fatal(err)
		}
		if err := s.Drained(Ref{Stream: r.Stream, Seq: r.Seq + 2}); err != nil {
			t.Fatal(err)
		}
	}
	// The same identities change nothing.
	if err := s.Bind("own@1"); err != nil {
		t.Fatal(err)
	}
	if err := s.BindStream("legacy", "old@1"); err != nil {
		t.Fatal(err)
	}
	// The legacy stream is recreated: only its files go.
	if err := s.BindStream("legacy", "old@2"); err != nil {
		t.Fatal(err)
	}
	got := layout(t, s)

	// listed/4, shown/3, drained/5 and 3.json of the own stream remain; the legacy@ ones are gone.
	want := []string{".stream", ".stream.legacy", "3.json", "drained/", "drained/5", "listed/", "listed/4", "shown/", "shown/3"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("after rebinding legacy: %v, want %v", got, want)
	}
	if b, _ := os.ReadFile(filepath.Join(s.Dir, ".stream.legacy")); string(b) != "old@2" {
		t.Fatalf(".stream.legacy = %q", b)
	}
	// And the own stream recreated drops its files but leaves legacy's (here, new).
	if err := s.Put(Entry{Stream: "legacy", Seq: 9, Body: []byte("y")}); err != nil {
		t.Fatal(err)
	}
	if err := s.Bind("own@2"); err != nil {
		t.Fatal(err)
	}
	want = []string{".stream", ".stream.legacy", "drained/", "legacy@9.json", "listed/", "shown/"}
	if got := layout(t, s); !reflect.DeepEqual(got, want) {
		t.Fatalf("after rebinding own: %v, want %v", got, want)
	}
}

func TestStreamNamesThatCannotBeFileNamesAreRefused(t *testing.T) {
	s := newSpool(t)
	for _, bad := range []string{"a/b", "a@b", ".x", "a\\b"} {
		if err := s.Put(Entry{Stream: bad, Seq: 1}); err == nil {
			t.Errorf("Put accepted stream %q", bad)
		}
		if err := s.BindStream(bad, "x"); err == nil {
			t.Errorf("BindStream accepted stream %q", bad)
		}
	}
}

func TestRefOrderAndNames(t *testing.T) {
	rs := []Ref{{"b", 1}, {"", 9}, {"a", 2}, {"", 3}}
	var names []string
	for _, r := range rs {
		names = append(names, r.name())
	}
	if !reflect.DeepEqual(names, []string{"b@1", "9", "a@2", "3"}) {
		t.Fatalf("%v", names)
	}
	for _, r := range rs {
		if got, ok := parseName(r.name()); !ok || got != r {
			t.Fatalf("parse %q: %v %v", r.name(), got, ok)
		}
	}
	if !(Ref{"", 9}).Less(Ref{"a", 1}) || (Ref{"a", 2}).Less(Ref{"a", 1}) {
		t.Fatal("order")
	}
	for _, bad := range []string{"", "x", "@3", "a@", "a@x", "-1"} {
		if _, ok := parseName(bad); ok {
			t.Errorf("parseName(%q) ok", bad)
		}
	}
}

// A file named only by its sequence, as every spool written before streams was, reads as the primary stream.
func TestOldFilesReadAsThePrimaryStream(t *testing.T) {
	s := newSpool(t)
	b := `{"seq":7,"from":"b","at":"0001-01-01T00:00:00Z","body":"aGk="}`
	if err := os.WriteFile(filepath.Join(s.Dir, "7.json"), []byte(b), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(s.drained(), "8"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	w, err := s.Waiting()
	if err != nil || len(w) != 1 || w[0].Ref() != (Ref{Seq: 7}) || !s.IsDrained(Ref{Seq: 8}) {
		t.Fatalf("%+v %v", w, err)
	}
}
