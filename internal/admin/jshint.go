package admin

import (
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/nats-io/nats.go/jetstream"
)

// IsJetStreamNotEnabled reports whether err is the server's "jetstream not enabled" answer (API error code 10076) or
// its per-account form (10039). It uses the typed errors, never the message text.
func IsJetStreamNotEnabled(err error) bool {
	return errors.Is(err, jetstream.ErrJetStreamNotEnabled) || errors.Is(err, jetstream.ErrJetStreamNotEnabledForAccount)
}

type jsNotEnabledError struct {
	msg string
	err error
}

func (e *jsNotEnabledError) Error() string { return e.msg + " [server said: " + e.err.Error() + "]" }
func (e *jsNotEnabledError) Unwrap() error { return e.err }

// JetStreamNotEnabledHint wraps err with the likely cause when it is the JetStream-not-enabled error, and returns
// every other error (and nil) untouched. nats-server gives JetStream 75% of the free disk by default and silently
// leaves an account without JetStream when its JWT disk limit does not fit. limit is the account's JWT disk limit in
// bytes, or 0 when it is not known (the number is then left out). The original error stays reachable with errors.Is
// and errors.As.
func JetStreamNotEnabledHint(err error, limit int64) error {
	if err == nil || !IsJetStreamNotEnabled(err) {
		return err
	}
	var lim, free string
	if limit > 0 {
		lim = " (" + gib(float64(limit)/(1<<30)) + ")"
		free = "about " + gib(float64(limit)/(1<<30)/0.75)
	} else {
		free = "about a third more than that limit"
	}
	return &jsNotEnabledError{
		msg: fmt.Sprintf("the server could not enable JetStream for the account: it could not reserve the account's disk limit%s. nats-server gives JetStream 75%% of the free disk by default, so the free disk under the server's store directory is probably under %s; free some disk, or lower the account's limit (for example nsc edit account NAME --js-disk-storage 1G, then push)", lim, free),
		err: err,
	}
}

// gib is g gibibytes as text with at most one decimal: 2 GiB, 2.7 GiB.
func gib(g float64) string {
	s := strconv.FormatFloat(g, 'f', 1, 64)
	return strings.TrimSuffix(s, ".0") + " GiB"
}
