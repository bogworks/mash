// Characterisation tests: these pin the who --json schema; a change here is a change to a published contract and needs its own decision.

package main

import (
	"testing"
	"time"

	"github.com/bogworks/mash/internal/endpoint"
	"github.com/bogworks/mash/internal/store"
)

// The who --json contract (ADR 0049): every field, in order, for a fully populated record with a live lease,
// and the smallest record.
func TestGoldenWhoJSONShape(t *testing.T) {
	full := endpoint.Presence{
		Record: store.Record{
			Owner: "own", Group: "grp", Instance: "ins", Agent: "claude-code", Role: "r", Host: "h", Note: "n",
			Description: "d", Caps: []string{"c1", "c2"}, Labels: map[string]string{"b": "2", "a": "1"},
		},
		Online:   true,
		LastSeen: time.Date(2026, 11, 1, 12, 0, 0, 0, time.UTC),
	}
	small := endpoint.Presence{Record: store.Record{Owner: "o", Group: "g", Instance: "a"}}
	live := map[string]endpoint.LeaseInfo{"own_grp_ins": {Host: "lh", Version: "1.0"}}
	const want = `[
  {
    "name": "own_grp_ins",
    "identity": "own.grp.ins",
    "owner": "own",
    "group": "grp",
    "instance": "ins",
    "agent": "claude-code",
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
    "lease": {
      "host": "lh",
      "version": "1.0"
    }
  },
  {
    "name": "o_g_a",
    "identity": "o.g.a",
    "owner": "o",
    "group": "g",
    "instance": "a",
    "online": false
  }
]`
	if got := whoJSON([]endpoint.Presence{full, small}, true, live); got != want {
		t.Errorf("who --json --long:\n%s", got)
	}
	if got := whoJSON(nil, false, nil); got != "[]" {
		t.Errorf("empty who --json = %q", got)
	}
}
