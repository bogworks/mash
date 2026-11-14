package policyserve

import (
	"maps"
	"testing"
)

// The kept signers are the held owner group keys of other ranges; a group whose name merely starts with the range's
// name, without the dot, is another range's.
func TestKeptSignersAreOtherRangesGroups(t *testing.T) {
	keys := groupKeys{"foo.c": "AFOOC", "foo.d": "AFOOD", "bar.x": "ABARX", "foobar.y": "AFOOBARY"}
	got, err := KeptSigners(keys, "foo")
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{"bar.x": "ABARX", "foobar.y": "AFOOBARY"}
	if !maps.Equal(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
	if none, _ := KeptSigners(groupKeys{"foo.c": "A"}, "foo"); len(none) != 0 || none == nil {
		t.Errorf("a single-range store keeps %v, want an empty non-nil set", none)
	}
}
