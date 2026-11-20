package endpoint

import (
	"errors"
	"strings"

	"github.com/nats-io/nats.go"
)

// SetConnectionHook sets f to be called with an authorization failure of this connection (the credentials were
// revoked or reissued), and with nil each time it connects again.
func (e *Endpoint) SetConnectionHook(f func(error)) {
	if e.connHook != nil {
		e.connHook.Store(&f)
	}
}

// IsAuthFailure reports whether err is the server refusing, expiring or revoking the endpoint's credentials.
func IsAuthFailure(err error) bool { return isAuthFailure(err) }

func isAuthFailure(err error) bool {
	if errors.Is(err, nats.ErrAuthorization) || errors.Is(err, nats.ErrAuthExpired) || errors.Is(err, nats.ErrAuthRevoked) ||
		errors.Is(err, nats.ErrAccountAuthExpired) {
		return true
	}
	t := strings.ToLower(err.Error())
	return strings.Contains(t, "authorization violation") || strings.Contains(t, "authentication revoked") ||
		strings.Contains(t, "authentication expired")
}
