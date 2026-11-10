package spool

import (
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"testing"
)

// treeOf lists every path under root with its mode and size, so a stray write, removal or rename shows up.
func treeOf(t *testing.T, root string) []string {
	t.Helper()
	var out []string
	err := filepath.WalkDir(root, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		fi, err := d.Info()
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(root, p)
		out = append(out, rel+"|"+fi.Mode().String()+"|"+strconv.FormatInt(fi.Size(), 10))
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

var unsafeStreams = []string{"../x", "a/b", `a\b`, "..", ".", "@", "a\x00b", "a@b", "x@", ".hidden", "../../x"}

// Every Ref-taking method refuses a Ref whose Stream could name a path outside the mark directories, and touches
// nothing: the directory tree (with decoy files a traversal would hit or remove) is unchanged.
func TestRefMethodsRefuseUnsafeStreams(t *testing.T) {
	s := newSpool(t)
	root := filepath.Dir(filepath.Dir(filepath.Dir(filepath.Dir(s.Dir))))
	for _, p := range []string{
		filepath.Join(s.Dir, "x"), filepath.Join(s.Dir, "x.json"), filepath.Join(s.Dir, "x@1"),
		filepath.Join(s.Dir, "shown", "x"), filepath.Join(filepath.Dir(s.Dir), "x"),
		filepath.Join(filepath.Dir(s.Dir), "x.json"), filepath.Join(s.Dir, "1"),
	} {
		if err := os.WriteFile(p, []byte("decoy"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	before := treeOf(t, root)
	for _, st := range unsafeStreams {
		for _, seq := range []uint64{0, 1} {
			r := Ref{Stream: st, Seq: seq}
			if s.MarkShown(r) == nil {
				t.Errorf("MarkShown(%q) accepted", st)
			}
			if s.MarkShown(Ref{Seq: 1}, r) == nil {
				t.Errorf("MarkShown(valid, %q) accepted", st)
			}
			if s.Listed(r) == nil {
				t.Errorf("Listed(%q) accepted", st)
			}
			if s.Drained(r) == nil {
				t.Errorf("Drained(%q) accepted", st)
			}
			if s.Acked(r) == nil {
				t.Errorf("Acked(%q) accepted", st)
			}
			if s.IsListed(r) || s.IsDrained(r) || s.Has(r) {
				t.Errorf("a query accepted %q", st)
			}
		}
		if after := treeOf(t, root); !reflect.DeepEqual(before, after) {
			t.Fatalf("stream %q changed the tree:\n before %v\n after  %v", st, before, after)
		}
	}
}

func TestRefMethodsStillWorkForValidStreams(t *testing.T) {
	s := newSpool(t)
	for _, st := range []string{"", "legacy", "a.b", "x-y_z"} {
		r := Ref{Stream: st, Seq: 3}
		if err := s.Put(Entry{Stream: st, Seq: 3}); err != nil {
			t.Fatal(err)
		}
		if !s.Has(r) {
			t.Fatalf("Has(%q)", st)
		}
		if err := s.MarkShown(r); err != nil {
			t.Fatal(err)
		}
		if err := s.Listed(r); err != nil || !s.IsListed(r) {
			t.Fatalf("Listed(%q) %v", st, err)
		}
		if err := s.Drained(r); err != nil || !s.IsDrained(r) || s.Has(r) {
			t.Fatalf("Drained(%q) %v", st, err)
		}
		if err := s.Acked(r); err != nil || s.IsDrained(r) {
			t.Fatalf("Acked(%q) %v", st, err)
		}
	}
}
