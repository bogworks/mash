package admin

import (
	"strings"
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

// An inbox with more replicas than wanted was raised on purpose and is accepted; one with fewer is refused, and the
// refusal says to raise it, not to deprovision. Nothing else is relaxed.
func TestCheckInboxStreamReplicas(t *testing.T) {
	r1, r3 := store.DefaultLimits(), store.DefaultLimits()
	r3.Replicas = 3
	for _, c := range []struct {
		name      string
		got, want int
		ok        bool
	}{{"equal R1", 1, 1, true}, {"equal R3", 3, 3, true}, {"larger accepted", 3, 1, true}, {"unset reads as 1", 0, 1, true}, {"smaller refused", 1, 3, false}} {
		want := store.InboxConfigFor("o", "a", r1)
		want.Replicas = c.want
		got := store.InboxConfigFor("o", "a", r3)
		got.Replicas = c.got
		err := checkInboxStream("a", got, want)
		if c.ok && err != nil {
			t.Errorf("%s: %v", c.name, err)
		}
		if !c.ok {
			if err == nil || !strings.Contains(err.Error(), "raise the replica count of mash-owned state to 3") || strings.Contains(err.Error(), "deprovision") {
				t.Errorf("%s: %v", c.name, err)
			}
		}
	}
	// Another field still differs: refused as before, even with a larger replica count.
	want := store.InboxConfigFor("o", "a", r1)
	got := store.InboxConfigFor("o", "a", r3)
	got.MaxBytes++
	if err := checkInboxStream("a", got, want); err == nil || !strings.Contains(err.Error(), "max_bytes") {
		t.Errorf("a max_bytes drift: %v", err)
	}
}
