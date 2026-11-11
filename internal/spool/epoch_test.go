package spool

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func epochEntry(stream string, seq, epoch uint64) Entry {
	return Entry{Seq: seq, Stream: stream, From: "a", Trust: "verified", At: time.Now(), Body: []byte("b"), Epoch: epoch}
}

func TestEpochRoundTrip(t *testing.T) {
	s := newSpool(t)
	if _, ok := s.Epoch(); ok {
		t.Fatal("an epoch with none written")
	}
	if err := s.NoteEpoch(7); err != nil {
		t.Fatal(err)
	}
	if n, ok := s.Epoch(); !ok || n != 7 {
		t.Fatalf("got %d %v", n, ok)
	}
	if err := s.NoteEpoch(0); err != nil {
		t.Fatal(err)
	}
	if n, ok := s.Epoch(); !ok || n != 0 {
		t.Fatalf("none read as %d %v", n, ok)
	}
	if err := os.WriteFile(filepath.Join(s.Dir, epochFile), []byte("junk"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, ok := s.Epoch(); ok {
		t.Fatal("a damaged epoch file read as a record")
	}
}

// A legacy entry stamped with an epoch other than the current one is skipped by Waiting (so never shown or drained);
// own-stream entries and entries with no epoch (an older spool) are not affected; a missing file fails closed for the
// stamped ones only.
func TestWaitingSkipsLegacyEntryOfAnotherEpoch(t *testing.T) {
	s := newSpool(t)
	for _, e := range []Entry{epochEntry("", 1, 0), epochEntry("legacy", 1, 0), epochEntry("legacy", 2, 5), epochEntry("legacy", 3, 6)} {
		if err := s.Put(e); err != nil {
			t.Fatal(err)
		}
	}
	seqs := func() (out []string) {
		w, err := s.Waiting()
		if err != nil {
			t.Fatal(err)
		}
		for _, e := range w {
			out = append(out, e.Ref().name())
		}
		return out
	}
	want := func(what string, w ...string) {
		t.Helper()
		got := seqs()
		if len(got) != len(w) {
			t.Fatalf("%s: got %v want %v", what, got, w)
		}
		for i := range w {
			if got[i] != w[i] {
				t.Fatalf("%s: got %v want %v", what, got, w)
			}
		}
	}
	want("no epoch file", "1", "legacy@1")
	if err := s.NoteEpoch(5); err != nil {
		t.Fatal(err)
	}
	want("epoch 5", "1", "legacy@1", "legacy@2")
	if err := s.NoteEpoch(6); err != nil {
		t.Fatal(err)
	}
	want("epoch 6", "1", "legacy@1", "legacy@3")
	if err := s.NoteEpoch(0); err != nil {
		t.Fatal(err)
	}
	want("none", "1", "legacy@1")
}

// A failed write of the epoch removes the file, so the previous epoch cannot stay readable.
func TestNoteOrRemoveOnFailedWrite(t *testing.T) {
	s := newSpool(t)
	if err := s.NoteEpoch(7); err != nil {
		t.Fatal(err)
	}
	boom := errors.New("disk full")
	err := noteOrRemove(s.Dir, "none", func(string) error { return boom })
	if !errors.Is(err, boom) {
		t.Fatalf("err = %v", err)
	}
	if _, ok := s.Epoch(); ok {
		t.Fatal("the previous epoch is still readable after a failed write")
	}
	if err := noteOrRemove(s.Dir, "none", func(string) error { return boom }); !errors.Is(err, boom) {
		t.Fatalf("with no file: %v", err)
	}
}
