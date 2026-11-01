//go:build !unix

package config

// CheckCredsMode is a no-op where file modes carry no owner/group/other bits.
func CheckCredsMode(string) error { return nil }
