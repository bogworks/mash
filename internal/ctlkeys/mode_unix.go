//go:build unix

package ctlkeys

import "os"

// tooOpen reports whether group or other can touch a file or directory with this mode.
func tooOpen(m os.FileMode) bool { return m.Perm()&0o077 != 0 }
