//go:build !unix

package main

import "errors"

// epLock cannot lock on this platform.
func epLock(path string) (unlock func(), err error) {
	return nil, errors.New("reissue locking needs a unix system")
}
