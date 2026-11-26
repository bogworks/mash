package endpoint

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/bogworks/mash/internal/config"
	"github.com/bogworks/mash/internal/sapsdk"
)

// A malformed entry, a not-verified entry and an entry on both lists: the third is one row, counted once, as malformed.
func TestRefusedClassesBothListsCountOnceAsMalformed(t *testing.T) {
	mal := []seqEntry{{Seq: 2, Class: sapsdk.ClassTimeNotUTC}, {Seq: 5, Class: sapsdk.ClassSpaceInSubject}}
	unv := []uint64{3, 5}
	out := map[uint64]string{}
	addRefused(out, mal, unv, 1, 10)
	rc := countRefused(out)
	if rc.Total != 3 || rc.ByClass[sapsdk.ClassTimeNotUTC] != 1 || rc.ByClass[sapsdk.ClassSpaceInSubject] != 1 || rc.ByClass[ClassNotVerified] != 1 {
		t.Fatalf("counts: %+v", rc)
	}
	if out[5] != sapsdk.ClassSpaceInSubject {
		t.Fatalf("seq 5 class %q, want malformed", out[5])
	}
}

// An entry the stream aged out is not counted; an entry with no class is an unreadable header.
func TestRefusedClassesAgingOutLowersCounts(t *testing.T) {
	mal := []seqEntry{{Seq: 2, Class: sapsdk.ClassTimeNotUTC}, {Seq: 8}}
	unv := []uint64{3, 9}
	count := func(first, last uint64) RefusedCounts {
		out := map[uint64]string{}
		addRefused(out, mal, unv, first, last)
		return countRefused(out)
	}
	if rc := count(1, 10); rc.Total != 4 || rc.ByClass[sapsdk.ClassUnreadable] != 1 {
		t.Fatalf("all: %+v", rc)
	}
	if rc := count(4, 10); rc.Total != 2 || rc.ByClass[ClassNotVerified] != 1 {
		t.Fatalf("aged: %+v", rc)
	}
	if rc := count(10, 10); rc.Total != 0 || len(rc.ByClass) != 0 {
		t.Fatalf("all gone: %+v", rc)
	}
}

// With no stream to ask, the lists on disk are counted; a list of another stream is not.
func TestRefusedCountsOnDisk(t *testing.T) {
	dir := t.TempDir()
	cfg := config.Config{Owner: "acme", Group: "demo", Name: "bob", StateDir: dir}
	sp, err := SpoolDir(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(sp, 0o700); err != nil {
		t.Fatal(err)
	}
	write := func(name, body string) {
		if err := os.WriteFile(filepath.Join(sp, name), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write(malformedSeqsFile, "stream s1\n2 "+sapsdk.ClassTimeNotUTC+"\n5 "+sapsdk.ClassSpaceInSubject+"\n")
	write(refusedSeqsFile, "stream s1\n3\n5\n")
	rc := RefusedCountsOnDisk(cfg)
	if rc.Total != 3 || rc.ByClass[ClassNotVerified] != 1 {
		t.Fatalf("counts: %+v", rc)
	}
	write(refusedSeqsFile, "stream other\n3\n5\n")
	if rc := RefusedCountsOnDisk(cfg); rc.Total != 2 {
		t.Fatalf("a list of another stream counted: %+v", rc)
	}
}
