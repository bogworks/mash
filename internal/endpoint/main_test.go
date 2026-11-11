package endpoint

import (
	"os"
	"testing"
)

// The tests open several endpoints in one process, which share a parent and a session id. Without this each would take
// the lease from the one before as an earlier serve of the same session (ADR 0090); a test that wants that sets its own
// identity with setSession.
func TestMain(m *testing.M) {
	OurSession = func() SessionIdent { return SessionIdent{} }
	os.Exit(m.Run())
}
