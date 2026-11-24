package main

import (
	"testing"

	"github.com/bogworks/mash/internal/endpoint"
)

// A count pass that failed part way (Partial with no cap named) is not exact either (#958).
func TestCappedWhyCountsAPassThatStoppedPartWay(t *testing.T) {
	if got := cappedWhy(endpoint.InboxRead{Partial: true}); got != "the count stopped part way" {
		t.Fatalf("cappedWhy(Partial, no cap) = %q", got)
	}
	if got := cappedWhy(endpoint.InboxRead{}); got != "" {
		t.Fatalf("cappedWhy(exact) = %q, want empty", got)
	}
}

// Every reason, in its longest form, fits "exact: no (" + reason + ")" in 80 columns (#1415).
func TestCappedWhyFitsEightyColumns(t *testing.T) {
	for _, r := range []endpoint.InboxRead{
		{PartialBy: "messages", PartialAt: 1}, {PartialBy: "messages", PartialAt: 9999},
		{PartialBy: "bytes", PartialAt: 1023 << 20}, {PartialBy: "bytes", PartialAt: 1<<32 - 1}, {Partial: true},
	} {
		why := cappedWhy(r)
		if line := "exact: no (" + why + ")"; why == "" || len([]rune(line)) > 80 {
			t.Errorf("%+v: %q is %d columns", r, line, len([]rune(line)))
		}
	}
	if got := cappedWhy(endpoint.InboxRead{PartialBy: "bytes", PartialAt: 32 << 20}); got != "only the oldest 32 MiB of message text was checked" {
		t.Errorf("bytes cap: %q", got)
	}
}
