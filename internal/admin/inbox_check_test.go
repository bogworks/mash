package admin

import (
	"testing"
	"time"

	"github.com/bogworks/mash/internal/store"
)

// TestCheckInboxConsumerIgnoresServerSetRequestLimits: a hosted server (Synadia Cloud) fills in max_request_batch on
// the consumer it was asked to create; a rerun must still see its own inbox as intact.
func TestCheckInboxConsumerIgnoresServerSetRequestLimits(t *testing.T) {
	want := store.ConsumerConfig()
	got := want
	got.MaxRequestBatch, got.MaxRequestExpires, got.MaxRequestMaxBytes = 4096, time.Minute, 1<<20
	if err := checkInboxConsumer("a", got, want); err != nil {
		t.Fatalf("server-set request limits rejected: %v", err)
	}
}
