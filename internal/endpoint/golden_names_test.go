// Characterisation tests (plan 2b, Phase 0): these pin today's identifiers and validity for legacy identities; a change here is a change to a legacy identifier and needs its own decision.

package endpoint

import (
	"path/filepath"
	"testing"
)

func TestGoldenRelayCountersPath(t *testing.T) {
	if RelayCountersFile != "relay-counters.json" {
		t.Errorf("RelayCountersFile = %q", RelayCountersFile)
	}
	if got, want := relayCountersPath("/s", "o", "a"), filepath.FromSlash("/s/spool/o/a/relay-counters.json"); got != want {
		t.Errorf("relayCountersPath = %q, want %q", got, want)
	}
	if relayCountersPath("", "o", "a") != "" || relayCountersPath("/s", "", "a") != "" || relayCountersPath("/s", "o", "") != "" {
		t.Error("relayCountersPath is not empty for a missing part")
	}
}
