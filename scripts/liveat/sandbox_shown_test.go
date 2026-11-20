package main

import (
	"strings"
	"testing"
)

// The container's output is scanned before it is shown: a seed in it is removed and reported, clean output is kept.
func TestShownOutputRemovesASeed(t *testing.T) {
	seed := "SU" + strings.Repeat("A", 54) // the shape only, not a key
	out, errOut, leaked := shownOutput(Result{Stdout: "ok " + seed + "\n", Stderr: "fine\n"})
	if !leaked || strings.Contains(out, seed) || !strings.Contains(out, "[seed removed]") || errOut != "fine\n" {
		t.Fatalf("got %q %q %v", out, errOut, leaked)
	}
	out, _, leaked = shownOutput(Result{Stdout: "PASS everything\n"})
	if leaked || out != "PASS everything\n" {
		t.Fatalf("clean output changed: %q %v", out, leaked)
	}
}
