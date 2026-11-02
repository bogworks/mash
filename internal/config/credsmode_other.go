//go:build !unix

package config

import "os"

// CheckCredsMode is a no-op where file modes carry no owner/group/other bits.
func CheckCredsMode(string) error { return nil }

// ReadCredsFile is os.ReadFile where file modes carry no owner/group/other bits.
func ReadCredsFile(path string) ([]byte, error) { return os.ReadFile(path) }
