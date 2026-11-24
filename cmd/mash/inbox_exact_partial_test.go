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
