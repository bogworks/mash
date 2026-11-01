//go:build !linux && !darwin

package endpoint

import "errors"

// machineID is unknown on this OS, so a lease is never taken over at once.
func machineID() (string, error) {
	return "", errors.New("machine identity unknown on this OS")
}
