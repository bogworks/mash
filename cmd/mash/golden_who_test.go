// Characterisation tests (plan 2b, Phase 0): these pin today's identifiers and validity for legacy identities; a change here is a change to a legacy identifier and needs its own decision.

package main

import (
	"testing"
	"time"

	"github.com/bogworks/mash/internal/endpoint"
	"github.com/bogworks/mash/internal/store"
)

// The who --json contract (ADR 0049): every field, in order, for a fully populated zoned record with a live lease,
// and the smallest legacy record.
func TestGoldenWhoJSONShape(t *testing.T) {
	const n48 = "abcdefghijklmnopqrstuvwxyz0123456789abcdefghijkl"
	full := endpoint.Presence{
		Record: store.Record{
			Name: n48, Owner: n48, Zone: "z0123456789abcdefghijklmnopqrstu", Role: "r", Host: "h", Note: "n",
			Description: "d", Caps: []string{"c1", "c2"}, Labels: map[string]string{"b": "2", "a": "1"},
		},
		Online:   true,
		LastSeen: time.Date(2026, 11, 1, 12, 0, 0, 0, time.UTC),
	}
	legacy := endpoint.Presence{Record: store.Record{Name: "a", Owner: "o"}}
	live := map[string]endpoint.LeaseInfo{n48: {Host: "lh", Version: "1.0"}}
	const want = `[
  {
    "name": "abcdefghijklmnopqrstuvwxyz0123456789abcdefghijkl",
    "owner": "abcdefghijklmnopqrstuvwxyz0123456789abcdefghijkl",
    "online": true,
    "last_seen": "2026-11-01T12:00:00Z",
    "role": "r",
    "host": "h",
    "note": "n",
    "description": "d",
    "caps": [
      "c1",
      "c2"
    ],
    "labels": {
      "a": "1",
      "b": "2"
    },
    "zone": "z0123456789abcdefghijklmnopqrstu",
    "lease": {
      "host": "lh",
      "version": "1.0"
    }
  },
  {
    "name": "a",
    "owner": "o",
    "online": false
  }
]`
	if got := whoJSON([]endpoint.Presence{full, legacy}, true, live); got != want {
		t.Errorf("who --json --long:\n%s", got)
	}
	if got := whoJSON(nil, false, nil); got != "[]" {
		t.Errorf("empty who --json = %q", got)
	}
}
