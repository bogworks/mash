package spool

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/bogworks/mash/internal/sap"
)

// Compatibility gate for the trust labels the spool writes to disk. A spool written by one release is read by the next
// (a session's hook drains what the serve process spooled before an upgrade), so the label strings and the bytes of an
// entry file must not move unnoticed. Regenerate the goldens deliberately with UPDATE_GOLDEN=1; the default run only reads.

// compatLabels is every sap.Trust constant, in the order of testdata/trust_labels.golden.
var compatLabels = []sap.Trust{
	sap.TrustVerified, sap.TrustClaimed, sap.TrustAbsent, sap.TrustForged,
	sap.TrustMalformed, sap.TrustUnknownSigner, sap.TrustStale,
}

func updateGolden() bool { return os.Getenv("UPDATE_GOLDEN") == "1" }

// compatGolden returns the committed golden at path; under UPDATE_GOLDEN=1 it writes got there first.
func compatGolden(t *testing.T, path string, got []byte) []byte {
	t.Helper()
	if updateGolden() {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, got, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("%v (run with UPDATE_GOLDEN=1 to create it)", err)
	}
	return want
}

func TestCompatTrustLabelStrings(t *testing.T) {
	var b strings.Builder
	for _, l := range compatLabels {
		b.WriteString(string(l) + "\n")
	}
	want := compatGolden(t, filepath.Join("testdata", "trust_labels.golden"), []byte(b.String()))
	if b.String() != string(want) {
		t.Fatalf("trust labels changed\n got:\n%s\nwant:\n%s", b.String(), want)
	}
}

// compatEntry is the fixed entry written for one label. Only Seq, Trust and Body vary.
func compatEntry(i int, l sap.Trust) Entry {
	return Entry{
		Seq:      uint64(i + 1),
		From:     "ag.team.peer",
		Trust:    string(l),
		Instance: "inst1",
		Owner:    "team",
		Urgent:   true,
		Kind:     "task",
		ReplyTo:  3,
		Via:      "old #5",
		At:       time.Date(2026, 11, 15, 12, 0, 0, 0, time.UTC),
		Body:     []byte("hello " + string(l)),
	}
}

func compatFile(l sap.Trust) string { return filepath.Join("testdata", "compat", string(l)+".json") }

// The bytes the real write path (Spool.Put) puts on disk are fixed. Nothing needed normalising: the file name is
// <seq>.json and the content holds no pid or wall-clock time beyond the entry's own At.
func TestCompatSpoolEntryBytes(t *testing.T) {
	s := newSpool(t)
	for i, l := range compatLabels {
		e := compatEntry(i, l)
		if err := s.Put(e); err != nil {
			t.Fatal(err)
		}
		got, err := os.ReadFile(s.file(e.Ref()))
		if err != nil {
			t.Fatal(err)
		}
		want := compatGolden(t, compatFile(l), got)
		if !bytes.Equal(got, want) {
			t.Errorf("%s: entry bytes changed\n got: %s\nwant: %s", l, got, want)
		}
	}
}

// A spool written by an earlier release (the goldens) reads back the same: label and every other field.
func TestCompatSpoolReadsGoldens(t *testing.T) {
	s := newSpool(t)
	for i, l := range compatLabels {
		b, err := os.ReadFile(compatFile(l))
		if err != nil {
			t.Fatalf("%v (run TestCompatSpoolEntryBytes with UPDATE_GOLDEN=1)", err)
		}
		if err := os.WriteFile(s.file(Ref{Seq: uint64(i + 1)}), b, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	got, err := s.Waiting()
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != len(compatLabels) {
		t.Fatalf("read %d entries, want %d", len(got), len(compatLabels))
	}
	for i, l := range compatLabels {
		want := compatEntry(i, l)
		e := got[i]
		if e.Trust != string(l) {
			t.Errorf("seq %d: trust %q, want %q", e.Seq, e.Trust, l)
		}
		if e.Seq != want.Seq || e.From != want.From || e.Instance != want.Instance || e.Owner != want.Owner ||
			e.Urgent != want.Urgent || e.Kind != want.Kind || e.ReplyTo != want.ReplyTo || e.Via != want.Via ||
			!e.At.Equal(want.At) || !bytes.Equal(e.Body, want.Body) {
			t.Errorf("seq %d: read %+v, want %+v", e.Seq, e, want)
		}
	}
}
