//go:build !unix

package ctlkeys

// syncDir is a no-op where directories cannot be fsynced.
func syncDir(string) error { return nil }
