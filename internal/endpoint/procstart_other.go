//go:build !linux && !darwin

package endpoint

import "errors"

// procStart is unknown on this OS, so a holder is never judged a reused pid.
func procStart(pid int) (string, error) {
	return "", errors.New("process start time unknown on this OS")
}
