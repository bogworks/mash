package endpoint

import (
	"errors"
	"testing"

	"github.com/nats-io/nats.go"
)

// A revoked key's live connection is dropped with "authentication revoked", and a reconnect is refused with
// "Authorization Violation": both are authorization failures a session records for the CLI.
func TestIsAuthFailure(t *testing.T) {
	for _, err := range []error{
		nats.ErrAuthorization, nats.ErrAuthExpired, nats.ErrAuthRevoked, nats.ErrAccountAuthExpired,
		errors.New("nats: Authorization Violation"), errors.New("nats: authentication revoked"),
		errors.New("nats: user authentication expired"),
	} {
		if !isAuthFailure(err) {
			t.Errorf("%v: not an authorization failure", err)
		}
	}
	for _, err := range []error{errors.New("nats: no servers available for connection"), errors.New("context deadline exceeded"), nats.ErrTimeout} {
		if isAuthFailure(err) {
			t.Errorf("%v: taken for an authorization failure", err)
		}
	}
}
