package spool

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/bogworks/mash/internal/ledger"
)

// The presented ledger lives in the spool directory (ADR 0109). No spool scan may take it for a message, and no clearing
// of waiting files or rebinding of a stream may remove it.
func TestLedgerFileIsNotASpoolEntry(t *testing.T) {
	s := newSpool(t)
	if err := s.Bind("g1"); err != nil {
		t.Fatal(err)
	}
	for _, seq := range []uint64{1, 2} {
		if err := s.Put(Entry{Seq: seq, From: "b", Body: []byte("x")}); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.Drained(Ref{Seq: 1}); err != nil {
		t.Fatal(err)
	}
	if err := s.Listed(Ref{Seq: 2}); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(s.Dir, ledger.File)
	if err := ledger.New(path).Note("g1", ledger.Presented, 1); err != nil {
		t.Fatal(err)
	}
	w, err := s.Waiting()
	if err != nil || len(w) != 1 || w[0].Seq != 2 || !w[0].Listed {
		t.Fatalf("Waiting = %+v, %v", w, err)
	}
	if !s.IsDrained(Ref{Seq: 1}) || !s.IsListed(Ref{Seq: 2}) {
		t.Fatal("drained or listed marks changed")
	}
	if err := s.ClearWaiting(); err != nil {
		t.Fatal(err)
	}
	if err := s.Bind("g2"); err != nil { // a re-provisioned stream clears its files, not the ledger
		t.Fatal(err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("the ledger did not survive: %v", err)
	}
	if w, _ := s.Waiting(); len(w) != 0 {
		t.Fatalf("Waiting after clear = %+v", w)
	}
}
