package main

import (
	"github.com/bogworks/mash/internal/endpoint"
	"github.com/bogworks/mash/internal/sapsdk/discovery"
)

// enableVerifiedDiscovery turns on the S5 checks of discovery (REQ-016, TO-12) for this process: presence and the
// recipient discovery of prompt accept an answer only when discovery.Verify accepts it, and count the ones that fail.
// main calls it once, first, so every command and `mash serve` has it. Tests that call run() in process do not go
// through main and keep main's own discovery unless they set the switch themselves.
func enableVerifiedDiscovery() {
	endpoint.EnableVerifiedDiscovery(func(subject string, metadata map[string]string, hasRecord bool, user, owner, account string) (string, bool) {
		return discovery.Verify(discovery.Registration{Subject: subject, Metadata: metadata},
			discovery.Directory{HasRecord: hasRecord, User: user, Owner: owner, Account: account})
	})
}
