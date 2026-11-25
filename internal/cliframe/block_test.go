package cliframe

import "testing"

func TestBlockPadsToLongestLabelPlusOne(t *testing.T) {
	got := Block(Row{"to", "a"}, Row{"urgent", "no"})
	if want := "to:     a\nurgent: no"; got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
	if got := Block(Row{"Join file", "x"}); got != "Join file: x" {
		t.Fatalf("one row: %q", got)
	}
	if got := Block(); got != "" {
		t.Fatalf("empty: %q", got)
	}
}
