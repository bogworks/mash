package release

import "testing"

// Throwaway (ops3, ADR 0127 red-run proof): fails on purpose. Never merges.
func TestZZRedProbe(t *testing.T) {
	t.Fatal("red-run proof: this failure is on purpose")
}
